package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newFoldersCommand builds the `folders` command group for managing the virtual
// folder hierarchy.
func newFoldersCommand(env *appEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "folders",
		Aliases: []string{"folder"},
		Short:   "Create, browse, and manage folders",
	}
	cmd.AddCommand(
		newFoldersCreateCommand(env),
		newFoldersContentsCommand(env),
		newFoldersMoveCommand(env),
		newFoldersDeleteCommand(env),
	)
	return cmd
}

func newFoldersCreateCommand(env *appEnv) *cobra.Command {
	var parent string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			var parentID *string
			if parent != "" {
				parentID = &parent
			}
			folder, err := sess.client.CreateFolder(cmd.Context(), args[0], parentID)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, folder); handled || herr != nil {
				return herr
			}
			env.successf(out, "Created folder %q (%s)", folder.Name, folder.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "parent folder ID (default: root)")
	return cmd
}

func newFoldersContentsCommand(env *appEnv) *cobra.Command {
	var (
		page     int
		pageSize int
	)
	cmd := &cobra.Command{
		Use:     "contents <folder-id>",
		Aliases: []string{"ls"},
		Short:   "List the contents of a folder",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			contents, err := sess.client.FolderContents(cmd.Context(), args[0], page, pageSize)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, contents); handled || herr != nil {
				return herr
			}
			if env.opts.quiet {
				for i := range contents.Contents {
					fmt.Fprintln(out, contents.Contents[i].ID)
				}
				return nil
			}
			if len(contents.Contents) == 0 {
				fmt.Fprintln(out, "Folder is empty.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, env.style.HeaderCell("TYPE\tID\tNAME\tSIZE\tMODIFIED"))
			for i := range contents.Contents {
				f := &contents.Contents[i]
				kind := "file"
				size := formatBytes(f.Size)
				if f.IsDirectory {
					kind = env.style.YellowCell("dir")
					size = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					kind, f.ID, f.Name, size, f.UpdatedAt.Local().Format(time.RFC3339))
			}
			_ = tw.Flush()
			if contents.TotalPages > 0 {
				fmt.Fprintf(out, "\nPage %d of %d (%d total)\n", contents.Page, contents.TotalPages, contents.Total)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&page, "page", 1, "page number")
	cmd.Flags().IntVar(&pageSize, "page-size", 50, "items per page")
	return cmd
}

func newFoldersMoveCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "move <file-id> [dest-folder-id]",
		Short:             "Move a file or folder (omit the destination to move to root)",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeFileIDs(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			var newParent *string
			dest := "root"
			if len(args) == 2 && args[1] != "" {
				newParent = &args[1]
				dest = args[1]
			}
			if err := sess.client.MoveFile(cmd.Context(), args[0], newParent); err != nil {
				return err
			}
			env.successf(cmd.OutOrStdout(), "Moved %q to %s", args[0], dest)
			return nil
		},
	}
}

func newFoldersDeleteCommand(env *appEnv) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <folder-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a folder and everything inside it (recursive)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			// Recursive delete is destructive: confirm interactively unless
			// --yes is given or input isn't a terminal.
			if !yes && term.IsTerminal(int(os.Stdin.Fd())) {
				confirmed, cerr := confirm(cmd, fmt.Sprintf("Recursively delete folder %q and all its contents?", id))
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
			if err := sess.client.DeleteFolder(cmd.Context(), id); err != nil {
				return err
			}
			env.successf(cmd.OutOrStdout(), "Deleted folder %q", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}
