package detect

import (
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/walk"
)

// Rebase moves a path a probe reported into the home Classify works against.
//
// The two halves of a detector see two different homes. A probe runs against
// the real filesystem and the paths it is told — `pnpm store path` →
// /Users/andrewsam/Library/pnpm/store/v10 — are rooted in the invoking user's
// home. Classify runs against a finished tree whose display paths are rooted
// in [classify.Context.Home], which on a real scan is the same string and in a
// test is the fixture's display home while the probe saw a temporary
// directory. Rewriting the prefix is what lets a detector use the measured
// answer in both, instead of throwing away the measurement in tests.
//
// A path outside the probe's home is returned unchanged: /opt/homebrew and
// /Library/Ruby are the same on both sides.
func Rebase(p, from, to string) string {
	switch {
	case p == "" || from == "" || to == "" || from == to:
		return p
	case p == from:
		return to
	case strings.HasPrefix(p, from+"/"):
		return path.Join(to, p[len(from)+1:])
	}
	return p
}

// Prefer is the first of several candidate paths the walk actually retained,
// or the first candidate when it retained none.
//
// It is how a detector uses what the tool told it without losing the bytes
// when the tool is wrong. `npm config get cache` naming a directory the walk
// never saw means the cache is somewhere the scan did not reach, and claiming
// the default instead keeps the ordinary case correct; claiming nothing would
// leave real bytes in Other.
func Prefer(t *walk.Tree, candidates ...string) string {
	first := ""
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if first == "" {
			first = c
		}
		if _, ok := lookup(t, c); ok {
			return c
		}
	}
	return first
}

// ChildNames are the names of a directory's children as the walk retained
// them, or nothing when the walk never saw the directory.
//
// It is how a detector enumerates a set it cannot know in advance — the
// subdirectories of ~/.cache, the store generations under ~/Library/pnpm —
// without listing the real filesystem a second time. The walk has already
// been there, and asking it costs nothing.
func ChildNames(t *walk.Tree, display string) []string {
	n, ok := lookup(t, display)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		names = append(names, c.Name)
	}
	return names
}

// CodeRoots are the context's code roots as absolute display paths, with "~"
// resolved against the context's own home rather than the process's.
func CodeRoots(cx classify.Context) []string {
	roots := cx.CodeRoots
	if roots == nil {
		roots = classify.DefaultCodeRoots
	}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		switch {
		case r == "":
		case r == "~":
			if cx.Home != "" {
				out = append(out, cx.Home)
			}
		case strings.HasPrefix(r, "~/"):
			if cx.Home != "" {
				out = append(out, path.Join(cx.Home, r[2:]))
			}
		default:
			out = append(out, path.Clean(r))
		}
	}
	return out
}

// Measure moves the paths a probe reported into Classify's home and refuses
// the ones that land inside a code root.
//
// A tool asked for its cache answers relative to its configuration, and its
// configuration can come from a project: `yarn config get cacheFolder` inside
// a Berry project names that project's .yarn/cache, which is often committed.
// A path under a code root is the user's source until something proves
// otherwise, so a measurement that lands there is dropped and the caller
// falls back to its static default, and Notes says why for the why panel.
type Measure struct {
	// From is the home the probe ran against; To is Classify's.
	From, To string
	// Notes are the evidence lines for the measurements refused.
	Notes []string

	roots []string
}

// NewMeasure builds a Measure for one Classify call.
func NewMeasure(from string, cx classify.Context) *Measure {
	m := &Measure{From: from, To: cx.Home}
	for _, r := range CodeRoots(cx) {
		// A root that is the home or above it would refuse every
		// measurement, including the defaults it would fall back to;
		// such a root says where projects are, not that the whole home
		// is source.
		if r == "/" || r == cx.Home || strings.HasPrefix(cx.Home, strings.TrimRight(r, "/")+"/") {
			continue
		}
		m.roots = append(m.roots, r)
	}
	return m
}

// Path is a measured path rebased into Classify's home, or "" when there was
// no measurement or it lies inside a code root.
func (m *Measure) Path(measured string) string {
	if m == nil {
		return measured
	}
	p := Rebase(measured, m.From, m.To)
	if p == "" {
		return ""
	}
	for _, r := range m.roots {
		if p == r || strings.HasPrefix(p, strings.TrimRight(r, "/")+"/") {
			m.Notes = append(m.Notes, "a tool reported "+p+", which is inside the code root "+r+
				"; it is treated as project source and the default location is used instead")
			return ""
		}
	}
	return p
}

// At is Prefer over the measured path and a fallback.
func (m *Measure) At(t *walk.Tree, measured, fallback string) string {
	return Prefer(t, m.Path(measured), fallback)
}
