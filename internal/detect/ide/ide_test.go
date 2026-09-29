package ide_test

import (
	"errors"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/ide"
)

const home = detecttest.Home

// corpus mirrors what this machine has: four editors, a terminal, Neovim and
// three coding agents.
func corpus() map[string]int64 {
	return map[string]int64{
		home + "/.vscode/extensions/ms-python/x":                                  400_000,
		home + "/Library/Application Support/Code/CachedData/abc/x":               300_000,
		home + "/Library/Application Support/Code/CachedExtensionVSIXs/x":         250_000,
		home + "/Library/Application Support/Code/User/workspaceStorage/aaa/x":    200_000,
		home + "/Library/Application Support/Code/User/settings.json":             150_000,
		home + "/Library/Application Support/Code/User/globalStorage/state.vscdb": 120_000,
		home + "/Library/Application Support/Code/logs/20260101/x":                100_000,
		home + "/Library/Application Support/Code/GPUCache/x":                     90_000,
		home + "/.cursor/extensions/x":                                            80_000,
		home + "/Library/Application Support/Cursor/CachedData/x":                 70_000,
		home + "/.antigravity/extensions/x":                                       60_000,
		home + "/Library/Application Support/Antigravity/CachedData/x":            50_000,
		home + "/Library/Application Support/Zed/languages/rust-analyzer/x":       40_000,
		home + "/Library/Caches/Zed/x":                                            30_000,
		home + "/Library/Application Support/dev.warp.Warp-Stable/x":              25_000,
		home + "/Library/Application Support/JetBrains/Toolbox/apps/x":            24_000,
		home + "/Library/Caches/JetBrains/IntelliJ/index/x":                       23_000,
		home + "/.local/share/nvim/mason/packages/x":                              22_000,
		home + "/.cache/nvim/luac/x":                                              21_000,
		home + "/.claude/projects/x":                                              20_000,
		home + "/.codex/sessions/x":                                               19_000,
		home + "/.cache/codex-runtimes/node/x":                                    18_000,
		home + "/Library/Application Support/Codex/x":                             17_000,
		home + "/.gemini/x":                                                       16_000,
	}
}

// TestNothingInstalled: a machine with no editor at all is Missing, which is
// an answer.
func TestNothingInstalled(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, ide.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	claims, sum := ide.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims on a machine with no editors", len(claims))
	}
}

// TestProbeFindsTheDirectories: there is nothing to run, so the probe is the
// directory listing and the facts are what existed.
func TestProbeFindsTheDirectories(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, err := detecttest.Probe(t, ide.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*ide.Facts)
	want := map[string]bool{
		".vscode": true, "Library/Application Support/Code": true,
		".cursor": true, "Library/Application Support/Cursor": true,
		".antigravity": true, "Library/Application Support/Antigravity": true,
		".claude": true, ".codex": true, ".gemini": true,
		".local/share/nvim": true, ".cache/nvim": true, ".cache/codex-runtimes": true,
	}
	found := map[string]bool{}
	for _, rel := range got.Found {
		found[rel] = true
	}
	for rel := range want {
		if !found[rel] {
			t.Errorf("%s exists but the probe did not find it", rel)
		}
	}
}

// TestReclaimTags is the distinction this detector exists for: the cache, the
// extensions and the settings sit side by side and are three different things.
func TestReclaimTags(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, err := detecttest.Probe(t, ide.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := ide.New().Classify(f.Tree, facts, f.Context)

	want := []struct {
		path    string
		owner   string
		key     string
		reclaim classify.Reclaim
	}{
		{home + "/Library/Application Support/Code/CachedData", "VS Code", "app:com.microsoft.VSCode", classify.Regenerable},
		{home + "/Library/Application Support/Code/CachedExtensionVSIXs", "VS Code", "app:com.microsoft.VSCode", classify.Regenerable},
		{home + "/Library/Application Support/Code/User/workspaceStorage", "VS Code", "app:com.microsoft.VSCode", classify.Regenerable},
		{home + "/Library/Application Support/Code/logs", "VS Code", "app:com.microsoft.VSCode", classify.Regenerable},
		{home + "/Library/Application Support/Code/GPUCache", "VS Code", "app:com.microsoft.VSCode", classify.Regenerable},
		{home + "/Library/Application Support/Code/User", "VS Code", "app:com.microsoft.VSCode", classify.UserData},
		{home + "/Library/Application Support/Code/User/globalStorage", "VS Code", "cli:code", classify.ToolManaged},
		{home + "/.vscode/extensions", "VS Code", "app:com.microsoft.VSCode", classify.ToolManaged},
		{home + "/.cursor/extensions", "Cursor", "app:com.todesktop.230313mzl4w4u92", classify.ToolManaged},
		{home + "/.cursor", "Cursor", "cask:cursor", classify.ToolManaged},
		{home + "/Library/Application Support/Cursor/CachedData", "Cursor", "cask:cursor", classify.Regenerable},
		{home + "/.antigravity", "Antigravity", "product:antigravity", classify.ToolManaged},
		{home + "/Library/Application Support/Zed/languages", "Zed", "app:dev.zed.Zed", classify.ToolManaged},
		{home + "/Library/Caches/Zed", "Zed", "app:dev.zed.Zed", classify.Regenerable},
		{home + "/Library/Application Support/dev.warp.Warp-Stable", "Warp", "product:warp", classify.Unknown},
		{home + "/Library/Application Support/JetBrains/Toolbox", "JetBrains Toolbox", "vendor:com.jetbrains", classify.ToolManaged},
		{home + "/Library/Caches/JetBrains", "JetBrains", "vendor:com.jetbrains", classify.Regenerable},
		{home + "/.local/share/nvim", "Neovim", "cli:nvim", classify.ToolManaged},
		{home + "/.cache/nvim", "Neovim", "cli:nvim", classify.Regenerable},
		{home + "/.claude", "Claude Code", "cli:claude-code", classify.Unknown},
		{home + "/.codex", "Codex", "cli:codex", classify.Unknown},
		{home + "/.cache/codex-runtimes", "Codex", "cli:codex", classify.Regenerable},
		{home + "/.gemini", "Gemini CLI", "cli:gemini", classify.Unknown},
	}
	for _, w := range want {
		c, ok := detecttest.ClaimAt(claims, w.path)
		if !ok {
			t.Errorf("no claim at %s", w.path)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", w.path, c.Bucket)
		}
		if c.Owner != w.owner {
			t.Errorf("%s owner = %q, want %q", w.path, c.Owner, w.owner)
		}
		if c.Reclaim != w.reclaim {
			t.Errorf("%s reclaim = %s, want %s", w.path, c.Reclaim, w.reclaim)
		}
		if !detecttest.HasKey(c, w.key) {
			t.Errorf("%s keys = %v, want %q among them", w.path, c.OwnerKeys, w.key)
		}
		if c.Source.Detector != "ide" {
			t.Errorf("%s source = %s", w.path, c.Source)
		}
	}
	if _, ok := detecttest.Tool(sum, home+"/.vscode/extensions"); !ok {
		t.Error("the extensions directory has no summary row")
	}
}

// TestSettingsAreNeverReclaimable is worth its own assertion: an editor's User
// directory is the one thing in it that exists nowhere else.
func TestSettingsAreNeverReclaimable(t *testing.T) {
	f := detecttest.Build(t, corpus())
	claims, _ := ide.New().Classify(f.Tree, &ide.Facts{}, f.Context)
	c, ok := detecttest.ClaimAt(claims, home+"/Library/Application Support/Code/User")
	if !ok {
		t.Fatal("the User directory was not claimed")
	}
	if c.Reclaim.Reclaimable() {
		t.Errorf("VS Code's settings are tagged %s, which is reclaimable", c.Reclaim)
	}
}

// TestNamedDirectoriesOutrankGenericOnes: three of these directories are also
// claimed by cli-tools, which names them from the directory itself. Both are
// detector claims over the same node at the same depth, so the priority is
// what keeps the specific answer.
func TestNamedDirectoriesOutrankGenericOnes(t *testing.T) {
	f := detecttest.Build(t, corpus())
	claims, _ := ide.New().Classify(f.Tree, &ide.Facts{}, f.Context)
	for _, p := range []string{
		home + "/.local/share/nvim", home + "/.cache/nvim", home + "/.cache/codex-runtimes",
	} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Priority <= 0 {
			t.Errorf("%s has priority %d, so cli-tools wins the tie on the alphabet", p, c.Priority)
		}
	}
}

// TestClassifyWithoutAHome: no home is nothing to say.
func TestClassifyWithoutAHome(t *testing.T) {
	f := detecttest.Build(t, corpus())
	claims, sum := ide.New().Classify(f.Tree, &ide.Facts{}, classify.Context{})
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims with no home", len(claims))
	}
}
