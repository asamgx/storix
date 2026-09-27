package projects_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/projects"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/walk"
)

const home = detecttest.Home

// lastCommit is the timestamp the stub git answers with: 2026-03-01.
const lastCommit int64 = 1772323200

// fixture is a built tree plus the two spellings of the home the two halves
// of a detector see: the real one a probe stats, and the display one
// classification works in.
type fixture struct {
	tree     *walk.Tree
	real     string
	realRoot string
	cx       classify.Context
}

// build makes a tree the way detecttest.Build does, but through the
// detector's own leaf-retention hook.
//
// It cannot use detecttest.Build, and that is the point of this detector: the
// manifests that decide what a project is are a few hundred bytes each, so a
// walk without the hook folds every one of them into its parent and the tree
// this detector reads has no evidence in it at all. Building through the hook
// is what makes the retention part of the test rather than an assumption.
func build(t *testing.T, files map[string]int64) *fixture {
	t.Helper()
	f := testutil.New(t)
	for rel, size := range files {
		if size == 0 {
			f.Dir(rel)
			continue
		}
		f.File(rel, int(size))
	}

	realHome := f.Path("Users/andrew")
	realRoot := f.Path("Users/andrew/code")
	hook := projects.New().RetainLeaf(classify.Context{Home: realHome, CodeRoots: []string{realRoot}})
	if hook == nil {
		t.Fatal("the detector retained no leaves, so no manifest can be in the tree")
	}

	tree, err := walk.Walk(context.Background(), walk.Options{
		Root:           f.Root,
		ExemptPrefixes: []string{},
		SkipNames:      []string{},
		SkipPaths:      []string{},
		RetainLeaf:     hook,
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	tree.Root.Name = mac.DataRoot

	return &fixture{
		tree: tree, real: realHome, realRoot: realRoot,
		cx: classify.Context{Home: home, CodeRoots: []string{home + "/code"}},
	}
}

// env is an environment whose git answers instantly with a fixed commit time
// and tracks nothing.
func (f *fixture) env(withGit bool) (detect.Env, *probe.Recorder) {
	return f.envTracking(withGit, nil)
}

// envTracking is env with git reporting the given files as tracked, keyed by
// the project directory's base name.
func (f *fixture) envTracking(withGit bool, tracked map[string][]string) (detect.Env, *probe.Recorder) {
	rec := probe.NewRecorder(gitStub{tracked: tracked})
	env := detect.Env{
		Runner: rec, Home: f.real, Euid: 501,
		ReadFile: detect.ReadFile, ReadDir: detect.ReadDir, Stat: detect.Stat,
		LookPath: func(name string) (string, error) {
			if withGit && name == "git" {
				return "/usr/bin/git", nil
			}
			return "", fs.ErrNotExist
		},
	}
	return env, rec
}

// gitStub answers every `git log` with the same timestamp and every `git
// ls-files` with the files tracked lists for that project.
type gitStub struct{ tracked map[string][]string }

func (g gitStub) Run(_ context.Context, c probe.Cmd) probe.Result {
	if c.Name != "git" || len(c.Args) < 3 {
		return probe.Result{Missing: true, ErrText: "not git"}
	}
	if c.Args[2] == "ls-files" {
		var out strings.Builder
		for _, f := range g.tracked[filepath.Base(c.Args[1])] {
			out.WriteString(f)
			out.WriteByte(0)
		}
		return probe.Result{Stdout: out.String()}
	}
	return probe.Result{Stdout: strconv.FormatInt(lastCommit, 10) + "\n"}
}

// corpus is the tree every test but the retention one works against. It
// covers the five shapes a project takes and the two shapes it does not.
func corpus() map[string]int64 {
	const u = "Users/andrew/"
	return map[string]int64{
		// A repository with its history here and a build directory.
		u + "code/alpha/.git/objects/pack/pack-1.pack": 300_000,
		u + "code/alpha/src/main.go":                   10_000,
		u + "code/alpha/node_modules/lib/index.js":     500_000,

		// A project with a manifest and no repository at all, which is
		// how four of the six largest projects on the machine this was
		// written against look.
		u + "code/beta/package.json":   200,
		u + "code/beta/dist/bundle.js": 400_000,
		u + "code/beta/src/app.ts":     5_000,

		// A worktree: .git is a file pointing elsewhere, and the
		// vendor directory is Go's because it has a modules.txt.
		u + "code/gamma/.git":                      60,
		u + "code/gamma/go.mod":                    120,
		u + "code/gamma/vendor/modules.txt":        300,
		u + "code/gamma/vendor/example.com/x/x.go": 200_000,

		// A repository holding a submodule. The submodule is not a
		// project of its own; its build output belongs to delta.
		u + "code/delta/.git/config":                   400,
		u + "code/delta/sub/.git":                      60,
		u + "code/delta/sub/package.json":              150,
		u + "code/delta/sub/node_modules/dep/index.js": 250_000,

		// A PHP project whose vendor directory has no modules.txt, so
		// nothing says it can be re-fetched.
		u + "code/php/composer.json":        100,
		u + "code/php/vendor/lib/Thing.php": 150_000,

		// Not a project: no repository and no manifest.
		u + "code/notes/reading.md": 700_000,
	}
}

// TestRetainLeafKeepsTheEvidence is the hook's own test: the manifests and
// the .git files under a code root end up in the tree, and nothing else does.
func TestRetainLeafKeepsTheEvidence(t *testing.T) {
	f := build(t, corpus())

	for _, want := range []string{
		home + "/code/beta/package.json",
		home + "/code/gamma/.git",
		home + "/code/gamma/go.mod",
		home + "/code/gamma/vendor/modules.txt",
		home + "/code/delta/sub/.git",
		home + "/code/php/composer.json",
	} {
		if _, ok := detect.Lookup(f.tree, want); !ok {
			t.Errorf("%s was folded away; the detector cannot see it", want)
		}
	}
	// Small files that are not evidence stay folded into their parent.
	// (reading.md is not in this list: at 700 KB it is above the walker's
	// own threshold and gets a node whatever the hook says.)
	for _, unwanted := range []string{
		home + "/code/alpha/src/main.go",
		home + "/code/beta/src/app.ts",
	} {
		if _, ok := detect.Lookup(f.tree, unwanted); ok {
			t.Errorf("%s was retained; the hook is keeping more than the evidence", unwanted)
		}
	}
}

// TestRetainLeafIsScopedToTheCodeRoots: the hook is called for every small
// file on the volume, so a manifest outside a code root must not be kept.
func TestRetainLeafIsScopedToTheCodeRoots(t *testing.T) {
	cx := classify.Context{Home: home, CodeRoots: []string{home + "/code"}}
	hook := projects.New().RetainLeaf(cx)
	if hook == nil {
		t.Fatal("no hook")
	}

	cases := []struct {
		dir  string
		name string
		want bool
	}{
		{mac.ScanPath(home + "/code/alpha"), "package.json", true},
		{mac.ScanPath(home + "/code/a/b/c"), "go.mod", true},
		{mac.ScanPath(home + "/code/a"), ".git", true},
		{mac.ScanPath(home + "/code/a/vendor"), "modules.txt", true},
		{mac.ScanPath(home + "/code/alpha"), "README.md", false},
		{mac.ScanPath(home + "/Library/Caches/x"), "package.json", false},
		{mac.ScanPath("/opt/homebrew/lib/node_modules/npm"), "package.json", false},
		{mac.ScanPath(home + "/coder"), "go.mod", false},
	}
	for _, c := range cases {
		e := &walk.Entry{Name: c.name, Kind: walk.KindFile, Size: 120}
		if got := hook(c.dir, e); got != c.want {
			t.Errorf("hook(%s, %s) = %v, want %v", c.dir, c.name, got, c.want)
		}
	}
}

// TestRetainLeafDoesNotAllocate: the hook runs on every worker for every
// small file, so an allocation in it would be a per-file allocation across a
// whole volume.
func TestRetainLeafDoesNotAllocate(t *testing.T) {
	hook := projects.New().RetainLeaf(classify.Context{Home: home, CodeRoots: []string{home + "/code"}})
	dir := mac.ScanPath(home + "/code/alpha")
	hit := &walk.Entry{Name: "package.json", Kind: walk.KindFile}
	miss := &walk.Entry{Name: "README.md", Kind: walk.KindFile}

	if n := testing.AllocsPerRun(200, func() { hook(dir, hit); hook(dir, miss) }); n != 0 {
		t.Errorf("the hook allocated %v times per call pair", n)
	}
}

// TestProbeFindsTheProjects: discovery descends the code root, stops at the
// first project on each branch, and dates each one with a single git call.
func TestProbeFindsTheProjects(t *testing.T) {
	f := build(t, corpus())
	env, rec := f.env(true)

	facts, err := detecttest.Probe(t, projects.New(), env)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*projects.Facts)

	want := map[string]bool{
		"code/alpha": true, "code/beta": true, "code/gamma": true,
		"code/delta": true, "code/php": true,
	}
	if len(got.Projects) != len(want) {
		t.Fatalf("projects = %+v, want %v", got.Projects, want)
	}
	for _, p := range got.Projects {
		if !want[p.Rel] {
			t.Errorf("unexpected project %q", p.Rel)
		}
		if p.LastCommit != lastCommit {
			t.Errorf("%s last commit = %d, want %d", p.Rel, p.LastCommit, lastCommit)
		}
	}
	// Two calls per project: `git log` for the date and `git ls-files`
	// for which build-directory names git tracks.
	if n := len(rec.Records()); n != 2*len(want) {
		t.Errorf("%d git calls for %d projects, want two each", n, len(want))
	}
	// The submodule is inside a project, so it is never discovered as one
	// and never dated: that is the double-attribution guard.
	for _, r := range rec.Records() {
		for _, a := range r.Cmd.Args {
			if filepath.Base(a) == "sub" {
				t.Error("git was run inside a submodule of a project already dated")
			}
		}
	}
}

// TestProbeWithoutGit: no git is a degradation, not an absence. The projects
// are still found and Classify falls back to directory modification times.
func TestProbeWithoutGit(t *testing.T) {
	f := build(t, corpus())
	env, rec := f.env(false)

	facts, err := detecttest.Probe(t, projects.New(), env)
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if len(rec.Records()) != 0 {
		t.Errorf("git was run although it is not on the path: %v", rec.Records())
	}
	if got := facts.(*projects.Facts); len(got.Projects) != 5 {
		t.Errorf("projects = %+v, want all five found without git", got.Projects)
	}

	_, sum := projects.New().Classify(f.tree, facts, f.cx)
	for _, p := range sum.Projects {
		if p.LastActivity.IsZero() {
			t.Errorf("%s has no last activity, so the mtime fallback did not happen", p.Root)
		}
		if p.LastActivity.Unix() == lastCommit {
			t.Errorf("%s was dated from git, which was never run", p.Root)
		}
	}
}

// TestNoCodeRoot: a machine with nowhere to look is Missing.
func TestNoCodeRoot(t *testing.T) {
	f := build(t, map[string]int64{"Users/andrew/Documents/note.txt": 10})
	env, _ := f.env(true)
	if _, err := detecttest.Probe(t, projects.New(), env); !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
}

// TestClassifyProjects is the whole shape of the answer: which directories
// are projects, what they built, and what may never be deleted.
func TestClassifyProjects(t *testing.T) {
	f := build(t, corpus())
	env, _ := f.env(true)
	facts, err := detecttest.Probe(t, projects.New(), env)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := projects.New().Classify(f.tree, facts, f.cx)

	// The code root itself is claimed, so everything under it that no
	// rule reaches still lands in Developer.
	root, ok := detecttest.ClaimAt(claims, home+"/code")
	if !ok {
		t.Fatal("the code root was not claimed")
	}
	if root.Bucket != classify.BucketDeveloper || root.Owner != "Code root" {
		t.Errorf("code root = %s / %q", root.Bucket, root.Owner)
	}

	sources := []string{
		home + "/code/alpha", home + "/code/beta", home + "/code/gamma",
		home + "/code/delta", home + "/code/php",
	}
	for _, p := range sources {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Category != "Project source" || c.Reclaim != classify.UserData {
			t.Errorf("%s = %q / %s, want project source and user data", p, c.Category, c.Reclaim)
		}
		if !detecttest.HasKey(c, "project:"+filepath.Base(p)) {
			t.Errorf("%s keys = %v", p, c.OwnerKeys)
		}
	}

	// A directory with neither a repository nor a manifest is not a
	// project, and the detector leaves it to the catalog.
	if _, ok := detecttest.ClaimAt(claims, home+"/code/notes"); ok {
		t.Error("a directory with no .git and no manifest was claimed as a project")
	}
	// A submodule inside a project is part of that project.
	if _, ok := detecttest.ClaimAt(claims, home+"/code/delta/sub"); ok {
		t.Error("a submodule inside a project was claimed as a project of its own")
	}

	artifacts := []struct {
		path    string
		owner   string
		reclaim classify.Reclaim
	}{
		{home + "/code/alpha/node_modules", "alpha", classify.Regenerable},
		{home + "/code/beta/dist", "beta", classify.Regenerable},
		{home + "/code/gamma/vendor", "gamma", classify.Regenerable},
		{home + "/code/delta/sub/node_modules", "delta", classify.Regenerable},
		{home + "/code/php/vendor", "php", classify.UserData},
	}
	for _, w := range artifacts {
		c, ok := detecttest.ClaimAt(claims, w.path)
		if !ok {
			t.Errorf("no claim at %s", w.path)
			continue
		}
		if c.Category != "Build artifacts" {
			t.Errorf("%s category = %q", w.path, c.Category)
		}
		if c.Owner != w.owner {
			t.Errorf("%s owner = %q, want %q", w.path, c.Owner, w.owner)
		}
		if c.Reclaim != w.reclaim {
			t.Errorf("%s reclaim = %s, want %s", w.path, c.Reclaim, w.reclaim)
		}
	}

	history := []string{home + "/code/alpha/.git", home + "/code/gamma/.git", home + "/code/delta/.git",
		home + "/code/delta/sub/.git"}
	for _, p := range history {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Category != "Repo history" || c.Reclaim != classify.UserData {
			t.Errorf("%s = %q / %s, want repo history and user data", p, c.Category, c.Reclaim)
		}
	}

	byRoot := map[string]detect.Project{}
	for _, p := range sum.Projects {
		byRoot[p.Root] = p
	}
	if len(byRoot) != 5 {
		t.Fatalf("summary has %d projects, want five", len(byRoot))
	}
	if !byRoot[home+"/code/alpha"].VCS {
		t.Error("alpha has a .git directory but is not marked as under version control")
	}
	if !byRoot[home+"/code/gamma"].VCS {
		t.Error("a worktree's .git file did not count as version control")
	}
	if byRoot[home+"/code/beta"].VCS {
		t.Error("beta has no .git but is marked as under version control")
	}
	// A vendor directory with no modules.txt is not reclaimable, so it is
	// not counted as artifact bytes either.
	if got := byRoot[home+"/code/php"].ArtifactBytes; got != 0 {
		t.Errorf("php artifact bytes = %d, want 0: nothing says its vendor can be re-fetched", got)
	}
	if got := byRoot[home+"/code/alpha"].ArtifactBytes; got < 500_000 {
		t.Errorf("alpha artifact bytes = %d, want its node_modules", got)
	}
	if got := byRoot[home+"/code/delta"].ArtifactBytes; got < 250_000 {
		t.Errorf("delta artifact bytes = %d, want its submodule's node_modules", got)
	}

	// Sorted by what the build cost, largest first, which is the order
	// the Developer view reads in.
	for i := 1; i < len(sum.Projects); i++ {
		if sum.Projects[i-1].ArtifactBytes < sum.Projects[i].ArtifactBytes {
			t.Fatalf("projects are not sorted by artifact bytes: %+v", sum.Projects)
		}
	}
	for _, p := range sum.Projects {
		if p.LastActivity.Unix() != lastCommit {
			t.Errorf("%s last activity = %s, want the commit time", p.Root, p.LastActivity)
		}
	}
}

// TestLastActivityFallsBackToMtime: a project git cannot date is dated from
// its directory, because a date that is only approximately right is still
// what the Developer view needs to sort stale projects to the top.
func TestLastActivityFallsBackToMtime(t *testing.T) {
	f := build(t, corpus())
	_, sum := projects.New().Classify(f.tree, &projects.Facts{}, f.cx)
	if len(sum.Projects) != 5 {
		t.Fatalf("projects = %d, want five with no facts at all", len(sum.Projects))
	}
	cutoff := time.Now().Add(-time.Hour)
	for _, p := range sum.Projects {
		if p.LastActivity.Before(cutoff) {
			t.Errorf("%s last activity = %s, want the fixture's own mtime", p.Root, p.LastActivity)
		}
	}
}

// TestClassifyWithoutAHome: no home means no code roots, which means nothing
// to say rather than a claim on the root of the volume.
func TestClassifyWithoutAHome(t *testing.T) {
	f := build(t, corpus())
	claims, sum := projects.New().Classify(f.tree, &projects.Facts{}, classify.Context{})
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims with no home and no code roots", len(claims))
	}
}

// TestRecordedGitOutputParses guards the one thing the fixture from this
// machine is evidence of: that real `git log --format=%ct` output is what the
// probe expects. The paths in it are this machine's, so it cannot drive a
// fixture tree; the output shape is what it is kept for.
func TestRecordedGitOutputParses(t *testing.T) {
	replay, err := probe.LoadFixture("testdata/this-machine.json")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(replay.Records) == 0 {
		t.Fatal("the recording is empty")
	}
	for key, res := range replay.Records {
		if !res.OK() {
			continue
		}
		secs, err := strconv.ParseInt(trimNewline(res.Stdout), 10, 64)
		if err != nil {
			t.Errorf("%s printed %q, which is not a unix time: %v", key, res.Stdout, err)
			continue
		}
		if secs < 1_000_000_000 {
			t.Errorf("%s printed %d, which is before 2001", key, secs)
		}
	}
}

// trimNewline drops the trailing newline git prints.
func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// TestBuildNamesNeedEvidence is the D34 guard for directories that are named
// like build output and are not. A Terraform folder called env, a Go package
// called build and a committed dist are all source, and a name alone is not
// evidence that a tool made them.
func TestBuildNamesNeedEvidence(t *testing.T) {
	const u = "Users/andrew/"
	f := build(t, map[string]int64{
		// A Terraform tree with an env folder and no pyvenv.cfg: the
		// case found on the machine this was written against.
		u + "code/infra/.git/config":           400,
		u + "code/infra/iac/app/env/main.tf":   120_000,
		u + "code/infra/iac/app/env/vars.tf":   100,
		u + "code/infra/iac/app/versions.tf":   100,
		u + "code/infra/iac/app/venv/notes.md": 90_000,

		// A Go module with a package called build and a root build
		// folder of packaging scripts; nothing Go writes is called build.
		u + "code/goproj/go.mod":                      100,
		u + "code/goproj/internal/build/build.go":     110_000,
		u + "code/goproj/build/package/Dockerfile":    80_000,
		u + "code/goproj/cmd/target/main.go":          70_000,
		u + "code/goproj/internal/coverage/report.go": 60_000,
		u + "code/goproj/internal/out/writer.go":      50_000,
		u + "code/goproj/internal/node_modules/x.js":  40_000,

		// A real virtualenv: pyvenv.cfg is what `python -m venv` writes.
		u + "code/py/pyproject.toml":                 100,
		u + "code/py/.venv/pyvenv.cfg":               80,
		u + "code/py/.venv/lib/python3.12/site/x.py": 200_000,
		u + "code/py/dist/py-1.0.tar.gz":             30_000,

		// A node project whose dist is committed.
		u + "code/web/.git/config":     400,
		u + "code/web/package.json":    200,
		u + "code/web/dist/index.js":   150_000,
		u + "code/web/build/out.js":    140_000,
		u + "code/rust/Cargo.toml":     100,
		u + "code/rust/target/debug/x": 300_000,
	})
	env, _ := f.envTracking(true, map[string][]string{"web": {"dist/index.js", "src/app.ts"}})
	facts, err := detecttest.Probe(t, projects.New(), env)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, _ := projects.New().Classify(f.tree, facts, f.cx)

	source := []string{
		home + "/code/infra/iac/app/env",
		home + "/code/infra/iac/app/venv",
		home + "/code/goproj/internal/build",
		home + "/code/goproj/build",
		home + "/code/goproj/cmd/target",
		home + "/code/goproj/internal/coverage",
		home + "/code/goproj/internal/out",
		home + "/code/web/dist",
	}
	for _, p := range source {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s; a catalog rule for its name would decide it", p)
			continue
		}
		if c.Reclaim != classify.UserData {
			t.Errorf("%s = %q / %s, want user data: nothing says a tool made it", p, c.Category, c.Reclaim)
		}
	}

	built := map[string]classify.Reclaim{
		home + "/code/py/.venv":                     classify.ToolManaged,
		home + "/code/py/dist":                      classify.Regenerable,
		home + "/code/web/build":                    classify.Regenerable,
		home + "/code/rust/target":                  classify.Regenerable,
		home + "/code/goproj/internal/node_modules": classify.Regenerable,
	}
	for p, want := range built {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Category != "Build artifacts" || c.Reclaim != want {
			t.Errorf("%s = %q / %s, want build artifacts / %s", p, c.Category, c.Reclaim, want)
		}
	}
}

// TestTrackedCheckFallsBackToEvidence: when git could not be asked, a
// directory that passes the evidence rules is still claimed, because the
// evidence rules alone are what the budget falls back to.
func TestTrackedCheckFallsBackToEvidence(t *testing.T) {
	f := build(t, map[string]int64{
		"Users/andrew/code/web/.git/config":   400,
		"Users/andrew/code/web/package.json":  200,
		"Users/andrew/code/web/dist/index.js": 150_000,
	})
	claims, _ := projects.New().Classify(f.tree, &projects.Facts{}, f.cx)
	c, ok := detecttest.ClaimAt(claims, home+"/code/web/dist")
	if !ok || c.Reclaim != classify.Regenerable {
		t.Errorf("dist next to package.json with no git answer = %+v, want regenerable", c)
	}
}

// TestRetainLeafKeepsPyvenvCfg: the file that proves a virtualenv is a few
// dozen bytes, so without the hook it is folded away and no venv could ever
// be told apart from a folder called env.
func TestRetainLeafKeepsPyvenvCfg(t *testing.T) {
	hook := projects.New().RetainLeaf(classify.Context{Home: home, CodeRoots: []string{home + "/code"}})
	e := &walk.Entry{Name: "pyvenv.cfg", Kind: walk.KindFile, Size: 80}
	if !hook(mac.ScanPath(home+"/code/py/.venv"), e) {
		t.Error("pyvenv.cfg under a code root was not retained")
	}
}
