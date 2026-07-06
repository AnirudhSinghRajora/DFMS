package dfmsclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// memTokenStore is an in-memory TokenStore for tests.
type memTokenStore struct {
	tokens  Tokens
	present bool
	saves   int
	deletes int
}

func (m *memTokenStore) Load() (Tokens, error) {
	if !m.present {
		return Tokens{}, ErrNoCredentials
	}
	return m.tokens, nil
}

func (m *memTokenStore) Save(t Tokens) error {
	m.tokens = t
	m.present = true
	m.saves++
	return nil
}

func (m *memTokenStore) Delete() error {
	m.tokens = Tokens{}
	m.present = false
	m.deletes++
	return nil
}

// makeJWT mints a token whose exp claim is set as given. The signature is
// irrelevant because the client decodes claims without verifying them.
func makeJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{"sub": "u1", "email": "a@b.c", "role": "user", "exp": exp.Unix()}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-key"))
	if err != nil {
		t.Fatalf("minting test JWT: %v", err)
	}
	return signed
}

func writeTokens(w http.ResponseWriter, access, refresh string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tokens": map[string]string{"access_token": access, "refresh_token": refresh},
	})
}

func write401(w http.ResponseWriter, code string) {
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": "unauthorized"},
	})
}

func TestAuthTransport_ProactiveRefresh(t *testing.T) {
	const newAccess = "new-access-token"
	var refreshCalls, protectedCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathRefresh:
			refreshCalls++
			writeTokens(w, newAccess, "new-refresh")
		case "/api/v1/files":
			protectedCalls++
			if r.Header.Get("Authorization") != "Bearer "+newAccess {
				write401(w, "AUTH_TOKEN_EXPIRED")
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	store := &memTokenStore{
		tokens:  Tokens{AccessToken: makeJWT(t, time.Now().Add(-time.Minute)), RefreshToken: "old-refresh"},
		present: true,
	}
	hc := NewAuthHTTPClient(srv.URL, store, false)

	resp := doGet(t, hc, srv.URL+"/api/v1/files")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if refreshCalls != 1 {
		t.Errorf("refresh called %d times, want 1 (proactive)", refreshCalls)
	}
	if protectedCalls != 1 {
		t.Errorf("protected endpoint called %d times, want 1", protectedCalls)
	}
	if store.tokens.AccessToken != newAccess {
		t.Errorf("rotated access token not persisted: %q", store.tokens.AccessToken)
	}
}

func TestAuthTransport_ReactiveRefreshRetriesOnce(t *testing.T) {
	const newAccess = "new-access-token"
	var refreshCalls, protectedCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathRefresh:
			refreshCalls++
			writeTokens(w, newAccess, "new-refresh")
		case "/api/v1/files":
			protectedCalls++
			if r.Header.Get("Authorization") != "Bearer "+newAccess {
				write401(w, "AUTH_TOKEN_EXPIRED") // first call: stale token
				return
			}
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	// Access token still looks valid (exp in the future), so refresh is reactive.
	store := &memTokenStore{
		tokens:  Tokens{AccessToken: makeJWT(t, time.Now().Add(time.Hour)), RefreshToken: "old-refresh"},
		present: true,
	}
	hc := NewAuthHTTPClient(srv.URL, store, false)

	resp := doGet(t, hc, srv.URL+"/api/v1/files")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after reactive refresh", resp.StatusCode)
	}
	if refreshCalls != 1 {
		t.Errorf("refresh called %d times, want exactly 1", refreshCalls)
	}
	if protectedCalls != 2 {
		t.Errorf("protected endpoint called %d times, want 2 (original + retry)", protectedCalls)
	}
}

func TestAuthTransport_RefreshFailureClearsTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathRefresh {
			write401(w, "AUTH_TOKEN_INVALID")
			return
		}
		t.Errorf("protected endpoint should not be reached, got %s", r.URL.Path)
	}))
	defer srv.Close()

	store := &memTokenStore{
		tokens:  Tokens{AccessToken: makeJWT(t, time.Now().Add(-time.Minute)), RefreshToken: "dead-refresh"},
		present: true,
	}
	hc := NewAuthHTTPClient(srv.URL, store, false)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v1/files", nil)
	resp, err := hc.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
	if store.present {
		t.Error("tokens should have been cleared after a failed refresh")
	}
	if store.deletes != 1 {
		t.Errorf("Delete called %d times, want 1", store.deletes)
	}
}

func TestAuthTransport_NoCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be reached without credentials")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hc := NewAuthHTTPClient(srv.URL, &memTokenStore{}, false)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v1/files", nil)
	resp, err := hc.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
}

func TestAuthTransport_AuthEndpointsBypassBearer(t *testing.T) {
	var sawAuthHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuthHeader = true
		}
		writeTokens(w, "a", "b")
	}))
	defer srv.Close()

	store := &memTokenStore{tokens: Tokens{AccessToken: makeJWT(t, time.Now().Add(time.Hour)), RefreshToken: "r"}, present: true}
	hc := NewAuthHTTPClient(srv.URL, store, false)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+pathLogin, http.NoBody)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	resp.Body.Close()
	if sawAuthHeader {
		t.Error("auth endpoints must not carry an Authorization header")
	}
}

func doGet(t *testing.T, hc *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}
