package tui

import (
	"io"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
)

// sectionModel is a scrollable view over one section of the static report.
//
// The unaccounted account, the developer section and the containers section
// are the same widget three times: internal/report renders the text, a
// viewport scrolls it, and the TUI adds nothing of its own. Sharing the
// widget is what guarantees the three views and the printed report can never
// disagree about a number, because none of them holds any arithmetic.
//
// The render runs once, when a result is adopted, rather than on every frame:
// a section is a few hundred lines of text that only a new scan can change.
type sectionModel struct {
	vp     viewport.Model
	render func(io.Writer, *scan.Result, report.Options) error
	// empty is what the view says when the section had nothing to print,
	// which on a machine without Docker or without a toolchain is the
	// ordinary answer rather than a fault.
	empty string
	ready bool
	// blank records that the last render produced no text.
	blank bool
}

// newSection builds an empty view over one report renderer.
func newSection(render func(io.Writer, *scan.Result, report.Options) error, empty string) sectionModel {
	return sectionModel{vp: viewport.New(), render: render, empty: empty}
}

// setSize resizes the viewport.
func (m *sectionModel) setSize(w, h int) {
	m.vp.SetWidth(w)
	m.vp.SetHeight(max(h, 1))
}

// setResult renders a scan into the viewport, keeping the scroll position so
// a rescan does not jump the reader back to the top.
func (m *sectionModel) setResult(res *scan.Result, o report.Options) {
	var b strings.Builder
	if err := m.render(&b, res, o); err != nil {
		b.WriteString("  " + err.Error())
	}
	text := strings.TrimLeft(b.String(), "\n")
	y := m.vp.YOffset()
	m.vp.SetContent(text)
	if m.ready {
		m.vp.SetYOffset(y)
	}
	m.blank = strings.TrimSpace(text) == ""
	m.ready = true
}

// Update forwards a message to the viewport, which owns the scroll keys.
func (m *sectionModel) Update(msg tea.Msg) tea.Cmd {
	vp, cmd := m.vp.Update(msg)
	m.vp = vp
	return cmd
}

// View draws the visible part of the section.
func (m *sectionModel) View(st Styles) string {
	switch {
	case !m.ready:
		return st.Dim.Render("  nothing to show until the scan finishes")
	case m.blank:
		return st.Dim.Render("  " + m.empty)
	default:
		return m.vp.View()
	}
}
