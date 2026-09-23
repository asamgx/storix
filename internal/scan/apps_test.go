package scan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/walk"
)

// TestScanBuildsAnAppsReport is the wiring in one assertion: a scan of a tree
// holding an application produces an inventory, and the inventory knows what
// the application costs.
func TestScanBuildsAnAppsReport(t *testing.T) {
	f := testutil.New(t)
	f.File("Applications/Stremio.app/Contents/Info.plist", 0)
	f.File("Applications/Stremio.app/Contents/MacOS/stub", 4096)
	f.File("Users/andrew/Library/Application Support/Stremio/data.bin", 200_000)

	res, err := Run(context.Background(), Config{
		Roots:   []string{f.Root},
		Home:    f.Path("Users/andrew"),
		NoCache: true,
		Probe:   probe.NewReplay(nil),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	rep, ok := Apps(res)
	if !ok {
		t.Fatal("the scan produced no application inventory")
	}
	if rep.Schema != apps.ReportSchema {
		t.Errorf("schema = %d, want %d", rep.Schema, apps.ReportSchema)
	}
	if rep.Counts.Bundles == 0 {
		t.Error("no bundles were counted")
	}

	var found bool
	for _, e := range rep.Apps {
		if e.Label != "Stremio" {
			continue
		}
		found = true
		// The footprint crosses the buckets: the bundle is in
		// Applications and the support directory is in App data, and
		// neither row of the ledger mentions the other.
		if e.Footprint.Bundle == 0 {
			t.Error("the bundle contributed nothing to the footprint")
		}
		if e.Footprint.Data < 200_000 {
			t.Errorf("data = %d, want the support directory's bytes", e.Footprint.Data)
		}
		if e.Footprint.Total != e.Footprint.Bundle+e.Footprint.Data+e.Footprint.Caches+
			e.Footprint.Dev+e.Footprint.Containers {
			t.Errorf("the footprint columns do not add up to its total: %+v", e.Footprint)
		}
	}
	if !found {
		t.Errorf("Stremio is not in the inventory: %+v", rep.Apps)
	}
}

// TestAppsSurvivesTheCache is the reason the report is stored rather than
// recomputed: a cached scan has not probed anything, so rebuilding the
// inventory from it would mean running codesign, pkgutil and lsregister
// again, which is exactly what the cache exists to avoid.
func TestAppsSurvivesTheCache(t *testing.T) {
	f := testutil.New(t)
	f.File("Applications/Stremio.app/Contents/Info.plist", 0)
	f.File("Users/andrew/Library/Application Support/Stremio/data.bin", 200_000)

	store := cache.Store{Dir: t.TempDir()}
	cfg := Config{
		Roots:   []string{f.Root},
		Home:    f.Path("Users/andrew"),
		Probe:   probe.NewReplay(nil),
		Version: "test",
		Persist: Cacher{Store: store, Version: "test"}.Persist,
	}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	before, ok := Apps(res)
	if !ok {
		t.Fatal("the scan produced no inventory to cache")
	}
	if res.PersistErr != nil {
		t.Fatalf("persist: %v", res.PersistErr)
	}

	path, meta, err := store.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if _, held := meta.Sections[SectionApps]; !held {
		t.Fatalf("the cache file holds no %q section", SectionApps)
	}
	loadedMeta, tree, err := store.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	restored, err := resultFromCache(cfg, loadedMeta, tree, path)
	if err != nil {
		t.Fatalf("resultFromCache: %v", err)
	}
	after, ok := Apps(restored)
	if !ok {
		t.Fatal("the inventory did not survive the cache")
	}
	if len(after.Apps) != len(before.Apps) {
		t.Errorf("restored %d applications, stored %d", len(after.Apps), len(before.Apps))
	}
	if len(after.Apps) > 0 && after.Apps[0].Footprint.Total != before.Apps[0].Footprint.Total {
		t.Errorf("footprint changed across the cache: %d then %d",
			before.Apps[0].Footprint.Total, after.Apps[0].Footprint.Total)
	}
}

// TestAppsAbsentFromAPreInventoryCache covers the file written by a storix
// that had no inventory: it loads, and Apps says there is none rather than
// showing an empty table that looks like a machine with no applications.
func TestAppsAbsentFromAPreInventoryCache(t *testing.T) {
	res := &Result{}
	if _, ok := Apps(res); ok {
		t.Error("a result with no inventory reported one")
	}
	res.Apps = &apps.Report{Schema: apps.ReportSchema}
	if _, ok := Apps(res); ok {
		t.Error("an empty inventory reported one")
	}
}

// appsClockFixture is one directory whose application is gone, walked as a
// scan would walk it. It is the smallest tree that produces a verdict carrying
// an age, which is what every assertion here is about.
type appsClockFixture struct {
	tree *walk.Tree
	cx   classify.Context
	outs []detect.Outcome
}

func newAppsClockFixture(t *testing.T) *appsClockFixture {
	t.Helper()
	f := testutil.New(t)
	f.File("Users/andrewsam/Library/Application Support/Vivaldi/state.bin", 64<<10)
	tree, err := walk.Walk(t.Context(), walk.Options{Root: f.Root})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	det := &apps.Detector{Opts: apps.Options{Root: f.Root}}
	return &appsClockFixture{
		tree: tree,
		cx:   classify.Context{Home: f.Root + "/Users/andrewsam"},
		outs: []detect.Outcome{{Detector: det, Facts: &apps.Facts{
			AppDirBundles: []apps.BundleInfo{{
				Path: f.Root + "/Applications/Placeholder.app",
				ID:   "com.example.placeholder", DisplayName: "Placeholder",
			}},
		}}},
	}
}

// reportAt rebuilds the application inventory as if the walk had finished at
// the given instant.
func (f *appsClockFixture) reportAt(t *testing.T, finished time.Time) string {
	t.Helper()
	f.tree.Finished = finished
	claims, _, _ := detect.Classify(f.tree, f.outs, f.cx)
	class := classifyWith(f.tree, Config{}, claims)
	rep := appsReport(f.tree, f.outs, class, f.cx)
	if rep == nil {
		t.Fatal("no application inventory was built")
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

// TestTheAppsReportIsDatedByTheScanNotTheReading is the bug behind the red
// test in reclassify_test.go.
//
// The inventory is rebuilt from the stored facts on every cache load, and its
// verdicts are full of ages: "written 3 hours ago", and a thirty-day window
// deciding whether a directory is an orphan. Measured from the wall clock,
// the same facts produced a different document every hour — so the rebuilt
// report stopped matching the stored one, and a scan reread a month later
// could call data orphaned that the scan itself had found fresh.
//
// The walk's finish time is the instant the facts describe and it is stored in
// the cache file, so the report is a function of the scan.
func TestTheAppsReportIsDatedByTheScanNotTheReading(t *testing.T) {
	t.Parallel()
	f := newAppsClockFixture(t)
	finished := f.tree.Finished

	// The same scan clock rebuilds the same document, however much time
	// passes between the two readings.
	first := f.reportAt(t, finished)
	second := f.reportAt(t, finished)
	if first != second {
		t.Error("two rebuilds of one scan produced different documents")
	}

	// The data was written moments ago, so a scan that finished moments ago
	// finds it fresh and says so rather than calling it an orphan.
	if !strings.Contains(first, "written less than an hour ago") {
		t.Errorf("the fresh report does not date the data from the scan:\n%s", first)
	}
	if strings.Contains(first, `"state":"orphan-likely"`) {
		t.Errorf("data written moments before the scan was called an orphan:\n%s", first)
	}

	// Moving only the scan's own clock moves the ages with it. Nothing about
	// the wall clock changed between these calls, so this is what proves the
	// report is dated by the scan.
	fiveHours := f.reportAt(t, finished.Add(5*time.Hour))
	if !strings.Contains(fiveHours, "written 5 hours ago") {
		t.Errorf("a scan five hours after the write does not say so:\n%s", fiveHours)
	}

	// And past the thirty-day window the same facts are an orphan, which is
	// the other half of what the clock decides.
	later := f.reportAt(t, finished.Add(400*24*time.Hour))
	if !strings.Contains(later, `"state":"orphan-likely"`) {
		t.Errorf("a scan long after the last write does not report an orphan:\n%s", later)
	}
	if strings.Contains(later, "written") {
		t.Errorf("the recency keep signal survived the window:\n%s", later)
	}
}

// TestAScanWithNoClockStillBuildsAReport keeps the fallback honest: a tree
// that carries no finish time — one a test hand-built — must still produce an
// inventory rather than dating everything to the epoch.
func TestAScanWithNoClockStillBuildsAReport(t *testing.T) {
	t.Parallel()
	f := newAppsClockFixture(t)
	report := f.reportAt(t, time.Time{})
	if report == "" || report == "null" {
		t.Fatal("a tree with no finish time produced no report")
	}
	// Dating from the epoch would put the data fifty-odd years in the
	// future and every age would be nonsense.
	if strings.Contains(report, "an unknown time") {
		t.Errorf("the ages were measured from the zero time:\n%s", report)
	}
}
