package catalog_test

import (
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify/catalog"
)

// ruleCase is the positive and negative paths for one rule.
type ruleCase struct {
	pos []string
	neg []string
}

// sample is the name substituted for a capture or a "*" when a positive path
// is derived from a pattern. It is deliberately unlike any real directory so
// that a derived path cannot accidentally hit a more specific rule.
const sample = "xsample"

// ruleCases returns a case for every rule in the catalog.
//
// Most cases are derived: the positive path is the rule's own pattern with its
// captures filled in, and the negative is its parent directory, which must
// belong to some other rule or to none. That is not circular — the assertion
// is that the path resolves to *this* rule rather than to a neighbour, which
// is exactly the specificity ordering a tuning change breaks. The explicit
// table below overrides the derivation wherever two rules compete for the same
// shape of path, which is where a hand-written case earns its keep.
func ruleCases() map[string]ruleCase {
	out := make(map[string]ruleCase, len(catalog.Rules()))
	for _, r := range catalog.Rules() {
		p := derive(r.Match)
		out[r.ID] = ruleCase{pos: []string{p}, neg: []string{parentOf(p)}}
	}
	for id, c := range explicitCases {
		base := out[id]
		if len(c.pos) > 0 {
			base.pos = c.pos
		}
		if len(c.neg) > 0 {
			base.neg = c.neg
		}
		out[id] = base
	}
	return out
}

// derive turns a pattern into a concrete display path.
func derive(match string) string {
	if match == "~" {
		return testHome
	}
	p := strings.Replace(match, "~/", testHome+"/", 1)
	var segs []string
	for _, seg := range strings.Split(strings.Trim(p, "/"), "/") {
		segs = append(segs, fill(seg))
	}
	return "/" + path.Join(segs...)
}

// fill replaces the captures and wildcards in one segment.
func fill(seg string) string {
	if seg == "*" {
		return sample
	}
	open := strings.IndexByte(seg, '{')
	if open < 0 {
		return strings.ReplaceAll(seg, "*", sample)
	}
	closeAt := strings.IndexByte(seg, '}')
	return seg[:open] + sample + seg[closeAt+1:]
}

// parentOf is the parent directory of a derived path, or the path itself when
// it is already at the root, in which case the case table names a negative.
func parentOf(p string) string {
	d := path.Dir(p)
	if d == "/" || d == "." {
		return "/nowhere-in-particular"
	}
	return d
}

// explicitCases are the rules whose neighbours compete for the same paths.
// Each one names a path the rule must take and a path it must leave alone.
var explicitCases = map[string]ruleCase{
	// Home: a plain name is personal, a dot-name is a tool's, and the Trash
	// is the Trash even though all three patterns are equally specific.
	"personal.home.other": {
		pos: []string{testHome + "/Screenshots"},
		neg: []string{testHome + "/.cache", testHome + "/Library", testHome + "/code", testHome + "/.Trash"},
	},
	"dev.dotdir.generic": {
		pos: []string{testHome + "/.somecli"},
		neg: []string{testHome + "/.Trash", testHome + "/.cargo", testHome + "/Documents"},
	},
	"trash.user.home": {
		pos: []string{testHome + "/.Trash"},
		neg: []string{testHome + "/.cache", "/.Trashes"},
	},

	// Caches: a bundle id, an Apple bundle id and the two updater shapes.
	"cache.user.bundle": {
		pos: []string{testHome + "/Library/Caches/com.spotify.client"},
		neg: []string{
			testHome + "/Library/Caches/com.apple.Safari",
			testHome + "/Library/Caches/com.bitwarden.desktop.ShipIt",
			testHome + "/Library/Caches/notion-updater",
			testHome + "/Library/Caches/go-build",
		},
	},
	"cache.apple.bundle": {
		pos: []string{testHome + "/Library/Caches/com.apple.Safari"},
		neg: []string{testHome + "/Library/Caches/com.spotify.client"},
	},
	"cache.updater.shipit": {
		pos: []string{testHome + "/Library/Caches/com.bitwarden.desktop.ShipIt"},
		neg: []string{testHome + "/Library/Caches/com.bitwarden.desktop"},
	},
	"cache.updater.suffix": {
		pos: []string{
			testHome + "/Library/Caches/lens-desktop-updater",
			testHome + "/Library/Caches/notion-updater",
		},
		neg: []string{testHome + "/Library/Caches/lens-desktop"},
	},

	// Library trees: the catch-all must leave every named subtree alone.
	"appdata.library.other": {
		pos: []string{testHome + "/Library/Accounts"},
		neg: []string{
			testHome + "/Library/Caches",
			testHome + "/Library/Mail",
			testHome + "/Library/Developer",
			testHome + "/Library/pnpm",
		},
	},
	"appdata.appsupport.name": {
		pos: []string{testHome + "/Library/Application Support/Spotify"},
		neg: []string{
			testHome + "/Library/Application Support/Code",
			testHome + "/Library/Application Support/MobileSync",
			testHome + "/Library/Application Support/com.apple.spotlight",
			testHome + "/Library/Application Support/OrbStack",
		},
	},
	"appdata.groups.name": {
		pos: []string{testHome + "/Library/Group Containers/group.com.whatsapp.family"},
		neg: []string{
			testHome + "/Library/Group Containers/HUAQ24HBR6.dev.orbstack",
			testHome + "/Library/Group Containers/group.com.apple.notes",
		},
	},
	"appdata.containers.bundle": {
		pos: []string{testHome + "/Library/Containers/com.docker.docker.helper"},
		neg: []string{
			testHome + "/Library/Containers/com.apple.Notes",
			testHome + "/Library/Containers/com.docker.docker",
			testHome + "/Library/Containers/com.utmapp.UTM",
		},
	},

	// Developer paths that share a parent with a generic rule.
	"dev.cli.cache-owner": {
		pos: []string{testHome + "/.cache/some-tool"},
		neg: []string{testHome + "/.cache/uv", testHome + "/.cache/nvim", testHome + "/.cache/huggingface"},
	},
	"dev.cli.local-share": {
		pos: []string{testHome + "/.local/share/something"},
		neg: []string{testHome + "/.local/share/nvim", testHome + "/.local/share/fnm", testHome + "/.local/bin"},
	},
	"dev.cli.config-owner": {
		pos: []string{testHome + "/.config/starship"},
		neg: []string{testHome + "/.config/containers"},
	},
	"dev.homebrew.other": {
		pos: []string{"/opt/homebrew/share"},
		neg: []string{"/opt/homebrew/Cellar", "/opt/homebrew/Caskroom"},
	},
	"dev.homebrew.formula": {
		pos: []string{"/opt/homebrew/Cellar/ffmpeg"},
		neg: []string{"/opt/homebrew/Cellar"},
	},
	"sys.opt.other": {
		pos: []string{"/opt/something"},
		neg: []string{"/opt/homebrew"},
	},
	"dev.local.other": {
		pos: []string{"/usr/local/share"},
		neg: []string{"/usr/local/Cellar", "/usr/local/Homebrew", "/usr/local/Caskroom"},
	},

	// Applications: bundles, vendor folders and the macOS installer all sit
	// directly under /Applications.
	"apps.system.bundle": {
		pos: []string{"/Applications/Arc.app", "/Applications/Xcode.app"},
		neg: []string{"/Applications/Install macOS Tahoe.app", "/Applications/Autodesk"},
	},
	"apps.system.vendor": {
		pos: []string{"/Applications/Autodesk"},
		neg: []string{"/Applications/Arc.app", "/Applications/Utilities", "/Applications/Setapp"},
	},
	"apps.system.installer": {
		pos: []string{"/Applications/Install macOS Tahoe.app"},
		neg: []string{"/Applications/Arc.app"},
	},
	"apps.cask.token": {
		pos: []string{"/opt/homebrew/Caskroom/cursor"},
		neg: []string{"/opt/homebrew/Caskroom"},
	},

	// System paths whose parents are also claimed.
	"sys.library.other": {
		pos: []string{"/Library/Frameworks"},
		neg: []string{"/Library/Caches", "/Library/Developer", "/Library/Application Support", "/Library/Updates"},
	},
	"sys.var.other": {
		pos: []string{"/private/var/networkd"},
		neg: []string{"/private/var/db", "/private/var/folders", "/private/var/log", "/private/var/tmp"},
	},
	"sys.library.cache-owner": {
		pos: []string{"/Library/Caches/com.apple.iconservices.store"},
		neg: []string{"/Library/Caches"},
	},
	"sys.usr.other": {
		pos: []string{"/usr/share"},
		neg: []string{"/usr/local"},
	},

	// Backups and Xcode share ~/Library/Developer and Application Support.
	"bak.xcode.archives": {
		pos: []string{testHome + "/Library/Developer/Xcode/Archives"},
		neg: []string{testHome + "/Library/Developer/Xcode/DerivedData"},
	},
	"bak.mobilesync.root": {
		pos: []string{testHome + "/Library/Application Support/MobileSync"},
		neg: []string{testHome + "/Library/Application Support/Spotify"},
	},
	"dev.xcode.developer": {
		pos: []string{testHome + "/Library/Developer"},
		neg: []string{testHome + "/Library/Developer/CoreSimulator", testHome + "/Library"},
	},

	// Containers: the OrbStack group container must beat the generic one.
	"ctr.orbstack.group": {
		pos: []string{testHome + "/Library/Group Containers/HUAQ24HBR6.dev.orbstack"},
		neg: []string{testHome + "/Library/Group Containers/group.com.docker"},
	},
	"ctr.kube.cache": {
		pos: []string{testHome + "/.kube/cache"},
		neg: []string{testHome + "/.kube"},
	},
	"ctr.podman.config": {
		pos: []string{testHome + "/.config/containers"},
		neg: []string{testHome + "/.config/starship"},
	},

	// Rules whose derived parent is the volume root.
	"personal.users.root": {neg: []string{"/Applications"}},
	"apps.system.root":    {neg: []string{"/Library"}},
	"sys.library.root":    {neg: []string{"/Users"}},
	"sys.usr.root":        {neg: []string{"/opt"}},
	"sys.opt.root":        {neg: []string{"/usr"}},
	"dev.nix.store":       {neg: []string{"/opt"}},
	"sys.meta.spotlight":  {neg: []string{"/.fseventsd"}},
	"sys.meta.fseventsd":  {neg: []string{"/.Spotlight-V100"}},
	"sys.meta.revisions":  {neg: []string{"/.Spotlight-V100"}},
	"sys.meta.temporary":  {neg: []string{"/.Spotlight-V100"}},
	"sys.meta.pkinstall":  {neg: []string{"/.Spotlight-V100"}},
	"sys.meta.cores":      {neg: []string{"/usr"}},
	"sys.meta.home":       {neg: []string{"/usr"}},
	"sys.meta.vm":         {neg: []string{"/usr"}},
	"trash.volume.root":   {neg: []string{testHome + "/.Trash"}},
	"personal.home.root":  {pos: []string{testHome}, neg: []string{"/Users", "/Users/Shared"}},
	"personal.shared":     {neg: []string{testHome}},

	// The code-root rules are generated from the context, not the catalog,
	// so they are asserted here rather than in the derived table.
	"bak.timemachine.local": {
		pos: []string{"/Volumes/com.apple.TimeMachine.localsnapshots"},
		neg: []string{"/Volumes"},
	},
}
