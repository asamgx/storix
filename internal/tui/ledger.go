package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// ledgerModel is the twelve-row answer to "what is on this disk", and the
// drill-down that turns one of those rows back into directories.
//
// The table is the ledger's own bucket rows: nothing here adds a byte to
// anything. The drill list is the classification's roots for a bucket — the
// nodes where the bucket begins — which is the shortest list of paths that
// covers the whole of it, rather than the longest list of files inside it.
type ledgerModel struct {
	l     *ledger.Ledger
	class *classify.Classification
	tree  *walk.Tree

	cursor, offset int
	width, height  int

	// drill is the bucket the reader opened, zero when the table is what
	// is on screen. roots is that bucket's list, largest first.
	drill         classify.Bucket
	roots         []ledgerRoot
	dCursor, dOff int
}

// ledgerRoot is one line of a bucket's drill-down.
type ledgerRoot struct {
	node       *walk.Node
	path       string
	bytes      int64
	owner      string
	reclaim    classify.Reclaim
	classified bool
}

// newLedger builds the empty view.
func newLedger() ledgerModel { return ledgerModel{width: 80, height: 1} }

// setResult takes a finished scan. The cursor keeps its row across a rescan,
// because the twelve rows are the same twelve rows; an open drill-down is
// closed, because its paths may not be there any more.
func (m *ledgerModel) setResult(res *scan.Result) {
	m.l, m.class, m.tree = res.Ledger, res.Class, res.Tree
	m.drill, m.roots = 0, nil
	m.dCursor, m.dOff = 0, 0
	if m.cursor >= len(m.buckets()) {
		m.cursor = 0
		m.offset = 0
	}
}

// setSize records the space the table has, in rows of the table itself.
func (m *ledgerModel) setSize(w, h int) {
	m.width, m.height = w, max(h, 1)
	m.clamp()
}

// buckets is the twelve rows, empty until a scan has been adopted.
func (m *ledgerModel) buckets() []ledger.Bucket {
	if m.l == nil {
		return nil
	}
	return m.l.Buckets
}

// drilling reports whether the bucket list is what the keys act on.
func (m *ledgerModel) drilling() bool { return m.drill != 0 }

// rowCount is how many rows the view on screen has.
func (m *ledgerModel) rowCount() int {
	if m.drilling() {
		return len(m.roots)
	}
	return len(m.buckets())
}

// move shifts the cursor by n rows and keeps it on screen.
func (m *ledgerModel) move(n int) { m.moveTo(m.cursorAt() + n) }

// moveTo places the cursor on an absolute row.
func (m *ledgerModel) moveTo(i int) {
	n := m.rowCount()
	if n == 0 {
		return
	}
	i = min(max(i, 0), n-1)
	if m.drilling() {
		m.dCursor = i
	} else {
		m.cursor = i
	}
	m.clamp()
}

// cursorAt is the cursor of whichever list is on screen.
func (m *ledgerModel) cursorAt() int {
	if m.drilling() {
		return m.dCursor
	}
	return m.cursor
}

// clamp scrolls the window so the cursor is inside it.
func (m *ledgerModel) clamp() {
	h := max(m.height, 1)
	cur, off, n := m.cursorAt(), m.offset, m.rowCount()
	if m.drilling() {
		off = m.dOff
	}
	if cur < off {
		off = cur
	}
	if cur >= off+h {
		off = cur - h + 1
	}
	if maxOff := max(n-h, 0); off > maxOff {
		off = maxOff
	}
	off = max(off, 0)
	if m.drilling() {
		m.dOff = off
		return
	}
	m.offset = off
}

// bucketAt maps a table row onto its classification bucket. The ledger builds
// its rows from classify.Buckets() in order, so the index is the bucket.
func bucketAt(i int) classify.Bucket {
	all := classify.Buckets()
	if i < 0 || i >= len(all) {
		return 0
	}
	return all[i]
}

// selectedBucket is the bucket row under the cursor.
func (m *ledgerModel) selectedBucket() (ledger.Bucket, classify.Bucket, bool) {
	bs := m.buckets()
	if m.cursor < 0 || m.cursor >= len(bs) {
		return ledger.Bucket{}, 0, false
	}
	return bs[m.cursor], bucketAt(m.cursor), true
}

// selectedRoot is the drill-down line under the cursor.
func (m *ledgerModel) selectedRoot() (ledgerRoot, bool) {
	if !m.drilling() || m.dCursor < 0 || m.dCursor >= len(m.roots) {
		return ledgerRoot{}, false
	}
	return m.roots[m.dCursor], true
}

// open descends: from the table into a bucket's roots, and from a root into
// the browser. The node it returns is what the root model opens in Browse.
func (m *ledgerModel) open() (*walk.Node, bool) {
	if m.drilling() {
		r, ok := m.selectedRoot()
		if !ok || r.node == nil {
			return nil, false
		}
		return r.node, true
	}
	_, b, ok := m.selectedBucket()
	if !ok || b == 0 {
		return nil, false
	}
	m.roots = m.bucketRoots(b)
	m.drill = b
	m.dCursor, m.dOff = 0, 0
	return nil, false
}

// back closes the drill-down, returning to the table.
func (m *ledgerModel) back() bool {
	if !m.drilling() {
		return false
	}
	m.drill, m.roots = 0, nil
	m.dCursor, m.dOff = 0, 0
	return true
}

// maxDrillRoots caps the drill list. A bucket on a full machine has thousands
// of roots and the tail of that list is directories of a few kilobytes; the
// head is what the reader came for, and Browse is where the rest lives.
const maxDrillRoots = 500

// bucketRoots is a bucket's claim roots, largest first.
func (m *ledgerModel) bucketRoots(b classify.Bucket) []ledgerRoot {
	ids := m.class.Roots(b)
	out := make([]ledgerRoot, 0, len(ids))
	for _, id := range ids {
		n := m.nodeOf(id)
		if n == nil {
			continue
		}
		r := ledgerRoot{node: n, path: mac.DisplayPath(n.Path()), bytes: n.Bytes}
		if cl, ok := m.class.OfNode(n); ok {
			r.owner, r.reclaim, r.classified = cl.Owner, cl.Reclaim, true
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].bytes != out[j].bytes {
			return out[i].bytes > out[j].bytes
		}
		return out[i].path < out[j].path
	})
	if len(out) > maxDrillRoots {
		out = out[:maxDrillRoots]
	}
	return out
}

// nodeOf resolves a node id against the tree the classification was built on.
func (m *ledgerModel) nodeOf(id int32) *walk.Node {
	if m.tree == nil || id < 0 || int(id) >= len(m.tree.Nodes) {
		return nil
	}
	return m.tree.Nodes[id]
}

// used is what the bars and percentages are measured against: the volume's
// used space, so the twelve bars add up to a full disk rather than to the
// largest bucket. It is the same denominator the printed report uses.
func (m *ledgerModel) used() int64 {
	if m.l == nil {
		return 0
	}
	if u := m.l.Volume.UsedAfter; u > 0 {
		return u
	}
	return m.l.Scanned.Bytes
}

// Column widths of the bucket table. The note is the column that gives way.
const (
	bucketLabelWidth = 24
	reclaimBarWidth  = 8
	noteMin          = 10
)

// ledgerCols is the width of the columns that vary with the terminal.
type ledgerCols struct{ label, bar, rbar, note int }

// ledgerLayout divides the width. A narrow terminal loses the reclaim bar
// first and the note second, because both restate something the row already
// says in numbers.
func ledgerLayout(width int) ledgerCols {
	c := ledgerCols{label: bucketLabelWidth, bar: barWidth, rbar: reclaimBarWidth}
	fixed := func(rbar int) int {
		n := markerWidth + c.label + colGap + c.bar + colGap + bytesWidth + colGap + pctWidth + colGap + bytesWidth
		if rbar > 0 {
			n += rbar + colGap
		}
		return n
	}
	if width-fixed(c.rbar) < noteMin {
		c.rbar = 0
	}
	if rest := width - fixed(c.rbar) - colGap; rest >= noteMin {
		c.note = rest
	}
	if width < fixed(0) {
		c.bar = max(c.bar-(fixed(0)-width), 0)
		c.label = max(min(c.label, width-markerWidth-bytesWidth-pctWidth-bytesWidth-3*colGap), 8)
	}
	return c
}

// View renders whichever list is in front.
func (m *ledgerModel) View(st Styles, u units.Format) string {
	if m.l == nil || len(m.buckets()) == 0 {
		return st.Dim.Render("  no ledger yet")
	}
	if m.drilling() {
		return m.drillView(st, u)
	}
	return m.tableView(st, u)
}

// tableView is the twelve rows.
func (m *ledgerModel) tableView(st Styles, u units.Format) string {
	c := ledgerLayout(m.width)
	used := m.used()
	bs := m.buckets()

	var sb strings.Builder
	sb.WriteString(m.header(st, u, "LEDGER  every byte of used space, in one of twelve buckets", used))
	sb.WriteByte('\n')
	sb.WriteString(m.columnsLine(st, c))
	sb.WriteByte('\n')

	end := min(m.offset+m.height, len(bs))
	for i := m.offset; i < end; i++ {
		sb.WriteString(m.bucketRow(st, u, c, i, bs[i], used, i == m.cursor))
		if i < end-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// header is the title line with the total on the right.
func (m *ledgerModel) header(st Styles, u units.Format, title string, total int64) string {
	right := u.Bytes(total) + " used"
	left := truncate(title, max(m.width-len(right)-2, 1))
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return st.Crumb.Render(left) + strings.Repeat(" ", gap) + st.Dim.Render(right)
}

// columnsLine names the columns of the bucket table.
func (m *ledgerModel) columnsLine(st Styles, c ledgerCols) string {
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	sb.WriteString(padRight("bucket", c.label, "bucket"))
	if c.bar > 0 {
		sb.WriteString(" " + strings.Repeat(" ", c.bar))
	}
	sb.WriteString(" " + padLeft("size", bytesWidth, "size"))
	sb.WriteString(" " + padLeft("%", pctWidth, "%"))
	if c.rbar > 0 {
		sb.WriteString(" " + strings.Repeat(" ", c.rbar))
	}
	sb.WriteString(" " + padLeft("free", bytesWidth, "free"))
	if c.note > 0 {
		sb.WriteString(" " + "note")
	}
	return st.Dim.Render(strings.TrimRight(sb.String(), " "))
}

// bucketRow draws one of the twelve.
func (m *ledgerModel) bucketRow(st Styles, u units.Format, c ledgerCols, i int, b ledger.Bucket, used int64, selected bool) string {
	label := fmt.Sprintf("%2d %s", i+1, b.Label)
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	sb.WriteString(padRight(st.Label.Render(truncate(label, c.label)), c.label, truncate(label, c.label)))
	if c.bar > 0 {
		sb.WriteString(" " + bar(st, b.Bytes, used, c.bar))
	}
	size := u.Fixed(b.Bytes)
	if !b.Known {
		size = "unknown"
	}
	sb.WriteString(" " + padLeft(st.Value.Render(size), bytesWidth, size))
	pct := units.Percent(b.Bytes, used)
	sb.WriteString(" " + padLeft(st.Dim.Render(pct), pctWidth, pct))
	if c.rbar > 0 {
		sb.WriteString(" " + reclaimBar(st, b.Reclaimable, b.Bytes, c.rbar))
	}
	free := ""
	if b.Reclaimable > 0 {
		free = u.Fixed(b.Reclaimable)
	}
	sb.WriteString(" " + padLeft(st.Good.Render(free), bytesWidth, free))
	if c.note > 0 && b.Note != "" {
		note := truncate(b.Note, c.note)
		sb.WriteString(" " + st.Dim.Render(note))
	}
	line := strings.TrimRight(sb.String(), " ")
	if selected {
		return st.Selected.Render(stripStyles(line))
	}
	return line
}

// reclaimBar draws the reclaimable share of one bucket, which is a bar inside
// the bucket rather than inside the disk: the question it answers is "how
// much of this could I have back", not "how big is this".
func reclaimBar(st Styles, part, whole int64, width int) string {
	if width <= 0 {
		return ""
	}
	if part <= 0 {
		return st.Dim.Render(strings.Repeat(" ", width))
	}
	return bar(st, part, whole, width)
}

// drillView is one bucket's roots.
//
// A root's size is its whole subtree, which is larger than its share of the
// bucket whenever a rule inside it claims a different one: the pnpm cache
// under ~/Library is Developer, not application data. The header says what
// the bucket itself holds so the two numbers are never mistaken for each
// other, and the bars are shares of what is on screen.
func (m *ledgerModel) drillView(st Styles, u units.Format) string {
	title := "LEDGER › " + m.drill.Label()
	if len(m.roots) == maxDrillRoots {
		title += fmt.Sprintf("  (the largest %d)", maxDrillRoots)
	}
	var total int64
	for _, r := range m.roots {
		total += r.bytes
	}

	var sb strings.Builder
	sb.WriteString(m.drillHeader(st, u, title))
	sb.WriteByte('\n')
	sb.WriteString(m.drillColumns(st))
	sb.WriteByte('\n')
	if len(m.roots) == 0 {
		sb.WriteString(st.Dim.Render("  nothing on this machine is in this bucket"))
		return sb.String()
	}
	c := drillLayout(m.width)
	end := min(m.dOff+m.height, len(m.roots))
	for i := m.dOff; i < end; i++ {
		sb.WriteString(m.rootRow(st, u, c, m.roots[i], total, i == m.dCursor))
		if i < end-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// drillHeader names the bucket and what the ledger puts in it.
func (m *ledgerModel) drillHeader(st Styles, u units.Format, title string) string {
	right := fmt.Sprintf("%s in this bucket  %s", u.Bytes(m.drilledBytes()), roots(len(m.roots)))
	left := truncate(title, max(m.width-lipgloss.Width(right)-2, 1))
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return st.Crumb.Render(left) + strings.Repeat(" ", gap) + st.Dim.Render(right)
}

// drilledBytes is the ledger's own total for the bucket being drilled.
func (m *ledgerModel) drilledBytes() int64 {
	bs := m.buckets()
	if i := int(m.drill) - 1; i >= 0 && i < len(bs) {
		return bs[i].Bytes
	}
	return 0
}

// roots renders a count of drill-down entries with the right noun.
func roots(n int) string {
	if n == 1 {
		return "1 root"
	}
	return count(n) + " roots"
}

// drillLayout divides the width of the drill list between the path and the
// columns beside it.
func drillLayout(width int) columns {
	c := columns{bar: barWidth}
	fixed := markerWidth + bytesWidth + pctWidth + 3*colGap
	if width >= chipsMinWidth {
		c.owner, c.reclaim = ownerWidth, reclaimWidth
		fixed += c.owner + c.reclaim + 2*colGap
	}
	rest := width - fixed
	if rest < nameMin+barWidth+colGap {
		c.bar = max(rest-nameMin-colGap, 0)
	}
	c.name = max(rest-c.bar-colGap, nameMin)
	return c
}

// drillColumns names the columns of the drill list.
func (m *ledgerModel) drillColumns(st Styles) string {
	c := drillLayout(m.width)
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	sb.WriteString(padRight("path", c.name, "path"))
	if c.bar > 0 {
		sb.WriteString(" " + strings.Repeat(" ", c.bar))
	}
	sb.WriteString(" " + padLeft("size", bytesWidth, "size"))
	sb.WriteString(" " + padLeft("%", pctWidth, "%"))
	if c.owner > 0 {
		sb.WriteString(" " + padRight("owner", c.owner, "owner"))
	}
	if c.reclaim > 0 {
		sb.WriteString(" tag")
	}
	return st.Dim.Render(strings.TrimRight(sb.String(), " "))
}

// rootRow draws one line of the drill list.
func (m *ledgerModel) rootRow(st Styles, u units.Format, c columns, r ledgerRoot, total int64, selected bool) string {
	path := truncateLeft(r.path, c.name)
	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth))
	sb.WriteString(padRight(st.Dir.Render(path), c.name, path))
	if c.bar > 0 {
		sb.WriteString(" " + bar(st, r.bytes, total, c.bar))
	}
	size := u.Fixed(r.bytes)
	sb.WriteString(" " + padLeft(st.Value.Render(size), bytesWidth, size))
	pct := units.Percent(r.bytes, total)
	sb.WriteString(" " + padLeft(st.Dim.Render(pct), pctWidth, pct))
	if c.owner > 0 {
		owner := truncate(r.owner, c.owner)
		sb.WriteString(" " + padRight(st.Dim.Render(owner), c.owner, owner))
	}
	if c.reclaim > 0 {
		tag, style := "", st.ChipDim
		if r.classified {
			tag, style = reclaimChip(st, r.reclaim)
		}
		sb.WriteString(" " + style.Render(tag))
	}
	line := strings.TrimRight(sb.String(), " ")
	if selected {
		return st.Selected.Render(stripStyles(line))
	}
	return line
}
