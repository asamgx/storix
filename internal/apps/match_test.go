package apps

import (
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
)

// testResolver builds a resolver over a hand-made inventory, for the steps
// the corpus does not reach.
func testResolver(t *testing.T, f *Facts) *resolver {
	t.Helper()
	return &resolver{
		inv:               BuildInventory(nil, f, Paths{Home: "/Users/andrewsam", User: "andrewsam"}),
		prods:             defaultIndex,
		codesignAvailable: true,
		projectNames:      map[string]string{"mochi": "/Users/andrewsam/code"},
	}
}

func candidate(name, dir string, key KeyKind) Candidate {
	return Candidate{
		Name:    name,
		RawName: name,
		Path:    dir + "/" + name,
		Loc:     Location{Dir: dir, Key: key, Category: "Cache", Reclaim: classify.Regenerable, Depth: 1},
	}
}

// TestResolveReceiptMissingLocation is the GlobalProtect case: a receipt
// whose install location and files are gone is the strongest orphan evidence
// there is, and the evidence line has to say so in words.
func TestResolveReceiptMissingLocation(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{Receipts: []Receipt{{
		PkgID:          "com.example.thing.pkg",
		Volume:         "/",
		Location:       "Applications/Thing.app",
		LocationExists: false,
		FilesChecked:   true,
		FilesPresent:   0,
		FilesTotal:     42,
	}}})

	m := r.Resolve(candidate("com.example.thing.pkg", "/private/var/db/receipts", KeyReceipt))
	if m.Rule != "apps/receipt" {
		t.Fatalf("Rule = %q, want apps/receipt", m.Rule)
	}
	if m.Confidence != classify.Likely {
		t.Errorf("Confidence = %v, want likely", m.Confidence)
	}
	joined := strings.Join(m.Evidence, "\n")
	for _, want := range []string{"location missing", "0 of 42 files present", "com.example.thing.pkg"} {
		if !strings.Contains(joined, want) {
			t.Errorf("evidence is missing %q:\n%s", want, joined)
		}
	}
}

// TestResolveVendorPrefixSingleApp is step 7's safe case: one installed
// application carries the vendor, so a sibling directory is its.
func TestResolveVendorPrefixSingleApp(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{AppDirBundles: []BundleInfo{
		{Path: "/Applications/Thing.app", ID: "com.solovendor.thing", DisplayName: "Thing"},
	}})

	m := r.Resolve(candidate("com.solovendor.helper", "/Users/andrewsam/Library/Caches", KeyNameOrID))
	if m.Rule != "apps/vendor-prefix" {
		t.Fatalf("Rule = %q, want apps/vendor-prefix; evidence %v", m.Rule, m.Evidence)
	}
	if m.Owner.Key != "app:com.solovendor.thing" {
		t.Errorf("Owner.Key = %q", m.Owner.Key)
	}
	if m.Confidence != classify.Likely {
		t.Errorf("Confidence = %v, want likely", m.Confidence)
	}
}

// TestResolveVendorPrefixSeveralApps is the case the rule is narrow for. Two
// applications share the vendor, so nothing but an alias may pick between
// them and the owner is the vendor itself.
func TestResolveVendorPrefixSeveralApps(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{AppDirBundles: []BundleInfo{
		{Path: "/Applications/One.app", ID: "com.bigvendor.one", DisplayName: "One"},
		{Path: "/Applications/Two.app", ID: "com.bigvendor.two", DisplayName: "Two"},
	}})

	m := r.Resolve(candidate("com.bigvendor.mystery", "/Users/andrewsam/Library/Caches", KeyNameOrID))
	if m.Owner.Kind != KindVendor {
		t.Fatalf("Owner.Kind = %v, want vendor; rule %q", m.Owner.Kind, m.Rule)
	}
	if m.Owner.Key != "vendor:com.bigvendor" {
		t.Errorf("Owner.Key = %q", m.Owner.Key)
	}
	if m.Confidence != classify.Corroborating {
		t.Errorf("Confidence = %v, want corroborating", m.Confidence)
	}
}

// TestResolveDistinctRefusesVendor is the Antigravity and Atlas guard at the
// vendor step: a distinct product never inherits a sibling's identity, even
// when that sibling is the only installed application of the vendor.
func TestResolveDistinctRefusesVendor(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{AppDirBundles: []BundleInfo{
		{Path: "/Applications/ChatGPT.app", ID: "com.openai.codex", DisplayName: "ChatGPT"},
	}})

	m := r.Resolve(candidate("com.openai.atlas", "/Users/andrewsam/Library/Caches", KeyNameOrID))
	if m.Owner.Key != "product:atlas" {
		t.Fatalf("Owner.Key = %q, want product:atlas; rule %q", m.Owner.Key, m.Rule)
	}
	if m.Owner.Label != "Atlas" {
		t.Errorf("Owner.Label = %q, want Atlas", m.Owner.Label)
	}
}

// TestResolveHelperSuffix covers step 1's suffix handling: an application's
// helpers carry its identifier with one or two extra labels.
func TestResolveHelperSuffix(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{AppDirBundles: []BundleInfo{
		{Path: "/Applications/Bitwarden.app", ID: "com.bitwarden.desktop", DisplayName: "Bitwarden"},
	}})

	for _, name := range []string{
		"com.bitwarden.desktop.safari",
		"com.bitwarden.desktop.helper.renderer",
	} {
		m := r.Resolve(candidate(name, "/Users/andrewsam/Library/Caches", KeyNameOrID))
		if m.Owner.Key != "app:com.bitwarden.desktop" {
			t.Errorf("%s resolved to %q by %q", name, m.Owner.Key, m.Rule)
		}
		if m.Confidence != classify.Strong {
			t.Errorf("%s confidence = %v, want strong", name, m.Confidence)
		}
	}
}

// TestResolveGroupContainerStripsWrappers covers the WhatsApp shape: a group
// container whose name is an ordinary identifier behind a "group." prefix.
func TestResolveGroupContainerStripsWrappers(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{AppDirBundles: []BundleInfo{
		{Path: "/Applications/WhatsApp.app", ID: "net.whatsapp.WhatsApp", DisplayName: "WhatsApp"},
	}})

	m := r.Resolve(candidate("group.net.whatsapp.WhatsApp.shared",
		"/Users/andrewsam/Library/Group Containers", KeyGroupContainer))
	if m.Owner.Key != "app:net.whatsapp.WhatsApp" {
		t.Fatalf("Owner.Key = %q, rule %q, evidence %v", m.Owner.Key, m.Rule, m.Evidence)
	}
	if m.Confidence != classify.Strong {
		t.Errorf("Confidence = %v, want strong", m.Confidence)
	}
}

// TestResolveUnresolvedTeamIDSaysSo is the degradation the plan asks for: a
// team id nothing could be matched to must be reported as unresolved rather
// than as an owner that does not exist.
func TestResolveUnresolvedTeamIDSaysSo(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{})
	r.codesignAvailable = false

	m := r.Resolve(candidate("HUAQ24HBR6.dev.orbstack",
		"/Users/andrewsam/Library/Group Containers", KeyGroupContainer))
	if m.Owner.Kind != KindUnknown {
		t.Errorf("Owner.Kind = %v, want unknown", m.Owner.Kind)
	}
	joined := strings.Join(m.Evidence, "\n")
	if !strings.Contains(joined, "team id unresolved: codesign unavailable") {
		t.Errorf("evidence does not explain the degradation:\n%s", joined)
	}
}

// TestResolveCaskOnlyWithoutReceipt is the degraded cask case: with the
// receipt unreadable the cask still exists, but it can no longer name the
// data it owns, so the directory falls through to unknown rather than being
// attributed to the wrong application.
func TestResolveCaskOnlyWithoutReceipt(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{Casks: []Cask{
		{Token: "cursor", ReceiptErr: "permission denied"},
	}})

	m := r.Resolve(candidate("com.todesktop.9999", "/Users/andrewsam/Library/Caches", KeyNameOrID))
	if m.Owner.Kind != KindUnknown {
		t.Errorf("Owner.Kind = %v, want unknown; rule %q", m.Owner.Kind, m.Rule)
	}
}

// TestResolveOwnBuildByProjectName covers the second own-build signal: the
// directory is named after a project under a code root.
func TestResolveOwnBuildByProjectName(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{})

	m := r.Resolve(candidate("mochi", "/Users/andrewsam/Library/Application Support", KeyNameOrID))
	if m.Owner.Key != "project:mochi" || m.Rule != "apps/own-build" {
		t.Fatalf("Owner.Key = %q, rule = %q", m.Owner.Key, m.Rule)
	}
	if m.Owner.Kind != KindOwnBuild {
		t.Errorf("Owner.Kind = %v, want own-build", m.Owner.Kind)
	}
}

// TestResolveUpdaterSuffixIsRegenerable checks the category and reclaim
// override the updater step applies, which is what R2 asks for.
func TestResolveUpdaterSuffixIsRegenerable(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{AppDirBundles: []BundleInfo{
		{Path: "/Applications/Lens.app", ID: "com.electron.kontena-lens", DisplayName: "Lens"},
	}})

	m := r.Resolve(candidate("lens-desktop-updater", "/Users/andrewsam/Library/Caches", KeyNameOrID))
	if m.Rule != "apps/updater-suffix" {
		t.Fatalf("Rule = %q", m.Rule)
	}
	if m.Category != "Updater cache" {
		t.Errorf("Category = %q", m.Category)
	}
	if !m.HasReclaim || m.Reclaim != classify.Regenerable {
		t.Errorf("Reclaim = %v (set %v), want regenerable", m.Reclaim, m.HasReclaim)
	}
	if m.Owner.Key != "app:com.electron.kontena-lens" {
		t.Errorf("Owner.Key = %q", m.Owner.Key)
	}
}

func TestResolveUnknownListsWhatWasTried(t *testing.T) {
	t.Parallel()
	r := testResolver(t, &Facts{})
	m := r.Resolve(candidate("SomethingNobodyKnows", "/Users/andrewsam/Library/Caches", KeyNameOrID))
	if m.Owner.Key != "unknown:SomethingNobodyKnows" {
		t.Errorf("Owner.Key = %q", m.Owner.Key)
	}
	if m.Confidence != classify.UnknownOwner {
		t.Errorf("Confidence = %v", m.Confidence)
	}
	if len(m.Evidence) == 0 {
		t.Error("an unknown owner should still say what was tried")
	}
}

func TestNormalizeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind KeyKind
		in   string
		want string
	}{
		{KeyPlist, "com.google.Chrome.plist", "com.google.Chrome"},
		{KeyByHostPlist, "com.todesktop.abc.ShipIt.0123ABCD-4567-89EF-0123-456789ABCDEF.plist", "com.todesktop.abc.ShipIt"},
		{KeySavedState, "company.thebrowser.Browser.savedState", "company.thebrowser.Browser"},
		{KeyCookies, "com.spotify.client.binarycookies", "com.spotify.client"},
		{KeyReceipt, "com.autodesk.cer.bom", "com.autodesk.cer"},
		{KeyReceipt, "com.autodesk.cer.plist", "com.autodesk.cer"},
		{KeyLaunchItem, "com.google.keystone.agent.plist", "com.google.keystone.agent"},
		{KeyNameOrID, "Application Support", "Application Support"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeName(tc.kind, tc.in); got != tc.want {
				t.Errorf("NormalizeName(%v, %q) = %q, want %q", tc.kind, tc.in, got, tc.want)
			}
		})
	}
}

func TestTrimUpdaterSuffix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in    string
		base  string
		found bool
	}{
		{"notion.id.ShipIt", "notion.id", true},
		{"lens-desktop-updater", "lens-desktop", true},
		{"bitwarden-updater", "bitwarden", true},
		{"Sparkle.Updater", "Sparkle", true},
		{"Code", "Code", false},
		{".ShipIt", ".ShipIt", false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			base, found := TrimUpdaterSuffix(tc.in)
			if base != tc.base || found != tc.found {
				t.Errorf("TrimUpdaterSuffix(%q) = (%q, %v), want (%q, %v)", tc.in, base, found, tc.base, tc.found)
			}
		})
	}
}
