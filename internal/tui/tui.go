// Package tui is storix's interactive interface: a progress view while a
// scan runs, a directory browser over the tree it produced, and the
// unaccounted report beside it.
//
// The model owns the session; everything it shows comes from one
// scan.Result, whether that result was walked just now or read from the
// cache. Nothing here computes a byte count: the arithmetic belongs to
// internal/ledger and the rendering of the ledger to internal/report.
package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/walk"
)

// view is the screen the model is showing.
type view uint8

const (
	viewProgress view = iota
	viewLedger
	viewBrowse
	viewApps
	viewDeveloper
	viewContainers
	viewUnaccounted
	viewHelp
)

// resultViews are the views a finished scan can show, in the order of
// docs/03: the digits 1 to 6 select one directly and tab cycles them.
var resultViews = []view{
	viewLedger, viewBrowse, viewApps, viewDeveloper, viewContainers, viewUnaccounted,
}

// Model is the root model: it owns the scan session and routes keys to the
// view in front.
type Model struct {
	ctx context.Context //nolint:containedctx // the parent of every scan session this model starts

	cfg  scan.Config
	keys KeyMap
	st   Styles
	help help.Model

	view view
	prev view
	w, h int

	session *session
	gen     int
	result  *scan.Result

	// quitting records that the user has already asked a running scan to
	// stop. The next quit press leaves rather than asking again, which is
	// what keeps the interface closeable while a walk is unwinding.
	quitting bool

	progress progressModel
	ledger   ledgerModel
	browse   browseModel
	apps     appsModel
	dev      sectionModel
	cont     sectionModel
	unacc    sectionModel
	why      whyModel

	// input is the filter prompt, shared by every table that has one;
	// filtering says the keyboard belongs to it and filterFor is the view
	// the typed text is applied to.
	input     textinput.Model
	filtering bool
	filterFor view

	status string
	err    error
}

// New builds the model. With initial non-nil the interface opens on that
// result, which is how a cached scan renders instantly; with nil it starts a
// scan when the program starts.
func New(ctx context.Context, cfg scan.Config, initial *scan.Result) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := &Model{
		ctx:      ctx,
		cfg:      cfg,
		keys:     DefaultKeyMap(),
		st:       NewStyles(ColorEnabled(), true),
		help:     newHelp(ColorEnabled(), true),
		w:        80,
		h:        24,
		progress: newProgress(),
		ledger:   newLedger(),
		apps:     newApps(),
		dev:      newDeveloper(),
		cont:     newContainers(),
		unacc:    newUnaccounted(),
	}
	m.browse = newBrowse(nil)
	if initial != nil {
		m.adopt(initial)
		m.view = viewLedger
	}
	return m
}

// newHelp builds the key-hint renderer with our palette.
func newHelp(color, dark bool) help.Model {
	h := help.New()
	h.Styles = HelpStyles(color, dark)
	return h
}

// Run starts the interface and blocks until the user quits.
//
// A scan that failed outright leaves the interface with nothing to show; its
// error is returned here so the caller reports it, rather than the screen
// simply closing. A cancelled context is reported as the cancellation, which
// is what the exit code is derived from.
func Run(ctx context.Context, cfg scan.Config, initial *scan.Result) error {
	final, err := tea.NewProgram(New(ctx, cfg, initial), tea.WithContext(ctx)).Run()
	if m, ok := final.(*Model); ok && m.err != nil {
		return m.err
	}
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Init starts the scan, if one is needed, and arms the listeners.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
	if m.session == nil && m.result == nil {
		m.startScan()
	}
	if m.session != nil {
		m.view = viewProgress
		cmds = append(cmds, listen(m.session), m.progress.spin.Tick, tick())
	}
	return tea.Batch(cmds...)
}

// startScan opens a new session. The generation it carries is what makes a
// late message from the previous scan harmless.
func (m *Model) startScan() tea.Cmd {
	m.gen++
	m.progress = newProgress()
	m.quitting = false
	m.session = newSession(m.ctx, m.gen, m.cfg)
	return tea.Batch(listen(m.session), m.progress.spin.Tick, tick())
}

// adopt takes a finished scan as what the views show.
func (m *Model) adopt(res *scan.Result) {
	m.result = res
	if res.Tree != nil {
		if m.browse.root == nil {
			m.browse = newBrowse(res.Tree.Root)
		} else {
			m.browse.setTree(res.Tree.Root)
		}
	}
	m.browse.setClass(res.Class)
	m.ledger.setResult(res)
	m.apps.setResult(res)
	o := m.reportOptions()
	m.dev.setResult(res, o)
	m.cont.setResult(res, o)
	m.unacc.setResult(res, o)
	m.resize()
}

// reportOptions are the settings the unaccounted view renders with.
func (m *Model) reportOptions() report.Options {
	return report.Options{
		Units:   m.cfg.Units,
		Color:   ColorEnabled(),
		Top:     -1,
		Version: m.cfg.Version,
	}
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		m.resize()
		return m, nil

	case tea.BackgroundColorMsg:
		m.st = NewStyles(ColorEnabled(), msg.IsDark())
		m.help.Styles = HelpStyles(ColorEnabled(), msg.IsDark())
		return m, nil

	case spinner.TickMsg:
		if m.session == nil {
			return m, nil
		}
		s, cmd := m.progress.spin.Update(msg)
		m.progress.spin = s
		return m, cmd

	case tickMsg:
		if m.session == nil {
			return m, nil
		}
		return m, tick()

	case progressMsg:
		if !m.current(msg.gen) {
			return m, nil
		}
		m.progress.p = progressCounters{
			Dirs: msg.p.Dirs, Files: msg.p.Files, Bytes: msg.p.Bytes,
			Errors: msg.p.Errors, Skipped: msg.p.Skipped,
			Current: msg.p.Current, Elapsed: msg.p.Elapsed,
		}
		m.progress.ticks++
		return m, listen(m.session)

	case walkDoneMsg:
		if !m.current(msg.gen) {
			return m, nil
		}
		return m, listen(m.session)

	case eventsClosedMsg:
		if !m.current(msg.gen) {
			return m, nil
		}
		return m, collect(m.session)

	case scanDoneMsg:
		return m, m.finish(msg)

	case copiedMsg:
		m.status = statusFor("copied "+msg.path, "could not copy the path", msg.err)
		return m, nil

	case openedMsg:
		m.status = statusFor("revealed "+msg.path+" in Finder", "could not open Finder", msg.err)
		return m, nil

	case tea.KeyPressMsg:
		return m, m.key(msg)
	}

	switch m.view {
	case viewDeveloper:
		return m, m.dev.Update(msg)
	case viewContainers:
		return m, m.cont.Update(msg)
	case viewUnaccounted:
		return m, m.unacc.Update(msg)
	default:
		return m, nil
	}
}

// current reports whether a message belongs to the running session.
func (m *Model) current(gen int) bool { return m.session != nil && m.session.gen == gen }

// finish takes delivery of a finished scan.
func (m *Model) finish(msg scanDoneMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.session = nil
	m.quitting = false
	if msg.err != nil {
		m.err = msg.err
		m.status = "scan failed: " + msg.err.Error()
		if m.result == nil {
			// Nothing to browse and nothing coming: say so and leave.
			return tea.Quit
		}
		return nil
	}
	if msg.res == nil {
		return nil
	}
	m.adopt(msg.res)
	m.status = m.doneStatus(msg.res)
	if m.view == viewProgress {
		m.view = viewLedger
	}
	return nil
}

// doneStatus is the line under a freshly finished scan.
func (m *Model) doneStatus(res *scan.Result) string {
	if res.Tree != nil && res.Tree.Incomplete {
		return "scan interrupted; the totals below are a lower bound"
	}
	if res.PersistErr != nil {
		return "scan finished; it was not cached (" + res.PersistErr.Error() + ")"
	}
	return "scan finished in " + fmtDuration(res.Timing.Total)
}

// statusFor picks the message for an action that may have failed.
func statusFor(ok, bad string, err error) string {
	if err != nil {
		return bad + ": " + err.Error()
	}
	return ok
}

// key routes a key press to whatever has the keyboard.
func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	// The filter input takes every key but the two that end it.
	if m.filtering {
		return m.filterKey(msg)
	}
	if m.view == viewHelp {
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m.quit()
		default:
			m.view = m.prev
			return nil
		}
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m.quit()
	case key.Matches(msg, m.keys.Help):
		m.prev, m.view = m.view, viewHelp
		return nil
	case key.Matches(msg, m.keys.Rescan):
		if m.session != nil {
			m.status = "a scan is already running"
			return nil
		}
		m.status = "rescanning"
		return m.startScan()
	case key.Matches(msg, m.keys.Switch):
		m.toggleView()
		return nil
	case key.Matches(msg, m.keys.Why):
		m.why.toggle()
		m.resize()
		return nil
	case key.Matches(msg, m.keys.Ledger):
		return m.show(viewLedger)
	case key.Matches(msg, m.keys.Browse):
		return m.show(viewBrowse)
	case key.Matches(msg, m.keys.Apps):
		return m.show(viewApps)
	case key.Matches(msg, m.keys.Developer):
		return m.show(viewDeveloper)
	case key.Matches(msg, m.keys.Containers):
		return m.show(viewContainers)
	case key.Matches(msg, m.keys.Unacc):
		return m.show(viewUnaccounted)
	}

	switch m.view {
	case viewLedger:
		return m.ledgerKey(msg)
	case viewBrowse:
		return m.browseKey(msg)
	case viewApps:
		return m.appsKey(msg)
	case viewDeveloper:
		return m.dev.Update(msg)
	case viewContainers:
		return m.cont.Update(msg)
	case viewUnaccounted:
		return m.unacc.Update(msg)
	default:
		return nil
	}
}

// show switches to a result view, which only a finished scan has.
func (m *Model) show(v view) tea.Cmd {
	if m.result != nil {
		m.view = v
	}
	return nil
}

// ledgerKey handles the bucket table and its drill-down.
func (m *Model) ledgerKey(msg tea.KeyPressMsg) tea.Cmd {
	l := &m.ledger
	switch {
	case key.Matches(msg, m.keys.Up):
		l.move(-1)
	case key.Matches(msg, m.keys.Down):
		l.move(1)
	case key.Matches(msg, m.keys.PageUp):
		l.move(-l.height)
	case key.Matches(msg, m.keys.PageDown):
		l.move(l.height)
	case key.Matches(msg, m.keys.Top):
		l.moveTo(0)
	case key.Matches(msg, m.keys.Bottom):
		l.moveTo(l.rowCount() - 1)
	case key.Matches(msg, m.keys.Open):
		drilling := l.drilling()
		n, ok := l.open()
		switch {
		case ok:
			m.openInBrowse(n)
		case drilling:
			m.status = "this row has no directory to open"
		default:
			m.status = "enter opens a path; backspace returns to the buckets"
		}
	case key.Matches(msg, m.keys.Parent):
		if !l.back() {
			m.status = "already at the buckets"
		}
	case key.Matches(msg, m.keys.Finder):
		return m.reveal()
	case key.Matches(msg, m.keys.Copy):
		return m.copy()
	}
	return nil
}

// appsKey handles the application table.
func (m *Model) appsKey(msg tea.KeyPressMsg) tea.Cmd {
	a := &m.apps
	switch {
	case key.Matches(msg, m.keys.Up):
		a.move(-1)
	case key.Matches(msg, m.keys.Down):
		a.move(1)
	case key.Matches(msg, m.keys.PageUp):
		a.move(-a.height)
	case key.Matches(msg, m.keys.PageDown):
		a.move(a.height)
	case key.Matches(msg, m.keys.Top):
		a.moveTo(0)
	case key.Matches(msg, m.keys.Bottom):
		a.moveTo(len(a.visible()) - 1)
	case key.Matches(msg, m.keys.Open):
		if n, ok := a.open(); ok {
			m.openInBrowse(n)
		} else {
			m.status = "this owner has no directory the scan walked"
		}
	case key.Matches(msg, m.keys.SortSize):
		a.sortBy(false)
		m.status = "sorted by footprint, largest first"
	case key.Matches(msg, m.keys.SortName):
		a.sortBy(true)
		m.status = "sorted by name, A to Z"
	case key.Matches(msg, m.keys.Filter):
		m.startFilter()
	case key.Matches(msg, m.keys.Escape):
		if a.filter != "" {
			a.setFilter("")
			m.status = "filter cleared"
		}
	case key.Matches(msg, m.keys.Finder):
		return m.reveal()
	case key.Matches(msg, m.keys.Copy):
		return m.copy()
	}
	return nil
}

// openInBrowse jumps the browser to a node and shows it, which is what enter
// does in the ledger drill-down and in the applications table.
func (m *Model) openInBrowse(n *walk.Node) {
	if n == nil {
		return
	}
	m.browse.jumpTo(n)
	m.view = viewBrowse
	m.status = "opened " + mac.DisplayPath(n.Path())
}

// quit cancels a running scan, or leaves when nothing is running.
//
// The first press during a scan stops the walk and waits for the partial
// tree, which is worth showing and worth caching; the second leaves without
// waiting for it. Leaving is always two presses away, however long the walk
// takes to unwind: a scan that will not stop must not hold the terminal.
//
// Leaving early costs the cached scan and nothing else. The walk is already
// cancelled, and the store writes a scan to a temporary file and renames it,
// so an interrupted write leaves the previous cache entry intact.
func (m *Model) quit() tea.Cmd {
	if m.session == nil {
		return tea.Quit
	}
	if m.quitting {
		return tea.Quit
	}
	m.quitting = true
	m.session.cancel()
	m.progress.cancelled = true
	m.status = "stopping the scan; press q again to leave now"
	return nil
}

// toggleView steps to the next result view, wrapping at the end.
func (m *Model) toggleView() {
	if m.result == nil {
		return
	}
	for i, v := range resultViews {
		if v == m.view {
			m.view = resultViews[(i+1)%len(resultViews)]
			return
		}
	}
	m.view = resultViews[0]
}

// browseKey handles the directory table's own keys.
func (m *Model) browseKey(msg tea.KeyPressMsg) tea.Cmd {
	b := &m.browse
	switch {
	case key.Matches(msg, m.keys.Up):
		b.move(-1)
	case key.Matches(msg, m.keys.Down):
		b.move(1)
	case key.Matches(msg, m.keys.PageUp):
		b.move(-b.height)
	case key.Matches(msg, m.keys.PageDown):
		b.move(b.height)
	case key.Matches(msg, m.keys.Top):
		b.moveTo(0)
	case key.Matches(msg, m.keys.Bottom):
		b.moveTo(len(b.visible()) - 1)
	case key.Matches(msg, m.keys.Open):
		if b.open() {
			m.status = ""
		} else {
			m.status = "not a directory you can open; press b to enter bundles"
		}
	case key.Matches(msg, m.keys.Parent):
		if b.parent() {
			m.status = ""
		} else {
			m.status = "already at the root of the scan"
		}
	case key.Matches(msg, m.keys.SortSize):
		b.sortBy(sortSize)
		m.status = b.sortLabel()
	case key.Matches(msg, m.keys.SortName):
		b.sortBy(sortName)
		m.status = b.sortLabel()
	case key.Matches(msg, m.keys.SortMtime):
		b.sortBy(sortMtime)
		m.status = b.sortLabel()
	case key.Matches(msg, m.keys.SortCount):
		b.sortBy(sortCount)
		m.status = b.sortLabel()
	case key.Matches(msg, m.keys.Apparent):
		b.toggleApparent()
		m.status = "showing " + sizeKind(b.opts.apparent) + " bytes"
	case key.Matches(msg, m.keys.Bundles):
		b.toggleBundles()
		m.status = "bundles are " + onOff(b.opts.bundles) + " openable"
	case key.Matches(msg, m.keys.Filter):
		m.startFilter()
	case key.Matches(msg, m.keys.Escape):
		if b.opts.filter != "" {
			b.setFilter("")
			m.status = "filter cleared"
		}
	case key.Matches(msg, m.keys.Finder):
		return m.reveal()
	case key.Matches(msg, m.keys.Copy):
		return m.copy()
	}
	return nil
}

// startFilter opens the filter prompt over whichever table is in front.
func (m *Model) startFilter() {
	in := textinput.New()
	in.Prompt = "filter: "
	in.SetValue(m.currentFilter())
	in.CursorEnd()
	in.SetWidth(max(m.w-12, 10))
	m.input = in
	m.filtering, m.filterFor = true, m.view
	m.status = "filtering; enter keeps it, esc clears it"
	m.input.Focus()
}

// currentFilter is the filter the view in front already has.
func (m *Model) currentFilter() string {
	if m.view == viewApps {
		return m.apps.filter
	}
	return m.browse.opts.filter
}

// applyFilter hands the typed text to the view the prompt was opened over.
func (m *Model) applyFilter(s string) {
	if m.filterFor == viewApps {
		m.apps.setFilter(s)
		return
	}
	m.browse.setFilter(s)
}

// filterKey feeds the filter prompt and applies what it holds.
func (m *Model) filterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.applyFilter("")
		m.status = "filter cleared"
		return nil
	case "enter":
		m.filtering = false
		m.status = "filter: " + m.input.Value()
		if m.input.Value() == "" {
			m.status = "filter cleared"
		}
		return nil
	}
	in, cmd := m.input.Update(msg)
	m.input = in
	m.applyFilter(in.Value())
	return cmd
}

// reveal opens the selected entry in Finder.
func (m *Model) reveal() tea.Cmd {
	p, ok := m.selectedPath()
	if !ok {
		return nil
	}
	return openInFinder(mac.DisplayPath(p), p)
}

// copy puts the selected path on the clipboard.
func (m *Model) copy() tea.Cmd {
	p, ok := m.selectedPath()
	if !ok {
		return nil
	}
	return copyPath(mac.DisplayPath(p))
}

// selectedPath is the scan path the Finder and clipboard keys act on: the
// row under the cursor in whichever view is in front.
//
// In Browse a row that is the aggregate of the directory's small files has no
// node of its own, so the open directory is what the keys act on.
func (m *Model) selectedPath() (string, bool) {
	switch m.view {
	case viewLedger:
		if r, ok := m.ledger.selectedRoot(); ok && r.node != nil {
			return r.node.Path(), true
		}
		return "", false
	case viewApps:
		return m.apps.selectedPath()
	}
	r, ok := m.browse.selected()
	if !ok || m.browse.dir == nil {
		return "", false
	}
	if r.node == nil {
		return m.browse.dir.Path(), true
	}
	return r.node.Path(), true
}

// resize hands every view the space it has. A table keeps two lines for its
// header and two for the footer; the why panel takes its share off the width
// beside the view, or off the height under it.
func (m *Model) resize() {
	w, textH := m.bodyWidth(), m.bodyHeight()
	tableH := max(textH-1, 1)
	m.ledger.setSize(w, tableH)
	m.browse.setSize(w, tableH)
	m.apps.setSize(w, tableH)
	m.dev.setSize(w, textH)
	m.cont.setSize(w, textH)
	m.unacc.setSize(w, textH)
}

// bodyWidth is the width the view in front is drawn at.
func (m *Model) bodyWidth() int {
	if m.whyBeside() {
		return max(m.w-m.whyWidth()-1, nameMin+bytesWidth)
	}
	return m.w
}

// bodyHeight is the height the view in front is drawn at, before its own
// header lines are taken off.
func (m *Model) bodyHeight() int {
	h := max(m.h-3, 1)
	if m.why.on && !m.whyBeside() {
		h = max(h-whyBottomLines-1, 1)
	}
	if m.banner() != "" {
		h--
	}
	return max(h, 1)
}

// whyBeside reports whether the panel goes to the right of the view rather
// than under it.
func (m *Model) whyBeside() bool { return m.why.on && m.w >= whyWideMin }

// whyWidth is the panel's width when it sits beside the view.
func (m *Model) whyWidth() int { return max(m.w*whyPanelShare/100, 24) }

// View renders the screen.
func (m *Model) View() tea.View {
	body := m.withWhy(m.bodyView())
	v := tea.NewView(m.pad(body) + "\n" + m.footer())
	v.AltScreen = true
	v.WindowTitle = "storix"
	return v
}

// bodyView draws whichever view is in front, under the banner an interrupted
// scan earns.
//
// The banner belongs to every result view rather than to the browser alone:
// a reader who lands on the ledger is being shown totals that are a lower
// bound, and that is exactly when they need telling.
func (m *Model) bodyView() string {
	if b := m.banner(); b != "" && m.view != viewProgress && m.view != viewHelp {
		return b + "\n" + m.resultView()
	}
	return m.resultView()
}

// banner is the warning an interrupted scan earns, empty otherwise.
func (m *Model) banner() string {
	if m.result == nil || m.result.Tree == nil || !m.result.Tree.Incomplete {
		return ""
	}
	return m.st.Banner.Render("  INCOMPLETE — the scan was interrupted; totals are a lower bound")
}

// resultView draws the view in front without the banner.
func (m *Model) resultView() string {
	switch m.view {
	case viewProgress:
		return m.progress.View(m.st, m.cfg.Units, m.w, m.rootName())
	case viewHelp:
		return m.helpView()
	case viewLedger:
		return m.ledger.View(m.st, m.cfg.Units)
	case viewApps:
		return m.apps.View(m.st, m.cfg.Units)
	case viewDeveloper:
		return m.dev.View(m.st)
	case viewContainers:
		return m.cont.View(m.st)
	case viewUnaccounted:
		return m.unacc.View(m.st)
	default:
		return m.browseView()
	}
}

// withWhy puts the panel beside the view on a wide terminal and under it on a
// narrow one. It is not drawn over the progress screen or the help overlay:
// neither has a selection for it to explain.
func (m *Model) withWhy(body string) string {
	if !m.why.on || m.view == viewProgress || m.view == viewHelp {
		return body
	}
	c := m.whyContent()
	h := max(m.h-2, 1)
	if !m.whyBeside() {
		return body + "\n" + m.why.render(m.st, c, m.w, whyBottomLines)
	}
	pw := m.whyWidth()
	left := block(body, m.w-pw-1, h)
	right := block(m.why.render(m.st, c, pw, h), pw, h)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

// block pads text to an exact rectangle, so the panel beside it starts in the
// same column on every line.
func block(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		lines[i] = padRight(l, w, stripStyles(l))
	}
	return strings.Join(lines, "\n")
}

// whyContent is what the panel says about the selection of the view in front.
func (m *Model) whyContent() whyContent {
	if m.result == nil {
		return whyContent{}
	}
	switch m.view {
	case viewLedger:
		if m.ledger.drilling() {
			if r, ok := m.ledger.selectedRoot(); ok {
				return nodeWhy(m.result, r.node, m.cfg.Units)
			}
			return whyContent{}
		}
		if b, _, ok := m.ledger.selectedBucket(); ok {
			return bucketWhy(b, m.cfg.Units)
		}
	case viewBrowse:
		if r, ok := m.browse.selected(); ok && r.node != nil {
			return nodeWhy(m.result, r.node, m.cfg.Units)
		}
	case viewApps:
		if e, ok := m.apps.selected(); ok {
			return appWhy(e, m.cfg.Units)
		}
	}
	return whyContent{}
}

// pad fills the screen down to the footer, so the status line and the key
// hints sit at the bottom of the window rather than floating under a short
// directory listing.
func (m *Model) pad(body string) string {
	lines := strings.Count(body, "\n") + 1
	if want := max(m.h-2, 1); lines < want {
		return body + strings.Repeat("\n", want-lines)
	}
	return body
}

// browseView is the directory table.
func (m *Model) browseView() string {
	if m.result == nil {
		return m.st.Dim.Render("  no scan yet")
	}
	return m.browse.View(m.st, m.cfg.Units, m.source())
}

// helpView is the full keymap over the whole screen.
func (m *Model) helpView() string {
	return m.st.Title.Render("  storix keys") + "\n\n" +
		m.help.FullHelpView(m.keys.FullHelp()) + "\n\n" +
		m.st.Dim.Render("  any key returns")
}

// footer is the status line and the short help.
func (m *Model) footer() string {
	status := m.status
	if m.filtering {
		status = m.input.View()
	}
	if m.session != nil && m.view != viewProgress {
		status = fmt.Sprintf("%s scanning: %s dirs, %s files, %s",
			m.progress.spin.View(), count(m.progress.p.Dirs), count(m.progress.p.Files),
			m.cfg.Units.Bytes(int64(m.progress.p.Bytes)))
	}
	return m.st.Status.Render(truncate(status, m.w)) + "\n" +
		m.help.ShortHelpView(m.keys.ShortHelp())
}

// source says whether the numbers on screen were measured now or read back.
func (m *Model) source() string {
	if m.result == nil {
		return ""
	}
	if !m.result.FromCache {
		return "live"
	}
	return "cache " + cache.Age(m.result.CacheAge) + " old"
}

// rootName is the scanned root as a user sees it.
func (m *Model) rootName() string {
	if m.result != nil {
		if r := m.result.Root(); r != "" {
			return r
		}
	}
	if len(m.cfg.Roots) > 0 {
		return mac.DisplayPath(mac.ScanPath(m.cfg.Roots[0]))
	}
	return mac.DisplayPath(mac.DataRoot)
}

// sizeKind names the byte column.
func sizeKind(apparent bool) string {
	if apparent {
		return "apparent"
	}
	return "allocated"
}

// onOff renders a toggle.
func onOff(on bool) string {
	if on {
		return "now"
	}
	return "no longer"
}
