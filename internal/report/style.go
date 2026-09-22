package report

import (
	"io"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// styles are the report's palette.
//
// Colors are ANSI indices rather than hex so they follow whatever theme the
// terminal is set to, and every style is a plain one when color is off: a
// plain lipgloss.Style renders its argument unchanged, which is what makes
// the golden files readable and diffable.
type styles struct {
	title    lipgloss.Style
	label    lipgloss.Style
	note     lipgloss.Style
	num      lipgloss.Style
	good     lipgloss.Style
	warn     lipgloss.Style
	bar      lipgloss.Style
	barEmpty lipgloss.Style
	rule     lipgloss.Style
}

// newStyles builds the palette. With color off every style is the identity.
func newStyles(color bool) styles {
	plain := lipgloss.NewStyle()
	s := styles{plain, plain, plain, plain, plain, plain, plain, plain, plain}
	if !color {
		return s
	}
	s.title = plain.Bold(true)
	s.note = plain.Foreground(lipgloss.Color("8"))
	s.num = plain.Bold(true)
	s.good = plain.Foreground(lipgloss.Color("2"))
	s.warn = plain.Foreground(lipgloss.Color("3"))
	s.bar = plain.Foreground(lipgloss.Color("4"))
	s.barEmpty = plain.Foreground(lipgloss.Color("8"))
	s.rule = plain.Foreground(lipgloss.Color("8"))
	return s
}

// cell is one table cell: plain text plus the style to render it in. The text
// stays plain until render time so that column widths are measured on the
// characters a reader sees, not on escape sequences.
type cell struct {
	text string
	st   lipgloss.Style
}

// tableRow is a row of cells, or a horizontal rule.
type tableRow struct {
	cells []cell
	rule  bool
}

// table lays out columns without a tabwriter, which cannot measure styled
// text: widths come from lipgloss.Width and padding is applied before the
// style, so a colored report lines up exactly like a plain one.
type table struct {
	indent string
	gap    int
	right  []bool // per column: right-align
	rows   []tableRow
	st     styles
}

// tableIndent is the left margin every table in the report shares.
const tableIndent = "  "

// newTable starts a table. right marks the columns to right-align.
func newTable(st styles, right ...bool) *table {
	return &table{indent: tableIndent, gap: 2, right: right, st: st}
}

// add appends a row.
func (t *table) add(cells ...cell) { t.rows = append(t.rows, tableRow{cells: cells}) }

// rule appends a horizontal line the width of the table.
func (t *table) rule() { t.rows = append(t.rows, tableRow{rule: true}) }

// isRight reports whether column i is right-aligned.
func (t *table) isRight(i int) bool { return i < len(t.right) && t.right[i] }

// minFlexWidth is the narrowest a column may be squeezed to before the table
// is allowed to run past the page.
const minFlexWidth = 16

// render writes the table. Trailing blanks are trimmed so the golden files
// carry no invisible whitespace.
func (t *table) render(w io.Writer) {
	widths := t.measure()
	t.fit(widths)
	total := 0
	for i, n := range widths {
		total += n
		if i > 0 {
			total += t.gap
		}
	}

	var b strings.Builder
	for _, r := range t.rows {
		b.Reset()
		b.WriteString(t.indent)
		if r.rule {
			b.WriteString(t.st.rule.Render(strings.Repeat("─", total)))
			writeLine(w, b.String())
			continue
		}
		last := lastNonEmpty(r.cells)
		for i, c := range r.cells {
			if i > 0 {
				b.WriteString(strings.Repeat(" ", t.gap))
			}
			text := clip(c.text, widths[i])
			pad := widths[i] - lipgloss.Width(text)
			switch {
			case i > last:
				// Nothing to the right: no need to pad the line out.
				b.WriteString(c.st.Render(text))
			case t.isRight(i):
				b.WriteString(strings.Repeat(" ", pad) + c.st.Render(text))
			default:
				b.WriteString(c.st.Render(text) + strings.Repeat(" ", pad))
			}
		}
		writeLine(w, strings.TrimRight(b.String(), " "))
	}
}

// measure returns the natural width of every column.
func (t *table) measure() []int {
	var widths []int
	for _, r := range t.rows {
		for i, c := range r.cells {
			for len(widths) <= i {
				widths = append(widths, 0)
			}
			if n := lipgloss.Width(c.text); n > widths[i] {
				widths[i] = n
			}
		}
	}
	return widths
}

// fit squeezes the table onto the page by narrowing its widest prose column,
// which is the notes column in practice. Numbers and the names beside them
// are never clipped: a truncated path or byte count would be a lie, while a
// truncated note is visibly an abbreviation.
func (t *table) fit(widths []int) {
	over := len(t.indent) - textWidth
	for i, n := range widths {
		over += n
		if i > 0 {
			over += t.gap
		}
	}
	for over > 0 {
		widest, at := 0, -1
		for i, n := range widths {
			if !t.isRight(i) && i > 0 && n > widest {
				widest, at = n, i
			}
		}
		if at < 0 || widest <= minFlexWidth {
			return
		}
		cut := min(over, widest-minFlexWidth)
		widths[at] -= cut
		over -= cut
	}
}

// clip shortens s to w columns, marking the cut with an ellipsis.
func clip(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if w == 1 {
		return "…"
	}
	return strings.TrimRight(string(r[:w-1]), " ") + "…"
}

// lastNonEmpty is the index of the last cell with text in it.
func lastNonEmpty(cells []cell) int {
	last := -1
	for i, c := range cells {
		if c.text != "" {
			last = i
		}
	}
	return last
}

// writeLine writes one line, ignoring the error: a report writes to a
// terminal or a buffer, and the caller's own writer reports a broken pipe.
func writeLine(w io.Writer, s string) {
	_, _ = io.WriteString(w, s+"\n")
}

// bar draws a proportional bar of the given width.
func bar(st styles, part, whole int64, width int) string {
	if whole <= 0 || part <= 0 {
		return st.barEmpty.Render(strings.Repeat("░", width))
	}
	filled := int((float64(part)/float64(whole))*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	if filled == 0 && part > 0 {
		filled = 1
	}
	return st.bar.Render(strings.Repeat("█", filled)) +
		st.barEmpty.Render(strings.Repeat("░", width-filled))
}

// count formats an integer with thousands separators, which is the only way
// a seven-digit file count is readable at a glance.
func count[T int | int64 | uint32 | uint64](n T) string {
	s := strconv.FormatInt(int64(n), 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
