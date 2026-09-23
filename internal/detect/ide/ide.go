// Package ide detects the editors, terminals and coding agents a developer
// machine accumulates, and tells their three kinds of directory apart.
//
// There is nothing to probe. None of these tools has a query interface worth
// shelling out to, and asking an editor where it keeps its cache would mean
// launching it. The evidence is the directories themselves, which is enough,
// because what a reader needs from this detector is not "where is VS Code"
// but "which of these four gigabytes can I delete".
//
// That is the distinction the tables below draw, and the reason this is a
// detector rather than a dozen catalog rules. Every Electron editor keeps the
// same three things side by side under the same parent: compiled caches it
// rebuilds on demand, extensions it reinstalls from a marketplace, and
// settings that exist nowhere else. A rule can say "~/Library/Application
// Support/Cursor is Cursor's"; only the per-editor knowledge here can say
// that CachedData inside it is regenerable and User beside it is not.
package ide

import (
	"context"
	"fmt"
	"path"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "ide"

func init() { detect.Register(210, New()) }

// Detector finds editors, terminals and coding agents.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are the directories that existed when the probe ran, recorded so that
// a cached scan can tell "this machine has no JetBrains IDE" from "nobody
// looked".
type Facts struct {
	Found []string `json:"found,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// editor is one editor built on Electron or Code-OSS, which is all of the
// ones here bar Zed. They share a directory layout, so they are described by
// what differs and expanded into the common shape by [editorTargets].
type editor struct {
	// owner is the display label.
	owner string
	// keys are the prefixed identifiers an application footprint joins on.
	keys []string
	// dot is the editor's dot-directory below the home, empty when it has
	// none. It holds the extensions and the CLI's own state.
	dot string
	// support is its directory below ~/Library/Application Support.
	support string
	// caches is its directory below ~/Library/Caches, empty when it keeps
	// its caches under Application Support instead.
	caches string
}

// editors are the Electron editors, in the order the report lists them.
//
// Cursor carries three keys because it arrived three ways: the cask token is
// how it was installed, the ToDesktop bundle id is what the .app actually
// declares, and "cursor" is the command-line shim. An application footprint
// that joined on only one of them would miss most of Cursor's bytes.
var editors = []editor{
	{
		owner:   "VS Code",
		keys:    []string{"app:com.microsoft.VSCode", "cask:visual-studio-code", "cli:code"},
		dot:     ".vscode",
		support: "Code",
	},
	{
		owner:   "Cursor",
		keys:    []string{"app:com.todesktop.230313mzl4w4u92", "cask:cursor", "cli:cursor"},
		dot:     ".cursor",
		support: "Cursor",
	},
	{
		owner:   "Antigravity",
		keys:    []string{"product:antigravity", "vendor:com.google.antigravity", "cli:antigravity"},
		dot:     ".antigravity",
		support: "Antigravity",
	},
	{
		owner:   "Windsurf",
		keys:    []string{"app:com.exafunction.windsurf", "cask:windsurf", "cli:windsurf"},
		dot:     ".windsurf",
		support: "Windsurf",
	},
}

// supportPart is one directory inside an editor's Application Support folder.
type supportPart struct {
	rel     string
	reclaim classify.Reclaim
	kind    string
	explain string
}

// supportParts are the directories every Electron editor keeps in the same
// place under the same names. The order is deliberate: the regenerable ones
// first, so a reader scanning the report meets the reclaimable bytes before
// the ones they must keep.
var supportParts = []supportPart{
	{"CachedData", classify.Regenerable, "cache",
		"compiled extension code, rebuilt the next time the extension runs"},
	{"CachedExtensionVSIXs", classify.Regenerable, "cache",
		"extension packages kept after they were installed; the marketplace still has them"},
	{"CachedProfilesData", classify.Regenerable, "cache",
		"per-profile compiled data, rebuilt on demand"},
	{"Cache", classify.Regenerable, "cache", "the embedded browser's HTTP cache"},
	{"Code Cache", classify.Regenerable, "cache", "compiled JavaScript, rebuilt on the next launch"},
	{"GPUCache", classify.Regenerable, "cache", "shader cache, rebuilt on the next launch"},
	{"DawnGraphiteCache", classify.Regenerable, "cache", "shader cache, rebuilt on the next launch"},
	{"DawnWebGPUCache", classify.Regenerable, "cache", "shader cache, rebuilt on the next launch"},
	{"logs", classify.Regenerable, "data", "logs from past sessions"},
	{"blob_storage", classify.Regenerable, "cache", "transient blobs from the embedded browser"},
	{"User/workspaceStorage", classify.Regenerable, "data",
		"per-workspace state; losing it costs window layouts and undo history, not code"},
	{"User/History", classify.Regenerable, "data", "local edit history for files outside version control"},
	{"User/globalStorage", classify.ToolManaged, "data",
		"extension state and databases; the extensions rebuild what they can"},
	{"User", classify.UserData, "data",
		"settings, keybindings and snippets, which exist nowhere else"},
}

// location is one directory that does not fit the Electron shape.
type location struct {
	// rel is the path below the home directory.
	rel      string
	owner    string
	keys     []string
	category string
	reclaim  classify.Reclaim
	// kind is the summary row's kind; an empty kind claims the path
	// without listing it as a tool.
	kind    string
	name    string
	explain string
}

// locations are the editors, terminals and agents with their own layouts.
//
// The coding agents are here rather than in a bucket of their own because
// that is what they are on disk: a configuration directory, a cache of
// downloaded runtimes, and a transcript of past sessions. ~/.claude is the
// CLI's, not the desktop application's, so it carries a cli: key and no
// bundle id — attributing it to Claude for Desktop would put a developer
// tool's bytes under an application the user may not even have.
var locations = []location{
	{
		rel: "Library/Application Support/Zed", owner: "Zed", keys: []string{"app:dev.zed.Zed", "cli:zed"},
		category: "Zed", reclaim: classify.Unknown, kind: "data",
		explain: "Zed's application data: language servers, extensions and project state",
	},
	{
		rel: "Library/Application Support/Zed/languages", owner: "Zed", keys: []string{"app:dev.zed.Zed", "cli:zed"},
		category: "Zed", reclaim: classify.ToolManaged, kind: "toolchain",
		explain: "language servers Zed downloaded; it downloads them again when a file needs one",
	},
	{
		rel: "Library/Caches/Zed", owner: "Zed", keys: []string{"app:dev.zed.Zed", "cli:zed"},
		category: "Zed", reclaim: classify.Regenerable, kind: "cache", explain: "Zed's cache",
	},
	{
		rel: "Library/Application Support/dev.warp.Warp-Stable", owner: "Warp",
		keys:     []string{"product:warp", "app:dev.warp.Warp-Stable"},
		category: "Warp", reclaim: classify.Unknown, kind: "data",
		explain: "Warp's application data, including its command history",
	},
	{
		rel: "Library/Caches/dev.warp.Warp-Stable", owner: "Warp",
		keys:     []string{"product:warp", "app:dev.warp.Warp-Stable"},
		category: "Warp", reclaim: classify.Regenerable, kind: "cache", explain: "Warp's cache",
	},
	{
		rel: "Library/Application Support/JetBrains", owner: "JetBrains", keys: []string{"vendor:com.jetbrains"},
		category: "JetBrains", reclaim: classify.Unknown, kind: "data",
		explain: "settings and plugins for every installed JetBrains IDE",
	},
	{
		rel: "Library/Application Support/JetBrains/Toolbox", owner: "JetBrains Toolbox",
		keys: []string{"vendor:com.jetbrains"}, category: "JetBrains", reclaim: classify.ToolManaged,
		kind:    "versions",
		explain: "IDE versions Toolbox installed; Toolbox removes the superseded ones itself",
	},
	{
		rel: "Library/Caches/JetBrains", owner: "JetBrains", keys: []string{"vendor:com.jetbrains"},
		category: "JetBrains", reclaim: classify.Regenerable, kind: "cache",
		explain: "project indexes, rebuilt the next time a project is opened",
	},
	{
		rel: "Library/Logs/JetBrains", owner: "JetBrains", keys: []string{"vendor:com.jetbrains"},
		category: "JetBrains", reclaim: classify.Regenerable, kind: "data", explain: "JetBrains IDE logs",
	},
	{
		rel: ".local/share/nvim", owner: "Neovim", keys: []string{"cli:nvim"},
		category: "Neovim", reclaim: classify.ToolManaged, kind: "data",
		explain: "plugins and the language servers Mason installed; the plugin manager reinstalls them",
	},
	{
		rel: ".cache/nvim", owner: "Neovim", keys: []string{"cli:nvim"},
		category: "Neovim", reclaim: classify.Regenerable, kind: "cache",
		explain: "Neovim's compiled Lua and shada cache",
	},
	{
		rel: ".claude", owner: "Claude Code", keys: []string{"cli:claude-code"},
		category: "Coding agents", reclaim: classify.Unknown, kind: "data",
		explain: "Claude Code's configuration, session transcripts and project state",
	},
	{
		rel: ".codex", owner: "Codex", keys: []string{"cli:codex", "cask:codex"},
		category: "Coding agents", reclaim: classify.Unknown, kind: "data",
		explain: "the Codex CLI's configuration and session history",
	},
	{
		rel: ".cache/codex-runtimes", owner: "Codex", keys: []string{"cli:codex", "cask:codex"},
		category: "Coding agents", reclaim: classify.Regenerable, kind: "cache",
		explain: "language runtimes Codex downloaded to run code in; it downloads them again on demand",
	},
	{
		rel: "Library/Application Support/Codex", owner: "Codex", keys: []string{"cli:codex", "cask:codex"},
		category: "Coding agents", reclaim: classify.Unknown, kind: "data",
		explain: "the Codex application's data",
	},
	{
		rel: ".gemini", owner: "Gemini CLI", keys: []string{"cli:gemini"},
		category: "Coding agents", reclaim: classify.Unknown, kind: "data",
		explain: "the Gemini CLI's configuration and session history",
	},
}

// Probe looks for the directories. There is nothing to run, so the whole
// probe is a few dozen lstat calls beside a walk that makes millions.
func (*Detector) Probe(_ context.Context, env detect.Env) (detect.Facts, error) {
	if env.Home == "" {
		return nil, detect.Missingf("no home directory to look in")
	}
	f := &Facts{}
	for _, ed := range editors {
		for _, rel := range []string{ed.dot, path.Join("Library/Application Support", ed.support)} {
			if rel != "" && env.Exists(path.Join(env.Home, rel)) {
				f.Found = append(f.Found, rel)
			}
		}
	}
	for _, loc := range locations {
		if env.Exists(path.Join(env.Home, loc.rel)) {
			f.Found = append(f.Found, loc.rel)
		}
	}
	if len(f.Found) == 0 {
		return nil, detect.Missingf("no editor, terminal or coding agent directory exists under %s", env.Home)
	}
	return f, nil
}

// Classify claims whichever of the directories the walk found.
//
// It works from the tables rather than from the facts on purpose. The facts
// say what existed when the probe ran; the tree says what the walk measured,
// and detect.Claims drops a target the walk never saw. Driving the claims
// from the tree means a cached scan re-classified against a newer catalog
// gets the newer tables, which is the whole reason facts and classification
// are separate.
func (*Detector) Classify(t *walk.Tree, _ detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}

	var targets []detect.Target
	for _, ed := range editors {
		targets = append(targets, editorTargets(home, ed)...)
	}
	for _, loc := range locations {
		name := loc.name
		if name == "" {
			name = loc.owner
		}
		targets = append(targets, detect.Target{
			Path: path.Join(home, loc.rel), Bucket: classify.BucketDeveloper,
			Category: loc.category, Owner: loc.owner, OwnerKeys: loc.keys,
			Reclaim: loc.reclaim, Explain: loc.explain, Kind: loc.kind, Name: name,
			Priority: priority,
		})
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// priority lifts this detector's claims above another detector's generic
// ones on the same node.
//
// Three directories are named by both this detector and cli-tools, which
// attributes anything under ~/.cache and ~/.local/share to a name taken from
// the directory itself: ~/.local/share/nvim, ~/.cache/nvim and
// ~/.cache/codex-runtimes. Both claims are detector claims over the same node
// at the same depth, so without this the resolver falls through to comparing
// the detector names and "cli-tools" wins for no better reason than the
// alphabet — labelling 1.8 GB of Neovim plugins "nvim" instead of "Neovim"
// and dropping the cli:nvim join key.
//
// A detector that names a directory outranks one that reads its name off the
// filesystem, so the tie is broken on purpose rather than by accident.
//
// cli-tools demotes its own generic expansions by the same amount in the
// other direction, which decides these three on its own. Both are kept: the
// demotion needs no opt-in from a future detector, and this side keeps
// working if the demotion is ever removed.
const priority = 1

// editorTargets expands one Electron editor into the directories it keeps.
func editorTargets(home string, ed editor) []detect.Target {
	out := make([]detect.Target, 0, len(supportParts)+3)
	add := func(rel string, reclaim classify.Reclaim, kind, name, explain string) {
		out = append(out, detect.Target{
			Path: path.Join(home, rel), Bucket: classify.BucketDeveloper,
			Category: ed.owner, Owner: ed.owner, OwnerKeys: ed.keys,
			Reclaim: reclaim, Explain: explain, Kind: kind, Name: name,
			Priority: priority,
		})
	}

	if ed.dot != "" {
		add(ed.dot, classify.ToolManaged, "data", ed.owner,
			fmt.Sprintf("%s's extensions and command-line state", ed.owner))
		add(path.Join(ed.dot, "extensions"), classify.ToolManaged, "data", ed.owner+" extensions",
			fmt.Sprintf("installed extensions; %s reinstalls them from its marketplace", ed.owner))
	}

	support := path.Join("Library/Application Support", ed.support)
	add(support, classify.Unknown, "data", ed.owner,
		fmt.Sprintf("%s's application data: caches, extension state and settings together", ed.owner))
	for _, p := range supportParts {
		add(path.Join(support, p.rel), p.reclaim, p.kind, ed.owner+" "+path.Base(p.rel), p.explain)
	}

	if ed.caches != "" {
		add(path.Join("Library/Caches", ed.caches), classify.Regenerable, "cache", ed.owner+" cache",
			fmt.Sprintf("%s's cache", ed.owner))
	}
	return out
}
