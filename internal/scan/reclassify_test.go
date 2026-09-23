package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
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
	f.File("Users/andrew/Library/Preferences/com.example.plist", 300)
	f.File("Users/andrew/Library/Application Support/Slack/data", 250_000)
	f.File("Users/andrew/Documents/note.txt", 100)
	f.File("Users/andrew/code/storix/main.go", 2_000)
	f.File("Users/andrew/code/storix/node_modules/pkg/index.js", 60_000)
	f.File("Applications/Thing.app/Contents/MacOS/thing", 500_000)

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
// same classification as the scan that was stored, without a probe having run.
//
// The classification is compared structurally rather than by its total. The
// total is the sum of every bucket, which is the scanned bytes by
// construction, so it matches whatever the engine decided and even matches
// when the engine decided nothing at all: it is the one number about a
// classification that cannot fail.
func TestALoadedScanReclassifies(t *testing.T) {
	live, cfg, _ := scannedFixture(t)

	loaded, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v", ok, err)
	}
	if loaded.Class == nil {
		t.Fatal("the loaded scan has no classification")
	}
	if len(live.Class.Claims) == 0 {
		t.Fatal("the fixture classified nothing, so comparing the two would assert nothing")
	}
	liveShape, loadedShape := classShape(t, live), classShape(t, loaded)
	for _, part := range []string{"buckets", "owners", "conflicts", "unmatched", "rejected"} {
		if liveShape[part] != loadedShape[part] {
			t.Errorf("the reclassified %s differ\n live:   %s\n loaded: %s",
				part, clipJSON(liveShape[part]), clipJSON(loadedShape[part]))
		}
	}
	compareNodeClaims(t, live, loaded)
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

// TestTheRecomputedAppsReportEqualsTheStoredOne is the promise that lets the
// loader rebuild the inventory instead of trusting the stored one: the report
// rebuilt from the stored facts and the fresh classification is the same
// document, to the byte, as the one the scan wrote. The stored section is
// kept for the files that carry no apps facts, not because the analysis
// cannot be redone.
//
// It reads a cache this test wrote, from a fixture in its own temp directory.
// It used to read the developer's real cache instead, and that made it a test
// of how recently somebody had scanned: a cache written by an older build was
// compared against this build's output and failed with a byte count, on any
// machine whose newest scan predated the binary. A fixture has applications
// enough — a bundle with no identifier is exactly the case the inventory has
// to name from its own directory — and the comparison is between two runs of
// the same code over the same tree, which is what the promise is about.
func TestTheRecomputedAppsReportEqualsTheStoredOne(t *testing.T) {
	live, cfg, store := scannedFixture(t)
	_, meta := latestMeta(t, store)

	var stored *apps.Report
	if err := section(meta, SectionApps, &stored); err != nil {
		t.Fatalf("decode the apps section: %v", err)
	}
	if stored == nil || len(stored.Apps) == 0 {
		t.Fatalf("the fixture stored no application inventory (%+v), so there is nothing to rebuild", stored)
	}
	if live.Apps == nil {
		t.Fatal("the live scan produced no application inventory")
	}

	loaded, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v", ok, err)
	}
	if loaded.Apps == nil {
		t.Fatal("the loaded scan rebuilt no application inventory, and fell back to nothing")
	}
	if got, want := mustJSON(t, loaded.Apps), mustJSON(t, stored); got != want {
		t.Errorf("the rebuilt inventory differs from the stored one\n rebuilt: %s\n stored:  %s",
			clipJSON(got), clipJSON(want))
	}
}

// realCacheEnv opts a test in to reading the cache of the machine it runs on.
// Its value is the version that wrote the cache, not a flag: the gate is
// meta.Storix against it, and no test in this package can work that version
// out for itself. internal/cli holds it, set by an ldflag, and internal/cli
// imports this package; a test binary is not stamped with the VCS revision
// that BuildInfo appends either, so reproducing the string here would produce
// a different one and skip every time.
const realCacheEnv = "STORIX_REAL_CACHE_TEST"

// TestTheRecomputedAppsReportEqualsThisMachinesCache is the same promise
// against a real inventory of real applications, which a fixture cannot be.
//
// It is opt-in because it can only mean anything when the cache was written
// by the build under test, and nothing in this package can ask what that
// build calls itself. So the version is supplied along with the opt-in:
//
//	STORIX_REAL_CACHE_TEST="$(storix version | cut -d' ' -f2-)" \
//	  go test ./internal/scan/ -run ThisMachinesCache
//
// A cache written by anything else is skipped rather than failed. Comparing
// this build's output against an older build's stored report says only that
// the two builds differ, which is what the version already said.
func TestTheRecomputedAppsReportEqualsThisMachinesCache(t *testing.T) {
	version := os.Getenv(realCacheEnv)
	if version == "" {
		t.Skipf("set %s to the version `storix version` prints to check this machine's own cache", realCacheEnv)
	}
	store, err := cache.DefaultStore()
	if err != nil {
		t.Skipf("no cache store: %v", err)
	}
	_, meta, err := store.Latest()
	if err != nil {
		t.Skipf("no stored scan: %v", err)
	}
	if meta.Storix != version {
		t.Skipf("the newest cache was written by storix %q, not %q: rescan before checking it",
			meta.Storix, version)
	}
	var stored *apps.Report
	if err := section(meta, SectionApps, &stored); err != nil {
		t.Fatalf("decode the apps section: %v", err)
	}
	if stored == nil {
		t.Skip("the stored scan carries no application inventory")
	}
	res, ok, err := LoadLatest(Config{FromCache: true, Units: units.Decimal, Version: version})
	if err != nil || !ok {
		t.Fatalf("the stored scan did not load: %v, %v", ok, err)
	}
	if got, want := mustJSON(t, res.Apps), mustJSON(t, stored); got != want {
		t.Errorf("the rebuilt inventory differs from the stored one (%d vs %d bytes)", len(got), len(want))
	}
}

// classShape reduces a classification to the facts a reader depends on, one
// JSON document per part so that a failure names which part moved: what each
// bucket holds and how it splits, who owns what and under which join keys,
// what disagreed, what nothing claimed, and what was refused.
//
// Node claims are left out and compared separately, because a single JSON
// blob of every node would report "these two trees differ" and nothing more.
func classShape(t *testing.T, r *Result) map[string]string {
	t.Helper()
	if r.Class == nil {
		t.Fatal("the scan has no classification")
	}
	c := r.Class

	buckets := make([]map[string]any, 0, len(classify.Buckets()))
	for _, b := range classify.Buckets() {
		bt := c.Buckets[b]
		buckets = append(buckets, map[string]any{
			"bucket": b.ID(), "bytes": bt.Bytes, "files": bt.Files,
			"categories": bt.Categories, "by_reclaim": bt.ByReclaim,
		})
	}
	owners := make(map[string]any, len(c.Owners))
	for name, o := range c.Owners {
		keys := slices.Clone(o.Keys)
		sort.Strings(keys)
		owners[name] = map[string]any{"bytes": o.Bytes, "by_bucket": o.ByBucket, "keys": keys}
	}
	conflicts := make([]string, 0, len(c.Conflicts))
	for _, cf := range c.Conflicts {
		conflicts = append(conflicts, fmt.Sprintf("%s: %s beat %s over %d bytes",
			cf.Path, cf.Winner, cf.Loser, cf.Bytes))
	}
	unmatched := make([]string, 0, len(c.Unmatched))
	for i, id := range c.Unmatched {
		unmatched = append(unmatched, fmt.Sprintf("%s %d", r.Tree.Nodes[id].Display(), c.UnmatchedBytes(i)))
	}
	return map[string]string{
		"buckets":   mustJSON(t, buckets),
		"owners":    mustJSON(t, owners),
		"conflicts": mustJSON(t, conflicts),
		"unmatched": mustJSON(t, unmatched),
		"rejected":  mustJSON(t, c.Rejected),
	}
}

// compareNodeClaims checks that every node came back under the same effective
// claim, joined on the display path rather than on the node id: the cache
// format is free to renumber a preorder index, and a comparison that assumed
// the numbering would be testing the numbering.
func compareNodeClaims(t *testing.T, live, loaded *Result) {
	t.Helper()
	want := nodeClaims(live)
	got := nodeClaims(loaded)
	if len(got) != len(want) {
		t.Errorf("the loaded tree holds %d nodes, the live one %d", len(got), len(want))
	}
	paths := make([]string, 0, len(want))
	for p := range want {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	bad := 0
	for _, p := range paths {
		g, ok := got[p]
		if ok && g == want[p] {
			continue
		}
		if bad++; bad > 5 {
			t.Errorf("…and %d more nodes", len(want)-5)
			return
		}
		t.Errorf("%s: loaded as %q, live as %q", p, g, want[p])
	}
}

// nodeClaims is every node's effective claim, keyed by display path.
func nodeClaims(r *Result) map[string]string {
	out := make(map[string]string, len(r.Tree.Nodes))
	for _, n := range r.Tree.Nodes {
		cl, ok := r.Class.OfNode(n)
		if !ok {
			out[n.Display()] = "other"
			continue
		}
		out[n.Display()] = fmt.Sprintf("%s %s %s/%s %s",
			cl.Bucket.ID(), cl.Source, cl.Category, cl.Owner, cl.Reclaim)
	}
	return out
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
