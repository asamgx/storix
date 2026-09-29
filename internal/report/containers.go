package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/scan"
)

// Containers writes the containers and virtual machines section on its own.
// It exists for the TUI's Containers view and for `storix dev`, which render
// the same section into a viewport rather than reimplementing it.
func Containers(w io.Writer, r *scan.Result, o Options) error {
	if r == nil {
		return fmt.Errorf("report: nothing to render")
	}
	o = o.withDefaults()
	t := &textReport{w: w, r: r, l: r.Ledger, o: o, st: newStyles(o.Color), u: o.Units}
	t.containers()
	return nil
}

// runtimes gathers every detector's runtimes in registry order, so the
// section reads in the same order as the detectors table below it.
func (t *textReport) runtimes() []detect.Runtime {
	var out []detect.Runtime
	for _, st := range t.r.Detectors {
		out = append(out, t.r.Summaries[st.Name].Runtimes...)
	}
	return out
}

// containers prints, per runtime, the two numbers docs/04 asks for: what the
// host has actually given the runtime's disk image, and what the runtime's
// own daemon believes it is using.
//
// They are different questions with different answers and the gap between
// them is the point. A daemon reporting twenty gigabytes inside an image the
// host has given eighteen and a half is not a contradiction: the image is
// sparse, its contents compress, and space freed inside a guest is not space
// returned to the host until something compacts it.
func (t *textReport) containers() {
	rts := t.runtimes()
	if len(rts) == 0 {
		return
	}
	t.section("CONTAINERS & VMs  (what the host gave, what the runtime says it uses)")
	for i, rt := range rts {
		if i > 0 {
			writeLine(t.w, "")
		}
		t.runtime(rt)
	}
}

// runtime prints one runtime's two columns and the facts around them.
func (t *textReport) runtime(rt detect.Runtime) {
	writeLine(t.w, t.st.title.Render("  "+rt.Name))

	tbl := newTable(t.st, false, true, true, false, false)
	for i, img := range rt.HostImage {
		tbl.add(t.hostRow(i == 0, img)...)
	}
	if len(rt.HostImage) > 1 {
		tbl.add(
			cell{"total", t.st.note}, t.bytes(rt.HostBytes()), cell{"", t.st.note},
			cell{"", t.st.label}, cell{"allocated on the host", t.st.note},
		)
	}
	for i, line := range rt.GuestReported {
		tbl.add(t.guestRow(i == 0, line)...)
	}
	if len(rt.GuestReported) > 1 {
		tbl.add(
			cell{"total", t.st.note}, t.bytes(rt.GuestBytes()),
			cell{t.u.Bytes(rt.GuestReclaimable()) + " free", t.st.good},
			cell{"", t.st.label}, cell{"reported by the daemon", t.st.note},
		)
	}
	if len(tbl.rows) > 0 {
		tbl.render(t.w)
	}

	if rt.Context != "" {
		t.field("context", rt.Context, t.st.note)
	}
	if len(rt.Machines) > 0 {
		t.field("machines", strings.Join(rt.Machines, ", "), t.st.note)
	}
	if rt.Note != "" {
		t.field("note", rt.Note, t.st.note)
	}
}

// hostRow is one disk image as the host sees it.
func (t *textReport) hostRow(first bool, img detect.Tool) []cell {
	head := ""
	if first {
		head = "host"
	}
	return []cell{
		{head, t.st.note},
		{t.u.Bytes(img.Bytes), t.st.num},
		{"", t.st.note},
		{img.Name, t.st.label},
		{img.Note, t.st.note},
	}
}

// guestRow is one line of what the daemon believes it is using.
func (t *textReport) guestRow(first bool, line detect.Line) []cell {
	head := ""
	if first {
		head = "guest"
	}
	free := ""
	if line.Reclaimable > 0 {
		free = t.u.Bytes(line.Reclaimable) + " free"
		if line.Known {
			free += fmt.Sprintf(" (%d%%)", line.Percent)
		}
	}
	return []cell{
		{head, t.st.note},
		{t.u.Bytes(line.Size), t.st.num},
		{free, t.reclaimStyle(line)},
		{line.Type, t.st.label},
		{objectCount(line), t.st.note},
	}
}

// reclaimStyle greys out a reclaimable figure that is not safe to act on: a
// named volume is where a database lives, and printing "939 MB free" for it
// in the same green as a stale image would be an invitation to lose data.
func (t *textReport) reclaimStyle(line detect.Line) lipgloss.Style {
	if line.Reclaim.Reclaimable() {
		return t.st.good
	}
	return t.st.note
}

// objectCount says how many objects a df row covers and how many are in use.
func objectCount(line detect.Line) string {
	if line.Count == 0 {
		return ""
	}
	return fmt.Sprintf("%d, %d in use", line.Count, line.Active)
}

// detectors prints what happened to each detector: whether it ran, how long
// it took, and why it did not when it did not.
//
// It is a permanent section rather than a debug one because a missing tool
// changes what the ledger can say. A reader who sees Developer at 25 GB
// deserves to know that the homebrew detector timed out and the number is the
// static rules' answer rather than Homebrew's own.
func (t *textReport) detectors() {
	if len(t.r.Detectors) == 0 {
		return
	}
	t.section("DETECTORS")
	tbl := newTable(t.st, false, false, true, false)
	unverified := false
	for _, st := range t.r.Detectors {
		state := st.State.String()
		if !st.Verified {
			state += " *"
			unverified = true
		}
		tbl.add(
			cell{st.Name, t.st.label},
			cell{state, t.stateStyle(st.State)},
			cell{detectorDuration(st), t.st.note},
			cell{detectorReason(st), t.st.note},
		)
	}
	tbl.render(t.w)
	if unverified {
		writeLine(t.w, t.st.note.Render(
			"  * written from the tool's documentation; never yet run against a machine that had it"))
	}
}

// detectorDuration is how long a probe took, blank when it never ran.
func detectorDuration(st detect.Status) string {
	if st.Duration < time.Millisecond {
		return ""
	}
	return round(st.Duration)
}

// detectorReason is the reason, with the command count appended when a
// detector succeeded and so has no reason of its own to give.
func detectorReason(st detect.Status) string {
	if st.Reason != "" {
		return st.Reason
	}
	switch n := len(st.Commands); n {
	case 0:
		return ""
	case 1:
		return "1 command"
	default:
		return fmt.Sprintf("%d commands", n)
	}
}

// stateStyle colours a detector state: a panic is a bug worth noticing, a
// degradation is worth reading, and everything else is ordinary.
func (t *textReport) stateStyle(s detect.State) lipgloss.Style {
	switch s {
	case detect.Ok:
		return t.st.good
	case detect.Panic, detect.Timeout, detect.Degraded:
		return t.st.warn
	default:
		return t.st.note
	}
}
