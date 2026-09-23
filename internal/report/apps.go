package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/scan"
)

// Apps writes the application inventory: what is installed and what each
// application costs, then the owners whose application is not there any more.
//
// The two halves answer different questions and are laid out differently on
// purpose. The first is a size table a reader scans down, so it is columns.
// The second is a list of accusations — "this data belongs to something you
// no longer have" — and an accusation needs its evidence beside it, so it is
// rows of prose. Printing the second half as a size table would make it look
// like a list of things to delete, which is not what storix is claiming.
func Apps(w io.Writer, r *scan.Result, o Options) error {
	return AppsWith(w, r, o, AppsOptions{})
}

// appsReport renders one report document.
type appsReport struct {
	*textReport
	rep *apps.Report
	// all shows every row rather than the head of each list.
	all bool
}

// appsTopUnknown is how many unattributed directories are listed. The tail is
// long on every machine and uninteresting: what matters is the largest few,
// which are the ones worth an alias-table row.
const appsTopUnknown = 10

// AppsOptions tune the two renderers beyond the shared report Options.
type AppsOptions struct {
	// All lists every row instead of the head of each section.
	All bool
	// Binary lists the casks that install a command-line binary, which are
	// otherwise left out because they are never missing an application.
	Binary bool
	// OrphansOnly drops the installed table, leaving the sections about
	// software that is not there any more.
	OrphansOnly bool
}

// AppsWith renders with the command's own flags.
func AppsWith(w io.Writer, r *scan.Result, o Options, ao AppsOptions) error {
	if r == nil {
		return fmt.Errorf("report: nothing to render")
	}
	rep, ok := scan.Apps(r)
	if !ok {
		return fmt.Errorf("report: this scan holds no application inventory")
	}
	o = o.withDefaults()
	t := &textReport{w: w, r: r, l: r.Ledger, o: o, st: newStyles(o.Color), u: o.Units}
	a := &appsReport{textReport: t, rep: rep, all: ao.All}

	if !ao.OrphansOnly {
		a.installed()
	}
	a.caskOnly()
	a.orphans()
	a.inTrash()
	a.unknown()
	a.degraded()
	return nil
}

// installed prints the applications and what each one costs.
func (a *appsReport) installed() {
	if len(a.rep.Apps) == 0 {
		return
	}
	c := a.rep.Counts
	a.section(fmt.Sprintf(
		"APPLICATIONS  (%d bundles, %d casks, %d from the App Store)   footprint = bundle + linked data",
		c.Bundles, c.Casks, c.AppStore))

	// Every bucket the footprint spans gets a column. Leaving one out
	// would print rows whose parts visibly fail to add up to their total,
	// and the columns that are easy to forget are exactly the ones that
	// make the footprint worth having: VS Code keeps more in Developer
	// than in App data, and OrbStack's disk image dwarfs both.
	tbl := newTable(a.st, false, false, true, true, true, true, true, true, false, false)
	tbl.add(
		cell{"APP", a.st.label}, cell{"", a.st.label},
		cell{"FOOTPRINT", a.st.label}, cell{"BUNDLE", a.st.label},
		cell{"DATA", a.st.label}, cell{"CACHES", a.st.label},
		cell{"DEV", a.st.label}, cell{"VM", a.st.label},
		cell{"SOURCE", a.st.label}, cell{"CONFIDENCE", a.st.label},
	)
	tbl.rule()

	largest := int64(0)
	for _, e := range a.rep.Apps {
		largest = max(largest, e.Footprint.Total)
	}
	for _, e := range a.limit(a.rep.Apps) {
		size := e.Footprint
		tbl.add(
			cell{e.Label, a.st.label},
			cell{bar(a.st, size.Total, largest, 12), a.st.bar},
			a.bytes(size.Total), a.bytes(size.Bundle), a.bytes(size.Data),
			a.bytes(size.Caches), a.dash(size.Dev), a.dash(size.Containers),
			cell{sourceChips(e.Sources), a.st.note},
			cell{e.Confidence, a.confidenceStyle(e.Confidence)},
		)
	}
	tbl.render(a.w)
	a.more(len(a.rep.Apps))
}

// caskOnly prints the casks whose application is gone.
func (a *appsReport) caskOnly() {
	if len(a.rep.CasksMissingApp) == 0 && len(a.rep.CaskOnly) == 0 {
		return
	}
	a.section(fmt.Sprintf("CASK INSTALLED, APPLICATION MISSING  (%d)", len(a.rep.CasksMissingApp)))

	byToken := make(map[string]apps.Entry, len(a.rep.CaskOnly))
	for _, e := range a.rep.CaskOnly {
		byToken[strings.TrimPrefix(e.Owner, "cask:")] = e
	}
	tbl := newTable(a.st, false, false, false, true)
	tbl.add(
		cell{"CASK", a.st.label}, cell{"EXPECTED APP", a.st.label},
		cell{"INSTALLED", a.st.label}, cell{"DATA", a.st.label},
	)
	tbl.rule()
	for _, c := range a.rep.CasksMissingApp {
		when := "unknown"
		if !c.InstalledAt.IsZero() {
			when = c.InstalledAt.Format("2006-01-02")
		}
		data := cell{"none found", a.st.note}
		if e, ok := byToken[c.Token]; ok {
			data = a.bytes(e.Footprint.Total)
		}
		tbl.add(
			cell{c.Token, a.st.label}, cell{c.ExpectedApp, a.st.note},
			cell{when, a.st.note}, data,
		)
	}
	tbl.render(a.w)

	for _, e := range a.rep.CaskOnly {
		a.evidence(e)
	}
}

// orphans prints the owners whose application appears to be gone.
func (a *appsReport) orphans() {
	if len(a.rep.Orphans) == 0 {
		return
	}
	var total int64
	for _, e := range a.rep.Orphans {
		total += e.Footprint.Total
	}
	a.section(fmt.Sprintf("ORPHAN-LIKELY OWNERS  (%d, %s)", len(a.rep.Orphans), a.u.Bytes(total)))

	tbl := newTable(a.st, false, true, false, false)
	tbl.add(
		cell{"OWNER", a.st.label}, cell{"SIZE", a.st.label},
		cell{"LAST WRITE", a.st.label}, cell{"CONFIDENCE", a.st.label},
	)
	tbl.rule()
	for _, e := range a.limit(a.rep.Orphans) {
		tbl.add(
			cell{e.Label, a.st.label}, a.bytes(e.Footprint.Total),
			cell{lastWrite(e.LastWrite), a.st.note},
			cell{orphanGrade(e.Confidence), a.confidenceStyle(e.Confidence)},
		)
	}
	tbl.render(a.w)
	a.more(len(a.rep.Orphans))

	for _, e := range a.limit(a.rep.Orphans) {
		a.evidence(e)
	}
}

// inTrash prints applications waiting in the Trash, whose data is still whole.
func (a *appsReport) inTrash() {
	if len(a.rep.InTrash) == 0 {
		return
	}
	var total int64
	for _, e := range a.rep.InTrash {
		total += e.Footprint.Total
	}
	a.section(fmt.Sprintf("IN THE TRASH  (%d, %s)", len(a.rep.InTrash), a.u.Bytes(total)))
	for _, e := range a.rep.InTrash {
		a.evidence(e)
	}
}

// unknown prints the largest directories nothing could be attributed to.
//
// They are the list the alias table grows from, so the section says what it
// is for: an unknown owner is a gap in storix's knowledge and not a finding
// about the machine.
func (a *appsReport) unknown() {
	if len(a.rep.Unknown) == 0 {
		return
	}
	shown := a.rep.Unknown
	if !a.all && len(shown) > appsTopUnknown {
		shown = shown[:appsTopUnknown]
	}
	a.section(fmt.Sprintf("UNKNOWN OWNER  (%d of %d directories, largest first)",
		len(shown), a.rep.Counts.Candidates))

	tbl := newTable(a.st, false, true, false)
	for _, e := range shown {
		path := ""
		if len(e.Components) > 0 {
			path = e.Components[0].Path
		}
		tbl.add(cell{e.Label, a.st.label}, a.bytes(e.Footprint.Total), cell{path, a.st.note})
	}
	tbl.render(a.w)
	if len(a.rep.Unknown) > len(shown) {
		a.field("", fmt.Sprintf("%d more; --all lists them", len(a.rep.Unknown)-len(shown)), a.st.note)
	}
}

// degraded names the probes that did not run, so a short list is visibly
// short for a reason rather than looking like a clean machine.
func (a *appsReport) degraded() {
	if len(a.rep.Degraded) == 0 {
		return
	}
	a.section("DEGRADED PROBES")
	for _, d := range a.rep.Degraded {
		a.field(d.Probe, d.Reason, a.st.warn)
	}
}

// evidence prints one owner's reasoning under its row.
func (a *appsReport) evidence(e apps.Entry) {
	writeLine(a.w, "")
	head := e.Label
	if e.Footprint.Total > 0 {
		head += "  " + a.u.Bytes(e.Footprint.Total)
	}
	writeLine(a.w, "  "+a.st.title.Render(head)+"  "+a.st.note.Render(e.State))
	for _, line := range e.Evidence {
		a.field("", line, a.st.note)
	}
	for _, line := range e.Keep {
		a.field("keep", line, a.st.good)
	}
	for _, c := range a.components(e) {
		a.field("", c, a.st.note)
	}
}

// components lists an owner's directories, largest first, as one line each.
func (a *appsReport) components(e apps.Entry) []string {
	if len(e.Components) == 0 {
		return nil
	}
	shown := e.Components
	if !a.all && len(shown) > 5 {
		shown = shown[:5]
	}
	out := make([]string, 0, len(shown))
	for _, c := range shown {
		out = append(out, fmt.Sprintf("%s  %s  (%s)", a.u.Bytes(c.Bytes), c.Path, c.Bucket))
	}
	return out
}

// limit is the head of a list unless --all was asked for.
func (a *appsReport) limit(list []apps.Entry) []apps.Entry {
	if a.all || a.o.Top <= 0 || len(list) <= a.o.Top {
		return list
	}
	return list[:a.o.Top]
}

// more notes the rows a limit left out.
func (a *appsReport) more(total int) {
	if a.all || a.o.Top <= 0 || total <= a.o.Top {
		return
	}
	a.field("", fmt.Sprintf("%d more; --all lists them", total-a.o.Top), a.st.note)
}

// confidenceStyle colours an attribution by how much it should be trusted, so
// a reader can see at a glance which rows are evidence and which are guesses.
func (a *appsReport) confidenceStyle(conf string) lipgloss.Style {
	switch conf {
	case "strong":
		return a.st.good
	case "unknown", "corroborating":
		return a.st.warn
	default:
		return a.st.note
	}
}

// dash is a byte cell that prints nothing rather than "0 bytes", so the two
// columns most applications do not use stay quiet.
func (a *appsReport) dash(n int64) cell {
	if n == 0 {
		return cell{"-", a.st.note}
	}
	return a.bytes(n)
}

// sourceChips shortens where a bundle was found to something that fits beside
// six size columns. The long forms are in the evidence.
func sourceChips(sources []string) string {
	short := map[string]string{
		"/Applications":  "app",
		"~/Applications": "user",
		"Setapp":         "setapp",
		"vendor folder":  "vendor",
		"cask":           "cask",
		"nested":         "nested",
		"trash":          "trash",
	}
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		if v, ok := short[s]; ok {
			s = v
		}
		out = append(out, s)
	}
	return strings.Join(out, "+")
}

// lastWrite renders a timestamp, or a dash when the walk never saw the data.
func lastWrite(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02")
}

// orphanGrade is the word the report prints for an orphan's confidence.
// "corroborating" is accurate and unreadable; "possible" says the same thing
// to the person deciding whether to look.
func orphanGrade(conf string) string {
	if conf == "corroborating" {
		return "possible"
	}
	return conf
}
