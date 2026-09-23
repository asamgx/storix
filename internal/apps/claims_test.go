package apps

import (
	"slices"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
)

// claimsByPath indexes an analysis's claims by the display path they are
// about, which is how every assertion here reads.
func claimsByPath(t *testing.T, a *Analysis) map[string]classify.Claim {
	t.Helper()
	out := make(map[string]classify.Claim)
	for _, cl := range a.Claims() {
		if cl.Node == nil {
			t.Error("a claim was emitted with no node")
			continue
		}
		out[cl.Node.Display()] = cl
	}
	return out
}

// TestClaimsBucketBundles puts every installed application in bucket 2, which
// is the floor the Applications row of the ledger stands on.
func TestClaimsBucketBundles(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)
	claims := claimsByPath(t, a)

	vscode, ok := claims[c.Root+"/Applications/Visual Studio Code.app"]
	if !ok {
		t.Fatal("no claim for the VS Code bundle")
	}
	if vscode.Bucket != classify.BucketApps {
		t.Errorf("Bucket = %v, want apps", vscode.Bucket)
	}
	if vscode.Category != "Application" {
		t.Errorf("Category = %q", vscode.Category)
	}
	if vscode.Owner != "VS Code" {
		t.Errorf("Owner = %q, want the alias label", vscode.Owner)
	}
	if !slices.Contains(vscode.OwnerKeys, "app:com.microsoft.VSCode") {
		t.Errorf("OwnerKeys = %v", vscode.OwnerKeys)
	}
	if vscode.Reclaim != classify.UserData {
		t.Errorf("Reclaim = %v, want user-data", vscode.Reclaim)
	}
	if vscode.Confidence != classify.Strong {
		t.Errorf("Confidence = %v", vscode.Confidence)
	}
	if vscode.Source.Kind != classify.SourceApps {
		t.Errorf("Source.Kind = %v, want apps", vscode.Source.Kind)
	}
	if got := vscode.Source.String(); got != "apps:apps/bundle" {
		t.Errorf("Source = %q", got)
	}

	// An App Store install is labelled as one.
	if bw, found := claims[c.Root+"/Applications/Bitwarden.app"]; !found || bw.Category != "App Store" {
		t.Errorf("Bitwarden category = %q, want App Store", bw.Category)
	}
	// So is a bundle inside a publisher folder.
	autocad := claims[c.Root+"/Applications/Autodesk/AutoCAD 2027/AutoCAD 2027.app"]
	if autocad.Category != "Vendor folder" {
		t.Errorf("AutoCAD category = %q, want Vendor folder", autocad.Category)
	}
}

// TestClaimsBucketCaskroom is R3: the Caskroom belongs to this package, not
// to the homebrew detector, because the receipt inside it is what says which
// application the token installed.
func TestClaimsBucketCaskroom(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)
	claims := claimsByPath(t, a)

	cursor, ok := claims[c.Caskroom+"/cursor"]
	if !ok {
		t.Fatal("no claim for the cursor Caskroom directory")
	}
	if cursor.Bucket != classify.BucketApps || cursor.Category != "Homebrew cask" {
		t.Errorf("bucket %v category %q", cursor.Bucket, cursor.Category)
	}
	if !slices.Contains(cursor.OwnerKeys, "cask:cursor") {
		t.Errorf("OwnerKeys = %v", cursor.OwnerKeys)
	}
	// Its application is gone, so the directory is a stub that could go.
	if cursor.Reclaim != classify.Orphaned {
		t.Errorf("Reclaim = %v, want orphaned", cursor.Reclaim)
	}

	// A cask that installs a command-line binary never had an application
	// and is not a missing one.
	codex, ok := claims[c.Caskroom+"/codex"]
	if !ok {
		t.Fatal("no claim for the codex Caskroom directory")
	}
	if codex.Category != "Homebrew cask (binary)" {
		t.Errorf("codex category = %q", codex.Category)
	}
	if !slices.Contains(codex.OwnerKeys, "cli:codex") {
		t.Errorf("codex OwnerKeys = %v", codex.OwnerKeys)
	}
	if codex.Reclaim == classify.Orphaned {
		t.Error("a binary-only cask must not be orphaned")
	}
}

// TestClaimsReclaimFollowsTheVerdict is what turns the attribution into
// something the ledger can act on: the same directory is regenerable while
// its application is installed and orphaned once it is gone.
func TestClaimsReclaimFollowsTheVerdict(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)
	claims := claimsByPath(t, a)

	tests := []struct {
		path    string
		reclaim classify.Reclaim
		why     string
	}{
		{c.Home + "/Library/Caches/com.microsoft.VSCode", classify.Regenerable,
			"an installed application's cache is regenerable"},
		{c.Home + "/Library/Application Support/Code", classify.UserData,
			"an installed application's support directory is the user's data"},
		{c.Home + "/Library/Caches/com.todesktop.230313mzl4w4u92", classify.Orphaned,
			"a cask-only owner's data is orphaned"},
		{c.Home + "/Library/Application Support/dev.warp.Warp-Stable", classify.Orphaned,
			"an orphan-likely owner's data is orphaned"},
		{c.Home + "/Library/Application Support/mochi", classify.UserData,
			"the user's own build is their data"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			cl, ok := claims[tc.path]
			if !ok {
				t.Fatalf("no claim for %s", tc.path)
			}
			if cl.Bucket != classify.BucketAppData {
				t.Errorf("Bucket = %v, want app-data", cl.Bucket)
			}
			if cl.Reclaim != tc.reclaim {
				t.Errorf("Reclaim = %v, want %v because %s", cl.Reclaim, tc.reclaim, tc.why)
			}
		})
	}
}

// TestClaimsLeaveUnknownOwnersToTheCatalog is the precedence rule stated from
// the claim side. An apps claim outranks a catalog rule, so a claim that knows
// nothing would replace a rule that knows something.
func TestClaimsLeaveUnknownOwnersToTheCatalog(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)
	claims := claimsByPath(t, a)

	if cl, found := claims[c.Home+"/Library/Caches/SomethingNobodyKnows"]; found {
		t.Errorf("an unattributed directory was claimed as %q", cl.Owner)
	}
	// It is still in the report, which is where a reader and the tuning
	// log look for it.
	if _, ok := a.Verdicts["unknown:SomethingNobodyKnows"]; !ok {
		t.Error("the unknown owner vanished from the analysis")
	}
}

// TestClaimsCarryEvidence checks that the why panel has something to print:
// the match's reasoning, the verdict's, and the keep signals that blocked an
// orphan verdict.
func TestClaimsCarryEvidence(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)
	claims := claimsByPath(t, a)

	cl, ok := claims[c.Home+"/Library/Caches/com.todesktop.230313mzl4w4u92"]
	if !ok {
		t.Fatal("no claim for the Cursor cache")
	}
	joined := strings.Join(cl.Evidence, "\n")
	for _, want := range []string{"cask cursor", "~/Library/Caches/com.todesktop.*", "not on the volume"} {
		if !strings.Contains(joined, want) {
			t.Errorf("evidence is missing %q:\n%s", want, joined)
		}
	}
	if cl.Owner != "Cursor (cask-only)" {
		t.Errorf("Owner = %q, want the state in the label", cl.Owner)
	}
}

// TestClaimsUpdaterCategory is R2 from the claim side: an updater directory
// is relabelled and tagged regenerable whoever owns it.
func TestClaimsUpdaterCategory(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)
	claims := claimsByPath(t, a)

	cl, ok := claims[c.Home+"/Library/Caches/lens-desktop-updater"]
	if !ok {
		t.Fatal("no claim for the Lens updater cache")
	}
	if cl.Category != "Updater cache" {
		t.Errorf("Category = %q", cl.Category)
	}
	if cl.Reclaim != classify.Regenerable {
		t.Errorf("Reclaim = %v, want regenerable", cl.Reclaim)
	}
	if !slices.Contains(cl.OwnerKeys, "app:com.electron.kontena-lens") {
		t.Errorf("OwnerKeys = %v", cl.OwnerKeys)
	}
}

// TestClaimsNeverTouchAppleNames restates the boundary at the claim level:
// macOS's own directories belong to the rule catalog.
func TestClaimsNeverTouchAppleNames(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	for _, cl := range a.Claims() {
		if cl.Node == nil {
			continue
		}
		base := cl.Node.Name
		if IsAppleName(NormalizeName(KeyNameOrID, base)) {
			t.Errorf("claimed an Apple-owned directory: %s", cl.Node.Display())
		}
	}
}

// TestClaimsSpecificityPrefersTheDeeperPath makes sure a claim on a vendor
// folder's child outranks one on the folder, so "Google/Chrome" wins over
// "Google" when the engine resolves them.
func TestClaimsSpecificityPrefersTheDeeperPath(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)
	claims := claimsByPath(t, a)

	parent := claims[c.Home+"/Library/Application Support/Google"]
	child := claims[c.Home+"/Library/Application Support/Google/Chrome"]
	if parent.Depth == 0 || child.Depth == 0 {
		t.Fatal("the vendor folder and its child were not both claimed")
	}
	if child.Depth <= parent.Depth {
		t.Errorf("child depth %d, parent depth %d: the deeper claim must win", child.Depth, parent.Depth)
	}
	if child.Literals != child.Depth {
		t.Errorf("an apps claim is a fully literal path: literals %d, depth %d", child.Literals, child.Depth)
	}
}

// TestDetectorClassifyEmitsClaims wires the whole milestone together: the
// detector that the scan calls now returns claims rather than nothing.
func TestDetectorClassifyEmitsClaims(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	d := c.detector(t)
	raw, _ := d.Probe(t.Context(), c.env(t))

	claims, summary := d.Classify(c.walk(t), raw, contextFor(c))
	if len(claims) == 0 {
		t.Fatal("Classify emitted no claims")
	}
	if !summary.Empty() {
		t.Error("the apps detector publishes its own report, not a tool summary")
	}
	var apps, data int
	for _, cl := range claims {
		switch cl.Bucket {
		case classify.BucketApps:
			apps++
		case classify.BucketAppData:
			data++
		default:
			t.Errorf("claimed bucket %v, which this package does not own", cl.Bucket)
		}
		if cl.Source.Kind != classify.SourceApps {
			t.Errorf("claim from %v, want apps", cl.Source.Kind)
		}
	}
	if apps == 0 || data == 0 {
		t.Errorf("claims by bucket: apps %d, app-data %d", apps, data)
	}
}
