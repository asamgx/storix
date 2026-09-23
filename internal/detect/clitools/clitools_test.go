package clitools_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/clitools"
	"github.com/asamgx/storix/internal/detect/detecttest"
)

// home is the fixture user's home in the form Classify sees.
const home = detecttest.Home

// tree mirrors the long tail on this machine: named caches storix has an
// explanation for, a cache it has never heard of, and data directories that
// are not caches at all.
func tree(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		home + "/.cache/codex-runtimes/node/bin/node":     60_000,
		home + "/.cache/puppeteer/chrome/chrome":          40_000,
		home + "/.cache/hamsa_tts/model.bin":              7_000,
		home + "/.cache/uv/wheels/a.whl":                  90_000,
		home + "/.local/share/nvim/mason/bin/ls":          50_000,
		home + "/.local/share/cursor-agent/bin/agent":     20_000,
		home + "/Library/Caches/ms-playwright/chromium/a": 80_000,
		home + "/Library/Caches/node-gyp/22.20.0/node.h":  9_000,
		home + "/Library/Caches/gopls/index":              5_000,
		home + "/Library/Caches/go-build/aa/obj":          45_000,
		home + "/.deno/deps/https/a":                      6_000,
		home + "/.terraform.d/plugin-cache/registry/a":    30_000,
	})
}

func TestProbeFindsTheDirectories(t *testing.T) {
	f := tree(t)
	if _, err := detecttest.Probe(t, clitools.New(), f.Env(t, "testdata/missing.json")); err != nil {
		t.Fatalf("Probe: %v, want a clean run: this detector asks nothing", err)
	}
}

// TestProbeMissing: a home with none of the XDG or Library cache directories
// is a machine this detector knows nothing about, and it says so rather than
// reporting a successful probe that found nothing.
func TestProbeMissing(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 100})
	_, err := detecttest.Probe(t, clitools.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Errorf("err = %v, want missing", err)
	}
}

// TestCacheDirsAreOwnedByName is the point of the detector: the name of a
// subdirectory of ~/.cache is the name of the tool that wrote it, and that is
// a better owner than "Tool caches" even for a tool storix has never heard
// of.
func TestCacheDirsAreOwnedByName(t *testing.T) {
	f := tree(t)
	det := clitools.New()
	facts, _ := detecttest.Probe(t, det, f.Env(t, "testdata/missing.json"))
	claims, sum := det.Classify(f.Tree, facts, f.Context)

	for _, name := range []string{"codex-runtimes", "puppeteer", "hamsa_tts"} {
		p := home + "/.cache/" + name
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Owner != name || !detecttest.HasKey(c, "cli:"+name) {
			t.Errorf("%s owner = %q %v, want the directory's own name", p, c.Owner, c.OwnerKeys)
		}
		if c.Reclaim != classify.Regenerable {
			t.Errorf("%s reclaim = %s, want regenerable", p, c.Reclaim)
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", p, c.Bucket)
		}
	}

	// A name storix recognises earns an explanation; one it does not says so
	// rather than inventing one.
	known, _ := detecttest.Tool(sum, home+"/.cache/codex-runtimes")
	if known.Note != "" {
		t.Errorf("a recognised cache carries the unknown-name note: %q", known.Note)
	}
	unknown, ok := detecttest.Tool(sum, home+"/.cache/hamsa_tts")
	if !ok || !strings.Contains(unknown.Note, "no entry") {
		t.Errorf("unknown cache row = %+v, want a note admitting storix has no entry", unknown)
	}
}

// TestDeferredCachesBelongToTheirOwnDetector: ~/.cache/uv is measured by the
// python detector with `uv cache dir`, so claiming it here as well would put
// the same path in two groups and count its bytes twice.
func TestDeferredCachesBelongToTheirOwnDetector(t *testing.T) {
	f := tree(t)
	det := clitools.New()
	facts, _ := detecttest.Probe(t, det, f.Env(t, "testdata/missing.json"))
	claims, _ := det.Classify(f.Tree, facts, f.Context)

	if _, ok := detecttest.ClaimAt(claims, home+"/.cache/uv"); ok {
		t.Error("cli-tools claimed ~/.cache/uv; the python detector measures it")
	}
	if _, ok := detecttest.ClaimAt(claims, home+"/Library/Caches/go-build"); ok {
		t.Error("cli-tools claimed the Go build cache; `go env GOCACHE` measures it")
	}
	// The root is still claimed, so nothing under it falls into Other.
	if _, ok := detecttest.ClaimAt(claims, home+"/.cache"); !ok {
		t.Error("~/.cache itself was not claimed")
	}
}

// TestShareDirsAreNotCaches: ~/.local/share holds plugins, models and state a
// tool will not rebuild, so none of it is counted as free space.
func TestShareDirsAreNotCaches(t *testing.T) {
	f := tree(t)
	det := clitools.New()
	facts, _ := detecttest.Probe(t, det, f.Env(t, "testdata/missing.json"))
	claims, _ := det.Classify(f.Tree, facts, f.Context)

	for _, name := range []string{"nvim", "cursor-agent"} {
		p := home + "/.local/share/" + name
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Owner != name {
			t.Errorf("%s owner = %q, want the directory's own name", p, c.Owner)
		}
		if c.Reclaim.Reclaimable() {
			t.Errorf("%s is tagged %s; a tool's data directory is not free space", p, c.Reclaim)
		}
	}
}

// TestNamedLibraryCaches: the tools worth explaining get an owner a person
// recognises rather than the directory's own spelling.
func TestNamedLibraryCaches(t *testing.T) {
	f := tree(t)
	det := clitools.New()
	facts, _ := detecttest.Probe(t, det, f.Env(t, "testdata/missing.json"))
	claims, _ := det.Classify(f.Tree, facts, f.Context)

	for _, tc := range []struct {
		path    string
		owner   string
		key     string
		reclaim classify.Reclaim
	}{
		{home + "/Library/Caches/ms-playwright", "Playwright", "cli:playwright", classify.ToolManaged},
		{home + "/Library/Caches/node-gyp", "node-gyp", "cli:node-gyp", classify.Regenerable},
		{home + "/Library/Caches/gopls", "gopls", "cli:gopls", classify.Regenerable},
		{home + "/.deno", "Deno", "cli:deno", classify.ToolManaged},
		{home + "/.terraform.d", "Terraform", "cli:terraform", classify.Regenerable},
		{home + "/.terraform.d/plugin-cache", "Terraform", "cli:terraform", classify.Regenerable},
	} {
		c, ok := detecttest.ClaimAt(claims, tc.path)
		if !ok {
			t.Errorf("no claim at %s", tc.path)
			continue
		}
		if c.Owner != tc.owner || !detecttest.HasKey(c, tc.key) {
			t.Errorf("%s owner = %q %v, want %q %s", tc.path, c.Owner, c.OwnerKeys, tc.owner, tc.key)
		}
		if c.Reclaim != tc.reclaim {
			t.Errorf("%s reclaim = %s, want %s", tc.path, c.Reclaim, tc.reclaim)
		}
		if len(c.Evidence) == 0 {
			t.Errorf("%s was claimed without an explanation for the why panel", tc.path)
		}
		if c.Source.Kind != classify.SourceDetector || c.Source.Detector != "cli-tools" {
			t.Errorf("%s source = %s, want detector:cli-tools", tc.path, c.Source)
		}
	}
}

// TestRowsAreLargestFirst: this detector produces more rows than any other,
// so the ones worth acting on have to survive a --top cut.
func TestRowsAreLargestFirst(t *testing.T) {
	f := tree(t)
	det := clitools.New()
	facts, _ := detecttest.Probe(t, det, f.Env(t, "testdata/missing.json"))
	_, sum := det.Classify(f.Tree, facts, f.Context)

	if len(sum.Tools) < 5 {
		t.Fatalf("tools = %d, want a row per directory", len(sum.Tools))
	}
	for i := 1; i < len(sum.Tools); i++ {
		if sum.Tools[i-1].Bytes < sum.Tools[i].Bytes {
			t.Fatalf("row %d (%d bytes) comes before row %d (%d bytes)",
				i-1, sum.Tools[i-1].Bytes, i, sum.Tools[i].Bytes)
		}
	}
}
