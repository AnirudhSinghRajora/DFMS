package dfmsclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/AnirudhSinghRajora/DFMS/pkg/models"
)

const pathSearch = apiPrefix + "/search"

// SearchOptions holds the query and optional filters for a file search. Zero or
// nil fields are omitted from the request.
type SearchOptions struct {
	Query    string
	MimeType string
	MinSize  *int64
	MaxSize  *int64
	After    *time.Time
	Before   *time.Time
	Page     int
	PageSize int
}

// SearchResults is one page of search hits.
type SearchResults struct {
	Results    []models.File `json:"results"`
	Total      int64         `json:"total"`
	Page       int           `json:"page"`
	PageSize   int           `json:"page_size"`
	TotalPages int           `json:"total_pages"`
}

// Search finds files matching opts.
func (c *Client) Search(ctx context.Context, opts *SearchOptions) (*SearchResults, error) {
	q := url.Values{}
	q.Set("q", opts.Query)
	if opts.MimeType != "" {
		q.Set("type", opts.MimeType)
	}
	if opts.MinSize != nil {
		q.Set("min_size", strconv.FormatInt(*opts.MinSize, 10))
	}
	if opts.MaxSize != nil {
		q.Set("max_size", strconv.FormatInt(*opts.MaxSize, 10))
	}
	if opts.After != nil {
		q.Set("after", opts.After.Format(time.RFC3339))
	}
	if opts.Before != nil {
		q.Set("before", opts.Before.Format(time.RFC3339))
	}
	if opts.Page > 0 {
		q.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(opts.PageSize))
	}

	req, err := c.newJSONRequest(ctx, http.MethodGet, pathSearch+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out SearchResults
	if err := c.doJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
