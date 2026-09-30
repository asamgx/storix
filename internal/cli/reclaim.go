package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/reclaim"
	"github.com/asamgx/storix/internal/report"
)

func init() { register(newReclaimCmd()) }

// reclaimOptions holds the flags of `storix reclaim`.
type reclaimOptions struct {
	dev        devOptions
	plan       bool
	all        bool
	tiers      []string
	staleAfter string
}

func newReclaimCmd() *cobra.Command {
	o := &reclaimOptions{}
	cmd := &cobra.Command{
		Use:   "reclaim",
		Short: "Plan what could be freed, with each tool's own command and what it costs",
		Long: `reclaim prints the reclaim plan: every directory storix could suggest
freeing, grouped by what freeing it costs.

  safe         nothing beyond a slower first use (the detector vouches for it)
  redownload   the tool or application downloads it again when needed
  reinstall    build output of a project untouched for --stale-after
  check        storix cannot vouch for it: leftovers, unknowns, the Trash
  in-use       listed to account for it, never suggested
  never        your data or configuration

Each item names the tool's own command where there is one, and what running
it costs. Bytes are counted once across the plan; the figures a tool reports
itself (brew cleanup, docker) are shown apart because they overlap the walk.
Lines of your Brewfiles and shell files that would reinstall removed software
are listed too.

The plan is read-only: storix changes nothing, and running a plan is not
available yet. Scans are reused as bare storix reuses them.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReclaim(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.plan, "plan", false, "print the plan (the only mode this release has)")
	f.BoolVar(&o.all, "all", false, "list every in-use and never item rather than the largest few")
	f.StringArrayVar(&o.tiers, "tier", nil, "show only this tier; repeatable (safe, redownload, reinstall, check, in-use, never)")
	f.StringVar(&o.staleAfter, "stale-after", "30d", "how long a project must go untouched for its build output to count as stale (\"30d\" or a Go duration)")
	d := &o.dev
	f.StringSliceVar(&d.roots, "roots", []string{mac.DataRoot}, "roots to scan")
	f.BoolVar(&d.json, "json", false, "print the plan as JSON instead")
	f.BoolVar(&d.binary, "binary", false, "format sizes in KiB/MiB/GiB instead of Finder's KB/MB/GB")
	f.BoolVar(&d.fromCache, "from-cache", false, "plan from the stored scan, whatever its age")
	f.BoolVar(&d.scan, "scan", false, "always walk the disk instead of reusing a stored scan")
	f.BoolVar(&d.debug, "debug", false, "print timings")
	f.StringArrayVar(&d.disableDet, "disable-detector", nil, "switch off one tool detector by name; repeatable")
	f.StringSliceVar(&d.codeRoots, "code-roots", codeRootsDefault(), codeRootsUsage)
	return cmd
}

// reclaimDoc is the --json document.
type reclaimDoc struct {
	Schema int           `json:"schema"`
	Plan   *reclaim.Plan `json:"plan"`
}

// runReclaim builds and prints the plan for one scan.
func runReclaim(ctx context.Context, out, errOut io.Writer, o *reclaimOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	stale, err := parseStaleAfter(o.staleAfter)
	if err != nil {
		return &ConfigError{Err: err}
	}
	tiers, err := parseTiers(o.tiers)
	if err != nil {
		return &ConfigError{Err: err}
	}
	ro, cfg, err := o.dev.resolve()
	if err != nil {
		return err
	}
	res, err := devScan(ctx, errOut, cfg, o.dev.scan)
	if err != nil {
		return err
	}

	p := reclaim.Build(res, reclaim.Options{StaleAfter: stale})
	if o.dev.json {
		if len(tiers) > 0 {
			p.Items = p.Filter(tiers)
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(reclaimDoc{Schema: report.SchemaVersion, Plan: p})
	}
	if err := report.Reclaim(out, res, p, ro, report.PlanView{Tiers: tiers, All: o.all}); err != nil {
		return err
	}
	if !o.plan {
		_, err = fmt.Fprintln(errOut, "\nstorix only plans in this release: nothing has been changed, and running a plan is not available yet")
	}
	return err
}

// parseStaleAfter reads "30d", "7d" or any Go duration.
func parseStaleAfter(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("--stale-after %q: want a number of days such as 30d, or a duration", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--stale-after %q: want a number of days such as 30d, or a duration", s)
	}
	return d, nil
}

// parseTiers reads the --tier flags.
func parseTiers(names []string) ([]reclaim.Tier, error) {
	var out []reclaim.Tier
	for _, n := range names {
		for _, part := range strings.Split(n, ",") {
			t, err := detect.ParseTier(strings.TrimSpace(part))
			if err != nil || t == detect.TierUnset {
				return nil, fmt.Errorf("--tier %q: want one of safe, redownload, reinstall, check, in-use, never", part)
			}
			out = append(out, t)
		}
	}
	return out, nil
}
