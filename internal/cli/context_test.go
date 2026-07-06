package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AnirudhSinghRajora/DFMS/internal/cliconfig"
)

// runDfmsctl executes the command tree with a fresh root each call, simulating
// independent CLI invocations that share only the on-disk config. Output (both
// stdout and stderr) is captured and returned.
func runDfmsctl(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand(BuildInfo{Version: "test"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestContextLifecycle_Persists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := runDfmsctl(t, "context", "add", "local", "--url", "http://localhost:8080"); err != nil {
		t.Fatalf("add local: %v", err)
	}
	if _, err := runDfmsctl(t, "context", "add", "prod", "--url", "https://dfms.example.com"); err != nil {
		t.Fatalf("add prod: %v", err)
	}
	if _, err := runDfmsctl(t, "context", "use", "prod"); err != nil {
		t.Fatalf("use prod: %v", err)
	}

	out, err := runDfmsctl(t, "context", "list", "-o", "json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var views []contextView
	if err := json.Unmarshal([]byte(out), &views); err != nil {
		t.Fatalf("parse list json: %v\n%s", err, out)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 contexts, got %d: %+v", len(views), views)
	}
	byName := map[string]contextView{}
	for _, v := range views {
		byName[v.Name] = v
	}
	if !byName["prod"].Active {
		t.Errorf("prod should be active: %+v", byName["prod"])
	}
	if byName["local"].Active {
		t.Errorf("local should not be active: %+v", byName["local"])
	}
	if byName["local"].URL != "http://localhost:8080" {
		t.Errorf("local URL = %q", byName["local"].URL)
	}
}

func TestContextAdd_FirstBecomesActive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out, err := runDfmsctl(t, "context", "add", "only", "--url", "http://x:8080")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "active context") {
		t.Errorf("first context should be announced as active; got:\n%s", out)
	}

	show, err := runDfmsctl(t, "context", "show", "-o", "json")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var v contextView
	if err := json.Unmarshal([]byte(show), &v); err != nil {
		t.Fatalf("parse show json: %v\n%s", err, show)
	}
	if v.Name != "only" || !v.Active {
		t.Errorf("expected 'only' to be the active context, got %+v", v)
	}
}

func TestContextUse_UnknownErrorsClearly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := runDfmsctl(t, "context", "use", "ghost")
	if err == nil {
		t.Fatal("expected error using an unknown context")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing context, got: %v", err)
	}
}

func TestContextAdd_RejectsNonHTTPURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := runDfmsctl(t, "context", "add", "bad", "--url", "ftp://nope")
	if err == nil {
		t.Fatal("expected an error for a non-http(s) URL")
	}
}

func TestInvalidOutputFlagRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := runDfmsctl(t, "context", "list", "-o", "xml")
	if err == nil {
		t.Fatal("expected an error for an unsupported --output value")
	}
}

func TestActiveContextName_Precedence(t *testing.T) {
	cfg := &cliconfig.Config{
		CurrentContext: "fromfile",
		Contexts: map[string]cliconfig.Context{
			"fromfile": {URL: "http://file"},
			"fromenv":  {URL: "http://env"},
			"fromflag": {URL: "http://flag"},
		},
	}
	env := &appEnv{opts: &globalOptions{}}

	if got := env.activeContextName(cfg); got != "fromfile" {
		t.Errorf("with nothing set, want file value; got %q", got)
	}

	t.Setenv(envContext, "fromenv")
	if got := env.activeContextName(cfg); got != "fromenv" {
		t.Errorf("env should override file; got %q", got)
	}

	env.opts.context = "fromflag"
	if got := env.activeContextName(cfg); got != "fromflag" {
		t.Errorf("flag should override env; got %q", got)
	}
}
