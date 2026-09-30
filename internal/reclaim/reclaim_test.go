package reclaim

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/walk"
)

var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func file(name string, bytes int64) *walk.Node {
	return &walk.Node{Name: name, Kind: walk.KindFile, Bytes: bytes, Apparent: bytes}
}

func dir(name string, children ...*walk.Node) *walk.Node {
	n := &walk.Node{Name: name, Kind: walk.KindDir}
	sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
	for _, c := range children {
		c.Parent = n
		n.Children = append(n.Children, c)
	}
	return n
}

// fill sums a hand-built tree and numbers it in preorder, as the walker does.
func fill(root *walk.Node) *walk.Tree {
	t := &walk.Tree{Root: root, Finished: now}
	var visit func(*walk.Node)
	visit = func(n *walk.Node) {
		n.ID = int32(len(t.Nodes))
		t.Nodes = append(t.Nodes, n)
		for _, c := range n.Children {
			visit(c)
			n.Bytes += c.Bytes
		}
	}
	visit(root)
	return t
}

const (
	home = "/Users/u"
	gb   = int64(1_000_000_000)
)

// fixture is a machine with one of everything the plan reads: nested
// detector rows with different tiers, a tool's own reported figure, a
// container runtime, a stale and an active project, an installed
// application's cache, an orphan with a kept directory, a config link and a
// Brewfile line, and a Trash with something in it.
func fixture(t *testing.T) *scan.Result {
	t.Helper()
	root := dir(mac.DataRoot,
		dir("Users", dir("u",
			dir(".npm", file("blob", 3*gb), dir("_npx", file("x", gb/2))),
			dir(".cursor", file("cli-config.json", 4096), dir("extensions", file("e", gb))),
			dir(".Trash", file("old.zip", 2*gb)),
			dir("Library",
				dir("pnpm", dir("store", dir("v10", file("f", 6*gb)), dir("v3", file("f", gb)))),
				dir("Caches", dir("com.spotify.client", file("d", 2*gb))),
				dir("Application Support", dir("Cursor", file("s", gb/4))),
			),
			dir("code",
				dir("old", dir("node_modules", file("m", gb))),
				dir("new", dir("node_modules", file("m", 2*gb))),
			),
		)),
	)
	tr := fill(root)
	at := func(display string) *walk.Node {
		n, ok := tr.Lookup(mac.ScanPath(display))
		if !ok {
			t.Fatalf("fixture has no %s", display)
		}
		return n
	}
	tool := func(display, name string, tier detect.Tier, reclaim classify.Reclaim, command string) detect.Tool {
		n := at(display)
		return detect.Tool{Name: name, Path: display, Node: n.ID, Bytes: n.Bytes, Reclaim: reclaim,
			Tier: tier, Command: command}
	}
	artifact := func(display string) detect.Tool {
		n := at(display)
		return detect.Tool{Name: "node_modules", Path: display, Node: n.ID, Bytes: n.Bytes,
			Reclaim: classify.Regenerable, Impact: "`pnpm install` restores it"}
	}

	res := &scan.Result{
		Tree: tr,
		Detectors: []detect.Status{
			{Name: "node"}, {Name: "homebrew"}, {Name: "orbstack"}, {Name: "projects"},
		},
		Summaries: map[string]detect.Summary{
			"node": {Tools: []detect.Tool{
				tool(home+"/.npm", "npm cache", detect.TierSafe, classify.Regenerable, "npm cache clean --force"),
				tool(home+"/.npm/_npx", "npx", detect.TierSafe, classify.Regenerable, ""),
				tool(home+"/Library/pnpm", "pnpm store root", detect.TierInUse, classify.ToolManaged, ""),
				tool(home+"/Library/pnpm/store/v10", "pnpm store v10", detect.TierInUse, classify.ToolManaged, "pnpm store prune"),
				tool(home+"/Library/pnpm/store/v3", "pnpm store v3", detect.TierUnset, classify.Regenerable, ""),
			}},
			"homebrew": {Reclaimable: 150_000_000, ReclaimNote: "`brew cleanup -n` would free 150 MB"},
			"orbstack": {Runtimes: []detect.Runtime{{Name: "OrbStack", GuestReported: []detect.Line{
				{Type: "Build Cache", Size: 5 * gb, Reclaimable: 4 * gb},
				{Type: "Local Volumes", Size: 5 * gb, Reclaimable: gb},
			}}}},
			"projects": {Projects: []detect.Project{
				{Root: home + "/code/old", Node: at(home + "/code/old").ID, LastActivity: now.Add(-90 * 24 * time.Hour),
					Artifacts: []detect.Tool{artifact(home + "/code/old/node_modules")}},
				{Root: home + "/code/new", Node: at(home + "/code/new").ID, LastActivity: now.Add(-2 * 24 * time.Hour),
					Artifacts: []detect.Tool{artifact(home + "/code/new/node_modules")}},
			}},
		},
		Apps: &apps.Report{
			Apps: []apps.Entry{{Label: "Spotify", State: "installed", Components: []apps.ComponentRef{
				{Path: home + "/Library/Caches/com.spotify.client", Bytes: 2 * gb,
					Bucket: classify.BucketAppData.ID(), Category: "Cache", Reclaim: classify.Regenerable.String()},
			}}},
			Orphans: []apps.Entry{{
				Label: "Cursor", State: "orphan-likely",
				Components: []apps.ComponentRef{
					{Path: home + "/.cursor", Kept: "cursor-agent reads it"},
					{Path: home + "/.cursor/extensions"},
					{Path: home + "/Library/Application Support/Cursor"},
				},
				ConfigLinks: []apps.ConfigLink{{Path: home + "/Library/Application Support/Cursor/User/settings.json",
					Target: home + "/dotfiles/cursor/settings.json"}},
				References: []apps.Reference{{File: home + "/dotfiles/Brewfile", Line: 211, Kind: "cask", Token: "cursor"}},
			}},
		},
		Ledger: &ledger.Ledger{
			Scanned:   ledger.Line{Bytes: tr.Root.Bytes, Known: true},
			Purgeable: ledger.Line{Bytes: gb, Known: true},
			Buckets:   []ledger.Bucket{{ID: classify.BucketTrash.ID(), Bytes: 2 * gb, Known: true}},
		},
	}
	for _, rt := range res.Summaries["orbstack"].Runtimes {
		for i := range rt.GuestReported {
			detect.PlanDockerRow(&rt.GuestReported[i], "orbstack")
		}
	}
	return res
}

func byPath(p *Plan, display string) (Item, bool) {
	for _, it := range p.Items {
		if it.Path == display && !it.Reported {
			return it, true
		}
	}
	return Item{}, false
}

func byID(p *Plan, id string) (Item, bool) {
	for _, it := range p.Items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

// TestEachByteIsCountedOnce is the plan's first invariant. The npm cache
// holds the npx cache; the pnpm store root holds its live and superseded
// generations. Each byte is given to the deepest item that holds it, so the
// walked totals add up to no more than the walk saw.
func TestEachByteIsCountedOnce(t *testing.T) {
	t.Parallel()
	res := fixture(t)
	p := Build(res, Options{})

	for display, want := range map[string]int64{
		home + "/.npm":                   3 * gb,
		home + "/.npm/_npx":              gb / 2,
		home + "/Library/pnpm/store/v10": 6 * gb,
		home + "/Library/pnpm/store/v3":  gb,
	} {
		it, ok := byPath(p, display)
		if !ok {
			t.Errorf("no item for %s", display)
			continue
		}
		if it.Bytes != want {
			t.Errorf("%s bytes = %d, want %d", display, it.Bytes, want)
		}
	}

	// The store root holds nothing of its own once its generations are
	// items, so it is not listed.
	if it, ok := byPath(p, home+"/Library/pnpm"); ok {
		t.Errorf("the pnpm store root is listed with %d bytes of its own", it.Bytes)
	}

	var walked int64
	nodes := map[int32]string{}
	for _, it := range p.Items {
		if it.Reported {
			continue
		}
		walked += it.Bytes
		if it.Node >= 0 {
			if prev, dup := nodes[it.Node]; dup {
				t.Errorf("node %d is in two items: %s and %s", it.Node, prev, it.ID)
			}
			nodes[it.Node] = it.ID
		}
	}
	if walked > res.Ledger.Scanned.Bytes {
		t.Errorf("walked items total %d, more than the %d the walk saw", walked, res.Ledger.Scanned.Bytes)
	}
}

// TestTiers covers where each kind of item lands.
func TestTiers(t *testing.T) {
	t.Parallel()
	p := Build(fixture(t), Options{})
	for display, want := range map[string]Tier{
		home + "/.npm":                               Safe,
		home + "/Library/pnpm/store/v10":             InUse,
		home + "/Library/pnpm/store/v3":              Redownload, // unset → derived, never Safe
		home + "/code/old":                           Reinstall,  // untouched for 90 days
		home + "/code/new":                           InUse,      // used two days ago
		home + "/Library/Caches/com.spotify.client":  Redownload,
		home + "/.cursor":                            Never, // cursor-agent reads it
		home + "/.cursor/extensions":                 Check,
		home + "/Library/Application Support/Cursor": Check,
		home + "/.Trash":                             Check,
	} {
		it, ok := byPath(p, display)
		if !ok {
			t.Errorf("no item for %s", display)
			continue
		}
		if it.Tier != want {
			t.Errorf("%s tier = %s, want %s", display, it.Tier, want)
		}
	}
	vol, ok := byID(p, "orbstack:df:Local Volumes")
	if !ok || vol.Tier != Never || !vol.Reported {
		t.Errorf("docker volumes = %+v, want a reported item that is never suggested", vol)
	}
}

// TestReportedFiguresStayApart: brew's own figure and docker's overlap the
// walked directories, so they have their own column and never reach the
// walked totals.
func TestReportedFiguresStayApart(t *testing.T) {
	t.Parallel()
	p := Build(fixture(t), Options{})
	safe := p.Totals[Safe]
	if safe.Reported != 150_000_000+4*gb {
		t.Errorf("safe reported = %d, want brew's 150 MB and the 4 GB build cache", safe.Reported)
	}
	if safe.Walked != 3*gb+gb/2 {
		t.Errorf("safe walked = %d, want the npm cache and npx only", safe.Walked)
	}
}

// TestStaleAfterMovesProjects: the threshold is the user's, and a project
// used two days ago becomes reinstall-tier when the threshold is a day.
func TestStaleAfterMovesProjects(t *testing.T) {
	t.Parallel()
	p := Build(fixture(t), Options{StaleAfter: 24 * time.Hour})
	if it, _ := byPath(p, home+"/code/new"); it.Tier != Reinstall {
		t.Errorf("with a one-day threshold the active project is %s, want reinstall", it.Tier)
	}
}

// TestMovingToTheTrashFreesNothingYet: an item with no command of its own is
// moved to the Trash, and the plan says the space only comes back once the
// Trash is emptied.
func TestMovingToTheTrashFreesNothingYet(t *testing.T) {
	t.Parallel()
	p := Build(fixture(t), Options{})
	v3, _ := byPath(p, home+"/Library/pnpm/store/v3")
	if v3.Action != moveToTrash || !v3.AfterTrash {
		t.Errorf("v3 = %+v, want a move-to-Trash action freed after emptying", v3)
	}
	npm, _ := byPath(p, home+"/.npm")
	if npm.AfterTrash {
		t.Error("an item with its tool's own command was marked as freed only after the Trash")
	}
	if p.Totals[Redownload].AfterTrash == 0 {
		t.Error("the re-download tier has no after-Trash total")
	}
}

// TestOrphansBringTheirEdits: the Brewfile line that would reinstall a
// removed application is part of the plan, and the user's configuration
// link is listed as never to remove.
func TestOrphansBringTheirEdits(t *testing.T) {
	t.Parallel()
	p := Build(fixture(t), Options{})
	if len(p.Edits) != 1 || p.Edits[0].Line != 211 || !strings.Contains(p.Edits[0].Why, "brew bundle") {
		t.Errorf("edits = %+v, want the cask line with why", p.Edits)
	}
	link, ok := byID(p, "apps:"+home+"/Library/Application Support/Cursor/User/settings.json")
	if !ok || link.Tier != Never {
		t.Errorf("config link = %+v, want never", link)
	}
}

// TestUnreadableTrashIsANoteNotAZero: without Full Disk Access the Trash
// is unknown, and the plan says so instead of offering nothing.
func TestUnreadableTrashIsANoteNotAZero(t *testing.T) {
	t.Parallel()
	res := fixture(t)
	res.Ledger.Buckets[0].Known = false
	p := Build(res, Options{})
	if p.Trash.Known || !strings.Contains(p.Trash.Note, "Full Disk Access") {
		t.Errorf("trash = %+v, want unknown with a Full Disk Access note", p.Trash)
	}
}

func TestEmptyScanGivesAnEmptyPlan(t *testing.T) {
	t.Parallel()
	if p := Build(nil, Options{}); len(p.Items) != 0 {
		t.Errorf("a nil scan planned %d items", len(p.Items))
	}
}
