package dfmsclient

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/AnirudhSinghRajora/DFMS/pkg/models"
)

func versionsPath(id string) string { return filePath(id) + "/versions" }
func versionPath(id string, version int) string {
	return versionsPath(id) + "/" + strconv.Itoa(version)
}
func versionDownloadPath(id string, version int) string {
	return versionPath(id, version) + "/download"
}

// VersionList is the version history of a file.
type VersionList struct {
	FileName string        `json:"file_name"`
	Versions []models.File `json:"versions"`
	Total    int           `json:"total"`
}

// ListVersions returns the version history for a file.
func (c *Client) ListVersions(ctx context.Context, id string) (*VersionList, error) {
	req, err := c.newJSONRequest(ctx, http.MethodGet, versionsPath(id), nil)
	if err != nil {
		return nil, err
	}
	var out VersionList
	if err := c.doJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadVersion streams a specific version's content to w.
func (c *Client) DownloadVersion(ctx context.Context, id string, version int, w io.Writer) (*DownloadInfo, error) {
	return c.streamDownload(ctx, versionDownloadPath(id, version), w)
}

// DeleteVersion deletes a specific version of a file.
func (c *Client) DeleteVersion(ctx context.Context, id string, version int) error {
	req, err := c.newJSONRequest(ctx, http.MethodDelete, versionPath(id, version), nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, nil)
}
