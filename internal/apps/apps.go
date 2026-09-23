// Package apps builds the machine's application inventory and works out
// which directory belongs to which application.
//
// The ledger can say that ~/Library is 81.9 GB and that 32.1 GB of it is
// Application Support. It cannot say that 7 GB of that is Spotify, that
// 214 MB of Caches belongs to a Cursor installation whose bundle is gone, or
// that "com.paloaltonetworks.*" is the residue of software uninstalled in
// December. Those answers need an inventory of what is installed and a
// resolution order that turns a directory name into an owner.
//
// Nothing here deletes, moves or modifies anything. The probe runs seven
// commands, all of them read-only, and reads a few dozen property lists
// through the sanctioned reader in internal/detect, which refuses dataless
// files so that measuring an iCloud-evicted application cannot download it.
// The only file this package writes is the team id cache in teamid.go.
package apps

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// Detector is the apps detector. It satisfies detect.Detector.
type Detector struct {
	// TeamCachePath overrides where the team id cache lives; empty selects
	// DefaultTeamCachePath.
	TeamCachePath string

	// Opts tune the analysis.
	Opts Options

	// teams is the resolver, kept so a caller can read its call count.
	teams *TeamResolver
}

// New builds the detector with its defaults.
func New() *Detector { return &Detector{} }

// Name is the stable identifier.
func (d *Detector) Name() string { return "apps" }

// NewFacts returns an empty fact value for decoding a cached section.
func (d *Detector) NewFacts() detect.Facts { return &Facts{} }

// TeamResolver is the resolver the last probe used, or nil before one ran.
func (d *Detector) TeamResolver() *TeamResolver { return d.teams }

// probeTimeouts are the per-command budgets. They are generous next to what
// the commands cost and short next to what a person will wait.
const (
	brewTimeout       = 5 * time.Second
	pkgutilTimeout    = 5 * time.Second
	lsregisterTimeout = 10 * time.Second
	mdfindTimeout     = 5 * time.Second
)

// lsregisterPath is where LaunchServices keeps its dump tool. It is not on
// any PATH, so it is named in full.
const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/" +
	"Frameworks/LaunchServices.framework/Support/lsregister"

// maxSpotlightBundles caps the backstop listing, which on an unusual machine
// could otherwise return thousands of paths.
const maxSpotlightBundles = 200

// Probe interrogates the machine. It is the only part of this package that
// touches the outside world, and it never fails: a command that is missing or
// that times out becomes a Degradation and the rest of the probe carries on,
// because an inventory missing its cask receipts is far better than no
// inventory at all.
func (d *Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	f := &Facts{TeamIDs: map[string]string{}}
	p := &prober{d: d, env: env, f: f, paths: Paths{
		Root: d.Opts.Root,
		Home: env.Home,
		User: path.Base(env.Home),
	}}

	p.casks(ctx)
	p.receipts(ctx)
	p.registry(ctx)
	p.launchItems()
	p.bundles(ctx)
	p.groupContainers()
	p.teamIDs(ctx)

	if len(f.Degraded) > 0 {
		reasons := make([]string, 0, len(f.Degraded))
		for _, deg := range f.Degraded {
			reasons = append(reasons, deg.Probe+": "+deg.Reason)
		}
		return f, detect.Degradedf("%s", strings.Join(reasons, "; "))
	}
	return f, nil
}

// Classify turns the facts and the tree into claims and a summary.
//
// It is pure and cheap, which is what lets a scan loaded from the cache
// re-run it over the stored facts instead of asking the machine again. The
// summary is empty on purpose: the Developer and Containers views render
// typed rows a tool detector produces, while this package publishes its own
// report of footprints and verdicts, which [Analyze] and [Footprints] build.
func (d *Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	a := d.Analyze(t, f, cx)
	if a == nil {
		return nil, detect.Summary{}
	}
	return a.Claims(), detect.Summary{}
}

// Analyze runs the attribution over a tree and a set of facts, filling in the
// detector's options. It is exported because the apps report, the footprint
// view and `storix explain` all need the analysis rather than the claims.
func (d *Detector) Analyze(t *walk.Tree, f detect.Facts, cx classify.Context) *Analysis {
	facts, ok := f.(*Facts)
	if !ok || facts == nil {
		return nil
	}
	opts := d.Opts
	opts.CodesignAvailable = !degradedFor(facts, "codesign")
	return Analyze(t, facts, cx, opts)
}

// degradedFor reports whether a named probe failed.
func degradedFor(f *Facts, probeName string) bool {
	for _, deg := range f.Degraded {
		if deg.Probe == probeName {
			return true
		}
	}
	return false
}

// prober holds the state of one probe run.
type prober struct {
	d   *Detector
	env detect.Env
	f   *Facts
	// paths anchors the absolute directories the probe looks in. A real
	// scan leaves Root empty and they mean what they say; the corpus test
	// sets it, and the probe then stays inside the fixture instead of
	// reading the machine's own /Applications.
	paths Paths
}

// degrade records a probe that could not run.
func (p *prober) degrade(name, reason string) {
	p.f.Degraded = append(p.f.Degraded, Degradation{Probe: name, Reason: reason})
}

// readDir lists a directory's entry names through the sanctioned reader,
// which refuses a dataless directory and never follows a final symlink.
func (p *prober) readDir(dir string) ([]string, error) {
	if p.env.ReadDir == nil {
		return nil, &os.PathError{Op: "readdir", Path: dir, Err: os.ErrInvalid}
	}
	entries, err := p.env.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}

// casks reads the Caskroom: the token directories and each one's install
// receipt. No "brew list" and no "brew info" are run; the receipts on disk
// say everything those commands would, and reading them cannot touch the
// network.
func (p *prober) casks(ctx context.Context) {
	dir := p.caskroomDir(ctx)
	if dir == "" {
		p.degrade("brew", "no Caskroom directory found")
		return
	}
	p.f.CaskroomDir = dir
	tokens, err := p.readDir(dir)
	if err != nil {
		p.degrade("brew", "listing "+dir+": "+err.Error())
		return
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		if strings.HasPrefix(token, ".") || IsFontCask(token) {
			continue
		}
		receipt := filepath.Join(dir, token, ".metadata", "INSTALL_RECEIPT.json")
		data, readErr := p.env.ReadFile(receipt)
		if readErr != nil {
			p.f.Casks = append(p.f.Casks, Cask{
				Token: token, Dir: path.Join(dir, token), ReceiptErr: readErr.Error(),
			})
			continue
		}
		c, decErr := DecodeReceipt(token, data)
		c.Dir = path.Join(dir, token)
		if decErr != nil {
			p.degrade("brew", "receipt for "+token+": "+decErr.Error())
		}
		p.f.Casks = append(p.f.Casks, c)
	}
}

// caskroomDir asks Homebrew where the Caskroom is, falling back to the two
// standard locations so a machine whose brew is broken still gets its casks.
func (p *prober) caskroomDir(ctx context.Context) string {
	res := p.env.Runner.Run(ctx, probe.Cmd{
		Name:    "brew",
		Args:    []string{"--caskroom"},
		Env:     []string{"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "NO_COLOR=1"},
		Timeout: brewTimeout,
	})
	if res.OK() {
		if dir := strings.TrimSpace(res.Stdout); dir != "" {
			return dir
		}
	}
	for _, dir := range []string{"/opt/homebrew/Caskroom", "/usr/local/Caskroom"} {
		if anchored := p.paths.Resolve(dir); p.env.Exists(anchored) {
			return anchored
		}
	}
	return ""
}

// receipts reads the installer package database. Apple's own receipts are
// dropped: there are hundreds and none describes an application the user
// installed.
func (p *prober) receipts(ctx context.Context) {
	list := p.env.Runner.Run(ctx, probe.Cmd{
		Name: "pkgutil", Args: []string{"--pkgs"}, Timeout: pkgutilTimeout,
	})
	if !list.OK() {
		p.degrade("pkgutil", list.Reason())
		return
	}
	for _, id := range ParsePkgList(list.Stdout) {
		info := p.env.Runner.Run(ctx, probe.Cmd{
			Name: "pkgutil", Args: []string{"--pkg-info", id}, Timeout: pkgutilTimeout,
		})
		if !info.OK() {
			continue
		}
		rec := ParsePkgInfo(info.Stdout)
		if rec.PkgID == "" {
			continue
		}
		rec.LocationExists = rec.InstallPath() != "" && p.env.Exists(rec.InstallPath())
		// The file listing is evidence only for a receipt whose install
		// location is gone, and it is the one command here that can be
		// slow, so it is run for those receipts alone.
		if !rec.LocationExists {
			p.receiptFiles(ctx, &rec)
		}
		p.f.Receipts = append(p.f.Receipts, rec)
	}
}

// receiptFiles measures how much of a package is still on disk.
func (p *prober) receiptFiles(ctx context.Context, rec *Receipt) {
	res := p.env.Runner.Run(ctx, probe.Cmd{
		Name: "pkgutil", Args: []string{"--files", rec.PkgID}, Timeout: pkgutilTimeout,
	})
	if !res.OK() {
		return
	}
	vol := rec.Volume
	if vol == "" {
		vol = "/"
	}
	files := ParsePkgFiles(res.Stdout)
	rec.FilesTotal, rec.FilesChecked = len(files), true
	for _, rel := range files {
		if p.env.Exists(path.Join(vol, rel)) {
			rec.FilesPresent++
		}
	}
}

// registry reads the LaunchServices database. It is optional: a failure is a
// degradation, never fatal, because the register is corroborating evidence.
func (p *prober) registry(ctx context.Context) {
	res := p.env.Runner.Run(ctx, probe.Cmd{
		Name: lsregisterPath, Args: []string{"-dump"}, Timeout: lsregisterTimeout,
	})
	if !res.OK() {
		p.degrade("lsregister", res.Reason())
		return
	}
	entries := ParseLSRegisterDump(strings.NewReader(res.Stdout))
	if len(entries) < 10 {
		p.degrade("lsregister", "dump yielded only "+itoa(len(entries))+" entries; format may have changed")
	}
	for i := range entries {
		entries[i].Exists = p.env.Exists(entries[i].Path)
	}
	p.f.Registry = entries
}

// launchItems reads the three launchd directories. Every file under them is
// retained by the walker's exempt prefixes, so the names are also in the
// tree; they are read here because the tree holds no contents.
func (p *prober) launchItems() {
	dirs := []struct {
		path   string
		system bool
	}{
		{path.Join(p.env.Home, "Library", "LaunchAgents"), false},
		{p.paths.Resolve("/Library/LaunchAgents"), true},
		{p.paths.Resolve("/Library/LaunchDaemons"), true},
	}
	for _, d := range dirs {
		names, err := p.readDir(d.path)
		if err != nil {
			continue
		}
		sort.Strings(names)
		for _, name := range names {
			if !strings.HasSuffix(name, ".plist") {
				continue
			}
			full := filepath.Join(d.path, name)
			data, readErr := p.env.ReadFile(full)
			if readErr != nil {
				p.f.LaunchItems = append(p.f.LaunchItems, LaunchItem{
					Path: full, Label: strings.TrimSuffix(name, ".plist"), System: d.system,
				})
				continue
			}
			item, _ := ParseLaunchPlist(full, data, d.system)
			item.ProgramExists = item.Program != "" && p.env.Exists(item.Program)
			p.f.LaunchItems = append(p.f.LaunchItems, item)
		}
	}
}

// bundles reads the Info.plist of every application in the standard places,
// then falls back to Spotlight for the ones installed somewhere unusual.
func (p *prober) bundles(ctx context.Context) {
	seen := make(map[string]bool)
	for _, dir := range p.bundleDirs() {
		p.readBundlesIn(dir.path, dir.depth, seen)
	}
	p.spotlight(ctx, seen)
}

// bundleDir is one directory to look for applications in, and how deep.
type bundleDir struct {
	path  string
	depth int
}

// bundleDirs are the places an application is normally installed. The depth
// of three under /Applications covers a publisher folder such as
// /Applications/Autodesk/AutoCAD 2027/AutoCAD 2027.app.
func (p *prober) bundleDirs() []bundleDir {
	dirs := []bundleDir{
		{p.paths.Resolve("/Applications"), 3},
		{p.paths.Resolve("/Applications/Utilities"), 1},
		{p.paths.Resolve("/Applications/Setapp"), 1},
	}
	if p.env.Home != "" {
		dirs = append(dirs, bundleDir{filepath.Join(p.env.Home, "Applications"), 1})
	}
	if p.f.CaskroomDir != "" {
		dirs = append(dirs, bundleDir{p.f.CaskroomDir, 3})
	}
	return dirs
}

// readBundlesIn finds the application bundles under a directory and reads
// each one's Info.plist. It descends into ordinary directories up to depth
// and never into a bundle, because everything inside one belongs to it.
func (p *prober) readBundlesIn(dir string, depth int, seen map[string]bool) {
	if depth <= 0 {
		return
	}
	names, err := p.readDir(dir)
	if err != nil {
		return
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		fi, statErr := p.env.Stat(full)
		if !strings.EqualFold(filepath.Ext(name), ".app") {
			if statErr == nil && fi.IsDir && !isBundleDir(name) {
				p.readBundlesIn(full, depth-1, seen)
			}
			continue
		}
		// An application bundle is a directory. A name ending in ".app"
		// that is not one is a symlink, and the Caskroom is full of
		// them: uninstalling an application by dragging it to the Trash
		// leaves Homebrew's link behind, pointing at nothing. Counting
		// one as an installation would mean a cask whose application is
		// gone still looked installed, which is precisely the case this
		// milestone exists to find. Stat never follows a final symlink,
		// so a live link is rejected here too and the application is
		// found at its real location instead.
		if statErr != nil || !fi.IsDir {
			continue
		}
		if seen[full] {
			continue
		}
		seen[full] = true
		p.f.AppDirBundles = append(p.f.AppDirBundles, p.readBundle(full))
	}
}

// readBundle reads one bundle's Info.plist and looks for the App Store
// receipt beside it. A bundle whose plist will not parse still yields an
// entry: a name-only application is installed just as much as a named one.
func (p *prober) readBundle(bundlePath string) BundleInfo {
	info := BundleInfo{Path: bundlePath, DisplayName: BundleBaseName(bundlePath)}
	data, err := p.env.ReadFile(filepath.Join(bundlePath, "Contents", "Info.plist"))
	if err == nil {
		if parsed, parseErr := ParseInfoPlist(bundlePath, data); parseErr == nil {
			info = parsed
		}
	}
	info.MASReceipt = p.env.Exists(filepath.Join(bundlePath, "Contents", "_MASReceipt", "receipt"))
	return info
}

// spotlight finds bundles outside the standard directories, so that an
// application installed by JetBrains Toolbox or dragged to the Desktop is
// known to exist. Without it those would look like orphans.
func (p *prober) spotlight(ctx context.Context, seen map[string]bool) {
	res := p.env.Runner.Run(ctx, probe.Cmd{
		Name:    "mdfind",
		Args:    []string{"kMDItemContentType == 'com.apple.application-bundle'"},
		Timeout: mdfindTimeout,
	})
	if !res.OK() {
		p.degrade("mdfind", res.Reason())
		return
	}
	count := 0
	for _, line := range strings.Split(res.Stdout, "\n") {
		full := strings.TrimSpace(line)
		if full == "" || seen[full] || NestedInBundle(path.Dir(full)) {
			continue
		}
		seen[full] = true
		p.f.Spotlight = append(p.f.Spotlight, p.readBundle(full))
		if count++; count >= maxSpotlightBundles {
			return
		}
	}
}

// groupContainers lists the group container names, which decide whether
// codesign needs to run at all.
func (p *prober) groupContainers() {
	if p.env.Home == "" {
		return
	}
	names, err := p.readDir(filepath.Join(p.env.Home, "Library", "Group Containers"))
	if err != nil {
		return
	}
	sort.Strings(names)
	p.f.GroupContainerNames = names
}

// teamIDs resolves the signing team of every installed bundle, but only when
// a group container is namespaced by a team id that is not Apple's. On a
// machine with no such container this runs nothing at all.
func (p *prober) teamIDs(ctx context.Context) {
	if !NeedsTeamIDs(p.f.GroupContainerNames) {
		return
	}
	cachePath := p.d.TeamCachePath
	if cachePath == "" {
		cachePath = DefaultTeamCachePath()
	}
	r := NewTeamResolver(p.env.Runner, cachePath)
	_ = r.Load()
	p.d.teams = r

	inv := BuildInventory(nil, p.f, p.paths)
	r.Resolve(ctx, inv.InstalledBundles())
	if err := r.Save(); err != nil {
		p.degrade("codesign", "writing the team id cache: "+err.Error())
	}
	p.f.TeamIDs = r.Cache()
	if len(p.f.TeamIDs) == 0 {
		p.degrade("codesign", "no signatures could be read")
	}
}

// isBundleDir reports whether a directory name is a bundle of any kind, which
// the bundle search must not descend into.
func isBundleDir(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case "", ".":
		return false
	}
	for _, b := range []string{".app", ".framework", ".bundle", ".plugin", ".appex", ".xpc", ".kext", ".prefpane"} {
		if ext == b {
			return true
		}
	}
	return false
}

// itoa is the small integer formatting the evidence strings need.
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
