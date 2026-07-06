package dfmsclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

func TestClient_Login(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathLogin || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["email"] != "a@b.c" || body["password"] != "secret" {
			t.Errorf("unexpected login body: %v", body)
		}
		writeTokens(w, "access-1", "refresh-1")
	}))
	defer srv.Close()

	tokens, err := New(srv.URL).Login(context.Background(), "a@b.c", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" {
		t.Errorf("unexpected tokens: %+v", tokens)
	}
}

func TestClient_Login_InvalidCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"code":       apierrors.CodeAuthInvalidCredentials,
				"message":    "Invalid email or password",
				"request_id": "req-42",
			},
		})
	}))
	defer srv.Close()

	_, err := New(srv.URL).Login(context.Background(), "a@b.c", "wrong")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Code != apierrors.CodeAuthInvalidCredentials {
		t.Errorf("Code = %q", apiErr.Code)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.RequestID != "req-42" {
		t.Errorf("RequestID = %q, want req-42", apiErr.RequestID)
	}
}

func TestClient_Register(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathRegister {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["display_name"] != "Demo" {
			t.Errorf("display_name = %q", body["display_name"])
		}
		w.WriteHeader(http.StatusCreated)
		writeTokens(w, "access-r", "refresh-r")
	}))
	defer srv.Close()

	tokens, err := New(srv.URL).Register(context.Background(), "a@b.c", "password1", "Demo")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tokens.AccessToken != "access-r" {
		t.Errorf("AccessToken = %q", tokens.AccessToken)
	}
}

func TestIdentify(t *testing.T) {
	exp := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	id, err := Identify(makeJWT(t, exp))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if id.Email != "a@b.c" || id.Role != "user" || id.UserID != "u1" {
		t.Errorf("unexpected identity: %+v", id)
	}
	if !id.ExpiresAt.Equal(exp) {
		t.Errorf("ExpiresAt = %v, want %v", id.ExpiresAt, exp)
	}
	if id.Expired() {
		t.Error("token should not be reported expired")
	}
}
