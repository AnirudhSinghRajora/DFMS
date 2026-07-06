// Package cli implements the dfmsctl command tree: the root command, its global
// flags, and all subcommands. Command handlers stay thin and delegate I/O to
// internal/dfmsclient so the bulk of the logic is testable below the command
// layer.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// BuildInfo carries version metadata injected at link time. It is passed in
// from main rather than read from package-level link variables here, so this
// package has no link-time globals and remains straightforward to unit-test.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// globalOptions holds the persistent flags shared by every subcommand. Keeping
// them in one struct (rather than package globals) makes flag state explicit and
// lets tests construct an isolated command tree.
type globalOptions struct {
	context    string // server context to use, overriding the active one
	output     string // output format: table, json, or yaml
	color      string // color policy: auto, always, or never
	quiet      bool   // print only essential output (e.g. IDs)
	verbose    bool   // emit request/response diagnostics to stderr
	noProgress bool   // suppress transfer progress meters
}

// validate checks the global flags that have a fixed set of valid values. It
// runs once per invocation via the root command's PersistentPreRunE, so every
// subcommand can assume opts is well-formed.
func (o *globalOptions) validate() error {
	switch o.output {
	case outputTable, outputJSON, outputYAML:
	default:
		return fmt.Errorf("invalid --output %q (must be %s, %s, or %s)",
			o.output, outputTable, outputJSON, outputYAML)
	}
	switch o.color {
	case colorAuto, colorAlways, colorNever:
	default:
		return fmt.Errorf("invalid --color %q (must be %s, %s, or %s)",
			o.color, colorAuto, colorAlways, colorNever)
	}
	return nil
}

// NewRootCommand builds the root dfmsctl command with its global flags and
// wires in all subcommands. It returns a ready-to-Execute command, which keeps
// main thin and gives tests a fresh tree per call.
func NewRootCommand(build BuildInfo) *cobra.Command {
	opts := &globalOptions{}
	env := &appEnv{opts: opts, style: &styler{}}

	root := &cobra.Command{
		Use:   "dfmsctl",
		Short: "Command-line client for the DFMS distributed file store",
		Long: "dfmsctl is the command-line client for DFMS (Distributed File Management System).\n" +
			"It manages server contexts, authentication, and file operations against the DFMS API.",
		// Runtime errors are reported by main; don't dump usage text for them.
		// Cobra still prints usage for genuine flag/arg parsing errors.
		SilenceUsage: true,
		// Print errors ourselves (via main) so formatting stays consistent.
		SilenceErrors: true,
		// We provide an explicit "completion" subcommand with per-shell install
		// instructions, so disable Cobra's auto-generated one.
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		// Validate global flags and resolve color once, before any subcommand
		// runs, so every subcommand sees well-formed options and a ready styler.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			env.applyStyle(cmd)
			return nil
		},
	}

	// Tag flag-parsing errors so they map to the usage exit code.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err}
	})

	flags := root.PersistentFlags()
	flags.StringVarP(&opts.context, "context", "c", "",
		"server context to use (overrides the active context)")
	flags.StringVarP(&opts.output, "output", "o", "table",
		"output format: table, json, or yaml")
	flags.StringVar(&opts.color, "color", "auto",
		"colorize output: auto, always, or never")
	flags.BoolVarP(&opts.quiet, "quiet", "q", false,
		"print only essential output, such as IDs")
	flags.BoolVar(&opts.verbose, "verbose", false,
		"print request/response diagnostics to stderr")
	flags.BoolVar(&opts.noProgress, "no-progress", false,
		"disable transfer progress meters")

	root.AddCommand(newVersionCommand(build, opts))
	root.AddCommand(newContextCommand(env))
	root.AddCommand(newAuthCommand(env))
	root.AddCommand(newFilesCommand(env))
	root.AddCommand(newFoldersCommand(env))
	root.AddCommand(newStorageCommand(env))
	root.AddCommand(newAdminCommand(env))
	root.AddCommand(newCompletionCommand())

	registerFlagCompletions(root, env)

	return root
}

// PrintError writes err to the root command's error stream in the CLI's
// friendly, colorized format. main calls it so error presentation lives with
// the rest of the command layer (and honors --color) rather than in package main.
func PrintError(root *cobra.Command, err error) {
	mode := colorAuto
	if f := root.PersistentFlags().Lookup("color"); f != nil {
		mode = f.Value.String()
	}
	w := root.ErrOrStderr()
	formatError(w, newStyler(mode, w), err)
}
