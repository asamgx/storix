package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// scanOptions holds the flags of `storix scan`.
type scanOptions struct {
	roots       []string
	parallelism int
	threshold   string
	top         int
	report      bool
	debug       bool
}

func init() { register(newScanCmd()) }

func newScanCmd() *cobra.Command {
	o := &scanOptions{}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Walk a volume and report where its bytes are",
		Long: `scan walks one or more roots and prints allocated sizes.

This is the milestone-2 form of the command: it exercises the walker and
prints raw totals. The reconciled ledger, the cache and JSON output arrive in
later milestones.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScan(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&o.roots, "roots", []string{dataRoot}, "roots to walk")
	f.IntVar(&o.parallelism, "parallelism", 0, "worker count (default min(2*NumCPU, 16))")
	f.StringVar(&o.threshold, "threshold", "64KB", "aggregate files smaller than this into their parent")
	f.IntVar(&o.top, "top", 25, "how many directories to list")
	f.BoolVar(&o.report, "report", true, "print the text report")
	f.BoolVar(&o.debug, "debug", false, "print memory statistics, timings and live progress")
	return cmd
}

// dataRoot is the macOS data volume, the default scan root.
//
// TODO(M4): use mac.DataRoot.
const dataRoot = "/System/Volumes/Data"

func runScan(ctx context.Context, out, errOut io.Writer, o *scanOptions) error {
	threshold, err := units.Parse(o.threshold)
	if err != nil {
		return &ConfigError{Err: err}
	}
	if o.top < 0 {
		return &ConfigError{Err: fmt.Errorf("--top must not be negative")}
	}
	mounts, err := readMounts()
	if err != nil {
		return fmt.Errorf("reading the mount table: %w", err)
	}

	for i, root := range o.roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return &ConfigError{Err: fmt.Errorf("%s: %w", root, err)}
		}
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		if err := scanRoot(ctx, out, errOut, o, abs, threshold, mounts); err != nil {
			return err
		}
	}
	return nil
}

func scanRoot(ctx context.Context, out, errOut io.Writer, o *scanOptions, root string, threshold int64, mounts mountSet) error {
	opts := walk.Options{
		Root:               root,
		Parallelism:        o.parallelism,
		SmallFileThreshold: threshold,
		Mounts:             mounts,
	}

	var events chan walk.Event
	consumed := make(chan struct{})
	if o.debug {
		events = make(chan walk.Event, 1)
		opts.Events = events
		opts.ProgressInterval = 250 * time.Millisecond
		go func() {
			defer close(consumed)
			for e := range events {
				switch ev := e.(type) {
				case walk.ProgressEvent:
					fmt.Fprintf(errOut, "\r%-100.100s",
						fmt.Sprintf("%6.1fs  %8d dirs  %9d files  %10s  %s",
							ev.Elapsed.Seconds(), ev.Dirs, ev.Files,
							units.Decimal.Bytes(int64(ev.Bytes)), walk.DisplayPath(ev.Current)))
				case walk.DoneEvent:
					fmt.Fprintf(errOut, "\r%-100.100s\r", "")
					return
				}
			}
		}()
	} else {
		close(consumed)
	}

	started := time.Now()
	tree, err := walk.Walk(ctx, opts)
	elapsed := time.Since(started)
	if events != nil {
		// Walk sends DoneEvent when it succeeds and nothing when it fails, so
		// closing the channel is what ends the consumer in both cases.
		close(events)
	}
	<-consumed
	if err != nil {
		return &ConfigError{Err: err}
	}
	if !o.report {
		return nil
	}
	printReport(out, tree, elapsed, o)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func printReport(out io.Writer, tree *walk.Tree, elapsed time.Duration, o *scanOptions) {
	u := units.Decimal
	root := tree.Root
	fmt.Fprintf(out, "root       %s\n", walk.DisplayPath(root.Path()))
	fmt.Fprintf(out, "allocated  %s (%d bytes)\n", u.Bytes(root.Bytes), root.Bytes)
	fmt.Fprintf(out, "apparent   %s\n", u.Bytes(root.Apparent))
	fmt.Fprintf(out, "files      %d in %d directories (%d nodes retained)\n", root.Files, root.Dirs, len(tree.Nodes))
	fmt.Fprintf(out, "hard links %d groups, %s not double counted\n", tree.LinkGroups, u.Bytes(int64(tree.LinkBytesSaved)))
	fmt.Fprintf(out, "errors     %d unreadable paths, %d vanished\n", len(tree.Errors), tree.Vanished)
	if tree.Incomplete {
		_, _ = fmt.Fprintln(out, "status     INCOMPLETE (cancelled)")
	}

	if len(tree.SkippedMounts) > 0 {
		fmt.Fprintf(out, "\nskipped mounts (%d)\n", len(tree.SkippedMounts))
		for _, m := range tree.SkippedMounts {
			fmt.Fprintf(out, "  %-52s %-8s %s\n", walk.DisplayPath(m.Path), m.FSType, m.From)
		}
	}
	if len(tree.SkipListed) > 0 {
		fmt.Fprintf(out, "\nskip-listed (%d)\n", len(tree.SkipListed))
		for _, p := range tree.SkipListed {
			fmt.Fprintf(out, "  %s\n", walk.DisplayPath(p))
		}
	}

	if o.top > 0 {
		dirs := topDirs(tree.Root, 2, o.top)
		fmt.Fprintf(out, "\ntop %d directories at depth <= 2\n", len(dirs))
		for _, n := range dirs {
			fmt.Fprintf(out, "  %10s  %6s  %s\n",
				u.Bytes(n.Bytes), units.Percent(n.Bytes, root.Bytes), walk.DisplayPath(n.Path()))
		}
	}

	if len(tree.Errors) > 0 {
		shown := min(len(tree.Errors), 20)
		fmt.Fprintf(out, "\nunreadable (first %d of %d)\n", shown, len(tree.Errors))
		for _, e := range tree.Errors[:shown] {
			fmt.Fprintf(out, "  %-10s %-6s %s\n", e.Class, e.Op, walk.DisplayPath(e.Path))
		}
	}

	fmt.Fprintf(out, "\nelapsed    %s\n", elapsed.Round(time.Millisecond))
	if o.debug {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		fmt.Fprintf(out, "heap       %s alloc, %s sys, %d GCs\n",
			u.Bytes(int64(ms.HeapAlloc)), u.Bytes(int64(ms.Sys)), ms.NumGC)
		fmt.Fprintf(out, "workers    %d\n", tree.Opts.Parallelism)
	}
}

// topDirs returns the largest directories no deeper than maxDepth below root.
func topDirs(root *walk.Node, maxDepth, n int) []*walk.Node {
	var found []*walk.Node
	var visit func(*walk.Node, int)
	visit = func(nd *walk.Node, depth int) {
		if depth > 0 {
			found = append(found, nd)
		}
		if depth == maxDepth {
			return
		}
		for _, c := range nd.Children {
			if c.IsDir() {
				visit(c, depth+1)
			}
		}
	}
	visit(root, 0)
	sort.Slice(found, func(i, j int) bool {
		if found[i].Bytes != found[j].Bytes {
			return found[i].Bytes > found[j].Bytes
		}
		return found[i].Path() < found[j].Path()
	})
	if len(found) > n {
		found = found[:n]
	}
	return found
}

// mountSet is the set of mount points, used as the walker's mount guard.
//
// TODO(M4): replace with volume.ReadMountTable, which also carries per-volume
// statfs numbers and container grouping.
type mountSet map[string]mountEntry

type mountEntry struct{ fsType, from string }

// IsMountPoint implements walk.MountChecker.
func (m mountSet) IsMountPoint(path string) bool { _, ok := m[path]; return ok }

// MountInfo implements walk.MountDescriber.
func (m mountSet) MountInfo(path string) (string, string, bool) {
	e, ok := m[path]
	return e.fsType, e.from, ok
}

// readMounts reads the mount table. Every mount point other than the scan root
// is a different volume, and st_dev cannot tell them apart on an APFS volume
// group, so this list is the only thing that stops the walk double counting a
// nested mount such as the autofs /home or an NFS export inside the home
// directory.
func readMounts() (mountSet, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	set := make(mountSet, n)
	for i := range buf[:n] {
		on := cstr(buf[i].Mntonname[:])
		e := mountEntry{
			fsType: cstr(buf[i].Fstypename[:]),
			from:   cstr(buf[i].Mntfromname[:]),
		}
		set[on] = e
		// The mount table names data-volume mount points through their
		// firmlink ("/Users/x/OrbStack"), but a walk of the data volume sees
		// them as "/System/Volumes/Data/Users/x/OrbStack". Without both forms
		// the guard misses the nested NFS and autofs mounts and counts their
		// bytes twice.
		if on != "/" && !strings.HasPrefix(on, dataRoot) {
			set[dataRoot+on] = e
		}
	}
	return set, nil
}

// cstr trims a NUL-terminated C string out of a fixed-size byte array.
func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
