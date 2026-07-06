package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionCommand_Text(t *testing.T) {
	root := NewRootCommand(BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-06-22"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{"dfmsctl 1.2.3", "abc1234", "2026-06-22"} {
		if !strings.Contains(got, want) {
			t.Errorf("version output missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestVersionCommand_JSON(t *testing.T) {
	root := NewRootCommand(BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-06-22"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version", "--output", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	var info versionInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("version --output json did not emit valid JSON: %v\ngot:\n%s", err, out.String())
	}
	if info.Version != "1.2.3" || info.Commit != "abc1234" || info.BuildDate != "2026-06-22" {
		t.Errorf("unexpected version info: %+v", info)
	}
	if info.GoVersion == "" || info.Platform == "" {
		t.Errorf("runtime fields not populated: %+v", info)
	}
}
