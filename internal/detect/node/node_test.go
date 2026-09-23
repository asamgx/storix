package node_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/node"
)

// home is the fixture user's home in the form Classify sees.
const home = detecttest.Home

// store is where pnpm keeps its generations on this machine. Three of them
// exist and only v10 is live, which is the case this detector was written
// for: the names do not say which, and only `pnpm store path` does.
const store = home + "/Library/pnpm/store"

// nvm is the version manager's directory. Its alias files are real files in
// the fixture because the detector reads them rather than running anything —
// nvm is a shell function and there is nothing to run.
const nvm = home + "/.nvm"

// tree mirrors this machine's node layout at a size a test can hold.
func tree(t *testing.T) *detecttest.Fixture {
	t.Helper()
	f := detecttest.Build(t, map[string]int64{
		store + "/v3/files/00/a":                   16_000,
		store + "/v10/files/00/a":                  64_000,
		store + "/v11/files/00/a":                  15_000,
		home + "/.pnpm-store/v3/files/00/a":        4_000,
		home + "/Library/Caches/pnpm/metadata/a":   14_000,
		home + "/.npm/_cacache/index-v5/a":         32_000,
		home + "/.npm/_npx/abc/package.json":       4_000,
		home + "/Library/Caches/Yarn/v6/pkg/a":     3_000,
		home + "/.yarn/berry/cache/a.zip":          5_000,
		home + "/.bun/install/cache/pkg/a":         11_000,
		nvm + "/.cache/bin/node-v22/node.tar.gz":   6_000,
		nvm + "/versions/node/v18.20.8/bin/node":   3_400,
		nvm + "/versions/node/v20.19.4/bin/node":   2_400,
		nvm + "/versions/node/v22.20.0/bin/node":   2_000,
		nvm + "/alias/default":                     3,
		home + "/Library/Caches/node/corepack/a":   1_500,
		home + "/.local/share/fnm/node-versions/a": 500,
	})
	// nvm's alias chain, written the way nvm writes it: a file whose whole
	// content is the target. "22" is a version prefix, not a version.
	writeAlias(t, f, "default", "22")
	return f
}

// writeAlias writes one of nvm's alias files into the fixture. The tree has
// already been walked, so this only changes what the probe reads, which is
// the point: an alias file is evidence and not a size.
func writeAlias(t *testing.T, f *detecttest.Fixture, name, target string) {
	t.Helper()
	p := filepath.Join(f.Real, ".nvm", "alias", filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("creating the alias directory: %v", err)
	}
	if err := os.WriteFile(p, []byte(target+"\n"), 0o644); err != nil {
		t.Fatalf("writing the %s alias: %v", name, err)
	}
}

func TestProbeThisMachine(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, node.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got, ok := facts.(*node.Facts)
	if !ok {
		t.Fatalf("Probe returned %T", facts)
	}

	if got.NpmCache != home+"/.npm" {
		t.Errorf("npm cache = %q", got.NpmCache)
	}
	if got.PnpmStore != store+"/v10" {
		t.Errorf("pnpm store = %q, want the v10 generation `pnpm store path` names", got.PnpmStore)
	}
	if got.YarnVersion != "1.22.22" || got.Berry() {
		t.Errorf("yarn = %q, berry = %v; 1.22 is not Berry", got.YarnVersion, got.Berry())
	}
	if got.YarnCache != home+"/Library/Caches/Yarn/v6" {
		t.Errorf("yarn cache = %q, want the answer to `yarn cache dir`", got.YarnCache)
	}

	// The alias chain is the whole nvm story: "default" holds "22", which is
	// a prefix that has to be resolved against what is installed.
	if got.NvmCurrent != "v22.20.0" {
		t.Errorf("nvm current = %q, want v22.20.0 resolved from the default alias", got.NvmCurrent)
	}
	if len(got.NvmAlias) == 0 {
		t.Error("the alias chain was not recorded as evidence")
	}
	if len(got.NodeVersions) != 3 {
		t.Errorf("node versions = %v, want the three installed", got.NodeVersions)
	}
}

// TestBerryAsksTheOtherQuestion: Yarn 2 and later removed `yarn cache dir`,
// so the version decides which question is the right one to ask.
func TestBerryAsksTheOtherQuestion(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, node.New(), f.Env(t, "testdata/berry.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*node.Facts)
	if !got.Berry() {
		t.Fatalf("yarn %q was not recognised as Berry", got.YarnVersion)
	}
	if got.YarnCache != home+"/.yarn/berry/cache" {
		t.Errorf("yarn cache = %q, want the cacheFolder Berry reports", got.YarnCache)
	}
}

// TestPnpmGenerations is the milestone's pnpm gate: three generations on
// disk, the one pnpm named marked current and the other two marked
// superseded. The marker comes from the measurement and from nothing else —
// v11 sorts after v10 and is still not the live one.
func TestPnpmGenerations(t *testing.T) {
	f := tree(t)
	det := node.New()
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
		{store + "/v10", true, classify.ToolManaged},
		{store + "/v3", false, classify.Regenerable},
		{store + "/v11", false, classify.Regenerable},
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
		if !tc.current && !strings.Contains(tool.Note, "superseded") {
			t.Errorf("%s note = %q, want it to say superseded", tc.path, tool.Note)
		}
	}

	// The metadata cache is a separate directory from the store and must not
	// be confused with a generation of it.
	meta, ok := detecttest.Tool(sum, home+"/Library/Caches/pnpm")
	if !ok {
		t.Fatal("the pnpm metadata cache was not listed")
	}
	if meta.Reclaim != classify.Regenerable || !strings.Contains(meta.Note, "separate") {
		t.Errorf("metadata cache = %+v, want a regenerable row that says it is separate", meta)
	}
}

// TestNvmCurrent is the milestone's nvm gate.
func TestNvmCurrent(t *testing.T) {
	f := tree(t)
	det := node.New()
	facts, _ := detecttest.Probe(t, det, f.Env(t, "testdata/this-machine.json"))
	_, sum := det.Classify(f.Tree, facts, f.Context)

	for _, tc := range []struct {
		version string
		current bool
	}{
		{"v22.20.0", true},
		{"v20.19.4", false},
		{"v18.20.8", false},
	} {
		tool, ok := detecttest.Tool(sum, nvm+"/versions/node/"+tc.version)
		if !ok {
			t.Errorf("no tool row for node %s", tc.version)
			continue
		}
		if tool.Current != tc.current {
			t.Errorf("node %s current = %v, want %v", tc.version, tool.Current, tc.current)
		}
		if tool.Version != tc.version {
			t.Errorf("node row version = %q, want %q", tool.Version, tc.version)
		}
	}
}

// TestAliasChain: an alias may point at another alias, which nvm resolves and
// so must this.
func TestAliasChain(t *testing.T) {
	f := tree(t)
	writeAlias(t, f, "default", "lts/jod")
	writeAlias(t, f, "lts/jod", "v22.20.0")

	facts, err := detecttest.Probe(t, node.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got := facts.(*node.Facts).NvmCurrent; got != "v22.20.0" {
		t.Errorf("nvm current = %q, want the version at the end of the chain", got)
	}
}

func TestClassifyOwners(t *testing.T) {
	f := tree(t)
	det := node.New()
	facts, _ := detecttest.Probe(t, det, f.Env(t, "testdata/this-machine.json"))
	claims, _ := det.Classify(f.Tree, facts, f.Context)

	for _, tc := range []struct{ path, owner, key string }{
		{home + "/.npm", "npm", "cli:npm"},
		{home + "/.npm/_npx", "npm", "cli:npx"},
		{home + "/Library/pnpm", "pnpm", "cli:pnpm"},
		{home + "/Library/Caches/pnpm", "pnpm", "cli:pnpm"},
		{home + "/.yarn", "Yarn", "cli:yarn"},
		{home + "/.bun", "Bun", "cli:bun"},
		{nvm, "nvm", "cli:nvm"},
		{nvm + "/versions/node/v22.20.0", "Node.js", "cli:node"},
		{home + "/Library/Caches/node/corepack", "Node.js", "cli:corepack"},
	} {
		c, ok := detecttest.ClaimAt(claims, tc.path)
		if !ok {
			t.Errorf("no claim at %s", tc.path)
			continue
		}
		if c.Owner != tc.owner {
			t.Errorf("%s owner = %q, want %q", tc.path, c.Owner, tc.owner)
		}
		if !detecttest.HasKey(c, tc.key) {
			t.Errorf("%s keys = %v, want %s", tc.path, c.OwnerKeys, tc.key)
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", tc.path, c.Bucket)
		}
		if c.Source.Kind != classify.SourceDetector {
			t.Errorf("%s source = %s, want a detector claim", tc.path, c.Source)
		}
	}
}

func TestMissing(t *testing.T) {
	// A home with no .nvm and a recording naming no tools is a machine
	// without node at all.
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 100})
	_, err := detecttest.Probe(t, node.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Errorf("err = %v, want missing", err)
	}
}

// TestTimeoutStillBuckets: every package manager timed out, and the
// directories are still in Developer with their owners on them.
func TestTimeoutStillBuckets(t *testing.T) {
	f := tree(t)
	det := node.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Errorf("err = %v, want degraded", err)
	}

	claims, sum := det.Classify(f.Tree, facts, f.Context)
	for _, p := range []string{home + "/.npm", home + "/Library/pnpm", nvm} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s with every probe timed out", p)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", p, c.Bucket)
		}
	}

	// Without an answer from pnpm, the generations are still listed — from
	// the walk — and none of them is claimed to be the live one.
	for _, gen := range []string{store + "/v3", store + "/v10", store + "/v11"} {
		tool, ok := detecttest.Tool(sum, gen)
		if !ok {
			t.Errorf("no row for %s", gen)
			continue
		}
		if tool.Current {
			t.Errorf("%s was marked current although pnpm never answered", gen)
		}
	}
	// nvm needs no command, so its default is known even here.
	if tool, ok := detecttest.Tool(sum, nvm+"/versions/node/v22.20.0"); !ok || !tool.Current {
		t.Error("nvm's default was lost, although reading it needs no command")
	}
}

// TestMalformedOutput: the tools answer with something no parser can use.
func TestMalformedOutput(t *testing.T) {
	f := tree(t)
	det := node.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/malformed.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Errorf("err = %v, want degraded", err)
	}
	claims, _ := det.Classify(f.Tree, facts, f.Context)
	if len(claims) == 0 {
		t.Error("nothing was claimed; the documented defaults should still have been")
	}
}
