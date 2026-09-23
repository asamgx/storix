package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/walk"
)

// fixtureTree walks a real on-disk fixture so the round trip sees everything a
// scan produces: hard links, a sparse file, a symlink, aggregated small
// leaves, an unreadable directory and the errors that come with it.
func fixtureTree(t *testing.T) *walk.Tree {
	t.Helper()
	f := testutil.New(t)
	f.File("apps/big.bin", 200*1024)
	f.File("apps/small.txt", 10)
	f.Dir("empty")
	f.File("docs/a/b/c/deep.bin", 70*1024)
	f.Sparse("docs/sparse.img", 1<<20)
	f.Hardlink("apps/big.bin", "links/big-link.bin")
	f.Symlink("../apps/big.bin", "links/sym")
	f.Wide("many", 50)
	f.Unreadable("locked")

	tree, err := walk.Walk(context.Background(), walk.Options{
		Root:        f.Root,
		Parallelism: 4,
	})
	if err != nil {
		t.Fatalf("walk %s: %v", f.Root, err)
	}
	return tree
}

// dump renders everything a cache file is supposed to preserve, in a form two
// trees can be compared by. It deliberately includes the order of Tree.Nodes,
// the parent links, the flags, the errno and the Small aggregates.
func dump(t *walk.Tree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "root=%s nodes=%d incomplete=%v links=%d saved=%d vanished=%d\n",
		t.Root.Name, len(t.Nodes), t.Incomplete, t.LinkGroups, t.LinkBytesSaved, t.Vanished)
	fmt.Fprintf(&b, "opts root=%s parallelism=%d threshold=%d\n",
		t.Opts.Root, t.Opts.Parallelism, t.Opts.SmallFileThreshold)
	fmt.Fprintf(&b, "started=%s finished=%s\n",
		t.Started.UTC().Format(time.RFC3339Nano), t.Finished.UTC().Format(time.RFC3339Nano))
	for i, n := range t.Nodes {
		parent := "-"
		if n.Parent != nil {
			parent = n.Parent.Path()
		}
		fmt.Fprintf(&b, "%d %s kind=%s bytes=%d apparent=%d files=%d dirs=%d flags=%04x errno=%d children=%d parent=%s small=%d/%d/%d/%d\n",
			i, n.Path(), n.Kind, n.Bytes, n.Apparent, n.Files, n.Dirs, n.Flags, n.Errno,
			len(n.Children), parent, n.Small.Files, n.Small.Dataless, n.Small.Bytes, n.Small.Apparent)
	}
	for _, e := range t.Errors {
		fmt.Fprintf(&b, "error %s %s %d %s\n", e.Op, e.Path, int(e.Errno), e.Class)
	}
	for _, m := range t.SkippedMounts {
		fmt.Fprintf(&b, "mount %s %s %s %s\n", m.Path, m.FSType, m.From, m.Reason)
	}
	for _, p := range t.SkipListed {
		fmt.Fprintf(&b, "skiplisted %s\n", p)
	}
	return b.String()
}

func testMeta() Meta {
	return Meta{
		Storix:  "1.2.3-test",
		Written: time.Date(2026, 9, 22, 8, 30, 0, 123456789, time.UTC),
		Roots:   []string{"/System/Volumes/Data"},
		Sudo:    true,
	}
}

func encode(t *testing.T, meta Meta, tree *walk.Tree) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, meta, tree); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

func TestRoundTrip(t *testing.T) {
	tree := fixtureTree(t)
	meta := testMeta()
	meta.Root = tree.Root.Name
	meta.Roots = []string{tree.Root.Name}
	raw := encode(t, meta, tree)

	gotMeta, gotTree, err := Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want, got := dump(tree), dump(gotTree); want != got {
		t.Errorf("tree differs after a round trip\n--- want\n%s\n--- got\n%s", want, got)
	}
	if gotMeta.Storix != meta.Storix || !gotMeta.Written.Equal(meta.Written) {
		t.Errorf("metadata differs: got %+v", gotMeta)
	}
	if gotMeta.Schema != SchemaVersion {
		t.Errorf("schema = %d, want %d", gotMeta.Schema, SchemaVersion)
	}
	if !gotMeta.Sudo {
		t.Error("sudo flag lost")
	}
	if gotMeta.SmallFileThreshold != tree.Opts.SmallFileThreshold {
		t.Errorf("threshold = %d, want %d", gotMeta.SmallFileThreshold, tree.Opts.SmallFileThreshold)
	}
	if len(gotTree.Errors) == 0 {
		t.Error("the unreadable fixture directory produced no error in the cache")
	}
}

// TestRoundTripPointers checks the structural invariants the reader promises:
// one arena, parent links that agree with the child lists, and Tree.Nodes in
// the same preorder a fresh walk produces.
func TestRoundTripPointers(t *testing.T) {
	tree := fixtureTree(t)
	raw := encode(t, testMeta(), tree)
	_, got, err := Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Root.Parent != nil {
		t.Error("root has a parent")
	}
	seen := 0
	for _, n := range got.Nodes {
		seen++
		for _, c := range n.Children {
			if c.Parent != n {
				t.Fatalf("%s: child %s points at %v", n.Path(), c.Name, c.Parent)
			}
		}
		if !n.IsDir() && len(n.Children) != 0 {
			t.Fatalf("%s: leaf with %d children", n.Path(), len(n.Children))
		}
	}
	if seen != len(tree.Nodes) {
		t.Fatalf("visited %d nodes, want %d", seen, len(tree.Nodes))
	}
	if _, ok := got.Lookup(tree.Root.Name + "/apps/big.bin"); !ok {
		t.Error("Lookup failed on the rebuilt tree: children are not name sorted")
	}
}

func TestRoundTripSections(t *testing.T) {
	tree := fixtureTree(t)
	meta := testMeta()
	meta.Sections = map[string]json.RawMessage{
		"facts":  json.RawMessage(`{"root":"/System/Volumes/Data","euid":501}`),
		"ledger": json.RawMessage(`{"reconciles":false}`),
	}
	raw := encode(t, meta, tree)
	got, _, err := Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Sections["facts"]) != `{"root":"/System/Volumes/Data","euid":501}` {
		t.Errorf("facts section = %s", got.Sections["facts"])
	}
	if string(got.Sections["ledger"]) != `{"reconciles":false}` {
		t.Errorf("ledger section = %s", got.Sections["ledger"])
	}
}

func TestIncompleteTreeMarksMeta(t *testing.T) {
	tree := fixtureTree(t)
	tree.Incomplete = true
	raw := encode(t, Meta{Storix: "x"}, tree)
	meta, got, err := Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !meta.Incomplete || !got.Incomplete {
		t.Errorf("incomplete lost: meta=%v tree=%v", meta.Incomplete, got.Incomplete)
	}
}

func TestReadRejectsDamagedFiles(t *testing.T) {
	tree := fixtureTree(t)
	good := encode(t, testMeta(), tree)

	cases := []struct {
		name   string
		mutate func([]byte) []byte
		want   string
	}{
		{"truncated body", func(b []byte) []byte { return b[:len(b)-10] }, "sections end"},
		{"truncated to nothing", func(b []byte) []byte { return b[:20] }, "reading header"},
		{"wrong magic", func(b []byte) []byte { b[0] = 'X'; return b }, "bad magic"},
		{"wrong schema", func(b []byte) []byte { b[4] = SchemaVersion + 1; return b }, "schema"},
		{"corrupt node section", func(b []byte) []byte { b[len(b)-20] ^= 0xff; return b }, "checksum"},
		{"corrupt trailer", func(b []byte) []byte { b[len(b)-1] ^= 0xff; return b }, "checksum"},
		{"corrupt metadata", func(b []byte) []byte { b[headerSize] = '['; return b }, "metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.mutate(bytes.Clone(good))
			_, _, err := Read(bytes.NewReader(raw), int64(len(raw)))
			if err == nil {
				t.Fatal("Read accepted a damaged file")
			}
			if !errors.Is(err, ErrIncompatible) {
				t.Fatalf("error %v does not wrap ErrIncompatible", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestReadMetaSkipsTheTree(t *testing.T) {
	tree := fixtureTree(t)
	meta := testMeta()
	raw := encode(t, meta, tree)
	got, err := ReadMeta(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if !got.Written.Equal(meta.Written) || got.Storix != meta.Storix {
		t.Errorf("ReadMeta returned %+v", got)
	}
	if len(got.Tree.Errors) != len(tree.Errors) {
		t.Errorf("ReadMeta lost the error list: %d of %d", len(got.Tree.Errors), len(tree.Errors))
	}
}

// TestReadMetaIsCheap is the speed sanity check: reading the header and the
// JSON section must not cost anything like reading a whole tree.
func TestReadMetaIsCheap(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 200k node tree")
	}
	tree := synthTree(200_000, 50_000)
	raw := encode(t, testMeta(), tree)
	r := bytes.NewReader(raw)

	start := time.Now()
	if _, _, err := Read(r, int64(len(raw))); err != nil {
		t.Fatalf("Read: %v", err)
	}
	full := time.Since(start)

	start = time.Now()
	if _, err := ReadMeta(r); err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	meta := time.Since(start)

	if meta > full/5 {
		t.Errorf("ReadMeta took %v against a full read of %v; it is not reading only the header", meta, full)
	}
	t.Logf("%d nodes, %d bytes: Read %v, ReadMeta %v", len(tree.Nodes), len(raw), full, meta)
}

func TestWriteRejectsEmptyTree(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, Meta{}, nil); err == nil {
		t.Error("Write accepted a nil tree")
	}
	if err := Write(&buf, Meta{}, &walk.Tree{}); err == nil {
		t.Error("Write accepted a tree with no root")
	}
}

func TestNodeRecordSizeMatchesTheFieldList(t *testing.T) {
	widths := []int{4, 4, 1, 2, 2, 8, 8, 4, 4, 8, 4, 4, 4, 4, 8, 8}
	sum := 0
	for _, w := range widths {
		sum += w
	}
	if sum != nodeRecordSize {
		t.Errorf("nodeRecordSize = %d, field widths sum to %d", nodeRecordSize, sum)
	}
	if headerSize != 96 || trailerSize != 4 {
		t.Errorf("header/trailer sizes changed: %d/%d", headerSize, trailerSize)
	}
}

// TestRoundTripNodeIDs is the invariant every per-node consumer depends on:
// a node's ID is its index in Tree.Nodes, and it is the same index after the
// cache has taken the tree apart into arrays and rebuilt it. A classification
// keyed by ID would silently point at the wrong directories if this drifted.
func TestRoundTripNodeIDs(t *testing.T) {
	tree := fixtureTree(t)
	for i, n := range tree.Nodes {
		if n.ID != int32(i) {
			t.Fatalf("a fresh walk left node %q with id %d at index %d", n.Name, n.ID, i)
		}
	}

	raw := encode(t, testMeta(), tree)
	_, got, err := Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Nodes) != len(tree.Nodes) {
		t.Fatalf("the round trip returned %d nodes, want %d", len(got.Nodes), len(tree.Nodes))
	}
	for i, n := range got.Nodes {
		if n.ID != int32(i) {
			t.Errorf("node %q has id %d at index %d after a round trip", n.Name, n.ID, i)
		}
		if n.Path() != tree.Nodes[i].Path() {
			t.Errorf("id %d is %s after a round trip, was %s", i, n.Path(), tree.Nodes[i].Path())
		}
	}
}
