package tui

import (
	"os"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"

	"github.com/asamgx/storix/internal/classify"
)

// Styles is the palette of the whole interface.
//
// Colors are ANSI indices rather than hex, so they follow whatever theme the
// terminal is set to; only the two shades that have to differ between a dark
// and a light background are chosen by Dark. With color off every style is
// the identity, which is what makes the golden files readable.
type Styles struct {
	Title    lipgloss.Style
	Crumb    lipgloss.Style
	Dim      lipgloss.Style
	Label    lipgloss.Style
	Value    lipgloss.Style
	Bar      lipgloss.Style
	BarEmpty lipgloss.Style
	Selected lipgloss.Style
	Marker   lipgloss.Style
	Dir      lipgloss.Style
	Warn     lipgloss.Style
	Good     lipgloss.Style
	Banner   lipgloss.Style
	Status   lipgloss.Style

	// Chip and its variants draw the short tags the tables carry: a
	// reclaimability tag on a browse row, an application's state, how sure
	// an owner attribution is. ChipOk is for what is safe or certain,
	// ChipWarn for what wants the reader's attention, ChipDim for what is
	// merely stated.
	Chip     lipgloss.Style
	ChipOk   lipgloss.Style
	ChipWarn lipgloss.Style
	ChipDim  lipgloss.Style

	// Panel and PanelTitle draw the why panel beside or under a view.
	Panel      lipgloss.Style
	PanelTitle lipgloss.Style
}

// NewStyles builds the palette. With color false every style renders its
// argument unchanged; dark selects the shades for a dark background.
func NewStyles(color, dark bool) Styles {
	plain := lipgloss.NewStyle()
	s := Styles{
		Title: plain, Crumb: plain, Dim: plain, Label: plain, Value: plain,
		Bar: plain, BarEmpty: plain, Selected: plain, Marker: plain, Dir: plain,
		Warn: plain, Good: plain, Banner: plain, Status: plain,
		Chip: plain, ChipOk: plain, ChipWarn: plain, ChipDim: plain,
		Panel: plain, PanelTitle: plain,
	}
	if !color {
		return s
	}
	// 8 is bright black, which is a readable grey on a dark background; on a
	// light one it disappears, so the muted text is plain black-on-white
	// there.
	muted := lipgloss.Color("8")
	if !dark {
		muted = lipgloss.Color("7")
	}
	s.Title = plain.Bold(true)
	s.Crumb = plain.Bold(true).Foreground(lipgloss.Color("6"))
	s.Dim = plain.Foreground(muted)
	s.Label = plain
	s.Value = plain.Bold(true)
	s.Bar = plain.Foreground(lipgloss.Color("4"))
	s.BarEmpty = plain.Foreground(muted)
	s.Selected = plain.Reverse(true)
	s.Marker = plain.Foreground(lipgloss.Color("5"))
	s.Dir = plain.Foreground(lipgloss.Color("4"))
	s.Warn = plain.Foreground(lipgloss.Color("3"))
	s.Good = plain.Foreground(lipgloss.Color("2"))
	s.Banner = plain.Bold(true).Foreground(lipgloss.Color("3"))
	s.Status = plain.Foreground(muted)
	s.Chip = plain.Foreground(lipgloss.Color("6"))
	s.ChipOk = plain.Foreground(lipgloss.Color("2"))
	s.ChipWarn = plain.Foreground(lipgloss.Color("3"))
	s.ChipDim = plain.Foreground(muted)
	s.Panel = plain
	s.PanelTitle = plain.Bold(true).Foreground(lipgloss.Color("6"))
	return s
}

// reclaimChip is the short tag a browse row carries for its reclaimability,
// with the style that says how much attention it deserves. The tags are five
// columns at most so the column never moves.
func reclaimChip(st Styles, r classify.Reclaim) (string, lipgloss.Style) {
	switch r {
	case classify.Regenerable:
		return "regen", st.ChipOk
	case classify.ToolManaged:
		return "tool", st.ChipOk
	case classify.Orphaned:
		return "orph", st.ChipWarn
	case classify.UserData:
		return "user", st.ChipDim
	case classify.System:
		return "sys", st.ChipDim
	default:
		return "?", st.ChipDim
	}
}

// stateChip styles an application's state. Installed software is stated
// plainly; everything else is a claim about software that may not be there
// any more, which is what the reader is being asked to look at.
func stateChip(st Styles, state string) lipgloss.Style {
	switch state {
	case "installed":
		return st.ChipOk
	case "orphan-likely", "cask-only", "in-trash":
		return st.ChipWarn
	default:
		return st.ChipDim
	}
}

// confidenceChip styles how sure an owner attribution is.
func confidenceChip(st Styles, conf string) lipgloss.Style {
	switch conf {
	case "strong":
		return st.ChipOk
	case "likely":
		return st.Chip
	default:
		return st.ChipDim
	}
}

// HelpStyles is the palette of the key hints, plain when color is off so a
// captured screen holds the text and nothing else.
func HelpStyles(color, dark bool) help.Styles {
	if color {
		return help.DefaultStyles(dark)
	}
	plain := lipgloss.NewStyle()
	return help.Styles{
		Ellipsis: plain, ShortKey: plain, ShortDesc: plain, ShortSeparator: plain,
		FullKey: plain, FullDesc: plain, FullSeparator: plain,
	}
}

// ColorEnabled reports whether the interface may use color. NO_COLOR, set to
// anything, turns it off (https://no-color.org).
func ColorEnabled() bool { return os.Getenv("NO_COLOR") == "" }
