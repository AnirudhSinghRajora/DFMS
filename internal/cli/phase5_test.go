package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
	"github.com/AnirudhSinghRajora/DFMS/pkg/models"
)

// ─── writeStructured (renderer selection) ────────────────────────────────────

func TestWriteStructured_JSON_RoundTrip(t *testing.T) {
	in := &dfmsclient.UploadResult{
		ID: "file-42", Name: "report.pdf", Size: 2048, Version: 1,
		Checksum: "abc123", ChunkCount: 3, NewChunks: 2, DedupChunks: 1,
	}
	var buf bytes.Buffer
	handled, err := writeStructured(&buf, outputJSON, in)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected json to be handled")
	}

	var got dfmsclient.UploadResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal json output: %v\n%s", err, buf.String())
	}
	if got.ID != in.ID || got.Name != in.Name || got.Size != in.Size ||
		got.ChunkCount != in.ChunkCount || got.NewChunks != in.NewChunks {
		t.Errorf("round-trip mismatch:\n  got  %+v\n  want %+v", got, *in)
	}
}

func TestWriteStructured_YAML_RoundTrip(t *testing.T) {
	in := &dfmsclient.UploadResult{
		ID: "file-99", Name: "data.csv", Size: 4096, Version: 2,
		Checksum: "def456", ChunkCount: 5, NewChunks: 3, DedupChunks: 2,
	}
	var buf bytes.Buffer
	handled, err := writeStructured(&buf, outputYAML, in)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected yaml to be handled")
	}

	var got dfmsclient.UploadResult
	if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal yaml output: %v\n%s", err, buf.String())
	}
	if got.ID != in.ID || got.Name != in.Name || got.Size != in.Size {
		t.Errorf("round-trip mismatch:\n  got  %+v\n  want %+v", got, *in)
	}
}

func TestWriteStructured_JSON_FileList_UnmarshalsToModel(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mime := "application/pdf"
	checksum := "sha256:abc"
	in := &dfmsclient.FileList{
		Files: []models.File{
			{
				ID: "f1", Name: "doc.pdf", Size: 1024, Version: 1,
				Status: "active", MimeType: &mime, Checksum: &checksum,
				CreatedAt: now, UpdatedAt: now,
			},
			{
				ID: "f2", Name: "img.png", Size: 2048, Version: 2,
				Status: "active", CreatedAt: now, UpdatedAt: now,
			},
		},
		Total: 2, Page: 1, PageSize: 20, TotalPages: 1,
	}

	var buf bytes.Buffer
	if _, err := writeStructured(&buf, outputJSON, in); err != nil {
		t.Fatal(err)
	}

	var got dfmsclient.FileList
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal json: %v\n%s", err, buf.String())
	}
	if len(got.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(got.Files))
	}
	if got.Files[0].ID != "f1" || got.Files[0].Name != "doc.pdf" {
		t.Errorf("file[0] mismatch: %+v", got.Files[0])
	}
	if got.Files[1].ID != "f2" || got.Files[1].Size != 2048 {
		t.Errorf("file[1] mismatch: %+v", got.Files[1])
	}
	if got.Total != 2 || got.TotalPages != 1 {
		t.Errorf("pagination mismatch: total=%d pages=%d", got.Total, got.TotalPages)
	}
}

func TestWriteStructured_TableNotHandled(t *testing.T) {
	var buf bytes.Buffer
	handled, err := writeStructured(&buf, outputTable, map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Error("table format should not be handled by writeStructured")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for table format, got %q", buf.String())
	}
}

func TestWriteStructured_UnknownFormatNotHandled(t *testing.T) {
	var buf bytes.Buffer
	handled, err := writeStructured(&buf, "xml", nil)
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Error("unknown format should not be handled")
	}
}

// ─── Color / styler (NO_COLOR, --color, TTY detection) ──────────────────────

func TestStyler_ColorNever_ProducesNoANSI(t *testing.T) {
	s := newStyler(colorNever, os.Stdout)
	methods := map[string]string{
		"Bold":   s.Bold("text"),
		"Faint":  s.Faint("text"),
		"Red":    s.Red("text"),
		"Green":  s.Green("text"),
		"Yellow": s.Yellow("text"),
	}
	for name, out := range methods {
		if strings.Contains(out, "\x1b[") {
			t.Errorf("%s produced ANSI escape with --color=never: %q", name, out)
		}
		if out != "text" {
			t.Errorf("%s = %q, want %q", name, out, "text")
		}
	}
}

func TestStyler_ColorAlways_ProducesANSI(t *testing.T) {
	// Even writing to a bytes.Buffer (non-TTY), --color=always forces output.
	s := newStyler(colorAlways, &bytes.Buffer{})
	methods := map[string]string{
		"Bold":   s.Bold("text"),
		"Red":    s.Red("text"),
		"Green":  s.Green("text"),
		"Yellow": s.Yellow("text"),
	}
	for name, out := range methods {
		if !strings.Contains(out, "\x1b[") {
			t.Errorf("%s did not produce ANSI escape with --color=always: %q", name, out)
		}
		if !strings.Contains(out, "text") {
			t.Errorf("%s output missing original text: %q", name, out)
		}
	}
}

func TestStyler_ColorAuto_NonTTY_DisablesColor(t *testing.T) {
	// A bytes.Buffer is not a terminal, so auto mode disables color.
	s := newStyler(colorAuto, &bytes.Buffer{})
	if got := s.Red("text"); got != "text" {
		t.Errorf("Red on non-TTY = %q, want plain %q", got, "text")
	}
	if got := s.Bold("text"); got != "text" {
		t.Errorf("Bold on non-TTY = %q, want plain %q", got, "text")
	}
}

func TestStyler_NO_COLOR_DisablesColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	s := newStyler(colorAuto, os.Stdout)
	if got := s.Red("text"); strings.Contains(got, "\x1b[") {
		t.Errorf("Red should be disabled with NO_COLOR set: %q", got)
	}
}

func TestStyler_TERM_Dumb_DisablesColor(t *testing.T) {
	t.Setenv("TERM", "dumb")
	// Clear NO_COLOR to isolate TERM=dumb.
	t.Setenv("NO_COLOR", "")
	s := newStyler(colorAuto, os.Stdout)
	if got := s.Green("text"); strings.Contains(got, "\x1b[") {
		t.Errorf("Green should be disabled with TERM=dumb: %q", got)
	}
}

func TestStyler_CellMethods_TabwriterSafe(t *testing.T) {
	s := newStyler(colorAlways, &bytes.Buffer{})
	out := s.HeaderCell("ID")
	// Cell methods must bracket ANSI sequences in tabwriter.Escape (0xFF) bytes
	// so tabwriter excludes them from column width calculations.
	if !bytes.Contains([]byte(out), []byte{0xff}) {
		t.Errorf("HeaderCell missing tabwriter.Escape bytes: %q", out)
	}
	if !strings.Contains(out, "ID") {
		t.Errorf("HeaderCell output missing original text: %q", out)
	}
}

func TestStyler_CellMethods_DisabledReturnPlainText(t *testing.T) {
	s := newStyler(colorNever, &bytes.Buffer{})
	cells := map[string]string{
		"HeaderCell": s.HeaderCell("ID"),
		"GreenCell":  s.GreenCell("ok"),
		"YellowCell": s.YellowCell("warn"),
		"RedCell":    s.RedCell("err"),
	}
	for name, out := range cells {
		if strings.Contains(out, "\x1b[") || strings.Contains(out, "\xff") {
			t.Errorf("%s should be plain when disabled: %q", name, out)
		}
	}
}

func TestStyler_EmptyString_CellReturnsEmpty(t *testing.T) {
	s := newStyler(colorAlways, &bytes.Buffer{})
	// Cell methods guard against empty strings to avoid injecting bare ANSI
	// escapes into tabwriter output.
	if got := s.HeaderCell(""); got != "" {
		t.Errorf("HeaderCell(\"\") = %q, want \"\"", got)
	}
	if got := s.GreenCell(""); got != "" {
		t.Errorf("GreenCell(\"\") = %q, want \"\"", got)
	}
}

// ─── Error formatting ───────────────────────────────────────────────────────

func TestFormatError_APIError_IncludesCodeAndRequestID(t *testing.T) {
	err := &dfmsclient.APIError{
		StatusCode: 404, Code: "FILE_NOT_FOUND",
		Message: "file does not exist", RequestID: "req-abc-123",
	}
	var buf bytes.Buffer
	st := newStyler(colorNever, &buf)
	formatError(&buf, st, err)
	out := buf.String()

	if !strings.Contains(out, "Error:") {
		t.Error("missing Error: label")
	}
	if !strings.Contains(out, "[FILE_NOT_FOUND]") {
		t.Errorf("missing error code in output: %q", out)
	}
	if !strings.Contains(out, "file does not exist") {
		t.Errorf("missing message in output: %q", out)
	}
	if !strings.Contains(out, "req-abc-123") {
		t.Errorf("missing request_id in output: %q", out)
	}
}

func TestFormatError_NoCredentials_IncludesLoginHint(t *testing.T) {
	var buf bytes.Buffer
	st := newStyler(colorNever, &buf)
	formatError(&buf, st, dfmsclient.ErrNoCredentials)
	out := buf.String()

	if !strings.Contains(out, "Error:") {
		t.Error("missing Error: label")
	}
	if !strings.Contains(out, "dfmsctl auth login") {
		t.Errorf("missing login hint in output: %q", out)
	}
}

func TestFormatError_ConnectionError_IncludesHint(t *testing.T) {
	// ConnectionError requires a non-nil inner error (wraps transport failures).
	connErr := dfmsclient.NewConnectionError(io.ErrUnexpectedEOF)
	var buf bytes.Buffer
	st := newStyler(colorNever, &buf)
	formatError(&buf, st, connErr)
	out := buf.String()

	if !strings.Contains(out, "Error:") {
		t.Error("missing Error: label")
	}
	if !strings.Contains(out, "hint:") {
		t.Errorf("missing hint in connection error output: %q", out)
	}
}

func TestFormatError_WithColor_ContainsANSI(t *testing.T) {
	err := &dfmsclient.APIError{
		StatusCode: 500, Code: "INTERNAL", Message: "boom",
	}
	var buf bytes.Buffer
	st := newStyler(colorAlways, &buf)
	formatError(&buf, st, err)

	if !strings.Contains(buf.String(), "\x1b[") {
		t.Error("formatError with color=always should contain ANSI escapes")
	}
}

// ─── Completion commands ────────────────────────────────────────────────────

func TestCompletion_AllShells_ProduceOutput(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			root := NewRootCommand(BuildInfo{Version: "test"})
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetArgs([]string{"completion", shell})
			if err := root.Execute(); err != nil {
				t.Fatalf("completion %s failed: %v", shell, err)
			}
			if buf.Len() == 0 {
				t.Errorf("completion %s produced no output", shell)
			}
		})
	}
}

func TestCompletion_BareCommand_ShowsHelp(t *testing.T) {
	// Running `dfmsctl completion` without a shell should show help, not error.
	root := NewRootCommand(BuildInfo{Version: "test"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"completion"})
	// Cobra shows help for a parent command with no RunE and valid subcommands.
	_ = root.Execute()
	if !strings.Contains(buf.String(), "bash") || !strings.Contains(buf.String(), "zsh") {
		t.Errorf("completion help should list shells, got:\n%s", buf.String())
	}
}

// ─── End-to-end: -o json round-trip via fakeServer ──────────────────────────

func TestFilesGet_JSON_UnmarshalsToModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	now := time.Now().Truncate(time.Second).UTC()
	mux, _ := loginMux(t, "me@example.com")
	mux.HandleFunc("GET /api/v1/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"file": map[string]any{
				"id": r.PathValue("id"), "name": "report.pdf",
				"size": 5242880, "version": 3, "status": "active",
				"mime_type": "application/pdf", "checksum": "sha256:abc",
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mustRun(t, "context", "add", "test", "--url", srv.URL)
	mustRun(t, "auth", "login", "--email", "me@example.com")

	out := mustRun(t, "files", "get", "file-1", "-o", "json")
	var file models.File
	if err := json.Unmarshal([]byte(out), &file); err != nil {
		t.Fatalf("unmarshal files get json: %v\n%s", err, out)
	}
	if file.ID != "file-1" || file.Name != "report.pdf" || file.Size != 5242880 {
		t.Errorf("unexpected file: %+v", file)
	}
	if file.Version != 3 || file.Status != "active" {
		t.Errorf("unexpected version/status: %+v", file)
	}
}

func TestStorageUsage_YAML_UnmarshalsBack(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	mux, _ := loginMux(t, "me@example.com")
	mux.HandleFunc("GET /api/v1/storage/usage", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"used": 4096, "quota": 16384, "available": 12288, "used_pct": 25.0,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mustRun(t, "context", "add", "test", "--url", srv.URL)
	mustRun(t, "auth", "login", "--email", "me@example.com")

	out := mustRun(t, "storage", "usage", "-o", "yaml")
	var usage dfmsclient.StorageUsage
	if err := yaml.Unmarshal([]byte(out), &usage); err != nil {
		t.Fatalf("unmarshal storage yaml: %v\n%s", err, out)
	}
	if usage.Used != 4096 || usage.Quota != 16384 {
		t.Errorf("unexpected usage: %+v", usage)
	}
}

// ─── Quiet mode ─────────────────────────────────────────────────────────────

func TestFilesList_Quiet_PrintsOnlyIDs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	srv := newFakeServer(t)
	defer srv.Close()

	mustRun(t, "context", "add", "test", "--url", srv.URL)
	mustRun(t, "auth", "login", "--email", "me@example.com")

	// Upload two files.
	for _, name := range []string{"a.txt", "b.txt"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, "files", "upload", path)
	}

	out := mustRun(t, "files", "list", "-q")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("quiet list: expected 2 lines (IDs only), got %d:\n%s", len(lines), out)
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "file-") {
			t.Errorf("quiet list line should be a file ID, got %q", line)
		}
		// IDs only — no tab-separated columns or headers.
		if strings.Contains(line, "\t") || strings.Contains(line, "NAME") {
			t.Errorf("quiet mode should not contain table formatting: %q", line)
		}
	}
}

func TestFilesUpload_Quiet_PrintsOnlyID(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DFMSCTL_TOKEN_STORE", "file")
	t.Setenv("DFMSCTL_PASSWORD", "secret")

	srv := newFakeServer(t)
	defer srv.Close()

	mustRun(t, "context", "add", "test", "--url", srv.URL)
	mustRun(t, "auth", "login", "--email", "me@example.com")

	path := filepath.Join(t.TempDir(), "test.bin")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, "files", "upload", path, "-q")
	id := strings.TrimSpace(out)
	if !strings.HasPrefix(id, "file-") {
		t.Errorf("quiet upload should print only the file ID, got %q", id)
	}
	if strings.Contains(out, "Name:") || strings.Contains(out, "Chunks:") {
		t.Errorf("quiet mode should not contain table fields: %q", out)
	}
}

// ─── Progress meter (disabled on non-TTY) ───────────────────────────────────

func TestProgressMeter_Disabled_ReturnsOriginalStreams(t *testing.T) {
	r := strings.NewReader("hello")
	w := io.Discard

	m := newProgressMeter(io.Discard, "test", 100, false)
	if got := m.reader(r); got != r {
		t.Error("disabled meter.reader should return the original reader")
	}
	if got := m.writer(w); got != w {
		t.Error("disabled meter.writer should return the original writer")
	}
	// Finish on a disabled meter is a safe no-op.
	m.Finish()
	m.Finish() // double-finish must not panic
}

func TestProgressMeter_FinishIdempotent(t *testing.T) {
	m := newProgressMeter(io.Discard, "test", 0, true)
	m.Finish()
	m.Finish() // must not panic or block
}
