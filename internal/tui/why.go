package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// whyModel is the panel that answers "why is this here, and who says so".
//
// It is one widget for every view rather than one per view, because the
// answer has the same shape wherever it is asked: a thing, what storix
// decided about it, and the evidence that decision rests on. The views differ
// only in what they hand it.
type whyModel struct{ on bool }

// toggle opens or closes the panel.
func (w *whyModel) toggle() { w.on = !w.on }

// whyWideMin is the terminal width at which the panel goes beside the view
// instead of under it. Narrower than this, a 40 % panel would leave the table
// too thin to read a path in.
const whyWideMin = 100

// whyBottomLines is the height of the panel under a narrow view.
const whyBottomLines = 8

// whyPanelShare is the fraction of a wide terminal the panel takes.
const whyPanelShare = 40

// whyContent is what the panel draws: a thing, the fields storix decided
// about it, the verbatim evidence, and whatever else the view wanted said.
type whyContent struct {
	title    string
	fields   []whyField
	evidence []string
	sections []whySection
	notes    []string
}

// whyField is one decided fact.
type whyField struct{ label, value string }

// whySection is a labelled list, such as an application's components.
type whySection struct {
	title   string
	entries []whyEntry
}

// whyEntry is one line of a section, optionally with a path drawn under it.
// A path gets a line of its own because it is the longest thing in the panel
// and the only one whose tail matters more than its head.
type whyEntry struct {
	text string
	path string
}

// empty reports whether there is nothing to say.
func (c whyContent) empty() bool {
	return c.title == "" && len(c.fields) == 0 && len(c.evidence) == 0 && len(c.sections) == 0
}

// whyFieldWidth is the width of the label column inside the panel.
const whyFieldWidth = 10

// render draws the panel at a given size.
func (w *whyModel) render(st Styles, c whyContent, width, height int) string {
	lines := make([]string, 0, height)
	add := func(s string) {
		if len(lines) < height {
			lines = append(lines, s)
		}
	}

	add(st.PanelTitle.Render(truncate("WHY", width)))
	for _, l := range wrapText(c.title, width) {
		add(st.Crumb.Render(l))
	}
	if len(c.fields) > 0 {
		add("")
	}
	for _, f := range c.fields {
		label := padRight(st.Dim.Render(truncate(f.label, whyFieldWidth)), whyFieldWidth, truncate(f.label, whyFieldWidth))
		rest := max(width-whyFieldWidth-1, 8)
		wrapped := wrapText(f.value, rest)
		for i, l := range wrapped {
			if i == 0 {
				add(label + " " + st.Label.Render(l))
				continue
			}
			add(strings.Repeat(" ", whyFieldWidth+1) + st.Label.Render(l))
		}
	}
	if len(c.evidence) > 0 {
		add("")
		add(st.Dim.Render("evidence"))
		for _, e := range c.evidence {
			for i, l := range wrapText(e, max(width-2, 8)) {
				prefix := "  "
				if i > 0 {
					prefix = "    "
				}
				add(prefix + st.Label.Render(l))
			}
		}
	}
	for _, s := range c.sections {
		add("")
		add(st.Dim.Render(truncate(s.title, width)))
		for _, e := range s.entries {
			add("  " + st.Label.Render(truncate(e.text, max(width-2, 4))))
			if e.path != "" {
				add("    " + st.Dim.Render(truncateLeft(e.path, max(width-4, 4))))
			}
		}
	}
	for _, n := range c.notes {
		add("")
		for _, l := range wrapText(n, width) {
			add(st.Warn.Render(l))
		}
	}
	if c.empty() {
		add(st.Dim.Render(truncate("nothing is selected", width)))
	}
	return strings.Join(lines, "\n")
}

// wrapText breaks s into lines of at most w columns, on spaces where it can
// and mid-word where a single word is longer than the panel.
func wrapText(s string, w int) []string {
	if w <= 0 || s == "" {
		return nil
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
			for lipgloss.Width(line) > w {
				out = append(out, truncate(line, w))
				line = ""
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// nodeWhy is the panel's content for one node of the tree: what the engine
// decided, which rule or detector decided it, and the evidence behind it.
func nodeWhy(res *scan.Result, n *walk.Node, u units.Format) whyContent {
	if n == nil {
		return whyContent{}
	}
	c := whyContent{title: mac.DisplayPath(n.Path())}
	c.fields = append(c.fields, whyField{"size", u.Bytes(n.Bytes)})

	cl, ok := res.Class.OfNode(n)
	if !ok {
		c.fields = append(c.fields, whyField{"bucket", "Other"})
		c.notes = append(c.notes, "no rule and no detector claimed this path; it is counted in Other")
		return c
	}
	bucket := cl.Bucket.Label()
	if cl.Category != "" {
		bucket += " › " + cl.Category
	}
	c.fields = append(c.fields, whyField{"bucket", bucket})
	if cl.Owner != "" {
		owner := cl.Owner
		if cl.Confidence != classify.None {
			owner += " (" + cl.Confidence.String() + ")"
		}
		c.fields = append(c.fields, whyField{"owner", owner})
	}
	c.fields = append(c.fields, whyField{"reclaim", cl.Reclaim.String()})
	c.fields = append(c.fields, whyField{"source", cl.Source.String()})
	if from := res.Class.InheritedFromNode(n); from != nil {
		c.fields = append(c.fields, whyField{"inherited", "from " + mac.DisplayPath(from.Path())})
	}
	c.evidence = cl.Evidence
	if note, ok := detectorNote(res, cl.Source.Detector); ok {
		c.notes = append(c.notes, note)
	}
	return c
}

// detectorNote is the warning a claim earns when the detector behind it did
// not run cleanly: a degraded detector's claim is still the best answer
// available, and the reader is entitled to know it was a partial one.
func detectorNote(res *scan.Result, name string) (string, bool) {
	if name == "" || res == nil {
		return "", false
	}
	for _, st := range res.Detectors {
		if st.Name != name || st.State == detect.Ok {
			continue
		}
		note := "the " + name + " detector is " + st.State.String()
		if st.Reason != "" {
			note += ": " + st.Reason
		}
		return note, true
	}
	return "", false
}

// bucketWhy is the panel's content for one row of the ledger table.
func bucketWhy(b ledger.Bucket, u units.Format) whyContent {
	c := whyContent{title: b.Label}
	c.fields = append(c.fields,
		whyField{"id", b.ID},
		whyField{"size", u.Bytes(b.Bytes)},
	)
	if b.Reclaimable > 0 {
		c.fields = append(c.fields, whyField{"free", u.Bytes(b.Reclaimable)})
	}
	if b.Note != "" {
		c.fields = append(c.fields, whyField{"note", b.Note})
	}
	c.sections = append(c.sections,
		lineSection("by reclaimability", b.ByReclaim, u),
		lineSection("largest categories", b.Categories, u),
		lineSection("largest owners", b.Owners, u),
	)
	c.sections = nonEmptySections(c.sections)
	return c
}

// lineSection renders a list of ledger lines as panel text.
func lineSection(title string, ls []ledger.Line, u units.Format) whySection {
	s := whySection{title: title}
	for _, l := range ls {
		s.entries = append(s.entries, whyEntry{text: padLeft(u.Bytes(l.Bytes), whySizeWidth, u.Bytes(l.Bytes)) + "  " + l.Label})
	}
	return s
}

// whySizeWidth is the width of the size column inside a panel section, so a
// list of components reads down its numbers rather than zig-zagging.
const whySizeWidth = 9

// nonEmptySections drops the sections that had nothing in them.
func nonEmptySections(ss []whySection) []whySection {
	out := ss[:0]
	for _, s := range ss {
		if len(s.entries) > 0 {
			out = append(out, s)
		}
	}
	return out
}

// appWhy is the panel's content for one row of the Apps view: who the owner
// is, what storix concluded about it, and every signal on both sides.
func appWhy(e apps.Entry, u units.Format) whyContent {
	c := whyContent{title: appTitle(e)}
	c.fields = append(c.fields,
		whyField{"owner", e.Owner},
		whyField{"state", e.State},
		whyField{"confidence", e.Confidence},
		whyField{"footprint", u.Bytes(e.Footprint.Total)},
	)
	if !e.LastWrite.IsZero() {
		c.fields = append(c.fields, whyField{"last write", e.LastWrite.Format("2006-01-02")})
	}
	if len(e.Sources) > 0 {
		c.fields = append(c.fields, whyField{"sources", strings.Join(e.Sources, ", ")})
	}
	c.evidence = e.Evidence

	keep := whySection{title: "keep signals"}
	for _, k := range e.Keep {
		keep.entries = append(keep.entries, whyEntry{text: k})
	}

	comps := whySection{title: "components"}
	for _, comp := range e.Components {
		label := comp.Bucket
		if b, ok := bucketByID(comp.Bucket); ok {
			label = b.Label()
		}
		size := u.Bytes(comp.Bytes)
		comps.entries = append(comps.entries, whyEntry{
			text: padLeft(size, whySizeWidth, size) + "  " + label,
			path: mac.DisplayPath(comp.Path),
		})
	}
	c.sections = nonEmptySections([]whySection{keep, comps})
	return c
}

// appTitle is the name a reader would use for an entry.
func appTitle(e apps.Entry) string {
	if e.Label != "" {
		return e.Label
	}
	return e.Owner
}

// bucketByID maps a bucket id back onto the bucket, so a component recorded
// as "app-data" prints as "Application data".
func bucketByID(id string) (classify.Bucket, bool) {
	for _, b := range classify.Buckets() {
		if b.ID() == id {
			return b, true
		}
	}
	return 0, false
}
