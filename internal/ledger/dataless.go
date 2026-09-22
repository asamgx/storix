package ledger

import (
	"strings"

	"github.com/asamgx/storix/internal/walk"
)

// datalessLocations are the places evicted cloud files collect, named the way
// a user would name them. The patterns are matched against display paths;
// "*" stands for exactly one path segment, which is the user name.
var datalessLocations = []struct {
	label   string
	pattern []string
}{
	{"~/Library/Mobile Documents", []string{"Users", "*", "Library", "Mobile Documents"}},
	{"~/Library/CloudStorage", []string{"Users", "*", "Library", "CloudStorage"}},
	{"~/Desktop", []string{"Users", "*", "Desktop"}},
	{"~/Documents", []string{"Users", "*", "Documents"}},
}

// elsewhere collects dataless files outside the named locations.
const elsewhere = "elsewhere"

// datalessSummary counts the evicted cloud items the walk met.
//
// Their blocks are already zero, so they change no total; what they explain
// is a directory that looks empty on disk and is not. Retained files
// contribute their apparent size, aggregated ones only their count, because
// an aggregate keeps no per-file sizes.
func datalessSummary(t *walk.Tree) Dataless {
	var d Dataless
	counts := map[string]*Line{}

	add := func(loc string, files int64, apparent int64) {
		l, ok := counts[loc]
		if !ok {
			l = &Line{ID: loc, Label: loc, Known: true, Note: "in the cloud, not on disk"}
			counts[loc] = l
		}
		l.Count += files
		l.Bytes += apparent
		d.Files += files
		d.Apparent += apparent
	}

	for _, n := range t.Nodes {
		dataless := n.Has(walk.FlagDataless)
		if !dataless && n.Small.Dataless == 0 {
			continue
		}
		// Path building is O(depth), so it happens only for the handful of
		// nodes that actually carry cloud content.
		if dataless {
			add(locationOf(n.Display()), 1, n.Apparent)
		}
		if n.Small.Dataless > 0 {
			add(locationOf(n.Display()), int64(n.Small.Dataless), 0)
		}
	}

	d.ByLocation = orderedLines(counts)
	return d
}

// orderedLines returns the location lines in the fixed order of
// datalessLocations, with anything unclassified last.
func orderedLines(counts map[string]*Line) []Line {
	var out []Line
	for _, loc := range datalessLocations {
		if l, ok := counts[loc.label]; ok {
			out = append(out, *l)
		}
	}
	if l, ok := counts[elsewhere]; ok {
		out = append(out, *l)
	}
	return out
}

// locationOf classifies a display path into one of the named cloud
// locations.
func locationOf(display string) string {
	segs := strings.Split(strings.TrimPrefix(display, "/"), "/")
	for _, loc := range datalessLocations {
		if matchSegments(loc.pattern, segs) {
			return loc.label
		}
	}
	return elsewhere
}

// matchSegments reports whether pattern is a prefix of segs, with "*"
// matching any single segment.
func matchSegments(pattern, segs []string) bool {
	if len(segs) < len(pattern) {
		return false
	}
	for i, p := range pattern {
		if p != "*" && p != segs[i] {
			return false
		}
	}
	return true
}
