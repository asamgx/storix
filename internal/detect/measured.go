package detect

import (
	"path"
	"strings"

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
