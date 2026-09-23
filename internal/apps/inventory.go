package apps

import (
	"path"
	"sort"
	"strings"

	"github.com/asamgx/storix/internal/walk"
)

// Source is where an application bundle was found. The order matters: it is
// the order of decreasing confidence that the bundle is an installation
// rather than a copy, and the cut between SourceCaskroom and SourceNested is
// exactly the line between "this application is installed" and "a copy of
// this application exists somewhere".
type Source uint8

const (
	// SourceApplications is /Applications and /Applications/Utilities.
	SourceApplications Source = iota + 1
	// SourceUserApplications is ~/Applications.
	SourceUserApplications
	// SourceSetapp is /Applications/Setapp, which Setapp manages.
	SourceSetapp
	// SourceVendorFolder is a publisher folder under /Applications.
	SourceVendorFolder
	// SourceCaskroom is <caskroom>/<token>/<version>/X.app.
	SourceCaskroom
	// SourceNested is any other bundle: a JetBrains Toolbox app, an input
	// method, a Sparkle updater copy under Caches, an update staged under
	// Application Support. A nested bundle never counts as installed.
	SourceNested
	// SourceTrash is a bundle under a Trash directory.
	SourceTrash
)

var sourceNames = [...]string{"", "/Applications", "~/Applications", "Setapp", "vendor folder", "cask", "nested", "trash"}

func (s Source) String() string {
	if int(s) >= len(sourceNames) {
		return "invalid"
	}
	return sourceNames[s]
}

// Installed reports whether a bundle found at this source counts as an
// installed application. This single predicate is the orphan precondition:
// were a staged update copy under ~/Library/Application Support to count,
// every application that self-updates would look installed forever.
func (s Source) Installed() bool { return s >= SourceApplications && s <= SourceCaskroom }

// Bundle is one application bundle joined to the scanned tree.
type Bundle struct {
	BundleInfo
	Source Source
	// Cask is the cask whose app artifact names this bundle, when one does.
	Cask *Cask
	// Node is the bundle's node, nil when the bundle lies outside the
	// scanned subtree.
	Node *walk.Node
	// Bytes is the bundle's size, zero when Node is nil.
	Bytes int64
	// TeamID is the signing team, resolved lazily by the team resolver.
	TeamID string
}

// Degradation records a probe that did not run or did not parse. A degraded
// probe must never become a verdict: the report says what it could not see.
type Degradation struct {
	Probe  string `json:"probe"`
	Reason string `json:"reason"`
}

// Facts is everything the apps detector learned from the machine. It is what
// Probe returns and what the scan cache stores, so it is JSON round-trippable
// and holds no pointers and no tree references.
type Facts struct {
	// CaskroomDir is the display path of the Homebrew Caskroom.
	CaskroomDir string          `json:"caskroomDir,omitempty"`
	Casks       []Cask          `json:"casks,omitempty"`
	Receipts    []Receipt       `json:"receipts,omitempty"`
	Registry    []RegistryEntry `json:"registry,omitempty"`
	LaunchItems []LaunchItem    `json:"launchItems,omitempty"`
	// AppDirBundles are the Info.plists of bundles in the standard
	// application directories, vendor folders and the Caskroom.
	AppDirBundles []BundleInfo `json:"appDirBundles,omitempty"`
	// Spotlight are bundles found outside those directories, read so that
	// Classify never has to open a file.
	Spotlight []BundleInfo `json:"spotlight,omitempty"`
	// TeamIDs maps "<id>@<version>" to the signing team.
	TeamIDs map[string]string `json:"teamIds,omitempty"`
	// GroupContainerNames are the names under ~/Library/Group Containers,
	// which decide whether codesign needs to run at all.
	GroupContainerNames []string      `json:"groupContainerNames,omitempty"`
	Degraded            []Degradation `json:"degraded,omitempty"`
}

// Kind identifies the facts in the cache section and the detector registry.
func (Facts) Kind() string { return "apps" }

// empty reports whether the probe learned nothing about the machine.
func (f *Facts) empty() bool {
	return len(f.Casks) == 0 && len(f.Receipts) == 0 && len(f.Registry) == 0 &&
		len(f.LaunchItems) == 0 && len(f.AppDirBundles) == 0 && len(f.Spotlight) == 0
}

// Paths says where the scan is rooted.
//
// Root exists so that nothing in this package hard-codes an absolute system
// path. A real scan leaves it empty and "/Applications" means what it says;
// the corpus test sets it to a fixture directory, and the same code then
// resolves "/Applications" inside the fixture. Without it every test would
// have to run against the machine's own /Applications, which is the one thing
// a reproducible corpus cannot do.
type Paths struct {
	// Root is the display-path prefix of the scanned volume, empty for a
	// real scan.
	Root string
	// Home is the scan user's home as an absolute display path.
	Home string
	// User is the scan user's short name, which the own-build rule reads.
	User string
}

// Resolve turns a location's directory into an absolute path under Root,
// expanding "~" to the scan user's home.
//
// It is idempotent: a path already under Root is returned unchanged. Callers
// mix paths from several places — the location table, an installer receipt,
// a Spotlight result — and some of them are already anchored, so a Resolve
// that anchored unconditionally produced "<root>/<root>/Applications".
func (p Paths) Resolve(dir string) string {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		return expandTilde(dir, p.Home)
	}
	if p.Root == "" || p.Root == "/" || dir == p.Root {
		return dir
	}
	if strings.HasPrefix(dir, p.Root+"/") {
		return dir
	}
	return path.Join(p.Root, dir)
}

// Strip removes the Root prefix from a display path, giving the form the
// path would have on a real machine.
func (p Paths) Strip(display string) string {
	if p.Root == "" || p.Root == "/" {
		return display
	}
	if rest, ok := strings.CutPrefix(display, p.Root); ok {
		if rest == "" {
			return "/"
		}
		if strings.HasPrefix(rest, "/") {
			return rest
		}
	}
	return display
}

// OnVolume reports whether a display path lies on the volume this scan
// covers.
//
// It matters because Spotlight does not stop at the scanned disk. A Time
// Machine drive, a cloned system volume or a mounted disk image answers
// "kMDItemContentType == 'com.apple.application-bundle'" with every
// application it holds, and a copy on a backup is the opposite of evidence
// that the application is installed here: keeping a copy of what was deleted
// is what a backup is for.
//
// A scan with no root is rooted at the boot volume, where every other disk is
// mounted under /Volumes. A scan with one — a fixture, or another volume — is
// bounded by that root instead.
func (p Paths) OnVolume(display string) bool {
	if display == "" {
		return false
	}
	if p.Root == "" || p.Root == "/" {
		return !strings.HasPrefix(display, "/Volumes/")
	}
	return display == p.Root || strings.HasPrefix(display, p.Root+"/")
}

// Inventory is the in-memory join of Facts with the walked tree. It is built
// inside the analysis and never cached: the tree it points into changes every
// scan, while the facts it was built from do not.
type Inventory struct {
	Bundles     []*Bundle
	ByID        map[string][]*Bundle
	ByName      map[string][]*Bundle
	Casks       []*Cask
	CaskByToken map[string]*Cask
	Receipts    []Receipt
	Registry    []RegistryEntry
	LaunchItems []LaunchItem
	Degraded    []Degradation
	// CaskroomDir is the display path of the Caskroom, empty when Homebrew
	// was not found.
	CaskroomDir string
	// Paths is where the scan is rooted.
	Paths Paths
	// registryByID indexes the LaunchServices entries.
	registryByID map[string][]RegistryEntry
}

// BuildInventory joins facts to a tree. The tree may be nil, which is what
// the parser tests use: every bundle then has a nil Node and zero bytes, and
// every name-based lookup still works.
func BuildInventory(t *walk.Tree, f *Facts, p Paths) *Inventory {
	inv := &Inventory{
		ByID:         make(map[string][]*Bundle),
		ByName:       make(map[string][]*Bundle),
		CaskByToken:  make(map[string]*Cask, len(f.Casks)),
		Receipts:     f.Receipts,
		Registry:     f.Registry,
		LaunchItems:  f.LaunchItems,
		Degraded:     f.Degraded,
		CaskroomDir:  f.CaskroomDir,
		Paths:        p,
		registryByID: make(map[string][]RegistryEntry, len(f.Registry)),
	}
	for i := range f.Casks {
		c := &f.Casks[i]
		inv.Casks = append(inv.Casks, c)
		inv.CaskByToken[c.Token] = c
	}
	for _, e := range f.Registry {
		k := strings.ToLower(e.ID)
		inv.registryByID[k] = append(inv.registryByID[k], e)
	}

	seen := make(map[string]bool)
	add := func(info BundleInfo, src Source) *Bundle {
		if info.Path == "" || seen[info.Path] {
			return nil
		}
		seen[info.Path] = true
		b := &Bundle{BundleInfo: info, Source: src}
		if v, ok := f.TeamIDs[teamCacheKey(info)]; ok {
			b.TeamID = v
		}
		if n, ok := lookupDisplay(t, info.Path); ok {
			b.Node, b.Bytes = n, n.Bytes
		}
		inv.Bundles = append(inv.Bundles, b)
		return b
	}
	for _, info := range f.AppDirBundles {
		add(info, classifySource(info.Path, p, f.CaskroomDir))
	}
	for _, info := range f.Spotlight {
		add(info, classifySource(info.Path, p, f.CaskroomDir))
	}

	// The backstop (D24): every bundle node in the tree, whether or not a
	// probe found it. Identifiers come from the registry or the Spotlight
	// results by path; a bundle with neither is name-only, which is still
	// enough to stop an orphan verdict.
	if t != nil {
		byPath := make(map[string]string, len(f.Registry))
		for _, e := range f.Registry {
			if _, ok := byPath[e.Path]; !ok {
				byPath[e.Path] = e.ID
			}
		}
		for _, n := range t.Nodes {
			if !n.Has(walk.FlagBundle) || !strings.EqualFold(path.Ext(n.Name), ".app") {
				continue
			}
			display := n.Display()
			if NestedInBundle(path.Dir(display)) {
				continue
			}
			b := add(BundleInfo{Path: display, ID: byPath[display], DisplayName: BundleBaseName(display)},
				classifySource(display, p, f.CaskroomDir))
			if b != nil && b.Node == nil {
				b.Node, b.Bytes = n, n.Bytes
			}
		}
	}

	inv.link()
	return inv
}

// link builds the id and name indexes and attaches casks to bundles.
func (inv *Inventory) link() {
	// Installed sources first, so a lookup that takes the first hit gets
	// the installation rather than a staged copy.
	sort.SliceStable(inv.Bundles, func(i, j int) bool {
		if inv.Bundles[i].Source != inv.Bundles[j].Source {
			return inv.Bundles[i].Source < inv.Bundles[j].Source
		}
		return inv.Bundles[i].Path < inv.Bundles[j].Path
	})
	for _, b := range inv.Bundles {
		if b.ID != "" {
			k := idKey(b.ID)
			inv.ByID[k] = append(inv.ByID[k], b)
		}
		for _, n := range b.Names() {
			for _, k := range nameKeys(n) {
				inv.ByName[k] = append(inv.ByName[k], b)
			}
		}
	}
	for _, c := range inv.Casks {
		for _, appName := range c.Apps {
			for _, b := range inv.ByName[strings.ToLower(strings.TrimSuffix(appName, ".app"))] {
				if b.Source.Installed() && b.Cask == nil {
					b.Cask = c
				}
			}
		}
		if len(c.QuitIDs) == 0 {
			continue
		}
		for _, b := range inv.Bundles {
			if b.ID == "" || b.Cask != nil || !b.Source.Installed() {
				continue
			}
			if _, ok := c.MatchesID(b.ID); ok {
				b.Cask = c
			}
		}
	}
}

// Installed returns the installed bundle for an identifier.
func (inv *Inventory) Installed(id string) (*Bundle, bool) {
	for _, b := range inv.ByID[idKey(id)] {
		if b.Source.Installed() {
			return b, true
		}
	}
	return nil, false
}

// AnyBundle returns any bundle for an identifier, including nested copies and
// ones in the Trash. It is the backstop that keeps an unconventional install
// from being reported as an orphan.
func (inv *Inventory) AnyBundle(id string) (*Bundle, bool) {
	if bs := inv.ByID[idKey(id)]; len(bs) > 0 {
		return bs[0], true
	}
	return nil, false
}

// InstalledByName returns the installed bundle whose display, bundle or
// folder name matches.
func (inv *Inventory) InstalledByName(name string) (*Bundle, bool) {
	for _, k := range nameKeys(name) {
		for _, b := range inv.ByName[k] {
			if b.Source.Installed() {
				return b, true
			}
		}
	}
	return nil, false
}

// AnyByName returns any bundle matching a name, nested copies included.
func (inv *Inventory) AnyByName(name string) (*Bundle, bool) {
	for _, k := range nameKeys(name) {
		if bs := inv.ByName[k]; len(bs) > 0 {
			return bs[0], true
		}
	}
	return nil, false
}

// InstalledForVendor lists the installed bundles whose identifier shares a
// vendor prefix. The vendor rule needs exactly one to fire.
func (inv *Inventory) InstalledForVendor(vendor string) []*Bundle {
	var out []*Bundle
	for _, b := range inv.Bundles {
		if b.ID == "" || !b.Source.Installed() {
			continue
		}
		rdns, ok := ParseReverseDNS(b.ID)
		if ok && rdns.Vendor == vendor {
			out = append(out, b)
		}
	}
	return out
}

// InstalledForTeam lists the installed bundles signed by a team.
func (inv *Inventory) InstalledForTeam(team string) []*Bundle {
	var out []*Bundle
	for _, b := range inv.Bundles {
		if b.TeamID == team && team != "" && b.Source.Installed() {
			out = append(out, b)
		}
	}
	return out
}

// RegistryFor lists the LaunchServices entries for an identifier.
func (inv *Inventory) RegistryFor(id string) []RegistryEntry { return inv.registryByID[idKey(id)] }

// idKey is how a bundle identifier is keyed in every index here.
//
// macOS treats an identifier case-insensitively: Ollama's Info.plist says
// "com.electron.ollama" while its cache directory is named
// "com.electron.Ollama", and LaunchServices answers to both. Indexing by the
// literal string meant the directory matched nothing, the application looked
// absent, and its data was reported as an orphan. The owner key keeps the
// identifier as it was written; only the lookups fold.
func idKey(id string) string { return strings.ToLower(id) }

// InstalledBundles lists the bundles a team id lookup would have to sign, in
// a stable order: the input to the codesign pass.
func (inv *Inventory) InstalledBundles() []*Bundle {
	var out []*Bundle
	for _, b := range inv.Bundles {
		if b.Source.Installed() {
			out = append(out, b)
		}
	}
	return out
}

// classifySource decides what a bundle's location means. The rules are the
// ones docs/04 lists, with the Caskroom recognised by the probed path rather
// than by a hard-coded prefix so a machine with Homebrew under /usr/local
// behaves the same.
func classifySource(display string, p Paths, caskroom string) Source {
	switch {
	case PathInTrash(display):
		return SourceTrash
	case caskroom != "" && strings.HasPrefix(display, caskroom+"/"):
		return SourceCaskroom
	}
	if p.Home != "" {
		if rest, ok := strings.CutPrefix(display, p.Home+"/Applications/"); ok {
			if !strings.Contains(rest, "/") {
				return SourceUserApplications
			}
			return SourceNested
		}
	}
	rest, ok := strings.CutPrefix(p.Strip(display), "/Applications/")
	if !ok {
		return SourceNested
	}
	segs := strings.Split(rest, "/")
	switch {
	case len(segs) == 1:
		return SourceApplications
	case segs[0] == "Utilities" && len(segs) == 2:
		return SourceApplications
	case segs[0] == "Setapp" && len(segs) == 2:
		return SourceSetapp
	case len(segs) <= 3 && !strings.EqualFold(path.Ext(segs[0]), ".app"):
		// A publisher folder: /Applications/Autodesk/AutoCAD 2027/X.app.
		return SourceVendorFolder
	default:
		return SourceNested
	}
}

// teamCacheKey is how a bundle is keyed in the team id cache: identity and
// version together, so an update re-runs codesign and nothing else does.
func teamCacheKey(info BundleInfo) string {
	id := info.ID
	if id == "" {
		id = BundleBaseName(info.Path)
	}
	return id + "@" + info.Version
}
