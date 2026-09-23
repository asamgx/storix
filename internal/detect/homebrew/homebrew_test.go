package homebrew_test

import (
	"errors"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/homebrew"
)

// home is the fixture user's home in the form Classify sees.
const home = detecttest.Home

// cellar is where the fixture puts the formulae. The shape is this machine's
// and the numbers are shrunk: what is being tested is that the superseded keg
// is the one flagged, not that it is 85 MB.
const cellar = "/opt/homebrew/Cellar"

// tree is the machine every test here classifies. python@3.13 has two
// versions installed, which is the case the whole detector exists for, and
// aalib has one.
func tree(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		cellar + "/python@3.13/3.13.5/bin/python3":      40_000,
		cellar + "/python@3.13/3.13.8/bin/python3":      42_000,
		cellar + "/glib/2.86.1/lib/libglib.dylib":       30_000,
		cellar + "/glib/2.86.2/lib/libglib.dylib":       31_000,
		cellar + "/aalib/1.4rc5_2/bin/aafire":           5_000,
		"/opt/homebrew/Library/Taps/homebrew/core/x.rb": 9_000,
		"/opt/homebrew/.git/objects/pack/p.pack":        11_000,
		home + "/Library/Caches/Homebrew/bottle.tar.gz": 70_000,
		home + "/Library/Logs/Homebrew/glib/01.log":     1_000,
	})
}

func TestProbeThisMachine(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, homebrew.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got, ok := facts.(*homebrew.Facts)
	if !ok {
		t.Fatalf("Probe returned %T", facts)
	}

	if got.Prefix != "/opt/homebrew" || got.Cellar != cellar {
		t.Errorf("prefix/cellar = %q/%q", got.Prefix, got.Cellar)
	}
	if got.Caskroom != "/opt/homebrew/Caskroom" {
		t.Errorf("caskroom = %q", got.Caskroom)
	}
	if got.Cache != home+"/Library/Caches/Homebrew" {
		t.Errorf("cache = %q", got.Cache)
	}
	// The count is this machine's and is the gate the milestone names.
	if len(got.Formulae) != 247 {
		t.Errorf("formulae = %d, want the 247 this machine has", len(got.Formulae))
	}
	if n := len(got.MultiVersion()); n != 10 {
		t.Errorf("multi-version formulae = %d, want 10", n)
	}
	if !got.Cleanup.Known || got.Cleanup.Free != 148_700_000 {
		t.Errorf("cleanup = %d (known %v), want the 148.7MB summary line",
			got.Cleanup.Free, got.Cleanup.Known)
	}
	if got.Cleanup.Skipped == 0 {
		t.Error("the `Warning: Skipping …` lines were not counted")
	}
}

// TestCleanupShapes is the parser test the milestone asks for: both printed
// forms of a removal line, the warning noise around them, and the summary
// line that is the figure storix reports.
func TestCleanupShapes(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, homebrew.New(), f.Env(t, "testdata/cleanup-shapes.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	c := facts.(*homebrew.Facts).Cleanup

	if !c.Known || c.Free != 352_600_000 {
		t.Errorf("free = %d (known %v), want 352.6MB from the summary line", c.Free, c.Known)
	}
	if c.Skipped != 2 {
		t.Errorf("skipped = %d, want the two Warning lines", c.Skipped)
	}
	want := []struct {
		path  string
		bytes int64
		files int
	}{
		{"/opt/homebrew/Cellar/glib/2.86.1", 214_100_000, 0},
		{"/opt/homebrew/Library/Homebrew/vendor/portable-ruby/4.0.3", 34_600_000, 1705},
		{home + "/Library/Caches/Homebrew/bootsnap/d2fe20", 10_200_000, 1108},
	}
	if len(c.Removals) != len(want) {
		t.Fatalf("removals = %+v, want %d entries", c.Removals, len(want))
	}
	for i, w := range want {
		got := c.Removals[i]
		if got.Path != w.path || got.Bytes != w.bytes || got.Files != w.files {
			t.Errorf("removal[%d] = %+v, want %s %d bytes %d files", i, got, w.path, w.bytes, w.files)
		}
	}
	// The per-line sum is the cross-check, not the answer: brew's own total
	// is larger because the lines it prints are not everything it removes.
	if sum := c.LineSum(); sum != 258_900_000 {
		t.Errorf("line sum = %d, want the three entries added up", sum)
	}
}

func TestClassifyThisMachine(t *testing.T) {
	f := tree(t)
	det := homebrew.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := det.Classify(f.Tree, facts, f.Context)

	for _, p := range []string{
		"/opt/homebrew", cellar, home + "/Library/Caches/Homebrew",
		home + "/Library/Logs/Homebrew", "/opt/homebrew/Library/Taps", "/opt/homebrew/.git",
	} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s is in bucket %s, want developer", p, c.Bucket)
		}
		if c.Owner != "Homebrew" {
			t.Errorf("%s owner = %q, want Homebrew", p, c.Owner)
		}
		if !detecttest.HasKey(c, "cli:brew") {
			t.Errorf("%s owner keys = %v, want cli:brew", p, c.OwnerKeys)
		}
		if c.Source.Kind != classify.SourceDetector || c.Source.Detector != "homebrew" {
			t.Errorf("%s source = %s, want detector:homebrew", p, c.Source)
		}
	}

	// A formula is owned by its own name, so the owner totals can say which
	// formula the Cellar's bytes belong to.
	keg, ok := detecttest.ClaimAt(claims, cellar+"/python@3.13")
	if !ok {
		t.Fatal("no claim on the python@3.13 keg")
	}
	if keg.Owner != "python@3.13" || !detecttest.HasKey(keg, "cli:python@3.13") {
		t.Errorf("keg owner = %q %v", keg.Owner, keg.OwnerKeys)
	}

	// The Caskroom belongs to the application inventory, not here.
	if _, ok := detecttest.ClaimAt(claims, "/opt/homebrew/Caskroom"); ok {
		t.Error("the homebrew detector claimed the Caskroom; applications own it")
	}

	if sum.Reclaimable != 148_700_000 {
		t.Errorf("summary reclaimable = %d, want brew's own figure", sum.Reclaimable)
	}
	if sum.ReclaimNote == "" {
		t.Error("the reclaimable figure is reported without saying which command produced it")
	}
}

// TestSupersededKegIsFlagged is the milestone's per-formula gate: two versions
// of one formula, the newer marked current and the older marked superseded.
func TestSupersededKegIsFlagged(t *testing.T) {
	f := tree(t)
	det := homebrew.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	_, sum := det.Classify(f.Tree, facts, f.Context)

	for _, tc := range []struct {
		path    string
		current bool
		reclaim classify.Reclaim
	}{
		{cellar + "/python@3.13/3.13.8", true, classify.ToolManaged},
		{cellar + "/python@3.13/3.13.5", false, classify.Regenerable},
		{cellar + "/glib/2.86.2", true, classify.ToolManaged},
		{cellar + "/glib/2.86.1", false, classify.Regenerable},
	} {
		tool, ok := detecttest.Tool(sum, tc.path)
		if !ok {
			t.Errorf("no tool row for %s", tc.path)
			continue
		}
		if tool.Current != tc.current {
			t.Errorf("%s current = %v, want %v", tc.path, tool.Current, tc.current)
		}
		if tool.Reclaim != tc.reclaim {
			t.Errorf("%s reclaim = %s, want %s", tc.path, tool.Reclaim, tc.reclaim)
		}
		if tool.Note == "" {
			t.Errorf("%s has no note saying which it is", tc.path)
		}
	}

	// A formula with one version gets no version rows: there is nothing to
	// choose between.
	if _, ok := detecttest.Tool(sum, cellar+"/aalib/1.4rc5_2"); ok {
		t.Error("a single-version formula was given a version row")
	}
}

func TestMissing(t *testing.T) {
	f := tree(t)
	_, err := detecttest.Probe(t, homebrew.New(), f.Env(t, "testdata/missing.json"))
	if state, reason := stateOf(err); state != detect.Missing {
		t.Errorf("state = %s (%q), want missing with brew off the path", state, reason)
	}
}

// TestTimeoutStillBuckets is the degradation promise: brew answered nothing,
// and the Cellar and the cache are still in Developer with Homebrew's name on
// them. Only the evidence is lost.
func TestTimeoutStillBuckets(t *testing.T) {
	f := tree(t)
	det := homebrew.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/timeout.json"))
	if state, _ := stateOf(err); state != detect.Degraded {
		t.Errorf("state = %s, want degraded when every brew command timed out", state)
	}

	claims, sum := det.Classify(f.Tree, facts, f.Context)
	for _, p := range []string{cellar, home + "/Library/Caches/Homebrew"} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Fatalf("no claim at %s with the probe timed out", p)
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s is in bucket %s, want developer", p, c.Bucket)
		}
	}
	// With no listing, the formulae are read out of the tree instead.
	if _, ok := detecttest.ClaimAt(claims, cellar+"/glib"); !ok {
		t.Error("the formulae were not recovered from the walked Cellar")
	}
	if sum.Reclaimable != 0 {
		t.Errorf("reclaimable = %d, want nothing claimed when cleanup did not run", sum.Reclaimable)
	}
}

// TestMalformedOutput: brew is installed, answers, and says nothing a parser
// can use. That is a degradation and not a panic.
func TestMalformedOutput(t *testing.T) {
	f := tree(t)
	det := homebrew.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/malformed.json"))
	if state, _ := stateOf(err); state != detect.Degraded {
		t.Errorf("state = %s, want degraded", state)
	}
	claims, _ := det.Classify(f.Tree, facts, f.Context)
	if len(claims) == 0 {
		t.Error("nothing was claimed; the static paths should still have been")
	}
}

// stateOf maps a probe error onto the state the runner records. The
// sentinels are what a detector promises its caller, so the tests assert on
// the promise rather than on the wording of a reason.
func stateOf(err error) (detect.State, string) {
	switch {
	case err == nil:
		return detect.Ok, ""
	case errors.Is(err, detect.ErrMissing):
		return detect.Missing, err.Error()
	default:
		return detect.Degraded, err.Error()
	}
}
