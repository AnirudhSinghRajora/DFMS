// Phase 6 handlers: versioning, multipart upload, folders, search, and resumable downloads.
package main

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	pb "github.com/AnirudhSinghRajora/DFMS/api/proto/chunkpb"
	"github.com/AnirudhSinghRajora/DFMS/internal/metadata"
	"github.com/AnirudhSinghRajora/DFMS/internal/observability"
	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

// ── Versioning Handlers ──────────────────────────────────────

func listVersionsHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		fileID := c.Param("id")

		file, err := fileSvc.GetFile(c.Request.Context(), userID, fileID)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		if file == nil {
			apiErr := apierrors.NewNotFound(apierrors.CodeFileNotFound, "File not found")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		versions, err := fileSvc.ListVersions(c.Request.Context(), userID, file.Name, file.ParentID)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"file_name": file.Name,
			"versions":  versions,
			"total":     len(versions),
		})
	}
}

func downloadVersionHandler(fileSvc *metadata.FileService, chunkClient pb.ChunkServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		fileID := c.Param("id")
		versionStr := c.Param("version")

		version, err := strconv.Atoi(versionStr)
		if err != nil {
			apiErr := apierrors.NewBadRequest("Invalid version number")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		result, err := fileSvc.DownloadVersion(c.Request.Context(), userID, fileID, version)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		if result == nil {
			apiErr := apierrors.NewNotFound(apierrors.CodeFileNotFound, "Version not found")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Stream chunks to response
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", result.FileName))
		c.Header("Content-Type", result.MimeType)
		c.Header("Content-Length", strconv.FormatInt(result.Size, 10))
		c.Header("Accept-Ranges", "bytes")

		streamChunksToResponse(c, chunkClient, result.ChunkHashes)
	}
}

func deleteVersionHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		fileID := c.Param("id")
		versionStr := c.Param("version")

		version, err := strconv.Atoi(versionStr)
		if err != nil {
			apiErr := apierrors.NewBadRequest("Invalid version number")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		if err := fileSvc.DeleteVersion(c.Request.Context(), userID, fileID, version); err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Version %d deleted", version)})
	}
}

// ── Multipart Upload Handlers ────────────────────────────────

func multipartInitHandler(svc *metadata.MultipartService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		var req struct {
			FileName string `json:"file_name" binding:"required"`
			MimeType string `json:"mime_type"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			apiErr := apierrors.NewBadRequest("Invalid input: " + err.Error())
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		uploadID, err := svc.InitUpload(c.Request.Context(), userID, req.FileName, req.MimeType)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"upload_id": uploadID,
			"message":   "Multipart upload initialized. Upload parts using PUT.",
		})
	}
}

func multipartUploadPartHandler(svc *metadata.MultipartService) gin.HandlerFunc {
	return func(c *gin.Context) {
		uploadID := c.Param("uploadId")
		partNumStr := c.Param("partNum")

		partNum, err := strconv.Atoi(partNumStr)
		if err != nil {
			apiErr := apierrors.NewBadRequest("Invalid part number")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		partInfo, err := svc.UploadPart(c.Request.Context(), uploadID, partNum, c.Request.Body)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"part_num": partInfo.PartNum,
			"size":     partInfo.Size,
		})
	}
}

func multipartCompleteHandler(svc *metadata.MultipartService) gin.HandlerFunc {
	return func(c *gin.Context) {
		uploadID := c.Param("uploadId")

		observability.ActiveUploads.Inc()
		start := time.Now()
		resp, err := svc.CompleteUpload(c.Request.Context(), uploadID)
		observability.UploadDuration.Observe(time.Since(start).Seconds())
		observability.ActiveUploads.Dec()

		if err != nil {
			observability.UploadsTotal.WithLabelValues("error").Inc()
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		observability.UploadsTotal.WithLabelValues("success").Inc()
		c.JSON(http.StatusOK, resp)
	}
}

func multipartAbortHandler(svc *metadata.MultipartService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		uploadID := c.Param("uploadId")

		if err := svc.AbortUpload(c.Request.Context(), uploadID, userID); err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Multipart upload aborted"})
	}
}

// ── Folder Handlers ──────────────────────────────────────────

func createFolderHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		var req struct {
			Name     string  `json:"name" binding:"required"`
			ParentID *string `json:"parent_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			apiErr := apierrors.NewBadRequest("Invalid input: " + err.Error())
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		folder, err := fileSvc.CreateFolder(c.Request.Context(), userID, req.Name, req.ParentID)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusCreated, folder)
	}
}

func folderContentsHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		folderID := c.Param("id")

		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))

		contents, total, err := fileSvc.ListFolderContents(c.Request.Context(), userID, &folderID, page, pageSize)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"contents":    contents,
			"total":       total,
			"page":        page,
			"page_size":   pageSize,
			"total_pages": int(math.Ceil(float64(total) / float64(pageSize))),
		})
	}
}

func moveFileHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		fileID := c.Param("id")
		var req struct {
			NewParentID *string `json:"new_parent_id"` // nil = move to root
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			apiErr := apierrors.NewBadRequest("Invalid input: " + err.Error())
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		if err := fileSvc.MoveFile(c.Request.Context(), userID, fileID, req.NewParentID); err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "File moved successfully"})
	}
}

func deleteFolderHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		folderID := c.Param("id")

		confirm := c.Query("confirm")
		if confirm != "true" {
			apiErr := apierrors.NewBadRequest("Folder delete requires ?confirm=true query param")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		if err := fileSvc.DeleteFolder(c.Request.Context(), userID, folderID); err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Folder deleted recursively"})
	}
}

// ── Search Handler ───────────────────────────────────────────

func searchHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")

		q := metadata.SearchQuery{
			Query:    c.Query("q"),
			MimeType: c.Query("type"),
		}

		if v := c.Query("min_size"); v != "" {
			size, _ := strconv.ParseInt(v, 10, 64)
			q.MinSize = &size
		}
		if v := c.Query("max_size"); v != "" {
			size, _ := strconv.ParseInt(v, 10, 64)
			q.MaxSize = &size
		}
		if v := c.Query("after"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err == nil {
				q.After = &t
			}
		}
		if v := c.Query("before"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err == nil {
				q.Before = &t
			}
		}

		q.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
		q.PageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))

		files, total, err := fileSvc.Search(c.Request.Context(), userID, &q)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"results":     files,
			"total":       total,
			"page":        q.Page,
			"page_size":   q.PageSize,
			"total_pages": int(math.Ceil(float64(total) / float64(q.PageSize))),
		})
	}
}

// ── Helper: Stream chunks to HTTP response ───────────────────

// streamChunksToResponse downloads chunks via gRPC and streams them
// directly to the HTTP response writer using the DownloadFile RPC.
func streamChunksToResponse(c *gin.Context, chunkClient pb.ChunkServiceClient, hashes []string) {
	stream, err := chunkClient.DownloadFile(c.Request.Context(), &pb.DownloadFileRequest{
		ChunkHashes: hashes,
	})
	if err != nil {
		return
	}

	c.Status(http.StatusOK)
	for {
		resp, err := stream.Recv()
		if err != nil {
			break
		}
		if _, writeErr := c.Writer.Write(resp.Data); writeErr != nil {
			break
		}
	}
	c.Writer.Flush()
}

// Ensure unused import warnings don't fire.
var _ = observability.ActiveUploads
var _ = zap.String

