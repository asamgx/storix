package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/tui"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// scanOptions holds the flags of `storix scan`.
type scanOptions struct {
	roots       []string
	parallelism int
	threshold   string
	minSize     string
	top         int
	depth       int
	full        bool
	report      bool
	json        bool
	system      bool
	binary      bool
	debug       bool
	noCache     bool
	fromCache   bool
}

// defaultMinSize is the smallest node the JSON tree carries by default.
const defaultMinSize = "10MB"

func init() { register(newScanCmd()) }

func newScanCmd() *cobra.Command {
	o := &scanOptions{}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Walk a volume and report where its bytes are",
		Long: `scan walks the macOS data volume and prints a reconciled ledger: the bytes
it could see, the purgeable space the system reports, and the residual
between those and what the volume calls used.

The default output is the text report. --json emits the same scan as a
versioned document, with the tree limited by --depth and --min-size unless
--full is given, because the unlimited tree is hundreds of megabytes.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScan(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&o.roots, "roots", []string{mac.DataRoot}, "roots to walk")
	f.BoolVar(&o.report, "report", false, "print the text report (the default)")
	f.BoolVar(&o.json, "json", false, "print the scan as JSON instead")
	f.BoolVar(&o.system, "system", false, "include root-only system directories (needs sudo)")
	f.BoolVar(&o.binary, "binary", false, "format sizes in KiB/MiB/GiB instead of Finder's KB/MB/GB")
	f.IntVar(&o.parallelism, "parallelism", 0, "worker count (default min(2*NumCPU, 16))")
	f.StringVar(&o.threshold, "threshold", "64KB", "fold files smaller than this into their parent directory")
	f.IntVar(&o.top, "top", report.DefaultTop, "how many directories to list; 0 lists none")
	f.IntVar(&o.depth, "depth", report.DefaultDepth, "how deep the JSON tree goes; 0 emits the root alone")
	f.StringVar(&o.minSize, "min-size", defaultMinSize, "smallest node in the JSON tree; 0 includes everything")
	f.BoolVar(&o.full, "full", false, "drop the JSON tree limits and emit every node")
	f.BoolVar(&o.debug, "debug", false, "print timings and memory statistics")
	f.BoolVar(&o.noCache, "no-cache", false, "walk the disk and store nothing")
	f.BoolVar(&o.fromCache, "from-cache", false, "render the stored scan instead of walking the disk")
	return cmd
}

func runScan(ctx context.Context, out, errOut io.Writer, o *scanOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	opts, cfg, err := o.resolve()
	if err != nil {
		return err
	}
	if o.system && os.Geteuid() != 0 {
		_, _ = fmt.Fprintln(errOut, "storix: --system needs root; run `sudo storix scan --system` to include root-only directories")
	}

	// With no output chosen and a terminal to draw on, `storix scan` is the
	// interactive interface; a pipe or a redirect still gets the report,
	// which is what a script asked for.
	if o.interactive(out) {
		return runTUI(ctx, errOut, cfg)
	}

	for i, root := range o.roots {
		if i > 0 && !o.json {
			_, _ = fmt.Fprintln(out)
		}
		c := cfg
		c.Roots = []string{root}
		res, err := runOne(ctx, errOut, c)
		if err != nil {
			return err
		}
		if err := render(out, res, opts, o); err != nil {
			return err
		}
		reportSoftErrors(errOut, res, o)
	}
	// A cancelled scan still printed its partial report; the exit code is
	// what tells a script the numbers are a lower bound.
	return ctx.Err()
}

// resolve turns the flags into the two option structs, rejecting the
// combinations that cannot mean anything.
func (o *scanOptions) resolve() (report.Options, scan.Config, error) {
	var ro report.Options
	var cfg scan.Config

	if o.report && o.json {
		return ro, cfg, &ConfigError{Err: fmt.Errorf("choose either --report or --json, not both")}
	}
	if o.noCache && o.fromCache {
		return ro, cfg, &ConfigError{Err: fmt.Errorf("choose either --no-cache or --from-cache, not both")}
	}
	threshold, err := units.Parse(o.threshold)
	if err != nil {
		return ro, cfg, &ConfigError{Err: fmt.Errorf("invalid --threshold: %w", err)}
	}
	minSize, err := units.Parse(o.minSize)
	if err != nil {
		return ro, cfg, &ConfigError{Err: fmt.Errorf("invalid --min-size: %w", err)}
	}
	if o.top < 0 {
		return ro, cfg, &ConfigError{Err: fmt.Errorf("the --top count must not be negative")}
	}
	if o.depth < 0 {
		return ro, cfg, &ConfigError{Err: fmt.Errorf("the --depth limit must not be negative")}
	}
	if o.parallelism < 0 {
		return ro, cfg, &ConfigError{Err: fmt.Errorf("the --parallelism worker count must not be negative")}
	}

	u := units.Decimal
	if o.binary {
		u = units.Binary
	}
	ro = report.Options{
		Units:   u,
		Depth:   sentinel(o.depth),
		MinSize: sentinel64(minSize),
		Full:    o.full,
		Color:   useColor(),
		Top:     sentinel(o.top),
		Version: BuildInfo(),
	}
	cfg = scan.Config{
		Roots:              o.roots,
		Parallelism:        o.parallelism,
		SmallFileThreshold: threshold,
		System:             o.system,
		Units:              u,
		Debug:              o.debug,
		NoCache:            o.noCache,
		FromCache:          o.fromCache,
		Version:            BuildInfo(),
	}
	return ro, cfg, nil
}

// interactive reports whether this invocation should open the TUI: no output
// format was asked for, one root was named, and a person is watching.
func (o *scanOptions) interactive(out io.Writer) bool {
	return !o.report && !o.json && len(o.roots) == 1 && isTerminal(out)
}

// sentinel maps the flag value zero, which the user means as "none", onto the
// negative value the report package reads as "none"; the report reads zero as
// "use the default", which is what an unset field must mean.
func sentinel(n int) int {
	if n == 0 {
		return -1
	}
	return n
}

// sentinel64 is sentinel for a byte count.
func sentinel64(n int64) int64 {
	if n == 0 {
		return -1
	}
	return n
}

// runTUI opens the interactive interface over a fresh cached scan when there
// is one, and over a scan it starts when there is not.
func runTUI(ctx context.Context, errOut io.Writer, cfg scan.Config) error {
	initial, err := cachedScan(cfg, errOut)
	if err != nil {
		return err
	}
	return tui.Run(ctx, withCache(cfg, errOut), initial)
}

// cachedScan returns the stored scan to render instead of walking the disk,
// or nil to walk.
//
// A stale cache is not an error: bare storix reuses a scan under an hour old
// from the same build over the same roots (D25) and rescans otherwise, and
// --debug says which rule sent it back to the disk. --from-cache asks for
// the stored scan whatever its age, so an empty store is then a failure.
func cachedScan(cfg scan.Config, errOut io.Writer) (*scan.Result, error) {
	if cfg.NoCache {
		return nil, nil
	}
	res, ok, err := scan.LoadLatest(cfg)
	switch {
	case ok:
		return res, nil
	case err == nil:
		return nil, nil
	case cfg.FromCache:
		return nil, &ConfigError{Err: err}
	}
	var stale *scan.StaleError
	if errors.As(err, &stale) {
		if cfg.Debug {
			_, _ = fmt.Fprintf(errOut, "storix: scanning: %s\n", stale.Reason)
		}
		return nil, nil
	}
	_, _ = fmt.Fprintf(errOut, "storix: the stored scan could not be read (%v); scanning\n", err)
	return nil, nil
}

// withCache points the scan at the store. A store that cannot be located
// costs the cache, not the scan: a machine whose home directory is not
// readable can still be told where its bytes went.
func withCache(cfg scan.Config, errOut io.Writer) scan.Config {
	out, err := scan.WithCache(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "storix: this scan will not be cached (%v)\n", err)
		return cfg
	}
	return out
}

// runOne scans a single root, showing progress on the terminal while it runs.
func runOne(ctx context.Context, errOut io.Writer, cfg scan.Config) (*scan.Result, error) {
	if cfg.FromCache {
		res, err := cachedScan(cfg, errOut)
		if err != nil {
			return nil, err
		}
		if res != nil {
			return res, nil
		}
	}
	cfg = withCache(cfg, errOut)

	var events chan walk.Event
	done := make(chan struct{})
	if isTerminal(errOut) {
		events = make(chan walk.Event, 1)
		cfg.Events = events
		go func() { defer close(done); showProgress(errOut, events) }()
	} else {
		close(done)
	}

	res, err := scan.Run(ctx, cfg)
	if events != nil {
		// Walk sends its DoneEvent only when it succeeds, so closing the
		// channel is what ends the consumer either way.
		close(events)
	}
	<-done
	if err != nil {
		return nil, &ConfigError{Err: err}
	}
	return res, nil
}

// progressWidth is how much of the terminal the progress line may use.
const progressWidth = 110

// showProgress draws the walk's counters over one line of the terminal and
// erases it when the walk ends.
func showProgress(w io.Writer, events <-chan walk.Event) {
	u := units.Decimal
	drawn := false
	for e := range events {
		ev, ok := e.(walk.ProgressEvent)
		if !ok {
			continue
		}
		drawn = true
		line := fmt.Sprintf("%6.1fs  %9s dirs  %10s files  %11s  %s",
			ev.Elapsed.Seconds(), fmtCount(ev.Dirs), fmtCount(ev.Files),
			u.Bytes(int64(ev.Bytes)), mac.DisplayPath(ev.Current))
		_, _ = fmt.Fprintf(w, "\r%-*.*s", progressWidth, progressWidth, line)
	}
	if drawn {
		_, _ = fmt.Fprintf(w, "\r%*s\r", progressWidth, "")
	}
}

// fmtCount groups digits so a seven-figure file count is readable while it
// is still moving.
func fmtCount(n uint64) string {
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// render writes the report in the requested form.
func render(out io.Writer, res *scan.Result, ro report.Options, o *scanOptions) error {
	if o.json {
		return report.JSON(out, res, ro)
	}
	if err := report.Text(out, res, ro); err != nil {
		return err
	}
	if o.debug {
		printDebug(out, res)
	}
	return nil
}

// printDebug adds the stage timings and the heap profile the memory budget
// is judged against.
func printDebug(out io.Writer, res *scan.Result) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	u := units.Decimal
	_, _ = fmt.Fprintln(out, "\nDEBUG")
	_, _ = fmt.Fprintf(out, "  timing   facts %s, walk %s, finish %s, ledger %s, persist %s, total %s\n",
		dur(res.Timing.Facts), dur(res.Timing.Walk), dur(res.Timing.Finish),
		dur(res.Timing.Ledger), dur(res.Timing.Persist), dur(res.Timing.Total))
	_, _ = fmt.Fprintf(out, "  memory   %s heap, %s total allocated, %s from the OS, %d GCs\n",
		u.Bytes(int64(ms.HeapAlloc)), u.Bytes(int64(ms.TotalAlloc)), u.Bytes(int64(ms.Sys)), ms.NumGC)
	_, _ = fmt.Fprintf(out, "  workers  %d\n", res.Tree.Opts.Parallelism)
	if res.CachePath != "" {
		_, _ = fmt.Fprintf(out, "  cache    %s\n", res.CachePath)
	}
}

// dur formats a stage timing.
func dur(d time.Duration) string {
	if d >= time.Second {
		return d.Round(10 * time.Millisecond).String()
	}
	return d.Round(time.Millisecond).String()
}

// reportSoftErrors names the best-effort steps that failed. They never stop a
// scan, but a silent failure would leave the ledger quietly less trustworthy
// than it looks.
func reportSoftErrors(errOut io.Writer, res *scan.Result, o *scanOptions) {
	if res.FinishErr != nil {
		_, _ = fmt.Fprintf(errOut, "storix: no closing space reading (%v); the drift tolerance is zero\n", res.FinishErr)
	}
	if res.PersistErr != nil {
		_, _ = fmt.Fprintf(errOut, "storix: the scan was not cached (%v)\n", res.PersistErr)
	}
	if o.debug && res.DatalessErr != nil {
		_, _ = fmt.Fprintf(errOut, "storix: dataless materialization stays at its default (%v)\n", res.DatalessErr)
	}
}

// useColor reports whether the text report should be styled: a terminal on
// standard output, and NO_COLOR unset.
func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(os.Stdout)
}

// isTerminal reports whether w is a character device, which is the
// dependency-free way to ask whether a human is reading.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
