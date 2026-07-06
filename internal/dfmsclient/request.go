package dfmsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// API path prefix and endpoints. Centralized so the auth transport and the
// endpoint methods agree on exactly which paths are public auth endpoints.
const (
	apiPrefix = "/api/v1"

	pathRegister = apiPrefix + "/auth/register"
	pathLogin    = apiPrefix + "/auth/login"
	pathRefresh  = apiPrefix + "/auth/refresh"
)

// newJSONRequest builds a request with a JSON body (or no body when payload is
// nil) and the standard headers. The body is held in memory so the request is
// safely retryable by the auth transport.
func (c *Client) newJSONRequest(ctx context.Context, method, path string, payload any) (*http.Request, error) {
	var body *bytes.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		body = bytes.NewReader(data)
	} else {
		body = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	return req, nil
}

// doJSON sends req and decodes a 2xx JSON response into out (when non-nil). A
// non-2xx status is converted into an *APIError; transport failures into a
// *ConnectionError.
func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return classifyError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseError(resp)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}
