// Package clitools accounts for the long tail: the dozens of command-line
// tools that each keep a few hundred megabytes somewhere in the home
// directory and that no single detector would ever be written for.
//
// It runs no probes. Everything it knows comes from the tree the walk already
// produced, which is what makes it cheap enough to cover ~/.cache and
// ~/.local/share directory by directory: the name of a subdirectory of
// ~/.cache is the name of the tool that wrote it, and that is a better owner
// than "Tool caches" even when storix has never heard of the tool.
//
// A known name earns an explanation rather than a different bucket. Telling a
// reader that ~/Library/Caches/ms-playwright is a gigabyte of browser
// binaries that `playwright uninstall` removes is the whole difference
// between a number and an action.
package clitools

import (
	"context"
	"path"
	"sort"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "cli-tools"

func init() { detect.Register(190, New()) }

// Detector accounts for per-tool caches and data directories.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are empty: this detector asks the machine nothing.
//
// It still has a fact type because the framework stores one per detector in
// the cache, and a detector that returned nil would be indistinguishable from
// one whose probe had failed.
type Facts struct {
	// Probed records that the detector ran, which is the only thing there
	// is to record when there is nothing to ask.
	Probed bool `json:"probed"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// roots are the directories whose absence means this detector has nothing to
// say about the machine.
var roots = []string{".cache", ".local/share", "Library/Caches"}

// Probe runs no command. Every path this detector knows is a documented
// default, and starting a process to confirm a directory the walk has already
// measured would be a cost with no answer attached.
//
// It still looks: a machine with none of these directories is one this
// detector knows nothing about, and saying so is better than reporting a
// successful probe that found nothing.
func (*Detector) Probe(_ context.Context, env detect.Env) (detect.Facts, error) {
	for _, r := range roots {
		if env.Exists(env.Path(r)) {
			return &Facts{Probed: true}, nil
		}
	}
	return nil, detect.Missingf("none of ~/%s exists", strings.Join(roots, ", ~/"))
}

// generic is the priority of a claim made from a directory's own name rather
// than from knowing what the tool is. It loses to every other detector's
// claim on the same node, which is how ~/.local/share/nvim ends up owned by
// the ide detector's "Neovim" and not by this one's "nvim".
const generic = -1

// entry is one directory this detector recognises by name.
type entry struct {
	// owner is the display label; empty means "use the directory name".
	owner string
	// key is the join key's suffix after "cli:"; empty means the
	// directory name.
	key string
	// kind is the tool row's kind.
	kind string
	// category and reclaim place the bytes in the ledger.
	category string
	reclaim  classify.Reclaim
	explain  string
	note     string
}

// libraryCaches are the ~/Library/Caches directories that belong to a
// developer tool rather than to an application.
//
// go-build is deliberately absent: the go detector measures it with `go env
// GOCACHE` and claiming it here as well would have two detectors arguing over
// the same node for no gain.
var libraryCaches = map[string]entry{
	"ms-playwright": {
		owner: "Playwright", key: "playwright", kind: "cache", category: "Tool cache",
		reclaim: classify.ToolManaged,
		explain: "browser builds Playwright downloaded; `npx playwright uninstall` removes them",
	},
	"ms-playwright-go": {
		owner: "Playwright", key: "playwright", kind: "cache", category: "Tool cache",
		reclaim: classify.ToolManaged,
		explain: "browsers the Go Playwright driver downloaded",
	},
	"ms-playwright-mcp": {
		owner: "Playwright", key: "playwright", kind: "cache", category: "Tool cache",
		reclaim: classify.Regenerable,
		explain: "browser profiles the Playwright MCP server keeps between sessions",
	},
	"Cypress": {
		owner: "Cypress", key: "cypress", kind: "cache", category: "Tool cache",
		reclaim: classify.ToolManaged,
		explain: "one Cypress binary per version ever used; `cypress cache prune` keeps the current one",
	},
	"puppeteer": {
		owner: "Puppeteer", key: "puppeteer", kind: "cache", category: "Tool cache",
		reclaim: classify.ToolManaged,
		explain: "Chromium builds Puppeteer downloaded",
	},
	"node-gyp": {
		owner: "node-gyp", kind: "cache", category: "Package cache", reclaim: classify.Regenerable,
		explain: "node headers native modules are compiled against, one set per node version",
	},
	"typescript": {
		owner: "TypeScript", key: "tsc", kind: "cache", category: "Tool cache",
		reclaim: classify.Regenerable,
		explain: "type definitions the TypeScript language server downloaded automatically",
	},
	"prisma-nodejs": {
		owner: "Prisma", key: "prisma", kind: "cache", category: "Tool cache",
		reclaim: classify.Regenerable,
		explain: "Prisma query engine binaries, one per version and platform",
	},
	"dev.biomejs.biome": {
		owner: "Biome", key: "biome", kind: "cache", category: "Tool cache",
		reclaim: classify.Regenerable, explain: "Biome's formatter and linter cache",
	},
	"helm": {
		owner: "Helm", key: "helm", kind: "cache", category: "Tool cache",
		reclaim: classify.Regenerable,
		explain: "chart repository indexes and downloaded charts; `helm repo update` refills them",
	},
	"org.swift.swiftpm": {
		owner: "SwiftPM", key: "swift", kind: "cache", category: "Package cache",
		reclaim: classify.Regenerable,
		explain: "Swift Package Manager's shared dependency cache",
	},
	"gopls": {
		owner: "gopls", kind: "cache", category: "Tool cache", reclaim: classify.Regenerable,
		explain: "the Go language server's index; it rebuilds on the next editor session",
	},
	"golangci-lint": {
		owner: "golangci-lint", kind: "cache", category: "Tool cache", reclaim: classify.Regenerable,
		explain: "golangci-lint's analysis cache",
	},
	"goimports": {
		owner: "goimports", kind: "cache", category: "Tool cache", reclaim: classify.Regenerable,
		explain: "goimports' index of importable packages",
	},
	"staticcheck": {
		owner: "staticcheck", kind: "cache", category: "Tool cache", reclaim: classify.Regenerable,
		explain: "staticcheck's analysis cache",
	},
	"JNA": {
		owner: "Java", key: "java", kind: "cache", category: "Tool cache", reclaim: classify.Regenerable,
		explain: "native libraries JNA unpacked out of JARs",
	},
}

// dotDirs are the home dot-directories a command-line tool owns outright,
// rather than a cache it can refill.
var dotDirs = map[string]entry{
	".deno": {
		owner: "Deno", key: "deno", kind: "toolchain", category: "Toolchain",
		reclaim: classify.ToolManaged,
		explain: "the Deno runtime and every dependency it has cached",
	},
	".wasmtime": {
		owner: "Wasmtime", key: "wasmtime", kind: "toolchain", category: "Toolchain",
		reclaim: classify.ToolManaged, explain: "the Wasmtime runtime",
	},
	".terraform.d": {
		owner: "Terraform", key: "terraform", kind: "cache", category: "Package cache",
		reclaim: classify.Regenerable,
		explain: "Terraform's plugin cache: one copy of each provider, shared by every workspace",
	},
	".pulumi": {
		owner: "Pulumi", key: "pulumi", kind: "cache", category: "Package cache",
		reclaim: classify.Regenerable, explain: "Pulumi's downloaded plugins and language hosts",
	},
}

// nested are the directories worth a row of their own inside a dot-directory
// that has one already.
var nested = []struct {
	rel string
	entry
}{
	{".terraform.d/plugin-cache", entry{
		owner: "Terraform", key: "terraform", kind: "cache", category: "Package cache",
		reclaim: classify.Regenerable,
		explain: "provider binaries Terraform shares between workspaces",
	}},
	{".pulumi/plugins", entry{
		owner: "Pulumi", key: "pulumi", kind: "cache", category: "Package cache",
		reclaim: classify.Regenerable, explain: "Pulumi provider plugins",
	}},
}

// deferred are the ~/.cache subdirectories another detector in this set
// measures by asking the tool itself.
//
// `uv cache dir` and `go env GOCACHE` produce the same directories this
// detector would guess at, with evidence attached. Claiming them here as well
// would put the same path in two groups of the report and count its bytes
// twice in the reclaim lines, so the tool that asked keeps them. With that
// detector switched off the catalog's dev.* rules still bucket them.
var deferred = map[string]bool{
	"uv": true, "pre-commit": true, "pip": true,
	"pnpm": true, "go-build": true, "yarn": true,
}

// knownCaches explain the ~/.cache subdirectories worth naming. Anything not
// here is still claimed, with the directory's own name as its owner.
var knownCaches = map[string]string{
	"uv":               "uv's wheel cache; `uv cache clean` clears it",
	"pip":              "wheels pip downloaded",
	"pnpm":             "pnpm's metadata cache",
	"go-build":         "the Go build cache",
	"node":             "caches written by node and its tooling",
	"puppeteer":        "Chromium builds Puppeteer downloaded",
	"prisma":           "Prisma engine downloads",
	"huggingface":      "models and datasets the Hugging Face client downloaded",
	"torch":            "model weights PyTorch downloaded",
	"whisper":          "Whisper model weights",
	"pre-commit":       "hook environments pre-commit built",
	"nvim":             "Neovim's cache",
	"zig":              "the Zig compilation cache",
	"codex-runtimes":   "language runtimes Codex downloaded to run tools",
	"gh":               "the GitHub CLI's HTTP cache",
	"github-copilot":   "the Copilot CLI's session state",
	"tree-sitter":      "grammars tree-sitter compiled",
	"starship":         "the Starship prompt's cache",
	"deno":             "Deno's dependency cache",
	"yarn":             "Yarn's cache",
	"electron":         "Electron runtimes downloaded to build desktop apps",
	"electron-builder": "electron-builder's downloaded toolchains",
	"ms-playwright":    "browser builds Playwright downloaded",
	"selenium":         "browser drivers Selenium downloaded",
	"bazel":            "Bazel's repository cache",
	"ccache":           "the compiler cache",
	"sccache":          "the shared compiler cache",
	"pkg":              "packages a language toolchain downloaded",
}

// Classify walks the tree's own directory listings and turns each into a
// claim with the tool's name on it.
func (*Detector) Classify(t *walk.Tree, _ detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}

	var targets []detect.Target
	targets = append(targets, cacheDirs(t, home)...)
	targets = append(targets, shareDirs(t, home)...)
	targets = append(targets, namedCaches(t, home)...)
	targets = append(targets, namedDotDirs(home)...)

	for i := range targets {
		targets[i].Bucket = classify.BucketDeveloper
	}
	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	sortTools(tools)
	return claims, detect.Summary{Tools: tools}
}

// cacheDirs claims every subdirectory of ~/.cache under the name of the tool
// that made it.
func cacheDirs(t *walk.Tree, home string) []detect.Target {
	root := path.Join(home, ".cache")
	names := detect.ChildNames(t, root)
	out := make([]detect.Target, 0, len(names)+1)
	out = append(out, detect.Target{
		Path: root, Category: "Tool cache", Owner: "Tool caches", Reclaim: classify.Regenerable,
		Priority: generic,
		Explain:  "the XDG cache directory: one subdirectory per tool, all of it rebuildable",
		Evidence: []string{"the walk found " + plural(len(names), "tool cache") + " under ~/.cache"},
	})
	for _, name := range names {
		if deferred[name] {
			continue
		}
		explain, known := knownCaches[name]
		if !known {
			explain = name + "'s cache; the tool refills it on demand"
		}
		out = append(out, detect.Target{
			Path: path.Join(root, name), Category: "Tool cache", Owner: name,
			OwnerKeys: []string{"cli:" + name}, Reclaim: classify.Regenerable, Priority: generic,
			Kind: "cache", Name: name, Explain: explain, Note: unknownNote(known),
		})
	}
	return out
}

// shareDirs claims every subdirectory of ~/.local/share.
//
// These are not caches: a tool's data directory holds the plugins, models and
// state it would not know how to recreate, so the reclaim tag is Unknown
// rather than Regenerable and nothing here is counted as free space.
func shareDirs(t *walk.Tree, home string) []detect.Target {
	root := path.Join(home, ".local/share")
	names := detect.ChildNames(t, root)
	out := make([]detect.Target, 0, len(names)+1)
	out = append(out, detect.Target{
		Path: root, Category: "Tool data", Owner: "User-local tools", Reclaim: classify.Unknown,
		Priority: generic,
		Explain:  "the XDG data directory: per-user state for tools installed outside a package manager",
	})
	for _, name := range names {
		out = append(out, detect.Target{
			Path: path.Join(root, name), Category: "Tool data", Owner: name,
			OwnerKeys: []string{"cli:" + name}, Reclaim: classify.Unknown, Priority: generic,
			Kind: "data", Name: name,
			Explain: name + "'s per-user data; it is not a cache and the tool will not rebuild it",
		})
	}
	return out
}

// namedCaches claims the ~/Library/Caches directories a developer tool owns.
func namedCaches(t *walk.Tree, home string) []detect.Target {
	root := path.Join(home, "Library/Caches")
	names := make([]string, 0, len(libraryCaches))
	for name := range libraryCaches {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]detect.Target, 0, len(names))
	for _, name := range names {
		e := libraryCaches[name]
		p := path.Join(root, name)
		if _, ok := detect.Lookup(t, p); !ok {
			continue
		}
		out = append(out, target(p, name, e))
	}
	return out
}

// namedDotDirs claims the home dot-directories this detector knows by name.
func namedDotDirs(home string) []detect.Target {
	names := make([]string, 0, len(dotDirs))
	for name := range dotDirs {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]detect.Target, 0, len(names)+len(nested))
	for _, name := range names {
		out = append(out, target(path.Join(home, name), name, dotDirs[name]))
	}
	for _, n := range nested {
		out = append(out, target(path.Join(home, n.rel), path.Base(n.rel), n.entry))
	}
	return out
}

// target builds one target from a table entry.
func target(p, name string, e entry) detect.Target {
	owner := e.owner
	if owner == "" {
		owner = name
	}
	key := e.key
	if key == "" {
		key = name
	}
	return detect.Target{
		Path: p, Category: e.category, Owner: owner, OwnerKeys: []string{"cli:" + key},
		Reclaim: e.reclaim, Kind: e.kind, Name: owner, Note: e.note, Explain: e.explain,
	}
}

// unknownNote marks a directory storix has no explanation for, so that a
// reader can tell an educated guess from a recognised name.
func unknownNote(known bool) string {
	if known {
		return ""
	}
	return "named after the tool that wrote it; storix has no entry for it"
}

// plural renders a count with its noun.
func plural(n int, noun string) string {
	s := itoa(n) + " " + noun
	if n != 1 {
		s += "s"
	}
	return s
}

// itoa avoids a strconv import for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// sortTools puts the largest rows first, because this detector produces the
// most rows of any and the reader wants the ones worth acting on.
func sortTools(tools []detect.Tool) {
	sort.SliceStable(tools, func(i, j int) bool {
		if tools[i].Bytes != tools[j].Bytes {
			return tools[i].Bytes > tools[j].Bytes
		}
		return tools[i].Path < tools[j].Path
	})
}
