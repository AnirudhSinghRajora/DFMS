package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
)

// fakeServer is a minimal in-memory DFMS API sufficient to exercise the file
// commands end to end (auth + upload + list + get + download + delete).
type fakeServer struct {
	mu    sync.Mutex
	files map[string]storedFile
	next  int
}

type storedFile struct {
	name string
	data []byte
}

func newFakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := &fakeServer{files: map[string]storedFile{}}
	access := mintJWT(t, "me@example.com", time.Now().Add(time.Hour))

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tokens": map[string]string{"access_token": access, "refresh_token": "r"},
		})
	})
	mux.HandleFunc("POST /api/v1/files/upload", s.requireAuth(s.upload))
	mux.HandleFunc("GET /api/v1/files", s.requireAuth(s.list))
	mux.HandleFunc("GET /api/v1/files/{id}/download", s.requireAuth(s.download))
	mux.HandleFunc("GET /api/v1/files/{id}", s.requireAuth(s.get))
	mux.HandleFunc("DELETE /api/v1/files/{id}", s.requireAuth(s.delete))

	return httptest.NewServer(mux)
}

func (s *fakeServer) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			writeAPIError(w, http.StatusUnauthorized, "AUTH_TOKEN_MISSING", "missing token")
			return
		}
		h(w, r)
	}
}

func (s *fakeServer) upload(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "file required")
		return
	}
	defer file.Close()
	data, _ := io.ReadAll(file)

	s.mu.Lock()
	s.next++
	id := "file-" + strconv.Itoa(s.next)
	s.files[id] = storedFile{name: header.Filename, data: data}
	s.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"file": map[string]any{
			"id": id, "name": header.Filename, "size": len(data),
			"checksum": "sum", "version": 1,
			"chunk_count": 1, "new_chunks": 1, "dedup_chunks": 0,
		},
	})
}

func (s *fakeServer) list(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files := make([]map[string]any, 0, len(s.files))
	for id, f := range s.files {
		files = append(files, map[string]any{
			"id": id, "name": f.name, "size": len(f.data),
			"version": 1, "status": "active",
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"files": files, "total": len(files), "page": 1, "page_size": 20, "total_pages": 1,
	})
}

func (s *fakeServer) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[r.PathValue("id")]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "FILE_NOT_FOUND", "not found")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"file": map[string]any{
			"id": r.PathValue("id"), "name": f.name, "size": len(f.data),
			"version": 1, "status": "active",
		},
	})
}

func (s *fakeServer) download(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	f, ok := s.files[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		writeAPIError(w, http.StatusNotFound, "FILE_NOT_FOUND", "not found")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+f.name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(f.data)
}

func (s *fakeServer) delete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	if _, ok := s.files[id]; !ok {
		writeAPIError(w, http.StatusNotFound, "FILE_NOT_FOUND", "not found")
		return
	}
	delete(s.files, id)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "File deleted successfully"})
}

func TestFilesRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	srv := newFakeServer(t)
	defer srv.Close()

	mustRun(t, "context", "add", "test", "--url", srv.URL)
	mustRun(t, "auth", "login", "--email", "me@example.com")

	// Upload a file with known contents.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "report.bin")
	content := []byte("DFMS round-trip payload \x00\x01\x02 with binary bytes")
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, "files", "upload", srcPath, "-o", "json")
	var uploaded dfmsclient.UploadResult
	if err := json.Unmarshal([]byte(out), &uploaded); err != nil {
		t.Fatalf("parse upload json: %v\n%s", err, out)
	}
	if uploaded.Name != "report.bin" {
		t.Errorf("uploaded name = %q", uploaded.Name)
	}

	// It appears in the listing.
	out = mustRun(t, "files", "list", "-o", "json")
	var list dfmsclient.FileList
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("parse list json: %v\n%s", err, out)
	}
	if len(list.Files) != 1 || list.Files[0].ID != uploaded.ID {
		t.Fatalf("unexpected list: %+v", list.Files)
	}

	// Download to an explicit path and verify byte-for-byte.
	dstPath := filepath.Join(t.TempDir(), "downloaded.bin")
	mustRun(t, "files", "download", uploaded.ID, "-O", dstPath)
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read downloaded: %v", err)
	}
	if string(got) != string(content) {
		t.Error("downloaded content does not match the upload")
	}

	// Delete it; afterwards get returns not found mapped to the exit code.
	mustRun(t, "files", "delete", uploaded.ID, "--yes")
	_, err = runDfmsctl(t, "files", "get", uploaded.ID)
	if err == nil {
		t.Fatal("expected an error getting a deleted file")
	}
	if code := ExitCode(err); code != exitNotFound {
		t.Errorf("ExitCode = %d, want %d (not found)", code, exitNotFound)
	}
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runDfmsctl(t, args...)
	if err != nil {
		t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
	return out
}

func writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}
