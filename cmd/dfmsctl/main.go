// Command dfmsctl is the command-line client for DFMS, the Distributed File
// Management System. It wraps the DFMS HTTP API with ergonomic commands for
// managing servers, authentication, and file operations.
//
// See docs/CLI_DESIGN.md for the overall design and docs/CLI_IMPLEMENTATION_PLAN.md
// for the phased build plan.
package main

import (
	"os"

	"github.com/AnirudhSinghRajora/DFMS/internal/cli"
)

// Build metadata, injected at link time via -ldflags "-X main.version=...".
// The defaults keep `go run`/`go install` builds self-describing without a
// build system in the loop.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	root := cli.NewRootCommand(cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	})

	if err := root.Execute(); err != nil {
		// The root command silences Cobra's own error printing so messages are
		// formatted consistently here, and the exit code is mapped from the
		// error's domain (see cli.ExitCode).
		cli.PrintError(root, err)
		os.Exit(cli.ExitCode(err))
	}
}
