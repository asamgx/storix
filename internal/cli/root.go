// Package cli defines the storix command tree.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Root builds the storix command tree.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "storix",
		Short: "Explain where your Mac's disk space went",
		Long: `storix walks the macOS data volume and produces a reconciled ledger of disk
usage: every byte lands in one bucket and the buckets sum to what the volume
reports as used. Phase one is strictly read-only.`,
		Version:       BuildInfo(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the storix version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "storix "+BuildInfo())
			return err
		},
	}
}
