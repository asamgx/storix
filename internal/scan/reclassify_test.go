package scan

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/units"
)

// scannedFixture runs a real scan of a small fixture with no tool on the
// machine visible to it, which is what makes the result reproducible: every
// detector is Missing or degraded for the same reason on every machine.
func scannedFixture(t *testing.T) (*Result, Config, cache.Store) {
	t.Helper()
	store := homedStore(t)
	f := testutil.New(t)
	f.File("Users/andrew/Library/Caches/pkg/blob", 400_000)
	f.File("Users/andrew/Documents/note.txt", 100)
	f.File("Users/andrew/code/storix/main.go", 2_000)

	cfg := Config{
		Roots:   []string{f.Root},
		Home:    f.Path("Users/andrew"),
		Units:   units.Decimal,
		Version: storeVersion,
		Probe:   probe.NewReplay(nil),
	}
	cached, err := WithCache(cfg)
	if err != nil {
		t.Fatalf("WithCache: %v", err)
	}
	res, err := Run(context.Background(), cached)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PersistErr != nil || res.CachePath == "" {
		t.Fatalf("the scan was not cached: %v", res.PersistErr)
	}
	return res, cfg, store
}

// TestTheCacheCarriesTheDetectorsAndTheClassification is the shape of the two
// new sections: a row per detector in registry order with its own facts, and
// a classification summary that a person opening the file can read.
func TestTheCacheCarriesTheDetectorsAndTheClassification(t *testing.T) {
	res, _, store := scannedFixture(t)
	_, meta := latestMeta(t, store)

	var docs []detectorDoc
	if err := section(meta, SectionDetectors, &docs); err != nil {
		t.Fatalf("decode the detectors section: %v", err)
	}
	if len(docs) != len(res.Detectors) {
		t.Fatalf("the file holds %d detector rows, the scan had %d", len(docs), len(res.Detectors))
	}
	for i, doc := range docs {
		if doc.Name != res.Detectors[i].Name {
			t.Errorf("row %d is %q, want %q: the stored order is the registry order",
				i, doc.Name, res.Detectors[i].Name)
		}
		if doc.State != res.Detectors[i].State.String() {
			t.Errorf("%s: state = %q, want %q", doc.Name, doc.State, res.Detectors[i].State)
		}
	}

	var class classificationDoc
	if err := section(meta, SectionClassification, &class); err != nil {
		t.Fatalf("decode the classification section: %v", err)
	}
	if len(class.Buckets) != len(classify.Buckets()) {
		t.Fatalf("the summary holds %d buckets, want %d", len(class.Buckets), len(classify.Buckets()))
	}
	var total int64
	for _, b := range class.Buckets {
		total += b.Bytes
	}
	if total != res.Class.Total() {
		t.Errorf("the stored bucket totals sum to %d, the classification to %d", total, res.Class.Total())
	}
}

// TestTheRecomputedLedgerEqualsTheStoredOne is the promise that makes
// recomputing safe: the 1a ledger is a pure function of the facts and the
// tree, so rebuilding it from the cache produces the same document, to the
// byte, as the one the scan stored.
func TestTheRecomputedLedgerEqualsTheStoredOne(t *testing.T) {
	_, cfg, store := scannedFixture(t)
	_, meta := latestMeta(t, store)

	stored, ok := meta.Sections[SectionLedger]
	if !ok {
		t.Fatal("the file holds no ledger section")
	}

	loaded, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v", ok, err)
	}
	got, err := json.Marshal(loaded.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(stored) {
		t.Errorf("the recomputed ledger differs from the stored one\n--- recomputed ---\n%s\n--- stored ---\n%s",
			clipJSON(string(got)), clipJSON(string(stored)))
	}
	if len(loaded.Ledger.Buckets) != len(classify.Buckets()) {
		t.Errorf("the recomputed ledger holds %d buckets, want %d",
			len(loaded.Ledger.Buckets), len(classify.Buckets()))
	}
}

// TestALoadedScanReclassifies is the whole point of the two sections: the
// loaded scan carries the same detector statuses, the same summaries and the
// same buckets as the scan that was stored, without a probe having run.
func TestALoadedScanReclassifies(t *testing.T) {
	live, cfg, _ := scannedFixture(t)

	loaded, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v", ok, err)
	}
	if loaded.Class == nil {
		t.Fatal("the loaded scan has no classification")
	}
	if got, want := loaded.Class.Total(), live.Class.Total(); got != want {
		t.Errorf("the loaded classification totals %d bytes, the live one %d", got, want)
	}
	if len(loaded.Detectors) != len(live.Detectors) {
		t.Fatalf("the loaded scan has %d detectors, the live one %d", len(loaded.Detectors), len(live.Detectors))
	}
	for i, st := range loaded.Detectors {
		if st.Name != live.Detectors[i].Name || st.State != live.Detectors[i].State {
			t.Errorf("detector %d = %s/%s, want %s/%s", i,
				st.Name, st.State, live.Detectors[i].Name, live.Detectors[i].State)
		}
		if st.State == detect.NotProbed {
			t.Errorf("%s came back not-probed although the file carries its row", st.Name)
		}
	}
	liveSum, loadedSum := mustJSON(t, live.Summaries), mustJSON(t, loaded.Summaries)
	if liveSum != loadedSum {
		t.Errorf("the summaries differ\n live:   %s\n loaded: %s", clipJSON(liveSum), clipJSON(loadedSum))
	}
}

// TestAPre1bCacheLoadsDegraded is the compatibility promise. A file written
// before the detectors existed has neither new section; it must still load,
// with every detector honestly reported as never probed and the buckets
// coming from the catalog alone.
func TestAPre1bCacheLoadsDegraded(t *testing.T) {
	res, cfg, store := scannedFixture(t)

	// A pre-1b file is this file without the sections 1b added. Writing it
	// from a real scan is the only honest fixture: a hand-built one would
	// be a guess at what the old format looked like.
	path, meta := latestMeta(t, store)
	for _, name := range []string{SectionDetectors, SectionClassification, SectionApps} {
		delete(meta.Sections, name)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(meta, res.Tree); err != nil {
		t.Fatalf("rewrite the cache: %v", err)
	}

	loaded, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v", ok, err)
	}
	if len(loaded.Detectors) != len(detect.Default().Names()) {
		t.Fatalf("the loaded scan statused %d detectors, want all %d",
			len(loaded.Detectors), len(detect.Default().Names()))
	}
	for _, st := range loaded.Detectors {
		if st.State != detect.NotProbed {
			t.Errorf("%s = %s, want not-probed from a file that never stored it", st.Name, st.State)
		}
	}
	if loaded.Class == nil || loaded.Class.Total() != loaded.Ledger.Scanned.Bytes {
		t.Error("a pre-1b file did not classify from the catalog alone")
	}
	if len(loaded.Summaries) != 0 {
		t.Errorf("summaries = %v, want none: nothing was probed", loaded.Summaries)
	}
	if loaded.Apps != nil {
		t.Error("the loaded scan invented an application report")
	}
}

// TestAnUnknownDetectorIsKeptOpaque covers the other direction of
// compatibility: a file written by a build with a detector this one does not
// have. Its row is reported and its facts are left alone, rather than the
// load failing over a name nothing can decode.
func TestAnUnknownDetectorIsKeptOpaque(t *testing.T) {
	res, cfg, store := scannedFixture(t)

	path, meta := latestMeta(t, store)
	var docs []detectorDoc
	if err := section(meta, SectionDetectors, &docs); err != nil {
		t.Fatal(err)
	}
	docs = append(docs, detectorDoc{
		Name: "flux-capacitor", State: "ok", Duration: time.Second, Verified: true,
		Facts: json.RawMessage(`{"jigowatts":1.21}`),
	})
	raw, err := json.Marshal(docs)
	if err != nil {
		t.Fatal(err)
	}
	meta.Sections[SectionDetectors] = raw
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(meta, res.Tree); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v", ok, err)
	}
	var found bool
	for _, st := range loaded.Detectors {
		if st.Name != "flux-capacitor" {
			continue
		}
		found = true
		if st.State != detect.NotProbed {
			t.Errorf("the unknown detector came back as %s, want not-probed", st.State)
		}
	}
	if !found {
		t.Error("the unknown detector was dropped from the report rather than reported")
	}
}

// TestADisabledDetectorStaysDisabledOnLoad: --disable-detector is about this
// run, not about the run that filled the cache, so a detector switched off now
// must not classify from the facts the file happens to hold.
func TestADisabledDetectorStaysDisabledOnLoad(t *testing.T) {
	_, cfg, _ := scannedFixture(t)
	cfg.DisabledDetectors = []string{"homebrew"}

	loaded, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v", ok, err)
	}
	var found bool
	for _, st := range loaded.Detectors {
		if st.Name != "homebrew" {
			continue
		}
		found = true
		if st.State != detect.Disabled {
			t.Errorf("homebrew = %s, want disabled", st.State)
		}
	}
	if !found {
		t.Error("the disabled detector vanished from the report")
	}
	if _, ok := loaded.Summaries["homebrew"]; ok {
		t.Error("a disabled detector still produced a summary from the cached facts")
	}
}

// TestReclassifyOnLoadIsWithinBudget measures the M12 budget on the user's own
// cache: a full volume must reclassify in under 300 ms, because the TUI's
// first frame from a cache is that plus the decode.
//
// It runs only where there is a real cache to read, which is the machine the
// budget is stated for; everywhere else there is nothing to measure and the
// test says so rather than pretending.
func TestReclassifyOnLoadIsWithinBudget(t *testing.T) {
	store, err := cache.DefaultStore()
	if err != nil {
		t.Skipf("no cache store: %v", err)
	}
	if _, _, err := store.Latest(); err != nil {
		t.Skipf("no stored scan to measure: %v", err)
	}
	cfg := Config{FromCache: true, Units: units.Decimal}
	start := time.Now()
	res, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Skipf("the stored scan did not load: %v", err)
	}
	total := time.Since(start)
	t.Logf("%d nodes: load %s, of which reclassify %s and ledger %s",
		len(res.Tree.Nodes), total.Round(time.Millisecond),
		res.Timing.Classify.Round(time.Millisecond), res.Timing.Ledger.Round(time.Millisecond))
	if raceDetector {
		t.Skip("measured, not judged: the race detector costs an order of magnitude")
	}
	if res.Timing.Classify > 300*time.Millisecond {
		t.Errorf("reclassify took %s, budget is 300ms", res.Timing.Classify.Round(time.Millisecond))
	}
}

// TestTheRecomputedAppsReportEqualsTheStoredOne checks on the user's own
// cache that the application inventory is reconstructible: the report the
// loader rebuilds from the stored facts and the fresh classification is the
// same document, to the byte, as the one the scan stored. The stored section
// is kept for the files that carry no apps facts, not because the analysis
// cannot be redone.
//
// It runs only where there is a real cache with an inventory in it, because a
// fixture has no applications to inventory.
func TestTheRecomputedAppsReportEqualsTheStoredOne(t *testing.T) {
	store, err := cache.DefaultStore()
	if err != nil {
		t.Skipf("no cache store: %v", err)
	}
	_, meta, err := store.Latest()
	if err != nil {
		t.Skipf("no stored scan: %v", err)
	}
	var stored *apps.Report
	if err := section(meta, SectionApps, &stored); err != nil {
		t.Fatalf("decode the apps section: %v", err)
	}
	if stored == nil {
		t.Skip("the stored scan carries no application inventory")
	}
	res, ok, err := LoadLatest(Config{FromCache: true, Units: units.Decimal})
	if err != nil || !ok {
		t.Skipf("the stored scan did not load: %v", err)
	}
	got, want := mustJSON(t, res.Apps), mustJSON(t, stored)
	if got != want {
		t.Errorf("the rebuilt inventory differs from the stored one (%d vs %d bytes)", len(got), len(want))
	}
}

// latestMeta reads the newest file in the store and its metadata.
func latestMeta(t *testing.T, store cache.Store) (string, cache.Meta) {
	t.Helper()
	path, meta, err := store.Latest()
	if err != nil {
		t.Fatalf("no stored scan: %v", err)
	}
	return path, meta
}

// mustJSON renders a value for comparison in a failure message.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// clipJSON keeps a failure message readable.
func clipJSON(s string) string {
	const max = 600
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
