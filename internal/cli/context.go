package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/AnirudhSinghRajora/DFMS/internal/cliconfig"
)

// contextView is the machine-readable shape of a context for json/yaml output.
// It flattens the stored context with its name and resolved active state.
type contextView struct {
	Name               string `json:"name" yaml:"name"`
	URL                string `json:"url" yaml:"url"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify" yaml:"insecure_skip_verify"`
	Active             bool   `json:"active" yaml:"active"`
}

// newContextCommand builds the `context` command group for managing the set of
// known DFMS servers.
func newContextCommand(env *appEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"ctx"},
		Short:   "Manage DFMS server contexts",
		Long: "Manage the set of DFMS servers dfmsctl can talk to.\n\n" +
			"A context is a named server (its URL and TLS settings). One context is\n" +
			"active at a time; commands run against it unless --context overrides it.",
	}
	cmd.AddCommand(
		newContextAddCommand(env),
		newContextListCommand(env),
		newContextUseCommand(env),
		newContextRemoveCommand(env),
		newContextShowCommand(env),
	)
	return cmd
}

func newContextAddCommand(env *appEnv) *cobra.Command {
	var (
		serverURL string
		insecure  bool
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a server context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return errors.New("context name must not be empty")
			}
			if err := validateServerURL(serverURL); err != nil {
				return err
			}

			cfg, path, err := env.load()
			if err != nil {
				return err
			}
			_, existed := cfg.Context(name)
			cfg.SetContext(name, cliconfig.Context{URL: serverURL, InsecureSkipVerify: insecure})

			// Make the very first context active so the tool is usable right away.
			madeActive := cfg.CurrentContext == ""
			if madeActive {
				_ = cfg.Use(name) // cannot fail: just set above
			}

			if err := cfg.Save(path); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			verb := "Added"
			if existed {
				verb = "Updated"
			}
			env.successf(out, "%s context %q (%s)", verb, name, serverURL)
			if madeActive {
				fmt.Fprintf(out, "Set %q as the active context.\n", name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&serverURL, "url", "",
		"base URL of the DFMS API (e.g. https://dfms.example.com)")
	cmd.Flags().BoolVar(&insecure, "insecure-skip-verify", false,
		"skip TLS certificate verification for this context")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newContextListCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured server contexts",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := env.load()
			if err != nil {
				return err
			}
			active := env.activeContextName(cfg)
			out := cmd.OutOrStdout()

			views := make([]contextView, 0, len(cfg.Contexts))
			for _, name := range cfg.Names() {
				c := cfg.Contexts[name]
				views = append(views, contextView{
					Name:               name,
					URL:                c.URL,
					InsecureSkipVerify: c.InsecureSkipVerify,
					Active:             name == active,
				})
			}

			if handled, err := writeStructured(out, env.opts.output, views); handled || err != nil {
				return err
			}
			if len(views) == 0 {
				fmt.Fprintln(out, "No contexts configured. Add one with:")
				fmt.Fprintln(out, "  dfmsctl context add <name> --url <url>")
				return nil
			}

			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, env.style.HeaderCell("ACTIVE\tNAME\tURL\tINSECURE"))
			for _, v := range views {
				marker := ""
				if v.Active {
					marker = env.style.GreenCell("*")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%t\n", marker, v.Name, v.URL, v.InsecureSkipVerify)
			}
			return tw.Flush()
		},
	}
}

func newContextUseCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "use <name>",
		Short:             "Set the active server context",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeContextNames(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, path, err := env.load()
			if err != nil {
				return err
			}
			if err := cfg.Use(name); err != nil {
				return err
			}
			if err := cfg.Save(path); err != nil {
				return err
			}
			env.successf(cmd.OutOrStdout(), "Switched active context to %q", name)
			return nil
		},
	}
}

func newContextRemoveCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "remove <name>",
		Aliases:           []string{"rm"},
		Short:             "Remove a server context",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeContextNames(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, path, err := env.load()
			if err != nil {
				return err
			}
			wasActive := cfg.CurrentContext == name
			if err := cfg.RemoveContext(name); err != nil {
				return err
			}
			if err := cfg.Save(path); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			env.successf(out, "Removed context %q", name)
			if wasActive {
				fmt.Fprintln(out, "That was the active context; select another with 'dfmsctl context use <name>'.")
			}
			return nil
		},
	}
}

func newContextShowCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "show [name]",
		Short:             "Show details of a server context (defaults to the active one)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeContextNames(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := env.load()
			if err != nil {
				return err
			}
			active := env.activeContextName(cfg)

			name := active
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				return errors.New("no context specified and no active context is set")
			}

			c, ok := cfg.Context(name)
			if !ok {
				return fmt.Errorf("context %q does not exist", name)
			}
			view := contextView{
				Name:               name,
				URL:                c.URL,
				InsecureSkipVerify: c.InsecureSkipVerify,
				Active:             name == active,
			}

			out := cmd.OutOrStdout()
			if handled, err := writeStructured(out, env.opts.output, view); handled || err != nil {
				return err
			}

			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintf(tw, "Name:\t%s\n", view.Name)
			fmt.Fprintf(tw, "URL:\t%s\n", view.URL)
			fmt.Fprintf(tw, "Insecure:\t%t\n", view.InsecureSkipVerify)
			fmt.Fprintf(tw, "Active:\t%t\n", view.Active)
			return tw.Flush()
		},
	}
}

// validateServerURL ensures a context URL is an absolute http(s) URL with a host.
func validateServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must use the http or https scheme: %q", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("URL must include a host: %q", raw)
	}
	return nil
}
