package classify_test

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/classify/catalog"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/walk"
)

// realTree walks STORIX_BENCH_ROOT once. A synthetic tree would answer the
// wrong question here: what the engine costs depends on how many of the paths
// reach a capture edge, and only a real home directory has that distribution.
var realTree = sync.OnceValue(func() *walk.Tree {
	root := os.Getenv("STORIX_BENCH_ROOT")
	if root == "" {
		return nil
	}
	t, err := walk.Walk(context.Background(), walk.Options{Root: root})
	if err != nil {
		panic(err)
	}
	return t
})

// BenchmarkEngineRun measures one classification pass over a real tree.
//
// Measured 2026-09-23 on an Apple M4 over the whole data volume, 495 k
// retained nodes: the 288 catalog rules compile in 0.35 ms and Run takes
// 29 ms, allocating 10.9 MB in 71 k allocations and settling at about 390 B
// of heap per node. The plan budgets 300 ms, so the pass costs a tenth of its
// budget and under 0.2 % of the 19 s walk it follows.
func BenchmarkEngineRun(b *testing.B) {
	tree := realTree()
	if tree == nil {
		b.Skip("set STORIX_BENCH_ROOT to a real directory to run this")
	}
	e, err := classify.New(catalog.Rules(), classify.Context{Home: home()})
	if err != nil {
		b.Fatal(err)
	}

	var class *classify.Classification
	b.ResetTimer()
	for b.Loop() {
		class = e.Run(tree, nil)
	}
	b.StopTimer()

	if class.Total() != tree.Root.Bytes {
		b.Fatalf("the buckets do not partition the tree: %d != %d", class.Total(), tree.Root.Bytes)
	}
	b.ReportMetric(float64(len(tree.Nodes)), "nodes")
	b.ReportMetric(float64(len(class.Claims)), "claims")

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	b.ReportMetric(float64(ms.HeapAlloc)/float64(len(tree.Nodes)), "heapB/node")
}

// BenchmarkEngineCompile measures building the trie, which happens once per
// scan and once per cache load.
func BenchmarkEngineCompile(b *testing.B) {
	rules := catalog.Rules()
	ctx := classify.Context{Home: home()}
	for b.Loop() {
		if _, err := classify.New(rules, ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// home is the benchmarking user's home as a display path.
func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/Users/andrew"
	}
	return mac.DisplayPath(h)
}
