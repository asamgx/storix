package tui

import (
	"time"

	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/walk"
)

// Every message carries the generation of the scan session it belongs to, so
// that a late progress tick from a scan the user has already replaced with a
// rescan is dropped instead of overwriting the new one's counters.

// progressMsg is one counter snapshot from a running walk.
type progressMsg struct {
	gen int
	p   walk.Progress
}

// walkDoneMsg says the walk itself finished; the scan is still building its
// ledger and writing its cache.
type walkDoneMsg struct {
	gen int
	err error
}

// eventsClosedMsg says the event channel is closed, so the scan has returned
// and its result is waiting.
type eventsClosedMsg struct{ gen int }

// scanDoneMsg delivers a finished scan, complete or interrupted.
type scanDoneMsg struct {
	gen int
	res *scan.Result
	err error
}

// tickMsg keeps the elapsed time moving when the walk is quiet.
type tickMsg time.Time

// copiedMsg reports the result of copying a path to the clipboard.
type copiedMsg struct {
	path string
	err  error
}

// openedMsg reports the result of revealing a path in Finder.
type openedMsg struct {
	path string
	err  error
}
