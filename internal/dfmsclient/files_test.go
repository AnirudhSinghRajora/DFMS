package dfmsclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

func TestUploadFile_StreamsMultipart(t *testing.T) {
	content := make([]byte, 5<<20) // 5 MiB — large enough to prove no full buffering
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(content)

	var (
		streamed bool
		gotName  string
		gotCT    string
		gotSum   [32]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathUpload {
			t.Errorf("path = %s, want %s", r.URL.Path, pathUpload)
		}
		// A streamed (chunked) request has no fixed Content-Length: proof the
		// client did not buffer the whole file to compute one.
		streamed = r.ContentLength < 0

		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader: %v", err)
		}
		part, err := mr.NextPart()
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		gotName = part.FileName()
		gotCT = part.Header.Get("Content-Type")
		data, _ := io.ReadAll(part)
		gotSum = sha256.Sum256(data)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"file": map[string]any{
				"id": "f1", "name": gotName, "size": len(data),
				"checksum": "abc", "version": 1,
				"chunk_count": 5, "new_chunks": 5, "dedup_chunks": 0,
			},
		})
	}))
	defer srv.Close()

	res, err := New(srv.URL).UploadFile(context.Background(), "report.pdf", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if res.ID != "f1" || res.ChunkCount != 5 || res.NewChunks != 5 {
		t.Errorf("unexpected upload result: %+v", res)
	}
	if !streamed {
		t.Error("upload should stream (chunked), but the request had a fixed Content-Length")
	}
	if gotName != "report.pdf" {
		t.Errorf("multipart filename = %q", gotName)
	}
	if gotCT != "application/pdf" {
		t.Errorf("multipart Content-Type = %q, want application/pdf", gotCT)
	}
	if gotSum != want {
		t.Error("uploaded bytes do not match the source")
	}
}

func TestDownloadFile(t *testing.T) {
	content := []byte("the quick brown fox jumps over the lazy dog")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != downloadPath("f1") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", `attachment; filename="hello.txt"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	info, err := New(srv.URL).DownloadFile(context.Background(), "f1", &buf)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), content) {
		t.Error("downloaded bytes do not match")
	}
	if info.Filename != "hello.txt" {
		t.Errorf("Filename = %q, want hello.txt", info.Filename)
	}
	if info.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", info.Size, len(content))
	}
}

func TestGetFile_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": apierrors.CodeFileNotFound, "message": "File not found"},
		})
	}))
	defer srv.Close()

	_, err := New(srv.URL).GetFile(context.Background(), "missing")
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Code != apierrors.CodeFileNotFound || apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("unexpected error: %+v", apiErr)
	}
}

func TestListFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("page_size"); got != "50" {
			t.Errorf("page_size = %q, want 50", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"files": []map[string]any{
				{"id": "a", "name": "one.txt", "size": 10, "version": 1, "status": "active"},
				{"id": "b", "name": "two.txt", "size": 20, "version": 2, "status": "active"},
			},
			"total": 2, "page": 1, "page_size": 50, "total_pages": 1,
		})
	}))
	defer srv.Close()

	list, err := New(srv.URL).ListFiles(context.Background(), 1, 50)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(list.Files) != 2 || list.Files[0].ID != "a" || list.Total != 2 {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestDeleteFile(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != filePath("f1") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		called = true
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "File deleted successfully"})
	}))
	defer srv.Close()

	if err := New(srv.URL).DeleteFile(context.Background(), "f1"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if !called {
		t.Error("delete endpoint was not called")
	}
}
