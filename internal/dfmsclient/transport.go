package dfmsclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// expiryLeeway is how far before an access token's expiry the transport
// proactively refreshes it, absorbing clock skew and in-flight request time.
const expiryLeeway = 30 * time.Second

// responseHeaderTimeout bounds how long to wait for a server to begin
// responding. It caps hung connections without limiting the duration of a
// legitimately long upload or download (the body may stream for as long as
// needed, cancelable via the request context).
const responseHeaderTimeout = 30 * time.Second

// NewAuthHTTPClient returns an *http.Client whose transport authenticates every
// non-auth request against baseURL: it attaches the bearer token from store,
// refreshes it transparently when it expires, and persists rotated tokens.
//
// When insecure is true, TLS certificate verification is disabled for this
// client. That is an explicit, per-context opt-in meant only for self-signed
// development servers.
func NewAuthHTTPClient(baseURL string, store TokenStore, insecure bool) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.ResponseHeaderTimeout = responseHeaderTimeout
	if insecure {
		if base.TLSClientConfig == nil {
			base.TLSClientConfig = &tls.Config{} //nolint:gosec // verification toggled below
		}
		base.TLSClientConfig.InsecureSkipVerify = true
	}
	return &http.Client{
		// No overall timeout: uploads/downloads may run arbitrarily long. The
		// time-to-first-byte is bounded by ResponseHeaderTimeout above, and the
		// request context governs cancellation.
		Timeout: 0,
		Transport: &authTransport{
			base:    base,
			baseURL: strings.TrimRight(baseURL, "/"),
			store:   store,
			now:     time.Now,
		},
	}
}

// authTransport is an http.RoundTripper that adds DFMS bearer authentication and
// handles access-token refresh. Public auth endpoints pass through untouched.
type authTransport struct {
	base    http.RoundTripper
	baseURL string
	store   TokenStore
	now     func() time.Time // injectable clock for tests
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Login/register/refresh are unauthenticated; never attach a bearer or try
	// to refresh for them (refresh would otherwise recurse).
	if isAuthEndpoint(req.URL.Path) {
		return t.base.RoundTrip(req)
	}

	tokens, err := t.store.Load()
	if err != nil {
		return nil, err // ErrNoCredentials surfaces to the caller
	}

	access := tokens.AccessToken
	// Proactively refresh an expired (or nearly expired) access token so the
	// request carries a valid bearer on the first attempt.
	if t.isExpired(access) {
		refreshed, rerr := t.refresh(req.Context(), tokens.RefreshToken)
		if rerr != nil {
			return nil, rerr
		}
		access = refreshed.AccessToken
	}

	resp, err := t.send(req, access)
	if err != nil {
		return nil, err
	}

	// Reactive fallback: a 401 despite a seemingly valid token (clock skew,
	// server-side revocation). Refresh once and retry, but only when the body
	// can be replayed.
	if resp.StatusCode == http.StatusUnauthorized && canReplay(req) {
		_ = resp.Body.Close()
		refreshed, rerr := t.refresh(req.Context(), tokens.RefreshToken)
		if rerr != nil {
			return nil, rerr
		}
		return t.send(req, refreshed.AccessToken)
	}

	return resp, nil
}

// send issues the request with the given bearer token. It clones the request so
// the original is never mutated, and replays the body from GetBody when present
// so retries send a fresh copy.
func (t *authTransport) send(req *http.Request, access string) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+access)
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("preparing request body: %w", err)
		}
		r.Body = body
	}
	return t.base.RoundTrip(r)
}

// isExpired reports whether the access token is missing, unreadable, or within
// the leeway of its expiry. An unreadable token is treated as valid so the
// server can make the final call (the reactive path still covers a 401).
func (t *authTransport) isExpired(access string) bool {
	if access == "" {
		return true
	}
	exp, ok := tokenExpiry(access)
	if !ok {
		return false
	}
	return t.now().Add(expiryLeeway).After(exp)
}

// refresh exchanges the refresh token for a new token pair and persists it. It
// talks to the server through the base transport (bypassing this round-tripper)
// to avoid recursion. On a non-2xx response the stored tokens are cleared and
// ErrNoCredentials is returned, signaling the user to log in again.
func (t *authTransport) refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	if refreshToken == "" {
		return Tokens{}, ErrNoCredentials
	}

	payload, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return Tokens{}, fmt.Errorf("encoding refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+pathRefresh, bytes.NewReader(payload))
	if err != nil {
		return Tokens{}, fmt.Errorf("building refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return Tokens{}, &ConnectionError{err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The refresh token is invalid or expired: drop the dead session.
		_ = t.store.Delete()
		return Tokens{}, ErrNoCredentials
	}

	var body authResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Tokens{}, fmt.Errorf("decoding refresh response: %w", err)
	}
	tokens := body.toTokens()
	if err := t.store.Save(tokens); err != nil {
		return Tokens{}, fmt.Errorf("storing refreshed tokens: %w", err)
	}
	return tokens, nil
}

func isAuthEndpoint(path string) bool {
	switch path {
	case pathLogin, pathRegister, pathRefresh:
		return true
	default:
		return false
	}
}

// canReplay reports whether a request's body can be re-sent for a retry. Bodyless
// requests and those with a GetBody (in-memory bodies) are replayable; streaming
// bodies are not.
func canReplay(req *http.Request) bool {
	return req.Body == nil || req.GetBody != nil
}
