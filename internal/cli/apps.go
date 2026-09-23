package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
)

func init() { register(newAppsCmd()) }

// appsOptions holds the flags of `storix apps`.
type appsOptions struct {
	orphans   bool
	json      bool
	all       bool
	sortBy    string
	binary    bool
	fromCache bool
	debug     bool
	roots     []string
}

func newAppsCmd() *cobra.Command {
	o := &appsOptions{}
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "List installed applications, what each costs, and whose data is left behind",
		Long: `apps prints the application inventory: every installed application with its
footprint across the ledger's buckets, the Homebrew casks whose application is
gone, the owners whose application appears to have been removed, and the
directories nothing could be attributed to.

A footprint deliberately crosses the ledger's buckets. An application's bundle
is in Applications, its Library data is in App data and its caches may be in
Developer, so no single row of the ledger can say what it costs; this command
adds them up. The totals here are therefore not a partition of the disk and
must not be added to the ledger's.

It reuses a recent stored scan the way bare storix does, and --from-cache
renders the stored scan whatever its age.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApps(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&o.roots, "roots", []string{mac.DataRoot}, "roots to scan")
	f.BoolVar(&o.orphans, "orphans", false, "print only the sections about software that is no longer installed")
	f.BoolVar(&o.json, "json", false, "print the inventory as JSON instead")
	f.BoolVar(&o.all, "all", false, "list every row rather than the head of each section")
	f.StringVar(&o.sortBy, "sort", "size", "order the tables by size or name")
	f.BoolVar(&o.binary, "binary", false, "format sizes in KiB/MiB/GiB instead of Finder's KB/MB/GB")
	f.BoolVar(&o.fromCache, "from-cache", false, "render the stored scan instead of walking the disk")
	f.BoolVar(&o.debug, "debug", false, "print timings")
	return cmd
}

// runApps renders the inventory of one scan.
func runApps(ctx context.Context, out, errOut io.Writer, o *appsOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ro, cfg, err := o.resolve()
	if err != nil {
		return err
	}

	res, err := appsScan(ctx, errOut, cfg)
	if err != nil {
		return err
	}
	rep, ok := scan.Apps(res)
	if !ok {
		return &ConfigError{Err: fmt.Errorf(
			"this scan holds no application inventory; run `storix apps` without --from-cache to build one")}
	}
	rep.SortBy(o.sortBy == "name")

	if o.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	return report.AppsWith(out, res, ro, report.AppsOptions{
		All: o.all, Binary: o.binary, OrphansOnly: o.orphans,
	})
}

// appsScan produces the scan to render.
//
// It follows the same freshness rule as bare storix: a stored scan under an
// hour old from the same build over the same roots is reused, anything else
// is walked again. The one addition is the case this command has and the
// others do not: a cache written before the application inventory existed
// loads perfectly well and simply has no inventory in it, so it is walked
// again with a note rather than reported as an error.
func appsScan(ctx context.Context, errOut io.Writer, cfg scan.Config) (*scan.Result, error) {
	cached, err := cachedScan(cfg, errOut)
	if err != nil {
		return nil, err
	}
	if cached != nil {
		if _, ok := scan.Apps(cached); ok {
			return cached, nil
		}
		if cfg.FromCache {
			return cached, nil
		}
		_, _ = fmt.Fprintln(errOut,
			"storix: the stored scan predates the application inventory; scanning")
	}
	fresh := cfg
	fresh.FromCache = false
	return runOne(ctx, errOut, fresh)
}

// resolve turns the flags into the option structs.
func (o *appsOptions) resolve() (report.Options, scan.Config, error) {
	var ro report.Options
	var cfg scan.Config

	switch o.sortBy {
	case "size", "name":
	default:
		return ro, cfg, &ConfigError{Err: fmt.Errorf("--sort takes size or name, not %q", o.sortBy)}
	}

	u := units.Decimal
	if o.binary {
		u = units.Binary
	}
	ro = report.Options{
		Units:   u,
		Color:   useColor(),
		Top:     report.DefaultTop,
		Debug:   o.debug,
		Version: BuildInfo(),
	}
	cfg = scan.Config{
		Roots:     o.roots,
		Units:     u,
		Debug:     o.debug,
		FromCache: o.fromCache,
		Version:   BuildInfo(),
	}
	return ro, cfg, nil
}
