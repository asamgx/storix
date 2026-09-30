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
// appears last in the detectors table. It decides nothing else. It is not when
// the probe runs — every detector's probe goroutine starts together — and it
// is not precedence: an apps claim carries classify.SourceApps, which loses to
// any detector claim and beats a catalog rule whatever the registry order is.
func init() { detect.Register(300, New()) }

// Detector is the apps detector. It satisfies detect.Detector.
//
// One instance of it is registered at init and shared by every scan in the
// process, so nothing on it may be written by a probe. It carries
// configuration a caller sets before any scan starts and nothing else: a probe
// that stored its state here raced with a second scan.Run — a rescan from the
// interface, two tests in parallel — and handed the second scan the first
// one's answers. Whatever one probe learns belongs on its prober and comes
// back in its Facts.
type Detector struct {
	// TeamCachePath overrides where the team id cache lives; empty selects
	// DefaultTeamCachePath.
	TeamCachePath string

	// Opts tune the analysis.
	Opts Options
}

// New builds the detector with its defaults.
func New() *Detector { return &Detector{} }

// Name is the stable identifier.
func (d *Detector) Name() string { return "apps" }

// NewFacts returns an empty fact value for decoding a cached section.
func (d *Detector) NewFacts() detect.Facts { return &Facts{} }

// probeTimeouts are the per-command budgets. They are generous next to what
// the commands cost and short next to what a person will wait.
//
// lsregister is the outlier. The dump is two hundred thousand lines and takes
// two or three seconds on an idle machine — but this probe runs while the walk
// is saturating the same disk, and there it has been measured at 11.6 s. Ten
// seconds was therefore a cap the machine crossed on a normal scan, and every
// time it did, every bundle LaunchServices alone knew about disappeared from
// the inventory and its owner fell to orphan-likely with nothing to contradict
// it. The cap is what bounds a dump that has genuinely hung; probeBudget is
// what bounds the probe.
const (
	brewTimeout       = 5 * time.Second
	pkgutilTimeout    = 5 * time.Second
	lsregisterTimeout = 30 * time.Second
	mdfindTimeout     = 5 * time.Second
)

// probeBudget is how long the whole probe may take.
//
// The registry gives each detector a budget — [detect.DefaultTimeout] unless
// the scan started it with one of its own — and cancels a probe that outruns
// it, and a cancelled apps probe yields no inventory at all. The scan waits
// for a running probe until that budget is spent however soon the walk
// finishes, because its grace is a floor under the wait and not a ceiling; so
// the detector budget is the ceiling that matters, and this one is set under
// it. Every step's own cap is clamped to what is left of it rather than
// summed on top. A step that
// arrives with nothing left records a degradation and does not run, which is
// the difference between a report that is missing a section and one that
// quietly asserts the section was empty.
const probeBudget = 18 * time.Second

// minStepBudget is the least time worth giving a command. Below it the command
// would be killed before it could answer, and the degradation says so instead.
const minStepBudget = 250 * time.Millisecond

// lsregisterPath is where LaunchServices keeps its dump tool. It is not on
// any PATH, so it is named in full.
const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/" +
	"Frameworks/LaunchServices.framework/Support/lsregister"

// lsregisterMaxStdout is how much of the LaunchServices dump is kept.
//
// The runner's default is eight megabytes and the dump is 21.6 MB on the
// reference machine, so the default kept its head and dropped every
// registration in the tail. Sixty-four megabytes is three times the largest
// dump measured; one that outgrows even that comes back Truncated, which is
// not OK, and is recorded as a degradation rather than parsed as the whole
// register.
const lsregisterMaxStdout = 64 << 20

// maxSpotlightBundles caps the backstop listing, which on an unusual machine
// could otherwise return thousands of paths. An answer longer than the cap is
// recorded as a degradation, because every hit past it is an installation the
// verdicts never heard of.
//
// The cap has to sit well above an ordinary machine, or the degradation is
// permanent and no orphan is ever reported. Spotlight answers for the sealed
// system volume too: the reference machine returns 249 bundles outside the
// standard directories, 222 of them under /System. Each one costs a stat and
// an Info.plist read, so a thousand is still a fraction of a second.
const maxSpotlightBundles = 1000

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
	// probeTeamCache is the team id cache file rather than a command. A
	// cache that will not read or write costs a codesign run, never a
	// signature, so it is reported apart from probeCodesign and caps no
	// verdict.
	probeTeamCache = "teamid-cache"
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

	ctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()

	// The order is the order of decreasing tolerance for being cut short.
	// The Caskroom is one `brew --caskroom`, capped at brewTimeout, and then
	// a read from disk; LaunchServices is the slowest command, capped at
	// lsregisterTimeout and measured at 11.6 s under a busy disk, and the
	// one whose absence silently removes installed applications from the
	// inventory, so it goes early while the budget is untouched. The receipt loop is last
	// among the slow ones because a receipt that was not read is a keep
	// signal that was not found, which degrade() records and the verdicts
	// then refuse to call an orphan.
	p.casks(ctx)
	p.registry(ctx)
	p.receipts(ctx)
	p.launchItems()
	p.bundles(ctx)
	p.groupContainers()
	p.teamIDs(ctx)
	// What follows adds evidence and never decides a verdict, so it runs
	// last and does not degrade the probe when it cannot read something.
	p.clis()
	p.configLinks()
	p.references()

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

// followsApp are the detectors whose claims describe an application's data
// rather than a tool's. The ide detector knows ~/.cursor and Application
// Support/Cursor are Cursor's, and claims them ahead of this package; when
// Cursor is gone its claim still said "tool-managed", so the ledger showed
// the leftovers of three removed editors as in use. Container and toolchain
// detectors are not listed: ~/.docker belongs to whichever docker CLI is
// installed, whatever became of Docker Desktop.
var followsApp = map[string]bool{"ide": true}

// Refine marks the claims an editor detector won as orphaned when the
// application they belong to is gone.
//
// It runs after every detector has classified, because only then are both
// sides known: the ide detector's claim on the directory and this package's
// verdict on its owner. The verdict has already been through every keep
// signal and every incomplete-evidence check, so a claim is only changed
// when the owner was found reclaimable, and never on a directory a command-
// line tool of the same product still reads, nor on a symbolic link — the
// link is how the user's own configuration reaches the application, and it
// frees nothing.
func (d *Detector) Refine(t *walk.Tree, f detect.Facts, cx classify.Context, claims []classify.Claim) []classify.Claim {
	a := d.Analyze(t, f, cx)
	if a == nil {
		return claims
	}
	gone := a.goneOwnerKeys()
	if len(gone) == 0 {
		return claims
	}
	protected := a.protectedNodes()
	for i := range claims {
		cl := &claims[i]
		if cl.Source.Kind != classify.SourceDetector || !followsApp[cl.Source.ID] || cl.Node == nil {
			continue
		}
		if cl.Reclaim == classify.Orphaned || cl.Node.Kind == walk.KindSymlink {
			continue
		}
		label, ok := firstGone(gone, cl.OwnerKeys)
		if !ok {
			continue
		}
		// The evidence slice may be shared with other claims of the same
		// detector, so it is copied before anything is added to it.
		ev := append([]string(nil), cl.Evidence...)
		if reason, kept := protected[cl.Node]; kept {
			ev = append(ev, "kept: "+reason)
			cl.Evidence = ev
			continue
		}
		ev = append(ev, label+" is no longer installed, so what the "+cl.Source.ID+
			" detector found here is left behind (apps verdict)")
		cl.Reclaim, cl.Evidence = classify.Orphaned, ev
	}
	return claims
}

// goneOwnerKeys maps the identifying keys of every owner whose verdict is
// reclaimable to its label. A key an installed owner also answers to is left
// out: two owners sharing an identifier is exactly when a guess would free
// data that is in use.
func (a *Analysis) goneOwnerKeys() map[string]string {
	gone := make(map[string]string)
	inUse := make(map[string]bool)
	for _, key := range a.OwnerKeys() {
		o, v := a.Owners[key], a.Verdicts[key]
		if v == nil {
			continue
		}
		keys := []string{key}
		if o.Owner.Slug != "" {
			keys = append(keys, "product:"+o.Owner.Slug)
		}
		for _, id := range o.IDs {
			keys = append(keys, "app:"+id)
		}
		for _, k := range keys {
			if !strings.HasPrefix(k, "app:") && !strings.HasPrefix(k, "product:") {
				continue
			}
			k = strings.ToLower(k)
			if v.State.Reclaimable() {
				gone[k] = o.Owner.Label
			} else {
				inUse[k] = true
			}
		}
	}
	for k := range inUse {
		delete(gone, k)
	}
	return gone
}

// firstGone finds the removed owner a claim's keys name.
func firstGone(gone map[string]string, keys []string) (string, bool) {
	for _, k := range keys {
		if label, ok := gone[strings.ToLower(k)]; ok {
			return label, true
		}
	}
	return "", false
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

// factPaths is where the scan is rooted, in the coordinates the Facts use.
//
// p.paths is for reading the disk and is rooted wherever the scan was: a real
// scan is handed a home of "/System/Volumes/Data/Users/<u>", so its root is
// the data volume. Every path in the Facts has been through display, which
// strips that prefix. A question that compares a recorded path against the
// root — is this Spotlight hit on this volume, is this bundle in
// ~/Applications — therefore has to ask it of these Paths, or it compares a
// display path with a data-volume one and answers no for every path there is.
func (p *prober) factPaths() Paths {
	home := p.display(p.paths.Home)
	return Paths{Root: volumeRootOf(home), Home: home, User: p.paths.User}
}

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

// within clamps a command's own cap to what is left of the probe's budget.
//
// The second result is false when there is not enough left to be worth
// starting, so the caller degrades rather than issuing a command it knows will
// be killed. A context with no deadline — a test calling one step directly —
// leaves the cap alone.
func (p *prober) within(ctx context.Context, want time.Duration) (time.Duration, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return want, true
	}
	left := time.Until(deadline)
	if left < minStepBudget {
		return 0, false
	}
	return min(want, left), true
}

// outOfTime is the degradation for a step the budget did not reach.
func (p *prober) outOfTime(name string) {
	p.degrade(name, "the apps probe used its "+probeBudget.String()+" budget before this ran")
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
//
// No Caskroom is an answer, not a failure, when nothing could be hiding one:
// Homebrew is not installed, or it is and has never installed a cask. Calling
// that degraded put brew on the degraded list of every such machine, and since
// brew is one of the orphan probes, no orphan could ever be reported there.
// The failures are the cases where casks may exist that nobody read: brew ran
// and did not answer, or a Caskroom that is there would not list.
func (p *prober) casks(ctx context.Context) {
	dir, failure := p.caskroomDir(ctx)
	if failure != "" {
		p.degrade(probeBrew, failure)
		return
	}
	if dir == "" {
		return
	}
	tokens, err := p.readDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			p.degrade(probeBrew, "listing "+dir+": "+err.Error())
		}
		return
	}
	p.f.CaskroomDir = p.display(dir)
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
//
// An empty dir with an empty failure means there is no Caskroom to read. A
// failure means there may be one this probe could not find: brew ran and did
// not answer, and neither standard location exists, so a Homebrew installed
// under a custom prefix would go unread.
func (p *prober) caskroomDir(ctx context.Context) (dir, failure string) {
	budget, ok := p.within(ctx, brewTimeout)
	if !ok {
		return "", "the apps probe used its " + probeBudget.String() + " budget before this ran"
	}
	res := p.env.Runner.Run(ctx, probe.Cmd{
		Name:    "brew",
		Args:    []string{"--caskroom"},
		Env:     []string{"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "NO_COLOR=1"},
		Timeout: budget,
	})
	if res.OK() {
		if dir := strings.TrimSpace(res.Stdout); dir != "" {
			return dir, ""
		}
	}
	for _, dir := range []string{"/opt/homebrew/Caskroom", "/usr/local/Caskroom"} {
		if anchored := p.paths.Resolve(dir); p.env.Exists(anchored) {
			return anchored, ""
		}
	}
	if res.Missing {
		return "", ""
	}
	if res.OK() {
		return "", "brew --caskroom printed nothing and no Caskroom was found at the standard locations"
	}
	return "", "brew --caskroom: " + res.Reason() + ", and no Caskroom was found at the standard locations"
}

// receipts reads the installer package database. Apple's own receipts are
// dropped: there are hundreds and none describes an application the user
// installed.
func (p *prober) receipts(ctx context.Context) {
	budget, ok := p.within(ctx, pkgutilTimeout)
	if !ok {
		p.outOfTime(probePkgutil)
		return
	}
	list := p.env.Runner.Run(ctx, probe.Cmd{
		Name: "pkgutil", Args: []string{"--pkgs"}, Timeout: budget,
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
		step, have := p.within(ctx, pkgutilTimeout)
		if !have {
			p.degrade(probePkgutil, "the apps probe used its "+probeBudget.String()+
				" budget with "+itoa(len(ids)-i)+" receipts unread")
			return
		}
		info := p.env.Runner.Run(ctx, probe.Cmd{
			Name: "pkgutil", Args: []string{"--pkg-info", id}, Timeout: step,
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
	budget, ok := p.within(ctx, pkgutilTimeout)
	if !ok {
		return
	}
	res := p.env.Runner.Run(ctx, probe.Cmd{
		Name: "pkgutil", Args: []string{"--files", rec.PkgID}, Timeout: budget,
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
	budget, ok := p.within(ctx, lsregisterTimeout)
	if !ok {
		p.outOfTime(probeLSRegister)
		return
	}
	res := p.env.Runner.Run(ctx, probe.Cmd{
		Name: lsregisterPath, Args: []string{"-dump"}, Timeout: budget,
		MaxStdout: lsregisterMaxStdout,
	})
	// A truncated dump is not OK: the registrations it dropped are
	// identifiers nobody learned, which is a keep signal lost.
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
			// A plist that will not read or decode is still recorded,
			// with the reason in CheckErr: it is a job definition for
			// something, and whether that something still runs is
			// exactly what nobody found out.
			full := filepath.Join(d.path, name)
			data, readErr := p.env.ReadFile(full)
			if readErr != nil {
				p.f.LaunchItems = append(p.f.LaunchItems, LaunchItem{
					Path: p.display(full), Label: strings.TrimSuffix(name, ".plist"), System: d.system,
					CheckErr: UncheckedNote(full, readErr),
				})
				continue
			}
			item, parseErr := ParseLaunchPlist(p.display(full), data, d.system)
			switch {
			case parseErr != nil:
				item.CheckErr = "could not parse " + full + ": " + parseErr.Error()
			case item.Program != "":
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
		if seen[p.display(full)] {
			continue
		}
		seen[p.display(full)] = true
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
// known to exist. Without it those would look like orphans, which is why
// mdfind is one of the orphan probes: a search that failed, came back empty
// or was cut at the cap is recorded as a degradation, and a degradation keeps
// every orphan verdict at unknown.
func (p *prober) spotlight(ctx context.Context, seen map[string]bool) {
	budget, ok := p.within(ctx, mdfindTimeout)
	if !ok {
		p.outOfTime(probeSpotlight)
		return
	}
	res := p.env.Runner.Run(ctx, probe.Cmd{
		Name:    "mdfind",
		Args:    []string{"kMDItemContentType == 'com.apple.application-bundle'"},
		Timeout: budget,
	})
	if !res.OK() {
		p.degrade(probeSpotlight, res.Reason())
		return
	}
	// Every Mac has applications, Safari among them, so an empty answer is
	// not a machine with none: it is an index that is off or still being
	// built, which is a search that was not done.
	if strings.TrimSpace(res.Stdout) == "" {
		p.degrade(probeSpotlight, "mdfind found no applications at all; Spotlight indexing may be off")
		return
	}
	count := 0
	onVolume := p.factPaths()
	for _, line := range strings.Split(res.Stdout, "\n") {
		full := strings.TrimSpace(line)
		if full == "" || NestedInBundle(path.Dir(full)) {
			continue
		}
		// mdfind answers in whichever form the index holds, and the
		// directory listing records what it read in display form, so the
		// two are compared there.
		display := p.display(full)
		if seen[display] {
			continue
		}
		// Spotlight indexes every mounted volume, so a Time Machine disk
		// or a cloned system drive answers with the applications it
		// holds. A copy on another volume is not this volume's
		// installation, and counting one as such is how an application
		// deleted from this disk keeps looking installed.
		if !onVolume.OnVolume(display) {
			continue
		}
		// The index also outlives what it indexed. A hit that is
		// definitely gone is dropped; one that merely could not be
		// checked is kept, because the cost of wrongly dropping it is an
		// orphan verdict and the cost of keeping it is a stale row.
		if exists, err := p.env.Lookup(full); err == nil && !exists {
			continue
		}
		if count >= maxSpotlightBundles {
			p.degrade(probeSpotlight, "more than "+itoa(maxSpotlightBundles)+
				" applications outside the standard directories; the rest were not read")
			return
		}
		count++
		seen[display] = true
		p.f.Spotlight = append(p.f.Spotlight, p.readBundle(full))
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
	if err := r.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		p.degrade(probeTeamCache, "reading the team id cache: "+err.Error())
	}
	// With the budget spent the cached answers are still worth having, so
	// the resolver runs without a runner: it fills in what the cache knows
	// and asks codesign nothing.
	if _, ok := p.within(ctx, probe.DefaultTimeout); !ok {
		p.outOfTime(probeCodesign)
		r.Runner = nil
	}

	inv := BuildInventory(nil, p.f, p.factPaths())
	r.Resolve(ctx, inv.InstalledBundles())
	if err := r.Save(); err != nil {
		p.degrade(probeTeamCache, "writing the team id cache: "+err.Error())
	}
	p.f.TeamIDs = r.Cache()
	p.f.CodesignCalls = r.Calls()
	// The failures are this scan's own. Whether the cache is empty says
	// nothing about this run: a cache holding one earlier answer is never
	// empty, and a codesign that failed for every bundle today would have
	// been reported as a clean run.
	switch n, first := r.Failures(); {
	case n > 0:
		p.degrade(probeCodesign, itoa(n)+" signatures could not be read; "+first)
	case len(p.f.TeamIDs) == 0:
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
