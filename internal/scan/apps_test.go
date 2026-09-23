package scan

import (
	"context"
	"testing"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
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
