package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"
)

// newVersionCommand returns the `dfmsctl version` command. It honors the global
// --output flag so the build metadata can be consumed by scripts as JSON
// (`dfmsctl version -o json`) as well as read by humans.
func newVersionCommand(build BuildInfo, opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := versionInfo{
				Version:   build.Version,
				Commit:    build.Commit,
				BuildDate: build.Date,
				GoVersion: runtime.Version(),
				Platform:  runtime.GOOS + "/" + runtime.GOARCH,
			}
			return info.render(cmd.OutOrStdout(), opts.output)
		},
	}
}

// versionInfo is the structured build metadata reported by the version command.
type versionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// render writes the version information to w in the requested format. The "json"
// format emits the struct verbatim; any other value uses the human-readable
// layout (the full table/yaml renderer arrives with the output package in a
// later phase).
func (v *versionInfo) render(w io.Writer, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}

	_, err := fmt.Fprintf(w,
		"dfmsctl %s\n  commit:   %s\n  built:    %s\n  go:       %s\n  platform: %s\n",
		v.Version, v.Commit, v.BuildDate, v.GoVersion, v.Platform,
	)
	return err
}
