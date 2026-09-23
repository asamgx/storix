package apps

import (
	"encoding/json"
	"testing"
)

func TestClassifySource(t *testing.T) {
	t.Parallel()
	p := Paths{Home: "/Users/andrewsam", User: "andrewsam"}
	const caskroom = "/opt/homebrew/Caskroom"
	tests := []struct {
		path string
		want Source
	}{
		{"/Applications/Spotify.app", SourceApplications},
		{"/Applications/Utilities/Terminal.app", SourceApplications},
		{"/Applications/Setapp/Paste.app", SourceSetapp},
		{"/Applications/Autodesk/AutoCAD 2027/AutoCAD 2027.app", SourceVendorFolder},
		{"/Users/andrewsam/Applications/Chrome Apps.app", SourceUserApplications},
		{"/opt/homebrew/Caskroom/stremioservice/0.1.15/StremioService.app", SourceCaskroom},
		{"/Users/andrewsam/.Trash/DynamicLakePro.app", SourceTrash},
		{"/Users/andrewsam/Library/Application Support/com.raycast.macos/Updates/1.0/Raycast.app", SourceNested},
		{"/Users/andrewsam/Downloads/Installer.app", SourceNested},
		// Four levels under /Applications is deeper than a publisher
		// folder goes, so it is a nested copy.
		{"/Applications/Vendor/Suite/Sub/Deep.app", SourceNested},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := classifySource(tc.path, p, caskroom); got != tc.want {
				t.Errorf("classifySource(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestSourceInstalled pins the orphan precondition: everything down to the
// Caskroom counts as installed, nothing below it does.
func TestSourceInstalled(t *testing.T) {
	t.Parallel()
	installed := []Source{
		SourceApplications, SourceUserApplications, SourceSetapp,
		SourceVendorFolder, SourceCaskroom,
	}
	for _, s := range installed {
		if !s.Installed() {
			t.Errorf("%v should count as installed", s)
		}
	}
	for _, s := range []Source{SourceNested, SourceTrash} {
		if s.Installed() {
			t.Errorf("%v must not count as installed", s)
		}
	}
}

// TestInventoryPrefersInstalledCopies makes sure a lookup that takes the
// first hit gets the installation and not a staged update copy, whatever
// order the facts arrived in.
func TestInventoryPrefersInstalledCopies(t *testing.T) {
	t.Parallel()
	inv := BuildInventory(nil, &Facts{AppDirBundles: []BundleInfo{
		{Path: "/Users/andrewsam/Library/Application Support/com.raycast.macos/Updates/1.0/Raycast.app",
			ID: "com.raycast.macos", DisplayName: "Raycast"},
		{Path: "/Applications/Raycast.app", ID: "com.raycast.macos", DisplayName: "Raycast"},
	}}, Paths{Home: "/Users/andrewsam", User: "andrewsam"})

	b, ok := inv.AnyBundle("com.raycast.macos")
	if !ok {
		t.Fatal("no bundle found")
	}
	if b.Path != "/Applications/Raycast.app" {
		t.Errorf("AnyBundle returned %q, want the installation", b.Path)
	}
	if _, ok := inv.Installed("com.raycast.macos"); !ok {
		t.Error("Installed should find the /Applications copy")
	}
}

// TestInventoryNestedOnlyIsNotInstalled is the inverse: an application that
// exists only as a staged copy is found but is not installed, which is what
// keeps a self-updating application from hiding every orphan.
func TestInventoryNestedOnlyIsNotInstalled(t *testing.T) {
	t.Parallel()
	inv := BuildInventory(nil, &Facts{Spotlight: []BundleInfo{
		{Path: "/Users/andrewsam/Library/Caches/x/org.sparkle-project.Sparkle/Launcher/a/Updater.app",
			ID: "com.gone.app", DisplayName: "Updater"},
	}}, Paths{Home: "/Users/andrewsam", User: "andrewsam"})

	if _, ok := inv.Installed("com.gone.app"); ok {
		t.Error("a staged copy must not satisfy Installed")
	}
	if _, ok := inv.AnyBundle("com.gone.app"); !ok {
		t.Error("the backstop should still know the copy exists")
	}
}

func TestInventoryLinksCasksToBundles(t *testing.T) {
	t.Parallel()
	inv := BuildInventory(nil, &Facts{
		AppDirBundles: []BundleInfo{
			{Path: "/Applications/Arc.app", ID: "company.thebrowser.Browser", DisplayName: "Arc"},
			{Path: "/Applications/balenaEtcher.app", ID: "io.balena.etcher", DisplayName: "balenaEtcher"},
		},
		Casks: []Cask{
			{Token: "arc", Apps: []string{"Arc.app"}},
			// Matched by its quit glob rather than by name.
			{Token: "balenaetcher", QuitIDs: []string{"io.balena.etcher.*"}},
		},
	}, Paths{Home: "/Users/andrewsam", User: "andrewsam"})

	arc, _ := inv.Installed("company.thebrowser.Browser")
	if arc == nil || arc.Cask == nil || arc.Cask.Token != "arc" {
		t.Errorf("Arc was not linked to its cask: %+v", arc)
	}
	etcher, _ := inv.Installed("io.balena.etcher")
	if etcher == nil || etcher.Cask == nil || etcher.Cask.Token != "balenaetcher" {
		t.Errorf("balenaEtcher was not linked by its quit glob: %+v", etcher)
	}
}

func TestPathsResolveAndStrip(t *testing.T) {
	t.Parallel()
	rooted := Paths{Root: "/tmp/fixture", Home: "/tmp/fixture/Users/andrewsam", User: "andrewsam"}
	if got := rooted.Resolve("/Applications"); got != "/tmp/fixture/Applications" {
		t.Errorf("Resolve = %q", got)
	}
	if got := rooted.Resolve("~/Library/Caches"); got != "/tmp/fixture/Users/andrewsam/Library/Caches" {
		t.Errorf("Resolve(~) = %q", got)
	}
	if got := rooted.Strip("/tmp/fixture/Applications/X.app"); got != "/Applications/X.app" {
		t.Errorf("Strip = %q", got)
	}

	real := Paths{Home: "/Users/andrewsam", User: "andrewsam"}
	if got := real.Resolve("/Applications"); got != "/Applications" {
		t.Errorf("an unrooted Resolve changed the path: %q", got)
	}
	if got := real.Strip("/Applications/X.app"); got != "/Applications/X.app" {
		t.Errorf("an unrooted Strip changed the path: %q", got)
	}
}

func TestTeamCacheKey(t *testing.T) {
	t.Parallel()
	if got := teamCacheKey(BundleInfo{ID: "com.openai.codex", Version: "1.0"}); got != "com.openai.codex@1.0" {
		t.Errorf("teamCacheKey = %q", got)
	}
	// A bundle with no identifier still needs a stable key.
	got := teamCacheKey(BundleInfo{Path: "/Applications/Utilities/Claude Code URL Handler.app"})
	if got != "Claude Code URL Handler@" {
		t.Errorf("teamCacheKey = %q", got)
	}
}

// TestFactsRoundTrip is the cache contract: Facts must survive JSON, because
// a scan loaded from the cache re-runs the analysis over the stored facts
// rather than asking the machine again.
func TestFactsRoundTrip(t *testing.T) {
	t.Parallel()
	before := &Facts{
		CaskroomDir:         "/opt/homebrew/Caskroom",
		Casks:               []Cask{{Token: "cursor", Apps: []string{"Cursor.app"}, ZapPaths: []string{"~/.cursor"}}},
		Receipts:            []Receipt{{PkgID: "com.example.pkg", Location: "Applications/X.app", FilesChecked: true, FilesTotal: 3}},
		Registry:            []RegistryEntry{{ID: "com.example", Path: "/Applications/X.app", InTrash: true}},
		LaunchItems:         []LaunchItem{{Path: "/Library/LaunchDaemons/a.plist", Label: "a", Program: "/bin/true", ProgramExists: true}},
		AppDirBundles:       []BundleInfo{{Path: "/Applications/X.app", ID: "com.example", MASReceipt: true}},
		TeamIDs:             map[string]string{"com.example@1.0": "ABCDE12345"},
		GroupContainerNames: []string{"HUAQ24HBR6.dev.orbstack"},
		Degraded:            []Degradation{{Probe: "lsregister", Reason: "timed out"}},
	}
	data, err := json.Marshal(before)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var after Facts
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if after.Kind() != "apps" {
		t.Errorf("Kind = %q", after.Kind())
	}
	if len(after.Casks) != 1 || after.Casks[0].Token != "cursor" ||
		len(after.Casks[0].ZapPaths) != 1 {
		t.Errorf("casks did not survive: %+v", after.Casks)
	}
	if !after.Receipts[0].FilesChecked || after.Receipts[0].FilesTotal != 3 {
		t.Errorf("receipts did not survive: %+v", after.Receipts)
	}
	if !after.Registry[0].InTrash {
		t.Errorf("registry did not survive: %+v", after.Registry)
	}
	if !after.LaunchItems[0].ProgramExists {
		t.Errorf("launch items did not survive: %+v", after.LaunchItems)
	}
	if !after.AppDirBundles[0].MASReceipt {
		t.Errorf("bundles did not survive: %+v", after.AppDirBundles)
	}
	if after.TeamIDs["com.example@1.0"] != "ABCDE12345" {
		t.Errorf("team ids did not survive: %v", after.TeamIDs)
	}
	if len(after.Degraded) != 1 {
		t.Errorf("degradations did not survive: %v", after.Degraded)
	}
}

func TestCacheLike(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"Cache", "HTTP storage", "WebKit", "Saved state", "Updater cache"} {
		if !CacheLike(c) {
			t.Errorf("CacheLike(%q) = false", c)
		}
	}
	for _, c := range []string{"Container", "Application Support", "Preferences"} {
		if CacheLike(c) {
			t.Errorf("CacheLike(%q) = true", c)
		}
	}
}

// TestBundleIdentifiersMatchTheWayMacOSMatchesThem is finding 8.
//
// Ollama's Info.plist says "com.electron.ollama" and its cache directory is
// named "com.electron.Ollama". macOS treats the two as the same identifier and
// LaunchServices answers to both; a case-sensitive index answered to neither,
// so the directory matched nothing, the application looked absent, and live
// data was reported as an orphan.
func TestBundleIdentifiersMatchTheWayMacOSMatchesThem(t *testing.T) {
	t.Parallel()
	inv := BuildInventory(nil, &Facts{
		AppDirBundles: []BundleInfo{
			{Path: "/Applications/Ollama.app", ID: "com.electron.ollama", DisplayName: "Ollama"},
		},
		Registry: []RegistryEntry{
			{ID: "com.electron.ollama", Path: "/Applications/Ollama.app", Exists: true},
		},
	}, Paths{Home: "/Users/andrewsam"})

	for _, id := range []string{"com.electron.ollama", "com.electron.Ollama", "COM.ELECTRON.OLLAMA"} {
		if b, ok := inv.Installed(id); !ok || b.ID != "com.electron.ollama" {
			t.Errorf("Installed(%q) found nothing", id)
		}
		if _, ok := inv.AnyBundle(id); !ok {
			t.Errorf("AnyBundle(%q) found nothing", id)
		}
		if got := inv.RegistryFor(id); len(got) != 1 {
			t.Errorf("RegistryFor(%q) = %v, want the one registration", id, got)
		}
	}

	// A different identifier is still a different identifier.
	if _, ok := inv.Installed("com.electron.kontena-lens"); ok {
		t.Error("folding case also folded two different identifiers together")
	}
}

// TestTheAliasTableFoldsCaseToo covers the other index an identifier goes
// through, literal rows and glob families alike.
func TestTheAliasTableFoldsCaseToo(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"com.electron.Ollama", "com.electron.ollama"} {
		p, ok := defaultIndex.LookupID(id)
		if !ok {
			t.Errorf("LookupID(%q) found nothing", id)
			continue
		}
		if p.Slug != "ollama" {
			t.Errorf("LookupID(%q) = %q, want ollama", id, p.Slug)
		}
	}
	// "com.google.Chrome.*" is a glob family, and a directory that carries
	// the identifier in another case belongs to it just the same.
	if p, ok := defaultIndex.LookupID("com.google.chrome.framework"); !ok || p.Slug != "chrome" {
		t.Errorf("LookupID of a differently-cased helper = %v, %v", p, ok)
	}
}

// TestALaunchItemOwnsAnIdentifierWhateverItsCase keeps the third index in
// step: a keep signal that is not recognised is a keep signal that was not
// found.
func TestALaunchItemOwnsAnIdentifierWhateverItsCase(t *testing.T) {
	t.Parallel()
	item := LaunchItem{Label: "com.google.Keystone.Agent", BundleIDs: []string{"com.google.Chrome"}}
	for _, id := range []string{"com.google.keystone", "com.google.Keystone", "com.google.chrome"} {
		if !item.Owns(id) {
			t.Errorf("Owns(%q) = false", id)
		}
	}
	if item.Owns("com.google.antigravity") {
		t.Error("Owns matched an unrelated identifier")
	}
}

// TestACaskQuitIDMatchesWhateverTheCase is the fourth, and the one that
// decides whether a cask can claim a directory at all.
func TestACaskQuitIDMatchesWhateverTheCase(t *testing.T) {
	t.Parallel()
	c := &Cask{Token: "ollama-app", QuitIDs: []string{"com.electron.Ollama"}}
	for _, id := range []string{"com.electron.ollama", "com.electron.Ollama"} {
		if _, ok := c.MatchesID(id); !ok {
			t.Errorf("MatchesID(%q) = false", id)
		}
	}
	globbed := &Cask{Token: "etcher", QuitIDs: []string{"io.balena.Etcher.*"}}
	for _, id := range []string{"io.balena.etcher", "io.balena.etcher.helper"} {
		if _, ok := globbed.MatchesID(id); !ok {
			t.Errorf("MatchesID(%q) against a glob = false", id)
		}
	}
}
