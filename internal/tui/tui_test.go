package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// termWidth and termHeight are the size every teatest program is given.
const (
	termWidth  = 120
	termHeight = 40
)

// waitDuration is how long a test waits for the screen to show something.
const waitDuration = 5 * time.Second

// fixtureResult walks a real fixture tree and wraps it as a finished scan.
//
// The root node is renamed to a fixed path afterwards: the fixture lives in a
// temporary directory whose name changes every run, and the breadcrumb is
// part of what the golden files check.
func fixtureResult(t *testing.T) *scan.Result {
	t.Helper()
	f := testutil.New(t)
	f.File("Applications/Chat.app/Contents/binary", 300_000)
	f.File("Applications/notes.txt", 120_000)
	f.File("Library/Caches/pkg/blob-a", 900_000)
	f.File("Library/Caches/pkg/blob-b", 450_000)
	f.File("Library/Preferences/tiny.plist", 200)
	f.File("Movies/holiday.mov", 2_500_000)
	f.Dir("Empty")
	for i := range 5 {
		f.File("Library/Caches/pkg/small-"+string(rune('a'+i)), 1000)
	}

	tree, err := walk.Walk(context.Background(), walk.Options{Root: f.Root, Parallelism: 2})
	if err != nil {
		t.Fatalf("walk the fixture: %v", err)
	}
	tree.Root.Name = mac.DataRoot + "/fixture"
	tree.Started = time.Unix(1_759_000_000, 0).UTC()
	tree.Finished = tree.Started.Add(1500 * time.Millisecond)

	cfg := scan.Config{Roots: []string{f.Root}, Units: units.Decimal, Version: "test"}
	return &scan.Result{
		Config: cfg,
		Tree:   tree,
		Ledger: ledger.Build(nil, tree, units.Decimal),
	}
}

// testConfig is the configuration the interface is built with in tests. It
// never scans: every test either supplies a result or drives a fake session.
func testConfig() scan.Config {
	return scan.Config{Roots: []string{mac.DataRoot}, Units: units.Decimal, Version: "test", NoCache: true}
}

// run is the program under test plus every byte it has drawn.
//
// teatest's output is a buffer that empties as it is read, so a second wait
// would be blind to everything the first one swallowed. The tests here wait
// several times over one run, so what has been read is kept.
type run struct {
	tm   *teatest.TestModel
	seen bytes.Buffer
}

// drain moves whatever the program has drawn since the last look into seen.
func (r *run) drain() {
	b, _ := io.ReadAll(r.tm.Output())
	r.seen.Write(b)
}

// shown reports whether s has been on the screen at any point.
func (r *run) shown(s string) bool {
	r.drain()
	return bytes.Contains(r.seen.Bytes(), []byte(s))
}

// newTestModel builds the interface with color off, which has to happen
// before the model is built: the unaccounted view renders its report once,
// when the result is adopted.
func newTestModel(t *testing.T, res *scan.Result) *Model {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	return New(t.Context(), testConfig(), res)
}

// start runs a model under teatest at a fixed terminal size.
func start(t *testing.T, m *Model) *run {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	m.st = NewStyles(false, true)
	m.help.Styles = HelpStyles(false, true)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(termWidth, termHeight))
	t.Cleanup(func() { _ = tm.Quit() })
	return &run{tm: tm}
}

// press sends one key.
func press(r *run, s string) {
	tm := r.tm
	switch s {
	case "enter":
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	case "esc":
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	case "tab":
		tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	case "backspace":
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	case "down":
		tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	case "up":
		tm.Send(tea.KeyPressMsg{Code: tea.KeyUp})
	default:
		for _, r := range s {
			tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}

// waitFor waits for the screen to have shown s.
func waitFor(t *testing.T, r *run, s string) {
	t.Helper()
	deadline := time.Now().Add(waitDuration)
	for time.Now().Before(deadline) {
		if r.shown(s) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the screen never showed %q; it has shown:\n%s", s, r.seen.String())
}

// finish quits the program and returns the model as it ended.
func finish(t *testing.T, r *run) *Model {
	t.Helper()
	press(r, "q")
	r.tm.WaitFinished(t, teatest.WithFinalTimeout(waitDuration))
	m, ok := r.tm.FinalModel(t).(*Model)
	if !ok {
		t.Fatal("the final model is not a *Model")
	}
	return m
}

// view is the model's screen as text, which is what the golden files hold:
// the raw program output also carries the renderer's cursor movements and the
// frames it happened to draw, neither of which is the interface's doing.
func screen(m *Model) []byte {
	return []byte(strings.ReplaceAll(m.View().Content, "\r\n", "\n"))
}

// TestProgressViewCountsAndSwitchesToBrowse drives the progress view from a
// fake event channel: the counters have to appear while the walk runs, and
// the finished scan has to bring up the browser by itself.
func TestProgressViewCountsAndSwitchesToBrowse(t *testing.T) {
	m := newTestModel(t, nil)
	events := make(chan walk.Event, 1)
	result := make(chan scanOutcome, 1)
	m.session = &session{gen: 1, events: events, result: result, cancel: func() {}, start: time.Now()}

	tm := start(t, m)
	for i := range 5 {
		events <- walk.ProgressEvent{Progress: walk.Progress{
			Dirs: uint64(120 + i), Files: uint64(3400 + i), Bytes: uint64(6_700_000 + i),
			Errors: 2, Current: mac.DataRoot + "/Users/someone/Library/Caches",
			Elapsed: time.Duration(i+1) * 400 * time.Millisecond,
		}}
	}
	waitFor(t, tm, "3,404")
	waitFor(t, tm, "/Users/someone/Library/Caches")

	result <- scanOutcome{res: fixtureResult(t)}
	close(events)
	waitFor(t, tm, "/fixture")

	final := finish(t, tm)
	if final.view != viewBrowse {
		t.Errorf("the interface stayed on view %d, want the browser", final.view)
	}
	if final.progress.ticks < 5 {
		t.Errorf("the progress view took %d updates, want every one of the 5 events", final.progress.ticks)
	}
	if final.session != nil {
		t.Error("the session outlived the scan")
	}
}

// TestProgressViewRendersItsCounters is the golden of the scanning screen at
// a fixed set of counters.
func TestProgressViewRendersItsCounters(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	p := newProgress()
	p.p = progressCounters{
		Dirs: 1204, Files: 340_567, Bytes: 12_345_678_901, Errors: 7, Skipped: 2,
		Current: mac.DataRoot + "/Users/someone/Library/Application Support",
		Elapsed: 4200 * time.Millisecond,
	}
	out := p.View(NewStyles(false, true), units.Decimal, termWidth, "/")
	teatest.RequireEqualOutput(t, []byte(out))
}

// TestBrowseNavigatesSortsAndFilters walks the fixture with the keys of §6.
//
// The waits are on text the screen has to draw for the first time: the
// renderer sends only what changed, so a breadcrumb that shares a prefix
// with the previous one never appears whole in the stream. Where the state
// matters rather than the pixels, the model is checked at the end.
func TestBrowseNavigatesSortsAndFilters(t *testing.T) {
	res := fixtureResult(t)
	m := newTestModel(t, res)
	tm := start(t, m)
	waitFor(t, tm, "Movies/")

	press(tm, "down")  // the cursor starts on Movies, the largest
	press(tm, "enter") // open Library
	waitFor(t, tm, "Preferences/")
	press(tm, "enter") // and its largest child, Caches
	waitFor(t, tm, "pkg/")
	press(tm, "enter") // down to the files themselves
	waitFor(t, tm, "blob-a")
	press(tm, "backspace")
	press(tm, "backspace")
	press(tm, "backspace")

	press(tm, "n") // sort by name
	press(tm, "n") // and flip it
	press(tm, "s") // back to size

	press(tm, "/")
	press(tm, "mov")
	waitFor(t, tm, "filter: mov")
	press(tm, "enter")

	press(tm, "esc") // clear the filter
	press(tm, "a")   // apparent bytes
	waitFor(t, tm, "apparent")
	press(tm, "a")
	press(tm, "b") // bundles openable

	final := finish(t, tm)
	if final.browse.dir != final.browse.root {
		t.Errorf("the browser ended in %s, want the root", final.browse.dir.Path())
	}
	if final.browse.opts.filter != "" {
		t.Errorf("the filter %q survived esc", final.browse.opts.filter)
	}
	if final.browse.opts.sort != sortSize || final.browse.opts.desc {
		t.Errorf("the sort ended as %v desc=%v, want size descending", final.browse.opts.sort, final.browse.opts.desc)
	}
	if final.browse.opts.apparent {
		t.Error("a pressed twice should leave allocated bytes on screen")
	}
	if !final.browse.opts.bundles {
		t.Error("b did not turn bundles on")
	}
	teatest.RequireEqualOutput(t, screen(final))
}

// TestBrowseOpensAndLeavesBundlesAlone checks the one navigation rule that
// is not obvious: a bundle is a leaf until the user says otherwise.
func TestBrowseOpensAndLeavesBundlesAlone(t *testing.T) {
	res := fixtureResult(t)
	m := newTestModel(t, res)
	b := &m.browse
	b.setSize(termWidth, termHeight-4)

	openTo(t, b, "Applications")
	if !b.open() { // the cursor is on Chat.app, a bundle
		t.Log("Chat.app is a leaf, as it should be")
	}
	rows := b.visible()
	if rows[0].name != "Chat.app" || rows[0].openable {
		t.Fatalf("the first row is %+v, want a bundle that is not openable", rows[0])
	}
	b.toggleBundles()
	if !b.visible()[0].openable || !b.open() {
		t.Fatal("with bundles on the cursor must descend into Chat.app")
	}
	if got := mac.DisplayPath(b.dir.Path()); got != "/fixture/Applications/Chat.app" {
		t.Errorf("opened %s", got)
	}
}

// openTo moves the cursor onto name in the open directory and descends.
func openTo(t *testing.T, b *browseModel, name string) {
	t.Helper()
	for i, r := range b.visible() {
		if r.name == name {
			b.moveTo(i)
			if !b.open() {
				t.Fatalf("could not open %s", name)
			}
			return
		}
	}
	t.Fatalf("no row named %s in %s", name, b.dir.Path())
}

// TestUnaccountedViewShowsTheLedgerSections switches to the second view and
// goldens what it holds.
func TestUnaccountedViewShowsTheLedgerSections(t *testing.T) {
	res := fixtureResult(t)
	m := newTestModel(t, res)
	tm := start(t, m)
	waitFor(t, tm, "/fixture")

	press(tm, "2")
	waitFor(t, tm, "VOLUME")
	press(tm, "tab") // back to the browser
	waitFor(t, tm, "Applications/")
	press(tm, "tab") // and out again
	waitFor(t, tm, "VOLUME")

	final := finish(t, tm)
	if final.view != viewUnaccounted {
		t.Errorf("the interface ended on view %d, want the unaccounted report", final.view)
	}
	teatest.RequireEqualOutput(t, screen(final))
}

// TestHelpOverlayOpensAndCloses checks the overlay and the key that closes it.
func TestHelpOverlayOpensAndCloses(t *testing.T) {
	m := newTestModel(t, fixtureResult(t))
	tm := start(t, m)
	waitFor(t, tm, "/fixture")
	press(tm, "?")
	waitFor(t, tm, "storix keys")
	press(tm, "x") // any key returns
	waitFor(t, tm, "Applications/")
	final := finish(t, tm)
	if final.view != viewBrowse {
		t.Errorf("the help overlay did not return to view %d", viewBrowse)
	}
}

// TestQuitDuringAScanCancelsItAndShowsThePartialTree is the Ctrl-C rule: the
// first press stops the walk and shows what it got, the second leaves.
func TestQuitDuringAScanCancelsItAndShowsThePartialTree(t *testing.T) {
	m := newTestModel(t, nil)
	events := make(chan walk.Event, 1)
	result := make(chan scanOutcome, 1)
	cancelled := make(chan struct{})
	m.session = &session{
		gen: 1, events: events, result: result, start: time.Now(),
		cancel: func() { close(cancelled) },
	}

	tm := start(t, m)
	events <- walk.ProgressEvent{Progress: walk.Progress{Dirs: 3, Files: 9, Current: mac.DataRoot}}
	waitFor(t, tm, "scanning")

	tm.tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	select {
	case <-cancelled:
	case <-time.After(waitDuration):
		t.Fatal("ctrl+c did not cancel the scan")
	}
	waitFor(t, tm, "stopping the scan")

	partial := fixtureResult(t)
	partial.Tree.Incomplete = true
	result <- scanOutcome{res: partial}
	close(events)
	waitFor(t, tm, "INCOMPLETE")

	final := finish(t, tm)
	if final.view != viewBrowse {
		t.Errorf("an interrupted scan ended on view %d, want the browser", final.view)
	}
}

// TestAFailedScanLeavesWithItsError checks that a scan that could not start
// ends the interface with something to print, rather than a blank exit.
func TestAFailedScanLeavesWithItsError(t *testing.T) {
	m := newTestModel(t, nil)
	events := make(chan walk.Event, 1)
	result := make(chan scanOutcome, 1)
	m.session = &session{gen: 1, events: events, result: result, cancel: func() {}, start: time.Now()}

	tm := start(t, m)
	want := errors.New("scan root /nowhere: no such file or directory")
	result <- scanOutcome{err: want}
	close(events)
	tm.tm.WaitFinished(t, teatest.WithFinalTimeout(waitDuration))

	final, ok := tm.tm.FinalModel(t).(*Model)
	if !ok {
		t.Fatal("the final model is not a *Model")
	}
	if !errors.Is(final.err, want) {
		t.Errorf("the model ended with %v, want the scan error", final.err)
	}
}
