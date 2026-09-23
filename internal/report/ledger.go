package report

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/units"
)

// ledgerSection prints the twelve buckets of docs/02: one row each, sized
// against the volume's used space so the bars add up to a full disk rather
// than to the largest bucket.
//
// It is the first thing under the header because it is the answer to the
// question a reader opened the tool with. Everything below it explains one of
// these twelve rows.
func (t *textReport) ledgerSection() {
	l := t.l
	if len(l.Buckets) == 0 {
		return
	}
	used := l.Volume.UsedAfter
	if used <= 0 {
		used = l.Scanned.Bytes
	}

	t.section("LEDGER  (every byte of used space, in one of twelve buckets)")
	tbl := newTable(t.st, false, true, true, true, false)
	for i := range l.Buckets {
		b := &l.Buckets[i]
		tbl.add(
			cell{bar(t.st, b.Bytes, used, barWidth), lipgloss.NewStyle()},
			t.bucketBytes(b),
			cell{units.Percent(b.Bytes, used), t.st.note},
			t.reclaimCell(b),
			cell{fmt.Sprintf("%d  %s", i+1, b.Label), t.st.label},
		)
	}
	tbl.rule()
	tbl.add(
		cell{"", lipgloss.NewStyle()},
		t.bytes(used),
		cell{"", t.st.note},
		cell{"", t.st.note},
		cell{"used", t.st.label},
	)
	tbl.render(t.w)
	t.field("identity", fmt.Sprintf(
		"buckets 2–9 and 11 are the %s the walk saw; 1, 10 and 12 are read from the volume, not walked",
		t.u.Bytes(l.Scanned.Bytes)), t.st.note)
	if note := t.bucketNote(); note != "" {
		t.field("other", note, t.st.warn)
	}
}

// bucketBytes is the size cell, which says "unknown" rather than zero when a
// bucket could not be read.
func (t *textReport) bucketBytes(b *ledger.Bucket) cell {
	if !b.Known {
		return cell{"unknown", t.st.warn}
	}
	return cell{t.u.Bytes(b.Bytes), t.st.num}
}

// reclaimCell is how much of a bucket could be freed.
func (t *textReport) reclaimCell(b *ledger.Bucket) cell {
	if b.Reclaimable <= 0 {
		return cell{"", t.st.note}
	}
	return cell{t.u.Bytes(b.Reclaimable) + " free", t.st.good}
}

// bucketNote warns when Other is large enough to matter, because a large
// Other means the catalog missed something rather than that the disk is full
// of mystery.
func (t *textReport) bucketNote() string {
	b := t.bucketByID("other")
	if b == nil || b.Bytes == 0 {
		return ""
	}
	used := t.l.Volume.UsedAfter
	if used <= 0 || float64(b.Bytes)/float64(used) < 0.05 {
		return ""
	}
	return fmt.Sprintf("%s (%s of used space) matched no rule; run with --debug to see the largest unmatched directories",
		t.u.Bytes(b.Bytes), units.Percent(b.Bytes, used))
}

// bucketByID finds one bucket row.
func (t *textReport) bucketByID(id string) *ledger.Bucket {
	for i := range t.l.Buckets {
		if t.l.Buckets[i].ID == id {
			return &t.l.Buckets[i]
		}
	}
	return nil
}

// buckets prints what is inside each non-empty bucket: its reclaimability
// split, its largest categories and its largest owners. The three answer the
// three questions a bucket raises in order — can I delete it, what kind of
// thing is it, and whose is it.
func (t *textReport) buckets() {
	if t.o.Top < 0 || len(t.l.Buckets) == 0 {
		return
	}
	n := min(t.o.Top, bucketDetailLines)
	t.section("BUCKETS  (what is inside each one)")
	for i := range t.l.Buckets {
		b := &t.l.Buckets[i]
		if b.Bytes == 0 && b.Note == "" {
			continue
		}
		t.bucketDetail(b, n)
	}
}

// bucketDetailLines caps how many category and owner rows a bucket shows,
// whatever --top asks for: the section is a summary, and the drill-down lives
// in the TUI.
const bucketDetailLines = 8

// bucketDetail prints one bucket's heading and its three lists.
func (t *textReport) bucketDetail(b *ledger.Bucket, n int) {
	writeLine(t.w, "")
	head := fmt.Sprintf("  %s — %s", b.Label, t.u.Bytes(b.Bytes))
	if !b.Known {
		head = fmt.Sprintf("  %s — unknown", b.Label)
	}
	if b.Files > 0 {
		head += fmt.Sprintf(", %s", files(b.Files))
	}
	writeLine(t.w, t.st.title.Render(head))
	if b.Note != "" {
		t.field("", b.Note, t.st.note)
	}
	if len(b.ByReclaim) > 0 {
		t.field("reclaim", t.reclaimSummary(b), t.st.note)
	}
	tbl := newTable(t.st, false, true, true, false)
	t.bucketRows(tbl, "category", b.Categories, b.Bytes, n)
	t.bucketRows(tbl, "owner", b.Owners, b.Bytes, n)
	if len(tbl.rows) > 0 {
		tbl.render(t.w)
	}
}

// bucketRows adds one labelled group of lines to a bucket's table.
func (t *textReport) bucketRows(tbl *table, kind string, lines []ledger.Line, total int64, n int) {
	if len(lines) == 0 {
		return
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	for i, ln := range lines {
		head := ""
		if i == 0 {
			head = kind
		}
		tbl.add(
			cell{head, t.st.note},
			cell{t.u.Bytes(ln.Bytes), t.st.num},
			cell{units.Percent(ln.Bytes, total), t.st.note},
			cell{ln.Label, t.st.label},
		)
	}
}

// reclaimSummary is the one-line split of a bucket by reclaimability tag.
func (t *textReport) reclaimSummary(b *ledger.Bucket) string {
	s := ""
	for i, ln := range b.ByReclaim {
		if i > 0 {
			s += ", "
		}
		s += t.u.Bytes(ln.Bytes) + " " + ln.Label
	}
	return s
}

// unmatched lists the largest directories no rule reached. It is a tuning
// aid rather than a report section, so it only appears under --debug: the
// answer to "why is Other so large" is a list of paths to write rules for.
func (t *textReport) unmatched() {
	if !t.o.Debug || t.r.Class == nil || len(t.r.Class.Unmatched) == 0 {
		return
	}
	n := min(len(t.r.Class.Unmatched), unmatchedLines)
	t.section(fmt.Sprintf("UNMATCHED  (%d largest directories no rule reached)", n))
	tbl := newTable(t.st, false, true, false)
	for i := range n {
		id := t.r.Class.Unmatched[i]
		if int(id) >= len(t.r.Tree.Nodes) {
			continue
		}
		tbl.add(
			cell{"", t.st.note},
			cell{t.u.Bytes(t.r.Class.UnmatchedBytes(i)), t.st.num},
			cell{t.r.Tree.Nodes[id].Display(), t.st.label},
		)
	}
	tbl.render(t.w)
}

// unmatchedLines is how many unmatched directories --debug prints. Ten is
// what fits on a screen beside the rest of the debug output, and the eleventh
// is never the one that changes a rule.
const unmatchedLines = 10
