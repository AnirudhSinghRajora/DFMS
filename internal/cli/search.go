package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
)

func newFilesSearchCommand(env *appEnv) *cobra.Command {
	var (
		mimeType string
		minSize  int64
		maxSize  int64
		page     int
		pageSize int
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search your files by name, with optional filters",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := dfmsclient.SearchOptions{
				Query:    args[0],
				MimeType: mimeType,
				Page:     page,
				PageSize: pageSize,
			}
			if cmd.Flags().Changed("min-size") {
				opts.MinSize = &minSize
			}
			if cmd.Flags().Changed("max-size") {
				opts.MaxSize = &maxSize
			}

			sess, err := env.session()
			if err != nil {
				return err
			}
			results, err := sess.client.Search(cmd.Context(), &opts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, results); handled || herr != nil {
				return herr
			}
			if env.opts.quiet {
				for i := range results.Results {
					fmt.Fprintln(out, results.Results[i].ID)
				}
				return nil
			}
			if len(results.Results) == 0 {
				fmt.Fprintln(out, "No matching files.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, env.style.HeaderCell("ID\tNAME\tSIZE\tMODIFIED"))
			for i := range results.Results {
				f := &results.Results[i]
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
					f.ID, f.Name, formatBytes(f.Size), f.UpdatedAt.Local().Format(time.RFC3339))
			}
			_ = tw.Flush()
			if results.TotalPages > 0 {
				fmt.Fprintf(out, "\nPage %d of %d (%d total)\n", results.Page, results.TotalPages, results.Total)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&mimeType, "type", "", "filter by MIME type (exact match)")
	cmd.Flags().Int64Var(&minSize, "min-size", 0, "filter to files at least this many bytes")
	cmd.Flags().Int64Var(&maxSize, "max-size", 0, "filter to files at most this many bytes")
	cmd.Flags().IntVar(&page, "page", 1, "page number")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "items per page")
	return cmd
}
