package dfmsclient

import (
	"net/http"
	"testing"
	"time"
)

func TestNew_Defaults(t *testing.T) {
	c := New("https://dfms.example.com")

	if got := c.BaseURL(); got != "https://dfms.example.com" {
		t.Errorf("BaseURL() = %q, want %q", got, "https://dfms.example.com")
	}
	if c.userAgent != "dfmsctl" {
		t.Errorf("userAgent = %q, want %q", c.userAgent, "dfmsctl")
	}
	if c.httpClient == nil {
		t.Fatal("httpClient is nil; want a default client")
	}
	if c.httpClient.Timeout != defaultTimeout {
		t.Errorf("default timeout = %v, want %v", c.httpClient.Timeout, defaultTimeout)
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c := New("https://dfms.example.com/")
	if got := c.BaseURL(); got != "https://dfms.example.com" {
		t.Errorf("BaseURL() = %q, want the trailing slash trimmed", got)
	}
}

func TestNew_AppliesOptions(t *testing.T) {
	custom := &http.Client{Timeout: 5 * time.Second}
	c := New("https://x", WithHTTPClient(custom), WithUserAgent("test-agent"))

	if c.httpClient != custom {
		t.Error("WithHTTPClient did not install the custom client")
	}
	if c.userAgent != "test-agent" {
		t.Errorf("userAgent = %q, want %q", c.userAgent, "test-agent")
	}
}

func TestNew_OptionsIgnoreZeroValues(t *testing.T) {
	c := New("https://x", WithHTTPClient(nil), WithUserAgent(""))

	if c.httpClient == nil {
		t.Error("WithHTTPClient(nil) should be ignored, leaving the default client")
	}
	if c.userAgent != "dfmsctl" {
		t.Errorf("WithUserAgent(\"\") should be ignored; got %q", c.userAgent)
	}
}
