package classify

import "testing"

// TestSpecificityRanksSegmentClasses covers the collisions that made a
// per-segment class necessary. Every pair below is the same depth with the
// same number of literal segments, so ranking by depth and a literal count
// alone would decide them alphabetically by rule id: on a real machine that
// is fifteen directories under ~/Library/Caches landing in whichever bucket
// the loser happened to name.
func TestSpecificityRanksSegmentClasses(t *testing.T) {
	cases := []struct {
		name        string
		winner      Rule
		loser       Rule
		path        string
		alsoMatches string // a path only the loser takes
	}{
		{
			name:        "in-segment glob beats a bare capture",
			winner:      Rule{ID: "a.shipit", Match: "~/Library/Caches/*.ShipIt", Bucket: BucketAppData, Category: "Updater cache"},
			loser:       Rule{ID: "z.bundle", Match: "~/Library/Caches/{bundleid}", Bucket: BucketAppData, Category: "Cache"},
			path:        "/Users/andrew/Library/Caches/com.bitwarden.desktop.ShipIt",
			alsoMatches: "/Users/andrew/Library/Caches/com.spotify.client",
		},
		{
			name:        "a captured suffix beats a bare capture",
			winner:      Rule{ID: "z.updater", Match: "~/Library/Caches/{product}-updater", Bucket: BucketAppData, Category: "Updater cache", Owner: "{product}"},
			loser:       Rule{ID: "a.bundle", Match: "~/Library/Caches/{bundleid}", Bucket: BucketAppData, Category: "Cache", Owner: "{bundleid}"},
			path:        "/Users/andrew/Library/Caches/lens-desktop-updater",
			alsoMatches: "/Users/andrew/Library/Caches/com.spotify.client",
		},
		{
			// Both segments carry literal text, so the classes tie and the
			// amount of literal text decides: "Install macOS *.app" pins
			// down thirteen characters the bundle rule leaves open. Note
			// that neither rule sets a Priority here — the catalog does,
			// but this case exists to show the ordering stands without it.
			name:        "more literal text beats less within a class",
			winner:      Rule{ID: "z.installer", Match: "/Applications/Install macOS *.app", Bucket: BucketApps, Category: "macOS installer"},
			loser:       Rule{ID: "a.bundle", Match: "/Applications/{name}.app", Bucket: BucketApps, Category: "Application"},
			path:        "/Applications/Install macOS Tahoe.app",
			alsoMatches: "/Applications/Arc.app",
		},
		{
			name:        "an uncaptured updater glob beats a bare capture",
			winner:      Rule{ID: "z.updater-glob", Match: "~/Library/Caches/*-updater", Bucket: BucketAppData, Category: "Updater cache"},
			loser:       Rule{ID: "a.name", Match: "~/Library/Caches/{name}", Bucket: BucketAppData, Category: "Cache", Owner: "{name}"},
			path:        "/Users/andrew/Library/Caches/notion-updater",
			alsoMatches: "/Users/andrew/Library/Caches/Arc",
		},
		{
			name:        "a constrained capture beats an unconstrained one",
			winner:      Rule{ID: "z.globbed", Match: "/Applications/{name:*-beta}", Bucket: BucketApps, Category: "Beta"},
			loser:       Rule{ID: "a.plain", Match: "/Applications/{name}", Bucket: BucketApps, Category: "Vendor folder"},
			path:        "/Applications/Autodesk-beta",
			alsoMatches: "/Applications/Autodesk",
		},
		{
			name:        "a named home beats every home",
			winner:      Rule{ID: "z.home", Match: "/Users/andrew/Library", Bucket: BucketAppData, Category: "Library"},
			loser:       Rule{ID: "a.any", Match: "/Users/*/Library", Bucket: BucketPersonal, Category: "Other home"},
			path:        "/Users/andrew/Library",
			alsoMatches: "/Users/someone/Library",
		},
		{
			name:        "a deeper literal beats a shallower one on the same path",
			winner:      Rule{ID: "a.deep", Match: "~/Library/Caches/com.apple.dt.Xcode", Bucket: BucketDeveloper, Category: "Tool cache"},
			loser:       Rule{ID: "z.apple", Match: "~/Library/Caches/com.apple.{name}", Bucket: BucketAppData, Category: "Apple caches"},
			path:        "/Users/andrew/Library/Caches/com.apple.dt.Xcode",
			alsoMatches: "/Users/andrew/Library/Caches/com.apple.Safari",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Both orderings, so a pass cannot come from the order the
			// rules happen to sit in the table.
			for _, rules := range [][]Rule{{c.winner, c.loser}, {c.loser, c.winner}} {
				e, err := New(rules, Context{Home: testHome, CodeRoots: []string{}})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				cl, ok := e.Match(c.path, true)
				if !ok {
					t.Fatalf("%s matched no rule", c.path)
				}
				if cl.Source.ID != c.winner.ID {
					t.Errorf("%s resolved to %s, want %s", c.path, cl.Source.ID, c.winner.ID)
				}
				other, ok := e.Match(c.alsoMatches, true)
				if !ok || other.Source.ID != c.loser.ID {
					t.Errorf("%s resolved to %v, want %s", c.alsoMatches, other.Source.ID, c.loser.ID)
				}
			}
		})
	}
}

// TestLiteralCharsSeparatesEqualClasses pins the tie-break itself.
func TestLiteralCharsSeparatesEqualClasses(t *testing.T) {
	more := mustPattern(t, "Applications/Install macOS *.app")
	less := mustPattern(t, "Applications/{name}.app")
	if more.shape() != less.shape() {
		t.Fatalf("these two patterns should share a shape: %d vs %d", more.shape(), less.shape())
	}
	if more.literals <= less.literals {
		t.Errorf("literal characters: %d vs %d, want the installer to pin down more",
			more.literals, less.literals)
	}
}

// TestSegmentClasses pins the class of each segment form, because the whole
// ordering rests on them.
func TestSegmentClasses(t *testing.T) {
	cases := map[string]uint64{
		"Library":          classLiteral,
		"*.ShipIt":         classLiteralText,
		"Install macOS *":  classLiteralText,
		"{name}.app":       classLiteralText,
		"{name:*-beta}":    classGlobbed,
		"{name}":           classCapture,
		"*":                classAny,
		".{name}":          classLiteralText,
		"{name:*}.ShipIt":  classLiteralText,
		"com.apple.{name}": classLiteralText,
	}
	for part, want := range cases {
		seg, err := parseSegment(part)
		if err != nil {
			t.Errorf("%q: %v", part, err)
			continue
		}
		if got := seg.class(); got != want {
			t.Errorf("%q: class %d, want %d", part, got, want)
		}
	}
}

// TestShapeComparesDeepestFirst checks the packing: a difference in the
// deepest segment outranks any difference closer to the root.
func TestShapeComparesDeepestFirst(t *testing.T) {
	deepWins := mustPattern(t, "Users/*/Library/Caches/*.ShipIt")
	shallowWins := mustPattern(t, "Users/andrew/Library/Caches/{name}")
	if deepWins.shape() <= shallowWins.shape() {
		t.Errorf("a literal-text leaf under a wildcard home should outrank a bare capture under a named one: %d vs %d",
			deepWins.shape(), shallowWins.shape())
	}
	same := mustPattern(t, "Users/andrew/Library/Caches/*.ShipIt")
	if same.shape() <= deepWins.shape() {
		t.Error("with equal leaves, the named home should outrank the wildcard one")
	}
}

func mustPattern(t *testing.T, s string) pattern {
	t.Helper()
	p, err := parsePattern(s)
	if err != nil {
		t.Fatalf("parsePattern(%q): %v", s, err)
	}
	return p
}
