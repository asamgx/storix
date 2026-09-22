package walk

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/testutil"
)

// TestParallelismDoesNotChangeTheResult is the central scheduler guarantee:
// one worker and sixty-four workers must produce the same tree, byte for byte
// and flag for flag, including hard-link ownership.
func TestParallelismDoesNotChangeTheResult(t *testing.T) {
	f := buildFixture(t)
	one := dumpTree(walkFixture(t, f, func(o *Options) { o.Parallelism = 1 }))
	many := dumpTree(walkFixture(t, f, func(o *Options) { o.Parallelism = 64 }))
	if one != many {
		t.Errorf("parallelism changed the result:\n--- P=1 ---\n%s\n--- P=64 ---\n%s", one, many)
	}
}

func TestDeterministicAcrossRuns(t *testing.T) {
	f := buildFixture(t)
	first := dumpTree(walkFixture(t, f, func(o *Options) { o.Parallelism = 64 }))
	for i := range 3 {
		again := dumpTree(walkFixture(t, f, func(o *Options) { o.Parallelism = 64 }))
		if first != again {
			t.Fatalf("run %d differs from the first", i+2)
		}
	}
}

func TestDeepTree(t *testing.T) {
	const depth = 200
	f := testutil.New(t)
	f.Deep("deep", depth)

	one := walkFixture(t, f, func(o *Options) { o.Parallelism = 1 })
	many := walkFixture(t, f, func(o *Options) { o.Parallelism = 64 })

	if got := one.Root.Dirs; got != depth+1 {
		t.Errorf("dirs = %d, want %d", got, depth+1)
	}
	if one.Root.Files != depth {
		t.Errorf("files = %d, want %d", one.Root.Files, depth)
	}
	if dumpTree(one) != dumpTree(many) {
		t.Error("deep tree differs between P=1 and P=64")
	}
	deepest := f.Path("deep")
	for i := range depth {
		deepest += "/d" + itoa(i)
	}
	if _, ok := one.Lookup(deepest); !ok {
		t.Error("the deepest directory is missing")
	}
}

func TestWideTree(t *testing.T) {
	if testing.Short() {
		t.Skip("creates 20k files")
	}
	const n = 20000
	f := testutil.New(t)
	f.Wide("wide", n)

	one := walkFixture(t, f, func(o *Options) { o.Parallelism = 1 })
	many := walkFixture(t, f, func(o *Options) { o.Parallelism = 64 })

	wide, ok := one.Lookup(f.Path("wide"))
	if !ok {
		t.Fatal("wide directory missing")
	}
	if wide.Small.Files != n {
		t.Errorf("aggregated %d files, want %d", wide.Small.Files, n)
	}
	if dumpTree(one) != dumpTree(many) {
		t.Error("wide tree differs between P=1 and P=64")
	}
}

// TestCancelStopsQuickly cancels a walk of a tree far too large to finish and
// checks that it returns promptly with a usable, honestly-labelled tree.
func TestCancelStopsQuickly(t *testing.T) {
	gen := &genReader{root: "/gen", maxDepth: 6, subdirs: 10, files: 20, fileBytes: 1 << 20, delay: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	tree, err := Walk(ctx, Options{Root: "/gen", Reader: gen, Parallelism: 16})
	elapsed := time.Since(start)
	cancel()

	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("cancel took %s, want well under 2s", elapsed)
	}
	if !tree.Incomplete {
		t.Error("cancelled walk is not marked Incomplete")
	}
	if len(tree.Nodes) == 0 || tree.Nodes[0] != tree.Root {
		t.Error("cancelled walk did not produce a finalized tree")
	}
	// A partial tree is still internally consistent.
	var sum int64
	for _, c := range tree.Root.Children {
		sum += c.Bytes
	}
	if tree.Root.Bytes != sum+tree.Root.Small.Bytes {
		t.Errorf("partial totals do not add up: %d vs %d", tree.Root.Bytes, sum+tree.Root.Small.Bytes)
	}
}

func TestCancelBeforeStart(t *testing.T) {
	gen := &genReader{root: "/gen", maxDepth: 3, subdirs: 4, files: 4, fileBytes: 1 << 20}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tree, err := Walk(ctx, Options{Root: "/gen", Reader: gen, Parallelism: 8})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if !tree.Incomplete {
		t.Error("walk of a cancelled context is not marked Incomplete")
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	f := buildFixture(t)
	before := runtime.NumGoroutine()

	for range 3 {
		events := make(chan Event, 4)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for e := range events {
				if _, ok := e.(DoneEvent); ok {
					return
				}
			}
		}()
		_, err := Walk(t.Context(), Options{
			Root: f.Root, Parallelism: 32, Events: events, ProgressInterval: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		<-done
	}

	// Goroutines exit asynchronously; give them a moment before judging.
	var after int
	for range 100 {
		after = runtime.NumGoroutine()
		if after <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutines: %d before, %d after", before, after)
}

func TestProgressEvents(t *testing.T) {
	gen := &genReader{root: "/gen", maxDepth: 3, subdirs: 6, files: 30, fileBytes: 1 << 20, delay: time.Millisecond}
	events := make(chan Event, 1)

	type result struct {
		progress int
		done     *DoneEvent
		last     Progress
	}
	got := make(chan result, 1)
	go func() {
		var r result
		for e := range events {
			switch ev := e.(type) {
			case ProgressEvent:
				r.progress++
				r.last = ev.Progress
			case DoneEvent:
				r.done = &ev
				got <- r
				return
			}
		}
	}()

	tree, err := Walk(t.Context(), Options{
		Root: "/gen", Reader: gen, Parallelism: 8, Events: events, ProgressInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.progress == 0 {
		t.Error("no progress events")
	}
	if r.done == nil || r.done.Tree != tree {
		t.Error("DoneEvent did not carry the finished tree")
	}
	if r.last.Dirs == 0 || r.last.Files == 0 || r.last.Bytes == 0 {
		t.Errorf("progress counters look empty: %+v", r.last)
	}
	if r.last.Current == "" {
		t.Error("progress carried no current path")
	}
	if r.last.Elapsed <= 0 {
		t.Error("progress carried no elapsed time")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
