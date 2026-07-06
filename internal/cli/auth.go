package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
)

// envPassword lets automation supply a password without a flag (which would
// leak into shell history and process listings) or an interactive prompt.
const envPassword = "DFMSCTL_PASSWORD"

// newAuthCommand builds the `auth` command group: register, login, logout, and
// status, all operating against the active context.
func newAuthCommand(env *appEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with a DFMS server",
		Long: "Register, log in, and inspect the session for the active context.\n\n" +
			"Tokens are stored in the OS keyring when available, otherwise in an\n" +
			"owner-only file. Access tokens are refreshed automatically.",
	}
	cmd.AddCommand(
		newAuthRegisterCommand(env),
		newAuthLoginCommand(env),
		newAuthLogoutCommand(env),
		newAuthStatusCommand(env),
	)
	return cmd
}

func newAuthRegisterCommand(env *appEnv) *cobra.Command {
	var (
		email       string
		displayName string
		pwStdin     bool
	)
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Create an account and start a session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			password, err := readNewPassword(cmd, pwStdin)
			if err != nil {
				return err
			}
			sess, err := env.session()
			if err != nil {
				return err
			}
			tokens, err := sess.client.Register(cmd.Context(), email, password, displayName)
			if err != nil {
				return err
			}
			if err := sess.tokens.Save(tokens); err != nil {
				return fmt.Errorf("storing credentials: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registered and logged in as %s (context %q)\n", email, sess.contextName)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "account email address")
	cmd.Flags().StringVar(&displayName, "display-name", "", "display name for the account")
	cmd.Flags().BoolVar(&pwStdin, "password-stdin", false, "read the password from stdin instead of prompting")
	_ = cmd.MarkFlagRequired("email")
	_ = cmd.MarkFlagRequired("display-name")
	return cmd
}

func newAuthLoginCommand(env *appEnv) *cobra.Command {
	var (
		email   string
		pwStdin bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a DFMS server and store the session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			password, err := readPassword(cmd, pwStdin, "Password: ")
			if err != nil {
				return err
			}
			if password == "" {
				return errors.New("password must not be empty")
			}
			sess, err := env.session()
			if err != nil {
				return err
			}
			tokens, err := sess.client.Login(cmd.Context(), email, password)
			if err != nil {
				return err
			}
			if err := sess.tokens.Save(tokens); err != nil {
				return fmt.Errorf("storing credentials: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s (context %q)\n", email, sess.contextName)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "account email address")
	cmd.Flags().BoolVar(&pwStdin, "password-stdin", false, "read the password from stdin instead of prompting")
	_ = cmd.MarkFlagRequired("email")
	return cmd
}

func newAuthLogoutCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored session for the active context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			if err := sess.tokens.Delete(); err != nil {
				return fmt.Errorf("clearing credentials: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged out of context %q\n", sess.contextName)
			return nil
		},
	}
}

// statusView is the machine-readable shape of the auth status. ExpiresAt is a
// pointer so it is omitted (rather than shown as the zero time) when logged out.
type statusView struct {
	Context   string     `json:"context" yaml:"context"`
	LoggedIn  bool       `json:"logged_in" yaml:"logged_in"`
	Email     string     `json:"email,omitempty" yaml:"email,omitempty"`
	Role      string     `json:"role,omitempty" yaml:"role,omitempty"`
	UserID    string     `json:"user_id,omitempty" yaml:"user_id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	Expired   bool       `json:"expired,omitempty" yaml:"expired,omitempty"`
}

func newAuthStatusCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the authentication status for the active context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			tokens, err := sess.tokens.Load()
			if errors.Is(err, dfmsclient.ErrNoCredentials) {
				view := statusView{Context: sess.contextName, LoggedIn: false}
				if handled, herr := writeStructured(out, env.opts.output, view); handled || herr != nil {
					return herr
				}
				fmt.Fprintf(out, "Not logged in to context %q. Run 'dfmsctl auth login'.\n", sess.contextName)
				return nil
			}
			if err != nil {
				return err
			}

			id, err := dfmsclient.Identify(tokens.AccessToken)
			if err != nil {
				return err
			}
			view := statusView{
				Context:  sess.contextName,
				LoggedIn: true,
				Email:    id.Email,
				Role:     id.Role,
				UserID:   id.UserID,
			}
			if !id.ExpiresAt.IsZero() {
				expiresAt := id.ExpiresAt
				view.ExpiresAt = &expiresAt
				view.Expired = id.Expired()
			}
			if handled, herr := writeStructured(out, env.opts.output, view); handled || herr != nil {
				return herr
			}

			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintf(tw, "Context:\t%s\n", view.Context)
			fmt.Fprintf(tw, "Email:\t%s\n", view.Email)
			fmt.Fprintf(tw, "Role:\t%s\n", view.Role)
			fmt.Fprintf(tw, "User ID:\t%s\n", view.UserID)
			if view.ExpiresAt != nil {
				state := "valid"
				if view.Expired {
					state = "expired (will refresh on next use)"
				}
				fmt.Fprintf(tw, "Expires:\t%s (%s)\n", view.ExpiresAt.Local().Format(time.RFC3339), state)
			}
			return tw.Flush()
		},
	}
}

// readPassword obtains a password without exposing it on the command line. It
// prefers --password-stdin, then the DFMSCTL_PASSWORD environment variable, and
// otherwise prompts interactively with terminal echo disabled.
func readPassword(cmd *cobra.Command, fromStdin bool, prompt string) (string, error) {
	if fromStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if pw := os.Getenv(envPassword); pw != "" {
		return pw, nil
	}

	fmt.Fprint(cmd.ErrOrStderr(), prompt)
	secret, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(secret), nil
}

// readNewPassword reads a password for account creation, confirming it by a
// second prompt when running interactively (skipped for stdin/env automation).
func readNewPassword(cmd *cobra.Command, fromStdin bool) (string, error) {
	password, err := readPassword(cmd, fromStdin, "Password: ")
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	if !fromStdin && os.Getenv(envPassword) == "" {
		confirm, err := readPassword(cmd, false, "Confirm password: ")
		if err != nil {
			return "", err
		}
		if confirm != password {
			return "", errors.New("passwords do not match")
		}
	}
	return password, nil
}
