package catalog_test

import (
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/classify/catalog"
)

// testHome is the home the test paths are written against.
const testHome = "/Users/andrew"

// engine compiles the catalog the way a scan would.
func engine(t *testing.T) *classify.Engine {
	t.Helper()
	e, err := classify.New(catalog.Rules(), classify.Context{
		Home:      testHome,
		CodeRoots: []string{testHome + "/code"},
	})
	if err != nil {
		t.Fatalf("compiling the catalog: %v", err)
	}
	return e
}

func TestRuleIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range catalog.Rules() {
		if seen[r.ID] {
			t.Errorf("duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
	}
}

func TestEveryRuleIsDescribed(t *testing.T) {
	for _, r := range catalog.Rules() {
		if r.Explain == "" {
			t.Errorf("%s: no explanation for the why panel", r.ID)
		}
		if r.Category == "" {
			t.Errorf("%s: no category", r.ID)
		}
		if !r.Bucket.Valid() {
			t.Errorf("%s: invalid bucket", r.ID)
		}
	}
}

// TestRuleCases asserts that every rule in the catalog has at least one path
// that resolves to it and at least one that does not. The negative case is
// what catches a rule that is accidentally more specific than its neighbour:
// a pattern can only steal bytes from a rule it outranks, so every rule names
// a path its neighbour must keep.
func TestRuleCases(t *testing.T) {
	e := engine(t)
	cases := ruleCases()

	for _, r := range catalog.Rules() {
		c, ok := cases[r.ID]
		if !ok {
			t.Errorf("%s: no test case; every rule needs one positive and one negative path", r.ID)
			continue
		}
		if len(c.pos) == 0 || len(c.neg) == 0 {
			t.Errorf("%s: needs at least one positive and one negative path", r.ID)
			continue
		}
		for _, p := range c.pos {
			cl, found := e.Match(p, true)
			if !found {
				t.Errorf("%s: %s matched no rule", r.ID, p)
				continue
			}
			if cl.Source.ID != r.ID {
				t.Errorf("%s: %s resolved to %s", r.ID, p, cl.Source.ID)
			}
		}
		for _, p := range c.neg {
			if cl, found := e.Match(p, true); found && cl.Source.ID == r.ID {
				t.Errorf("%s: %s should not resolve to this rule", r.ID, p)
			}
		}
	}

	for id := range cases {
		if !known(id) {
			t.Errorf("test case %q names a rule that is not in the catalog", id)
		}
	}
}

// known reports whether the catalog holds a rule with this id.
func known(id string) bool {
	for _, r := range catalog.Rules() {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestSurveyExpectations pins the bucket of the directories that dominate a
// real machine, taken from the phase 1b planning survey. These are the paths
// a tuning change is most likely to move by accident, so they are asserted by
// name rather than left to the acceptance script to notice.
func TestSurveyExpectations(t *testing.T) {
	e := engine(t)
	want := map[string]classify.Bucket{
		// ~/Library, the largest tree on the survey machine.
		testHome + "/Library/Application Support/Spotify":                classify.BucketAppData,
		testHome + "/Library/Application Support/Arc":                    classify.BucketAppData,
		testHome + "/Library/Application Support/stremio-server":         classify.BucketAppData,
		testHome + "/Library/Application Support/Notion":                 classify.BucketAppData,
		testHome + "/Library/Application Support/Code":                   classify.BucketDeveloper,
		testHome + "/Library/Application Support/Zed":                    classify.BucketDeveloper,
		testHome + "/Library/Group Containers/HUAQ24HBR6.dev.orbstack":   classify.BucketContainers,
		testHome + "/Library/Group Containers/group.com.whatsapp.family": classify.BucketAppData,
		testHome + "/Library/Caches/Arc":                                 classify.BucketAppData,
		testHome + "/Library/Caches/pnpm":                                classify.BucketDeveloper,
		testHome + "/Library/Caches/go-build":                            classify.BucketDeveloper,
		testHome + "/Library/Caches/ms-playwright":                       classify.BucketDeveloper,
		testHome + "/Library/Caches/lens-desktop-updater":                classify.BucketAppData,
		testHome + "/Library/Caches/com.bitwarden.desktop.ShipIt":        classify.BucketAppData,
		testHome + "/Library/pnpm":                                       classify.BucketDeveloper,

		// Home dot-directories.
		testHome + "/.cache":       classify.BucketDeveloper,
		testHome + "/.npm":         classify.BucketDeveloper,
		testHome + "/go":           classify.BucketDeveloper,
		testHome + "/.antigravity": classify.BucketDeveloper,
		testHome + "/.local":       classify.BucketDeveloper,
		testHome + "/.vscode":      classify.BucketDeveloper,
		testHome + "/.cursor":      classify.BucketDeveloper,
		testHome + "/.rustup":      classify.BucketDeveloper,
		testHome + "/.bun":         classify.BucketDeveloper,
		testHome + "/.claude":      classify.BucketDeveloper,
		testHome + "/.nvm":         classify.BucketDeveloper,
		testHome + "/.pyenv":       classify.BucketDeveloper,
		testHome + "/.yarn":        classify.BucketDeveloper,
		testHome + "/.codex":       classify.BucketDeveloper,
		testHome + "/.pnpm-store":  classify.BucketDeveloper,
		testHome + "/.Trash":       classify.BucketTrash,

		// Code roots and the rest of the machine.
		testHome + "/code":                                   classify.BucketDeveloper,
		testHome + "/code/gib":                               classify.BucketDeveloper,
		testHome + "/code/gib/node_modules":                  classify.BucketDeveloper,
		testHome + "/Documents":                              classify.BucketPersonal,
		testHome + "/Downloads":                              classify.BucketPersonal,
		"/Applications":                                      classify.BucketApps,
		"/Applications/Xcode.app":                            classify.BucketApps,
		"/Applications/Autodesk":                             classify.BucketApps,
		"/opt/homebrew":                                      classify.BucketDeveloper,
		"/opt/homebrew/Cellar":                               classify.BucketDeveloper,
		"/opt/homebrew/Caskroom":                             classify.BucketApps,
		"/opt/homebrew/share":                                classify.BucketDeveloper,
		"/Library/Application Support":                       classify.BucketAppData,
		"/Library/Developer":                                 classify.BucketDeveloper,
		"/Library/Updates":                                   classify.BucketSystemCaches,
		"/private/var/db":                                    classify.BucketSystemCaches,
		"/private/var/folders":                               classify.BucketSystemCaches,
		testHome + "/Library/Developer/Xcode/Archives":       classify.BucketBackups,
		testHome + "/Library/Application Support/MobileSync": classify.BucketBackups,
	}
	for p, b := range want {
		cl, ok := e.Match(p, true)
		if !ok {
			t.Errorf("%s: matched no rule, so it would land in Other", p)
			continue
		}
		if cl.Bucket != b {
			t.Errorf("%s: bucket %s, want %s (rule %s)", p, cl.Bucket, b, cl.Source.ID)
		}
	}
}

// TestOwnerCaptures checks that the capturing rules name the right owner.
func TestOwnerCaptures(t *testing.T) {
	e := engine(t)
	want := map[string]string{
		testHome + "/Library/Caches/com.spotify.client":           "com.spotify.client",
		testHome + "/Library/Caches/com.bitwarden.desktop.ShipIt": "com.bitwarden.desktop",
		testHome + "/Library/Caches/lens-desktop-updater":         "lens-desktop",
		testHome + "/Library/Application Support/Slack":           "Slack",
		testHome + "/.cache/uv":                                   "uv",
		"/Applications/Arc.app":                                   "Arc",
		"/opt/homebrew/Caskroom/cursor":                           "cursor",
		"/opt/homebrew/Cellar/ffmpeg":                             "ffmpeg",
	}
	for p, owner := range want {
		cl, ok := e.Match(p, true)
		if !ok {
			t.Errorf("%s: matched no rule", p)
			continue
		}
		if cl.Owner != owner {
			t.Errorf("%s: owner %q, want %q (rule %s)", p, cl.Owner, owner, cl.Source.ID)
		}
	}
}

// TestOwnerKeyPrefixes enforces R1: every owner key is prefixed, so a join
// between an application's footprint and a rule's claim cannot accidentally
// match a bundle id against a cask token.
func TestOwnerKeyPrefixes(t *testing.T) {
	allowed := map[string]bool{
		"app": true, "team": true, "vendor": true, "cask": true,
		"cli": true, "project": true, "product": true, "unknown": true,
	}
	for _, r := range catalog.Rules() {
		for _, k := range r.OwnerKeys {
			if k == "macos" {
				continue
			}
			prefix, rest, found := cut(k)
			if !found || !allowed[prefix] || rest == "" {
				t.Errorf("%s: owner key %q is not one of the allowed prefixed forms", r.ID, k)
			}
		}
	}
}

// cut splits an owner key at its first colon.
func cut(k string) (prefix, rest string, found bool) {
	for i := range len(k) {
		if k[i] == ':' {
			return k[:i], k[i+1:], true
		}
	}
	return k, "", false
}
