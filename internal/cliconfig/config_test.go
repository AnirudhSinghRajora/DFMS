package cliconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	cfg := empty()
	cfg.SetContext("prod", Context{URL: "https://dfms.example.com", InsecureSkipVerify: true})
	cfg.SetContext("local", Context{URL: "http://localhost:8080"})
	if err := cfg.Use("prod"); err != nil {
		t.Fatalf("Use: %v", err)
	}

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Errorf("file permissions = %v, want %v", perm, filePerm)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.CurrentContext != "prod" {
		t.Errorf("CurrentContext = %q, want %q", got.CurrentContext, "prod")
	}
	prod, ok := got.Context("prod")
	if !ok || prod.URL != "https://dfms.example.com" || !prod.InsecureSkipVerify {
		t.Errorf("prod context round-tripped incorrectly: %+v (ok=%v)", prod, ok)
	}
	if names := got.Names(); len(names) != 2 || names[0] != "local" || names[1] != "prod" {
		t.Errorf("Names() = %v, want sorted [local prod]", names)
	}
}

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load of missing file should not error: %v", err)
	}
	if got.CurrentContext != "" || len(got.Contexts) != 0 {
		t.Errorf("expected empty config, got %+v", got)
	}
	// The map must be usable without a nil check by callers.
	got.SetContext("x", Context{URL: "http://x"})
	if _, ok := got.Context("x"); !ok {
		t.Error("SetContext on a freshly loaded empty config did not take effect")
	}
}

func TestLoad_MalformedFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("\tnot: [valid"), filePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected an error parsing malformed config")
	}
}

func TestUse_UnknownContextErrors(t *testing.T) {
	cfg := empty()
	err := cfg.Use("ghost")
	if err == nil {
		t.Fatal("expected error using an unknown context")
	}
	if cfg.CurrentContext != "" {
		t.Errorf("CurrentContext should remain empty, got %q", cfg.CurrentContext)
	}
}

func TestRemoveContext_ClearsActiveSelection(t *testing.T) {
	cfg := empty()
	cfg.SetContext("prod", Context{URL: "https://x"})
	if err := cfg.Use("prod"); err != nil {
		t.Fatal(err)
	}

	if err := cfg.RemoveContext("prod"); err != nil {
		t.Fatalf("RemoveContext: %v", err)
	}
	if cfg.CurrentContext != "" {
		t.Errorf("removing the active context should clear it, got %q", cfg.CurrentContext)
	}
	if err := cfg.RemoveContext("prod"); err == nil {
		t.Error("removing an unknown context should error")
	}
}

func TestDefaultPath_HonorsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join("/tmp/xdg-test", appDirName, "config.yaml")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
