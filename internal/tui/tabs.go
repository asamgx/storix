package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// viewNames are each result view's label in the tab bar: the full word, and
// the short form a narrower terminal gets.
var viewNames = map[view][2]string{
	viewDashboard:   {"Home", "Home"},
	viewLedger:      {"Ledger", "Led"},
	viewBrowse:      {"Browse", "Brw"},
	viewApps:        {"Apps", "Apps"},
	viewDeveloper:   {"Developer", "Dev"},
	viewContainers:  {"Containers", "Ctr"},
	viewUnaccounted: {"Unaccounted", "Unacc"},
	viewPlan:        {"Plan", "Plan"},
}

// tabKey is the digit that selects a view: its position in resultViews,
// which is also the order tab cycles in, so the bar and the keys cannot
// disagree.
func tabKey(i int) string { return strconv.Itoa(i) }

// Widths at which the tab bar gives up its full labels, and then its labels.
const (
	tabsFullMin  = 100
	tabsShortMin = 60
)

// tabBar is the one line above every result view that says which views
// exist, which key reaches each, and which one is in front (D54).
//
// It shortens rather than wraps: full labels on a wide terminal, short ones
// on a medium one, and on a narrow one only the digits with the current
// view still named, so the reader always knows where they are.
func tabBar(st Styles, current view, width int) string {
	var parts []string
	for i, v := range resultViews {
		names := viewNames[v]
		label := tabKey(i) + " " + names[0]
		switch {
		case width < tabsShortMin && v != current:
			label = tabKey(i)
		case width < tabsShortMin:
			label = tabKey(i) + " " + names[0]
		case width < tabsFullMin:
			label = tabKey(i) + " " + names[1]
		}
		if v == current {
			parts = append(parts, st.TabActive.Render(" "+label+" "))
		} else {
			parts = append(parts, st.Tab.Render(" "+label+" "))
		}
	}
	line := strings.Join(parts, "")
	if lipgloss.Width(line) > width {
		return truncate(stripStyles(line), width)
	}
	return line
}
