package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// loginServer returns a test server that answers the login endpoint with a token
// pair whose access token is a real (unsigned-verification) JWT for email.
func loginServer(t *testing.T, email string) *httptest.Server {
	t.Helper()
	access := mintJWT(t, email, time.Now().Add(15*time.Minute))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/login" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tokens": map[string]string{"access_token": access, "refresh_token": "refresh-1"},
		})
	}))
}

func mintJWT(t *testing.T, email string, exp time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{"sub": "user-1", "email": email, "role": "user", "exp": exp.Unix()}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestAuthLoginLogoutStatus(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("DFMSCTL_TOKEN_STORE", "file") // hermetic: never touch a real keyring
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	srv := loginServer(t, "me@example.com")
	defer srv.Close()

	if _, err := runDfmsctl(t, "context", "add", "test", "--url", srv.URL); err != nil {
		t.Fatalf("context add: %v", err)
	}

	// Before login, status reports logged out.
	out, err := runDfmsctl(t, "auth", "status", "-o", "json")
	if err != nil {
		t.Fatalf("status (pre-login): %v", err)
	}
	if got := parseStatus(t, out); got.LoggedIn {
		t.Errorf("expected logged out before login, got %+v", got)
	}

	// Log in.
	out, err = runDfmsctl(t, "auth", "login", "--email", "me@example.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out, "Logged in as me@example.com") {
		t.Errorf("unexpected login output: %q", out)
	}

	// Tokens persisted with owner-only permissions.
	tokensPath := filepath.Join(configHome, "dfms", "tokens.json")
	info, err := os.Stat(tokensPath)
	if err != nil {
		t.Fatalf("tokens file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("tokens file perm = %v, want 0600", perm)
	}

	// Status now reflects the session decoded from the stored token.
	out, err = runDfmsctl(t, "auth", "status", "-o", "json")
	if err != nil {
		t.Fatalf("status (post-login): %v", err)
	}
	st := parseStatus(t, out)
	if !st.LoggedIn || st.Email != "me@example.com" || st.Role != "user" {
		t.Errorf("unexpected status: %+v", st)
	}

	// Log out, then status is logged out again.
	if _, err = runDfmsctl(t, "auth", "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	out, err = runDfmsctl(t, "auth", "status", "-o", "json")
	if err != nil {
		t.Fatalf("status (post-logout): %v", err)
	}
	if parseStatus(t, out).LoggedIn {
		t.Error("expected logged out after logout")
	}
}

func TestAuthLogin_NoActiveContext(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	_, err := runDfmsctl(t, "auth", "login", "--email", "me@example.com")
	if err == nil {
		t.Fatal("expected an error when no context is configured")
	}
}

func parseStatus(t *testing.T, out string) statusView {
	t.Helper()
	var v statusView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("parsing status json: %v\n%s", err, out)
	}
	return v
}
