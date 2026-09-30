package apps

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/walk"
)

// symlink creates a link and its parent directory.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestProbeFindsConfigLinksAndReferences is the stow layout of the reference
// machine: shell files and editor settings are links into ~/dotfiles, and the
// Brewfiles live one directory down inside it. Removing Cursor left
// `cask "cursor"` in two Brewfiles and an Antigravity PATH line in .zshrc,
// and storix said nothing about either.
func TestProbeFindsConfigLinksAndReferences(t *testing.T) {
	t.Parallel()
	pf := newProbeFixture(t)
	home := pf.home
	writeFile(t, home+"/dotfiles/zsh/.zshrc", []byte("# a comment naming $HOME/.ignored\nexport PATH=\"$HOME/.antigravity/antigravity/bin:$PATH\"\n"))
	writeFile(t, home+"/dotfiles/_brew_pro/Brewfile", []byte("brew \"git\"\ncask \"cursor\"\n# cask \"old\"\ncask 'mattermost'\n"))
	writeFile(t, home+"/dotfiles/cursor/settings.json", []byte("{}"))
	if err := os.MkdirAll(home+"/dotfiles/warp/.warp", 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, "dotfiles/zsh/.zshrc", home+"/.zshrc")
	symlink(t, "dotfiles/warp/.warp", home+"/.warp")
	symlink(t, "../../../../dotfiles/cursor/settings.json",
		home+"/Library/Application Support/Cursor/User/settings.json")

	p := pf.prober()
	p.env.Readlink = os.Readlink
	p.configLinks()
	p.references()

	links := map[string]string{}
	for _, l := range p.f.ConfigLinks {
		links[l.Path] = l.Target
	}
	for link, target := range map[string]string{
		home + "/.warp":  home + "/dotfiles/warp/.warp",
		home + "/.zshrc": home + "/dotfiles/zsh/.zshrc",
		home + "/Library/Application Support/Cursor/User/settings.json": home + "/dotfiles/cursor/settings.json",
	} {
		if links[link] != target {
			t.Errorf("link %s → %q, want %q (all: %v)", link, links[link], target, links)
		}
	}

	var got []string
	for _, r := range p.f.References {
		got = append(got, r.Kind+":"+r.Token+"@"+filepath.Base(r.File))
	}
	for _, want := range []string{"cask:cursor@Brewfile", "cask:mattermost@Brewfile", "brew:git@Brewfile", "shell:/.antigravity@.zshrc"} {
		if !slices.Contains(got, want) {
			t.Errorf("reference %s not found in %v", want, got)
		}
	}
	for _, r := range p.f.References {
		if r.Token == "old" || r.Token == "/.ignored" {
			t.Errorf("a commented-out line was read as a reference: %+v", r)
		}
	}
}

// cursorFixture is Cursor with its editor removed: the editor's support
// folder, and ~/.cursor holding both the CLI's config and the editor's
// extensions.
func cursorFixture(t *testing.T) *verdictFixture {
	return newVerdictFixture(t,
		"Users/andrewsam/Library/Application Support/Cursor",
		"Users/andrewsam/.cursor",
		"Users/andrewsam/.cursor/extensions",
		"Users/andrewsam/Library/Application Support/dev.warp.Warp-Stable")
}

// TestVerdictNamesWhatStillUsesOrReinstallsAnOrphan covers items the cleanup
// of the reference machine turned up by hand: a CLI that still reads part of
// the data, the user's own configuration linked in, and Brewfile and shell
// lines that would bring the application back.
func TestVerdictNamesWhatStillUsesOrReinstallsAnOrphan(t *testing.T) {
	t.Parallel()
	vf := cursorFixture(t)
	facts := &Facts{
		CLIs:        []FoundCLI{{Name: "cursor-agent", Path: vf.home + "/.local/bin/cursor-agent"}},
		ConfigLinks: []ConfigLink{{Path: vf.home + "/.warp", Target: vf.home + "/dotfiles/warp/.warp"}},
		References: []Reference{
			{File: vf.home + "/dotfiles/_brew_pro/Brewfile", Line: 211, Kind: "cask", Token: "cursor"},
			{File: vf.home + "/.zshrc", Line: 12, Kind: "shell", Token: "/.warp", Text: `source "$HOME/.warp/x"`},
			{File: vf.home + "/dotfiles/_brew_pro/Brewfile", Line: 3, Kind: "cask", Token: "zed"},
		},
	}
	a := vf.analyze(t, facts, Options{})

	cursor := verdictByLabel(t, a, "Cursor")
	if cursor.State != StateOrphanLikely {
		t.Fatalf("Cursor state = %v, want orphan-likely: the editor is gone", cursor.State)
	}
	if len(cursor.Protected) != 1 || cursor.Protected[0].Path != vf.home+"/.cursor" {
		t.Errorf("Cursor protected = %+v, want ~/.cursor kept for cursor-agent", cursor.Protected)
	}
	ev := strings.Join(cursor.Evidence, "\n")
	for _, want := range []string{"cursor-agent command-line tool is still installed", "Brewfile:211 still lists cask \"cursor\""} {
		if !strings.Contains(ev, want) {
			t.Errorf("Cursor evidence lacks %q:\n%s", want, ev)
		}
	}
	if strings.Contains(ev, "zed") {
		t.Errorf("another cask's Brewfile line was attached to Cursor:\n%s", ev)
	}

	warp := verdictByLabel(t, a, "Warp")
	if len(warp.Links) != 1 || !strings.Contains(strings.Join(warp.Evidence, "\n"), "your own configuration") {
		t.Errorf("Warp links = %+v, evidence %v; want ~/.warp named as the user's configuration", warp.Links, warp.Evidence)
	}
	if len(warp.References) != 1 {
		t.Errorf("Warp references = %+v, want the .zshrc line", warp.References)
	}
}

// TestRefineOrphansAnEditorsDataUnlessACLIReadsIt is item three: the ide
// detector's claims on a removed editor's directories said tool-managed or
// unknown, so the ledger showed 2.5 GB of leftovers as in use. They now say
// orphaned — except ~/.cursor, which cursor-agent reads, and except claims a
// container or toolchain detector made.
func TestRefineOrphansAnEditorsDataUnlessACLIReadsIt(t *testing.T) {
	t.Parallel()
	for _, withCLI := range []bool{true, false} {
		vf := cursorFixture(t)
		facts := &Facts{}
		if withCLI {
			facts.CLIs = []FoundCLI{{Name: "cursor-agent", Path: vf.home + "/.local/bin/cursor-agent"}}
		}
		keys := []string{"app:com.todesktop.230313mzl4w4u92", "cask:cursor", "cli:cursor"}
		claim := func(rel, detector string, reclaim classify.Reclaim) classify.Claim {
			n, ok := lookupDisplay(vf.tree, vf.home+rel)
			if !ok {
				t.Fatalf("fixture has no %s", rel)
			}
			return classify.Claim{Node: n, Bucket: classify.BucketDeveloper, Owner: "Cursor", OwnerKeys: keys,
				Reclaim: reclaim, Evidence: []string{"shared"},
				Source: classify.Source{Kind: classify.SourceDetector, ID: detector, Detector: detector}}
		}
		claims := []classify.Claim{
			claim("/.cursor", "ide", classify.ToolManaged),
			claim("/.cursor/extensions", "ide", classify.ToolManaged),
			claim("/Library/Application Support/Cursor", "ide", classify.Unknown),
			claim("/Library/Application Support/Cursor", "docker", classify.ToolManaged),
		}
		d := &Detector{Opts: Options{Root: vf.root, Now: time.Now().Add(365 * 24 * time.Hour)}}
		got := d.Refine(vf.tree, facts, classify.Context{Home: vf.home}, claims)

		wantDot := classify.ToolManaged
		if !withCLI {
			wantDot = classify.Orphaned
		}
		for i, want := range []classify.Reclaim{wantDot, classify.Orphaned, classify.Orphaned, classify.ToolManaged} {
			if got[i].Reclaim != want {
				t.Errorf("cli=%v: %s (%s) reclaim = %s, want %s", withCLI,
					got[i].Node.Display(), got[i].Source.ID, got[i].Reclaim, want)
			}
		}
		if withCLI && !strings.Contains(strings.Join(got[0].Evidence, "\n"), "kept: the cursor-agent") {
			t.Errorf("~/.cursor does not say why it is kept: %v", got[0].Evidence)
		}
		if got[3].Evidence[0] != "shared" || len(got[3].Evidence) != 1 {
			t.Errorf("a claim Refine left alone had its evidence changed: %v", got[3].Evidence)
		}
	}
}

// TestRefineNeverOrphansASymlink: the link is how the user's configuration
// reaches the application, and removing it frees nothing.
func TestRefineNeverOrphansASymlink(t *testing.T) {
	t.Parallel()
	vf := cursorFixture(t)
	n, _ := lookupDisplay(vf.tree, vf.home+"/.cursor")
	link := &walk.Node{Name: "settings.json", Parent: n, Kind: walk.KindSymlink}
	claims := []classify.Claim{{Node: link, OwnerKeys: []string{"product:cursor"}, Reclaim: classify.UserData,
		Source: classify.Source{Kind: classify.SourceDetector, ID: "ide"}}}
	d := &Detector{Opts: Options{Root: vf.root, Now: time.Now().Add(365 * 24 * time.Hour)}}
	got := d.Refine(vf.tree, &Facts{}, classify.Context{Home: vf.home}, claims)
	if got[0].Reclaim != classify.UserData {
		t.Errorf("a symlink was tagged %s", got[0].Reclaim)
	}
}
