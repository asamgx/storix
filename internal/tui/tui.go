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

	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
)

// view is the screen the model is showing.
type view uint8

const (
	viewProgress view = iota
	viewBrowse
	viewUnaccounted
	viewHelp
)

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

	progress progressModel
	browse   browseModel
	unacc    unaccountedModel

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
		unacc:    newUnaccounted(),
	}
	m.browse = newBrowse(nil)
	if initial != nil {
		m.adopt(initial)
		m.view = viewBrowse
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
	m.unacc.setResult(res, m.reportOptions())
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

	if m.view == viewUnaccounted {
		return m, m.unacc.Update(msg)
	}
	return m, nil
}

// current reports whether a message belongs to the running session.
func (m *Model) current(gen int) bool { return m.session != nil && m.session.gen == gen }

// finish takes delivery of a finished scan.
func (m *Model) finish(msg scanDoneMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.session = nil
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
		m.view = viewBrowse
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
	if m.browse.filtering && m.view == viewBrowse {
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
	case key.Matches(msg, m.keys.Browse):
		if m.result != nil {
			m.view = viewBrowse
		}
		return nil
	case key.Matches(msg, m.keys.Unacc):
		if m.result != nil {
			m.view = viewUnaccounted
		}
		return nil
	}

	switch m.view {
	case viewBrowse:
		return m.browseKey(msg)
	case viewUnaccounted:
		return m.unacc.Update(msg)
	default:
		return nil
	}
}

// quit cancels a running scan, or leaves when nothing is running.
//
// The first press during a scan stops the walk and waits for the partial
// tree, which is worth showing and worth caching; the second leaves.
func (m *Model) quit() tea.Cmd {
	if m.session != nil {
		m.session.cancel()
		m.progress.cancelled = true
		m.status = "stopping the scan; the partial tree will be shown"
		return nil
	}
	return tea.Quit
}

// toggleView swaps the two result views.
func (m *Model) toggleView() {
	if m.result == nil {
		return
	}
	if m.view == viewUnaccounted {
		m.view = viewBrowse
		return
	}
	m.view = viewUnaccounted
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

// startFilter opens the filter prompt over the current directory.
func (m *Model) startFilter() {
	in := textinput.New()
	in.Prompt = "filter: "
	in.SetValue(m.browse.opts.filter)
	in.CursorEnd()
	in.SetWidth(max(m.w-12, 10))
	m.browse.filter = in
	m.browse.filtering = true
	m.status = "filtering; enter keeps it, esc clears it"
	m.browse.filter.Focus()
}

// filterKey feeds the filter prompt and applies what it holds.
func (m *Model) filterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.browse.filtering = false
		m.browse.setFilter("")
		m.status = "filter cleared"
		return nil
	case "enter":
		m.browse.filtering = false
		m.status = "filter: " + m.browse.filter.Value()
		if m.browse.filter.Value() == "" {
			m.status = "filter cleared"
		}
		return nil
	}
	in, cmd := m.browse.filter.Update(msg)
	m.browse.filter = in
	m.browse.setFilter(in.Value())
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

// selectedPath is the scan path under the cursor, or the open directory when
// the cursor is on the aggregate of small files.
func (m *Model) selectedPath() (string, bool) {
	r, ok := m.browse.selected()
	if !ok || m.browse.dir == nil {
		return "", false
	}
	if r.node == nil {
		return m.browse.dir.Path(), true
	}
	return r.node.Path(), true
}

// resize hands every view the space it has. The browse table keeps two lines
// for its header and two for the footer.
func (m *Model) resize() {
	m.browse.setSize(m.w, max(m.h-4, 1))
	m.unacc.setSize(m.w, max(m.h-3, 1))
}

// View renders the screen.
func (m *Model) View() tea.View {
	var body string
	switch m.view {
	case viewProgress:
		body = m.progress.View(m.st, m.cfg.Units, m.w, m.rootName())
	case viewHelp:
		body = m.helpView()
	case viewUnaccounted:
		body = m.unacc.View(m.st)
	default:
		body = m.browseView()
	}
	v := tea.NewView(m.pad(body) + "\n" + m.footer())
	v.AltScreen = true
	v.WindowTitle = "storix"
	return v
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

// browseView is the table plus the banner an interrupted scan earns.
func (m *Model) browseView() string {
	if m.result == nil {
		return m.st.Dim.Render("  no scan yet")
	}
	var b strings.Builder
	if m.result.Tree != nil && m.result.Tree.Incomplete {
		b.WriteString(m.st.Banner.Render("  INCOMPLETE — the scan was interrupted; totals are a lower bound"))
		b.WriteByte('\n')
	}
	b.WriteString(m.browse.View(m.st, m.cfg.Units, m.source()))
	return b.String()
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
	if m.browse.filtering && m.view == viewBrowse {
		status = m.browse.filter.View()
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
