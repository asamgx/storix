// Package projects finds the source projects under a developer's code roots
// and separates what they built from what they wrote.
//
// This is the detector that makes ~/code legible. Thirteen gigabytes under
// one directory is not an answer; "seven of those are node_modules across
// four projects, one of which has not been touched since March" is. Decision
// D34 puts the whole of a code root in the Developer bucket for that reason:
// the source and the history are the user's data and must never be suggested
// for deletion, the build output beside them is regenerable, and only a
// per-project view shows which is which.
//
// # What counts as a project
//
// The nearest directory holding a `.git` or a build manifest. Both halves
// matter: four of the six largest projects on the machine this was written
// against have no `.git` at their top level, because they are checkouts
// nested inside a parent directory or were never committed. A `.git`-only
// rule would have missed them and charged their node_modules to a directory
// called "gib".
//
// Once a directory is a project, the directories inside it are not. A
// monorepo's packages and a repository's submodules belong to the project
// that contains them, which is what stops a submodule's `.git` file from
// being attributed twice.
//
// # Evidence below the threshold
//
// A manifest is a few kilobytes, so the walker folds it into its parent and
// the tree never sees it. This is the one detector that implements
// [detect.LeafRetainer] for that reason: the hook retains manifests, `.git`
// files and the two files that prove a build directory's origin (a venv's
// pyvenv.cfg, a Go vendor's modules.txt) under the code roots and nothing
// else, at a cost of one name comparison per small file and one prefix
// comparison per name that matches.
//
// # A name is not evidence
//
// A directory called env, build or target is build output only when
// something beside it says a tool made it: a virtualenv holds pyvenv.cfg,
// Cargo's target sits next to Cargo.toml, a bundler's dist next to
// package.json. One without that evidence, or one git tracks files in, is
// the user's source and is claimed as such, which also keeps a catalog rule
// for the bare name from deciding it (D34).
package projects

import (
	"context"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "projects"

func init() { detect.Register(220, New()) }

// gitBudget is the total time all the `git log` calls together may take. It
// is a budget rather than a per-call timeout because the number of projects
// is unbounded and the walk is not: a machine with three hundred checkouts
// must not spend a minute dating them.
const gitBudget = 5 * time.Second

// gitCallTimeout bounds one `git log`. A repository whose objects are on a
// network volume can take seconds on its own.
const gitCallTimeout = 2 * time.Second

// maxDiscoveryDepth is how far below a code root the probe looks for a
// project before giving up on that branch. A checkout three directories down
// is normal; one ten down is somebody's backup folder.
const maxDiscoveryDepth = 3

// maxListings bounds the directories the probe lists while looking for
// projects, so a code root pointed at a home directory cannot turn the probe
// into a second walk.
const maxListings = 2000

// maxProjects bounds how many projects are dated and reported.
const maxProjects = 300

// maxArtifactDepth is how deep inside a project the tree is searched for
// build directories. A monorepo nests its packages two or three levels down;
// past a dozen, something is generating directories.
const maxArtifactDepth = 12

// manifests are the files that make a directory a project. They are the
// docs/04 list: one per ecosystem, each of them the file that ecosystem's
// build tool refuses to run without.
var manifests = []string{
	"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "Package.swift",
	"pom.xml", "build.gradle", "build.gradle.kts", "pubspec.yaml", "Gemfile",
	"composer.json",
}

// gitMarker is the name of both a repository directory and the one-line file
// a worktree or submodule has in its place.
const gitMarker = ".git"

// vendorModules is the file that tells a Go `vendor` directory apart from a
// hand-maintained one. With it, the directory is a build input `go mod
// vendor` reproduces; without it, somebody put those files there.
const vendorModules = "modules.txt"

// pyvenvCfg is the file `python -m venv` and virtualenv write at the top of
// every environment. A directory called env or venv without it is not one.
const pyvenvCfg = "pyvenv.cfg"

// gitLsFilesMax caps the answer to `git ls-files` over the build-directory
// names. A project that commits a large dist lists every file in it; past
// this the answer is truncated, the probe treats the project as unchecked
// and the evidence rules alone decide.
const gitLsFilesMax = 16 << 20

// Detector finds source projects under the code roots.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Found is one project the probe dated.
type Found struct {
	// Rel is the project root relative to the home directory, which is
	// what survives the difference between the probe's view of the
	// filesystem and the tree's display paths.
	Rel string `json:"rel"`
	// LastCommit is the unix time of the most recent commit, zero when
	// git could not say.
	LastCommit int64 `json:"last_commit,omitempty"`
	// GitChecked is set when `git ls-files` answered in full for the
	// build-directory names, so Tracked is the whole list.
	GitChecked bool `json:"git_checked,omitempty"`
	// Tracked are the project-relative directories, named like build
	// output, that git tracks at least one file in.
	Tracked []string `json:"tracked,omitempty"`
}

// Facts are what the probe learned: the project roots it found and when each
// was last committed to.
type Facts struct {
	Projects []Found `json:"projects,omitempty"`
	// Roots are the code roots that existed, home-relative.
	Roots []string `json:"roots,omitempty"`
	// Budget is set when the git budget ran out before every project was
	// dated, so the report can say the remaining dates are directory
	// modification times.
	Budget bool `json:"budget_exhausted,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// RetainLeaf implements detect.LeafRetainer.
//
// The hook runs on every walker worker concurrently for every small file
// outside the exempt prefixes, so it does the cheap test first: a name
// comparison against a fixed set, and only for a name that matched, a prefix
// comparison against the code roots. It allocates nothing.
func (*Detector) RetainLeaf(cx classify.Context) func(dir string, e *walk.Entry) bool {
	prefixes := retainPrefixes(cx)
	if len(prefixes) == 0 {
		return nil
	}
	return func(dir string, e *walk.Entry) bool {
		if !isMarkerName(e.Name) {
			return false
		}
		for _, p := range prefixes {
			if strings.HasPrefix(dir, p) {
				return true
			}
		}
		return false
	}
}

// isMarkerName reports whether a file name is evidence this detector needs.
// A switch over string constants compiles to a length test and a comparison
// and allocates nothing, which is the requirement the walker imposes.
func isMarkerName(name string) bool {
	switch name {
	case gitMarker, vendorModules, pyvenvCfg,
		"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "Package.swift",
		"pom.xml", "build.gradle", "build.gradle.kts", "pubspec.yaml", "Gemfile",
		"composer.json":
		return true
	}
	return false
}

// retainPrefixes are the scan-path prefixes the hook tests against, built
// once before the walk starts.
//
// Both spellings of each root are kept. On a real machine the walk's paths
// are rooted at the data volume, so "/Users/u/code" is seen as
// "/System/Volumes/Data/Users/u/code"; in a test the fixture is a temporary
// directory and its own path is what the walker reports. Keeping both costs
// one extra string comparison for a file that is already known to be a
// manifest.
func retainPrefixes(cx classify.Context) []string {
	roots := detect.CodeRoots(cx)
	out := make([]string, 0, len(roots)*2)
	seen := make(map[string]bool, len(roots)*2)
	add := func(p string) {
		if p == "" || p == "/" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, r := range roots {
		add(strings.TrimRight(path.Clean(r), "/") + "/")
		add(strings.TrimRight(mac.ScanPath(r), "/") + "/")
	}
	return out
}

// Probe finds the projects on disk and asks git when each was last committed
// to.
//
// Discovery duplicates a little of what Classify does against the tree, and
// it has to: the probe runs while the walk is still running, so there is no
// tree to consult, and `git log` cannot be run from Classify because Classify
// has no way to run anything. The two halves are joined afterwards by the
// project's path relative to the home.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	if env.Home == "" || env.ReadDir == nil {
		return nil, detect.Missingf("no home directory to look for code roots in")
	}

	f := &Facts{}
	var roots []string
	for _, rel := range defaultRoots() {
		p := path.Join(env.Home, rel)
		if env.Exists(p) {
			roots = append(roots, p)
			f.Roots = append(f.Roots, rel)
		}
	}
	if len(roots) == 0 {
		return nil, detect.Missingf("none of %s exists", strings.Join(defaultRoots(), ", "))
	}

	listings := 0
	var found []string
	for _, root := range roots {
		discover(env, root, 0, &listings, &found)
	}
	if len(found) == 0 {
		return f, detect.Degradedf("no project with a .git or a build manifest under %s",
			strings.Join(f.Roots, ", "))
	}

	f.Projects = make([]Found, 0, len(found))
	for _, p := range found {
		f.Projects = append(f.Projects, Found{Rel: relTo(env.Home, p)})
	}

	if !env.Has("git") {
		return f, detect.Degradedf("git is not on the path; project dates come from directory modification times")
	}
	dateProjects(ctx, env, found, f)
	if f.Budget {
		return f, detect.Degradedf("the %s budget for `git log` ran out; the remaining dates come from directory modification times", gitBudget)
	}
	return f, nil
}

// defaultRoots are the code roots the probe looks in, home-relative.
//
// The probe cannot see the configured roots: detect.Env carries the machine,
// not the classification context, and the context only reaches this detector
// at Classify and RetainLeaf time. A root configured outside this list is
// still classified — Classify works from the context — but its projects are
// dated from their directory modification time rather than from git.
func defaultRoots() []string {
	out := make([]string, 0, len(classify.DefaultCodeRoots))
	for _, r := range classify.DefaultCodeRoots {
		out = append(out, strings.TrimPrefix(r, "~/"))
	}
	return out
}

// discover descends a directory looking for the nearest projects, stopping on
// each branch as soon as it finds one.
func discover(env detect.Env, dir string, depth int, listings *int, found *[]string) {
	if depth > maxDiscoveryDepth || *listings >= maxListings || len(*found) >= maxProjects {
		return
	}
	*listings++
	entries, err := env.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		name := e.Name()
		if name == gitMarker || isManifest(name) {
			*found = append(*found, dir)
			return
		}
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || isArtifactName(name) || strings.HasPrefix(name, ".") {
			continue
		}
		discover(env, path.Join(dir, name), depth+1, listings, found)
	}
}

// isManifest reports whether a file name is a build manifest.
func isManifest(name string) bool {
	for _, m := range manifests {
		if name == m {
			return true
		}
	}
	return false
}

// dateProjects asks git two things about each found project within one
// shared budget: when it was last committed to, and which of its
// build-directory names git tracks files in. The order is discovery order,
// and the budget simply stops the loop; a project it never reached is dated
// from its directory and judged by the evidence rules alone.
func dateProjects(ctx context.Context, env detect.Env, dirs []string, f *Facts) {
	deadline := time.Now().Add(gitBudget)
	timeout := func() time.Duration {
		return min(gitCallTimeout, time.Until(deadline))
	}
	for i, dir := range dirs {
		if timeout() <= 0 {
			f.Budget = true
			return
		}
		res := env.Runner.Run(ctx, probe.Cmd{
			Name:    "git",
			Args:    []string{"-C", dir, "log", "-1", "--format=%ct"},
			Timeout: timeout(),
		})
		if res.OK() {
			if secs := parseUnix(res.Stdout); secs > 0 {
				f.Projects[i].LastCommit = secs
			}
		}

		if timeout() <= 0 {
			f.Budget = true
			return
		}
		res = env.Runner.Run(ctx, probe.Cmd{
			Name:      "git",
			Args:      append([]string{"-C", dir, "ls-files", "-z", "--"}, trackedPathspecs...),
			Timeout:   timeout(),
			MaxStdout: gitLsFilesMax,
		})
		// A project that is not a repository, a git that failed, and an
		// answer cut off at the cap all leave the project unchecked.
		if res.OK() {
			f.Projects[i].GitChecked = true
			f.Projects[i].Tracked = trackedDirs(res.Stdout)
		}
	}
}

// trackedPathspecs select, in `git ls-files`, every file inside a directory
// named like build output at any depth.
var trackedPathspecs = func() []string {
	out := make([]string, 0, len(artifacts)+1)
	for name := range artifacts {
		out = append(out, ":(glob)**/"+name+"/**")
	}
	sort.Strings(out)
	return append(out, ":(glob)**/*"+eggInfoSuffix+"/**")
}()

// trackedDirs are the build-output-named directories that hold the files a
// NUL-separated `git ls-files` listing names, each once.
func trackedDirs(out string) []string {
	seen := map[string]bool{}
	var dirs []string
	for file := range strings.SplitSeq(out, "\x00") {
		parts := strings.Split(file, "/")
		for j := 0; j < len(parts)-1; j++ {
			if !isArtifactName(parts[j]) {
				continue
			}
			d := strings.Join(parts[:j+1], "/")
			if !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
	}
	sort.Strings(dirs)
	return dirs
}

// parseUnix reads the unix timestamp `git log --format=%ct` prints.
func parseUnix(out string) int64 {
	var n int64
	digits := 0
	for _, r := range strings.TrimSpace(out) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
		digits++
		if digits > 18 {
			return 0
		}
	}
	if digits == 0 {
		return 0
	}
	return n
}

// relTo is p relative to base, or empty when p is not under it.
func relTo(base, p string) string {
	base = strings.TrimRight(path.Clean(base), "/")
	p = path.Clean(p)
	switch {
	case base == "" || p == base:
		return ""
	case strings.HasPrefix(p, base+"/"):
		return p[len(base)+1:]
	}
	return ""
}

// artifact describes one kind of build directory.
type artifact struct {
	reclaim classify.Reclaim
	explain string
}

// artifacts are the directory names docs/04 lists as build output, with what
// each one is. Everything here is regenerable except `vendor`, which is
// decided per directory by [vendorArtifact]; the generic names among them
// are only build output with evidence beside them, see [producers].
var artifacts = map[string]artifact{
	"node_modules":  {classify.Regenerable, "installed npm dependencies; the package manager reinstalls them from the lockfile"},
	".pnpm-store":   {classify.Regenerable, "a project-local pnpm store, refilled on the next install"},
	"target":        {classify.Regenerable, "Cargo's build output; `cargo build` recreates it"},
	"dist":          {classify.Regenerable, "the packaged build output"},
	"build":         {classify.Regenerable, "the build directory"},
	"out":           {classify.Regenerable, "the build output directory"},
	".next":         {classify.Regenerable, "Next.js build output and its cache"},
	".nuxt":         {classify.Regenerable, "Nuxt build output"},
	".svelte-kit":   {classify.Regenerable, "SvelteKit build output"},
	".turbo":        {classify.Regenerable, "Turborepo's task cache; the next run refills it"},
	".parcel-cache": {classify.Regenerable, "Parcel's build cache"},
	".cache":        {classify.Regenerable, "a project-local build cache"},
	".venv":         {classify.ToolManaged, "a Python virtual environment; recreate it from the lockfile"},
	"venv":          {classify.ToolManaged, "a Python virtual environment; recreate it from the lockfile"},
	"env":           {classify.ToolManaged, "a Python virtual environment; recreate it from the lockfile"},
	"__pycache__":   {classify.Regenerable, "compiled Python bytecode, regenerated on import"},
	".pytest_cache": {classify.Regenerable, "pytest's cache of the last run"},
	".mypy_cache":   {classify.Regenerable, "mypy's incremental cache"},
	".ruff_cache":   {classify.Regenerable, "ruff's cache"},
	".tox":          {classify.Regenerable, "tox's per-environment installs, rebuilt on the next run"},
	".gradle":       {classify.Regenerable, "the project's Gradle state and caches"},
	".idea":         {classify.Regenerable, "the JetBrains project index and workspace state"},
	"DerivedData":   {classify.Regenerable, "a project-local Xcode build directory"},
	".build":        {classify.Regenerable, "SwiftPM's build output; `swift build` recreates it"},
	"Pods":          {classify.Regenerable, "installed CocoaPods; `pod install` recreates them"},
	".terraform":    {classify.Regenerable, "downloaded Terraform providers and modules; `terraform init` fetches them again"},
	".serverless":   {classify.Regenerable, "Serverless Framework packaging output"},
	".dart_tool":    {classify.Regenerable, "Dart's per-project tool state"},
	"coverage":      {classify.Regenerable, "coverage reports from the last test run"},
	".nyc_output":   {classify.Regenerable, "nyc's raw coverage data"},
	".eggs":         {classify.Regenerable, "setuptools' downloaded build dependencies"},
	".zig-cache":    {classify.Regenerable, "Zig's per-project build cache"},
	"zig-out":       {classify.Regenerable, "Zig's build output"},
	".direnv":       {classify.Regenerable, "direnv's materialised environment, rebuilt on the next entry"},
	".devenv":       {classify.Regenerable, "devenv's materialised environment"},
	"vendor":        {classify.UserData, "vendored dependencies"},
}

// producers are, for the build-directory names common enough to be source
// too, the manifests of the ecosystems that write them. The directory counts
// as build output only when one of these sits directly beside it; a Go
// package called build or a Terraform folder called out has none.
var producers = map[string][]string{
	"target":        {"Cargo.toml", "pom.xml"},
	"build":         {"package.json", "build.gradle", "build.gradle.kts", "pyproject.toml", "pubspec.yaml"},
	"dist":          {"package.json", "pyproject.toml"},
	"out":           {"package.json"},
	".next":         {"package.json"},
	".nuxt":         {"package.json"},
	".svelte-kit":   {"package.json"},
	".turbo":        {"package.json"},
	".parcel-cache": {"package.json"},
	".cache":        {"package.json"},
	"coverage":      {"package.json"},
	".nyc_output":   {"package.json"},
}

// venvNames are the directory names a Python virtual environment is
// conventionally given, and each is only one when it holds pyvenv.cfg.
var venvNames = map[string]bool{".venv": true, "venv": true, "env": true}

// unproven says why a directory named like build output is not treated as
// one, or "" when the evidence is there. parent is the directory holding it.
func unproven(parent, n *walk.Node) string {
	if venvNames[n.Name] {
		if hasChild(n, pyvenvCfg) {
			return ""
		}
		return "named " + n.Name + " but holds no pyvenv.cfg, so it is not a Python virtual environment; kept as source"
	}
	want, ok := producers[n.Name]
	if !ok {
		return ""
	}
	for _, m := range want {
		if hasChild(parent, m) {
			return ""
		}
	}
	return "named " + n.Name + " but nothing beside it (" + strings.Join(want, ", ") +
		") says a build tool made it; kept as source"
}

// hasChild reports whether a directory's retained children include name.
func hasChild(n *walk.Node, name string) bool {
	for _, c := range n.Children {
		if c.Name == name {
			return true
		}
	}
	return false
}

// eggInfoSuffix is the one artifact name that is a pattern rather than a
// literal: setuptools names the directory after the distribution.
const eggInfoSuffix = ".egg-info"

// isArtifactName reports whether a directory name is build output.
func isArtifactName(name string) bool {
	if _, ok := artifacts[name]; ok {
		return true
	}
	return strings.HasSuffix(name, eggInfoSuffix) && len(name) > len(eggInfoSuffix)
}

// artifactFor describes one build directory, resolving the `vendor` question
// from whether the directory holds a Go module manifest.
func artifactFor(n *walk.Node) artifact {
	if a, ok := artifacts[n.Name]; ok {
		if n.Name == "vendor" {
			return vendorArtifact(n)
		}
		return a
	}
	return artifact{classify.Regenerable, "generated package metadata, rebuilt by the next build"}
}

// vendorArtifact decides what a `vendor` directory is. Go writes
// vendor/modules.txt and reproduces the whole directory from it; a PHP or
// hand-maintained vendor directory has no such file and its contents may be
// the only copy.
func vendorArtifact(n *walk.Node) artifact {
	for _, child := range n.Children {
		if child.Name == vendorModules {
			return artifact{classify.Regenerable,
				"Go's vendored dependencies; `go mod vendor` reproduces them from go.mod"}
		}
	}
	return artifact{classify.UserData,
		"vendored dependencies with no modules.txt, so nothing here says they can be re-fetched"}
}

// Classify finds the projects in the tree and claims what they built.
func (d *Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	idx := index(facts)

	var targets []detect.Target
	var found []detect.Project
	for _, root := range detect.CodeRoots(cx) {
		n, ok := detect.Lookup(t, root)
		if !ok || !n.IsDir() {
			continue
		}
		targets = append(targets, detect.Target{
			Path: root, Bucket: classify.BucketDeveloper, Category: "Project source",
			Owner: "Code root", Reclaim: classify.UserData,
			Explain: "a code root: the projects under it are yours, their build output is not",
		})
		collect(n, root, cx.Home, idx, &targets, &found)
	}
	if len(targets) == 0 {
		return nil, detect.Summary{}
	}

	claims, _ := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].ArtifactBytes > found[j].ArtifactBytes })
	return claims, detect.Summary{Projects: found}
}

// index maps a home-relative project path to what the probe learned of it.
func index(f *Facts) map[string]*Found {
	if f == nil {
		return nil
	}
	out := make(map[string]*Found, len(f.Projects))
	for i := range f.Projects {
		out[f.Projects[i].Rel] = &f.Projects[i]
	}
	return out
}

// tracked is the set of a project's build-named directories git tracks
// files in, or nil when git was not asked or did not answer in full.
func tracked(fd *Found) map[string]bool {
	if fd == nil || !fd.GitChecked {
		return nil
	}
	out := make(map[string]bool, len(fd.Tracked))
	for _, d := range fd.Tracked {
		out[d] = true
	}
	return out
}

// collect walks a directory looking for projects, and describes each one it
// finds. It does not look inside a project for more projects: a monorepo's
// packages and a repository's submodules are parts of the project that holds
// them, which is what keeps a submodule's history from being counted twice.
func collect(n *walk.Node, display, home string, idx map[string]*Found,
	targets *[]detect.Target, found *[]detect.Project) {

	if len(*found) >= maxProjects {
		return
	}
	if isProject(n) {
		describe(n, display, home, idx, targets, found)
		return
	}
	for _, child := range n.Children {
		if !child.IsDir() || isArtifactName(child.Name) {
			continue
		}
		collect(child, path.Join(display, child.Name), home, idx, targets, found)
	}
}

// isProject reports whether a directory is the root of a project: it holds a
// .git, of either shape, or a build manifest.
func isProject(n *walk.Node) bool {
	for _, child := range n.Children {
		if child.Name == gitMarker || isManifest(child.Name) {
			return true
		}
	}
	return false
}

// describe emits one project's claims and its summary row.
func describe(n *walk.Node, display, home string, idx map[string]*Found,
	targets *[]detect.Target, found *[]detect.Project) {

	name := n.Name
	keys := []string{"project:" + name}
	*targets = append(*targets, detect.Target{
		Path: display, Bucket: classify.BucketDeveloper, Category: "Project source",
		Owner: name, OwnerKeys: keys, Reclaim: classify.UserData,
		Explain:  "source of the project " + name + "; everything here but its build output is yours",
		Evidence: []string{"the nearest directory with a .git or a build manifest"},
	})

	fd := idx[relTo(home, display)]
	p := detect.Project{Root: display, Node: n.ID, VCS: hasGit(n)}
	gather(n, display, "", name, keys, tracked(fd), 0, targets, &p)

	p.LastActivity = lastActivity(n, fd)
	sort.SliceStable(p.Artifacts, func(i, j int) bool { return p.Artifacts[i].Bytes > p.Artifacts[j].Bytes })
	*found = append(*found, p)
}

// hasGit reports whether a project has its repository history here, in either
// shape: a .git directory holds it, a .git file points at somebody else's.
func hasGit(n *walk.Node) bool {
	for _, child := range n.Children {
		if child.Name == gitMarker {
			return true
		}
	}
	return false
}

// gather walks inside a project claiming its build directories and its
// repository history, and stops descending at each one it claims. rel is n's
// path inside the project and git the directories git tracks files in.
//
// A directory named like build output without the evidence for it, or with
// files git tracks, is claimed as source and descended into like any other:
// the claim is what stops a catalog rule for the bare name from deciding it.
func gather(n *walk.Node, display, rel, project string, keys []string, git map[string]bool, depth int,
	targets *[]detect.Target, p *detect.Project) {

	if depth > maxArtifactDepth {
		return
	}
	for _, child := range n.Children {
		childPath := path.Join(display, child.Name)
		childRel := path.Join(rel, child.Name)
		why := ""
		if child.IsDir() && isArtifactName(child.Name) {
			why = unproven(n, child)
			if why == "" && git[childRel] {
				why = "git tracks files in " + child.Name + ", so it is source under version control, not build output"
			}
		}
		switch {
		case child.Name == gitMarker:
			*targets = append(*targets, detect.Target{
				Path: childPath, Bucket: classify.BucketDeveloper, Category: "Repo history",
				Owner: project, OwnerKeys: keys, Reclaim: classify.UserData,
				Explain: gitExplain(child, project),
			})
		case !child.IsDir():
			continue
		case why != "":
			*targets = append(*targets, detect.Target{
				Path: childPath, Bucket: classify.BucketDeveloper, Category: "Project source",
				Owner: project, OwnerKeys: keys, Reclaim: classify.UserData,
				Explain: why,
			})
			gather(child, childPath, childRel, project, keys, git, depth+1, targets, p)
		case isArtifactName(child.Name):
			a := artifactFor(child)
			*targets = append(*targets, detect.Target{
				Path: childPath, Bucket: classify.BucketDeveloper, Category: "Build artifacts",
				Owner: project, OwnerKeys: keys, Reclaim: a.reclaim,
				Kind: "cache", Name: project + "/" + child.Name, Explain: a.explain,
			})
			if a.reclaim != classify.UserData {
				p.ArtifactBytes += child.Bytes
			}
			p.Artifacts = append(p.Artifacts, detect.Tool{
				Name: child.Name, Kind: "cache", Path: childPath, Node: child.ID,
				Bytes: child.Bytes, Reclaim: a.reclaim, Note: a.explain,
			})
		default:
			gather(child, childPath, childRel, project, keys, git, depth+1, targets, p)
		}
	}
}

// gitExplain says what a .git is, which depends on its shape.
func gitExplain(n *walk.Node, project string) string {
	if !n.IsDir() {
		return "a worktree or submodule marker pointing at " + project + "'s history, which is kept elsewhere"
	}
	return "git history of " + project + "; deleting it loses every unpushed commit"
}

// lastActivity is when a project was last touched: its most recent commit
// when git could say, its directory's modification time otherwise.
func lastActivity(n *walk.Node, fd *Found) time.Time {
	if fd != nil && fd.LastCommit > 0 {
		return time.Unix(fd.LastCommit, 0)
	}
	if n.Mtime > 0 {
		return time.Unix(n.Mtime, 0)
	}
	return time.Time{}
}
