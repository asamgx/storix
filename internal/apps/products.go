package apps

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// OwnerKind is what sort of thing owns a directory.
type OwnerKind uint8

const (
	// KindApp is an application with an installed bundle.
	KindApp OwnerKind = iota + 1
	// KindProduct is a known product with no installed bundle.
	KindProduct
	// KindCask is a Homebrew cask whose application is missing.
	KindCask
	// KindVendor is a publisher's shared directory.
	KindVendor
	// KindNonApp is software that never had a bundle: a CLI, a helper.
	KindNonApp
	// KindOwnBuild is something the user built themselves.
	KindOwnBuild
	// KindUnknown is a directory nothing could be said about.
	KindUnknown
)

var ownerKindNames = [...]string{"", "app", "product", "cask", "vendor", "non-app", "own-build", "unknown"}

func (k OwnerKind) String() string {
	if int(k) >= len(ownerKindNames) {
		return "invalid"
	}
	return ownerKindNames[k]
}

// Product is one row of the alias table: the several identifiers, directory
// names and cask tokens that all mean the same application.
//
// The table is typed Go rather than a config file for the same reason the
// rule catalog is: an alias that stops matching should be a test failure, and
// the collision invariant in products_test.go can only be checked over a
// table the compiler already agrees with.
type Product struct {
	// Slug is the stable key when no bundle is installed, so an orphan
	// keeps one identity across runs: "cursor", "warp", "antigravity".
	Slug string
	// Label is what the report prints.
	Label string
	// BundleIDs are the product's identifiers, primary first, helpers
	// after. An entry may end in "*" to cover a family of helper ids.
	BundleIDs []string
	// Vendor is the reverse-DNS publisher prefix, empty when the product
	// has none worth sharing.
	Vendor string
	// Names are directory and display names, matched case-insensitively
	// and with spaces both kept and removed.
	Names []string
	// Casks are Homebrew cask tokens that install this product.
	Casks []string
	// TeamIDs are a static hint only; codesign stays the source of truth.
	TeamIDs []string
	// Kind is App unless the product is a CLI or a helper.
	Kind OwnerKind
	// Distinct keeps a product from ever being attributed to a sibling
	// that shares its vendor: Antigravity is not Chrome, Atlas is not
	// ChatGPT, however much of "com.google" or "com.openai" they share.
	Distinct bool
}

// Products is the alias table, seeded from a real machine's corpus. Rows are
// grouped by how they were discovered rather than alphabetically, because a
// reader adding a row is nearly always looking at one family at a time.
var Products = []Product{
	// Editors and IDEs.
	{Slug: "vscode", Label: "VS Code", BundleIDs: []string{"com.microsoft.VSCode", "com.microsoft.VSCode.ShipIt"},
		Vendor: "com.microsoft", Names: []string{"Code", "Visual Studio Code", "Microsoft"}, Casks: []string{"visual-studio-code"}},
	{Slug: "cursor", Label: "Cursor", BundleIDs: []string{"com.todesktop.230313mzl4w4u92"},
		Names: []string{"Cursor", ".cursor"}, Casks: []string{"cursor"}, Distinct: true},
	{Slug: "zed", Label: "Zed", BundleIDs: []string{"dev.zed.Zed"}, Vendor: "dev.zed", Names: []string{"Zed"}, Casks: []string{"zed"}},
	{Slug: "sublime", Label: "Sublime Text", BundleIDs: []string{"com.sublimetext.4", "com.sublimetext.3"},
		Vendor: "com.sublimetext", Names: []string{"Sublime Text"}, Casks: []string{"sublime-text"}},
	{Slug: "android-studio", Label: "Android Studio", BundleIDs: []string{"com.google.android.studio"},
		Names: []string{"AndroidStudio", "Android Studio", "AndroidStudio2025.1.3", "Google/AndroidStudio"}},
	{Slug: "antigravity", Label: "Antigravity", BundleIDs: []string{"com.google.antigravity", "com.google.antigravity.*"},
		Names: []string{"Antigravity", ".antigravity"}, Distinct: true},
	{Slug: "tabnine", Label: "TabNine", BundleIDs: []string{"com.tabnine.*"}, Names: []string{"TabNine"}, Distinct: true},

	// Browsers.
	{Slug: "chrome", Label: "Google Chrome", BundleIDs: []string{
		"com.google.Chrome", "com.google.Chrome.*", "com.google.Keystone", "com.google.Keystone.Agent",
		"com.google.keystone.*", "com.google.GoogleUpdater", "com.google.GoogleUpdater.*"},
		Names: []string{"Google", "Chrome", "Google Chrome", "GoogleUpdater", "Keystone", "RLZ", "consentOptions", "GoogleSoftwareUpdate"},
		Casks: []string{"google-chrome"}},
	{Slug: "brave", Label: "Brave Browser", BundleIDs: []string{"com.brave.Browser", "com.brave.Browser.*"},
		Vendor: "com.brave", Names: []string{"BraveSoftware", "Brave Browser", "Brave"}, Casks: []string{"brave-browser"}},
	{Slug: "arc", Label: "Arc", BundleIDs: []string{"company.thebrowser.Browser", "company.thebrowser.Browser.*"},
		Vendor: "company.thebrowser", Names: []string{"Arc"}, Casks: []string{"arc"}},
	{Slug: "opera", Label: "Opera", BundleIDs: []string{"com.operasoftware.Opera", "com.operasoftware.*"},
		Vendor: "com.operasoftware", Names: []string{"Opera", "com.operasoftware.Opera"}, Distinct: true},
	{Slug: "vivaldi", Label: "Vivaldi", BundleIDs: []string{"com.vivaldi.Vivaldi"}, Names: []string{"Vivaldi"}, Distinct: true},
	{Slug: "edge", Label: "Microsoft Edge", BundleIDs: []string{"com.microsoft.edgemac", "com.microsoft.edgemac.*"},
		Names: []string{"Microsoft Edge"}, Distinct: true},
	{Slug: "chromium", Label: "Chromium", BundleIDs: []string{"org.chromium.Chromium"}, Names: []string{"Chromium"}, Distinct: true},

	// AI tools.
	{Slug: "chatgpt", Label: "ChatGPT", BundleIDs: []string{
		"com.openai.codex", "com.openai.chat", "com.openai.chat.*", "com.openai.sky.CUAService"},
		Vendor: "com.openai", Names: []string{"OpenAI", "ChatGPT", "ChatGPTHelper"}, Casks: []string{"chatgpt"}, TeamIDs: []string{"2DC432GLL2"}},
	{Slug: "atlas", Label: "Atlas", BundleIDs: []string{"com.openai.atlas", "com.openai.atlas.*"},
		Names: []string{"Atlas", "ChatGPT Atlas"}, Distinct: true},
	{Slug: "codex-cli", Label: "Codex CLI", Names: []string{"Codex", ".codex"}, Casks: []string{"codex"}, Kind: KindNonApp},
	{Slug: "claude", Label: "Claude", BundleIDs: []string{"com.anthropic.claudefordesktop", "com.anthropic.claudefordesktop.*"},
		Vendor: "com.anthropic", Names: []string{"Claude"}, Casks: []string{"claude"}},
	{Slug: "claude-code", Label: "Claude Code", Names: []string{".claude", "Claude Code"}, Casks: []string{"claude-code@latest"}, Kind: KindNonApp},
	{Slug: "ollama", Label: "Ollama", BundleIDs: []string{"com.electron.ollama"}, Names: []string{"Ollama"}, Casks: []string{"ollama-app"}},

	// Communication and media.
	{Slug: "slack", Label: "Slack", BundleIDs: []string{"com.tinyspeck.slackmacgap", "com.tinyspeck.slackmacgap.*"},
		Vendor: "com.tinyspeck", Names: []string{"Slack"}, Casks: []string{"slack"}},
	{Slug: "discord", Label: "Discord", BundleIDs: []string{"com.hnc.Discord", "com.hnc.Discord.*"},
		Vendor: "com.hnc", Names: []string{"Discord"}, Casks: []string{"discord"}},
	{Slug: "whatsapp", Label: "WhatsApp", BundleIDs: []string{
		"net.whatsapp.WhatsApp", "net.whatsapp.WhatsApp.*", "net.whatsapp.WhatsAppSMB", "group.com.facebook.family"},
		Vendor: "net.whatsapp", Names: []string{"WhatsApp"}, Casks: []string{"whatsapp"}},
	{Slug: "mattermost", Label: "Mattermost", BundleIDs: []string{"Mattermost.Desktop", "com.mattermost.desktop"},
		Names: []string{"Mattermost"}, Casks: []string{"mattermost"}, Distinct: true},
	{Slug: "spotify", Label: "Spotify", BundleIDs: []string{"com.spotify.client", "com.spotify.client.*"},
		Vendor: "com.spotify", Names: []string{"Spotify"}, Casks: []string{"spotify"}},
	{Slug: "stremio", Label: "Stremio", BundleIDs: []string{
		"com.westbridge.stremio5-mac", "com.smartcodeltd.stremio", "com.stremio.Stremio", "com.stremio.service"},
		Names: []string{"Stremio", "Smart Code ltd", "stremio-server", "StremioService"},
		Casks: []string{"stremio", "stremioservice"}},
	{Slug: "vlc", Label: "VLC", BundleIDs: []string{"org.videolan.vlc"}, Names: []string{"VLC"}, Casks: []string{"vlc"}},
	{Slug: "transmission", Label: "Transmission", BundleIDs: []string{"org.m0k.transmission"}, Names: []string{"Transmission"}, Casks: []string{"transmission"}},

	// Productivity.
	{Slug: "notion", Label: "Notion", BundleIDs: []string{"notion.id", "notion.id.*"}, Names: []string{"Notion"}, Casks: []string{"notion"}},
	{Slug: "obsidian", Label: "Obsidian", BundleIDs: []string{"md.obsidian", "md.obsidian.*"}, Names: []string{"Obsidian"}, Casks: []string{"obsidian"}},
	{Slug: "raycast", Label: "Raycast", BundleIDs: []string{"com.raycast.macos", "com.raycast.macos.*"},
		Vendor: "com.raycast", Names: []string{"Raycast"}, Casks: []string{"raycast"}, TeamIDs: []string{"SY64MV22J9"}},
	{Slug: "bitwarden", Label: "Bitwarden", BundleIDs: []string{"com.bitwarden.desktop", "com.bitwarden.desktop.*"},
		Vendor: "com.bitwarden", Names: []string{"Bitwarden"}, Casks: []string{"bitwarden"}, TeamIDs: []string{"LTZ2PFU5D6"}},
	{Slug: "numi", Label: "Numi", BundleIDs: []string{"com.dmitrynikolaev.numi", "com.dmitrynikolaev.numi.*"}, Names: []string{"Numi"}, Casks: []string{"numi"}},
	{Slug: "postman", Label: "Postman", BundleIDs: []string{"com.postmanlabs.mac"}, Vendor: "com.postmanlabs", Names: []string{"Postman"}, Casks: []string{"postman"}},
	{Slug: "localsend", Label: "LocalSend", BundleIDs: []string{"org.localsend.localsendApp", "org.localsend.localsendApp.*"},
		Names: []string{"LocalSend", "localsend.shared_group", "localsend"}, Casks: []string{"localsend"}},
	{Slug: "devtoys", Label: "DevToys", BundleIDs: []string{"com.devtoys", "com.devtoys.*"}, Names: []string{"DevToys"}, Casks: []string{"devtoys"}, Distinct: true},

	// Developer tools with bundles.
	{Slug: "lens", Label: "Lens", BundleIDs: []string{"com.electron.kontena-lens"},
		Names: []string{"Lens", "lens-desktop", "OpenLens"}, Casks: []string{"lens"}},
	{Slug: "orbstack", Label: "OrbStack", BundleIDs: []string{"dev.kdrag0n.MacVirt", "dev.orbstack.OrbStack", "dev.orbstack.*"},
		Vendor: "dev.orbstack", Names: []string{"OrbStack", ".orbstack"}, Casks: []string{"orbstack"}, TeamIDs: []string{"HUAQ24HBR6"}},
	{Slug: "github-desktop", Label: "GitHub Desktop", BundleIDs: []string{"com.github.GitHubClient"}, Names: []string{"GitHub Desktop"}, Casks: []string{"github"}},
	{Slug: "ghostty", Label: "Ghostty", BundleIDs: []string{"com.mitchellh.ghostty"}, Names: []string{"Ghostty", "com.mitchellh.ghostty"}, Casks: []string{"ghostty"}},
	{Slug: "iterm2", Label: "iTerm2", BundleIDs: []string{"com.googlecode.iterm2"}, Names: []string{"iTerm2", "iTerm"}, Casks: []string{"iterm2"}},
	{Slug: "dbeaver", Label: "DBeaver", BundleIDs: []string{"org.jkiss.dbeaver.core.product"}, Names: []string{"DBeaver", "DBeaverData"}, Casks: []string{"dbeaver-community"}},
	{Slug: "beyond-compare", Label: "Beyond Compare", BundleIDs: []string{"com.ScooterSoftware.BeyondCompare", "com.ScooterSoftware.BeyondCompare.*"},
		Vendor: "com.ScooterSoftware", Names: []string{"Beyond Compare", "Beyond Compare 5"}, Casks: []string{"beyond-compare"}},
	{Slug: "balenaetcher", Label: "balenaEtcher", BundleIDs: []string{"io.balena.etcher", "io.balena.etcher.*"},
		Vendor: "io.balena", Names: []string{"balenaEtcher"}, Casks: []string{"balenaetcher"}},

	// Menu bar and utilities.
	{Slug: "stats", Label: "Stats", BundleIDs: []string{"eu.exelban.Stats", "eu.exelban.Stats.*"},
		Vendor: "eu.exelban", Names: []string{"Stats"}, Casks: []string{"stats"}, TeamIDs: []string{"RP2S87B72W"}},
	{Slug: "aerospace", Label: "AeroSpace", BundleIDs: []string{"bobko.aerospace"}, Names: []string{"AeroSpace", ".aerospace"}, Casks: []string{"aerospace"}},
	{Slug: "hiddenbar", Label: "Hidden Bar", BundleIDs: []string{"com.dwarvesv.minimalbar"}, Vendor: "com.dwarvesv", Names: []string{"Hidden Bar"}, Casks: []string{"hiddenbar"}},
	{Slug: "spotmenu", Label: "SpotMenu", BundleIDs: []string{"com.github.kmikiy.SpotMenu"}, Names: []string{"SpotMenu"}, Casks: []string{"spotmenu"}},
	{Slug: "alcove", Label: "Alcove", BundleIDs: []string{"com.henrikruscon.Alcove"}, Names: []string{"Alcove"}},
	{Slug: "amphetamine", Label: "Amphetamine", BundleIDs: []string{"com.if.Amphetamine"}, Names: []string{"Amphetamine"}},
	{Slug: "lulu", Label: "LuLu", BundleIDs: []string{"com.objective-see.lulu", "com.objective-see.lulu.*"},
		Vendor: "com.objective-see", Names: []string{"LuLu"}, Casks: []string{"lulu"}},
	{Slug: "syncthing", Label: "Syncthing", BundleIDs: []string{"com.github.syncthing.syncthing"}, Names: []string{"Syncthing"}, Casks: []string{"syncthing-app"}},
	{Slug: "keycastr", Label: "KeyCastr", BundleIDs: []string{"com.subosito.KeyCastr", "io.github.keycastr"}, Names: []string{"KeyCastr"}, Casks: []string{"keycastr"}},
	{Slug: "grandperspective", Label: "GrandPerspective", BundleIDs: []string{"net.sourceforge.grandperspective"}, Names: []string{"GrandPerspective"}, Casks: []string{"grandperspective"}},
	{Slug: "ui-launcher", Label: "UI Launcher", BundleIDs: []string{"com.electron.ui-launcher"}, Names: []string{"UI Launcher"}, Distinct: true},
	{Slug: "boringnotch", Label: "boringNotch", BundleIDs: []string{"theboringteam.boringnotch"}, Names: []string{"boringNotch"}, Distinct: true},
	{Slug: "dynamiclakepro", Label: "DynamicLake Pro", BundleIDs: []string{"com.aviorrok.DynamicLakePro", "com.aviorrok.DynamicLakePro.*"},
		Vendor: "com.aviorrok", Names: []string{"DynamicLakePro", "DynamicLake Pro"}, Distinct: true},

	// Vendors with heavyweight installers.
	{Slug: "autocad", Label: "AutoCAD", BundleIDs: []string{"com.autodesk.AutoCAD2027", "com.autodesk.*"},
		Vendor: "com.autodesk", Names: []string{"Autodesk", "AutoCAD 2027", "AdskLicensing"}},
	{Slug: "globalprotect", Label: "GlobalProtect", BundleIDs: []string{"com.paloaltonetworks.GlobalProtect", "com.paloaltonetworks.*"},
		Vendor: "com.paloaltonetworks", Names: []string{"PaloAltoNetworks", "GlobalProtect"}, TeamIDs: []string{"PXPZ95SK77"}, Distinct: true},
	{Slug: "wondershare", Label: "Wondershare", BundleIDs: []string{"com.wondershare.*", "com.wondershare.Installer", "com.wondershare.mac-drfoneframe"},
		Vendor: "com.wondershare", Names: []string{"Wondershare"}, Distinct: true},
	{Slug: "warp-cf", Label: "Cloudflare WARP", BundleIDs: []string{"com.cloudflare.1dot1dot1dot1.macos", "com.cloudflare.1dot1dot1dot1.macos.*"},
		Vendor: "com.cloudflare", Names: []string{"Cloudflare"}, Casks: []string{"cloudflare-warp"}},
	{Slug: "macdroid", Label: "MacDroid", BundleIDs: []string{"us.electronic.macdroid", "us.electronic.macdroid.*"},
		Vendor: "us.electronic", Names: []string{"MacDroid"}, Casks: []string{"macdroid"}, TeamIDs: []string{"XS85JU6YZ3"}},
	{Slug: "proxifier", Label: "Proxifier", BundleIDs: []string{"com.initex.proxifier.v3.macos", "com.initex.proxifier.*"},
		Vendor: "com.initex", Names: []string{"Proxifier"}, Casks: []string{"proxifier"}, TeamIDs: []string{"NXELXU5YLW"}},
	{Slug: "diskspeedtest", Label: "Disk Speed Test", BundleIDs: []string{"com.blackmagic-design.DiskSpeedTest"}, Names: []string{"Disk Speed Test"}},

	// Orphan-likely products with no bundle on this machine.
	{Slug: "warp", Label: "Warp", BundleIDs: []string{"dev.warp.Warp-Stable", "dev.warp.*"}, Names: []string{"Warp", ".warp"}, Distinct: true},
	{Slug: "cap", Label: "Cap", BundleIDs: []string{"so.cap.desktop", "so.cap.*"}, Names: []string{"Cap"}, Distinct: true},
}

// nonAppNames is software that never had an application bundle and must
// therefore never be reported as an orphan, however absent a bundle is.
// Homebrew formula names and binary-only cask tokens join this set at run
// time; these are the ones no detector supplies.
var nonAppNames = []string{
	"k9s", "lazygit", "zoxide", "gk", "GitKrakenCLI", "turborepo", "fastmcp",
	"firestore", "segment", "go", "storix", "CEF", ".wrangler",
	"create-next-app-nodejs", "nextjs-nodejs", "stremio-server",
	"ngrok", "gcloud", "gcloud-cli", "temurin",
}

// nonAppGlobs are non-app identifier families.
var nonAppGlobs = []string{"com.segment.storage.*", "*-nodejs", "net.temurin.*", "net.adoptopenjdk.*"}

// productIndex is the lookup structure over the alias table. Literal ids and
// names are maps; the handful of glob ids are a slice, scanned only when the
// maps miss, so the common case stays one hash lookup.
type productIndex struct {
	byID    map[string]*Product
	byName  map[string]*Product
	byCask  map[string]*Product
	byTeam  map[string][]*Product
	globs   []productGlob
	nonApp  map[string]bool
	nonGlob []string
	all     []*Product
}

type productGlob struct {
	pattern string
	product *Product
}

// newProductIndex builds the lookup structure and enforces the collision
// invariant: no identifier and no name may mean two products. A collision is
// an error rather than a last-write-wins, because the symptom of one would be
// an application's data silently attributed to a different application.
func newProductIndex(ps []Product) (*productIndex, error) {
	ix := &productIndex{
		byID:   make(map[string]*Product, len(ps)*3),
		byName: make(map[string]*Product, len(ps)*3),
		byCask: make(map[string]*Product, len(ps)),
		byTeam: make(map[string][]*Product, len(ps)),
		nonApp: make(map[string]bool, len(nonAppNames)),
	}
	// The table is copied rather than indexed in place: the defaults this
	// loop fills in would otherwise be writes to the package-level
	// Products, which a second index built concurrently would race on.
	rows := make([]Product, len(ps))
	copy(rows, ps)

	seenSlug := make(map[string]bool, len(rows))
	for i := range rows {
		p := &rows[i]
		if p.Slug == "" || p.Label == "" {
			return nil, fmt.Errorf("apps: product %d has no slug or label", i)
		}
		if seenSlug[p.Slug] {
			return nil, fmt.Errorf("apps: duplicate product slug %q", p.Slug)
		}
		seenSlug[p.Slug] = true
		if p.Kind == 0 {
			p.Kind = KindApp
		}
		ix.all = append(ix.all, p)
		for _, id := range p.BundleIDs {
			if strings.ContainsAny(id, "*?") {
				ix.globs = append(ix.globs, productGlob{id, p})
				continue
			}
			if prev, ok := ix.byID[id]; ok && prev != p {
				return nil, fmt.Errorf("apps: bundle id %q claimed by %q and %q", id, prev.Slug, p.Slug)
			}
			ix.byID[id] = p
		}
		for _, n := range p.Names {
			for _, k := range nameKeys(n) {
				if prev, ok := ix.byName[k]; ok && prev != p {
					return nil, fmt.Errorf("apps: name %q claimed by %q and %q", n, prev.Slug, p.Slug)
				}
				ix.byName[k] = p
			}
		}
		for _, c := range p.Casks {
			if prev, ok := ix.byCask[c]; ok && prev != p {
				return nil, fmt.Errorf("apps: cask %q claimed by %q and %q", c, prev.Slug, p.Slug)
			}
			ix.byCask[c] = p
		}
		for _, tid := range p.TeamIDs {
			ix.byTeam[tid] = append(ix.byTeam[tid], p)
		}
	}
	// Longer glob patterns are more specific, so ordering them by length
	// makes "com.openai.chat.*" win over a hypothetical "com.openai.*".
	sort.SliceStable(ix.globs, func(i, j int) bool {
		return len(ix.globs[i].pattern) > len(ix.globs[j].pattern)
	})
	for _, n := range nonAppNames {
		ix.nonApp[strings.ToLower(n)] = true
	}
	ix.nonGlob = nonAppGlobs
	return ix, nil
}

// defaultIndex is the package-level alias table, built once. The build cannot
// fail silently: products_test.go asserts newProductIndex returns no error,
// and a collision introduced by a new row fails that test.
var defaultIndex = mustIndex()

func mustIndex() *productIndex {
	ix, err := newProductIndex(Products)
	if err != nil {
		panic(err)
	}
	return ix
}

// nameKeys are the forms a directory name is matched under: lower case, and
// lower case with spaces removed, so "Beyond Compare 5" also answers to
// "beyondcompare5".
func nameKeys(n string) []string {
	l := strings.ToLower(strings.TrimSpace(n))
	if l == "" {
		return nil
	}
	keys := []string{l}
	if squashed := strings.ReplaceAll(l, " ", ""); squashed != l {
		keys = append(keys, squashed)
	}
	return keys
}

// LookupID finds the product an identifier belongs to, trying literal ids
// first and glob families afterwards.
func (ix *productIndex) LookupID(id string) (*Product, bool) {
	if p, ok := ix.byID[id]; ok {
		return p, true
	}
	for _, g := range ix.globs {
		if ok, _ := path.Match(g.pattern, id); ok {
			return g.product, true
		}
	}
	return nil, false
}

// LookupName finds the product a directory or display name belongs to.
//
// A trailing version is trimmed on the second attempt, because vendors write
// the release into the directory name: "AndroidStudio2025.1.3" is the same
// product as "AndroidStudio", and enumerating every release in the table
// would mean the table went stale every quarter.
func (ix *productIndex) LookupName(name string) (*Product, bool) {
	for _, k := range nameKeys(name) {
		if p, ok := ix.byName[k]; ok {
			return p, true
		}
	}
	for _, k := range nameKeys(name) {
		trimmed := strings.TrimRight(k, "0123456789.-_ ")
		if trimmed == "" || trimmed == k {
			continue
		}
		if p, ok := ix.byName[trimmed]; ok {
			return p, true
		}
	}
	return nil, false
}

// LookupCask finds the product a cask token installs.
func (ix *productIndex) LookupCask(token string) (*Product, bool) {
	p, ok := ix.byCask[token]
	return p, ok
}

// LookupTeam lists the products statically hinted for a team id.
func (ix *productIndex) LookupTeam(team string) []*Product { return ix.byTeam[team] }

// IsNonApp reports whether a name is software that never had a bundle.
func (ix *productIndex) IsNonApp(name string) bool {
	if ix.nonApp[strings.ToLower(name)] {
		return true
	}
	for _, g := range ix.nonGlob {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// Key is the owner key a product resolves to when none of its bundles is
// installed. An installed product keys on its bundle id instead, which is
// what lets a detector that knows only the id join the same owner.
func (p *Product) Key() string {
	if p.Kind == KindNonApp {
		return "cli:" + p.Slug
	}
	return "product:" + p.Slug
}
