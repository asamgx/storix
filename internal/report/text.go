// Package report renders a finished scan, as a styled text report for a
// terminal or as versioned JSON for another program.
//
// Both renderers read the same scan.Result and neither computes anything: the
// arithmetic belongs to internal/ledger, so the two outputs can never
// disagree about a number.
package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// Defaults for Options.
const (
	// DefaultTop is how many directories the text report lists.
	DefaultTop = 25
	// DefaultDepth is how deep the JSON tree goes.
	DefaultDepth = 3
	// DefaultMinSize is the smallest node the JSON tree includes.
	DefaultMinSize = 10 * 1000 * 1000
	// topDepth is how far below the root the directory listing looks. Depth
	// 2 is "/Users/andrewsam" and "~/Library": deep enough to name a
	// culprit, shallow enough that the list is not all one subtree.
	topDepth = 2
	// barWidth is the width of the proportional bars.
	barWidth = 14
	// maxPathsPerClass is how many unreadable paths each class shows.
	maxPathsPerClass = 10
	// textWidth is the width prose is wrapped to. It is fixed rather than
	// read from the terminal so that a redirected report and a piped one
	// are the same document.
	textWidth = 100
)

// Options control both renderers.
type Options struct {
	// Units selects decimal (Finder) or binary formatting.
	Units units.Format
	// Depth limits the JSON tree: zero selects DefaultDepth, a negative
	// value emits the root alone. Ignored when Full is set.
	Depth int
	// MinSize is the smallest node the JSON tree carries: zero selects
	// DefaultMinSize, a negative value includes everything. Ignored when
	// Full is set.
	MinSize int64
	// Full removes the Depth and MinSize limits. The tree is then hundreds
	// of megabytes on a full volume, which is why it is not the default.
	Full bool
	// Color enables ANSI styling in the text report. The caller sets it
	// from whether the output is a terminal and NO_COLOR is unset.
	Color bool
	// Top is how many directories the text report lists: zero selects
	// DefaultTop, a negative value drops the section.
	Top int
	// Version is the storix version recorded in the JSON document. The
	// report cannot read it from internal/cli, which imports this package.
	Version string
	// Debug adds the sections that exist to tune storix rather than to
	// describe the machine, such as the directories no rule matched.
	Debug bool
}

// withDefaults fills the zero values in.
func (o Options) withDefaults() Options {
	if o.Top == 0 {
		o.Top = DefaultTop
	}
	if o.Depth == 0 {
		o.Depth = DefaultDepth
	}
	if o.MinSize == 0 {
		o.MinSize = DefaultMinSize
	}
	return o
}

// Text writes the human-readable report.
func Text(w io.Writer, r *scan.Result, o Options) error {
	if r == nil || r.Tree == nil || r.Ledger == nil {
		return fmt.Errorf("report: nothing to render")
	}
	o = o.withDefaults()
	st := newStyles(o.Color)
	u := o.Units

	t := &textReport{w: w, r: r, l: r.Ledger, o: o, st: st, u: u}
	t.header()
	t.ledgerSection()
	t.buckets()
	t.containers()
	t.detectors()
	t.volume()
	t.container()
	t.topDirs()
	t.skipped()
	t.unreadable()
	t.dataless()
	t.snapshots()
	t.hints()
	t.counters()
	t.unmatched()
	return nil
}

// Unaccounted writes the sections that say what the scan could not see: the
// two ledger identities and then the skipped mounts, unreadable paths, cloud
// files, snapshots, hints and walk counters.
//
// It is Text without the header and the directory listing, and it exists for
// the TUI's Unaccounted view, which renders the same sections into a
// viewport rather than reimplementing them.
func Unaccounted(w io.Writer, r *scan.Result, o Options) error {
	if r == nil || r.Tree == nil || r.Ledger == nil {
		return fmt.Errorf("report: nothing to render")
	}
	o = o.withDefaults()
	t := &textReport{w: w, r: r, l: r.Ledger, o: o, st: newStyles(o.Color), u: o.Units}
	t.volume()
	t.container()
	t.detectors()
	t.skipped()
	t.unreadable()
	t.dataless()
	t.snapshots()
	t.hints()
	t.counters()
	return nil
}

// textReport carries the state every section needs.
type textReport struct {
	w  io.Writer
	r  *scan.Result
	l  *ledger.Ledger
	o  Options
	st styles
	u  units.Format
}

// section starts a titled block.
func (t *textReport) section(title string) {
	writeLine(t.w, "")
	writeLine(t.w, t.st.title.Render(title))
}

// bytes is a right-aligned byte cell.
func (t *textReport) bytes(n int64) cell { return cell{t.u.Bytes(n), t.st.num} }

// line renders a ledger line as label / note / bytes.
func (t *textReport) line(l ledger.Line) []cell {
	val := cell{t.u.Bytes(l.Bytes), t.st.num}
	if !l.Known {
		val = cell{"unknown", t.st.warn}
	}
	return []cell{{l.Label, t.st.label}, {l.Note, t.st.note}, val}
}

func (t *textReport) header() {
	tbl := newTable(t.st)
	tbl.add(cell{"root", t.st.label}, cell{t.l.Root, t.st.num})
	tbl.add(cell{"started", t.st.label}, cell{t.r.Tree.Started.Format(time.RFC3339), t.st.note})
	tbl.add(cell{"elapsed", t.st.label}, cell{round(t.l.Counters.Elapsed), t.st.note})
	tbl.add(cell{"source", t.st.label}, cell{t.source(), t.st.note})
	if t.l.Counters.Incomplete {
		tbl.add(cell{"status", t.st.label}, cell{"INCOMPLETE — interrupted, totals are a lower bound", t.st.warn})
	}
	writeLine(t.w, t.st.title.Render("storix scan"))
	tbl.render(t.w)
}

// source says whether the numbers were measured now or read from a cache.
func (t *textReport) source() string {
	if !t.r.FromCache {
		return "live"
	}
	return fmt.Sprintf("cache, %s old (%s)", round(t.r.CacheAge), t.r.CachePath)
}

// volume prints the volume identity: the three lines that make up used space
// and the verdict on the leftover.
func (t *textReport) volume() {
	l := t.l
	t.section("VOLUME  " + orDash(l.Volume.MountPoint))
	tbl := newTable(t.st, false, false, true)
	tbl.add(t.line(l.Scanned)...)
	tbl.add(t.line(l.Purgeable)...)
	tbl.add(t.residualRow()...)
	tbl.rule()
	tbl.add(cell{"used", t.st.label}, cell{"reported by the volume after the walk", t.st.note}, t.bytes(l.Volume.UsedAfter))
	tbl.render(t.w)

	t.field("identity", "scanned + purgeable + residual = used", t.st.note)
	t.field("residual", t.residualNote(), t.st.note)
	t.field("drift", fmt.Sprintf("%s before the walk, %s after; tolerance %s",
		t.u.Bytes(l.Volume.UsedBefore), t.u.Bytes(l.Volume.UsedAfter), t.u.Bytes(l.Volume.Tolerance)), t.st.note)
	t.field("verdict", t.verdict(), t.verdictStyle())
}

// fieldLabelWidth keeps the wrapped notes under the volume and container
// tables lined up with each other.
const fieldLabelWidth = 8

// field writes a labelled paragraph, wrapping it under its own label rather
// than letting a terminal fold a verdict into the left margin.
func (t *textReport) field(label, text string, st lipgloss.Style) {
	head := "  " + label + strings.Repeat(" ", max(fieldLabelWidth-len(label), 0)) + "  "
	indent := strings.Repeat(" ", len(head))
	for i, line := range wrapText(text, textWidth-len(head)) {
		prefix := head
		if i > 0 {
			prefix = indent
		}
		writeLine(t.w, prefix+st.Render(line))
	}
}

// residualRow renders the residual as a plain row. Its ledger label is a
// sentence about what the leftover is, too long for a table cell, so it goes
// on its own line under the table instead of being clipped.
func (t *textReport) residualRow() []cell {
	r := t.l.Residual
	return []cell{{"residual", t.st.label}, {"", t.st.note}, {t.u.Bytes(r.Bytes), t.st.num}}
}

// residualNote is the sign-dependent description of the residual.
func (t *textReport) residualNote() string {
	r := t.l.Residual
	if r.Note == "" {
		return r.Label
	}
	return r.Label + "; " + r.Note
}

// verdict is the one-line reconciliation judgement.
func (t *textReport) verdict() string {
	if t.l.Reconciles {
		return "reconciles — " + t.l.Reason
	}
	return "does not reconcile — " + t.l.Reason
}

func (t *textReport) verdictStyle() lipgloss.Style {
	if t.l.Reconciles {
		return t.st.good
	}
	return t.st.warn
}

// container prints the container identity: the volumes that share the disk
// with the scanned one, plus the overhead that belongs to none of them.
func (t *textReport) container() {
	l := t.l
	if !l.Container.Known {
		return
	}
	t.section("CONTAINER  " + l.Container.ID)
	tbl := newTable(t.st, false, false, true)
	tbl.add(t.line(l.Data)...)
	for _, m := range l.MacOS {
		tbl.add(t.line(m)...)
	}
	tbl.add(t.line(l.Overhead)...)
	tbl.rule()
	tbl.add(cell{"used", t.st.label}, cell{"", t.st.note}, t.bytes(l.Container.Used))
	tbl.add(cell{"free", t.st.label}, cell{"available to an unprivileged writer", t.st.note}, t.bytes(l.Container.Free))
	tbl.rule()
	tbl.add(cell{"total", t.st.label}, cell{"", t.st.note}, t.bytes(l.Container.Total))
	tbl.render(t.w)
	writeLine(t.w, t.st.note.Render("  identity  data + macOS volumes + overhead = used, and used + free = total"))

	if len(l.OtherContainers) > 0 {
		other := newTable(t.st, false, false, true)
		writeLine(t.w, "")
		writeLine(t.w, t.st.note.Render("  volumes on other containers, listed but part of no sum"))
		for _, o := range l.OtherContainers {
			other.add(t.line(o)...)
		}
		other.render(t.w)
	}
}

// topDirs lists the largest directories near the root, which is the section
// a reader looking to free space reads first.
func (t *textReport) topDirs() {
	if t.o.Top < 0 {
		return
	}
	dirs := topDirs(t.r.Tree.Root, topDepth, t.o.Top)
	if len(dirs) == 0 {
		return
	}
	total := t.r.Tree.Root.Bytes
	t.section(fmt.Sprintf("TOP %d DIRECTORIES  (depth ≤ %d, allocated bytes)", len(dirs), topDepth))
	tbl := newTable(t.st, false, true, true, true, false)
	for _, n := range dirs {
		tbl.add(
			cell{bar(t.st, n.Bytes, total, barWidth), lipgloss.NewStyle()},
			cell{t.u.Bytes(n.Bytes), t.st.num},
			cell{units.Percent(n.Bytes, total), t.st.note},
			cell{files(n.Files), t.st.note},
			cell{n.Display() + partialMark(n), t.st.label},
		)
	}
	tbl.render(t.w)
}

// files renders a file count with the right noun.
func files[T int | int64 | uint32 | uint64](n T) string {
	if n == 1 {
		return "1 file"
	}
	return count(n) + " files"
}

// partialMark flags a directory whose total is a lower bound.
func partialMark(n *walk.Node) string {
	if n.Has(walk.FlagPartial) || n.Has(walk.FlagUnreadable) {
		return " !"
	}
	return ""
}

func (t *textReport) skipped() {
	if len(t.l.Skipped) == 0 {
		return
	}
	t.section(fmt.Sprintf("MOUNTS SKIPPED  (%d, their bytes belong to another volume)", len(t.l.Skipped)))
	tbl := newTable(t.st)
	for _, m := range t.l.Skipped {
		tbl.add(cell{m.Path, t.st.label}, cell{m.FSType, t.st.note}, cell{m.From, t.st.note})
	}
	tbl.render(t.w)
}

func (t *textReport) unreadable() {
	if len(t.l.UnreadableByClass) == 0 {
		return
	}
	t.section(fmt.Sprintf("UNREADABLE  (%d paths; their content is in the residual above)", t.l.Counters.Errors))
	for _, g := range t.l.UnreadableByClass {
		writeLine(t.w, fmt.Sprintf("  %s  %s",
			t.st.label.Render(g.Name),
			t.st.note.Render(fmt.Sprintf("%d %s — %s", g.Count, plural(g.Count, "path", "paths"), classHelp(g.Class)))))
		for i, p := range g.Paths {
			if i == maxPathsPerClass {
				writeLine(t.w, t.st.note.Render(fmt.Sprintf("    … and %d more", g.Count-maxPathsPerClass)))
				break
			}
			writeLine(t.w, "    "+p)
		}
	}
}

// classHelp says what an error class means for the reader.
func classHelp(c walk.ErrClass) string {
	switch c {
	case walk.ErrPermission:
		return "owned by another user or by root; readable with sudo"
	case walk.ErrTCC:
		return "privacy protected; readable with Full Disk Access"
	case walk.ErrProtected:
		return "data vaults; not readable even with Full Disk Access"
	case walk.ErrVanished:
		return "deleted while the walk was running"
	default:
		return "other errors"
	}
}

func (t *textReport) dataless() {
	d := t.l.Dataless
	if d.Files == 0 {
		return
	}
	t.section("IN THE CLOUD, NOT ON DISK")
	tbl := newTable(t.st, false, true, true)
	for _, loc := range d.ByLocation {
		tbl.add(
			cell{loc.Label, t.st.label},
			cell{files(loc.Count), t.st.note},
			cell{t.u.Bytes(loc.Bytes), t.st.num},
		)
	}
	tbl.rule()
	tbl.add(cell{"total", t.st.label}, cell{files(d.Files), t.st.note}, t.bytes(d.Apparent))
	tbl.render(t.w)
	writeLine(t.w, t.st.note.Render("  sizes are apparent bytes and cover retained files only; these files occupy no local blocks"))
}

func (t *textReport) snapshots() {
	s := t.l.Snapshots
	if !s.Known {
		if s.Err != "" {
			t.section("LOCAL SNAPSHOTS")
			writeLine(t.w, t.st.note.Render("  unknown: "+s.Err))
		}
		return
	}
	if len(s.Names) == 0 {
		return
	}
	t.section(fmt.Sprintf("LOCAL SNAPSHOTS  (%d; their blocks are used space no file accounts for)", len(s.Names)))
	for _, n := range s.Names {
		writeLine(t.w, "  "+n)
	}
}

func (t *textReport) hints() {
	if len(t.l.Hints) == 0 {
		return
	}
	t.section("HINTS")
	for _, h := range t.l.Hints {
		for i, line := range wrapText(h.Text, textWidth-4) {
			bullet := "  • "
			if i > 0 {
				bullet = "    "
			}
			writeLine(t.w, bullet+t.st.warn.Render(line))
		}
	}
}

func (t *textReport) counters() {
	c := t.l.Counters
	t.section("WALK")
	tbl := newTable(t.st, false, true, false)
	tbl.add(cell{"files", t.st.label}, cell{count(c.Files), t.st.num}, cell{fmt.Sprintf("in %s directories", count(c.Dirs)), t.st.note})
	tbl.add(cell{"nodes retained", t.st.label}, cell{count(c.Nodes), t.st.num}, cell{"the rest are folded into their parent", t.st.note})
	tbl.add(cell{"hard links", t.st.label}, cell{count(c.LinkGroups), t.st.num},
		cell{fmt.Sprintf("groups, %s not double counted", t.u.Bytes(int64(c.LinkBytesSaved))), t.st.note})
	tbl.add(cell{"unreadable", t.st.label}, cell{count(c.Errors), t.st.num}, cell{"paths", t.st.note})
	tbl.add(cell{"vanished", t.st.label}, cell{count(c.Vanished), t.st.num}, cell{"entries deleted mid-walk", t.st.note})
	tbl.add(cell{"timing", t.st.label}, cell{"", t.st.note}, cell{t.timing(), t.st.note})
	tbl.render(t.w)
}

// timing is the per-stage breakdown on one line.
func (t *textReport) timing() string {
	tm := t.r.Timing
	parts := []string{
		"facts " + round(tm.Facts),
		"walk " + round(tm.Walk),
		"finish " + round(tm.Finish),
		"ledger " + round(tm.Ledger),
	}
	if tm.Persist > 0 {
		parts = append(parts, "cache "+round(tm.Persist))
	}
	parts = append(parts, "total "+round(tm.Total))
	return strings.Join(parts, ", ")
}

// plural picks the singular or plural noun for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// wrapText breaks text into lines no wider than width, on spaces. A word
// longer than the width gets a line of its own rather than being cut.
func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	lines := []string{words[0]}
	for _, w := range words[1:] {
		i := len(lines) - 1
		if len(lines[i])+1+len(w) <= width {
			lines[i] += " " + w
			continue
		}
		lines = append(lines, w)
	}
	return lines
}

// round trims a duration to something a person reads.
func round(d time.Duration) string {
	switch {
	case d >= time.Minute:
		return d.Round(time.Second).String()
	case d >= time.Second:
		return d.Round(10 * time.Millisecond).String()
	default:
		return d.Round(time.Millisecond).String()
	}
}

// orDash renders an empty string as a dash.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// topDirs returns the n largest directories no deeper than maxDepth below
// root, largest first, with the path breaking ties so two runs of the same
// scan list them in the same order.
func topDirs(root *walk.Node, maxDepth, n int) []*walk.Node {
	if n <= 0 {
		return nil
	}
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
