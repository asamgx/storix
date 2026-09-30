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
// tagged, split the way the ledger splits it (D43): bytes that can be freed
// outright, and bytes only the tool itself can give back because it keeps
// what is still in use. Counting a live pnpm store or the default Rust
// toolchain as "reclaim" promised space no one should delete by hand.
func (t *textReport) reclaimLine(g toolGroup) {
	measured := g.summary.Reclaimable
	free, viaTool := reclaimShares(g.summary.Tools)

	var walked string
	switch {
	case free > 0 && viaTool > 0:
		walked = fmt.Sprintf("%s can be freed outright and %s more only through the tool, across the directories listed above",
			t.u.Bytes(free), t.u.Bytes(viaTool))
	case free > 0:
		walked = t.u.Bytes(free) + " can be freed outright, across the directories listed above"
	case viaTool > 0:
		walked = t.u.Bytes(viaTool) + " only through the tool, across the directories listed above"
	}

	switch {
	case measured > 0 && walked != "":
		t.field("reclaim", fmt.Sprintf("%s — %s; %s",
			t.u.Bytes(measured), reclaimSource(g.summary), walked), t.st.good)
	case measured > 0:
		t.field("reclaim", t.u.Bytes(measured)+" — "+reclaimSource(g.summary), t.st.good)
	case walked != "":
		t.field("reclaim", walked, t.st.good)
	}
}

// reclaimShares splits a detector's rows into what can be freed outright and
// what only the tool can give back, counting each byte once.
//
// A detector lists a directory and the interesting things inside it — the
// pnpm store root and each of its generations — and those rows carry
// different tags: the live generation is tool-managed, the superseded ones
// regenerable. Each byte is therefore counted under the deepest row that
// holds it, so the superseded generations count as free inside a store root
// that as a whole is not.
func reclaimShares(tools []detect.Tool) (free, viaTool int64) {
	sorted := append([]detect.Tool(nil), tools...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	own := make([]int64, len(sorted))
	var stack []int // indexes of the rows enclosing the current one
	for i, tool := range sorted {
		own[i] = tool.Bytes
		for len(stack) > 0 && !within(tool.Path, sorted[stack[len(stack)-1]].Path) {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			own[stack[len(stack)-1]] -= tool.Bytes
		}
		stack = append(stack, i)
	}
	for i, tool := range sorted {
		b := max(own[i], 0)
		switch {
		case tool.Reclaim.Freeable():
			free += b
		case tool.Reclaim == classify.ToolManaged:
			viaTool += b
		}
	}
	return free, viaTool
}

func reclaimSource(s detect.Summary) string {
	if s.ReclaimNote != "" {
		return s.ReclaimNote
	}
	return "reported by the tool itself"
}

// within reports whether path is dir or lies inside it. A row listed twice
// for the same directory is nested in the first, so it is not counted again.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
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
	// A project with no build output has nothing to give back, and on a
	// machine with dozens of repositories the rows of zeroes pushed the ones
	// that matter off the screen. They are counted, not listed.
	withArtifacts := make([]detect.Project, 0, len(projects))
	for _, p := range projects {
		if p.ArtifactBytes > 0 {
			withArtifacts = append(withArtifacts, p)
		}
	}
	clean := len(projects) - len(withArtifacts)

	writeLine(t.w, "")
	writeLine(t.w, t.st.title.Render("  projects"))

	shown := withArtifacts
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
	if hidden := len(withArtifacts) - len(shown); hidden > 0 {
		t.field("", fmt.Sprintf("and %d more projects, hidden by --top", hidden), t.st.note)
	}
	if clean > 0 {
		t.field("", fmt.Sprintf("%d more %s no build output", clean, plural(clean, "project has", "projects have")), t.st.note)
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
