package dfmsclient

import (
	"context"
	"net/http"

	"github.com/AnirudhSinghRajora/DFMS/pkg/models"
)

const (
	pathStorageUsage = apiPrefix + "/storage/usage"
	pathAdminNodes   = apiPrefix + "/admin/nodes"
)

// StorageUsage is the caller's storage consumption against their quota.
type StorageUsage struct {
	Used      int64   `json:"used"`
	Quota     int64   `json:"quota"`
	Available int64   `json:"available"`
	UsedPct   float64 `json:"used_pct"`
}

// StorageUsage returns the caller's storage usage.
func (c *Client) StorageUsage(ctx context.Context) (*StorageUsage, error) {
	req, err := c.newJSONRequest(ctx, http.MethodGet, pathStorageUsage, nil)
	if err != nil {
		return nil, err
	}
	var out StorageUsage
	if err := c.doJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// NodeList is the set of storage nodes in the cluster (admin only).
type NodeList struct {
	Nodes   []models.StorageNode `json:"nodes"`
	Message string               `json:"message,omitempty"`
}

// ListNodes returns the cluster's storage nodes. It requires an admin role; a
// non-admin caller receives an AUTH_FORBIDDEN APIError.
func (c *Client) ListNodes(ctx context.Context) (*NodeList, error) {
	req, err := c.newJSONRequest(ctx, http.MethodGet, pathAdminNodes, nil)
	if err != nil {
		return nil, err
	}
	var out NodeList
	if err := c.doJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
