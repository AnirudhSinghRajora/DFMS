// Package dfmsclient is a typed HTTP client for the DFMS API.
//
// It is consumed by the dfmsctl command tree (internal/cli) but deliberately
// has no dependency on the command framework, so it can be unit-tested in
// isolation and reused by other automation. Endpoint methods are added in later
// phases; this file establishes the client core and its construction options.
package dfmsclient

import (
	"net/http"
	"strings"
	"time"
)

// defaultTimeout bounds ordinary request/response round-trips. Streaming
// uploads and downloads override this with their own (longer or unbounded)
// timeouts once those endpoints are implemented.
const defaultTimeout = 30 * time.Second

// Client talks to a single DFMS API server identified by its base URL.
//
// A Client is safe for concurrent use by multiple goroutines as long as its
// configuration is not mutated after construction.
type Client struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string
}

// Option customizes a Client during construction. Options follow the functional
// options pattern so the constructor stays backward-compatible as settings grow.
type Option func(*Client)

// WithHTTPClient sets the underlying *http.Client. This is the seam used to
// install the authentication round-tripper (Phase 2) and to inject a test
// client in unit tests.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithUserAgent overrides the User-Agent header sent with every request.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// New returns a Client targeting baseURL (for example "https://dfms.example.com").
// Any trailing slash is trimmed so that endpoint paths join predictably.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: "dfmsctl",
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BaseURL returns the server base URL the client targets, without a trailing
// slash.
func (c *Client) BaseURL() string {
	return c.baseURL
}
