package dfmsclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

func TestListVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != versionsPath("f1") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"file_name": "report.pdf",
			"versions": []map[string]any{
				{"id": "v2", "name": "report.pdf", "version": 2, "size": 200, "status": "active"},
				{"id": "v1", "name": "report.pdf", "version": 1, "size": 100, "status": "superseded"},
			},
			"total": 2,
		})
	}))
	defer srv.Close()

	vl, err := New(srv.URL).ListVersions(context.Background(), "f1")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if vl.FileName != "report.pdf" || vl.Total != 2 || len(vl.Versions) != 2 {
		t.Errorf("unexpected versions: %+v", vl)
	}
}

func TestCreateFolder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "docs" {
			t.Errorf("name = %v", body["name"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "d1", "name": "docs", "is_directory": true, "status": "active",
		})
	}))
	defer srv.Close()

	folder, err := New(srv.URL).CreateFolder(context.Background(), "docs", nil)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if folder.ID != "d1" || !folder.IsDirectory {
		t.Errorf("unexpected folder: %+v", folder)
	}
}

func TestFolderContents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != folderContentsPath("d1") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"contents":    []map[string]any{{"id": "f1", "name": "a.txt", "size": 5}},
			"total":       1,
			"page":        1,
			"page_size":   50,
			"total_pages": 1,
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL).FolderContents(context.Background(), "d1", 1, 50)
	if err != nil {
		t.Fatalf("FolderContents: %v", err)
	}
	if len(c.Contents) != 1 || c.Total != 1 {
		t.Errorf("unexpected contents: %+v", c)
	}
}

func TestMoveFile_ToRoot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != moveFilePath("f1") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["new_parent_id"] != nil {
			t.Errorf("new_parent_id = %v, want null for root", body["new_parent_id"])
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "moved"})
	}))
	defer srv.Close()

	if err := New(srv.URL).MoveFile(context.Background(), "f1", nil); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
}

func TestDeleteFolder_RequiresConfirm(t *testing.T) {
	var sawConfirm bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawConfirm = r.URL.Query().Get("confirm") == "true"
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "deleted"})
	}))
	defer srv.Close()

	if err := New(srv.URL).DeleteFolder(context.Background(), "d1"); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if !sawConfirm {
		t.Error("DeleteFolder must send confirm=true")
	}
}

func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "report" {
			t.Errorf("q = %q", got)
		}
		if got := r.URL.Query().Get("type"); got != "application/pdf" {
			t.Errorf("type = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results":     []map[string]any{{"id": "f1", "name": "report.pdf", "size": 100}},
			"total":       1,
			"page":        1,
			"page_size":   20,
			"total_pages": 1,
		})
	}))
	defer srv.Close()

	res, err := New(srv.URL).Search(context.Background(), &SearchOptions{Query: "report", MimeType: "application/pdf"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 1 || res.Total != 1 {
		t.Errorf("unexpected results: %+v", res)
	}
}

func TestStorageUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"used": 1024, "quota": 4096, "available": 3072, "used_pct": 25.0,
		})
	}))
	defer srv.Close()

	u, err := New(srv.URL).StorageUsage(context.Background())
	if err != nil {
		t.Fatalf("StorageUsage: %v", err)
	}
	if u.Used != 1024 || u.Quota != 4096 || u.Available != 3072 || u.UsedPct != 25.0 {
		t.Errorf("unexpected usage: %+v", u)
	}
}

func TestListNodes_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": apierrors.CodeAuthForbidden, "message": "admin only"},
		})
	}))
	defer srv.Close()

	_, err := New(srv.URL).ListNodes(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Code != apierrors.CodeAuthForbidden {
		t.Errorf("Code = %q, want %q", apiErr.Code, apierrors.CodeAuthForbidden)
	}
}
