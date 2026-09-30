package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/reclaim"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
)

// dashModel is the landing view (D53): how full the disk is, how much could
// be got back and at what cost, the few biggest wins, and what storix could
// not see.
//
// Every number on it comes from the scan result or the reclaim plan the Plan
// view shows; the dashboard only lays them out. The five top wins are the
// only selectable rows, and enter opens one in the Plan view.
type dashModel struct {
	res  *scan.Result
	plan *reclaim.Plan
	top  []reclaim.Item

	cursor        int
	width, height int
}

// dashTopWins is how many of the plan's largest items the dashboard lists.
const dashTopWins = 5

// dashTwoColumns is the width at which the attention and blind-spot sections
// move beside the others.
const dashTwoColumns = 140

func newDashboard() dashModel { return dashModel{width: 80, height: 1} }

// setResult takes the scan and the plan built from it.
func (m *dashModel) setResult(res *scan.Result, p *reclaim.Plan) {
	m.res, m.plan = res, p
	m.top = topWins(p, dashTopWins)
	m.cursor = min(m.cursor, max(len(m.top)-1, 0))
}

// topWins are the largest items of the tiers the plan suggests.
func topWins(p *reclaim.Plan, n int) []reclaim.Item {
	if p == nil {
		return nil
	}
	var out []reclaim.Item
	for _, it := range p.Items {
		if it.Tier.Suggested() && it.Bytes > 0 {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (m *dashModel) setSize(w, h int) { m.width, m.height = w, max(h, 1) }

func (m *dashModel) move(n int) {
	if len(m.top) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+n, 0), len(m.top)-1)
}

func (m *dashModel) moveTo(i int) {
	if len(m.top) == 0 {
		return
	}
	m.cursor = min(max(i, 0), len(m.top)-1)
}

// selected is the top win under the cursor.
func (m *dashModel) selected() (reclaim.Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.top) {
		return reclaim.Item{}, false
	}
	return m.top[m.cursor], true
}

func (m *dashModel) selectedPath() (string, bool) {
	it, ok := m.selected()
	if !ok || it.Path == "" {
		return "", false
	}
	return mac.ScanPath(it.Path), true
}

// View draws the dashboard.
func (m *dashModel) View(st Styles, u units.Format) string {
	if m.res == nil || m.plan == nil {
		return st.Dim.Render("  nothing to show until the scan finishes")
	}
	w := m.width
	main := [][]string{m.disk(st, u, w), m.reclaim(st, u, w), m.wins(st, u, w)}
	side := [][]string{m.attention(st, u, w), m.blindSpots(st, w)}

	if w >= dashTwoColumns {
		lw := w * 3 / 5
		rw := w - lw - 2
		main = [][]string{m.disk(st, u, lw), m.reclaim(st, u, lw), m.wins(st, u, lw)}
		side = [][]string{m.attention(st, u, rw), m.blindSpots(st, rw)}
		left := fit(st, main, m.height, 2)
		right := fit(st, side, m.height, 0)
		return lipgloss.JoinHorizontal(lipgloss.Top,
			block(strings.Join(left, "\n"), lw, len(left)), "  ",
			strings.Join(right, "\n"))
	}
	return strings.Join(fit(st, append(main, side...), m.height, 2), "\n")
}

// fit stacks sections with a blank line between them, dropping them from the
// bottom when the height runs out. The first keep sections always stay,
// clipped if they must; a dropped section leaves a line saying where the
// rest is.
func fit(st Styles, sections [][]string, height, keep int) []string {
	var out []string
	for i, sec := range sections {
		if len(sec) == 0 {
			continue
		}
		need := len(sec)
		if len(out) > 0 {
			need++
		}
		if len(out)+need > height && i >= keep {
			more := st.Dim.Render("  … more in the Ledger (1) and Plan (7) views")
			// A section that does not fit whole still shows its head
			// when there is room for the title and a line of it: the
			// biggest win is worth more than a blank screen.
			room := height - len(out) - 2 // the blank before it, the "more" line
			if room >= 2 {
				out = append(out, "")
				out = append(out, sec[:min(room, len(sec))]...)
			}
			if len(out) < height {
				out = append(out, more)
			} else if len(out) > 0 {
				out[len(out)-1] = more
			}
			return out
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, sec...)
	}
	if len(out) > height {
		out = out[:height]
	}
	return out
}

// disk is how full the container is and where the numbers came from.
func (m *dashModel) disk(st Styles, u units.Format, w int) []string {
	l := m.res.Ledger
	if l == nil {
		return nil
	}
	lines := []string{st.Title.Render("DISK") + "  " + text(st.Dim, m.source(), w-6)}
	c := l.Container
	barW := min(max(w-4, 10), 60)
	if c.Known && c.Total > 0 {
		lines = append(lines, "  "+bar(st, c.Used, c.Total, barW))
		lines = append(lines, fmt.Sprintf("  %s used of %s · %s free",
			st.Value.Render(u.Bytes(c.Used)), u.Bytes(c.Total), st.Good.Render(u.Bytes(c.Free))))
		return lines
	}
	lines = append(lines, fmt.Sprintf("  %s scanned; the container's size is unknown for this volume",
		st.Value.Render(u.Bytes(l.Scanned.Bytes))))
	return lines
}

// source says whether the scan is live or read from the cache, and how old.
func (m *dashModel) source() string {
	if m.res.FromCache {
		return "from the stored scan, " + ageText(m.res.CacheAge) + " old · r rescans"
	}
	return "live scan · r rescans"
}

// ageText is a scan's age in the one unit that reads naturally.
func ageText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// reclaim is the headline: what the suggested tiers free, each byte once,
// and what each tier costs.
func (m *dashModel) reclaim(st Styles, u units.Format, w int) []string {
	p := m.plan
	free := p.Freeable()
	lines := []string{st.Title.Render("RECLAIMABLE") + "  " + text(st.Dim, "read-only; nothing has been changed", w-13)}
	lines = append(lines, "  "+st.Good.Render(u.Bytes(free))+" could be freed, each byte counted once")

	barW := min(max(w-44, 8), 30)
	var after, reported int64
	for _, t := range reclaim.TierOrder() {
		tot := p.Totals[t]
		if !t.Suggested() || tot == nil {
			continue
		}
		after += tot.AfterTrash
		reported += tot.Reported
		if tot.Items == 0 {
			continue
		}
		label := padRight(st.Label.Render(t.Label()), 12, t.Label())
		size := u.Fixed(tot.Walked)
		lines = append(lines, fmt.Sprintf("  %s %s %s  %s",
			label, bar(st, tot.Walked, free, barW),
			padLeft(st.Value.Render(size), bytesWidth, size),
			st.Dim.Render(fmt.Sprintf("%d %s", tot.Items, pluralWord(tot.Items, "item", "items")))))
	}
	if after > 0 {
		lines = append(lines, text(st.Dim, "  "+u.Bytes(after)+" of it only once the Trash is emptied", w))
	}
	if reported > 0 {
		lines = append(lines, text(st.Dim, "  ~"+u.Bytes(reported)+" more by the tools' own figures (brew cleanup, docker)", w))
	}
	if kept := m.viaTool(); kept > 0 {
		lines = append(lines, text(st.Dim, "  "+u.Bytes(kept)+" more is kept by its tool and not suggested (Ledger, via tool)", w))
	}
	return lines
}

// viaTool is the ledger's tool-managed total, which the ledger view shows in
// its own column. The Trash is left out: its bytes are tool-managed in the
// ledger (Finder empties it), but the plan already lists it as an item.
func (m *dashModel) viaTool() int64 {
	var n int64
	if m.res.Ledger != nil {
		for _, b := range m.res.Ledger.Buckets {
			if b.ID != classify.BucketTrash.ID() {
				n += b.ToolManaged
			}
		}
	}
	return n
}

// wins lists the largest suggested items with what frees each.
func (m *dashModel) wins(st Styles, u units.Format, w int) []string {
	if len(m.top) == 0 {
		return []string{st.Title.Render("TOP WINS"), st.Dim.Render("  the plan has nothing to suggest")}
	}
	lines := []string{st.Title.Render("TOP WINS") + "  " + text(st.Dim, "enter opens it in the Plan, w explains it", w-10)}
	chip := 12
	what := 0
	if w >= chipsMinWidth {
		what = min(w/3, 34)
	}
	name := max(w-markerWidth-bytesWidth-chip-what-4, nameMin)
	for i, it := range m.top {
		size := u.Fixed(it.Bytes)
		if it.Reported {
			size = "~" + strings.TrimSpace(size)
		}
		title := truncate(it.Title, name)
		var sb strings.Builder
		sb.WriteString(strings.Repeat(" ", markerWidth))
		sb.WriteString(padLeft(st.Value.Render(size), bytesWidth, size))
		sb.WriteString(" " + padRight(st.Label.Render(title), name, title))
		tier := it.Tier.Label()
		sb.WriteString(" " + padRight(st.Chip.Render(tier), chip, tier))
		if what > 0 {
			do := it.Command
			if do == "" {
				do = it.Action
			}
			sb.WriteString(" " + st.Dim.Render(truncate(do, what)))
		}
		lines = append(lines, finishRow(st, sb.String(), i == m.cursor))
	}
	return lines
}

// attention is what the reader should look at before anything else: the
// software that is gone, the lines that would bring it back, the Trash.
func (m *dashModel) attention(st Styles, u units.Format, w int) []string {
	lines := []string{st.Title.Render("NEEDS ATTENTION")}
	if rep, ok := scan.Apps(m.res); ok {
		gone := len(rep.Orphans) + len(rep.CaskOnly) + len(rep.InTrash)
		if gone > 0 {
			lines = append(lines, text(st.Warn, fmt.Sprintf("  %d %s left data behind (%s) · Apps (3)",
				gone, pluralWord(gone, "removed app", "removed apps"), u.Bytes(rep.NeedsAttention())), w))
		}
	}
	if n := len(m.plan.Edits); n > 0 {
		lines = append(lines, text(st.Warn, fmt.Sprintf("  %d %s in your dotfiles would reinstall removed software · Plan (7)",
			n, pluralWord(n, "line", "lines")), w))
	}
	switch tr := m.plan.Trash; {
	case !tr.Known:
		lines = append(lines, text(st.Warn, "  the Trash could not be read; grant Full Disk Access to see it", w))
	case tr.Bytes > 0:
		lines = append(lines, text(st.Label, fmt.Sprintf("  the Trash holds %s; emptying it frees that", u.Bytes(tr.Bytes)), w))
	}
	if len(lines) == 1 {
		lines = append(lines, st.Good.Render("  nothing needs attention"))
	}
	return lines
}

// blindSpots is what the scan could not see, so the numbers above are read
// as the lower bounds they are.
func (m *dashModel) blindSpots(st Styles, w int) []string {
	var lines []string
	if l := m.res.Ledger; l != nil {
		for _, h := range l.Hints {
			lines = append(lines, text(st.Dim, "  • "+firstSentence(h.Text), w))
		}
	}
	bad := 0
	for _, d := range m.res.Detectors {
		if d.State == detect.Degraded || d.State == detect.Panic || d.State == detect.Timeout {
			bad++
		}
	}
	if bad > 0 {
		lines = append(lines, text(st.Dim, fmt.Sprintf("  • %d %s could not finish; `storix doctor` says why",
			bad, pluralWord(bad, "detector", "detectors")), w))
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]string{st.Title.Render("BLIND SPOTS")}, lines...)
}

// text renders one plain line in a style, cut to the column width first so a
// narrow terminal never wraps a line of the dashboard onto the next.
func text(style lipgloss.Style, s string, w int) string {
	return style.Render(truncate(s, max(w, 1)))
}

// firstSentence shortens a hint to its first sentence.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i]
	}
	return strings.TrimSuffix(s, ".")
}
