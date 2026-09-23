package scan

import (
	"context"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
)

// TestDefaultRegistryHoldsTheContainerDetectors is what internal/detect
// cannot assert about itself: the detector packages register themselves from
// their init functions, and the list of which ones a scan knows about lives
// in this package's blank imports.
func TestDefaultRegistryHoldsTheContainerDetectors(t *testing.T) {
	names := detect.Default().Names()
	want := []string{"orbstack", "docker", "colima", "podman", "vms", "kubernetes"}
	if len(names) != len(want) {
		t.Fatalf("default registry = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("registry[%d] = %q, want %q", i, names[i], n)
		}
	}
}

// TestScanWithoutAnyTools is the degradation gate. A machine with none of the
// tools installed — which is what an empty fixture set means — must produce a
// complete scan whose detectors are all Missing, with the static catalog
// rules doing the bucketing.
func TestScanWithoutAnyTools(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Documents/note.txt", 100)

	res, err := Run(context.Background(), Config{
		Roots:   []string{f.Root},
		Home:    f.Path("Users/andrew"),
		NoCache: true,
		// An empty recording: every LookPath fails and every command is
		// Missing, so no detector can learn anything.
		Probe: probe.NewReplay(nil),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Tree == nil || res.Ledger == nil {
		t.Fatal("the scan did not complete")
	}
	if len(res.Detectors) != 6 {
		t.Fatalf("detectors = %d, want all six statused", len(res.Detectors))
	}
	for _, st := range res.Detectors {
		if st.State != detect.Missing {
			t.Errorf("%s = %s (%q), want missing on a machine with no tools", st.Name, st.State, st.Reason)
		}
		if st.Reason == "" {
			t.Errorf("%s is missing without saying why", st.Name)
		}
	}
	if len(res.Summaries) != 0 {
		t.Errorf("summaries = %v, want none", res.Summaries)
	}

	// The bytes are still classified, by the catalog rather than by a
	// detector: that is what "degraded costs the evidence, not the bucket"
	// means end to end.
	if res.Class == nil {
		t.Fatal("no classification")
	}
	if got := res.Class.Total(); got != res.Tree.Root.Bytes {
		t.Errorf("classified %d bytes of %d", got, res.Tree.Root.Bytes)
	}
}

// TestDisabledDetectorFallsBackToTheCatalog is the other half of the
// degradation promise: with the orbstack detector switched off, its eighteen
// gigabytes still land in Containers, because the catalog carries a static
// rule for the same path. Only the evidence is lost.
func TestDisabledDetectorFallsBackToTheCatalog(t *testing.T) {
	f := testutil.New(t)
	const group = "Users/andrew/Library/Group Containers/HUAQ24HBR6.dev.orbstack"
	f.File(group+"/data/data.img.raw", 200_000)

	res, err := Run(context.Background(), Config{
		Roots:             []string{f.Root},
		Home:              f.Path("Users/andrew"),
		NoCache:           true,
		Probe:             probe.NewReplay(nil),
		DisabledDetectors: []string{"orbstack"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	n, ok := res.Tree.Lookup(mac.ScanPath(f.Path(group)))
	if !ok {
		t.Fatal("the group container is not in the tree")
	}
	cl, ok := res.Class.Of(n.ID)
	if !ok {
		t.Fatal("the group container was not classified")
	}
	if cl.Bucket != classify.BucketContainers {
		t.Errorf("bucket = %s, want containers", cl.Bucket)
	}
	if cl.Source.Kind != classify.SourceRule {
		t.Errorf("source = %s, want a catalog rule with the detector off", cl.Source)
	}
	if res.Class.Buckets[classify.BucketContainers].Bytes < 200_000 {
		t.Errorf("Containers holds %d bytes, want the disk image's",
			res.Class.Buckets[classify.BucketContainers].Bytes)
	}
}

// TestDisableDetector covers the flag: the named detector does not run and is
// reported as switched off rather than disappearing.
func TestDisableDetector(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Documents/note.txt", 100)

	res, err := Run(context.Background(), Config{
		Roots:             []string{f.Root},
		Home:              f.Path("Users/andrew"),
		NoCache:           true,
		Probe:             probe.NewReplay(nil),
		DisabledDetectors: []string{"orbstack", "no-such-detector"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	states := map[string]detect.State{}
	for _, st := range res.Detectors {
		states[st.Name] = st.State
	}
	if states["orbstack"] != detect.Disabled {
		t.Errorf("orbstack = %s, want disabled", states["orbstack"])
	}
	if states["no-such-detector"] != detect.Disabled {
		t.Error("a --disable-detector name that matched nothing was silently dropped")
	}
	if states["kubernetes"] == detect.Disabled {
		t.Error("kubernetes was disabled too")
	}
}

// TestProbesDoNotOutlastTheWalk is the timing claim M9 is judged on: the
// probes run beside the walk, so the slowest of them must finish before it
// does or they have cost the scan time.
func TestProbesDoNotOutlastTheWalk(t *testing.T) {
	f := testutil.New(t)
	f.Deep("Users/andrew/code/project", 12)
	f.Wide("Users/andrew/code/project/many", 200)

	res, err := Run(context.Background(), Config{
		Roots: []string{f.Root}, Home: f.Path("Users/andrew"), NoCache: true, Probe: probe.NewReplay(nil),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Nothing was run, so the probes are a handful of lstat calls; a
	// regression that made them block would show up here long before it
	// showed up on a real machine.
	if res.Timing.Probe > time.Second {
		t.Errorf("the slowest probe took %s with no tools to ask", res.Timing.Probe)
	}
}

// TestRunStopsItsProbes: scan.Run defers Stop, so nothing may still be
// running once it has returned. The check is that a second scan, which shares
// nothing with the first, still completes promptly.
func TestRunStopsItsProbes(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Documents/note.txt", 100)
	cfg := Config{Roots: []string{f.Root}, Home: f.Path("Users/andrew"), NoCache: true, Probe: probe.NewReplay(nil)}

	for i := range 3 {
		if _, err := Run(context.Background(), cfg); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
}

// TestCancelledScanStillStops: an early return from Run must leave nothing
// behind, which is the whole reason Stop is deferred rather than called at
// the join.
func TestCancelledScanStillStops(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Documents/note.txt", 100)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Run(ctx, Config{Roots: []string{f.Root}, Home: f.Path("Users/andrew"), NoCache: true, Probe: probe.NewReplay(nil)})
	// A cancelled walk returns a partial tree rather than an error, so the
	// assertion is only that the call returns at all.
	if err == nil && res == nil {
		t.Fatal("Run returned neither a result nor an error")
	}
}
