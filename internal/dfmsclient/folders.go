package dfmsclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/AnirudhSinghRajora/DFMS/pkg/models"
)

const pathFolders = apiPrefix + "/folders"

func folderPath(id string) string         { return pathFolders + "/" + url.PathEscape(id) }
func folderContentsPath(id string) string { return folderPath(id) + "/contents" }
func moveFilePath(id string) string       { return filePath(id) + "/move" }

// FolderContents is one page of a folder's children (files and subfolders).
type FolderContents struct {
	Contents   []models.File `json:"contents"`
	Total      int64         `json:"total"`
	Page       int           `json:"page"`
	PageSize   int           `json:"page_size"`
	TotalPages int           `json:"total_pages"`
}

// CreateFolder creates a folder under parentID (nil = root) and returns it.
func (c *Client) CreateFolder(ctx context.Context, name string, parentID *string) (*models.File, error) {
	payload := map[string]any{"name": name, "parent_id": parentID}
	req, err := c.newJSONRequest(ctx, http.MethodPost, pathFolders, payload)
	if err != nil {
		return nil, err
	}
	var folder models.File
	if err := c.doJSON(req, &folder); err != nil {
		return nil, err
	}
	return &folder, nil
}

// FolderContents lists one page of a folder's children.
func (c *Client) FolderContents(ctx context.Context, id string, page, pageSize int) (*FolderContents, error) {
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))

	req, err := c.newJSONRequest(ctx, http.MethodGet, folderContentsPath(id)+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out FolderContents
	if err := c.doJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MoveFile moves a file (or folder) under newParentID (nil = root).
func (c *Client) MoveFile(ctx context.Context, id string, newParentID *string) error {
	payload := map[string]any{"new_parent_id": newParentID}
	req, err := c.newJSONRequest(ctx, http.MethodPut, moveFilePath(id), payload)
	if err != nil {
		return err
	}
	return c.doJSON(req, nil)
}

// DeleteFolder recursively deletes a folder and its contents. The server
// requires explicit confirmation, which this method always supplies (the CLI
// confirms with the user first).
func (c *Client) DeleteFolder(ctx context.Context, id string) error {
	req, err := c.newJSONRequest(ctx, http.MethodDelete, folderPath(id)+"?confirm=true", nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, nil)
}
