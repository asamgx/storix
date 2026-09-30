package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/reclaim"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// planModel is the reclaim plan as a table: tier headings, and under each the
// items with their size and the command that frees them.
//
// The plan is built once per scan by internal/reclaim; this view only lists
// it, the way the Apps view lists the inventory. Like the command-line plan it
// is read-only: nothing here runs a command.
type planModel struct {
	plan *reclaim.Plan
	tree *walk.Tree

	rows           []planRow
	cursor, offset int
	width, height  int

	filter string
	valid  bool
}

// planRow is a tier heading or one item.
type planRow struct {
	heading bool
	title   string
	item    reclaim.Item
}

func newPlan() planModel { return planModel{width: 80, height: 1} }

// setPlan takes a finished scan and the plan built from it. The root model
// builds the plan once and hands the same one to the dashboard, so the two
// views can never disagree about a number (D53).
func (m *planModel) setPlan(res *scan.Result, p *reclaim.Plan) {
	m.plan, m.tree = p, res.Tree
	m.valid = false
	m.cursor, m.offset = 0, 0
	m.visible()
	m.toSelectable(1)
}

// focus puts the cursor on the item with this id, clearing a filter that
// would hide it. It reports whether the item is in the plan.
func (m *planModel) focus(id string) bool {
	if m.filter != "" {
		m.setFilter("")
	}
	for i, r := range m.visible() {
		if !r.heading && r.item.ID == id {
			m.cursor = i
			m.clamp()
			return true
		}
	}
	return false
}

func (m *planModel) setSize(w, h int) {
	m.width, m.height = w, max(h, 1)
	m.clamp()
}

func (m *planModel) visible() []planRow {
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

// build lists the tiers in the plan's order, each under its heading.
func (m *planModel) build() []planRow {
	if m.plan == nil {
		return nil
	}
	f := strings.ToLower(m.filter)
	var rows []planRow
	for _, tier := range reclaim.TierOrder() {
		var items []planRow
		for _, it := range m.plan.Items {
			if it.Tier != tier {
				continue
			}
			if f != "" && !strings.Contains(strings.ToLower(it.Title+" "+it.Path), f) {
				continue
			}
			items = append(items, planRow{item: it})
		}
		if len(items) == 0 {
			continue
		}
		rows = append(rows, planRow{heading: true, title: m.tierHeading(tier, len(items))})
		rows = append(rows, items...)
	}
	return rows
}

// tierHeading is a tier's label with what it adds up to.
func (m *planModel) tierHeading(t reclaim.Tier, n int) string {
	tot := m.plan.Totals[t]
	head := strings.ToUpper(t.Label())
	if tot != nil {
		head += "  " + units.Decimal.Bytes(tot.Walked)
		if tot.Reported > 0 {
			head += " + ~" + units.Decimal.Bytes(tot.Reported) + " reported"
		}
	}
	head += fmt.Sprintf("  (%d %s", n, pluralWord(n, "item", "items"))
	if !t.Suggested() {
		head += ", listed only"
	}
	return head + ")"
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (m *planModel) setFilter(s string) {
	if m.filter == s {
		return
	}
	m.filter, m.valid = s, false
	m.cursor, m.offset = 0, 0
	m.visible()
	m.toSelectable(1)
}

func (m *planModel) move(n int) {
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

func (m *planModel) moveTo(i int) {
	rows := m.visible()
	if len(rows) == 0 {
		return
	}
	m.cursor = min(max(i, 0), len(rows)-1)
	m.toSelectable(1)
	m.clamp()
}

func (m *planModel) toSelectable(step int) {
	if len(m.rows) == 0 {
		return
	}
	if step == 0 {
		step = 1
	}
	for i := m.cursor; i >= 0 && i < len(m.rows); i += step {
		if !m.rows[i].heading {
			m.cursor = i
			return
		}
	}
	for i := m.cursor; i >= 0 && i < len(m.rows); i -= step {
		if !m.rows[i].heading {
			m.cursor = i
			return
		}
	}
}

func (m *planModel) clamp() {
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

// selected is the item under the cursor.
func (m *planModel) selected() (reclaim.Item, bool) {
	rows := m.visible()
	if m.cursor < 0 || m.cursor >= len(rows) || rows[m.cursor].heading {
		return reclaim.Item{}, false
	}
	return rows[m.cursor].item, true
}

// selectedPath is the scan path of the selected item, for Finder and copy.
func (m *planModel) selectedPath() (string, bool) {
	it, ok := m.selected()
	if !ok || it.Path == "" {
		return "", false
	}
	return mac.ScanPath(it.Path), true
}

// open is the node the selected item stands for, which enter opens in
// Browse.
func (m *planModel) open() (*walk.Node, bool) {
	it, ok := m.selected()
	if !ok || m.tree == nil {
		return nil, false
	}
	if it.Node >= 0 && int(it.Node) < len(m.tree.Nodes) {
		return m.tree.Nodes[it.Node], true
	}
	if p, ok := m.selectedPath(); ok {
		return m.tree.Lookup(p)
	}
	return nil, false
}

// planWhatMin is the narrowest the command column is drawn; below it the
// column is dropped and the why panel carries the command.
const planWhatMin = 24

// planCols divides the width between the item's name and what frees it.
func planCols(width int) (name, what int) {
	fixed := markerWidth + bytesWidth + colGap*2
	if width < chipsMinWidth {
		return max(width-fixed, nameMin), 0
	}
	what = max(min(width/3, 48), planWhatMin)
	return max(width-fixed-what-colGap, nameMin), what
}

// View renders the visible window of the plan.
func (m *planModel) View(st Styles, u units.Format) string {
	rows := m.visible()
	if m.plan == nil {
		return st.Dim.Render("  nothing to show until the scan finishes")
	}
	if len(rows) == 0 {
		if m.filter != "" {
			return st.Dim.Render("  no item matches " + m.filter)
		}
		return st.Dim.Render("  the plan has nothing to suggest on this machine")
	}
	name, what := planCols(m.width)

	var sb strings.Builder
	sb.WriteString(m.header(st, u))
	sb.WriteByte('\n')
	sb.WriteString(m.columnsLine(st, name, what))
	sb.WriteByte('\n')
	end := min(m.offset+m.height, len(rows))
	for i := m.offset; i < end; i++ {
		sb.WriteString(m.renderRow(st, u, rows[i], name, what, i == m.cursor))
		if i < end-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// header is the title, with what the suggested tiers free on the right.
func (m *planModel) header(st Styles, u units.Format) string {
	right := u.Bytes(m.plan.Freeable()) + " could be freed  read-only"
	left := truncate("RECLAIM PLAN", max(m.width-lipgloss.Width(right)-2, 1))
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return st.Crumb.Render(left) + strings.Repeat(" ", gap) + st.Dim.Render(right)
}

func (m *planModel) columnsLine(st Styles, name, what int) string {
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	sb.WriteString(padLeft("frees", bytesWidth, "frees"))
	sb.WriteString(" " + padRight("item", name, "item"))
	if what > 0 {
		sb.WriteString(" " + "run / do")
	}
	return st.Dim.Render(strings.TrimRight(sb.String(), " "))
}

func (m *planModel) renderRow(st Styles, u units.Format, r planRow, name, what int, selected bool) string {
	if r.heading {
		return st.Title.Render(truncate(r.title, m.width))
	}
	it := r.item
	size := u.Fixed(it.Bytes)
	if it.Reported {
		size = "~" + strings.TrimSpace(size)
	}
	title := truncate(it.Title, name)
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	sb.WriteString(padLeft(st.Value.Render(size), bytesWidth, size))
	sb.WriteString(" " + padRight(st.Label.Render(title), name, title))
	if what > 0 {
		do := it.Command
		style := st.Chip
		if do == "" {
			do, style = it.Action, st.Dim
		}
		do = truncate(do, what)
		sb.WriteString(" " + style.Render(do))
	}
	return finishRow(st, sb.String(), selected)
}

// planWhy is what the panel says about one item: what it costs, how to free
// it, and why storix put it where it is.
func planWhy(it reclaim.Item, u units.Format) whyContent {
	c := whyContent{title: it.Title}
	size := u.Bytes(it.Bytes)
	if it.Reported {
		size = "~" + size + " (the tool's own figure)"
	}
	c.fields = append(c.fields, whyField{"tier", it.Tier.Label()}, whyField{"frees", size})
	if it.Command != "" {
		c.fields = append(c.fields, whyField{"run", it.Command})
	}
	if it.Action != "" {
		c.fields = append(c.fields, whyField{"do", it.Action})
	}
	c.fields = append(c.fields, whyField{"found by", it.Source})
	if it.Impact != "" {
		c.notes = append(c.notes, it.Impact)
	}
	if it.AfterTrash {
		c.notes = append(c.notes, "the space comes back only once the Trash is emptied")
	}
	if !it.Tier.Suggested() {
		c.notes = append(c.notes, "listed to account for its bytes; the plan never suggests removing it")
	}
	c.notes = append(c.notes, "read-only: storix has changed nothing")
	if it.Path != "" {
		c.sections = append(c.sections, whySection{title: "path", entries: []whyEntry{{path: it.Path}}})
	}
	c.evidence = it.Evidence
	return c
}
