package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/AnirudhSinghRajora/DFMS/internal/cliconfig"
	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
)

func loginMux(t *testing.T, email string) (*http.ServeMux, string) {
	t.Helper()
	access := mintJWT(t, email, time.Now().Add(time.Hour))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tokens": map[string]string{"access_token": access, "refresh_token": "r"},
		})
	})
	return mux, access
}

func TestUpload_AutoSelectsMultipart(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	var (
		mu        sync.Mutex
		inited    bool
		completed bool
		assembled []byte
	)
	mux, _ := loginMux(t, "me@example.com")
	mux.HandleFunc("POST /api/v1/files/upload/multipart/init", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		inited = true
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "u1"})
	})
	mux.HandleFunc("PUT /api/v1/files/upload/multipart/{id}/part/{num}", func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		assembled = append(assembled, data...)
		mu.Unlock()
		num, _ := strconv.Atoi(r.PathValue("num"))
		_ = json.NewEncoder(w).Encode(map[string]any{"part_num": num, "size": len(data)})
	})
	mux.HandleFunc("POST /api/v1/files/upload/multipart/{id}/complete", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		completed = true
		size := len(assembled)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "f1", "name": "big.bin", "size": size, "version": 1,
			"chunk_count": 1, "new_chunks": 1, "dedup_chunks": 0,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Seed a config with a small multipart threshold so a modest file triggers it.
	cfg := &cliconfig.Config{
		Contexts: map[string]cliconfig.Context{},
		Defaults: &cliconfig.Defaults{MultipartThreshold: 1024},
	}
	path, err := cliconfig.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "context", "add", "test", "--url", srv.URL)
	mustRun(t, "auth", "login", "--email", "me@example.com")

	src := filepath.Join(t.TempDir(), "big.bin")
	content := bytes.Repeat([]byte("x"), 3000) // > 1024 threshold
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "files", "upload", src)

	mu.Lock()
	defer mu.Unlock()
	if !inited || !completed {
		t.Errorf("expected multipart flow (init=%v complete=%v)", inited, completed)
	}
	if !bytes.Equal(assembled, content) {
		t.Error("multipart-reassembled content does not match the source")
	}
}

func TestStorageUsage_Command(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	mux, _ := loginMux(t, "me@example.com")
	mux.HandleFunc("GET /api/v1/storage/usage", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"used": 2048, "quota": 8192, "available": 6144, "used_pct": 25.0,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mustRun(t, "context", "add", "test", "--url", srv.URL)
	mustRun(t, "auth", "login", "--email", "me@example.com")

	out := mustRun(t, "storage", "usage", "-o", "json")
	var usage dfmsclient.StorageUsage
	if err := json.Unmarshal([]byte(out), &usage); err != nil {
		t.Fatalf("parse usage json: %v\n%s", err, out)
	}
	if usage.Used != 2048 || usage.Quota != 8192 {
		t.Errorf("unexpected usage: %+v", usage)
	}
}

func TestAdminNodes_ForbiddenExitCode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	mux, _ := loginMux(t, "me@example.com")
	mux.HandleFunc("GET /api/v1/admin/nodes", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "AUTH_FORBIDDEN", "message": "admin only"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mustRun(t, "context", "add", "test", "--url", srv.URL)
	mustRun(t, "auth", "login", "--email", "me@example.com")

	_, err := runDfmsctl(t, "admin", "nodes")
	if err == nil {
		t.Fatal("expected a forbidden error")
	}
	if code := ExitCode(err); code != exitAuth {
		t.Errorf("ExitCode = %d, want %d (auth)", code, exitAuth)
	}
}
