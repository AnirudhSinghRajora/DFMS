package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newAdminCommand builds the `admin` command group. Its subcommands require an
// administrator role; non-admin callers receive a clear authorization error.
func newAdminCommand(env *appEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administrative commands (require an admin role)",
	}
	cmd.AddCommand(newAdminNodesCommand(env))
	return cmd
}

func newAdminNodesCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "nodes",
		Short: "List storage nodes in the cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			nodes, err := sess.client.ListNodes(cmd.Context())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, nodes); handled || herr != nil {
				return herr
			}
			if len(nodes.Nodes) == 0 {
				if nodes.Message != "" {
					fmt.Fprintln(out, nodes.Message)
				} else {
					fmt.Fprintln(out, "No storage nodes.")
				}
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, env.style.HeaderCell("ID\tNAME\tENDPOINT\tSTATUS\tUSED\tCAPACITY"))
			for i := range nodes.Nodes {
				n := &nodes.Nodes[i]
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					n.ID, n.Name, n.Endpoint, nodeStatus(env.style, n.Status),
					formatBytes(n.Used), formatBytes(n.Capacity))
			}
			return tw.Flush()
		},
	}
}

// nodeStatus colors a storage node's status: healthy states green, unhealthy
// states red, and anything else left unstyled. Coloring is applied with cell
// escapes so it is safe inside a tabwriter.
func nodeStatus(st *styler, status string) string {
	switch strings.ToLower(status) {
	case "online", "healthy", "active", "up", "ready":
		return st.GreenCell(status)
	case "offline", "unhealthy", "down", "error", "failed":
		return st.RedCell(status)
	default:
		return status
	}
}
