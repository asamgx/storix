package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/reclaim"
	"github.com/asamgx/storix/internal/scan"
)

// PlanView chooses what of a plan the text report shows.
type PlanView struct {
	// Tiers limits the items to these tiers; empty shows them all.
	Tiers []reclaim.Tier
	// All lists every item of the tiers that are only there to account
	// for bytes — in use and never — rather than the largest few.
	All bool
}

// planTopListed is how many in-use and never items are listed without All,
// and planTopSuggested how many of a suggested tier: the largest carry the
// space, and a tier of a hundred caches is read by its head.
const (
	planTopListed    = 8
	planTopSuggested = 20
)

// Reclaim writes the reclaim plan. It is `storix reclaim` and, through a
// viewport, the TUI's Plan view.
//
// The plan is read-only and the header says so: every line below it is a
// suggestion with its cost, never something storix has done or will do.
func Reclaim(w io.Writer, r *scan.Result, p *reclaim.Plan, o Options, v PlanView) error {
	if r == nil || p == nil {
		return fmt.Errorf("report: nothing to render")
	}
	o = o.withDefaults()
	t := &textReport{w: w, r: r, l: r.Ledger, o: o, st: newStyles(o.Color), u: o.Units}
	t.plan(p, v)
	return nil
}

func (t *textReport) plan(p *reclaim.Plan, v PlanView) {
	t.section("RECLAIM PLAN  (read-only; nothing has been changed)")
	t.planHeadline(p)

	shown := p.Filter(v.Tiers)
	for _, tier := range reclaim.TierOrder() {
		var items []reclaim.Item
		for _, it := range shown {
			if it.Tier == tier {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			continue
		}
		t.planTier(tier, p.Totals[tier], items, v.All)
	}
	t.planEdits(p.Edits)
	t.planTrash(p.Trash)
	for _, n := range p.Notes {
		t.field("note", n, t.st.note)
	}
}

// planHeadline is the plan in two numbers: what the suggested tiers free,
// each byte once, and what the tools themselves report on top of the walk.
func (t *textReport) planHeadline(p *reclaim.Plan) {
	var after, reported int64
	for tier, tot := range p.Totals {
		if tier.Suggested() {
			after += tot.AfterTrash
			reported += tot.Reported
		}
	}
	line := t.u.Bytes(p.Freeable()) + " could be freed across the suggested tiers, each byte counted once"
	if after > 0 {
		line += "; " + t.u.Bytes(after) + " of it only once the Trash is emptied"
	}
	t.field("freeable", line, t.st.good)
	if reported > 0 {
		t.field("reported", t.u.Bytes(reported)+" more by the tools' own figures (brew cleanup, docker), which overlap the walked bytes", t.st.note)
	}
	t.field("stale", "projects untouched for "+days(p.StaleAfter)+" count as stale; --stale-after changes it", t.st.note)
}

// planTier prints one tier's heading and its items.
func (t *textReport) planTier(tier reclaim.Tier, tot *reclaim.Totals, items []reclaim.Item, all bool) {
	head := fmt.Sprintf("%s — %s", tier.Label(), t.u.Bytes(tot.Walked))
	if tot.Reported > 0 {
		head += fmt.Sprintf(" walked, %s reported", t.u.Bytes(tot.Reported))
	}
	head += fmt.Sprintf(", %d %s", tot.Items, plural(tot.Items, "item", "items"))
	if !tier.Suggested() {
		head += "  (listed to account for it, never suggested)"
	}
	writeLine(t.w, "")
	writeLine(t.w, t.st.title.Render("  "+head))

	limit := planTopSuggested
	if !tier.Suggested() {
		limit = planTopListed
	}
	shown := items
	if !all && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, it := range shown {
		t.planItem(it)
	}
	if hidden := len(items) - len(shown); hidden > 0 {
		t.field("", fmt.Sprintf("and %d more; --all lists them", hidden), t.st.note)
	}
}

// planItem prints one item: its size, name and path, then what to run and
// what it costs.
func (t *textReport) planItem(it reclaim.Item) {
	size := t.u.Bytes(it.Bytes)
	if it.Reported {
		size = "~" + size
	}
	title := it.Title
	if it.Path != "" && !strings.HasSuffix(title, it.Path) {
		title += "  " + t.st.note.Render(it.Path)
	}
	writeLine(t.w, "  "+t.st.num.Render(fmt.Sprintf("%10s", size))+"  "+t.st.label.Render(title))
	indent := strings.Repeat(" ", 14)
	switch {
	case it.Command != "":
		writeLine(t.w, indent+t.st.note.Render("run ")+t.st.good.Render(it.Command))
	case it.Action != "":
		writeLine(t.w, indent+t.st.note.Render(it.Action))
	}
	if it.Impact != "" {
		for _, l := range wrapText(it.Impact, textWidth-len(indent)) {
			writeLine(t.w, indent+t.st.note.Render(l))
		}
	}
	if it.AfterTrash {
		writeLine(t.w, indent+t.st.note.Render("frees space once the Trash is emptied"))
	}
}

// planEdits lists the lines of the user's own files that name software
// already gone.
func (t *textReport) planEdits(edits []reclaim.Edit) {
	if len(edits) == 0 {
		return
	}
	writeLine(t.w, "")
	writeLine(t.w, t.st.title.Render(fmt.Sprintf("  Edits to your files — %d %s", len(edits), plural(len(edits), "line", "lines"))))
	for _, e := range edits {
		writeLine(t.w, "  "+t.st.label.Render(fmt.Sprintf("%s:%d", e.File, e.Line))+"  "+t.st.good.Render(e.Text))
		for _, l := range wrapText(e.Why, textWidth-14) {
			writeLine(t.w, strings.Repeat(" ", 14)+t.st.note.Render(l))
		}
	}
}

// planTrash says what the plan knows about the Trash.
func (t *textReport) planTrash(ts reclaim.TrashState) {
	switch {
	case !ts.Known:
		t.field("trash", ts.Note, t.st.warn)
	case ts.Bytes == 0:
		t.field("trash", "empty", t.st.note)
	}
}

// days formats a threshold the way --stale-after reads it.
func days(d time.Duration) string {
	n := int(d.Hours() / 24)
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

// planSummary is the short form the full report prints after the ledger:
// what each suggested tier would free.
func (t *textReport) planSummary() {
	p := reclaim.Build(t.r, reclaim.Options{})
	if len(p.Items) == 0 {
		return
	}
	t.section("RECLAIM  (what could be freed, by cost; `storix reclaim` has the details)")
	tbl := newTable(t.st, true, false, false)
	for _, tier := range reclaim.TierOrder() {
		tot := p.Totals[tier]
		if tot == nil || tot.Items == 0 || !tier.Suggested() {
			continue
		}
		extra := ""
		if tot.Reported > 0 {
			extra = "+ ~" + t.u.Bytes(tot.Reported) + " by the tools' own figures"
		}
		tbl.add(t.bytes(tot.Walked), cell{tier.Label(), t.st.label},
			cell{fmt.Sprintf("%d %s", tot.Items, plural(tot.Items, "item", "items")), t.st.note},
			cell{extra, t.st.note})
	}
	tbl.render(t.w)
	t.field("freeable", t.u.Bytes(p.Freeable())+", each byte counted once; nothing has been changed", t.st.good)
}
