package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// completionTimeout bounds the network call that backs dynamic completion of
// file IDs, so a slow or unreachable server never hangs the user's shell.
const completionTimeout = 3 * time.Second

// newCompletionCommand builds the `completion` command group which generates
// shell completion scripts for bash, zsh, fish, and powershell. Each
// subcommand's Long help includes install instructions so `dfmsctl completion
// bash --help` is self-contained.
func newCompletionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [shell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for dfmsctl.

The generated script enables tab completion for commands, flags, file IDs,
and context names. See the help for each sub-command for shell-specific
install instructions:

  dfmsctl completion bash --help
  dfmsctl completion zsh  --help`,
		// No bare "completion" execution — a shell must be specified.
		Args: cobra.NoArgs,
	}

	cmd.AddCommand(
		newCompletionBashCommand(),
		newCompletionZshCommand(),
		newCompletionFishCommand(),
		newCompletionPowershellCommand(),
	)
	return cmd
}

func newCompletionBashCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "bash",
		Short: "Generate bash completion script",
		Long: `Generate bash completion script for dfmsctl.

To load completions in your current shell session:

  source <(dfmsctl completion bash)

To install completions permanently (requires bash-completion):

  # Linux:
  dfmsctl completion bash > /etc/bash_completion.d/dfmsctl

  # macOS (Homebrew):
  dfmsctl completion bash > $(brew --prefix)/etc/bash_completion.d/dfmsctl

You will need to start a new shell for the completions to take effect.`,
		Args:               cobra.NoArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Root().GenBashCompletionV2(cmd.OutOrStdout(), true)
		},
	}
}

func newCompletionZshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "zsh",
		Short: "Generate zsh completion script",
		Long: `Generate zsh completion script for dfmsctl.

To load completions in your current shell session:

  source <(dfmsctl completion zsh)

To install completions permanently, place the output in a directory
listed in your $fpath:

  dfmsctl completion zsh > "${fpath[1]}/_dfmsctl"

You may need to run 'compinit' or start a new shell for completions
to take effect.`,
		Args:               cobra.NoArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
		},
	}
}

func newCompletionFishCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "fish",
		Short: "Generate fish completion script",
		Long: `Generate fish completion script for dfmsctl.

To load completions in your current shell session:

  dfmsctl completion fish | source

To install completions permanently:

  dfmsctl completion fish > ~/.config/fish/completions/dfmsctl.fish`,
		Args:               cobra.NoArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
		},
	}
}

func newCompletionPowershellCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "powershell",
		Short: "Generate powershell completion script",
		Long: `Generate powershell completion script for dfmsctl.

To load completions in your current shell session:

  dfmsctl completion powershell | Out-String | Invoke-Expression

To install completions permanently, add the above line to your
PowerShell profile.`,
		Args:               cobra.NoArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
		},
	}
}

// registerFlagCompletions wires shell completion for the persistent flags whose
// values come from a fixed set or the local config. Argument completion is
// attached per-command where the commands are defined (see completeContextNames
// and completeFileIDs).
func registerFlagCompletions(root *cobra.Command, env *appEnv) {
	fixed := func(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return values, cobra.ShellCompDirectiveNoFileComp
		}
	}
	_ = root.RegisterFlagCompletionFunc("output", fixed(outputTable, outputJSON, outputYAML))
	_ = root.RegisterFlagCompletionFunc("color", fixed(colorAuto, colorAlways, colorNever))
	_ = root.RegisterFlagCompletionFunc("context", completeContextNames(env))
}

// completeContextNames completes the first argument with configured context
// names. It only reads local config, so it is fast and side-effect free.
func completeContextNames(env *appEnv) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, _, err := env.load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := make([]string, 0, len(cfg.Contexts))
		for _, name := range cfg.Names() {
			if strings.HasPrefix(name, toComplete) {
				names = append(names, name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeFileIDs completes the first argument with the IDs of the caller's
// files, annotated with each file's name. It performs a short, best-effort API
// call; any failure (no session, unreachable server) yields no suggestions
// rather than an error, which is the right behavior for tab completion.
func completeFileIDs(env *appEnv) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		sess, err := env.session()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
		defer cancel()
		list, err := sess.client.ListFiles(ctx, 1, 100)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := make([]string, 0, len(list.Files))
		for i := range list.Files {
			f := &list.Files[i]
			if strings.HasPrefix(f.ID, toComplete) {
				out = append(out, f.ID+"\t"+f.Name)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
