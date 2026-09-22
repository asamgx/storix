package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/walk"
)

// Benchmark shape: the plan's estimate for a full data-volume scan is 0.9–1.5 M
// retained nodes over roughly 300 k distinct names.
const (
	benchNodes = 1_300_000
	benchNames = 300_000
)

// nameWords seed the synthetic name pool; repetition across directories is the
// point, since that is what the string table deduplicates.
var nameWords = []string{
	"Contents", "Resources", "node_modules", "dist", "build", "index", "main",
	"package", "lib", "src", "test", "cache", "Library", "Application Support",
	"com.apple.Safari", "data", "log", "tmp", "bin", "include", "share",
	"Frameworks", "Headers", "Versions", "objects", "pack", "blobs",
}

// synthTree builds a tree of about total nodes whose names are drawn from a
// pool of unique names. Children of a directory get consecutive names from the
// pool, so they are distinct within the directory, then sorted the way a walk
// leaves them.
func synthTree(total, unique int) *walk.Tree {
	rnd := rand.New(rand.NewPCG(0x5f0f1d, 0x7a11))
	names := make([]string, unique)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%d", nameWords[i%len(nameWords)], i)
	}

	root := &walk.Node{Name: "/System/Volumes/Data", Kind: walk.KindDir}
	count := 1
	queue := []*walk.Node{root}
	for qi := 0; qi < len(queue) && count < total; qi++ {
		d := queue[qi]
		k := min(2+rnd.IntN(24), total-count)
		kids := make([]*walk.Node, k)
		start := rnd.IntN(unique)
		for j := range k {
			n := &walk.Node{
				Name:     names[(start+j)%unique],
				Parent:   d,
				Bytes:    int64(rnd.IntN(1<<26)) &^ 4095,
				Apparent: int64(rnd.IntN(1 << 26)),
				Mtime:    1700000000 + int64(rnd.IntN(1<<24)),
				Files:    1,
			}
			// One child in eight is a directory, which puts the node count
			// near the plan's mix of ~0.5 M directories to ~1 M leaves.
			if rnd.IntN(8) == 0 {
				n.Kind = walk.KindDir
				n.Small = walk.Small{
					Files:    uint32(rnd.IntN(200)),
					Dataless: uint32(rnd.IntN(4)),
					Bytes:    int64(rnd.IntN(1 << 22)),
					Apparent: int64(rnd.IntN(1 << 22)),
				}
				queue = append(queue, n)
			} else {
				n.Kind = walk.KindFile
			}
			if rnd.IntN(64) == 0 {
				n.Flags |= walk.FlagLinkAlias
			}
			kids[j] = n
		}
		sort.Slice(kids, func(a, b int) bool { return kids[a].Name < kids[b].Name })
		d.Children = kids
		count += k
	}

	tree := &walk.Tree{
		Root:     root,
		Opts:     walk.Options{Root: root.Name, Parallelism: 16, SmallFileThreshold: 64 << 10},
		Started:  time.Now().Add(-time.Minute),
		Finished: time.Now(),
	}
	tree.Nodes = preorder(root, count)
	return tree
}

// benchTree is built once: a 1.3 M node tree costs a couple of seconds and a
// few hundred megabytes.
var benchTree = sync.OnceValue(func() *walk.Tree { return synthTree(benchNodes, benchNames) })

func benchMeta() Meta {
	return Meta{Storix: "bench", Written: time.Now(), Root: "/System/Volumes/Data"}
}

// gobNode is the fairest gob baseline: gob cannot encode the pointer tree at
// all (Parent links make it cyclic), so it encodes the same flattened records
// the flat format writes, with names inline rather than deduplicated.
type gobNode struct {
	Name                      string
	Parent                    uint32
	Bytes, Apparent, Mtime    int64
	Files, Dirs               uint32
	Kind                      uint8
	Flags, Errno              uint16
	ChildStart, ChildCount    uint32
	SmallFiles, SmallDataless uint32
	SmallBytes, SmallApparent int64
}

type gobTree struct {
	Meta  Meta
	Nodes []gobNode
}

func flattenForGob(t *walk.Tree) gobTree {
	lay, err := buildLayout(t.Root)
	if err != nil {
		panic(err)
	}
	out := gobTree{Meta: completeMeta(benchMeta(), t), Nodes: make([]gobNode, len(lay.order))}
	for i, n := range lay.order {
		out.Nodes[i] = gobNode{
			Name: n.Name, Parent: lay.parent[i],
			Bytes: n.Bytes, Apparent: n.Apparent, Mtime: n.Mtime,
			Files: n.Files, Dirs: n.Dirs,
			Kind: uint8(n.Kind), Flags: uint16(n.Flags), Errno: n.Errno,
			ChildStart: lay.childStart[i], ChildCount: lay.childCount[i],
			SmallFiles: n.Small.Files, SmallDataless: n.Small.Dataless,
			SmallBytes: n.Small.Bytes, SmallApparent: n.Small.Apparent,
		}
	}
	return out
}

func reportPerNode(b *testing.B, nodes int, size int) {
	b.ReportMetric(float64(size), "file_bytes")
	b.ReportMetric(float64(size)/float64(nodes), "bytes/node")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*nodes), "ns/node")
}

func BenchmarkGobEncode(b *testing.B) {
	tree := benchTree()
	flat := flattenForGob(tree)
	var size int
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(flat); err != nil {
			b.Fatal(err)
		}
		size = buf.Len()
	}
	reportPerNode(b, len(tree.Nodes), size)
}

func BenchmarkGobDecode(b *testing.B) {
	tree := benchTree()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(flattenForGob(tree)); err != nil {
		b.Fatal(err)
	}
	raw := buf.Bytes()
	b.ResetTimer()
	for b.Loop() {
		var out gobTree
		if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&out); err != nil {
			b.Fatal(err)
		}
	}
	reportPerNode(b, len(tree.Nodes), len(raw))
}

func BenchmarkFlatEncode(b *testing.B) {
	tree := benchTree()
	var size int
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		if err := Write(&buf, benchMeta(), tree); err != nil {
			b.Fatal(err)
		}
		size = buf.Len()
	}
	reportPerNode(b, len(tree.Nodes), size)
}

func BenchmarkFlatDecode(b *testing.B) {
	tree := benchTree()
	var buf bytes.Buffer
	if err := Write(&buf, benchMeta(), tree); err != nil {
		b.Fatal(err)
	}
	raw := buf.Bytes()
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := Read(bytes.NewReader(raw), int64(len(raw))); err != nil {
			b.Fatal(err)
		}
	}
	reportPerNode(b, len(tree.Nodes), len(raw))
}

func BenchmarkFlatReadMeta(b *testing.B) {
	tree := benchTree()
	var buf bytes.Buffer
	if err := Write(&buf, benchMeta(), tree); err != nil {
		b.Fatal(err)
	}
	raw := buf.Bytes()
	r := bytes.NewReader(raw)
	b.ResetTimer()
	for b.Loop() {
		if _, err := ReadMeta(r); err != nil {
			b.Fatal(err)
		}
	}
	reportPerNode(b, len(tree.Nodes), len(raw))
}

// realTree walks STORIX_BENCH_ROOT once, for the numbers that matter: a
// synthetic tree has a made-up name distribution.
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

func benchRealTree(b *testing.B) *walk.Tree {
	b.Helper()
	t := realTree()
	if t == nil {
		b.Skip("set STORIX_BENCH_ROOT to a real directory to run this")
	}
	return t
}

func BenchmarkRealEncode(b *testing.B) {
	tree := benchRealTree(b)
	var size int
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		if err := Write(&buf, benchMeta(), tree); err != nil {
			b.Fatal(err)
		}
		size = buf.Len()
	}
	reportPerNode(b, len(tree.Nodes), size)
}

func BenchmarkRealDecode(b *testing.B) {
	tree := benchRealTree(b)
	var buf bytes.Buffer
	if err := Write(&buf, benchMeta(), tree); err != nil {
		b.Fatal(err)
	}
	raw := buf.Bytes()
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := Read(bytes.NewReader(raw), int64(len(raw))); err != nil {
			b.Fatal(err)
		}
	}
	reportPerNode(b, len(tree.Nodes), len(raw))
}

func BenchmarkRealGobDecode(b *testing.B) {
	tree := benchRealTree(b)
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(flattenForGob(tree)); err != nil {
		b.Fatal(err)
	}
	raw := buf.Bytes()
	b.ResetTimer()
	for b.Loop() {
		var out gobTree
		if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&out); err != nil {
			b.Fatal(err)
		}
	}
	reportPerNode(b, len(tree.Nodes), len(raw))
}
