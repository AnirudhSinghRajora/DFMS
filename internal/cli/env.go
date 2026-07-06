package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/AnirudhSinghRajora/DFMS/internal/cliconfig"
)

// envContext is the environment variable that overrides the active context.
const envContext = "DFMSCTL_CONTEXT"

// appEnv bundles the per-invocation dependencies shared by subcommands: the
// resolved global flags plus helpers for loading the on-disk configuration and
// resolving which server context is active. It is the seam through which later
// phases also construct the API client, so commands depend on this rather than
// reaching for globals.
//
// style is resolved once, before any subcommand runs, against the command's
// output destination; it is never nil (a disabled styler is installed up front)
// so command code can colorize unconditionally.
type appEnv struct {
	opts  *globalOptions
	style *styler
}

// applyStyle resolves the color policy for the command's stdout destination and
// installs the styler. It runs in the root PersistentPreRunE so every subcommand
// sees a ready styler matched to where its output actually goes.
func (e *appEnv) applyStyle(cmd *cobra.Command) {
	e.style = newStyler(e.opts.color, cmd.OutOrStdout())
}

// successf writes a confirmation message, rendered green when color is enabled.
func (e *appEnv) successf(w io.Writer, format string, a ...any) {
	fmt.Fprintln(w, e.style.Green(fmt.Sprintf(format, a...)))
}

// progressMeter builds a transfer progress meter bound to the command's stderr.
// It is enabled only for interactive, human-oriented runs: a terminal stderr,
// table output, and neither --quiet nor --no-progress. Otherwise it is a no-op.
func (e *appEnv) progressMeter(cmd *cobra.Command, label string, total int64) *progressMeter {
	enabled := !e.opts.quiet &&
		e.opts.output == outputTable &&
		!e.opts.noProgress &&
		isTerminalWriter(cmd.ErrOrStderr())
	return newProgressMeter(cmd.ErrOrStderr(), label, total, enabled)
}

// load reads the CLI configuration from its default location, returning the
// parsed config and the path it came from so callers can save changes back. A
// missing file yields an empty config rather than an error.
func (e *appEnv) load() (*cliconfig.Config, string, error) {
	path, err := cliconfig.DefaultPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := cliconfig.Load(path)
	if err != nil {
		return nil, "", err
	}
	return cfg, path, nil
}

// activeContextName resolves which context is active, applying the precedence
//
//	--context flag  >  DFMSCTL_CONTEXT env  >  config file current_context
//
// It returns "" when none of those is set.
func (e *appEnv) activeContextName(cfg *cliconfig.Config) string {
	if e.opts.context != "" {
		return e.opts.context
	}
	if name := os.Getenv(envContext); name != "" {
		return name
	}
	return cfg.CurrentContext
}
