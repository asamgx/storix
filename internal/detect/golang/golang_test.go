package golang_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/golang"
)

// home is the fixture user's home in the form Classify sees.
const home = detecttest.Home

// tree mirrors this machine's Go layout: the build cache under
// ~/Library/Caches and the module cache under the workspace, which is why one
// command asking for both is worth more than either default.
func tree(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		home + "/Library/Caches/go-build/aa/obj":          40_000,
		home + "/go/pkg/mod/github.com/x/y@v1.0.0/go.mod": 25_000,
		home + "/go/bin/storix":                           12_000,
		home + "/Library/Caches/gopls/index":              3_000,
	})
}

func TestProbeThisMachine(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, golang.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got, ok := facts.(*golang.Facts)
	if !ok {
		t.Fatalf("Probe returned %T", facts)
	}
	if got.GoCache != home+"/Library/Caches/go-build" {
		t.Errorf("GOCACHE = %q", got.GoCache)
	}
	if got.GoModCache != home+"/go/pkg/mod" {
		t.Errorf("GOMODCACHE = %q", got.GoModCache)
	}
	if got.GoPath != home+"/go" {
		t.Errorf("GOPATH = %q", got.GoPath)
	}
	// GOROOT is a Homebrew symlink on this machine. It is recorded and not
	// claimed, so that two detectors never argue over the same bytes.
	if got.GoRoot != "/opt/homebrew/opt/go/libexec" {
		t.Errorf("GOROOT = %q", got.GoRoot)
	}
}

func TestClassifyThisMachine(t *testing.T) {
	f := tree(t)
	det := golang.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := det.Classify(f.Tree, facts, f.Context)

	for _, tc := range []struct {
		path    string
		reclaim classify.Reclaim
	}{
		{home + "/go", classify.ToolManaged},
		{home + "/Library/Caches/go-build", classify.Regenerable},
		{home + "/go/pkg/mod", classify.Regenerable},
		{home + "/go/bin", classify.ToolManaged},
	} {
		c, ok := detecttest.ClaimAt(claims, tc.path)
		if !ok {
			t.Errorf("no claim at %s", tc.path)
			continue
		}
		if c.Owner != "Go" || !detecttest.HasKey(c, "cli:go") {
			t.Errorf("%s owner = %q %v, want Go / cli:go", tc.path, c.Owner, c.OwnerKeys)
		}
		if c.Reclaim != tc.reclaim {
			t.Errorf("%s reclaim = %s, want %s", tc.path, c.Reclaim, tc.reclaim)
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", tc.path, c.Bucket)
		}
		if c.Source.Kind != classify.SourceDetector || c.Source.Detector != "go" {
			t.Errorf("%s source = %s, want detector:go", tc.path, c.Source)
		}
	}

	// GOROOT lives inside Homebrew's Cellar, which Homebrew owns.
	if _, ok := detecttest.ClaimAt(claims, "/opt/homebrew/opt/go/libexec"); ok {
		t.Error("the go detector claimed GOROOT; Homebrew installed it and owns it")
	}
	// gopls has its own cache and is the cli-tools detector's to name.
	if _, ok := detecttest.Tool(sum, home+"/Library/Caches/gopls"); ok {
		t.Error("the go detector listed gopls' cache; cli-tools names that one")
	}
	if len(sum.Tools) == 0 {
		t.Error("no tool rows")
	}
}

func TestMissing(t *testing.T) {
	f := tree(t)
	_, err := detecttest.Probe(t, golang.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Errorf("err = %v, want missing when `go` is not on the path", err)
	}
}

// TestTimeoutStillBuckets: `go env` never answered and the caches are still
// in Developer, from the documented defaults.
func TestTimeoutStillBuckets(t *testing.T) {
	f := tree(t)
	det := golang.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Errorf("err = %v, want degraded", err)
	}
	claims, _ := det.Classify(f.Tree, facts, f.Context)
	for _, p := range []string{home + "/Library/Caches/go-build", home + "/go/pkg/mod", home + "/go/bin"} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s with go env timed out", p)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", p, c.Bucket)
		}
	}
}

// TestShortOutput: `go env` printed fewer lines than it was asked for. That
// is a degradation, and the lines it did print are still used.
func TestShortOutput(t *testing.T) {
	f := tree(t)
	det := golang.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/malformed.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Errorf("err = %v, want degraded", err)
	}
	if !strings.Contains(err.Error(), "1 of 4") {
		t.Errorf("reason = %q, want it to say how many lines came back", err)
	}
	claims, _ := det.Classify(f.Tree, facts, f.Context)
	if _, ok := detecttest.ClaimAt(claims, home+"/Library/Caches/go-build"); !ok {
		t.Error("the one line go did print was not used")
	}
	if _, ok := detecttest.ClaimAt(claims, home+"/go/pkg/mod"); !ok {
		t.Error("the missing lines did not fall back to the documented defaults")
	}
}
