package dfmsclient

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/AnirudhSinghRajora/DFMS/pkg/models"
)

// File endpoints.
const (
	pathUpload = apiPrefix + "/files/upload"
	pathFiles  = apiPrefix + "/files"
)

func filePath(id string) string     { return pathFiles + "/" + url.PathEscape(id) }
func downloadPath(id string) string { return filePath(id) + "/download" }

// UploadResult is the metadata returned after an upload, including deduplication
// statistics (how many chunks were new versus already present).
type UploadResult struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	MimeType    string    `json:"mime_type,omitempty"`
	Checksum    string    `json:"checksum"`
	Version     int       `json:"version"`
	ChunkCount  int       `json:"chunk_count"`
	NewChunks   int       `json:"new_chunks"`
	DedupChunks int       `json:"dedup_chunks"`
	CreatedAt   time.Time `json:"created_at"`
}

// FileList is a paginated listing of files.
type FileList struct {
	Files      []models.File `json:"files"`
	Total      int64         `json:"total"`
	Page       int           `json:"page"`
	PageSize   int           `json:"page_size"`
	TotalPages int           `json:"total_pages"`
}

// DownloadInfo describes a downloaded file's server-provided metadata.
type DownloadInfo struct {
	Filename    string
	Size        int64
	ContentType string
}

// quoteEscaper escapes the characters that are special inside a quoted
// MIME header parameter, matching mime/multipart's own escaping.
var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// UploadFile streams r to the server as a multipart upload under the given name.
// The body is produced through an io.Pipe so the file is never buffered whole in
// memory, regardless of size.
func (c *Client) UploadFile(ctx context.Context, name string, r io.Reader) (*UploadResult, error) {
	filename := filepath.Base(name)

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	// Produce the multipart body in the background, streaming from r. Any error
	// is propagated to the reader (and thus to Do) via CloseWithError.
	go func() {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="file"; filename=%q`, quoteEscaper.Replace(filename)))
		header.Set("Content-Type", contentTypeForName(filename))

		part, err := mw.CreatePart(header)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.CloseWithError(mw.Close())
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+pathUpload, pr)
	if err != nil {
		return nil, fmt.Errorf("building upload request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	var env struct {
		File UploadResult `json:"file"`
	}
	if err := c.doJSON(req, &env); err != nil {
		return nil, err
	}
	return &env.File, nil
}

// ListFiles returns one page of the caller's files.
func (c *Client) ListFiles(ctx context.Context, page, pageSize int) (*FileList, error) {
	q := url.Values{}
	q.Set("page", fmt.Sprintf("%d", page))
	q.Set("page_size", fmt.Sprintf("%d", pageSize))

	req, err := c.newJSONRequest(ctx, http.MethodGet, pathFiles+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var list FileList
	if err := c.doJSON(req, &list); err != nil {
		return nil, err
	}
	return &list, nil
}

// GetFile returns metadata for a single file.
func (c *Client) GetFile(ctx context.Context, id string) (*models.File, error) {
	req, err := c.newJSONRequest(ctx, http.MethodGet, filePath(id), nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		File models.File `json:"file"`
	}
	if err := c.doJSON(req, &env); err != nil {
		return nil, err
	}
	return &env.File, nil
}

// DeleteFile deletes a file by ID.
func (c *Client) DeleteFile(ctx context.Context, id string) error {
	req, err := c.newJSONRequest(ctx, http.MethodDelete, filePath(id), nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, nil)
}

// DownloadFile streams the file's content to w, never buffering it whole, and
// returns the server-provided metadata (filename, size, content type).
func (c *Client) DownloadFile(ctx context.Context, id string, w io.Writer) (*DownloadInfo, error) {
	return c.streamDownload(ctx, downloadPath(id), w)
}

// streamDownload performs a streaming GET of a download endpoint, copying the
// body to w. It backs both file and version downloads.
func (c *Client) streamDownload(ctx context.Context, path string, w io.Writer) (*DownloadInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building download request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, classifyError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseError(resp)
	}

	info := &DownloadInfo{
		Filename:    filenameFromDisposition(resp.Header.Get("Content-Disposition")),
		Size:        resp.ContentLength,
		ContentType: resp.Header.Get("Content-Type"),
	}
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return info, fmt.Errorf("streaming download: %w", err)
	}
	if info.Size < 0 {
		info.Size = written // server did not send Content-Length
	}
	return info, nil
}

func contentTypeForName(name string) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func filenameFromDisposition(value string) string {
	if value == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(value); err == nil {
		return params["filename"]
	}
	return ""
}
