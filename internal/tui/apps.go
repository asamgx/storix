package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// appsModel is the application inventory as a table: what is installed and
// what each one costs, then the owners whose application storix cannot find.
//
// The two halves are one scrolling list with unselectable headings rather
// than two panes, because the reader's question moves between them — "what is
// big" and "what is left over" are the same question asked of the same
// machine — and a single cursor is the cheapest way to let it.
type appsModel struct {
	rep  *apps.Report
	tree *walk.Tree

	rows           []appRow
	cursor, offset int
	width, height  int

	// byName orders both sections alphabetically instead of by size.
	byName bool
	// filter narrows both sections to the owners whose name contains it.
	filter string
	valid  bool
}

// appSection says which of the two tables a row belongs to, and so which
// columns it is drawn with.
type appSection uint8

const (
	sectionHeading appSection = iota
	sectionInstalled
	sectionAttention
)

// appRow is one line: a heading, or one owner.
type appRow struct {
	section appSection
	title   string
	entry   apps.Entry
	// state is the chip the attention table carries. It is the entry's own
	// state except for the unknown list, whose entries have none.
	state string
}

// newApps builds the empty view.
func newApps() appsModel { return appsModel{width: 80, height: 1} }

// setResult takes a finished scan.
func (m *appsModel) setResult(res *scan.Result) {
	rep, ok := scan.Apps(res)
	if !ok {
		rep = nil
	}
	m.rep, m.tree = rep, res.Tree
	m.valid = false
	m.cursor, m.offset = 0, 0
	m.visible()
	m.toSelectable(1)
}

// setSize records the space the table has.
func (m *appsModel) setSize(w, h int) {
	m.width, m.height = w, max(h, 1)
	m.clamp()
}

// appsUnknownTop is how many unattributed directories the attention table
// lists. The tail is long on every machine and each line is a guess; the head
// is the part worth a reader's attention.
const appsUnknownTop = 20

// visible builds the row list, rebuilding it when the sort or filter changed.
func (m *appsModel) visible() []appRow {
	if m.valid {
		return m.rows
	}
	m.rows = m.build()
	m.valid = true
	if m.cursor >= len(m.rows) {
		m.cursor = max(len(m.rows)-1, 0)
	}
	m.clamp()
	return m.rows
}

// build assembles the two sections.
func (m *appsModel) build() []appRow {
	if m.rep == nil {
		return nil
	}
	var rows []appRow
	installed := m.order(m.match(m.rep.Apps))
	if len(installed) > 0 {
		rows = append(rows, appRow{section: sectionHeading,
			title: fmt.Sprintf("INSTALLED  (%d, what each one costs)", len(installed))})
		for _, e := range installed {
			rows = append(rows, appRow{section: sectionInstalled, entry: e})
		}
	}

	var attention []appRow
	add := func(list []apps.Entry, state string, limit int) {
		list = m.order(m.match(list))
		if limit > 0 && len(list) > limit {
			list = list[:limit]
		}
		for _, e := range list {
			s := state
			if s == "" {
				s = e.State
			}
			attention = append(attention, appRow{section: sectionAttention, entry: e, state: s})
		}
	}
	add(m.rep.CaskOnly, "cask-only", 0)
	add(m.rep.InTrash, "in-trash", 0)
	add(m.rep.Orphans, "orphan-likely", 0)
	add(m.rep.Unknown, "unknown", appsUnknownTop)
	if len(attention) > 0 {
		rows = append(rows, appRow{section: sectionHeading,
			title: fmt.Sprintf("NEEDS ATTENTION  (%d owners whose software storix cannot find)", len(attention))})
		rows = append(rows, attention...)
	}
	return rows
}

// match applies the filter.
func (m *appsModel) match(list []apps.Entry) []apps.Entry {
	if m.filter == "" {
		return list
	}
	f := strings.ToLower(m.filter)
	out := make([]apps.Entry, 0, len(list))
	for _, e := range list {
		if strings.Contains(strings.ToLower(appTitle(e)), f) || strings.Contains(strings.ToLower(e.Owner), f) {
			out = append(out, e)
		}
	}
	return out
}

// order sorts a copy of a list, leaving the report's own order alone: the
// report belongs to the scan result, which the printed report reads too.
func (m *appsModel) order(list []apps.Entry) []apps.Entry {
	out := make([]apps.Entry, len(list))
	copy(out, list)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if m.byName {
			return strings.ToLower(appTitle(a)) < strings.ToLower(appTitle(b))
		}
		if a.Footprint.Total != b.Footprint.Total {
			return a.Footprint.Total > b.Footprint.Total
		}
		return appTitle(a) < appTitle(b)
	})
	return out
}

// sortBy switches the order, flipping nothing: the two orders answer two
// different questions and neither has a reverse worth a key.
func (m *appsModel) sortBy(byName bool) {
	if m.byName == byName {
		return
	}
	m.byName, m.valid = byName, false
	m.cursor, m.offset = 0, 0
	m.visible()
	m.toSelectable(1)
}

// setFilter narrows both sections.
func (m *appsModel) setFilter(s string) {
	if m.filter == s {
		return
	}
	m.filter, m.valid = s, false
	m.cursor, m.offset = 0, 0
	m.visible()
	m.toSelectable(1)
}

// move shifts the cursor, stepping over the headings.
func (m *appsModel) move(n int) {
	rows := m.visible()
	if len(rows) == 0 {
		return
	}
	step := 1
	if n < 0 {
		step = -1
	}
	for range abs(n) {
		m.cursor = min(max(m.cursor+step, 0), len(rows)-1)
		m.toSelectable(step)
	}
	m.clamp()
}

// moveTo places the cursor on an absolute row, then onto the nearest row that
// is not a heading.
func (m *appsModel) moveTo(i int) {
	rows := m.visible()
	if len(rows) == 0 {
		return
	}
	m.cursor = min(max(i, 0), len(rows)-1)
	m.toSelectable(1)
	m.clamp()
}

// toSelectable walks the cursor off a heading in the given direction, and
// back the other way when it ran out of rows.
func (m *appsModel) toSelectable(step int) {
	if len(m.rows) == 0 {
		return
	}
	if step == 0 {
		step = 1
	}
	for i := m.cursor; i >= 0 && i < len(m.rows); i += step {
		if m.rows[i].section != sectionHeading {
			m.cursor = i
			return
		}
	}
	for i := m.cursor; i >= 0 && i < len(m.rows); i -= step {
		if m.rows[i].section != sectionHeading {
			m.cursor = i
			return
		}
	}
}

// clamp scrolls the window so the cursor is inside it.
func (m *appsModel) clamp() {
	h := max(m.height, 1)
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if maxOff := max(len(m.rows)-h, 0); m.offset > maxOff {
		m.offset = maxOff
	}
	m.offset = max(m.offset, 0)
}

// selected is the entry under the cursor.
func (m *appsModel) selected() (apps.Entry, bool) {
	rows := m.visible()
	if m.cursor < 0 || m.cursor >= len(rows) || rows[m.cursor].section == sectionHeading {
		return apps.Entry{}, false
	}
	return rows[m.cursor].entry, true
}

// largest is the owner's biggest component: the directory the reader would
// open first, and what enter and the Finder key act on.
func largestComponent(e apps.Entry) (apps.ComponentRef, bool) {
	var best apps.ComponentRef
	found := false
	for _, c := range e.Components {
		if !found || c.Bytes > best.Bytes {
			best, found = c, true
		}
	}
	return best, found
}

// selectedPath is the scan path of the selected owner's largest component.
func (m *appsModel) selectedPath() (string, bool) {
	e, ok := m.selected()
	if !ok {
		return "", false
	}
	c, ok := largestComponent(e)
	if !ok {
		return "", false
	}
	return mac.ScanPath(c.Path), true
}

// open resolves the selected owner's largest component to a node of the tree,
// which is what the root model opens in Browse.
func (m *appsModel) open() (*walk.Node, bool) {
	p, ok := m.selectedPath()
	if !ok || m.tree == nil {
		return nil, false
	}
	return m.tree.Lookup(p)
}

// Column widths of the two tables.
const (
	appsBarWidth    = 12
	appsNameMin     = 16
	sourceChipWidth = 6
	stateChipWidth  = 13
	confChipWidth   = 8
	dateWidth       = 10
)

// appsCols is the layout of both tables. The name column is the same width in
// each so the two sections read as one list.
type appsCols struct {
	name, bar int
	chips     bool
}

// appsLayout divides the width. The chips are the first thing a narrow
// terminal loses, except the state chip, which is the whole point of the
// second table and stays at any width.
func appsLayout(width int) appsCols {
	c := appsCols{bar: appsBarWidth, chips: width >= chipsMinWidth}
	installed := markerWidth + colGap + c.bar + 4*(colGap+bytesWidth)
	attention := markerWidth + colGap + c.bar + colGap + bytesWidth + colGap + dateWidth + colGap + stateChipWidth
	if c.chips {
		installed += colGap + sourceChipWidth + colGap + confChipWidth
		attention += colGap + confChipWidth
	}
	c.name = max(width-max(installed, attention)-colGap, appsNameMin)
	return c
}

// View renders the visible window of the list.
func (m *appsModel) View(st Styles, u units.Format) string {
	rows := m.visible()
	if m.rep == nil {
		return st.Dim.Render("  this scan holds no application inventory")
	}
	if len(rows) == 0 {
		if m.filter != "" {
			return st.Dim.Render("  no application matches " + m.filter)
		}
		return st.Dim.Render("  no application was found on this machine")
	}
	c := appsLayout(m.width)
	total := m.largestFootprint(rows)

	var sb strings.Builder
	sb.WriteString(m.header(st, u))
	sb.WriteByte('\n')
	sb.WriteString(m.columnsLine(st, c))
	sb.WriteByte('\n')
	end := min(m.offset+m.height, len(rows))
	for i := m.offset; i < end; i++ {
		sb.WriteString(m.renderRow(st, u, c, rows[i], total, i == m.cursor))
		if i < end-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// largestFootprint is what the bars are measured against: the biggest owner
// on screen, so the bars compare applications with each other rather than
// with a disk none of them fills.
func (m *appsModel) largestFootprint(rows []appRow) int64 {
	var n int64
	for _, r := range rows {
		if r.entry.Footprint.Total > n {
			n = r.entry.Footprint.Total
		}
	}
	return n
}

// header is the title line, with what the inventory found on the right.
func (m *appsModel) header(st Styles, u units.Format) string {
	counts := m.rep.Counts
	right := fmt.Sprintf("%d bundles  %d casks  %s to look at",
		counts.Bundles, counts.Casks, u.Bytes(m.rep.NeedsAttention()))
	left := truncate("APPLICATIONS", max(m.width-lipgloss.Width(right)-2, 1))
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return st.Crumb.Render(left) + strings.Repeat(" ", gap) + st.Dim.Render(right)
}

// columnsLine names the columns of the section the cursor is in.
//
// The two tables have different columns, and the header line does not scroll
// with them, so it follows the cursor instead: a reader looking at the
// orphans is never told that the date under their eyes is a cache size.
func (m *appsModel) columnsLine(st Styles, c appsCols) string {
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	if m.cursorSection() == sectionAttention {
		sb.WriteString(padRight("owner", c.name, "owner"))
		sb.WriteString(" " + strings.Repeat(" ", c.bar))
		sb.WriteString(" " + padLeft("size", bytesWidth, "size"))
		sb.WriteString(" " + padLeft("last write", dateWidth, "last write"))
		sb.WriteString(" " + padRight("state", stateChipWidth, "state"))
		if c.chips {
			sb.WriteString(" " + "sure")
		}
		return st.Dim.Render(strings.TrimRight(sb.String(), " "))
	}
	sb.WriteString(padRight("application", c.name, "application"))
	sb.WriteString(" " + strings.Repeat(" ", c.bar))
	for _, h := range []string{"total", "bundle", "data", "caches"} {
		sb.WriteString(" " + padLeft(h, bytesWidth, h))
	}
	if c.chips {
		sb.WriteString(" " + padRight("source", sourceChipWidth, "source"))
		sb.WriteString(" " + "sure")
	}
	return st.Dim.Render(strings.TrimRight(sb.String(), " "))
}

// cursorSection is which of the two tables the cursor is in.
func (m *appsModel) cursorSection() appSection {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return sectionInstalled
	}
	return m.rows[m.cursor].section
}

// renderRow draws one line of whichever table it belongs to.
func (m *appsModel) renderRow(st Styles, u units.Format, c appsCols, r appRow, total int64, selected bool) string {
	switch r.section {
	case sectionHeading:
		return st.Title.Render(truncate(r.title, m.width))
	case sectionAttention:
		return finishRow(st, m.attentionRow(st, u, c, r, total), selected)
	default:
		return finishRow(st, m.installedRow(st, u, c, r, total), selected)
	}
}

// finishRow trims a row and applies the selection.
func finishRow(st Styles, line string, selected bool) string {
	line = strings.TrimRight(line, " ")
	if selected {
		return st.Selected.Render(stripStyles(line))
	}
	return line
}

// installedRow is one application and what it costs, broken into the three
// kinds of bytes a reader treats differently.
func (m *appsModel) installedRow(st Styles, u units.Format, c appsCols, r appRow, total int64) string {
	e := r.entry
	name := truncate(appTitle(e), c.name)
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	sb.WriteString(padRight(st.Label.Render(name), c.name, name))
	sb.WriteString(" " + bar(st, e.Footprint.Total, total, c.bar))
	for i, n := range []int64{e.Footprint.Total, e.Footprint.Bundle, e.Footprint.Data, e.Footprint.Caches} {
		text := ""
		if n > 0 {
			text = u.Fixed(n)
		}
		style := st.Dim
		if i == 0 {
			style = st.Value
		}
		sb.WriteString(" " + padLeft(style.Render(text), bytesWidth, text))
	}
	if c.chips {
		src := truncate(firstSource(e), sourceChipWidth)
		sb.WriteString(" " + padRight(st.Chip.Render(src), sourceChipWidth, src))
		conf := truncate(confidenceText(e.Confidence), confChipWidth)
		sb.WriteString(" " + confidenceChip(st, e.Confidence).Render(conf))
	}
	return sb.String()
}

// attentionRow is one owner storix cannot account for, with the claim it is
// making about it and how sure it is.
//
// The bar is measured against the same total the installed table uses, which
// the view works out once: recomputing it here made drawing the table cost
// the square of its length, and left the two tables on different scales.
func (m *appsModel) attentionRow(st Styles, u units.Format, c appsCols, r appRow, total int64) string {
	e := r.entry
	name := truncate(appTitle(e), c.name)
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	sb.WriteString(padRight(st.Label.Render(name), c.name, name))
	sb.WriteString(" " + bar(st, e.Footprint.Total, total, c.bar))
	size := u.Fixed(e.Footprint.Total)
	sb.WriteString(" " + padLeft(st.Value.Render(size), bytesWidth, size))
	last := ""
	if !e.LastWrite.IsZero() {
		last = e.LastWrite.Format("2006-01-02")
	}
	sb.WriteString(" " + padLeft(st.Dim.Render(last), dateWidth, last))
	state := truncate(r.state, stateChipWidth)
	sb.WriteString(" " + padRight(stateChip(st, r.state).Render(state), stateChipWidth, state))
	if c.chips {
		conf := truncate(confidenceText(e.Confidence), confChipWidth)
		sb.WriteString(" " + confidenceChip(st, e.Confidence).Render(conf))
	}
	return sb.String()
}

// firstSource is the shortest true answer to "how do you know this is
// installed": the first signal the inventory recorded, in a word narrow
// enough to be a chip. The inventory's own names are locations rather than
// labels, and "/Appli…" in a six-column cell says nothing.
func firstSource(e apps.Entry) string {
	if len(e.Sources) == 0 {
		return ""
	}
	switch s := e.Sources[0]; s {
	case "/Applications":
		return "apps"
	case "~/Applications":
		return "~apps"
	case "vendor folder":
		return "vendor"
	default:
		return s
	}
}

// confidenceText is the word the chip shows, the report's own wording.
func confidenceText(conf string) string { return report.OrphanGrade(conf) }

// abs is the magnitude of a cursor step.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
