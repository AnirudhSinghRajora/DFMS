package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newFilesVersionsCommand builds the `files versions` group for managing a
// file's version history.
func newFilesVersionsCommand(env *appEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "List, download, and delete file versions",
	}
	cmd.AddCommand(
		newVersionsListCommand(env),
		newVersionsDownloadCommand(env),
		newVersionsDeleteCommand(env),
	)
	return cmd
}

func newVersionsListCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "list <file-id>",
		Aliases:           []string{"ls"},
		Short:             "List a file's version history",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeFileIDs(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			versions, err := sess.client.ListVersions(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, versions); handled || herr != nil {
				return herr
			}
			if len(versions.Versions) == 0 {
				fmt.Fprintln(out, "No versions.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, env.style.HeaderCell("VERSION\tID\tSIZE\tSTATUS\tCREATED"))
			for i := range versions.Versions {
				v := &versions.Versions[i]
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
					v.Version, v.ID, formatBytes(v.Size), v.Status, v.CreatedAt.Local().Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
}

func newVersionsDownloadCommand(env *appEnv) *cobra.Command {
	var outFile string
	cmd := &cobra.Command{
		Use:               "download <file-id> <version>",
		Short:             "Download a specific version of a file",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeFileIDs(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			version, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid version %q: must be a number", args[1])
			}

			sess, err := env.session()
			if err != nil {
				return err
			}

			if outFile == "-" {
				_, err = sess.client.DownloadVersion(cmd.Context(), id, version, cmd.OutOrStdout())
				return err
			}

			destDir := "."
			if outFile != "" {
				destDir = filepath.Dir(outFile)
			}
			tmp, err := os.CreateTemp(destDir, ".dfms-download-*")
			if err != nil {
				return fmt.Errorf("creating temp file: %w", err)
			}
			tmpName := tmp.Name()

			info, derr := sess.client.DownloadVersion(cmd.Context(), id, version, tmp)
			closeErr := tmp.Close()
			if derr != nil {
				_ = os.Remove(tmpName)
				return derr
			}
			if closeErr != nil {
				_ = os.Remove(tmpName)
				return fmt.Errorf("writing download: %w", closeErr)
			}

			dest := outFile
			if dest == "" {
				name := info.Filename
				if name == "" {
					name = fmt.Sprintf("%s.v%d", id, version)
				}
				dest = filepath.Base(name)
			}
			if err := os.Rename(tmpName, dest); err != nil {
				_ = os.Remove(tmpName)
				return fmt.Errorf("saving download: %w", err)
			}
			if !env.opts.quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "Downloaded %s (%s)\n", dest, formatBytes(info.Size))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&outFile, "output-file", "O", "",
		`destination path, or "-" for stdout (default: the server-provided name)`)
	return cmd
}

func newVersionsDeleteCommand(env *appEnv) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <file-id> <version>",
		Aliases: []string{"rm"},
		Short:   "Delete a specific version of a file",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			version, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid version %q: must be a number", args[1])
			}

			if !yes && term.IsTerminal(int(os.Stdin.Fd())) {
				confirmed, cerr := confirm(cmd, fmt.Sprintf("Delete version %d of file %q?", version, id))
				if cerr != nil {
					return cerr
				}
				if !confirmed {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}

			sess, err := env.session()
			if err != nil {
				return err
			}
			if err := sess.client.DeleteVersion(cmd.Context(), id, version); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted version %d of file %q\n", version, id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}
