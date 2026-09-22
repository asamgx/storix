package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// sortKey is the column the rows of a directory are ordered by.
type sortKey uint8

const (
	sortSize sortKey = iota
	sortName
	sortMtime
	sortCount
)

func (k sortKey) String() string {
	switch k {
	case sortName:
		return "name"
	case sortMtime:
		return "modified"
	case sortCount:
		return "files"
	default:
		return "size"
	}
}

// Markers in the first column. They are single-width runes so the table
// lines up whatever the row carries.
const (
	markerBundle   = "▣" // a directory macOS presents as one object
	markerAlias    = "⇄" // a hard link whose bytes belong to another path
	markerDataless = "☁" // content in the cloud, no local blocks
	markerPartial  = "!" // an unreadable or partly unreadable subtree
)

// row is one line of the browse table. A row with no node is the synthetic
// aggregate of the directory's small files, which have no node of their own.
type row struct {
	node     *walk.Node
	name     string
	bytes    int64
	files    uint32
	mtime    int64
	mark     string
	partial  bool
	dir      bool // a directory the cursor can descend into
	openable bool // a directory that is not a bundle, or bundles are on
}

// rowOpts is everything that decides which rows a directory has and in what
// order. It is the cache key of the row list.
type rowOpts struct {
	sort     sortKey
	desc     bool
	filter   string
	apparent bool // show apparent bytes instead of allocated
	bundles  bool // let the cursor descend into bundles
}

// buildRows lists a directory's children as table rows, filtered and sorted.
//
// The small files the walker folded into the directory are shown as one
// synthetic row rather than left out: a directory whose bytes are a hundred
// thousand tiny files should say so instead of looking empty.
func buildRows(dir *walk.Node, o rowOpts) []row {
	if dir == nil {
		return nil
	}
	filter := strings.ToLower(o.filter)
	rows := make([]row, 0, len(dir.Children)+1)
	for _, c := range dir.Children {
		if filter != "" && !strings.Contains(strings.ToLower(c.Name), filter) {
			continue
		}
		rows = append(rows, nodeRow(c, o))
	}
	if filter == "" && dir.Small.Files > 0 {
		rows = append(rows, smallRow(dir, o))
	}
	sortRows(rows, o)
	return rows
}

// nodeRow describes one child.
func nodeRow(n *walk.Node, o rowOpts) row {
	r := row{
		node:    n,
		name:    n.Name,
		bytes:   n.Bytes,
		files:   n.Files,
		mtime:   n.Mtime,
		partial: n.Has(walk.FlagPartial) || n.Has(walk.FlagUnreadable),
		dir:     n.IsDir(),
	}
	if o.apparent {
		r.bytes = n.Apparent
	}
	switch {
	case n.Has(walk.FlagBundle):
		r.mark = markerBundle
	case n.Has(walk.FlagLinkAlias):
		r.mark = markerAlias
	case n.Has(walk.FlagDataless):
		r.mark = markerDataless
	}
	r.openable = r.dir && (o.bundles || !n.Has(walk.FlagBundle))
	return r
}

// smallRow is the aggregate of the leaves that never got a node.
func smallRow(dir *walk.Node, o rowOpts) row {
	s := dir.Small
	bytes := s.Bytes
	if o.apparent {
		bytes = s.Apparent
	}
	name := fmt.Sprintf("… %s small files", count(s.Files))
	if s.Files == 1 {
		name = "… 1 small file"
	}
	return row{name: name, bytes: bytes, files: s.Files}
}

// sortRows orders rows by the chosen column, with the name breaking ties so
// that two runs of the same scan list a directory identically.
func sortRows(rows []row, o rowOpts) {
	less := func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch o.sort {
		case sortName:
			if a.name != b.name {
				return a.name < b.name
			}
		case sortMtime:
			if a.mtime != b.mtime {
				return a.mtime > b.mtime
			}
		case sortCount:
			if a.files != b.files {
				return a.files > b.files
			}
		default:
			if a.bytes != b.bytes {
				return a.bytes > b.bytes
			}
		}
		return a.name < b.name
	}
	if o.desc {
		sort.SliceStable(rows, func(i, j int) bool { return less(j, i) })
		return
	}
	sort.SliceStable(rows, less)
}

// Column widths. The bar is the only column that gives way on a narrow
// terminal, and the name column never falls below nameMin.
const (
	markerWidth = 2
	bytesWidth  = 9
	pctWidth    = 6
	filesWidth  = 10
	barWidth    = 14
	nameMin     = 12
	colGap      = 1
)

// columns is the width of the two flexible columns at a given terminal width.
type columns struct{ name, bar int }

// layout divides the width between the name and the bar.
func layout(width int) columns {
	fixed := markerWidth + bytesWidth + pctWidth + filesWidth + 4*colGap
	rest := width - fixed
	c := columns{bar: barWidth}
	if rest < nameMin+barWidth+colGap {
		c.bar = max(rest-nameMin-colGap, 0)
	}
	c.name = rest - c.bar
	if c.bar > 0 {
		c.name -= colGap
	}
	if c.name < nameMin {
		c.name = nameMin
	}
	return c
}

// renderRow draws one table row. A selected row is drawn in reverse video as
// one piece, so the highlight covers the whole line rather than stopping at
// the first styled column.
func renderRow(st Styles, u units.Format, r row, c columns, total int64, selected bool) string {
	name := r.name
	if r.dir {
		name += "/"
	}
	var b strings.Builder
	b.WriteString(pad(st.Marker.Render(r.mark), 1))
	b.WriteString(pad(partialStyle(st, r.partial).Render(markerIf(r.partial)), 1))
	b.WriteString(" ")
	b.WriteString(padRight(nameStyle(st, r).Render(truncate(name, c.name)), c.name, truncate(name, c.name)))
	if c.bar > 0 {
		b.WriteString(" ")
		b.WriteString(bar(st, r.bytes, total, c.bar))
	}
	b.WriteString(" ")
	b.WriteString(padLeft(st.Value.Render(u.Fixed(r.bytes)), bytesWidth, u.Fixed(r.bytes)))
	b.WriteString(" ")
	b.WriteString(padLeft(st.Dim.Render(units.Percent(r.bytes, total)), pctWidth, units.Percent(r.bytes, total)))
	b.WriteString(" ")
	files := count(r.files)
	b.WriteString(padLeft(st.Dim.Render(files), filesWidth, files))
	line := b.String()
	if selected {
		return st.Selected.Render(stripStyles(line))
	}
	return line
}

// markerIf returns the partial marker when it applies.
func markerIf(partial bool) string {
	if partial {
		return markerPartial
	}
	return ""
}

func partialStyle(st Styles, partial bool) lipgloss.Style {
	if partial {
		return st.Warn
	}
	return st.Dim
}

// nameStyle colors a row by what it is.
func nameStyle(st Styles, r row) lipgloss.Style {
	switch {
	case r.node == nil:
		return st.Dim
	case r.dir:
		return st.Dir
	default:
		return st.Label
	}
}

// stripStyles removes the ANSI sequences from a rendered line so that the
// selection style applies to the whole of it.
func stripStyles(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	esc := false
	for _, r := range s {
		switch {
		case esc && (r == 'm' || r == 'K'):
			esc = false
		case esc:
		case r == 0x1b:
			esc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pad, padLeft and padRight align a styled cell by measuring the plain text
// it was rendered from: lipgloss.Width of styled text is right, but the
// plain text is cheaper and the two agree.
func pad(styled string, w int) string { return padRight(styled, w, stripStyles(styled)) }

func padRight(styled string, w int, plain string) string {
	if n := w - lipgloss.Width(plain); n > 0 {
		return styled + strings.Repeat(" ", n)
	}
	return styled
}

func padLeft(styled string, w int, plain string) string {
	if n := w - lipgloss.Width(plain); n > 0 {
		return strings.Repeat(" ", n) + styled
	}
	return styled
}

// truncate shortens s to w columns, marking the cut with an ellipsis. The
// head of a file name is the part that identifies it, so the tail goes.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if w == 1 {
		return "…"
	}
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// truncateLeft shortens a path from the left, which is where a breadcrumb
// has its least interesting part.
func truncateLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}

// bar draws a proportional bar.
func bar(st Styles, part, whole int64, width int) string {
	if width <= 0 {
		return ""
	}
	if whole <= 0 || part <= 0 {
		return st.BarEmpty.Render(strings.Repeat("░", width))
	}
	filled := int((float64(part)/float64(whole))*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	if filled == 0 {
		filled = 1
	}
	return st.Bar.Render(strings.Repeat("█", filled)) + st.BarEmpty.Render(strings.Repeat("░", width-filled))
}

// count formats an integer with thousands separators.
func count[T int | int64 | uint32 | uint64](n T) string {
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
