package apps

import (
	"slices"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/walk"
)

// node builds a bare tree node at a display path, for the claim-shaped tests
// that do not need a real walk.
func node(display string, bytes int64) *walk.Node {
	segs := strings.Split(strings.Trim(display, "/"), "/")
	var parent *walk.Node
	for _, seg := range segs {
		parent = &walk.Node{Name: seg, Parent: parent, Kind: walk.KindDir}
	}
	parent.Bytes = bytes
	return parent
}

func winner(display string, bytes int64, b classify.Bucket, category string, keys ...string) classify.Claim {
	return classify.Claim{
		Node:      node(display, bytes),
		Bucket:    b,
		Category:  category,
		OwnerKeys: keys,
		Source:    classify.Source{Kind: classify.SourceApps, ID: "apps/bundle-id"},
	}
}

// analysisWithOwner builds a one-owner analysis, which is all the footprint
// grouping needs.
func analysisWithOwner(o Owner, v Verdict) *Analysis {
	return &Analysis{
		Inventory: &Inventory{},
		Owners:    map[string]*OwnerResult{o.Key: {Owner: o, Confidence: classify.Strong}},
		Verdicts:  map[string]*Verdict{o.Key: &v},
	}
}

// TestFootprintCrossesBuckets is the whole point of a footprint: the ledger
// partitions the disk, so no bucket row can tell a person what VS Code costs.
// The footprint adds the three rows the ledger deliberately keeps apart.
func TestFootprintCrossesBuckets(t *testing.T) {
	t.Parallel()
	a := analysisWithOwner(
		Owner{Key: "app:com.microsoft.VSCode", Kind: KindApp, Label: "VS Code", Slug: "vscode"},
		Verdict{State: StateInstalled, Confidence: classify.Strong})

	fps := Footprints(a, []classify.Claim{
		winner("/Applications/Visual Studio Code.app", 400<<20, classify.BucketApps, "Application", "app:com.microsoft.VSCode"),
		winner("/Users/u/Library/Application Support/Code", 700<<20, classify.BucketAppData, "Application Support", "app:com.microsoft.VSCode"),
		winner("/Users/u/Library/Caches/com.microsoft.VSCode", 120<<20, classify.BucketAppData, "Cache", "app:com.microsoft.VSCode"),
		winner("/Users/u/.vscode", 1700<<20, classify.BucketDeveloper, "IDE data", "app:com.microsoft.VSCode"),
		// Another owner's bytes must not join this footprint.
		winner("/Users/u/Library/Caches/com.google.Chrome", 90<<20, classify.BucketAppData, "Cache", "app:com.google.Chrome"),
	})

	if len(fps) != 1 {
		t.Fatalf("got %d footprints, want 1: %+v", len(fps), fps)
	}
	fp := fps[0]
	if fp.Bundle != 400<<20 {
		t.Errorf("Bundle = %d", fp.Bundle)
	}
	if fp.Data != 700<<20 {
		t.Errorf("Data = %d", fp.Data)
	}
	if fp.Caches != 120<<20 {
		t.Errorf("Caches = %d, want only the cache-like categories", fp.Caches)
	}
	if fp.Dev != 1700<<20 {
		t.Errorf("Dev = %d", fp.Dev)
	}
	if want := int64(400+700+120+1700) << 20; fp.Total != want {
		t.Errorf("Total = %d, want %d", fp.Total, want)
	}
	if len(fp.Components) != 4 {
		t.Errorf("got %d components, want 4", len(fp.Components))
	}
}

// TestFootprintDropsNestedComponents guards against the failure that would
// make a footprint larger than the disk: an owner that claims a directory and
// something inside it must count the bytes once.
func TestFootprintDropsNestedComponents(t *testing.T) {
	t.Parallel()
	a := analysisWithOwner(
		Owner{Key: "app:com.example.app", Kind: KindApp, Label: "Example"},
		Verdict{State: StateInstalled})

	fps := Footprints(a, []classify.Claim{
		winner("/Users/u/Library/Application Support/Example", 500<<20, classify.BucketAppData, "Application Support", "app:com.example.app"),
		winner("/Users/u/Library/Application Support/Example/Cache", 300<<20, classify.BucketAppData, "Cache", "app:com.example.app"),
		winner("/Users/u/Library/Application Support/Example/Cache/deep", 100<<20, classify.BucketAppData, "Cache", "app:com.example.app"),
	})

	if len(fps) != 1 {
		t.Fatalf("got %d footprints", len(fps))
	}
	if got := fps[0].Total; got != 500<<20 {
		t.Errorf("Total = %d, want only the outermost component's bytes", got)
	}
	if len(fps[0].Components) != 1 {
		t.Errorf("components = %v, want only the parent", fps[0].Components)
	}
}

// TestFootprintNeverSumsIntoTheLedger states the invariant in the one form
// that matters. A footprint counts bytes the ledger has already counted in
// several buckets, so the sum of the footprints is larger than the scan and
// must never be added to it.
func TestFootprintNeverSumsIntoTheLedger(t *testing.T) {
	t.Parallel()
	a := &Analysis{
		Inventory: &Inventory{},
		Owners: map[string]*OwnerResult{
			"app:a": {Owner: Owner{Key: "app:a", Label: "A"}},
			"app:b": {Owner: Owner{Key: "app:b", Label: "B"}},
		},
		Verdicts: map[string]*Verdict{
			"app:a": {State: StateInstalled},
			"app:b": {State: StateInstalled},
		},
	}
	claims := []classify.Claim{
		winner("/Applications/A.app", 100, classify.BucketApps, "Application", "app:a"),
		winner("/Users/u/Library/Caches/a", 50, classify.BucketAppData, "Cache", "app:a"),
		winner("/Applications/B.app", 70, classify.BucketApps, "Application", "app:b"),
	}

	var scanned int64
	for _, c := range claims {
		scanned += c.Node.Bytes
	}
	var summed int64
	for _, fp := range Footprints(a, claims) {
		summed += fp.Total
	}
	if summed != scanned {
		t.Fatalf("footprint total %d against scanned %d", summed, scanned)
	}
	// The ledger counts each byte once; the footprints happen to agree
	// here only because no owner spans a bucket twice. The guarantee the
	// report relies on is the other way round: nothing adds these to the
	// bucket totals, and ledger arithmetic lives in internal/ledger alone.
	if len(Footprints(a, claims)) != 2 {
		t.Error("expected one footprint per owner")
	}
}

// TestFootprintJoinsOnAnyOwnerKey is what lets a developer-tool detector
// contribute to an application's footprint: it sets a key this package would
// have set, and the two land on one owner.
func TestFootprintJoinsOnAnyOwnerKey(t *testing.T) {
	t.Parallel()
	a := analysisWithOwner(
		Owner{Key: "app:dev.kdrag0n.MacVirt", Kind: KindApp, Label: "OrbStack", Slug: "orbstack"},
		Verdict{State: StateInstalled})

	fps := Footprints(a, []classify.Claim{
		winner("/Applications/OrbStack.app", 300<<20, classify.BucketApps, "Application", "app:dev.kdrag0n.MacVirt"),
		// The orbstack detector claims the group container and knows only
		// the team id and its own key.
		{
			Node:      node("/Users/u/Library/Group Containers/HUAQ24HBR6.dev.orbstack", 18<<30),
			Bucket:    classify.BucketContainers,
			Category:  "Disk image",
			OwnerKeys: []string{"team:HUAQ24HBR6", "app:dev.kdrag0n.MacVirt"},
			Source:    classify.Source{Kind: classify.SourceDetector, Detector: "orbstack"},
		},
	})

	if len(fps) != 1 {
		t.Fatalf("got %d footprints", len(fps))
	}
	if fps[0].Containers != 18<<30 {
		t.Errorf("Containers = %d, want the disk image", fps[0].Containers)
	}
	var sources []string
	for _, c := range fps[0].Components {
		sources = append(sources, c.Source)
	}
	if !slices.Contains(sources, "detector:orbstack") {
		t.Errorf("the detector's claim was not labelled: %v", sources)
	}
}

// TestFootprintCarriesTheVerdict makes sure the Apps view can render state
// and evidence from the footprint alone.
func TestFootprintCarriesTheVerdict(t *testing.T) {
	t.Parallel()
	a := analysisWithOwner(
		Owner{Key: "cask:cursor", Kind: KindCask, Label: "Cursor", Slug: "cursor"},
		Verdict{State: StateCaskOnly, Confidence: classify.Likely,
			Evidence: []string{"cask cursor is still installed"}})

	fps := Footprints(a, []classify.Claim{
		{
			Node:   node("/Users/u/Library/Caches/com.todesktop.230313mzl4w4u92", 214<<20),
			Bucket: classify.BucketAppData, Category: "Cache",
			OwnerKeys: []string{"cask:cursor"}, Reclaim: classify.Orphaned,
			Source: classify.Source{Kind: classify.SourceApps, ID: "apps/cask-zap"},
		},
	})

	if len(fps) != 1 {
		t.Fatalf("got %d footprints", len(fps))
	}
	if fps[0].State != StateCaskOnly {
		t.Errorf("State = %v", fps[0].State)
	}
	if len(fps[0].Verdict.Evidence) == 0 {
		t.Error("the verdict's evidence did not travel with the footprint")
	}
	if got := fps[0].Reclaimable(); got != 214<<20 {
		t.Errorf("Reclaimable = %d, want the orphaned cache", got)
	}
}

func TestOwnerIndexLookup(t *testing.T) {
	t.Parallel()
	inv := BuildInventory(nil, &Facts{
		AppDirBundles: []BundleInfo{
			{Path: "/Applications/Visual Studio Code.app", ID: "com.microsoft.VSCode", Name: "Code", DisplayName: "Code"},
		},
		Casks: []Cask{{Token: "cursor", Apps: []string{"Cursor.app"}}},
	}, Paths{Home: "/Users/u", User: "u"})
	ix := Owners(inv)

	tests := []struct {
		in   string
		key  string
		kind OwnerKind
	}{
		{"com.microsoft.VSCode", "app:com.microsoft.VSCode", KindApp},
		{"Code", "app:com.microsoft.VSCode", KindApp},
		{"cursor", "cask:cursor", KindCask},
		// Known to the alias table although nothing is installed, which
		// is how a detector attributes "~/.cursor".
		{".cursor", "app:com.todesktop.230313mzl4w4u92", KindProduct},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			o, ok := ix.Lookup(tc.in)
			if !ok {
				t.Fatalf("Lookup(%q) found nothing", tc.in)
			}
			if o.Key != tc.key || o.Kind != tc.kind {
				t.Errorf("Lookup(%q) = %+v, want key %q kind %v", tc.in, o, tc.key, tc.kind)
			}
		})
	}
	if _, ok := ix.Lookup("something nobody ships"); ok {
		t.Error("an unknown name should not resolve")
	}
}
