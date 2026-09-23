package ledger

import (
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// classified builds a classification by hand: the ledger must not care how
// the engine arrived at the totals, only that they partition the scanned
// bytes, so the test states them directly.
func classified(scanned int64, parts map[classify.Bucket]int64) *classify.Classification {
	c := &classify.Classification{Owners: map[string]*classify.OwnerTotal{}}
	var assigned int64
	for b, n := range parts {
		c.Buckets[b].Bytes = n
		c.Buckets[b].Files = 1
		c.Buckets[b].ByReclaim[classify.Regenerable] = n / 2
		c.Buckets[b].ByReclaim[classify.UserData] = n - n/2
		c.Buckets[b].Categories = map[string]int64{b.Label() + " things": n}
		assigned += n
	}
	c.Buckets[classify.BucketOther].Bytes += scanned - assigned
	return c
}

func TestBucketsPartitionScanned(t *testing.T) {
	const scanned = 170_000_000_000
	f := fakeFacts(mac.DataRoot, dataUsed-100_000_000, dataUsed, known(1_500_000_000))
	tr := tree(mac.DataRoot, scanned)
	class := classified(scanned, map[classify.Bucket]int64{
		classify.BucketApps:         29_200_000_000,
		classify.BucketAppData:      42_000_000_000,
		classify.BucketDeveloper:    48_000_000_000,
		classify.BucketContainers:   19_000_000_000,
		classify.BucketPersonal:     12_000_000_000,
		classify.BucketBackups:      3_000_000_000,
		classify.BucketSystemCaches: 8_000_000_000,
		classify.BucketTrash:        400_000_000,
	})

	l := BuildClassified(f, tr, units.Decimal, class)

	if len(l.Buckets) != 12 {
		t.Fatalf("buckets = %d, want 12", len(l.Buckets))
	}
	for i, b := range classify.Buckets() {
		if l.Buckets[i].ID != b.ID() {
			t.Errorf("bucket %d is %q, want %q: the order is docs/02's", i+1, l.Buckets[i].ID, b.ID())
		}
	}
	if got := l.WalkedBucketBytes(); got != l.Scanned.Bytes {
		t.Errorf("walked buckets sum to %d, scanned is %d: the partition is broken by %d bytes",
			got, l.Scanned.Bytes, got-l.Scanned.Bytes)
	}
	if got := l.bucket(classify.BucketPurgeable).Bytes; got != l.Purgeable.Bytes {
		t.Errorf("purgeable bucket = %d, want the purgeable line %d", got, l.Purgeable.Bytes)
	}
	var macos int64
	for _, line := range l.MacOS {
		macos += line.Bytes
	}
	if got := l.bucket(classify.BucketMacOS).Bytes; got != macos {
		t.Errorf("macOS bucket = %d, want the sum of the macOS lines %d", got, macos)
	}
}

func TestBucketsWithoutClassification(t *testing.T) {
	const scanned = 170_000_000_000
	f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(1_500_000_000))
	l := Build(f, tree(mac.DataRoot, scanned), units.Decimal)

	if len(l.Buckets) != 12 {
		t.Fatalf("buckets = %d, want 12 even without a classification", len(l.Buckets))
	}
	other := l.bucket(classify.BucketOther)
	if other.Bytes != scanned {
		t.Errorf("Other = %d, want every walked byte (%d)", other.Bytes, scanned)
	}
	if other.Note != "not classified" {
		t.Errorf("Other note = %q, want it to say the scan was not classified", other.Note)
	}
	if got := l.WalkedBucketBytes(); got != l.Scanned.Bytes {
		t.Errorf("walked buckets sum to %d, scanned is %d", got, l.Scanned.Bytes)
	}
}

func TestUnaccountedBucketSign(t *testing.T) {
	// A positive residual is space the walk could not see and becomes the
	// bucket's bytes; a negative one is APFS clones counted twice, which is
	// not space and must not be shown as any.
	positive := BuildClassified(
		fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(1_000_000_000)),
		tree(mac.DataRoot, dataUsed-10_000_000_000), units.Decimal, nil)
	negative := BuildClassified(
		fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(1_000_000_000)),
		tree(mac.DataRoot, dataUsed+10_000_000_000), units.Decimal, nil)

	pos := positive.bucket(classify.BucketUnaccounted)
	if pos.Bytes != positive.Residual.Bytes || pos.Bytes <= 0 {
		t.Errorf("positive residual: bucket = %d, residual = %d", pos.Bytes, positive.Residual.Bytes)
	}
	neg := negative.bucket(classify.BucketUnaccounted)
	if neg.Bytes != 0 {
		t.Errorf("negative residual: bucket = %d, want 0", neg.Bytes)
	}
	if neg.Note == "" {
		t.Error("a negative residual needs a note saying the scan counted blocks twice")
	}
}

func TestBucketReclaimableAndLines(t *testing.T) {
	class := &classify.Classification{Owners: map[string]*classify.OwnerTotal{
		"Homebrew": {Bytes: 4_000_000_000, Keys: []string{"cli:brew"}},
		"pnpm":     {Bytes: 9_600_000_000, Keys: []string{"cli:pnpm"}},
	}}
	class.Owners["Homebrew"].ByBucket[classify.BucketDeveloper] = 4_000_000_000
	class.Owners["pnpm"].ByBucket[classify.BucketDeveloper] = 9_600_000_000
	dev := &class.Buckets[classify.BucketDeveloper]
	dev.Bytes = 13_600_000_000
	dev.ByReclaim[classify.Regenerable] = 4_000_000_000
	dev.ByReclaim[classify.ToolManaged] = 9_600_000_000
	dev.Categories = map[string]int64{"Package store": 9_600_000_000, "Homebrew": 4_000_000_000}

	l := BuildClassified(fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0)),
		tree(mac.DataRoot, 13_600_000_000), units.Decimal, class)

	b := l.bucket(classify.BucketDeveloper)
	if b.Reclaimable != 13_600_000_000 {
		t.Errorf("reclaimable = %d, want regenerable plus tool-managed", b.Reclaimable)
	}
	if len(b.ByReclaim) != 2 || b.ByReclaim[0].Label != "tool-managed" {
		t.Errorf("reclaim lines = %+v, want the largest first", b.ByReclaim)
	}
	if len(b.Categories) != 2 || b.Categories[0].Label != "Package store" {
		t.Errorf("category lines = %+v", b.Categories)
	}
	if len(b.Owners) != 2 || b.Owners[0].Label != "pnpm" {
		t.Errorf("owner lines = %+v", b.Owners)
	}
}

// TestBucketsFromRealWalk runs the engine over a small hand-built tree and
// checks that the ledger's partition still holds to the byte when the numbers
// come from the classifier rather than from the test.
func TestBucketsFromRealWalk(t *testing.T) {
	root := &walk.Node{Name: mac.DataRoot, Kind: walk.KindDir}
	users := &walk.Node{Name: "Users", Kind: walk.KindDir, Parent: root}
	home := &walk.Node{Name: "u", Kind: walk.KindDir, Parent: users}
	docs := &walk.Node{Name: "Documents", Kind: walk.KindDir, Parent: home, Bytes: 5_000_000, Files: 2}
	apps := &walk.Node{Name: "Applications", Kind: walk.KindDir, Parent: root}
	arc := &walk.Node{Name: "Arc.app", Kind: walk.KindDir, Parent: apps, Bytes: 900_000_000, Files: 10}
	stray := &walk.Node{Name: "strange", Kind: walk.KindDir, Parent: root, Bytes: 7_000_000, Files: 1}

	root.Children = []*walk.Node{apps, users, stray}
	users.Children = []*walk.Node{home}
	home.Children = []*walk.Node{docs}
	apps.Children = []*walk.Node{arc}
	for _, n := range []*walk.Node{home, users, apps, root} {
		for _, c := range n.Children {
			n.Bytes += c.Bytes
			n.Files += c.Files
		}
	}
	tr := &walk.Tree{
		Root:     root,
		Nodes:    []*walk.Node{root, apps, arc, users, home, docs, stray},
		Started:  before,
		Finished: after,
	}

	e, err := classify.New([]classify.Rule{
		{ID: "apps.bundle", Match: "/Applications/{name}.app", Bucket: classify.BucketApps,
			Category: "Application", Owner: "{name}", Reclaim: classify.UserData},
		{ID: "personal.documents", Match: "~/Documents", Bucket: classify.BucketPersonal,
			Category: "Documents", Owner: "Documents", Reclaim: classify.UserData},
	}, classify.Context{Home: "/Users/u", CodeRoots: []string{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	class := e.Run(tr, nil)

	f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0))
	l := BuildClassified(f, tr, units.Decimal, class)
	if got := l.WalkedBucketBytes(); got != l.Scanned.Bytes {
		t.Fatalf("walked buckets sum to %d, scanned is %d", got, l.Scanned.Bytes)
	}
	if got := l.bucket(classify.BucketApps).Bytes; got != arc.Bytes {
		t.Errorf("Applications = %d, want the bundle's %d", got, arc.Bytes)
	}
	if got := l.bucket(classify.BucketOther).Bytes; got != stray.Bytes {
		t.Errorf("Other = %d, want the unclassified directory's %d", got, stray.Bytes)
	}
}
