package dfmsclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"
)

// DefaultPartSize is the size of each part in a multipart (chunked) upload.
const DefaultPartSize = 16 << 20 // 16 MiB

// abortTimeout bounds a best-effort multipart abort.
const abortTimeout = 15 * time.Second

// Multipart upload endpoints.
const pathMultipartInit = apiPrefix + "/files/upload/multipart/init"

func multipartPartPath(uploadID string, partNum int) string {
	return apiPrefix + "/files/upload/multipart/" + url.PathEscape(uploadID) + "/part/" + strconv.Itoa(partNum)
}

func multipartCompletePath(uploadID string) string {
	return apiPrefix + "/files/upload/multipart/" + url.PathEscape(uploadID) + "/complete"
}

func multipartAbortPath(uploadID string) string {
	return apiPrefix + "/files/upload/multipart/" + url.PathEscape(uploadID)
}

// UploadInParts uploads r as a large file using the server's multipart protocol
// (init → upload parts → complete). r is read in partSize-byte chunks, so memory
// use stays bounded regardless of file size. If any step fails, the in-progress
// upload is aborted so the server does not retain orphaned parts.
func (c *Client) UploadInParts(ctx context.Context, name string, r io.Reader, partSize int64) (*UploadResult, error) {
	if partSize <= 0 {
		partSize = DefaultPartSize
	}

	uploadID, err := c.initMultipart(ctx, name, contentTypeForName(name))
	if err != nil {
		return nil, err
	}

	if upErr := c.uploadParts(ctx, uploadID, r, partSize); upErr != nil {
		_ = c.abortMultipart(ctx, uploadID)
		return nil, upErr
	}

	result, err := c.completeMultipart(ctx, uploadID)
	if err != nil {
		_ = c.abortMultipart(ctx, uploadID)
		return nil, err
	}
	return result, nil
}

func (c *Client) uploadParts(ctx context.Context, uploadID string, r io.Reader, partSize int64) error {
	buf := make([]byte, partSize)
	for partNum := 1; ; partNum++ {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			if err := c.uploadPart(ctx, uploadID, partNum, buf[:n]); err != nil {
				return err
			}
		}
		switch {
		case readErr == nil:
			continue // a full part; more data remains
		case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
			return nil // EOF (no more data) or the final short part (already sent)
		default:
			return fmt.Errorf("reading upload data: %w", readErr)
		}
	}
}

func (c *Client) initMultipart(ctx context.Context, name, mimeType string) (string, error) {
	payload := map[string]string{"file_name": filepath.Base(name), "mime_type": mimeType}
	req, err := c.newJSONRequest(ctx, http.MethodPost, pathMultipartInit, payload)
	if err != nil {
		return "", err
	}
	var out struct {
		UploadID string `json:"upload_id"`
	}
	if err := c.doJSON(req, &out); err != nil {
		return "", err
	}
	if out.UploadID == "" {
		return "", errors.New("server did not return an upload id")
	}
	return out.UploadID, nil
}

func (c *Client) uploadPart(ctx context.Context, uploadID string, partNum int, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.baseURL+multipartPartPath(uploadID, partNum), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("building part request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	return c.doJSON(req, nil)
}

func (c *Client) completeMultipart(ctx context.Context, uploadID string) (*UploadResult, error) {
	req, err := c.newJSONRequest(ctx, http.MethodPost, multipartCompletePath(uploadID), nil)
	if err != nil {
		return nil, err
	}
	var result UploadResult
	if err := c.doJSON(req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// abortMultipart cancels an upload. It runs on a context detached from the
// caller's cancellation so a Ctrl-C'd or failed upload still cleans up server
// state.
func (c *Client) abortMultipart(parent context.Context, uploadID string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), abortTimeout)
	defer cancel()

	req, err := c.newJSONRequest(ctx, http.MethodDelete, multipartAbortPath(uploadID), nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, nil)
}
