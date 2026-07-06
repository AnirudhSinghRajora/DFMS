package dfmsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

func TestUploadInParts_Success(t *testing.T) {
	content := bytes.Repeat([]byte("abcdefghij"), 350) // 3500 bytes
	const partSize = 1024                              // → 4 parts (1024*3 + 428)

	var (
		mu        sync.Mutex
		parts     = map[int][]byte{}
		assembled []byte
		completed bool
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/files/upload/multipart/init", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "up1"})
	})
	mux.HandleFunc("PUT /api/v1/files/upload/multipart/{id}/part/{num}", func(w http.ResponseWriter, r *http.Request) {
		num, _ := strconv.Atoi(r.PathValue("num"))
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		parts[num] = data
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"part_num": num, "size": len(data)})
	})
	mux.HandleFunc("POST /api/v1/files/upload/multipart/{id}/complete", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		completed = true
		for i := 1; i <= len(parts); i++ {
			assembled = append(assembled, parts[i]...)
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "f1", "name": "big.bin", "size": len(assembled),
			"checksum": "x", "version": 1, "chunk_count": 4, "new_chunks": 4, "dedup_chunks": 0,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	result, err := New(srv.URL).UploadInParts(context.Background(), "big.bin", bytes.NewReader(content), partSize)
	if err != nil {
		t.Fatalf("UploadInParts: %v", err)
	}
	if result.ID != "f1" || result.ChunkCount != 4 {
		t.Errorf("unexpected result: %+v", result)
	}
	if !completed {
		t.Error("complete was not called")
	}
	if len(parts) != 4 {
		t.Errorf("got %d parts, want 4", len(parts))
	}
	if !bytes.Equal(assembled, content) {
		t.Error("reassembled parts do not match the source content")
	}
}

func TestUploadInParts_AbortsOnFailure(t *testing.T) {
	var (
		mu      sync.Mutex
		aborted bool
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/files/upload/multipart/init", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "up1"})
	})
	mux.HandleFunc("PUT /api/v1/files/upload/multipart/{id}/part/{num}", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"part_num": 1, "size": 4})
	})
	mux.HandleFunc("POST /api/v1/files/upload/multipart/{id}/complete", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "INTERNAL_ERROR", "message": "boom"},
		})
	})
	mux.HandleFunc("DELETE /api/v1/files/upload/multipart/{id}", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		aborted = true
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "aborted"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := New(srv.URL).UploadInParts(context.Background(), "x.bin", bytes.NewReader([]byte("data")), 1024)
	if err == nil {
		t.Fatal("expected an error when complete fails")
	}
	mu.Lock()
	defer mu.Unlock()
	if !aborted {
		t.Error("a failed multipart upload must be aborted to avoid orphaned parts")
	}
}
