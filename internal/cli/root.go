// Package cli defines the storix command tree.
package cli

import (
	"sort"

	"github.com/spf13/cobra"
)

// subcommands is populated by register calls from each command's file so that
// files can be added without editing Root.
var subcommands []*cobra.Command

// register adds a subcommand to the root command tree.
func register(cmd *cobra.Command) { subcommands = append(subcommands, cmd) }

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
	cmds := append([]*cobra.Command(nil), subcommands...)
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name() < cmds[j].Name() })
	root.AddCommand(cmds...)
	return root
}
