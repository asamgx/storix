package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// browseModel is the directory table: which directory is open, where the
// cursor is, and how the rows are ordered.
//
// The row list is cached against the directory and the options that produce
// it, so moving the cursor costs a render of the visible window and nothing
// else; a directory of twenty thousand entries is sorted once.
type browseModel struct {
	root   *walk.Node
	dir    *walk.Node
	trail  []position // cursor and offset of every ancestor, innermost last
	cursor int
	offset int
	opts   rowOpts

	rows     []row
	cachedOn *walk.Node
	cachedBy rowOpts
	valid    bool

	// filter is the prompt shown while the user is typing one; filtering
	// says the keyboard belongs to it.
	filter    textinput.Model
	filtering bool

	// height is how many rows fit on the screen, set by the root model.
	height int
	width  int
}

// position remembers where the cursor was in a directory the user left.
type position struct {
	dir    *walk.Node
	cursor int
	offset int
}

// newBrowse opens a tree at its root.
func newBrowse(root *walk.Node) browseModel {
	return browseModel{root: root, dir: root, opts: rowOpts{sort: sortSize}, height: 1, width: 80}
}

// setTree points the browser at a new tree, staying on the same directory
// when the rescan still has it.
func (b *browseModel) setTree(root *walk.Node) {
	if root == nil {
		return
	}
	path := ""
	if b.dir != nil {
		path = b.dir.Path()
	}
	b.root = root
	b.dir = root
	b.trail = nil
	b.cursor, b.offset = 0, 0
	b.valid = false
	if path == "" {
		return
	}
	// Reopen the directory the user was looking at, rebuilding the trail so
	// that backspace still walks back up.
	if n, ok := lookup(root, path); ok && n != root {
		b.openPath(n)
	}
}

// openPath descends from the root to n, recording the trail.
func (b *browseModel) openPath(n *walk.Node) {
	var chain []*walk.Node
	for p := n; p != nil && p != b.root; p = p.Parent {
		chain = append(chain, p)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		b.trail = append(b.trail, position{dir: b.dir, cursor: b.cursor, offset: b.offset})
		b.dir = chain[i]
		b.cursor, b.offset = 0, 0
		b.valid = false
	}
}

// lookup finds a node by scan path inside a tree whose Tree wrapper is not
// at hand; the browse model only ever holds the root node.
func lookup(root *walk.Node, path string) (*walk.Node, bool) {
	t := &walk.Tree{Root: root}
	return t.Lookup(path)
}

// visible returns the rows, building them when the cache is cold.
func (b *browseModel) visible() []row {
	if b.valid && b.cachedOn == b.dir && b.cachedBy == b.opts {
		return b.rows
	}
	b.rows = buildRows(b.dir, b.opts)
	b.cachedOn, b.cachedBy, b.valid = b.dir, b.opts, true
	if b.cursor >= len(b.rows) {
		b.cursor = max(len(b.rows)-1, 0)
	}
	b.clampOffset()
	return b.rows
}

// selected is the row under the cursor.
func (b *browseModel) selected() (row, bool) {
	rows := b.visible()
	if b.cursor < 0 || b.cursor >= len(rows) {
		return row{}, false
	}
	return rows[b.cursor], true
}

// move shifts the cursor by n rows and keeps it on screen.
func (b *browseModel) move(n int) {
	rows := b.visible()
	if len(rows) == 0 {
		b.cursor, b.offset = 0, 0
		return
	}
	b.cursor = min(max(b.cursor+n, 0), len(rows)-1)
	b.clampOffset()
}

// moveTo places the cursor on an absolute row.
func (b *browseModel) moveTo(i int) {
	rows := b.visible()
	if len(rows) == 0 {
		return
	}
	b.cursor = min(max(i, 0), len(rows)-1)
	b.clampOffset()
}

// clampOffset scrolls the window so the cursor is inside it.
func (b *browseModel) clampOffset() {
	h := max(b.height, 1)
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.cursor >= b.offset+h {
		b.offset = b.cursor - h + 1
	}
	if maxOff := max(len(b.rows)-h, 0); b.offset > maxOff {
		b.offset = maxOff
	}
	if b.offset < 0 {
		b.offset = 0
	}
}

// open descends into the selected directory.
//
// A filter belongs to the directory it was typed in: carrying "Applications"
// into /Applications would show an empty table, so navigating clears it.
func (b *browseModel) open() bool {
	r, ok := b.selected()
	if !ok || !r.openable || r.node == nil {
		return false
	}
	b.trail = append(b.trail, position{dir: b.dir, cursor: b.cursor, offset: b.offset})
	b.dir = r.node
	b.cursor, b.offset = 0, 0
	b.clearFilter()
	b.valid = false
	return true
}

// clearFilter drops the filter and its prompt.
func (b *browseModel) clearFilter() {
	b.opts.filter = ""
	b.filtering = false
}

// parent goes back up, restoring the cursor the user left behind.
func (b *browseModel) parent() bool {
	if len(b.trail) == 0 {
		return false
	}
	p := b.trail[len(b.trail)-1]
	b.trail = b.trail[:len(b.trail)-1]
	b.dir, b.cursor, b.offset = p.dir, p.cursor, p.offset
	b.clearFilter()
	b.valid = false
	return true
}

// sortBy switches the sort column, flipping the direction when the column is
// already the one in use.
func (b *browseModel) sortBy(k sortKey) {
	if b.opts.sort == k {
		b.opts.desc = !b.opts.desc
	} else {
		b.opts.sort, b.opts.desc = k, false
	}
	b.valid = false
	b.cursor, b.offset = 0, 0
}

// sortName is how the status line names the order in force. Modification
// time has no column of its own, so the status line is the only place the
// order can be read; the others mark their column as well.
func (b *browseModel) sortLabel() string {
	dir := "largest first"
	switch {
	case b.opts.sort == sortName && !b.opts.desc:
		dir = "A to Z"
	case b.opts.sort == sortName:
		dir = "Z to A"
	case b.opts.sort == sortMtime && !b.opts.desc:
		dir = "newest first"
	case b.opts.sort == sortMtime:
		dir = "oldest first"
	case b.opts.desc:
		dir = "smallest first"
	}
	return "sorted by " + b.opts.sort.String() + ", " + dir
}

// setFilter narrows the directory to the names containing s.
func (b *browseModel) setFilter(s string) {
	if b.opts.filter == s {
		return
	}
	b.opts.filter = s
	b.valid = false
	b.cursor, b.offset = 0, 0
}

// toggleApparent switches between allocated and apparent bytes.
func (b *browseModel) toggleApparent() { b.opts.apparent = !b.opts.apparent; b.valid = false }

// toggleBundles switches whether the cursor may descend into bundles.
func (b *browseModel) toggleBundles() { b.opts.bundles = !b.opts.bundles; b.valid = false }

// setSize records the space the table has, in rows of the table itself.
func (b *browseModel) setSize(w, h int) {
	b.width, b.height = w, max(h, 1)
	b.clampOffset()
}

// total is what the bars and percentages are measured against: the open
// directory's own total, so a row's bar is its share of what is on screen.
func (b *browseModel) total() int64 {
	if b.dir == nil {
		return 0
	}
	if b.opts.apparent {
		return b.dir.Apparent
	}
	return b.dir.Bytes
}

// header is the breadcrumb line: where the cursor is, what the directory
// holds, and where the numbers came from.
func (b *browseModel) header(st Styles, u units.Format, source string) string {
	if b.dir == nil {
		return ""
	}
	crumb := mac.DisplayPath(b.dir.Path())
	right := fmt.Sprintf("%s  %s  %s", u.Bytes(b.total()), files(b.dir.Files), source)
	left := truncateLeft(crumb, max(b.width-lipgloss.Width(right)-2, 1))
	gap := max(b.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return st.Crumb.Render(left) + strings.Repeat(" ", gap) + st.Dim.Render(right)
}

// columnsLine names the columns and marks the sort.
func (b *browseModel) columnsLine(st Styles) string {
	c := layout(b.width)
	arrow := "▼"
	if b.opts.desc {
		arrow = "▲"
	}
	head := func(k sortKey, label string) string {
		if b.opts.sort == k {
			return label + arrow
		}
		return label
	}
	size := "size"
	if b.opts.apparent {
		size = "apparent"
	}
	name, bytes, fileCount := head(sortName, "name"), head(sortSize, size), head(sortCount, "files")

	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", markerWidth+colGap))
	sb.WriteString(padRight(truncate(name, c.name), c.name, truncate(name, c.name)))
	if c.bar > 0 {
		sb.WriteString(" " + strings.Repeat(" ", c.bar))
	}
	sb.WriteString(" " + padLeft(bytes, bytesWidth, bytes))
	sb.WriteString(" " + padLeft("%", pctWidth, "%"))
	sb.WriteString(" " + padLeft(fileCount, filesWidth, fileCount))
	return st.Dim.Render(strings.TrimRight(sb.String(), " "))
}

// View renders the table: the breadcrumb, the column names, and the rows
// that fit. Only the visible window is rendered, so a directory of twenty
// thousand entries costs the same as one of twenty.
func (b *browseModel) View(st Styles, u units.Format, source string) string {
	rows := b.visible()
	total := b.total()
	c := layout(b.width)

	var sb strings.Builder
	sb.WriteString(b.header(st, u, source))
	sb.WriteByte('\n')
	sb.WriteString(b.columnsLine(st))
	sb.WriteByte('\n')

	if len(rows) == 0 {
		sb.WriteString(st.Dim.Render(b.emptyText()))
		return sb.String()
	}
	end := min(b.offset+b.height, len(rows))
	for i := b.offset; i < end; i++ {
		sb.WriteString(renderRow(st, u, rows[i], c, total, i == b.cursor))
		if i < end-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// emptyText says why a directory shows nothing.
func (b *browseModel) emptyText() string {
	switch {
	case b.opts.filter != "":
		return "  no entry here matches " + b.opts.filter
	case b.dir != nil && b.dir.Has(walk.FlagUnreadable):
		return "  this directory could not be read"
	default:
		return "  this directory is empty"
	}
}

// files renders a file count with the right noun.
func files[T int | int64 | uint32 | uint64](n T) string {
	if n == 1 {
		return "1 file"
	}
	return count(n) + " files"
}
