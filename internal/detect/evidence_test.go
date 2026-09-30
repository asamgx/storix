package detect

import (
	"slices"
	"testing"
)

// TestEvidenceForKeepsTheAnswersAboutThePath: the node detector's claim on
// the pnpm store carried yarn's version and nvm's default, because every
// answer was attached to every claim.
func TestEvidenceForKeepsTheAnswersAboutThePath(t *testing.T) {
	t.Parallel()
	lines := []Scoped{
		{Text: "unscoped"},
		{Path: "/Users/u/.npm", Text: "npm"},
		{Path: "/Users/u/Library/pnpm", Text: "pnpm"},
		{Path: "/Users/u/.nvm", Text: "nvm"},
		{Path: "/Users/u/.npmrc-not-npm", Text: "prefix only"},
	}
	cases := map[string][]string{
		"/Users/u/Library/pnpm":          {"unscoped", "pnpm"},
		"/Users/u/Library/pnpm/store/v3": {"unscoped", "pnpm"},
		"/Users/u/.npm/_npx":             {"unscoped", "npm"},
		"/Users/u":                       {"unscoped", "npm", "pnpm", "nvm", "prefix only"},
	}
	for target, want := range cases {
		if got := EvidenceFor(lines, target); !slices.Equal(got, want) {
			t.Errorf("EvidenceFor(%s) = %v, want %v", target, got, want)
		}
	}
}
