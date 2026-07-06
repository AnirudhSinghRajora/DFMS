package dfmsclient

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func responseWith(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestParseError_Envelope(t *testing.T) {
	resp := responseWith(http.StatusNotFound,
		`{"error":{"code":"FILE_NOT_FOUND","message":"no such file","request_id":"r1"}}`)
	defer resp.Body.Close()

	err := parseError(resp)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Code != "FILE_NOT_FOUND" || apiErr.Message != "no such file" || apiErr.RequestID != "r1" {
		t.Errorf("unexpected APIError: %+v", apiErr)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Error(), "r1") {
		t.Errorf("Error() should include the request id: %q", apiErr.Error())
	}
}

func TestParseError_NonJSONFallback(t *testing.T) {
	resp := responseWith(http.StatusBadGateway, "upstream is down")
	defer resp.Body.Close()
	apiErr, ok := parseError(resp).(*APIError)
	if !ok {
		t.Fatalf("want *APIError")
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Message, "upstream is down") {
		t.Errorf("Message = %q, want the raw body", apiErr.Message)
	}
}

func TestParseError_EmptyBodyFallback(t *testing.T) {
	resp := responseWith(http.StatusInternalServerError, "")
	defer resp.Body.Close()
	apiErr, ok := parseError(resp).(*APIError)
	if !ok {
		t.Fatalf("want *APIError")
	}
	if !strings.Contains(apiErr.Message, "500") {
		t.Errorf("Message = %q, want a status-based fallback", apiErr.Message)
	}
}
