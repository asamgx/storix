package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/xcode"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/volume"
)

func init() { register(newDoctorCmd()) }

func newDoctorCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report what storix can and cannot see on this Mac",
		Long: `doctor prints the environment a scan would run in: whether this build can
turn off dataless materialization and read purgeable space, which terminal is
running, whether Full Disk Access is granted, the container and volume space
numbers, the mounts nested inside the scan root, and local snapshots.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runDoctor(ctx, cmd.OutOrStdout(), root)
		},
	}
	cmd.Flags().StringVar(&root, "root", mac.DataRoot, "scan root to report on")
	return cmd
}

// doctorUnits formats bytes the way the rest of the CLI does by default.
const doctorUnits = units.Decimal

// volAttrSpikeTolerance is the difference below which getattrlist and statfs
// are considered to agree: 1 MiB, far above the few blocks that separate two
// readings taken microseconds apart.
const volAttrSpikeTolerance = 1 << 20

func runDoctor(ctx context.Context, w io.Writer, root string) error {
	root = mac.ScanPath(root)

	// Set the policy before collecting, so the reading below shows its effect.
	setErr := mac.SetDatalessMaterializationOff()

	facts, err := volume.Collect(ctx, root)
	if err != nil {
		return fmt.Errorf("collecting volume facts: %w", err)
	}

	doctorBuild(w, root)
	doctorDataless(w, setErr, facts)
	doctorPurgeable(w, facts)
	doctorVolAttrs(w, facts)
	doctorPermissions(w, facts)
	doctorContainer(w, facts)
	doctorNested(w, facts)
	doctorSnapshots(w, facts)
	doctorPaths(w)
	doctorTools(ctx, w)
	return nil
}

// doctorf writes a formatted line, ignoring the write error: doctor prints a
// report and a failed write is reported by the caller's own writer.
func doctorf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// doctorSection writes a heading and returns a tabwriter for its rows. Callers must
// flush it.
func doctorSection(w io.Writer, title string) *tabwriter.Writer {
	doctorf(w, "\n%s\n", title)
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

func doctorRow(tw *tabwriter.Writer, label string, valuef string, args ...any) {
	doctorf(tw, "  %s\t%s\n", label, fmt.Sprintf(valuef, args...))
}

func doctorYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func doctorBuild(w io.Writer, root string) {
	tw := doctorSection(w, "build")
	doctorRow(tw, "version", "%s", BuildInfo())
	doctorRow(tw, "cgo", "%s", doctorYesNo(mac.CgoEnabled))
	doctorRow(tw, "go", "%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	doctorRow(tw, "scan root", "%s", root)
	_ = tw.Flush()
}

func doctorDataless(w io.Writer, setErr error, f *volume.Facts) {
	tw := doctorSection(w, "dataless materialization (evicted iCloud files)")
	if setErr == nil {
		doctorRow(tw, "set policy off", "ok")
	} else {
		doctorRow(tw, "set policy off", "unavailable: %v", setErr)
	}
	doctorRow(tw, "current policy", "%s", f.Dataless)
	if !mac.CgoEnabled {
		doctorRow(tw, "note", "the walker never opens a file and never lists a dataless directory, so a scan is still safe")
	}
	_ = tw.Flush()
}

func doctorPurgeable(w io.Writer, f *volume.Facts) {
	tw := doctorSection(w, "purgeable space")
	p := f.Purgeable
	if !p.Known {
		doctorRow(tw, "value", "unknown")
		doctorRow(tw, "reason", "%s", p.Err)
		_ = tw.Flush()
		return
	}
	doctorRow(tw, "value", "%s (%d bytes)", doctorUnits.Bytes(p.Bytes), p.Bytes)
	doctorRow(tw, "source", "%s", p.Source)
	doctorRow(tw, "important-usage capacity", "%s (%d bytes)", doctorUnits.Bytes(p.ImportantUsage), p.ImportantUsage)
	doctorRow(tw, "statfs available", "%s (%d bytes)", doctorUnits.Bytes(p.StatfsAvail), p.StatfsAvail)
	_ = tw.Flush()
}

func doctorVolAttrs(w io.Writer, f *volume.Facts) {
	tw := doctorSection(w, "getattrlist vs statfs (volume attributes cross-check)")
	rv, ok := f.RootVolume()
	if !ok {
		doctorRow(tw, "statfs", "no volume found for %s", f.Root)
		_ = tw.Flush()
		return
	}
	attrs, err := mac.GetVolAttrs(rv.MountPoint)
	if err != nil {
		doctorRow(tw, "getattrlist", "failed: %v", err)
		_ = tw.Flush()
		return
	}
	doctorf(tw, "  %s\t%s\t%s\t%s\n", "attribute", "getattrlist", "statfs", "difference")
	cmp := func(name string, a, s int64) {
		doctorf(tw, "  %s\t%d\t%d\t%d\n", name, a, s, a-s)
	}
	cmp("size", attrs.Size, rv.Total)
	cmp("space free", attrs.SpaceFree, rv.Free)
	cmp("space avail", attrs.SpaceAvail, rv.Avail)
	cmp("space used", attrs.SpaceUsed, rv.UsedStatfs)
	_ = tw.Flush()

	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	spike := attrs.SpaceAvail - rv.Avail
	doctorRow(tw, "purgeable spike (Q8)", "SpaceAvail - Bavail*Bsize = %d bytes (%s)", spike, doctorUnits.Bytes(spike))
	// A handful of blocks of difference is the two calls landing on either
	// side of an allocation, not a purgeable signal.
	if spike < volAttrSpikeTolerance && spike > -volAttrSpikeTolerance {
		doctorRow(tw, "verdict", "getattrlist restates statfs; Foundation stays the purgeable source")
	} else {
		doctorRow(tw, "verdict", "getattrlist disagrees with statfs; compare against the Foundation number above")
	}
	doctorRow(tw, "used source", "%s (statfs free space is container-wide on APFS)", rv.UsedSource)
	_ = tw.Flush()
}

func doctorPermissions(w io.Writer, f *volume.Facts) {
	tw := doctorSection(w, "permissions")
	t := f.Terminal
	term := t.AppName
	if t.Program != "" {
		term += fmt.Sprintf(" (TERM_PROGRAM=%s)", t.Program)
	}
	if t.ViaTmux {
		term += " via tmux"
	}
	doctorRow(tw, "terminal", "%s", term)
	doctorRow(tw, "bundle id", "%s", doctorOrDash(t.BundleID))
	doctorRow(tw, "terminal pid", "%s", doctorOrDash(doctorPID(t.PID)))
	if f.FDA.Granted {
		doctorRow(tw, "full disk access", "granted (read %s)", f.FDA.Path)
	} else {
		doctorRow(tw, "full disk access", "not granted: %v", f.FDA.Err)
		doctorRow(tw, "hint", "%s", mac.FullDiskAccessHint(t.AppName))
	}
	uid, gid, viaSudo := mac.InvokingUser()
	doctorRow(tw, "euid", "%d", f.Euid)
	if viaSudo {
		doctorRow(tw, "sudo", "yes; cache would be written as uid %d gid %d", uid, gid)
	} else {
		doctorRow(tw, "sudo", "no; root-owned directories will be reported as unreadable")
	}
	_ = tw.Flush()
}

func doctorContainer(w io.Writer, f *volume.Facts) {
	tw := doctorSection(w, "volumes")
	doctorf(tw, "  %s\t%s\t%s\t%s\t%s\n", "mount point", "device", "type", "used", "container")
	for _, v := range f.Before.Volumes {
		doctorf(tw, "  %s\t%s\t%s\t%s\t%s\n",
			v.MountPoint, v.Device, v.FSType, doctorUnits.Bytes(v.Used), doctorOrDash(v.Container))
	}
	for _, e := range f.Before.Errors {
		doctorf(tw, "  %s\t%s\t%s\t%s\t%s\n", e.MountPoint, "-", "-", "statfs failed: "+e.Err, "-")
	}
	_ = tw.Flush()

	c := f.Container
	tw = doctorSection(w, fmt.Sprintf("container %s", doctorOrDash(c.ID)))
	if c.ID == "" {
		doctorRow(tw, "container", "none found for %s", f.Root)
		_ = tw.Flush()
		return
	}
	doctorRow(tw, "total", "%s", doctorUnits.Bytes(c.Total))
	doctorRow(tw, "free", "%s", doctorUnits.Bytes(c.Free))
	doctorRow(tw, "used", "%s", doctorUnits.Bytes(c.Used))
	var sum int64
	for _, v := range c.Volumes {
		sum += v.Used
	}
	doctorRow(tw, "sum of volume used", "%s across %d volumes", doctorUnits.Bytes(sum), len(c.Volumes))
	doctorRow(tw, "overhead", "%s (used - sum)", doctorUnits.Bytes(c.Overhead))
	_ = tw.Flush()
}

func doctorNested(w io.Writer, f *volume.Facts) {
	tw := doctorSection(w, "mounts nested inside the scan root (the walk will not descend into these)")
	nested := f.Mounts.Nested(f.Root)
	if len(nested) == 0 {
		doctorRow(tw, "none", "")
	}
	for _, v := range nested {
		path := v.MountPoint
		if v.DataPath != "" {
			path = v.DataPath + "  (" + v.MountPoint + ")"
		}
		doctorf(tw, "  %s\t%s\t%s\n", path, v.FSType, v.Device)
	}
	_ = tw.Flush()
}

func doctorSnapshots(w io.Writer, f *volume.Facts) {
	tw := doctorSection(w, "local Time Machine snapshots")
	s := f.Snapshots
	switch {
	case !s.Known:
		doctorRow(tw, "unknown", "%s", s.Err)
	case len(s.Names) == 0:
		doctorRow(tw, "count", "0")
	default:
		doctorRow(tw, "count", "%d", len(s.Names))
		for _, n := range s.Names {
			doctorf(tw, "  %s\n", n)
		}
	}
	_ = tw.Flush()
}

func doctorPaths(w io.Writer) {
	tw := doctorSection(w, "paths")
	home, err := os.UserHomeDir()
	if err != nil {
		doctorRow(tw, "home", "unknown: %v", err)
		_ = tw.Flush()
		return
	}
	cache := filepath.Join(home, "Library", "Application Support", "storix")
	state := "does not exist yet"
	if fi, err := os.Stat(cache); err == nil && fi.IsDir() {
		state = "exists"
	}
	doctorRow(tw, "scan cache", "%s (%s)", cache, state)
	if dir, err := mac.UserCacheDir(); err == nil {
		doctorRow(tw, "darwin user cache", "%s", dir)
	} else {
		doctorRow(tw, "darwin user cache", "unknown: %v", err)
	}
	_ = tw.Flush()
}

func doctorOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func doctorPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	return strconv.Itoa(pid)
}

// doctorDetectorTools names the external binaries each registered detector's
// Probe looks for with Env.Has, mirroring the phase 1b plan's own detector
// table (docs/04). A detector absent from this map walks static catalog
// paths only and never asks the machine for anything, so it can never be
// Missing.
var doctorDetectorTools = map[string][]string{
	"aimodels": {"ollama"},
	"colima":   {"colima", "limactl"},
	"docker":   {"docker"},
	"go":       {"go"},
	"homebrew": {"brew"},
	"nix":      {"nix"},
	"node":     {"bun", "npm", "pnpm", "yarn"},
	"orbstack": {"docker", "orb"},
	"podman":   {"podman"},
	"projects": {"git"},
	"python":   {"pip", "pip3"},
	"ruby":     {"gem"},
	"rust":     {"cargo", "rustup"},
	"xcode":    {"xcode-select", "xcodebuild", "xcrun"},
}

// doctorTools prints, for every registered detector, whether its tools are on
// the augmented PATH a scan's probes search — the same LookPath a real scan
// runs, not a fresh guess — and, for xcode, the first-launch gate verdict.
//
// The gate is the one probe this command runs itself rather than leaving to
// a scan: `xcodebuild -checkFirstLaunchStatus` only, through the xcode
// detector's own gated Probe, which is the sole caller in the whole program
// allowed to run `xcrun simctl` and does so only after the gate has passed.
// Nothing here ever calls simctl directly.
func doctorTools(ctx context.Context, w io.Writer) {
	env := detect.DefaultEnv(&probe.Exec{}, mac.ScanPath(scan.HomeDir()))
	reg := detect.Default()

	tw := doctorSection(w, "tools (detectors)")
	doctorf(tw, "  %s\t%s\t%s\n", "detector", "tool", "path")
	var missing []string
	for _, det := range reg.Detectors() {
		name := det.Name()
		tools := doctorDetectorTools[name]
		if len(tools) == 0 {
			doctorRow(tw, name, "%s", "no external tool; walks static catalog paths only")
			continue
		}
		found := false
		for i, tool := range tools {
			label := ""
			if i == 0 {
				label = name
			}
			if p, err := env.LookPath(tool); err == nil {
				doctorf(tw, "  %s\t%s\t%s\n", label, tool, p)
				found = true
			} else {
				doctorf(tw, "  %s\t%s\t%s\n", label, tool, "not found")
			}
		}
		if !found {
			missing = append(missing, fmt.Sprintf("%s: none of %s found on the augmented PATH",
				name, strings.Join(tools, ", ")))
		}
	}
	_ = tw.Flush()

	tw = doctorSection(w, "xcode gate")
	facts, _ := xcode.New().Probe(ctx, env)
	xf, _ := facts.(*xcode.Facts)
	doctorRow(tw, "verdict", "%s", xf.Verdict())
	_ = tw.Flush()

	tw = doctorSection(w, "detectors that would be Missing")
	if len(missing) == 0 {
		doctorRow(tw, "none", "%s", "every detector found at least one of its tools on the path")
	}
	for _, reason := range missing {
		doctorf(tw, "  %s\n", reason)
	}
	_ = tw.Flush()
}
