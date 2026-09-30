package tui

import (
	"strings"
	"testing"
)

// TestTabBarShortensRatherThanWraps: full labels on a wide terminal, short
// ones on a medium one, and on a narrow one only digits with the current
// view still named, so the reader always knows where they are (D54).
func TestTabBarShortensRatherThanWraps(t *testing.T) {
	st := NewStyles(false, true)
	cases := []struct {
		width int
		want  []string
		not   []string
	}{
		{120, []string{"0 Home", "1 Ledger", "4 Developer", "6 Unaccounted", "7 Plan"}, nil},
		{80, []string{"0 Home", "1 Led", "4 Dev", "6 Unacc", "7 Plan"}, []string{"Developer"}},
		{50, []string{"3 Apps", " 0 ", " 7 "}, []string{"Ledger", "Plan"}},
	}
	for _, c := range cases {
		got := tabBar(st, viewApps, c.width)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("width %d: %q lacks %q", c.width, got, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("width %d: %q still has %q", c.width, got, n)
			}
		}
		if w := len([]rune(stripStyles(got))); w > c.width {
			t.Errorf("width %d: the bar is %d wide", c.width, w)
		}
	}
}

// TestTabBarFollowsTheViewOrder: the digits are the positions in
// resultViews, which is also what tab cycles, so the bar cannot disagree
// with the keys.
func TestTabBarFollowsTheViewOrder(t *testing.T) {
	got := tabBar(NewStyles(false, true), viewDashboard, 200)
	last := -1
	for i, v := range resultViews {
		label := tabKey(i) + " " + viewNames[v][0]
		at := strings.Index(got, label)
		if at < 0 || at < last {
			t.Fatalf("%q is missing or out of order in %q", label, got)
		}
		last = at
	}
}

// TestTabBarMarksTheCurrentView: with color on, the current tab is drawn
// differently from the others.
func TestTabBarMarksTheCurrentView(t *testing.T) {
	st := NewStyles(true, true)
	a, b := tabBar(st, viewLedger, 120), tabBar(st, viewPlan, 120)
	if a == b {
		t.Error("the bar is the same whichever view is in front")
	}
	if st.TabActive.Render(" 1 Ledger ") == st.Tab.Render(" 1 Ledger ") {
		t.Error("the active and inactive tab styles are identical")
	}
}
