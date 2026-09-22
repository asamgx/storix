package tui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/walk"
)

// session is one running scan.
//
// The event channel holds a single element and the walker drops progress it
// cannot deliver, so a slow frame costs a tick rather than stalling the walk.
// The result channel is buffered too: the scan goroutine hands over its
// result and exits even if the program has already quit.
type session struct {
	gen    int
	cancel context.CancelFunc
	events chan walk.Event
	result chan scanOutcome
	start  time.Time
}

// scanOutcome is what scan.Run returned.
type scanOutcome struct {
	res *scan.Result
	err error
}

// newSession starts a scan and returns the session that follows it.
func newSession(ctx context.Context, gen int, cfg scan.Config) *session {
	scanCtx, cancel := context.WithCancel(ctx)
	s := &session{
		gen:    gen,
		cancel: cancel,
		events: make(chan walk.Event, 1),
		result: make(chan scanOutcome, 1),
		start:  time.Now(),
	}
	cfg.Events = s.events
	go func() {
		res, err := scan.Run(scanCtx, cfg)
		// Closing the channel is what tells the listener the scan is over:
		// the walker's DoneEvent is not sent when the walk fails, and the
		// ledger and the cache write happen after it.
		close(s.events)
		s.result <- scanOutcome{res: res, err: err}
		cancel()
	}()
	return s
}

// listen waits for one walk event. It is re-armed after every message, which
// is what keeps Update off the channel: the program blocks only inside this
// goroutine.
func listen(s *session) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-s.events
		if !ok {
			return eventsClosedMsg{gen: s.gen}
		}
		switch ev := e.(type) {
		case walk.ProgressEvent:
			return progressMsg{gen: s.gen, p: ev.Progress}
		case walk.DoneEvent:
			return walkDoneMsg{gen: s.gen, err: ev.Err}
		default:
			return nil
		}
	}
}

// collect waits for the finished scan behind a closed event channel.
func collect(s *session) tea.Cmd {
	return func() tea.Msg {
		out := <-s.result
		return scanDoneMsg{gen: s.gen, res: out.res, err: out.err}
	}
}

// tickInterval keeps the elapsed clock moving between progress events.
const tickInterval = 250 * time.Millisecond

// tick schedules the next elapsed-time refresh.
func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// openInFinder reveals a path in Finder, which is the one action that leaves
// the terminal. It never changes anything on disk.
func openInFinder(displayPath, scanPath string) tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("open", "-R", scanPath).Run()
		return openedMsg{path: displayPath, err: err}
	}
}

// copyPath puts a path on the clipboard.
func copyPath(displayPath string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(displayPath)
		return copiedMsg{path: displayPath, err: cmd.Run()}
	}
}
