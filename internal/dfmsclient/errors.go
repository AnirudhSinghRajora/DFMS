package dfmsclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

// ErrNoCredentials indicates that no usable session exists for the target
// server: the user has not logged in, or a refresh failed and the stored tokens
// were cleared. Commands translate this into a "run 'dfmsctl auth login'" hint.
var ErrNoCredentials = errors.New("not authenticated: run 'dfmsctl auth login' first")

// APIError is a structured error returned by the DFMS API. It mirrors the
// server's error envelope (pkg/errors) and carries the request ID for support
// and log correlation.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	var b strings.Builder
	if e.Code != "" {
		fmt.Fprintf(&b, "[%s] ", e.Code)
	}
	if e.Message != "" {
		b.WriteString(e.Message)
	} else {
		fmt.Fprintf(&b, "request failed with status %d", e.StatusCode)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request id: %s)", e.RequestID)
	}
	return b.String()
}

// ConnectionError wraps a transport-level failure (DNS, dial, TLS, timeout) so
// callers can distinguish "could not reach the server" from an error response.
type ConnectionError struct {
	err error
}

// NewConnectionError wraps err as a ConnectionError.
func NewConnectionError(err error) *ConnectionError { return &ConnectionError{err: err} }

func (e *ConnectionError) Error() string { return "connecting to server: " + e.err.Error() }
func (e *ConnectionError) Unwrap() error { return e.err }

// classifyError normalizes an error returned by http.Client.Do into one of the
// package's typed errors. ErrNoCredentials and APIError raised inside the auth
// transport are surfaced as-is (Do wraps RoundTrip errors in *url.Error, which
// errors.As/Is see through); everything else is treated as a connection error.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNoCredentials) {
		return ErrNoCredentials
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var connErr *ConnectionError
	if errors.As(err, &connErr) {
		return connErr
	}
	return &ConnectionError{err}
}

// parseError reads an error response body and converts it into an *APIError. It
// decodes the standard envelope when present and otherwise falls back to the
// raw body, so unexpected error shapes still produce a usable message.
func parseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var env apierrors.ErrorResponse
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Code != "" {
		return &APIError{
			StatusCode: resp.StatusCode,
			Code:       env.Error.Code,
			Message:    env.Error.Message,
			RequestID:  env.Error.RequestID,
		}
	}

	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    fallbackMessage(resp.StatusCode, body),
	}
}

func fallbackMessage(status int, body []byte) string {
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return msg
	}
	return fmt.Sprintf("request failed with status %d (%s)", status, http.StatusText(status))
}
