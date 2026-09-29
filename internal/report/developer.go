package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/scan"
)

// Developer writes the developer section on its own. It exists for the TUI's
// Developer view and for `storix dev`, which render the same section into a
// viewport rather than reimplementing it.
func Developer(w io.Writer, r *scan.Result, o Options) error {
	if r == nil {
		return fmt.Errorf("report: nothing to render")
	}
	o = o.withDefaults()
	t := &textReport{w: w, r: r, l: r.Ledger, o: o, st: newStyles(o.Color), u: o.Units}
	t.developer()
	return nil
}

// developer prints what each toolchain keeps on disk, grouped by the detector
// that found it.
//
// Grouping by detector rather than by size is deliberate. A reader of this
// section is not looking for the largest directory on the machine — the
// ledger above already said that — but for the answer to "what is Node
// costing me", and that answer is several directories in several places that
// only the detector knows belong together.
func (t *textReport) developer() {
	groups := t.toolGroups()
	projects := t.allProjects()
	if len(groups) == 0 && len(projects) == 0 {
		return
	}

	t.section("DEVELOPER  (what your toolchains keep, and what they would give back)")
	for i, g := range groups {
		if i > 0 {
			writeLine(t.w, "")
		}
		t.toolGroup(g)
	}
	t.projects(projects)
}

// toolGroup is one detector's rows.
type toolGroup struct {
	name    string
	status  detect.Status
	summary detect.Summary
}

// toolGroups gathers the detectors that found tools, in registry order, so
// the section reads in the same order as the detectors table below it.
func (t *textReport) toolGroups() []toolGroup {
	var out []toolGroup
	for _, st := range t.r.Detectors {
		sum := t.r.Summaries[st.Name]
		sum.Tools = t.developerTools(st.Name, sum.Tools)
		// A detector with no rows can still have something to say: brew
		// reports what `brew cleanup` would free whether or not the walk
		// reached the Cellar.
		if len(sum.Tools) == 0 && sum.Reclaimable == 0 {
			continue
		}
		out = append(out, toolGroup{name: st.Name, status: st, summary: sum})
	}
	return out
}

// developerTools keeps the rows this detector won and the classifier put in
// the Developer bucket.
//
// Two things are filtered out and both would mislead. A detector's summary is
// not by itself a developer summary: OrbStack's group container and kubectl's
// cache are tool rows too and belong in the containers section. And two
// detectors can recognise the same directory — the catch-all reading the name
// of ~/.local/share/nvim and the editor detector knowing it is Neovim's — in
// which case the classifier has already decided whose it is, and printing the
// loser's row again would show the same gigabyte twice under two names.
//
// A row the classifier never reached is kept rather than dropped: the walk
// may not have covered it, and saying nothing about it would be worse.
func (t *textReport) developerTools(detector string, tools []detect.Tool) []detect.Tool {
	if t.r.Class == nil {
		return tools
	}
	out := make([]detect.Tool, 0, len(tools))
	for _, tool := range tools {
		cl, ok := t.r.Class.Of(tool.Node)
		switch {
		case !ok:
		case cl.Bucket != classify.BucketDeveloper:
			continue
		case cl.Source.Kind == classify.SourceDetector && cl.Source.Detector != detector:
			continue
		}
		out = append(out, tool)
	}
	return out
}

// allProjects gathers every detector's projects. The projects detector is the
// only one that produces them, but reading them from the summaries rather
// than from a named detector means this section needs no edit when it lands.
func (t *textReport) allProjects() []detect.Project {
	var out []detect.Project
	for _, st := range t.r.Detectors {
		out = append(out, t.r.Summaries[st.Name].Projects...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ArtifactBytes > out[j].ArtifactBytes })
	return out
}

// toolGroup prints one detector's tool rows and what they add up to.
func (t *textReport) toolGroup(g toolGroup) {
	writeLine(t.w, t.st.title.Render("  "+g.name))

	rows := sortedTools(g.summary.Tools)
	shown := rows
	if t.o.Top > 0 && len(shown) > t.o.Top {
		shown = shown[:t.o.Top]
	}

	tbl := newTable(t.st, true, false, false, false, false)
	for _, tool := range shown {
		tbl.add(
			t.bytes(tool.Bytes),
			cell{reclaimTag(tool.Reclaim), t.toolReclaimStyle(tool)},
			cell{currentMark(tool), t.st.good},
			cell{toolLabel(tool), t.st.label},
			cell{tool.Note, t.st.note},
		)
	}
	tbl.render(t.w)

	if hidden := len(rows) - len(shown); hidden > 0 {
		t.field("", fmt.Sprintf("and %d smaller directories, hidden by --top", hidden), t.st.note)
	}
	t.reclaimLine(g)
}

// reclaimLine says what this detector's bytes would give back.
//
// Two numbers can appear and they answer different questions. The first is
// what the tool itself says it would free, which for Homebrew is the summary
// line of `brew cleanup -n` and knows about superseded kegs no path pattern
// can identify. The second is what storix adds up from the directories it
// tagged reclaimable. Where both exist both are printed, because a reader
// deciding what to delete wants the tool's own promise first.
func (t *textReport) reclaimLine(g toolGroup) {
	measured := g.summary.Reclaimable
	var walked int64
	for _, tool := range outermost(g.summary.Tools) {
		if tool.Reclaim.Reclaimable() {
			walked += tool.Bytes
		}
	}

	switch {
	case measured > 0 && walked > 0:
		t.field("reclaim", fmt.Sprintf("%s — %s; %s across the directories listed above",
			t.u.Bytes(measured), reclaimSource(g.summary), t.u.Bytes(walked)), t.st.good)
	case measured > 0:
		t.field("reclaim", t.u.Bytes(measured)+" — "+reclaimSource(g.summary), t.st.good)
	case walked > 0:
		t.field("reclaim", t.u.Bytes(walked)+" across the directories listed above", t.st.good)
	}
}

// reclaimSource attributes a tool-reported figure to the command that
// produced it.
func reclaimSource(s detect.Summary) string {
	if s.ReclaimNote != "" {
		return s.ReclaimNote
	}
	return "reported by the tool itself"
}

// outermost drops the rows nested inside another row, so that a total over
// them counts each byte once.
//
// A detector lists both a directory and the interesting things inside it —
// the Cellar and the two formulae with a superseded version, the pnpm store
// root and each of its generations — because both are worth seeing. Adding
// those rows up would count the same gigabyte three times, which is how a
// report ends up promising more free space than the disk has.
func outermost(tools []detect.Tool) []detect.Tool {
	sorted := append([]detect.Tool(nil), tools...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	out := make([]detect.Tool, 0, len(sorted))
	kept := ""
	for _, tool := range sorted {
		if kept != "" && strings.HasPrefix(tool.Path, kept+"/") {
			continue
		}
		kept = tool.Path
		out = append(out, tool)
	}
	return out
}

// sortedTools orders a detector's rows largest first, which is the order a
// reader wants and the order that survives a --top cut without losing
// anything worth acting on.
func sortedTools(tools []detect.Tool) []detect.Tool {
	out := append([]detect.Tool(nil), tools...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// toolLabel is the name, the version when there is one, and the path.
func toolLabel(tool detect.Tool) string {
	name := tool.Name
	if tool.Version != "" {
		name += " " + tool.Version
	}
	if name == "" {
		return tool.Path
	}
	return name + "  " + tool.Path
}

// currentMark flags the version in use among several installed. A star is
// enough: the note beside it says what it means.
func currentMark(tool detect.Tool) string {
	if tool.Current {
		return "*"
	}
	return ""
}

// reclaimTag is the short form of a reclaim tag, the same vocabulary the
// Browse view's chips use.
func reclaimTag(r classify.Reclaim) string {
	switch r.String() {
	case "regenerable":
		return "regen"
	case "tool-managed":
		return "tool"
	case "orphaned":
		return "orph"
	case "user-data":
		return "user"
	case "system":
		return "sys"
	default:
		return "?"
	}
}

// toolReclaimStyle greens a tag that is safe to act on and greys the rest,
// so a column of tags reads as a column of verdicts.
func (t *textReport) toolReclaimStyle(tool detect.Tool) lipgloss.Style {
	if tool.Reclaim.Reclaimable() {
		return t.st.good
	}
	return t.st.note
}

// projects prints the source projects under the code roots: what their build
// artifacts cost and when they were last touched.
//
// Stale projects with large artifacts sort to the top, because a directory
// nobody has opened in a year holding a gigabyte of node_modules is the
// easiest gigabyte on the machine to give back.
func (t *textReport) projects(projects []detect.Project) {
	if len(projects) == 0 {
		return
	}
	writeLine(t.w, "")
	writeLine(t.w, t.st.title.Render("  projects"))

	shown := projects
	if t.o.Top > 0 && len(shown) > t.o.Top {
		shown = shown[:t.o.Top]
	}
	tbl := newTable(t.st, true, false, false, false)
	for _, p := range shown {
		tbl.add(
			t.bytes(p.ArtifactBytes),
			cell{"build artifacts", t.st.note},
			cell{vcsMark(p), t.st.note},
			cell{p.Root + lastActivity(p), t.st.label},
		)
	}
	tbl.render(t.w)
	if hidden := len(projects) - len(shown); hidden > 0 {
		t.field("", fmt.Sprintf("and %d more projects, hidden by --top", hidden), t.st.note)
	}
}

// vcsMark says whether a project is under version control, which decides
// whether its source is recoverable from somewhere else.
func vcsMark(p detect.Project) string {
	if p.VCS {
		return "git"
	}
	return ""
}

// lastActivity appends when the project was last touched.
//
// It is a date rather than "three months ago" because a report is a document:
// the same scan rendered tomorrow would otherwise read differently, and a
// reader comparing two reports would see a difference that is not there.
func lastActivity(p detect.Project) string {
	if p.LastActivity.IsZero() {
		return ""
	}
	return "  (last touched " + p.LastActivity.UTC().Format(time.DateOnly) + ")"
}
