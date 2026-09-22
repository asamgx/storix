package walk

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/asamgx/storix/internal/testutil"
)

const kib = 1024

// buildFixture creates the tree every semantic test below walks: big and small
// files, three hard links to one inode, two hard links to an aggregated inode,
// a sparse file, a symlink to a directory, a wide directory and an empty one.
// It contains nothing unreadable, so `du` can size it too.
func buildFixture(t *testing.T) *testutil.Fixture {
	t.Helper()
	f := testutil.New(t)
	f.File("a/big.bin", 100*kib)
	f.File("a/small.txt", 10)
	f.File("a/sub/big2.bin", 70*kib)
	f.File("a/sub/tiny", 5)
	f.File("links/a.bin", 200*kib)
	f.Hardlink("links/a.bin", "links/b.bin")
	f.Hardlink("links/a.bin", "links/zz/c.bin")
	f.File("links/small.bin", 100)
	f.Hardlink("links/small.bin", "links/small2.bin")
	f.Sparse("sparse/s.img", 10<<20)
	f.File("target/payload.bin", 80*kib)
	f.Symlink("target", "slink")
	f.Wide("wide", 100)
	f.Dir("empty")
	f.File("prefs/tiny.plist", 1)
	return f
}

// walkFixture runs a walk with the given option tweaks applied.
func walkFixture(t *testing.T, f *testutil.Fixture, tweak func(*Options)) *Tree {
	t.Helper()
	opts := Options{Root: f.Root}
	if tweak != nil {
		tweak(&opts)
	}
	tree, err := Walk(t.Context(), opts)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return tree
}

// node looks up a fixture-relative path and fails when it is missing.
func node(t *testing.T, tree *Tree, f *testutil.Fixture, rel string) *Node {
	t.Helper()
	n, ok := tree.Lookup(f.Path(rel))
	if !ok {
		t.Fatalf("Lookup(%s): not found", rel)
	}
	return n
}

func TestWalkSizes(t *testing.T) {
	f := buildFixture(t)
	tree := walkFixture(t, f, nil)

	big := node(t, tree, f, "a/big.bin")
	if big.Bytes != 100*kib || big.Apparent != 100*kib {
		t.Errorf("a/big.bin: bytes=%d apparent=%d, want %d/%d", big.Bytes, big.Apparent, 100*kib, 100*kib)
	}
	if big.Kind != KindFile || big.Files != 1 {
		t.Errorf("a/big.bin: kind=%s files=%d", big.Kind, big.Files)
	}
	if _, ok := tree.Lookup(f.Path("a/small.txt")); ok {
		t.Error("a/small.txt should have been aggregated, not retained")
	}
	a := node(t, tree, f, "a")
	if a.Small.Files != 1 || a.Small.Bytes == 0 {
		t.Errorf("a.Small = %+v, want one aggregated file with bytes", a.Small)
	}
	if a.Dirs != 1 {
		t.Errorf("a.Dirs = %d, want 1", a.Dirs)
	}
	// a/big.bin + a/small.txt + a/sub/big2.bin + a/sub/tiny
	if a.Files != 4 {
		t.Errorf("a.Files = %d, want 4", a.Files)
	}

	sparse := node(t, tree, f, "sparse/s.img")
	if sparse.Bytes != 0 {
		t.Errorf("sparse file allocated %d bytes, want 0", sparse.Bytes)
	}
	if sparse.Apparent != 10<<20 {
		t.Errorf("sparse file apparent %d, want %d", sparse.Apparent, 10<<20)
	}
}

func TestWalkMatchesDu(t *testing.T) {
	f := buildFixture(t)
	tree := walkFixture(t, f, nil)
	want := f.DuKB(t)
	got := tree.Root.Bytes / 1024
	if got != want {
		t.Errorf("root allocated = %d KiB, du -sk = %d KiB", got, want)
	}
}

func TestWalkHardLinks(t *testing.T) {
	f := buildFixture(t)
	tree := walkFixture(t, f, nil)

	owner := node(t, tree, f, "links/a.bin")
	if !owner.Has(FlagLinkOwner) || owner.Bytes != 200*kib {
		t.Errorf("links/a.bin: flags=%04x bytes=%d, want owner with %d", owner.Flags, owner.Bytes, 200*kib)
	}
	for _, rel := range []string{"links/b.bin", "links/zz/c.bin"} {
		alias := node(t, tree, f, rel)
		if !alias.Has(FlagLinkAlias) || alias.Bytes != 0 || alias.Apparent != 0 {
			t.Errorf("%s: flags=%04x bytes=%d apparent=%d, want alias with 0", rel, alias.Flags, alias.Bytes, alias.Apparent)
		}
	}
	// The aggregated pair: bytes land once, in the owner's parent.
	links := node(t, tree, f, "links")
	if links.Small.Files != 2 {
		t.Errorf("links.Small.Files = %d, want 2 (small.bin and its alias)", links.Small.Files)
	}
	if links.Small.Bytes == 0 {
		t.Error("links.Small.Bytes = 0, want the owner's blocks")
	}
	if tree.LinkGroups != 2 {
		t.Errorf("LinkGroups = %d, want 2", tree.LinkGroups)
	}
	if tree.LinkBytesSaved != uint64(2*200*kib)+uint64(links.Small.Bytes) {
		t.Errorf("LinkBytesSaved = %d", tree.LinkBytesSaved)
	}
}

func TestWalkSymlinkNotFollowed(t *testing.T) {
	f := buildFixture(t)
	// Threshold 1 keeps the symlink itself as a node: it is a leaf with its
	// own tiny size, never a door into the target.
	tree := walkFixture(t, f, func(o *Options) { o.SmallFileThreshold = 1 })

	sl := node(t, tree, f, "slink")
	if sl.Kind != KindSymlink {
		t.Errorf("slink kind = %s, want symlink", sl.Kind)
	}
	if len(sl.Children) != 0 {
		t.Errorf("slink has %d children, want none", len(sl.Children))
	}
	if _, ok := tree.Lookup(f.Path("slink/payload.bin")); ok {
		t.Error("walker followed the symlink into the target directory")
	}
	// The payload is counted exactly once, through the real directory.
	target := node(t, tree, f, "target")
	if target.Bytes != 80*kib {
		t.Errorf("target bytes = %d, want %d", target.Bytes, 80*kib)
	}
}

func TestWalkAggregationAndExemption(t *testing.T) {
	f := buildFixture(t)

	plain := walkFixture(t, f, nil)
	if _, ok := plain.Lookup(f.Path("prefs/tiny.plist")); ok {
		t.Error("prefs/tiny.plist retained without an exemption")
	}
	wide := node(t, plain, f, "wide")
	if wide.Small.Files != 100 || len(wide.Children) != 0 {
		t.Errorf("wide: small=%+v children=%d, want 100 aggregated and no children", wide.Small, len(wide.Children))
	}

	exempt := walkFixture(t, f, func(o *Options) { o.ExemptPrefixes = []string{"prefs"} })
	tiny := node(t, exempt, f, "prefs/tiny.plist")
	if !tiny.Has(FlagExempt) {
		t.Errorf("prefs/tiny.plist flags = %04x, want FlagExempt", tiny.Flags)
	}
	if tiny.Bytes != 4096 && tiny.Bytes != 0 {
		t.Logf("prefs/tiny.plist allocated %d bytes", tiny.Bytes)
	}
	if exempt.Root.Bytes != plain.Root.Bytes {
		t.Errorf("exemption changed the total: %d vs %d", exempt.Root.Bytes, plain.Root.Bytes)
	}

	// The retain hook keeps a named leaf whatever its size.
	hooked := walkFixture(t, f, func(o *Options) {
		o.RetainLeaf = func(_ string, e *Entry) bool { return e.Name == "tiny" }
	})
	if _, ok := hooked.Lookup(f.Path("a/sub/tiny")); !ok {
		t.Error("RetainLeaf did not retain a/sub/tiny")
	}
}

func TestWalkThresholdDoesNotChangeTotals(t *testing.T) {
	f := buildFixture(t)
	small := walkFixture(t, f, func(o *Options) { o.SmallFileThreshold = 1 })
	big := walkFixture(t, f, func(o *Options) { o.SmallFileThreshold = 1 << 30 })
	if small.Root.Bytes != big.Root.Bytes || small.Root.Files != big.Root.Files {
		t.Errorf("threshold changed totals: %d/%d vs %d/%d",
			small.Root.Bytes, small.Root.Files, big.Root.Bytes, big.Root.Files)
	}
	if len(big.Root.Children) == 0 {
		t.Error("directories must be retained at any threshold")
	}
}

func TestWalkUnreadableDirectory(t *testing.T) {
	f := testutil.New(t)
	f.File("a/b/keep.bin", 70*kib)
	f.Unreadable("a/b/locked")

	tree := walkFixture(t, f, nil)

	locked := node(t, tree, f, "a/b/locked")
	if !locked.Has(FlagUnreadable) {
		t.Errorf("locked flags = %04x, want FlagUnreadable", locked.Flags)
	}
	if syscall.Errno(locked.Errno) != syscall.EACCES {
		t.Errorf("locked errno = %d, want EACCES", locked.Errno)
	}
	for _, rel := range []string{"a", "a/b"} {
		if n := node(t, tree, f, rel); !n.Has(FlagPartial) {
			t.Errorf("%s flags = %04x, want FlagPartial", rel, n.Flags)
		}
	}
	if !tree.Root.Has(FlagPartial) {
		t.Errorf("root flags = %04x, want FlagPartial", tree.Root.Flags)
	}
	if len(tree.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly one", tree.Errors)
	}
	e := tree.Errors[0]
	if e.Path != f.Path("a/b/locked") || e.Op != "readdir" || e.Class != ErrPermission {
		t.Errorf("error = %+v, want readdir EACCES on the locked dir", e)
	}
}

func TestWalkSkipsNestedMounts(t *testing.T) {
	f := buildFixture(t)
	mountPath := f.Path("target")
	mounts := fakeMounts{points: map[string]bool{mountPath: true}, fsType: "nfs", from: "OrbStack:/OrbStack"}

	tree := walkFixture(t, f, func(o *Options) { o.Mounts = mounts })

	target := node(t, tree, f, "target")
	if !target.Has(FlagMountSkipped) {
		t.Errorf("target flags = %04x, want FlagMountSkipped", target.Flags)
	}
	if len(target.Children) != 0 || target.Bytes != 0 {
		t.Errorf("target descended anyway: children=%d bytes=%d", len(target.Children), target.Bytes)
	}
	if len(tree.SkippedMounts) != 1 {
		t.Fatalf("skipped mounts = %+v, want one", tree.SkippedMounts)
	}
	sm := tree.SkippedMounts[0]
	if sm.Path != mountPath || sm.FSType != "nfs" || sm.From != "OrbStack:/OrbStack" {
		t.Errorf("skipped mount = %+v", sm)
	}
	if !tree.Root.Has(FlagPartial) {
		t.Error("root should be partial when a mount was skipped")
	}
}

func TestWalkSkipList(t *testing.T) {
	f := testutil.New(t)
	f.File(".fseventsd/x.bin", 70*kib)
	f.File("keep/y.bin", 70*kib)
	f.File("nope/z.bin", 70*kib)

	tree := walkFixture(t, f, func(o *Options) { o.SkipPaths = []string{f.Path("nope")} })

	for _, rel := range []string{".fseventsd", "nope"} {
		n := node(t, tree, f, rel)
		if !n.Has(FlagSkipListed) || len(n.Children) != 0 {
			t.Errorf("%s: flags=%04x children=%d, want skip-listed and empty", rel, n.Flags, len(n.Children))
		}
	}
	if tree.Root.Bytes != node(t, tree, f, "keep").Bytes {
		t.Errorf("root bytes = %d, want only keep's %d", tree.Root.Bytes, node(t, tree, f, "keep").Bytes)
	}
	if len(tree.SkipListed) != 2 {
		t.Errorf("SkipListed = %v, want two paths", tree.SkipListed)
	}
}

func TestWalkRootIsNeverSkipped(t *testing.T) {
	f := testutil.New(t)
	f.File("x.bin", 70*kib)
	mounts := fakeMounts{points: map[string]bool{f.Root: true}}

	tree := walkFixture(t, f, func(o *Options) { o.Mounts = mounts })
	x := node(t, tree, f, "x.bin")
	if x.Bytes == 0 || tree.Root.Bytes != x.Bytes {
		t.Errorf("root bytes = %d, want x.bin's %d: the scan root is a mount point by design", tree.Root.Bytes, x.Bytes)
	}
	if len(tree.SkippedMounts) != 0 {
		t.Errorf("skipped mounts = %+v, want none", tree.SkippedMounts)
	}
}

func TestLookupAndUnder(t *testing.T) {
	f := buildFixture(t)
	tree := walkFixture(t, f, nil)

	if n, ok := tree.Lookup(f.Root); !ok || n != tree.Root {
		t.Error("Lookup of the root failed")
	}
	if _, ok := tree.Lookup(filepath.Join(f.Root, "nope", "nothing")); ok {
		t.Error("Lookup found a path that does not exist")
	}
	if _, ok := tree.Lookup("/elsewhere"); ok {
		t.Error("Lookup found a path outside the root")
	}
	// Under matches the corresponding run of the preorder index.
	sub := tree.Under(f.Path("a"))
	if len(sub) == 0 {
		t.Fatal("Under returned nothing")
	}
	start := -1
	for i, n := range tree.Nodes {
		if n == sub[0] {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("Under's first node is not in Nodes")
	}
	for i, n := range sub {
		if tree.Nodes[start+i] != n {
			t.Fatalf("Under differs from Nodes at %d", i)
		}
	}
}

func TestWalkConfigErrors(t *testing.T) {
	if _, err := Walk(context.Background(), Options{}); err == nil {
		t.Error("empty root accepted")
	}
	if _, err := Walk(context.Background(), Options{Root: "relative/path"}); err == nil {
		t.Error("relative root accepted")
	}
	f := testutil.New(t)
	f.File("file.bin", 10)
	if _, err := Walk(context.Background(), Options{Root: f.Path("file.bin")}); err == nil {
		t.Error("a file was accepted as a scan root")
	}
	if _, err := Walk(context.Background(), Options{Root: f.Path("missing")}); err == nil {
		t.Error("a missing root was accepted")
	}
}
