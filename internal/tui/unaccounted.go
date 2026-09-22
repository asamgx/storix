package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
)

// unaccountedModel is the scrollable account of what the scan could not see:
// the ledger identities, the skipped mounts, the unreadable paths, the cloud
// files, the snapshots and the hints.
//
// The sections are rendered by internal/report, the same code the static
// report prints, so the two can never disagree about a number.
type unaccountedModel struct {
	vp    viewport.Model
	ready bool
}

// newUnaccounted builds the empty view.
func newUnaccounted() unaccountedModel {
	return unaccountedModel{vp: viewport.New()}
}

// setSize resizes the viewport.
func (m *unaccountedModel) setSize(w, h int) {
	m.vp.SetWidth(w)
	m.vp.SetHeight(max(h, 1))
}

// setResult renders a scan into the viewport, keeping the scroll position so
// a rescan does not jump the reader back to the top.
func (m *unaccountedModel) setResult(res *scan.Result, o report.Options) {
	var b strings.Builder
	if err := report.Unaccounted(&b, res, o); err != nil {
		b.WriteString("  " + err.Error())
	}
	y := m.vp.YOffset()
	m.vp.SetContent(strings.TrimLeft(b.String(), "\n"))
	if m.ready {
		m.vp.SetYOffset(y)
	}
	m.ready = true
}

// Update forwards a message to the viewport, which owns the scroll keys.
func (m *unaccountedModel) Update(msg tea.Msg) tea.Cmd {
	vp, cmd := m.vp.Update(msg)
	m.vp = vp
	return cmd
}

// View draws the visible part of the report.
func (m *unaccountedModel) View(st Styles) string {
	if !m.ready {
		return st.Dim.Render("  nothing to show until the scan finishes")
	}
	return m.vp.View()
}
