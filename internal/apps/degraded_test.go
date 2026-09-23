package apps

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
)

// probeFixture is the smallest environment a probe needs: a command runner
// answering from a table, and file access that a test can make fail on
// demand. It exists because every case in this file is about what the probe
// does when something it asked for does not answer.
type probeFixture struct {
	home    string
	records map[string]probe.Result
	// statErr, when set, decides what a stat of a path returns instead of
	// asking the filesystem.
	statErr func(string) error
	// dirErr does the same for a directory listing.
	dirErr func(string) error
}

func newProbeFixture(t *testing.T) *probeFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the fixture root: %v", err)
	}
	return &probeFixture{
		home:    filepath.Join(root, "Users", "andrewsam"),
		records: map[string]probe.Result{},
	}
}

func (pf *probeFixture) env() detect.Env {
	return detect.Env{
		Runner:   &probe.Replay{Records: pf.records},
		Home:     pf.home,
		ReadFile: detect.ReadFile,
		ReadDir: func(p string) ([]os.DirEntry, error) {
			if pf.dirErr != nil {
				if err := pf.dirErr(p); err != nil {
					return nil, &fs.PathError{Op: "readdir", Path: p, Err: err}
				}
			}
			return detect.ReadDir(p)
		},
		Stat: func(p string) (detect.FileInfo, error) {
			if pf.statErr != nil {
				if err := pf.statErr(p); err != nil {
					return detect.FileInfo{}, &fs.PathError{Op: "lstat", Path: p, Err: err}
				}
			}
			return detect.Stat(p)
		},
		LookPath: func(name string) (string, error) { return name, nil },
	}
}

func (pf *probeFixture) prober() *prober {
	return &prober{
		d: &Detector{}, env: pf.env(), f: &Facts{TeamIDs: map[string]string{}},
		paths: Paths{Root: volumeRootOf(pf.home), Home: pf.home, User: path.Base(pf.home)},
	}
}

// degradationFor finds the recorded failure of a probe, if there is one.
func degradationFor(f *Facts, name string) (Degradation, bool) {
	for _, d := range f.Degraded {
		if d.Probe == name {
			return d, true
		}
	}
	return Degradation{}, false
}

func joinDegradations(f *Facts) string {
	var out []string
	for _, d := range f.Degraded {
		out = append(out, d.Probe+": "+d.Reason)
	}
	return strings.Join(out, " | ")
}

// TestAFailedReceiptLookupIsRecorded covers a probe that used to lose evidence
// without saying so. An installer receipt whose location still exists is a
// keep signal, and a `pkgutil --pkg-info` that does not answer is that keep
// signal never found — which leaves an orphan verdict with nothing arguing
// against it. Skipping the receipt silently made that invisible.
func TestAFailedReceiptLookupIsRecorded(t *testing.T) {
	t.Parallel()
	pf := newProbeFixture(t)
	pf.records["pkgutil --pkgs"] = probe.Result{
		Stdout: "com.example.one\ncom.example.two\n",
	}
	pf.records["pkgutil --pkg-info com.example.one"] = probe.Result{TimedOut: true, Duration: time.Second}
	pf.records["pkgutil --pkg-info com.example.two"] = probe.Result{
		Stdout: "package-id: com.example.two\nversion: 1\nvolume: /\nlocation: Applications/Two.app\n",
	}

	p := pf.prober()
	p.receipts(context.Background())

	deg, ok := degradationFor(p.f, probePkgutil)
	if !ok {
		t.Fatalf("a failed --pkg-info was not recorded: %s", joinDegradations(p.f))
	}
	if !strings.Contains(deg.Reason, "com.example.one") {
		t.Errorf("degradation = %q, want it to name the receipt", deg.Reason)
	}
	if !strings.Contains(deg.Reason, "timed out") {
		t.Errorf("degradation = %q, want it to name the failure", deg.Reason)
	}
	// The one that answered is still read: a single bad receipt stops
	// nothing.
	if len(p.f.Receipts) != 1 || p.f.Receipts[0].PkgID != "com.example.two" {
		t.Errorf("receipts = %+v, want the one that answered", p.f.Receipts)
	}
}

// TestTheReceiptLoopGivesUpAfterARunOfFailures is the other half. One failure
// is a receipt the database cannot describe; a run of them is the database
// itself not answering, and putting several hundred more calls to it costs
// seconds and learns nothing.
func TestTheReceiptLoopGivesUpAfterARunOfFailures(t *testing.T) {
	t.Parallel()
	pf := newProbeFixture(t)
	var ids []string
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		ids = append(ids, "com.example."+id)
	}
	pf.records["pkgutil --pkgs"] = probe.Result{Stdout: strings.Join(ids, "\n") + "\n"}
	asked := map[string]bool{}
	pf.records["pkgutil --pkg-info "+ids[0]] = probe.Result{ExitCode: 1, Stderr: "No receipt\n"}

	// Every --pkg-info answers with a failure, and the loop has to stop
	// before it has asked for all six.
	for _, id := range ids {
		pf.records["pkgutil --pkg-info "+id] = probe.Result{ExitCode: 1, Stderr: "No receipt\n"}
	}
	p := pf.prober()
	inner := p.env.Runner
	p.env.Runner = runnerFunc(func(ctx context.Context, c probe.Cmd) probe.Result {
		asked[c.Key()] = true
		return inner.Run(ctx, c)
	})
	p.receipts(context.Background())

	if len(asked) > maxReceiptFailures+1 {
		t.Errorf("asked %d times, want the loop to stop after %d failures in a row",
			len(asked), maxReceiptFailures)
	}
	if !strings.Contains(joinDegradations(p.f), "gave up") {
		t.Errorf("degradations = %q, want one saying the loop gave up", joinDegradations(p.f))
	}
	if !strings.Contains(joinDegradations(p.f), "receipts were not read") {
		t.Errorf("degradations = %q, want it to say how many receipts were skipped",
			joinDegradations(p.f))
	}
}

// runnerFunc adapts a function to probe.Runner.
type runnerFunc func(context.Context, probe.Cmd) probe.Result

func (f runnerFunc) Run(ctx context.Context, c probe.Cmd) probe.Result { return f(ctx, c) }

// TestALaunchdDirectoryThatWillNotListIsRecorded covers the second silent
// loss. Every plist under a LaunchAgents directory is a candidate keep signal,
// so a directory that is there and refuses to open is a set of them nobody
// read. A directory that simply does not exist is not a failure and must not
// be reported as one, or every machine without ~/Library/LaunchAgents would
// carry a permanent warning.
func TestALaunchdDirectoryThatWillNotListIsRecorded(t *testing.T) {
	t.Parallel()
	pf := newProbeFixture(t)
	agents := filepath.Join(pf.home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	pf.dirErr = func(p string) error {
		if p == agents {
			return syscall.EPERM
		}
		return nil
	}

	p := pf.prober()
	p.launchItems()

	deg, ok := degradationFor(p.f, probeLaunchd)
	if !ok {
		t.Fatalf("a launchd directory that would not list was not recorded: %s", joinDegradations(p.f))
	}
	if !strings.Contains(deg.Reason, agents) {
		t.Errorf("degradation = %q, want it to name the directory", deg.Reason)
	}

	// The two /Library directories do not exist under the fixture root, and
	// that is an answer rather than a failure.
	if n := strings.Count(joinDegradations(p.f), probeLaunchd+":"); n != 1 {
		t.Errorf("degradations = %q, want only the directory that refused", joinDegradations(p.f))
	}
}

// TestAPathThatCannotBeCheckedIsNotAPathThatIsGone is finding 4 at the probe.
// A stat refused because the process lacks Full Disk Access says nothing about
// whether the file is there, and the three facts that read one — a receipt's
// install location, a LaunchServices registration, a launch item's program —
// all feed the orphan decision. Reading "could not look" as "gone" is how a
// machine without that grant reports its installed software as uninstalled.
func TestAPathThatCannotBeCheckedIsNotAPathThatIsGone(t *testing.T) {
	t.Parallel()
	pf := newProbeFixture(t)
	denied := "/Applications/Denied.app"
	pf.statErr = func(p string) error {
		if strings.HasSuffix(p, "Denied.app") {
			return syscall.EPERM
		}
		return nil
	}
	pf.records["pkgutil --pkgs"] = probe.Result{Stdout: "com.example.denied\ncom.example.gone\n"}
	pf.records["pkgutil --pkg-info com.example.denied"] = probe.Result{
		Stdout: "package-id: com.example.denied\nversion: 1\nvolume: /\nlocation: Applications/Denied.app\n",
	}
	pf.records["pkgutil --pkg-info com.example.gone"] = probe.Result{
		Stdout: "package-id: com.example.gone\nversion: 1\nvolume: /\nlocation: Applications/Gone.app\n",
	}
	pf.records["pkgutil --files com.example.gone"] = probe.Result{Stdout: "Applications/Gone.app\n"}

	p := pf.prober()
	p.receipts(context.Background())

	byID := map[string]Receipt{}
	for _, r := range p.f.Receipts {
		byID[r.PkgID] = r
	}
	dn := byID["com.example.denied"]
	if dn.LocationExists {
		t.Error("a path that could not be stat'd was reported as present")
	}
	if dn.CheckErr == "" {
		t.Fatal("a path that could not be stat'd was recorded as a definite absence")
	}
	if !strings.Contains(dn.CheckErr, denied) || !strings.Contains(dn.CheckErr, "could not check") {
		t.Errorf("CheckErr = %q, want it to name the path and what happened", dn.CheckErr)
	}
	if !strings.Contains(dn.CheckErr, "Full Disk Access") {
		t.Errorf("CheckErr = %q, want the one thing a reader can do about it", dn.CheckErr)
	}
	// A location that could not be checked is not a location that is gone,
	// so the file census that only runs for a missing location must not.
	if dn.FilesChecked {
		t.Error("the file listing ran for a location that was never shown to be missing")
	}

	gone := byID["com.example.gone"]
	if gone.LocationExists || gone.CheckErr != "" {
		t.Errorf("a genuinely missing location = %+v, want absent and known to be", gone)
	}
	if !gone.FilesChecked {
		t.Error("the file listing should still run for a location that is really gone")
	}
}

// TestSpotlightIgnoresOtherVolumes is finding 6 at the probe. Spotlight
// indexes every mounted disk, so a Time Machine drive answers the
// application-bundle query with everything it holds — and a copy on a backup
// is the opposite of evidence that the application is installed here.
func TestSpotlightIgnoresOtherVolumes(t *testing.T) {
	t.Parallel()
	pf := newProbeFixture(t)
	here := filepath.Join(filepath.Dir(filepath.Dir(pf.home)), "Applications", "Here.app")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(filepath.Dir(filepath.Dir(pf.home)), "Applications", "Stale.app")
	pf.records["mdfind kMDItemContentType == 'com.apple.application-bundle'"] = probe.Result{
		Stdout: strings.Join([]string{
			here,
			"/Volumes/Time Machine/Applications/Backed Up.app",
			stale,
		}, "\n"),
	}

	p := pf.prober()
	p.spotlight(context.Background(), map[string]bool{})

	var got []string
	for _, b := range p.f.Spotlight {
		got = append(got, b.Path)
	}
	if len(got) != 1 || got[0] != p.display(here) {
		t.Errorf("spotlight bundles = %v, want only the one on this volume that exists", got)
	}
}

// TestADegradedProbeCannotProduceAnOrphan is the rule inventory.go states and
// the verdicts did not follow: a degraded probe must never become a verdict.
//
// GlobalProtect is the shape. Its data is attributed by name, its application
// is gone, and the receipt database is what would say whether anything was
// installed there. With pkgutil timing out, nothing has been searched — so the
// answer is that nothing is known, not that the software was removed.
func TestADegradedProbeCannotProduceAnOrphan(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	env := c.env(t)
	replay, ok := env.Runner.(*probe.Replay)
	if !ok {
		t.Fatalf("the corpus runner is %T", env.Runner)
	}
	replay.Records["pkgutil --pkgs"] = probe.Result{TimedOut: true, Duration: pkgutilTimeout}

	d := c.detector(t)
	raw, _ := d.Probe(context.Background(), env)
	facts, ok := raw.(*Facts)
	if !ok {
		t.Fatalf("Probe returned %T", raw)
	}
	if _, degraded := degradationFor(facts, probePkgutil); !degraded {
		t.Fatalf("the timed-out pkgutil was not recorded: %s", joinDegradations(facts))
	}

	opts := d.Opts
	opts.CodesignAvailable = true
	opts.Now = time.Now().Add(365 * 24 * time.Hour)
	a := Analyze(c.walk(t), facts, contextFor(c), opts)

	v := verdictByLabel(t, a, "GlobalProtect")
	if v.State != StateUnknown {
		t.Errorf("GlobalProtect state = %v, want unknown while the receipt database is unread", v.State)
	}
	if !strings.Contains(strings.Join(v.Evidence, " "), "orphan evidence incomplete: pkgutil") {
		t.Errorf("evidence = %v, want it to name the probe that did not answer", v.Evidence)
	}

	// Nothing at all may be called an orphan on this run, because the same
	// search failed for every owner.
	if orphans := a.OwnersInState(StateOrphanLikely); len(orphans) != 0 {
		t.Errorf("orphans = %v, want none while the evidence is incomplete", orphans)
	}

	// The same corpus with pkgutil answering still finds the orphan, so the
	// cap is the degradation and not the analysis giving up.
	_, healthy := c.analyze(t)
	if got := verdictByLabel(t, healthy, "GlobalProtect").State; got != StateOrphanLikely {
		t.Errorf("GlobalProtect with pkgutil answering = %v, want orphan-likely", got)
	}
}

// TestAnUnreadablePathBlocksTheOrphanVerdict is the per-owner form of the same
// rule. One launch item whose program could not be stat'd may be running the
// software right now, so the owner it belongs to cannot be called an orphan
// and the report says which path it could not check.
func TestAnUnreadablePathBlocksTheOrphanVerdict(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/Vivaldi")

	orphan := vf.analyze(t, &Facts{}, Options{})
	if got := verdictByLabel(t, orphan, "Vivaldi").State; got != StateOrphanLikely {
		t.Fatalf("Vivaldi = %v, want orphan-likely before anything is unreadable", got)
	}

	unreadable := vf.analyze(t, &Facts{
		LaunchItems: []LaunchItem{{
			Path:     "/Library/LaunchAgents/com.vivaldi.Vivaldi.plist",
			Label:    "com.vivaldi.Vivaldi",
			Program:  "/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
			CheckErr: "could not check /Applications/Vivaldi.app/Contents/MacOS/Vivaldi: operation not permitted; grant Full Disk Access",
		}},
	}, Options{})
	v := verdictByLabel(t, unreadable, "Vivaldi")
	if v.State != StateUnknown {
		t.Errorf("Vivaldi state = %v, want unknown while its launch item cannot be checked", v.State)
	}
	if !strings.Contains(strings.Join(v.Evidence, " "), "Full Disk Access") {
		t.Errorf("evidence = %v, want it to say what could not be checked", v.Evidence)
	}
}

// TestACopyOnAnotherVolumeIsNotAnInstallation is finding 6 at the verdict. A
// bundle found somewhere unconventional on this disk is still an installation
// — a JetBrains Toolbox directory, a Downloads folder — but one on a mounted
// backup is what a backup is for, and counting it kept every application ever
// deleted from this machine looking installed.
func TestACopyOnAnotherVolumeIsNotAnInstallation(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/Vivaldi")
	bundle := BundleInfo{ID: "com.vivaldi.Vivaldi", DisplayName: "Vivaldi"}

	elsewhere := bundle
	elsewhere.Path = "/Volumes/Time Machine/Applications/Vivaldi.app"
	backed := verdictByLabel(t, vf.analyze(t, &Facts{Spotlight: []BundleInfo{elsewhere}}, Options{}), "Vivaldi")
	if backed.State != StateOrphanLikely {
		t.Errorf("state with only a backup copy = %v, want orphan-likely", backed.State)
	}
	if !strings.Contains(strings.Join(backed.Evidence, " "), "on another volume") {
		t.Errorf("evidence = %v, want it to say the copy is elsewhere", backed.Evidence)
	}

	onDisk := bundle
	onDisk.Path = filepath.Join(vf.root, "Users", "andrewsam", "Downloads", "Vivaldi.app")
	here := verdictByLabel(t, vf.analyze(t, &Facts{Spotlight: []BundleInfo{onDisk}}, Options{}), "Vivaldi")
	if here.State != StateInstalled {
		t.Errorf("state with a copy on this volume = %v, want installed", here.State)
	}
}

// TestOnVolumeBoundsAnUnrootedScanAtTheBootDisk covers the case the fixture
// cannot: a real scan carries no root, and every other disk is under /Volumes.
func TestOnVolumeBoundsAnUnrootedScanAtTheBootDisk(t *testing.T) {
	t.Parallel()
	boot := Paths{Home: "/Users/andrewsam"}
	for _, p := range []string{"/Applications/Arc.app", "/Users/andrewsam/Applications/Arc.app"} {
		if !boot.OnVolume(p) {
			t.Errorf("OnVolume(%q) = false, want true on an unrooted scan", p)
		}
	}
	for _, p := range []string{"/Volumes/Time Machine/Applications/Arc.app", "/Volumes/Backup", ""} {
		if boot.OnVolume(p) {
			t.Errorf("OnVolume(%q) = true, want false: it is another disk", p)
		}
	}

	rooted := Paths{Root: "/tmp/fixture", Home: "/tmp/fixture/Users/u"}
	if !rooted.OnVolume("/tmp/fixture/Applications/Arc.app") {
		t.Error("a rooted scan does not recognise its own volume")
	}
	if rooted.OnVolume("/Applications/Arc.app") {
		t.Error("a rooted scan claimed a path outside its root")
	}
}

// TestAnUnreadableRegistrationIsNotAStaleOne guards the confidence grade.
// "LaunchServices still lists X at a path that is gone" is the evidence that
// promotes an orphan from possible to likely, and a path that could not be
// stat'd is not a path that is gone.
func TestAnUnreadableRegistrationIsNotAStaleOne(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/Chromium")

	stale := vf.analyze(t, &Facts{Registry: []RegistryEntry{{
		ID: "org.chromium.Chromium", Path: "/Applications/Chromium.app",
	}}}, Options{})
	if got := verdictByLabel(t, stale, "Chromium").Confidence; got != classify.Likely {
		t.Errorf("confidence with a registration at a missing path = %v, want likely", got)
	}

	unreadable := vf.analyze(t, &Facts{Registry: []RegistryEntry{{
		ID: "org.chromium.Chromium", Path: "/Applications/Chromium.app",
		CheckErr: "could not check /Applications/Chromium.app: operation not permitted",
	}}}, Options{})
	v := verdictByLabel(t, unreadable, "Chromium")
	if v.State != StateUnknown {
		t.Errorf("state = %v, want unknown while the registration cannot be checked", v.State)
	}
	if strings.Contains(strings.Join(v.Evidence, " "), "which is gone") {
		t.Errorf("evidence = %v, want it not to claim the path is gone", v.Evidence)
	}
}
