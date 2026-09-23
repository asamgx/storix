package walk

import (
	"runtime"
	"testing"
)

// maxBytesPerNode is the memory budget the tree has to live within. The plan
// budgets 120 B for a Node (112 before Node.ID was added in phase 1b) plus its
// name, its pointer in the parent's child slice and its slot in the preorder
// index; 200 B is the target and 220 the limit, which leaves room for
// allocator rounding without letting a field creep in unnoticed. A full scan retains on the order of a million nodes, so
// every byte here is a megabyte of resident memory.
const maxBytesPerNode = 220

func TestMemoryPerNode(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a 200k-node tree")
	}
	// A generated tree: 11,111 directories of 17 retained files each.
	gen := &genReader{root: "/mem", maxDepth: 4, subdirs: 10, files: 17, fileBytes: 128 * 1024}
	if n := gen.nodes(); n < 190_000 || n > 210_000 {
		t.Fatalf("generator produces %d nodes, want about 200k", n)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	tree, err := Walk(t.Context(), Options{Root: gen.root, Reader: gen, Parallelism: 8})
	if err != nil {
		t.Fatal(err)
	}
	// The reader is reachable through Opts; drop it so the measurement sees
	// the tree alone.
	tree.Opts.Reader = nil

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	nodes := len(tree.Nodes)
	if nodes != gen.nodes() {
		t.Fatalf("walked %d nodes, want %d", nodes, gen.nodes())
	}
	heap := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	perNode := heap / int64(nodes)
	t.Logf("%d nodes, %.1f MB retained, %d B/node", nodes, float64(heap)/(1<<20), perNode)
	if perNode > maxBytesPerNode {
		t.Errorf("%d B/node, want at most %d", perNode, maxBytesPerNode)
	}
	runtime.KeepAlive(tree)
}

func BenchmarkWalkGenerated(b *testing.B) {
	gen := &genReader{root: "/bench", maxDepth: 3, subdirs: 10, files: 17, fileBytes: 128 * 1024}
	for b.Loop() {
		if _, err := Walk(b.Context(), Options{Root: gen.root, Reader: gen, Parallelism: 8}); err != nil {
			b.Fatal(err)
		}
	}
}
