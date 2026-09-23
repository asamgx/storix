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
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// init registers the detector.
//
// The order puts apps after the container and developer-tool detectors, so it
// appears last in the detectors table and probes last. It does not decide
// precedence: an apps claim carries classify.SourceApps, which loses to any
// detector claim and beats a catalog rule whatever the registry order is.
func init() { detect.Register(300, New()) }

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

// maxReceiptFailures is how many consecutive `pkgutil --pkg-info` calls may
// fail before the receipt loop gives up.
//
// One failure is a receipt the database cannot describe and the loop carries
// on. Three in a row is the database itself being unavailable, and running
// the remaining several hundred calls against it costs seconds and learns
// nothing. Either way the degradation is recorded, because a receipt that was
// never read is a keep signal that was never found.
const maxReceiptFailures = 3

// Probe names. They are named constants rather than literals because a
// verdict reads them back: [Analysis.incompleteEvidence] decides whether an
// orphan verdict rests on evidence a degraded probe could not gather, and a
// typo there would silently un-guard the answer.
const (
	probeBrew         = "brew"
	probePkgutil      = "pkgutil"
	probeLSRegister   = "lsregister"
	probeSpotlight    = "mdfind"
	probeCodesign     = "codesign"
	probeApplications = "applications"
	probeLaunchd      = "launchd"
)

// Probe interrogates the machine. It is the only part of this package that
// touches the outside world, and it never fails: a command that is missing or
// that times out becomes a Degradation and the rest of the probe carries on,
// because an inventory missing its cask receipts is far better than no
// inventory at all.
func (d *Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	f := &Facts{TeamIDs: map[string]string{}}
	p := &prober{d: d, env: env, f: f, paths: Paths{
		Root: volumeRootOf(env.Home),
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

	// A probe that learned nothing at all is reporting a machine with no
	// applications to inventory, which is an answer rather than a failure:
	// a scan rooted at a directory that holds no /Applications, no
	// Caskroom and no receipts has nothing for this detector to say.
	// Calling that degraded would put a permanent warning on every partial
	// scan.
	if f.empty() {
		return f, detect.Missingf("no applications, casks, receipts or launch items under %s", env.Home)
	}
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
	return d.AnalyzeAt(t, f, cx, time.Time{})
}

// AnalyzeAt is Analyze with the clock named explicitly.
//
// Every age in a verdict — "written 3 hours ago", the thirty-day window that
// keeps a recently used tool out of the orphan list — is measured from some
// instant, and which instant it is decides whether a scan means the same thing
// tomorrow as it did today. A caller that holds the scan's own clock passes it
// here, so that rebuilding the report from a cache file reproduces the report
// that file was written with instead of re-dating it to the moment of reading.
// A zero now falls back to the tree's finish time and then to time.Now.
func (d *Detector) AnalyzeAt(t *walk.Tree, f detect.Facts, cx classify.Context, now time.Time) *Analysis {
	facts, ok := f.(*Facts)
	if !ok || facts == nil {
		return nil
	}
	opts := d.Opts
	opts.CodesignAvailable = !degradedFor(facts, probeCodesign)
	if opts.Now.IsZero() {
		opts.Now = now
	}
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
	// paths anchors the absolute directories the probe reads, in the
	// filesystem's own coordinates.
	//
	// A scan of the data volume reads "/System/Volumes/Data/Applications",
	// not "/Applications", and a scan of a fixture reads the fixture's.
	// Both are derived from the home the scan was given, which is the only
	// thing that says where the scan is rooted. Reading the absolute paths
	// as written instead meant a scan of a temporary directory quietly
	// inventoried the host's own applications and casks.
	paths Paths
}

// display converts a path the probe read into the form the walked tree uses,
// so that Facts are in one coordinate system whoever reads them: the tree,
// the analysis, the cache and the report.
func (p *prober) display(full string) string { return mac.DisplayPath(full) }

// check asks whether a path is there, in the form the facts record.
//
// The second result is empty when the answer is known either way, and carries
// the reason when it is not: a stat refused by the sandbox or by a missing
// Full Disk Access grant says nothing about whether the file is there, and the
// verdicts must not read it as "gone". See [detect.Env.Lookup].
func (p *prober) check(full string) (exists bool, checkErr string) {
	ok, err := p.env.Lookup(full)
	if err == nil {
		return ok, ""
	}
	return false, UncheckedNote(full, err)
}

// UncheckedNote is the evidence line for a path that could not be stat'd. It
// names the path and the reason, and adds the one thing a reader can do about
// the reason that causes nearly all of them.
func UncheckedNote(full string, err error) string {
	msg := err.Error()
	var pe *fs.PathError
	if errors.As(err, &pe) {
		msg = pe.Err.Error()
	}
	note := "could not check " + full + ": " + msg
	if errors.Is(err, fs.ErrPermission) {
		note += "; grant Full Disk Access"
	}
	return note
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
		p.degrade(probeBrew, "no Caskroom directory found")
		return
	}
	p.f.CaskroomDir = p.display(dir)
	tokens, err := p.readDir(dir)
	if err != nil {
		p.degrade(probeBrew, "listing "+dir+": "+err.Error())
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
				Token: token, Dir: p.display(path.Join(dir, token)), ReceiptErr: readErr.Error(),
			})
			continue
		}
		c, decErr := DecodeReceipt(token, data)
		c.Dir = p.display(path.Join(dir, token))
		if decErr != nil {
			p.degrade(probeBrew, "receipt for "+token+": "+decErr.Error())
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
		p.degrade(probePkgutil, list.Reason())
		return
	}
	// A receipt that could not be read is a keep signal that was not found,
	// and a keep signal that was not found is an orphan verdict nobody
	// argued against. So the first failure is recorded rather than skipped,
	// and a run of them stops the loop instead of grinding through several
	// hundred calls to a database that is not answering.
	ids := ParsePkgList(list.Stdout)
	said, consecutive := false, 0
	for i, id := range ids {
		info := p.env.Runner.Run(ctx, probe.Cmd{
			Name: "pkgutil", Args: []string{"--pkg-info", id}, Timeout: pkgutilTimeout,
		})
		if !info.OK() {
			consecutive++
			if !said {
				p.degrade(probePkgutil, "--pkg-info "+id+": "+info.Reason())
				said = true
			}
			if consecutive >= maxReceiptFailures {
				p.degrade(probePkgutil, "gave up after "+itoa(consecutive)+
					" consecutive --pkg-info failures; "+itoa(len(ids)-i-1)+
					" receipts were not read")
				return
			}
			continue
		}
		consecutive = 0
		rec := ParsePkgInfo(info.Stdout)
		if rec.PkgID == "" {
			continue
		}
		if install := rec.InstallPath(); install != "" {
			rec.LocationExists, rec.CheckErr = p.check(p.paths.Resolve(install))
		}
		// The file listing is evidence only for a receipt whose install
		// location is gone, and it is the one command here that can be
		// slow, so it is run for those receipts alone. A location that
		// could not be checked is not gone, so it is not run for one of
		// those either.
		if !rec.LocationExists && rec.CheckErr == "" {
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
		if p.env.Exists(p.paths.Resolve(path.Join(vol, rel))) {
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
		p.degrade(probeLSRegister, res.Reason())
		return
	}
	entries := ParseLSRegisterDump(strings.NewReader(res.Stdout))
	if len(entries) < 10 {
		p.degrade(probeLSRegister, "dump yielded only "+itoa(len(entries))+" entries; format may have changed")
	}
	for i := range entries {
		entries[i].Exists, entries[i].CheckErr = p.check(p.paths.Resolve(entries[i].Path))
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
			// A directory that is not there is an answer: plenty of
			// machines have no ~/Library/LaunchAgents. A directory
			// that is there and would not open is a set of keep
			// signals nobody read, and the verdicts have to know.
			if !errors.Is(err, fs.ErrNotExist) {
				p.degrade(probeLaunchd, "listing "+d.path+": "+err.Error())
			}
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
					Path: p.display(full), Label: strings.TrimSuffix(name, ".plist"), System: d.system,
				})
				continue
			}
			item, _ := ParseLaunchPlist(p.display(full), data, d.system)
			if item.Program != "" {
				item.ProgramExists, item.CheckErr = p.check(p.paths.Resolve(item.Program))
			}
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
		dirs = append(dirs, bundleDir{p.paths.Resolve(p.f.CaskroomDir), 3})
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
		// /Applications/Setapp and ~/Applications are absent on most
		// machines, which says nothing. A listing refused for any other
		// reason is a set of installed applications this scan did not
		// see, and every one of them is an orphan verdict waiting to be
		// wrong.
		if !errors.Is(err, fs.ErrNotExist) {
			p.degrade(probeApplications, "listing "+dir+": "+err.Error())
		}
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
	display := p.display(bundlePath)
	info := BundleInfo{Path: display, DisplayName: BundleBaseName(display)}
	data, err := p.env.ReadFile(filepath.Join(bundlePath, "Contents", "Info.plist"))
	if err == nil {
		if parsed, parseErr := ParseInfoPlist(display, data); parseErr == nil {
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
		p.degrade(probeSpotlight, res.Reason())
		return
	}
	count := 0
	for _, line := range strings.Split(res.Stdout, "\n") {
		full := strings.TrimSpace(line)
		if full == "" || seen[full] || NestedInBundle(path.Dir(full)) {
			continue
		}
		// Spotlight indexes every mounted volume, so a Time Machine disk
		// or a cloned system drive answers with the applications it
		// holds. A copy on another volume is not this volume's
		// installation, and counting one as such is how an application
		// deleted from this disk keeps looking installed.
		if !p.paths.OnVolume(p.display(full)) {
			continue
		}
		// The index also outlives what it indexed. A hit that is
		// definitely gone is dropped; one that merely could not be
		// checked is kept, because the cost of wrongly dropping it is an
		// orphan verdict and the cost of keeping it is a stale row.
		if exists, err := p.env.Lookup(full); err == nil && !exists {
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
		p.degrade(probeCodesign, "writing the team id cache: "+err.Error())
	}
	p.f.TeamIDs = r.Cache()
	if len(p.f.TeamIDs) == 0 {
		p.degrade(probeCodesign, "no signatures could be read")
	}
}

// volumeRootOf derives where a scan is rooted from the home it was given.
//
// A home of "/System/Volumes/Data/Users/andrewsam" says the scan is rooted at
// the data volume, so "/Applications" means
// "/System/Volumes/Data/Applications". A home of "/tmp/fixture/Users/andrew"
// says the same about a fixture. A home directly under "/Users" leaves the
// root empty, which is an unrooted machine and the ordinary case.
func volumeRootOf(home string) string {
	dir := path.Dir(path.Clean(home))
	if path.Base(dir) != "Users" {
		return ""
	}
	root := path.Dir(dir)
	if root == "/" || root == "." {
		return ""
	}
	return root
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
