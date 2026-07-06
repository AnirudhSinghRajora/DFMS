package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newStorageCommand builds the `storage` command group.
func newStorageCommand(env *appEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Inspect storage usage",
	}
	cmd.AddCommand(newStorageUsageCommand(env))
	return cmd
}

func newStorageUsageCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "usage",
		Short: "Show your storage usage against your quota",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			usage, err := sess.client.StorageUsage(cmd.Context())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, usage); handled || herr != nil {
				return herr
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintf(tw, "Used:\t%s\n", formatBytes(usage.Used))
			fmt.Fprintf(tw, "Quota:\t%s\n", formatBytes(usage.Quota))
			fmt.Fprintf(tw, "Available:\t%s\n", formatBytes(usage.Available))
			fmt.Fprintf(tw, "Used:\t%.1f%%\n", usage.UsedPct)
			return tw.Flush()
		},
	}
}
