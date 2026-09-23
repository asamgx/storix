package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
)

func init() { register(newDevCmd()) }

// devOptions holds the flags of `storix dev`.
type devOptions struct {
	roots      []string
	projects   bool
	json       bool
	binary     bool
	fromCache  bool
	scan       bool
	debug      bool
	disableDet []string
}

func newDevCmd() *cobra.Command {
	o := &devOptions{}
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Show what your toolchains keep, and what they would give back",
		Long: `dev prints the Developer section of the report on its own: every tool
detector's rows, grouped the way the full report groups them, and the source
projects under your code roots with their build artifacts and last activity.

It reuses a recent stored scan the way bare storix does: a scan under an hour
old from the same build over the same roots is rendered as is, and anything
staler triggers a fresh walk. --from-cache renders the stored scan whatever
its age instead, and --scan always walks the disk.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDev(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&o.roots, "roots", []string{mac.DataRoot}, "roots to scan")
	f.BoolVar(&o.projects, "projects", false, "print only the projects subsection")
	f.BoolVar(&o.json, "json", false, "print the developer section as JSON instead")
	f.BoolVar(&o.binary, "binary", false, "format sizes in KiB/MiB/GiB instead of Finder's KB/MB/GB")
	f.BoolVar(&o.fromCache, "from-cache", false, "render the stored scan instead of walking the disk, whatever its age")
	f.BoolVar(&o.scan, "scan", false, "always walk the disk instead of reusing a stored scan")
	f.BoolVar(&o.debug, "debug", false, "print timings")
	f.StringArrayVar(&o.disableDet, "disable-detector", nil,
		"switch off one tool detector by name; repeatable (the section still lists it, as disabled)")
	return cmd
}

// runDev renders the Developer section of one scan.
func runDev(ctx context.Context, out, errOut io.Writer, o *devOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ro, cfg, err := o.resolve()
	if err != nil {
		return err
	}

	res, err := devScan(ctx, errOut, cfg, o.scan)
	if err != nil {
		return err
	}

	if o.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(devJSONDoc(res))
	}
	if o.projects {
		return devPrintProjects(out, res, ro)
	}
	return report.Developer(out, res, ro)
}

// devScan produces the scan to render, following the same freshness rule as
// bare storix (D25): a recent stored scan is reused, anything stale or
// missing is walked. --scan skips straight to a fresh walk, and cachedScan
// and runOne are the same helpers scan.go's own commands use, not a copy of
// their logic.
func devScan(ctx context.Context, errOut io.Writer, cfg scan.Config, forceScan bool) (*scan.Result, error) {
	if !forceScan {
		cached, err := cachedScan(cfg, errOut)
		if err != nil {
			return nil, err
		}
		if cached != nil {
			return cached, nil
		}
	}
	fresh := cfg
	fresh.FromCache = false
	return runOne(ctx, errOut, fresh)
}

// resolve turns the flags into the option structs.
func (o *devOptions) resolve() (report.Options, scan.Config, error) {
	var ro report.Options
	var cfg scan.Config

	if o.fromCache && o.scan {
		return ro, cfg, &ConfigError{Err: fmt.Errorf("choose either --from-cache or --scan, not both")}
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
		Roots:             o.roots,
		Units:             u,
		Debug:             o.debug,
		FromCache:         o.fromCache,
		DisabledDetectors: o.disableDet,
		Version:           BuildInfo(),
	}
	return ro, cfg, nil
}

// devAllProjects gathers every detector's projects, largest artifact bytes
// first, the same order report.Developer's own projects table uses; it is a
// small reimplementation rather than a call into the report package, whose
// equivalent is unexported.
func devAllProjects(res *scan.Result) []detect.Project {
	var out []detect.Project
	for _, st := range res.Detectors {
		out = append(out, res.Summaries[st.Name].Projects...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ArtifactBytes > out[j].ArtifactBytes })
	return out
}

// devPrintProjects prints --projects' table on its own: the source projects
// under the code roots, without the tool groups above them.
func devPrintProjects(w io.Writer, res *scan.Result, ro report.Options) error {
	projects := devAllProjects(res)
	if len(projects) == 0 {
		_, err := fmt.Fprintln(w, "no projects found under the configured code roots")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", "ARTIFACTS", "VCS", "PROJECT", "LAST ACTIVITY")
	for _, p := range projects {
		vcs := ""
		if p.VCS {
			vcs = "git"
		}
		last := ""
		if !p.LastActivity.IsZero() {
			last = p.LastActivity.UTC().Format(time.DateOnly)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", ro.Units.Bytes(p.ArtifactBytes), vcs, p.Root, last)
	}
	return tw.Flush()
}

// devDoc is the --json document: schema 1, additive, built directly from
// Result.Detectors and Result.Summaries rather than from the text report's
// bucket-filtered rows, so it is a complete mechanical export of what every
// detector found.
type devDoc struct {
	Schema    int        `json:"schema"`
	Developer devSection `json:"developer"`
}

// devSection is the "developer" key of devDoc.
type devSection struct {
	Detectors []devDetector    `json:"detectors"`
	Projects  []detect.Project `json:"projects,omitempty"`
}

// devDetector is one detector's row in the --json document.
type devDetector struct {
	Name        string        `json:"name"`
	State       string        `json:"state"`
	Reason      string        `json:"reason,omitempty"`
	DurationNS  int64         `json:"duration_ns"`
	Reclaimable int64         `json:"reclaimable,omitempty"`
	Tools       []detect.Tool `json:"tools,omitempty"`
}

// devJSONDoc builds the --json document from one scan's result.
func devJSONDoc(res *scan.Result) devDoc {
	doc := devDoc{Schema: report.SchemaVersion}
	doc.Developer.Detectors = make([]devDetector, 0, len(res.Detectors))
	for _, st := range res.Detectors {
		sum := res.Summaries[st.Name]
		doc.Developer.Detectors = append(doc.Developer.Detectors, devDetector{
			Name:        st.Name,
			State:       st.State.String(),
			Reason:      st.Reason,
			DurationNS:  st.Duration.Nanoseconds(),
			Reclaimable: sum.Reclaimable,
			Tools:       sum.Tools,
		})
	}
	doc.Developer.Projects = devAllProjects(res)
	return doc
}
