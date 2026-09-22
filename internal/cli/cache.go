package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/units"
)

func init() { register(newCacheCmd()) }

// cacheUnits formats sizes the way the rest of the CLI does by default.
const cacheUnits = units.Decimal

// defaultKeep is how many scans `cache prune` leaves behind.
const defaultKeep = 10

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and tidy the stored scans",
		Long: `Scans are stored under ~/Library/Application Support/storix/scans so the TUI
and --from-cache can render a previous scan without walking the disk again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newCacheListCmd(), newCachePruneCmd(), newCacheClearCmd(), newCachePathCmd())
	return cmd
}

func newCacheListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the stored scans, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := cacheStore()
			if err != nil {
				return err
			}
			return runCacheList(cmd.OutOrStdout(), s, time.Now())
		},
	}
}

func newCachePruneCmd() *cobra.Command {
	keep := defaultKeep
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete all but the newest scans",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := cacheStore()
			if err != nil {
				return err
			}
			if keep < 0 {
				return &ConfigError{Err: fmt.Errorf("--keep must not be negative, got %d", keep)}
			}
			return runCachePrune(cmd.OutOrStdout(), s, keep)
		},
	}
	cmd.Flags().IntVar(&keep, "keep", defaultKeep, "how many scans to keep")
	return cmd
}

func newCacheClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Delete every stored scan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := cacheStore()
			if err != nil {
				return err
			}
			return runCacheClear(cmd.OutOrStdout(), s)
		},
	}
}

func newCachePathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the directory the scans are stored in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := cacheStore()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), s.Dir)
			return err
		},
	}
}

// cacheStore resolves the per-user store; a store that cannot be located is a
// configuration problem, not a scan failure.
func cacheStore() (cache.Store, error) {
	s, err := cache.DefaultStore()
	if err != nil {
		return cache.Store{}, &ConfigError{Err: err}
	}
	return s, nil
}

func runCacheList(w io.Writer, s cache.Store, now time.Time) error {
	entries, err := s.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		_, err := fmt.Fprintf(w, "no stored scans in %s\n", s.Dir)
		return err
	}
	latest, _, _ := s.Latest()

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	cachef(tw, "WRITTEN\tAGE\tROOT\tSIZE\tSTATE\tPATH\n")
	for _, e := range entries {
		written, age := "-", "-"
		if !e.Meta.Written.IsZero() {
			written = e.Meta.Written.Local().Format("2006-01-02 15:04:05")
			age = cache.Age(now.Sub(e.Meta.Written))
		}
		root := e.Meta.Root
		if root == "" {
			root = "-"
		}
		cachef(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			written, age, root, cacheUnits.Bytes(e.Size), cacheState(e, latest), e.Path)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if latest != "" {
		_, err = fmt.Fprintf(w, "\nlatest: %s\n", filepath.Base(latest))
	}
	return err
}

// cacheState summarizes what is notable about an entry: whether it is what
// `latest` points at, whether the scan finished, and whether it can be read.
func cacheState(e cache.Entry, latest string) string {
	switch {
	case e.Err != nil:
		return "unreadable"
	case e.Meta.Incomplete && e.Path == latest:
		return "latest, incomplete"
	case e.Meta.Incomplete:
		return "incomplete"
	case e.Path == latest:
		return "latest"
	default:
		return "ok"
	}
}

func runCachePrune(w io.Writer, s cache.Store, keep int) error {
	removed, err := s.Prune(keep)
	printRemoved(w, removed)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "removed %s, kept up to %d in %s\n", plural(len(removed), "scan"), keep, s.Dir)
	return err
}

func runCacheClear(w io.Writer, s cache.Store) error {
	removed, err := s.Clear()
	printRemoved(w, removed)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "removed %s from %s\n", plural(len(removed), "scan"), s.Dir)
	return err
}

func printRemoved(w io.Writer, removed []string) {
	for _, p := range removed {
		cachef(w, "removed %s\n", p)
	}
}

// cachef writes a formatted line whose write error is reported by the caller's
// own final write, as the doctor report does.
func cachef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
