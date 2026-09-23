// Package node detects the JavaScript toolchain: npm, pnpm, Yarn, Bun and
// the version managers that install node itself.
//
// This is the heaviest ecosystem on a developer's disk and the one where the
// same bytes are easiest to count twice or not at all, so every path here is
// measured rather than assumed:
//
//   - pnpm keeps a content-addressed store whose layout is versioned. Three
//     generations can sit side by side and only one of them is live; `pnpm
//     store path` is the only thing that knows which, so the other two are
//     marked superseded on its authority and not on a guess about version
//     numbers. ~/Library/Caches/pnpm is a fourth directory, a metadata cache
//     that is not part of the store at all.
//   - Yarn 1 and Yarn Berry keep their caches in different places and answer
//     different questions, so the version is asked first.
//   - `bun pm cache` fails outside a project directory, so bun is not asked.
//   - nvm is a shell function rather than a binary, so there is nothing to
//     run: the default version is read out of its alias files and resolved
//     against the versions on disk.
package node

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "node"

// maxAliasHops bounds the nvm alias chain. An alias may point at another
// alias — default → lts/* → jod → v22.20.0 — and a file that pointed at
// itself would otherwise loop.
const maxAliasHops = 8

func init() { detect.Register(110, New()) }

// Detector finds the node toolchain.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are what the probe learned.
type Facts struct {
	// Home is the home the probe ran against, so measured paths can be
	// moved into the home Classify works with.
	Home string `json:"home,omitempty"`
	// NpmCache is what `npm config get cache` answered.
	NpmCache string `json:"npm_cache,omitempty"`
	// PnpmStore is the live store generation, as `pnpm store path` names
	// it. Everything else in its parent is a previous generation, which is
	// a question for the tree rather than for a second directory listing.
	PnpmStore string `json:"pnpm_store,omitempty"`
	// YarnVersion is what `yarn --version` printed; Berry is 2 and above
	// and keeps its cache somewhere else.
	YarnVersion string `json:"yarn_version,omitempty"`
	YarnCache   string `json:"yarn_cache,omitempty"`
	// NvmDir is nvm's directory, NvmAlias the chain from `default` to a
	// version, and NvmCurrent the version it resolved to.
	NvmDir     string   `json:"nvm_dir,omitempty"`
	NvmAlias   []string `json:"nvm_alias,omitempty"`
	NvmCurrent string   `json:"nvm_current,omitempty"`
	// NodeVersions are the versions nvm has installed, oldest first.
	NodeVersions []string `json:"node_versions,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// Berry reports whether the installed Yarn is 2 or later, which decides
// which question about its cache is the right one.
func (f *Facts) Berry() bool {
	if f == nil || f.YarnVersion == "" {
		return false
	}
	major, _, _ := strings.Cut(f.YarnVersion, ".")
	return major != "" && major != "0" && major != "1"
}

// Probe asks each package manager where it keeps its bytes.
//
// Nothing here is fatal. A machine with pnpm and without yarn is ordinary, so
// a tool that is not on the path is skipped rather than reported, and only a
// tool that is installed and would not answer is a degradation.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	f := &Facts{Home: env.Home}
	var degraded []string
	found := false

	if env.Has("npm") {
		found = true
		if out, err := ask(ctx, env, "npm", "config", "get", "cache"); err != nil {
			degraded = append(degraded, err.Error())
		} else {
			f.NpmCache = out
		}
	}

	if env.Has("pnpm") {
		found = true
		if out, err := ask(ctx, env, "pnpm", "store", "path"); err != nil {
			degraded = append(degraded, err.Error())
		} else {
			f.PnpmStore = out
		}
	}

	if env.Has("yarn") {
		found = true
		switch out, err := ask(ctx, env, "yarn", "--version"); {
		case err != nil:
			degraded = append(degraded, err.Error())
		default:
			f.YarnVersion = out
			cache, cerr := yarnCache(ctx, env, f.Berry())
			if cerr != nil {
				degraded = append(degraded, cerr.Error())
			}
			f.YarnCache = cache
		}
	}

	if env.Has("bun") {
		found = true
	}

	if nvm := nvmDir(env); nvm != "" {
		found = true
		f.NvmDir = nvm
		f.NodeVersions = nodeVersions(env, nvm)
		f.NvmAlias, f.NvmCurrent = resolveDefault(env, nvm, f.NodeVersions)
		if f.NvmCurrent == "" && len(f.NodeVersions) > 0 {
			degraded = append(degraded, "nvm's default alias did not resolve to an installed version")
		}
	}

	if !found {
		return nil, detect.Missingf("none of npm, pnpm, yarn, bun or nvm is present")
	}
	if len(degraded) > 0 {
		return f, detect.Degradedf("%s", strings.Join(degraded, "; "))
	}
	return f, nil
}

// ask runs one command and returns its trimmed first line.
func ask(ctx context.Context, env detect.Env, name string, args ...string) (string, error) {
	res := env.Runner.Run(ctx, probe.Cmd{Name: name, Args: args})
	if !res.OK() {
		return "", fmt.Errorf("%s: %s", strings.Join(append([]string{name}, args...), " "), res.Reason())
	}
	out := strings.TrimSpace(res.Stdout)
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = strings.TrimSpace(out[:i])
	}
	return out, nil
}

// yarnCache asks the question the installed Yarn understands. Yarn 1 has
// `yarn cache dir`; Berry removed it and answers `yarn config get
// cacheFolder` instead.
func yarnCache(ctx context.Context, env detect.Env, berry bool) (string, error) {
	if berry {
		return ask(ctx, env, "yarn", "config", "get", "cacheFolder")
	}
	return ask(ctx, env, "yarn", "cache", "dir")
}

// nvmDir is where nvm keeps its versions, or nothing when nvm is not
// installed.
//
// It is the home-relative default rather than $NVM_DIR because a detector
// reads the machine, not the shell it happens to have been started from:
// under sudo the environment belongs to root, and a scan that resolved nvm
// differently depending on how it was launched would be worse than one that
// always looks in the documented place.
func nvmDir(env detect.Env) string {
	if dir := env.Path(".nvm"); env.Exists(dir) {
		return dir
	}
	return ""
}

// nodeVersions are the node versions nvm has installed, oldest first.
func nodeVersions(env detect.Env, nvm string) []string {
	if env.ReadDir == nil {
		return nil
	}
	entries, err := env.ReadDir(path.Join(nvm, "versions", "node"))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return lessVersion(out[i], out[j]) })
	return out
}

// resolveDefault follows nvm's `default` alias to an installed version, and
// returns the chain it walked as well as the answer.
//
// nvm is a shell function, so there is no `nvm current` to run: the alias
// files are the record. `default` holds something like "22", "lts/*" or
// "v22.20.0"; an alias name resolves through alias/<name>, and a bare version
// prefix resolves against what is installed, newest match winning — which is
// what nvm itself does.
func resolveDefault(env detect.Env, nvm string, installed []string) (chain []string, current string) {
	if env.ReadFile == nil {
		return nil, ""
	}
	name := "default"
	for hop := 0; hop < maxAliasHops; hop++ {
		data, err := env.ReadFile(path.Join(nvm, "alias", name))
		if err != nil {
			return chain, ""
		}
		target := strings.TrimSpace(string(data))
		if target == "" {
			return chain, ""
		}
		chain = append(chain, name+" → "+target)
		if v := matchVersion(target, installed); v != "" {
			return chain, v
		}
		if target == name {
			return chain, ""
		}
		name = target
	}
	return chain, ""
}

// matchVersion picks the installed version an alias target names: an exact
// match, or the newest version whose number starts with the target. "22"
// selects v22.20.0 over v22.9.0; "v18.20.8" selects itself.
func matchVersion(target string, installed []string) string {
	want := "v" + strings.TrimPrefix(target, "v")
	best := ""
	for _, v := range installed {
		switch {
		case v == want:
			return v
		case strings.HasPrefix(v, want+"."):
			if best == "" || lessVersion(best, v) {
				best = v
			}
		}
	}
	return best
}

// lessVersion orders "v18.20.8" before "v22.20.0", numerically per component.
func lessVersion(a, b string) bool {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := atoi(as[i])
		bn, berr := atoi(bs[i])
		if aerr && berr {
			if an != bn {
				return an < bn
			}
			continue
		}
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}

// atoi parses a version component, reporting whether it was a number.
func atoi(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// Classify turns the facts and the tree into claims and tool rows.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}
	at := func(measured, fallback string) string {
		if facts == nil {
			return fallback
		}
		return detect.Prefer(t, detect.Rebase(measured, facts.Home, home), fallback)
	}
	ev := evidence(facts)

	targets := []detect.Target{
		{
			Path: at(npmCacheOf(facts), path.Join(home, ".npm")), Category: "Package cache",
			Owner: "npm", OwnerKeys: []string{"cli:npm"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "npm cache",
			Explain: "packages npm downloaded; `npm cache clean --force` clears it",
		},
		{
			Path: path.Join(home, ".npm/_npx"), Category: "Package cache",
			Owner: "npm", OwnerKeys: []string{"cli:npx"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "npx one-off packages",
			Explain: "packages npx downloaded to run once and never cleaned up",
		},
		{
			Path: path.Join(home, "Library/Caches/pnpm"), Category: "Package cache",
			Owner: "pnpm", OwnerKeys: []string{"cli:pnpm"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "pnpm metadata cache",
			Note:    "separate from the store: registry metadata and side effects, not package content",
			Explain: "pnpm's metadata cache; it is rebuilt from the registry on demand",
		},
		{
			Path: path.Join(home, "Library/Caches/Yarn"), Category: "Package cache",
			Owner: "Yarn", OwnerKeys: []string{"cli:yarn"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "Yarn 1 cache",
			Explain: "the Yarn 1.x cache; `yarn cache clean` clears it",
		},
		{
			Path: path.Join(home, ".yarn"), Category: "Package cache",
			Owner: "Yarn", OwnerKeys: []string{"cli:yarn"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "Yarn Berry global folder",
			Explain: "Yarn Berry's global cache and releases",
		},
		{
			Path: path.Join(home, ".bun"), Category: "Toolchain",
			Owner: "Bun", OwnerKeys: []string{"cli:bun"}, Reclaim: classify.ToolManaged,
			Kind: "toolchain", Name: "Bun",
			Explain: "the Bun runtime and everything it installs",
		},
		{
			Path: path.Join(home, ".bun/install/cache"), Category: "Package cache",
			Owner: "Bun", OwnerKeys: []string{"cli:bun"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "Bun install cache",
			Note:    "`bun pm cache` cannot be asked outside a project, so this is Bun's documented default",
			Explain: "packages Bun downloaded; `bun pm cache rm` clears it",
		},
		{
			Path: path.Join(home, "Library/Caches/node/corepack"), Category: "Package cache",
			Owner: "Node.js", OwnerKeys: []string{"cli:corepack"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "corepack cache",
			Explain: "package manager releases corepack downloaded to run `pnpm` and `yarn`",
		},
		{
			Path: path.Join(home, "Library/Caches/node"), Category: "Package cache",
			Owner: "Node.js", OwnerKeys: []string{"cli:node"}, Reclaim: classify.Regenerable,
			Explain: "caches written by node's own tooling",
		},
		{
			Path: path.Join(home, ".volta"), Category: "Toolchain",
			Owner: "Volta", OwnerKeys: []string{"cli:volta"}, Reclaim: classify.ToolManaged,
			Kind: "toolchain", Name: "Volta",
			Explain: "toolchains Volta pinned per project",
		},
		{
			Path: path.Join(home, ".local/share/fnm"), Category: "Toolchain",
			Owner: "fnm", OwnerKeys: []string{"cli:fnm"}, Reclaim: classify.ToolManaged,
			Kind: "toolchain", Name: "fnm",
			Explain: "node versions fnm installed",
		},
	}
	targets = append(targets, pnpmStores(t, facts, home)...)
	targets = append(targets, nvmTargets(t, facts, home)...)
	targets = append(targets, yarnMeasured(t, facts, home)...)
	for i := range targets {
		targets[i].Bucket = classify.BucketDeveloper
		targets[i].Evidence = append(append([]string(nil), ev...), targets[i].Evidence...)
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// npmCacheOf is the measured npm cache, or nothing.
func npmCacheOf(f *Facts) string {
	if f == nil {
		return ""
	}
	return f.NpmCache
}

// yarnMeasured adds the exact cache directory Yarn named, when it is deeper
// than the directory the static rules know about.
//
// Yarn 1 answers ~/Library/Caches/Yarn/v6: the parent is the cache and the
// child is the cache format in use, so both are worth a row and the deeper
// one carries the version.
func yarnMeasured(t *walk.Tree, f *Facts, home string) []detect.Target {
	if f == nil || f.YarnCache == "" {
		return nil
	}
	cache := detect.Rebase(f.YarnCache, f.Home, home)
	if _, ok := detect.Lookup(t, cache); !ok {
		return nil
	}
	if cache == path.Join(home, "Library/Caches/Yarn") || cache == path.Join(home, ".yarn") {
		return nil
	}
	name := "Yarn cache"
	if f.Berry() {
		name = "Yarn Berry cache"
	}
	return []detect.Target{{
		Path: cache, Category: "Package cache", Owner: "Yarn", OwnerKeys: []string{"cli:yarn"},
		Reclaim: classify.Regenerable, Kind: "cache", Name: name, Version: path.Base(cache),
		Note:    "the cache directory Yarn " + f.YarnVersion + " reported",
		Explain: "packages Yarn downloaded; `yarn cache clean` clears them",
	}}
}

// pnpmStores describes the store and its generations.
//
// The store root is claimed whole so its bytes land in Developer even when
// pnpm could not be asked, and each generation beside the live one is listed
// as superseded. Which is live is pnpm's answer and nothing else: the
// directory names sort v10 before v11 and before v3, and on this machine the
// live one is v10.
func pnpmStores(t *walk.Tree, f *Facts, home string) []detect.Target {
	root := path.Join(home, "Library/pnpm")
	out := []detect.Target{
		{
			Path: root, Category: "Package store", Owner: "pnpm", OwnerKeys: []string{"cli:pnpm"},
			Reclaim: classify.ToolManaged, Kind: "data", Name: "pnpm store root",
			Note:    "node_modules hard-link into this store, so its bytes are counted here once",
			Explain: "the pnpm content-addressed store; every project's dependencies live here",
		},
		{
			Path: path.Join(home, ".pnpm-store"), Category: "Package store", Owner: "pnpm",
			OwnerKeys: []string{"cli:pnpm"}, Reclaim: classify.Regenerable,
			Kind: "versions", Name: "pnpm store", Version: "legacy location",
			Note:    "superseded store generation; pnpm no longer writes here",
			Explain: "an older pnpm store location, left behind by a pnpm upgrade",
		},
	}

	live := ""
	if f != nil {
		live = detect.Rebase(f.PnpmStore, f.Home, home)
	}
	gens := generationPaths(t, live, home)
	for _, gen := range gens {
		current := gen == live
		note := "superseded store generation; `pnpm store prune` only touches the live one"
		reclaim := classify.Regenerable
		if current {
			note = "the live store generation, as `pnpm store path` reports it"
			reclaim = classify.ToolManaged
		} else if live == "" {
			note = "pnpm could not be asked which generation is live"
			reclaim = classify.ToolManaged
		}
		out = append(out, detect.Target{
			Path: gen, Category: "Package store", Owner: "pnpm", OwnerKeys: []string{"cli:pnpm"},
			Reclaim: reclaim, Kind: "versions", Name: "pnpm store", Version: path.Base(gen),
			Current: current, Note: note,
			Explain: "pnpm store generation " + path.Base(gen),
		})
	}
	return out
}

// generationPaths are the store generations to describe.
//
// pnpm's store layout is versioned and a machine that has been through
// several pnpm majors keeps a directory per generation side by side. Which
// ones exist is a question the walk has already answered, so the parent of
// the live store is read out of the tree rather than listed again; when pnpm
// could not be asked, the documented location is used instead.
func generationPaths(t *walk.Tree, live, home string) []string {
	parent := path.Join(home, "Library/pnpm/store")
	if live != "" {
		parent = path.Dir(live)
	}
	names := detect.ChildNames(t, parent)
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, path.Join(parent, n))
	}
	return out
}

// nvmTargets describes nvm's directory and every node version in it.
func nvmTargets(t *walk.Tree, f *Facts, home string) []detect.Target {
	nvm := path.Join(home, ".nvm")
	if f != nil && f.NvmDir != "" {
		nvm = detect.Prefer(t, detect.Rebase(f.NvmDir, f.Home, home), nvm)
	}
	out := []detect.Target{
		{
			Path: nvm, Category: "Toolchain", Owner: "nvm", OwnerKeys: []string{"cli:nvm"},
			Reclaim: classify.ToolManaged, Kind: "toolchain", Name: "nvm",
			Explain: "node versions nvm installed; `nvm uninstall <version>` removes one",
		},
		{
			Path: path.Join(nvm, ".cache"), Category: "Package cache", Owner: "nvm",
			OwnerKeys: []string{"cli:nvm"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "nvm download cache",
			Explain: "node tarballs nvm downloaded; `nvm cache clear` removes them",
		},
	}

	versions := []string(nil)
	current := ""
	if f != nil {
		versions, current = f.NodeVersions, f.NvmCurrent
	}
	if len(versions) == 0 {
		versions = detect.ChildNames(t, path.Join(nvm, "versions", "node"))
	}
	for _, v := range versions {
		note := "installed, not the default"
		if v == current {
			note = "nvm's default version"
		}
		out = append(out, detect.Target{
			Path: path.Join(nvm, "versions", "node", v), Category: "Toolchain",
			Owner: "Node.js", OwnerKeys: []string{"cli:node"}, Reclaim: classify.ToolManaged,
			Kind: "versions", Name: "node", Version: v, Current: v == current, Note: note,
			Explain: "node " + v + " and the packages installed globally into it",
		})
	}
	return out
}

// evidence are the why-panel lines every node claim carries.
func evidence(f *Facts) []string {
	if f == nil {
		return []string{"the node detector did not answer; the paths come from the static catalog"}
	}
	var out []string
	if f.NpmCache != "" {
		out = append(out, "`npm config get cache` → "+f.NpmCache)
	}
	if f.PnpmStore != "" {
		out = append(out, "`pnpm store path` → "+f.PnpmStore+", which is the live store generation")
	}
	if f.YarnVersion != "" {
		question := "`yarn cache dir`"
		if f.Berry() {
			question = "`yarn config get cacheFolder`"
		}
		out = append(out, fmt.Sprintf("`yarn --version` → %s, so %s → %s", f.YarnVersion, question, f.YarnCache))
	}
	if len(f.NvmAlias) > 0 {
		out = append(out, "nvm alias "+strings.Join(f.NvmAlias, ", then ")+
			", resolved against "+strings.Join(f.NodeVersions, " "))
	}
	if f.NvmCurrent != "" {
		out = append(out, "nvm's default node is "+f.NvmCurrent)
	}
	return out
}
