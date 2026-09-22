package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/units"
)

// progressModel is the view a running scan shows: the walker's counters, the
// directory it is in, and how long it has been going.
type progressModel struct {
	spin    spinner.Model
	p       progressCounters
	started time.Time
	// ticks counts the progress events received, which is what the test
	// checks when it asserts the view updates while the walk runs.
	ticks int
	// cancelled records that the user asked the walk to stop; the walk is
	// still unwinding and the view says so rather than freezing.
	cancelled bool
}

// progressCounters is the part of walk.Progress the view shows.
type progressCounters struct {
	Dirs    uint64
	Files   uint64
	Bytes   uint64
	Errors  uint64
	Skipped uint64
	Current string
	Elapsed time.Duration
}

// newProgress builds the view with its spinner.
func newProgress() progressModel {
	s := spinner.New(spinner.WithSpinner(spinner.Dot))
	return progressModel{spin: s, started: time.Now()}
}

// elapsed is how long the scan has been running: the walker's own figure
// while it reports one, the wall clock before the first event arrives.
func (m progressModel) elapsed() time.Duration {
	if m.p.Elapsed > 0 {
		return m.p.Elapsed
	}
	return time.Since(m.started)
}

// View draws the scanning screen.
func (m progressModel) View(st Styles, u units.Format, width int, root string) string {
	var b strings.Builder
	title := "scanning " + root
	if m.cancelled {
		title = "stopping the scan of " + root
	}
	b.WriteString(m.spin.View() + " " + st.Title.Render(title) + "\n\n")

	rows := [][2]string{
		{"elapsed", fmtDuration(m.elapsed())},
		{"directories", count(m.p.Dirs)},
		{"files", count(m.p.Files)},
		{"bytes", u.Bytes(int64(m.p.Bytes))},
		{"unreadable", count(m.p.Errors)},
		{"mounts skipped", count(m.p.Skipped)},
	}
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("  %s %s\n",
			st.Dim.Render(padRight(r[0], 15, r[0])), st.Value.Render(r[1])))
	}
	b.WriteString("\n  ")
	b.WriteString(st.Dim.Render(truncateLeft(mac.DisplayPath(m.p.Current), max(width-4, 10))))
	return b.String()
}

// fmtDuration renders an elapsed time that moves smoothly: tenths under a
// minute, then minutes and seconds.
func fmtDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
