package scan

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/volume"
	"github.com/asamgx/storix/internal/walk"
)

func TestResolveRootsDefaultsToTheDataVolume(t *testing.T) {
	got, err := resolveRoots(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != mac.DataRoot {
		t.Errorf("roots = %v, want [%s]", got, mac.DataRoot)
	}
}

func TestResolveRootsRejectsWhatCannotBeWalked(t *testing.T) {
	f := testutil.New(t)
	file := f.File("a.txt", 10)

	cases := []struct {
		name string
		root string
		want string
	}{
		{"missing", filepath.Join(f.Root, "nope"), "no such file"},
		{"a file", file, "not a directory"},
		{"empty", "", "empty scan root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := resolveRoots([]string{c.root}); err == nil {
				t.Fatalf("resolveRoots(%q) accepted it", c.root)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestResolveRootsChecksEveryRoot(t *testing.T) {
	f := testutil.New(t)
	// The second root is the broken one: it must be caught before the first
	// costs a twenty-second walk.
	_, err := resolveRoots([]string{f.Root, filepath.Join(f.Root, "nope")})
	if err == nil {
		t.Fatal("a bad second root was accepted")
	}
}

func TestRunProducesAReconciledResult(t *testing.T) {
	f := testutil.New(t)
	f.File("big.bin", 200_000)
	f.Dir("sub")
	f.File("sub/small.txt", 10)

	var persisted *Result
	cfg := Config{
		Roots: []string{f.Root},
		Persist: func(_ context.Context, r *Result) (string, error) {
			persisted = r
			return "/tmp/storix-test.scan", nil
		},
	}
	res, err := Run(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if res.Tree == nil || res.Ledger == nil || res.Facts == nil {
		t.Fatalf("result is incomplete: %+v", res)
	}
	if res.Tree.Root.Bytes < 200_000 {
		t.Errorf("scanned %d bytes, want at least the 200 KB file", res.Tree.Root.Bytes)
	}
	if res.Ledger.Scanned.Bytes != res.Tree.Root.Bytes {
		t.Errorf("ledger scanned %d, tree root %d", res.Ledger.Scanned.Bytes, res.Tree.Root.Bytes)
	}
	l := res.Ledger
	if got := l.Scanned.Bytes + l.Purgeable.Bytes + l.Residual.Bytes; got != l.Volume.UsedAfter {
		t.Errorf("volume identity broken: %d != %d", got, l.Volume.UsedAfter)
	}
	if !l.PartialRoot {
		t.Error("a temporary directory was not reported as a partial root")
	}
	if persisted != res {
		t.Error("the persist hook did not receive the result")
	}
	if res.CachePath != "/tmp/storix-test.scan" {
		t.Errorf("CachePath = %q, want what the hook returned", res.CachePath)
	}
	if res.Timing.Total == 0 || res.Timing.Walk == 0 {
		t.Errorf("timings were not recorded: %+v", res.Timing)
	}
}

func TestRunOnACancelledContextStillBuildsTheLedger(t *testing.T) {
	f := testutil.New(t)
	f.Wide("many", 200)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res, err := Run(ctx, Config{Roots: []string{f.Root}})
	if err != nil {
		t.Fatalf("Run returned an error for a cancelled scan: %v", err)
	}
	if !res.Tree.Incomplete {
		t.Error("the tree of a cancelled walk is not marked incomplete")
	}
	if res.Ledger == nil {
		t.Fatal("no ledger was built for a cancelled scan")
	}
	if !res.Ledger.Counters.Incomplete {
		t.Error("the ledger does not record that the scan was interrupted")
	}
	if !strings.Contains(res.Ledger.Scanned.Note, "interrupted") {
		t.Errorf("scanned note = %q, want it to say the total is a lower bound", res.Ledger.Scanned.Note)
	}
}

func TestRunReportsAPersistFailureWithoutLosingTheScan(t *testing.T) {
	f := testutil.New(t)
	f.File("a.bin", 1024)
	want := errors.New("disk full")

	res, err := Run(t.Context(), Config{
		Roots:   []string{f.Root},
		Persist: func(context.Context, *Result) (string, error) { return "", want },
	})
	if err != nil {
		t.Fatalf("a failed cache write sank the scan: %v", err)
	}
	if !errors.Is(res.PersistErr, want) {
		t.Errorf("PersistErr = %v, want %v", res.PersistErr, want)
	}
	if res.Ledger == nil {
		t.Error("the ledger is missing although the walk succeeded")
	}
}

func TestRunSendsProgressEvents(t *testing.T) {
	f := testutil.New(t)
	f.Wide("many", 500)

	events := make(chan walk.Event, 8)
	seen := make(chan struct{})
	go func() {
		defer close(seen)
		for range events {
		}
	}()

	cfg := Config{Roots: []string{f.Root}, Events: events}
	if _, err := Run(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	close(events)
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the event consumer did not finish")
	}
}

func TestExemptPrefixesOnlyApplyToTheDataVolume(t *testing.T) {
	f := testutil.New(t)
	opts := walkOptions(Config{}, f.Root, factsFor(t, f.Root), nil, classify.Context{})
	if opts.ExemptPrefixes == nil || len(opts.ExemptPrefixes) != 0 {
		t.Errorf("ExemptPrefixes = %v, want an empty non-nil slice under a partial root", opts.ExemptPrefixes)
	}
	if opts.Mounts == nil {
		t.Error("the mount guard is off, so a nested mount would be counted twice")
	}

	whole := walkOptions(Config{}, mac.DataRoot, factsFor(t, mac.DataRoot), nil, classify.Context{})
	if whole.ExemptPrefixes != nil {
		t.Errorf("ExemptPrefixes = %v, want nil (the walker's defaults) on the data volume", whole.ExemptPrefixes)
	}
}

// TestMountGuardNamesTheMountItRefuses guards the skipped-mount lines: the
// mount table's describe method does not match the walker's interface by
// name, so without the adapter the report says a mount was skipped but not
// which volume its bytes went to.
func TestMountGuardNamesTheMountItRefuses(t *testing.T) {
	f := factsFor(t, mac.DataRoot)
	g := mountGuard{f.Mounts}

	if !g.IsMountPoint(mac.DataRoot) {
		t.Fatal("the data volume is not recognised as a mount point")
	}
	fsType, from, ok := g.MountInfo("/")
	if !ok {
		t.Fatal("the root mount could not be described")
	}
	if fsType == "" || from == "" {
		t.Errorf("MountInfo(/) = %q, %q; want a filesystem type and a source", fsType, from)
	}
	if _, _, ok := g.MountInfo("/definitely/not/a/mount"); ok {
		t.Error("a path that is not a mount point was described")
	}
}

func TestResultRootIsADisplayPath(t *testing.T) {
	var r *Result
	if got := r.Root(); got != "" {
		t.Errorf("Root() on a nil result = %q, want empty", got)
	}
}

// factsFor collects the real volume facts for a root, skipping the test when
// the machine will not answer.
func factsFor(t *testing.T, root string) *volume.Facts {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f, err := volume.Collect(ctx, root)
	if err != nil {
		t.Skipf("volume facts unavailable: %v", err)
	}
	return f
}
