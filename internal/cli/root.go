// Package cli defines the storix command tree.
package cli

import (
	"context"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/report"
)

// subcommands is populated by register calls from each command's file so that
// files can be added without editing Root.
var subcommands []*cobra.Command

// register adds a subcommand to the root command tree.
func register(cmd *cobra.Command) { subcommands = append(subcommands, cmd) }

// rootOptions holds the flags of bare `storix`.
type rootOptions struct {
	roots       []string
	parallelism int
	threshold   string
	binary      bool
	system      bool
	debug       bool
	noCache     bool
	fromCache   bool
	codeRoots   []string
}

// Root builds the storix command tree.
func Root() *cobra.Command {
	o := &rootOptions{}
	root := &cobra.Command{
		Use:   "storix",
		Short: "Explain where your Mac's disk space went",
		Long: `storix walks the macOS data volume and produces a reconciled ledger of disk
usage: every byte lands in one bucket and the buckets sum to what the volume
reports as used. Phase one is strictly read-only.

With no subcommand storix opens the interactive browser, over the stored
scan when one is less than an hour old and over a fresh walk otherwise.
Redirected to a file or a pipe it prints the text report instead.`,
		Version:       BuildInfo(),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRoot(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	f := root.Flags()
	f.StringSliceVar(&o.roots, "roots", []string{mac.DataRoot}, "roots to walk")
	f.BoolVar(&o.system, "system", false, "include root-only system directories (needs sudo)")
	f.BoolVar(&o.binary, "binary", false, "format sizes in KiB/MiB/GiB instead of Finder's KB/MB/GB")
	f.IntVar(&o.parallelism, "parallelism", 0, "worker count (default min(2*NumCPU, 16))")
	f.StringVar(&o.threshold, "threshold", "64KB", "fold files smaller than this into their parent directory")
	f.BoolVar(&o.debug, "debug", false, "print timings and memory statistics")
	f.BoolVar(&o.noCache, "no-cache", false, "walk the disk and store nothing")
	f.BoolVar(&o.fromCache, "from-cache", false, "render the stored scan instead of walking the disk")
	f.StringSliceVar(&o.codeRoots, "code-roots", codeRootsDefault(), "directories whose projects the developer view lists")

	cmds := append([]*cobra.Command(nil), subcommands...)
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name() < cmds[j].Name() })
	root.AddCommand(cmds...)
	return root
}

// runRoot is bare `storix`: the interactive browser on a terminal, the text
// report when the output is going somewhere else.
//
// It is `storix scan` with the two output flags off, so the freshness rule,
// the cache wiring and the progress line are defined in exactly one place.
func runRoot(ctx context.Context, out, errOut io.Writer, o *rootOptions) error {
	so := &scanOptions{
		roots:       o.roots,
		parallelism: o.parallelism,
		threshold:   o.threshold,
		minSize:     defaultMinSize,
		top:         report.DefaultTop,
		depth:       report.DefaultDepth,
		system:      o.system,
		binary:      o.binary,
		debug:       o.debug,
		noCache:     o.noCache,
		fromCache:   o.fromCache,
		codeRoots:   o.codeRoots,
	}
	return runScan(ctx, out, errOut, so)
}
