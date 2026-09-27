package apps

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/probe"
)

// TestTheLaunchServicesDumpIsGivenRoomForItsSize is the cap on the one command
// whose answer is larger than the runner's default. `lsregister -dump` is over
// twenty megabytes on the reference machine against an eight-megabyte default,
// so under the default every scan read the head of the dump and lost the rest.
func TestTheLaunchServicesDumpIsGivenRoomForItsSize(t *testing.T) {
	t.Parallel()
	pf := newProbeFixture(t)
	var asked probe.Cmd
	p := pf.prober()
	p.env.Runner = runnerFunc(func(_ context.Context, c probe.Cmd) probe.Result {
		asked = c
		return probe.Result{Missing: true}
	})
	p.registry(context.Background())

	if asked.Name != lsregisterPath {
		t.Fatalf("the dump was not run: %+v", asked)
	}
	if asked.MaxStdout < 64<<20 {
		t.Errorf("MaxStdout = %d, want at least 64 MiB for a dump measured at 21.6 MB", asked.MaxStdout)
	}
}

// TestATruncatedDumpIsADegradedDump is what happens when even that is not
// enough. A dump cut short is a register missing its tail, and every bundle
// named only in the tail is one whose identifier nobody learned — a keep
// signal lost, exactly as if the dump had timed out.
func TestATruncatedDumpIsADegradedDump(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	env := c.env(t)
	replay, ok := env.Runner.(*probe.Replay)
	if !ok {
		t.Fatalf("the corpus runner is %T", env.Runner)
	}
	full := replay.Records[lsregisterPath+" -dump"]
	full.Truncated = true
	replay.Records[lsregisterPath+" -dump"] = full

	d := c.detector(t)
	raw, _ := d.Probe(context.Background(), env)
	facts, ok := raw.(*Facts)
	if !ok {
		t.Fatalf("Probe returned %T", raw)
	}
	deg, degraded := degradationFor(facts, probeLSRegister)
	if !degraded {
		t.Fatalf("a truncated dump was not recorded: %s", joinDegradations(facts))
	}
	if !strings.Contains(deg.Reason, "truncated") {
		t.Errorf("degradation = %q, want it to say the dump was cut short", deg.Reason)
	}

	opts := d.Opts
	opts.CodesignAvailable = true
	opts.Now = time.Now().Add(365 * 24 * time.Hour)
	a := Analyze(c.walk(t), facts, contextFor(c), opts)
	for _, s := range reclaimableStates {
		if got := a.OwnersInState(s); len(got) != 0 {
			t.Errorf("%v owners = %v, want none while the dump is incomplete", s, got)
		}
	}
}

// timeoutRunner times out every codesign call and counts them.
type timeoutRunner struct {
	mu    sync.Mutex
	calls int
	// unsigned, when set, answers the way codesign answers for a bundle
	// with no signature at all: exit 1 and a definite statement.
	unsigned bool
}

func (r *timeoutRunner) Run(_ context.Context, _ probe.Cmd) probe.Result {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.unsigned {
		return probe.Result{ExitCode: 1, Stderr: "/Applications/X.app: code object is not signed at all\n"}
	}
	return probe.Result{TimedOut: true, Duration: 5 * time.Second}
}

func (r *timeoutRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestATimedOutSignatureIsNotCached is the cache that remembered a failure as
// an answer. A codesign run that timed out says nothing about the signature,
// and caching its empty team id meant the bundle was never asked about again:
// a group container that bundle owned stayed unattributed on every scan after.
func TestATimedOutSignatureIsNotCached(t *testing.T) {
	t.Parallel()
	cachePath := filepath.Join(t.TempDir(), "teamids.json")
	bundles := testBundles()[:1]

	runner := &timeoutRunner{}
	r := NewTeamResolver(runner, cachePath)
	r.Resolve(context.Background(), bundles)
	if n, first := r.Failures(); n != 1 || !strings.Contains(first, "timed out") {
		t.Errorf("Failures() = (%d, %q), want the timeout recorded", n, first)
	}
	if err := r.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	next := NewTeamResolver(runner, cachePath)
	if err := next.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("load: %v", err)
	}
	next.Resolve(context.Background(), bundles)
	if got := next.Calls(); got != 1 {
		t.Errorf("the next scan ran codesign %d times, want it asked again after a timeout", got)
	}

	// An unsigned bundle is a definite answer, and is asked about once.
	unsigned := &timeoutRunner{unsigned: true}
	cold := NewTeamResolver(unsigned, filepath.Join(t.TempDir(), "teamids.json"))
	cold.Resolve(context.Background(), bundles)
	if n, _ := cold.Failures(); n != 0 {
		t.Errorf("an unsigned bundle was counted as %d failures, want none", n)
	}
	if err := cold.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	warm := NewTeamResolver(unsigned, cold.Path)
	if err := warm.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	warm.Resolve(context.Background(), bundles)
	if got := warm.Calls(); got != 0 {
		t.Errorf("an unsigned bundle was asked about again %d times, want 0", got)
	}
}

// writeTeamCache puts a cache file in place, as an earlier scan would have.
func writeTeamCache(t *testing.T, teams map[string]string) string {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "teamids.json")
	data, err := json.Marshal(teamCacheDoc{Version: teamCacheVersion, Teams: teams})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return cachePath
}

// teamProber is a prober with one installed bundle, a team-prefixed group
// container, and a runner of the test's choosing.
func teamProber(t *testing.T, runner probe.Runner, cachePath string) *prober {
	t.Helper()
	p := dataVolumeProber(nil)
	p.env.Runner = runner
	p.d.TeamCachePath = cachePath
	p.f.GroupContainerNames = []string{"HUAQ24HBR6.dev.orbstack"}
	p.f.AppDirBundles = []BundleInfo{{Path: "/Applications/OrbStack.app", ID: "dev.kdrag0n.MacVirt", Version: "1.0"}}
	return p
}

// TestAFailedSignatureReadIsDegradedWhateverTheCacheHolds is the other half.
// "No signatures could be read" was decided by whether the cache was empty,
// and a cache holding any earlier answer is never empty — so a codesign that
// failed for every bundle on this scan was reported as a clean run.
func TestAFailedSignatureReadIsDegradedWhateverTheCacheHolds(t *testing.T) {
	t.Parallel()
	cachePath := writeTeamCache(t, map[string]string{"com.example.other@1": "ABCDE12345"})
	runner := &timeoutRunner{}
	p := teamProber(t, runner, cachePath)

	p.teamIDs(context.Background())

	if runner.count() != 1 {
		t.Fatalf("codesign ran %d times, want once", runner.count())
	}
	deg, ok := degradationFor(p.f, probeCodesign)
	if !ok {
		t.Fatalf("a timed-out codesign was not recorded: %s", joinDegradations(p.f))
	}
	if !strings.Contains(deg.Reason, "timed out") {
		t.Errorf("degradation = %q, want it to say what happened", deg.Reason)
	}
}

// TestAnUnreadableTeamCacheIsReported covers the error the probe discarded. A
// missing cache is a first scan; one that will not parse is worth a line.
func TestAnUnreadableTeamCacheIsReported(t *testing.T) {
	t.Parallel()
	cachePath := filepath.Join(t.TempDir(), "teamids.json")
	if err := os.WriteFile(cachePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := teamProber(t, &fakeRunner{byPath: map[string]string{"OrbStack.app": "HUAQ24HBR6"}}, cachePath)
	p.teamIDs(context.Background())
	if !strings.Contains(joinDegradations(p.f), "reading the team id cache") {
		t.Errorf("degradations = %q, want the unreadable cache named", joinDegradations(p.f))
	}

	// A cache that does not exist yet is not a failure.
	fresh := teamProber(t, &fakeRunner{byPath: map[string]string{"OrbStack.app": "HUAQ24HBR6"}},
		filepath.Join(t.TempDir(), "teamids.json"))
	fresh.teamIDs(context.Background())
	if len(fresh.f.Degraded) != 0 {
		t.Errorf("a first scan was degraded: %s", joinDegradations(fresh.f))
	}
}

// TestTheSignaturePassRespectsTheBudget is the step that ran with no budget
// check at all. With nothing left it issues no codesign and says so.
func TestTheSignaturePassRespectsTheBudget(t *testing.T) {
	t.Parallel()
	runner := &timeoutRunner{}
	p := teamProber(t, runner, filepath.Join(t.TempDir(), "teamids.json"))

	spent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	p.teamIDs(spent)

	if runner.count() != 0 {
		t.Errorf("codesign ran %d times with no budget left", runner.count())
	}
	if deg, ok := degradationFor(p.f, probeCodesign); !ok || !strings.Contains(deg.Reason, "budget") {
		t.Errorf("degradations = %q, want codesign out of budget", joinDegradations(p.f))
	}
}

// TestADegradedCodesignCapsATeamContainerOrphan is the verdict side. A group
// container named "ABCDE12345.dev.warp.Warp-Stable" is attributed through the
// signature of whichever installed application ABCDE12345 signed, and when no
// signature could be read that search was never done.
func TestADegradedCodesignCapsATeamContainerOrphan(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t,
		"Users/andrewsam/Library/Group Containers/ABCDE12345.dev.warp.Warp-Stable",
		"Users/andrewsam/Library/Application Support/Vivaldi")

	healthy := vf.analyze(t, &Facts{}, Options{CodesignAvailable: true})
	if got := verdictByLabel(t, healthy, "Warp").State; got != StateOrphanLikely {
		t.Fatalf("Warp with codesign answering = %v, want orphan-likely", got)
	}

	degraded := vf.analyze(t, &Facts{Degraded: []Degradation{{Probe: probeCodesign, Reason: "timed out"}}}, Options{})
	v := verdictByLabel(t, degraded, "Warp")
	if v.State != StateUnknown {
		t.Errorf("Warp state = %v, want unknown while no signature could be read", v.State)
	}
	if !strings.Contains(strings.Join(v.Evidence, " "), "orphan evidence incomplete: codesign") {
		t.Errorf("evidence = %v, want it to name codesign", v.Evidence)
	}
	// An owner that no team id names does not depend on a signature.
	if got := verdictByLabel(t, degraded, "Vivaldi").State; got != StateOrphanLikely {
		t.Errorf("Vivaldi = %v, want orphan-likely: codesign says nothing about it", got)
	}
}

// TestAMachineWithoutHomebrewCanStillHaveOrphans is the degradation that never
// cleared. A machine with no Homebrew has no casks, which is an answer; calling
// it a failed brew probe put brew in the degraded list on every scan, and since
// a failed brew probe caps every orphan verdict, the machine could never be
// told about one.
func TestAMachineWithoutHomebrewCanStillHaveOrphans(t *testing.T) {
	t.Parallel()
	pf := newProbeFixture(t)
	pf.records["brew --caskroom"] = probe.Result{Missing: true}
	p := pf.prober()
	p.casks(context.Background())

	if deg, ok := degradationFor(p.f, probeBrew); ok {
		t.Errorf("no Homebrew was recorded as a failure: %s", deg.Reason)
	}
	if len(p.f.Casks) != 0 || p.f.CaskroomDir != "" {
		t.Errorf("casks = %v in %q, want none", p.f.Casks, p.f.CaskroomDir)
	}

	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/Vivaldi")
	if got := verdictByLabel(t, vf.analyze(t, p.f, Options{}), "Vivaldi").State; got != StateOrphanLikely {
		t.Errorf("Vivaldi on a machine without Homebrew = %v, want orphan-likely", got)
	}
}

// TestCaskroomAnswersThatAreNotFailures covers the rest of the brew probe's
// cases, which divide into answers and failures by whether a set of casks
// could exist that nobody read.
func TestCaskroomAnswersThatAreNotFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		brew       probe.Result
		dir        string // created under the fixture root when not empty
		unreadable bool
		degraded   bool
	}{
		// Homebrew is installed and names a Caskroom that was never
		// created, because no cask was ever installed.
		{name: "brew with no casks", brew: probe.Result{Stdout: "<root>/opt/homebrew/Caskroom\n"}},
		// Homebrew answered but did not run to completion, and nothing
		// was found at the standard places: its casks may be anywhere.
		{name: "brew failed", brew: probe.Result{ExitCode: 1, Stderr: "Error: something\n"}, degraded: true},
		{name: "brew timed out", brew: probe.Result{TimedOut: true, Duration: brewTimeout}, degraded: true},
		// A Caskroom that is there and will not list.
		{name: "unreadable caskroom", brew: probe.Result{Stdout: "<root>/opt/homebrew/Caskroom\n"},
			dir: "opt/homebrew/Caskroom", unreadable: true, degraded: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pf := newProbeFixture(t)
			root := filepath.Dir(filepath.Dir(pf.home))
			res := tc.brew
			res.Stdout = strings.ReplaceAll(res.Stdout, "<root>", root)
			pf.records["brew --caskroom"] = res
			if tc.dir != "" {
				full := filepath.Join(root, tc.dir)
				mkdirAll(t, full)
				if tc.unreadable {
					pf.dirErr = func(p string) error {
						if p == full {
							return syscall.EPERM
						}
						return nil
					}
				}
			}
			p := pf.prober()
			p.casks(context.Background())
			_, got := degradationFor(p.f, probeBrew)
			if got != tc.degraded {
				t.Errorf("degraded = %v, want %v: %s", got, tc.degraded, joinDegradations(p.f))
			}
		})
	}
}

// mdfindQuery is the key the Spotlight backstop is recorded under.
const mdfindQuery = "mdfind kMDItemContentType == 'com.apple.application-bundle'"

// TestAFailedSpotlightSearchCapsAnOrphan is the backstop the verdicts did not
// wait for. Spotlight is how an application outside the standard directories
// is known to exist — one in ~/Downloads, one JetBrains Toolbox installed — and
// without it such an application's data is an orphan verdict nobody argued
// against.
func TestAFailedSpotlightSearchCapsAnOrphan(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/Vivaldi")
	if got := verdictByLabel(t, vf.analyze(t, &Facts{}, Options{}), "Vivaldi").State; got != StateOrphanLikely {
		t.Fatalf("Vivaldi with Spotlight answering = %v, want orphan-likely", got)
	}

	pf := newProbeFixture(t)
	pf.records[mdfindQuery] = probe.Result{TimedOut: true, Duration: mdfindTimeout}
	p := pf.prober()
	p.spotlight(context.Background(), map[string]bool{})

	v := verdictByLabel(t, vf.analyze(t, p.f, Options{}), "Vivaldi")
	if v.State != StateUnknown {
		t.Errorf("Vivaldi with Spotlight timed out = %v, want unknown", v.State)
	}
	if !strings.Contains(strings.Join(v.Evidence, " "), "orphan evidence incomplete: mdfind") {
		t.Errorf("evidence = %v, want it to name the search that did not run", v.Evidence)
	}
}

// TestSpotlightAnswersThatAreIncomplete covers the two ways mdfind succeeds
// without having searched everything: an empty answer, which is what it gives
// when indexing is off, and an answer longer than the probe reads.
func TestSpotlightAnswersThatAreIncomplete(t *testing.T) {
	t.Parallel()

	empty := newProbeFixture(t)
	empty.records[mdfindQuery] = probe.Result{}
	p := empty.prober()
	p.spotlight(context.Background(), map[string]bool{})
	if _, ok := degradationFor(p.f, probeSpotlight); !ok {
		t.Error("an empty Spotlight answer was taken as a machine with no applications anywhere")
	}

	long := newProbeFixture(t)
	root := filepath.Dir(filepath.Dir(long.home))
	var hits []string
	for i := range maxSpotlightBundles + 5 {
		hits = append(hits, filepath.Join(root, "Elsewhere", "App"+itoa(i)+".app"))
	}
	long.records[mdfindQuery] = probe.Result{Stdout: strings.Join(hits, "\n") + "\n"}
	// A hit that cannot be stat'd is kept, which lets the test reach the
	// cap without building two hundred bundles.
	long.statErr = func(string) error { return syscall.EPERM }
	p = long.prober()
	p.spotlight(context.Background(), map[string]bool{})
	if len(p.f.Spotlight) != maxSpotlightBundles {
		t.Errorf("read %d Spotlight bundles, want the cap of %d", len(p.f.Spotlight), maxSpotlightBundles)
	}
	deg, ok := degradationFor(p.f, probeSpotlight)
	if !ok || !strings.Contains(deg.Reason, itoa(maxSpotlightBundles)) {
		t.Errorf("degradations = %q, want the cap named", joinDegradations(p.f))
	}

	// Exactly the cap is a complete answer.
	exact := newProbeFixture(t)
	exact.records[mdfindQuery] = probe.Result{Stdout: strings.Join(hits[:maxSpotlightBundles], "\n") + "\n"}
	exact.statErr = long.statErr
	p = exact.prober()
	p.spotlight(context.Background(), map[string]bool{})
	if _, ok := degradationFor(p.f, probeSpotlight); ok {
		t.Errorf("an answer of exactly %d was called incomplete: %s", maxSpotlightBundles, joinDegradations(p.f))
	}
}

// TestAnUnparseableLaunchItemCapsItsOwnersOrphan is the launch item that was
// neither a keep signal nor a gap. A plist that would not read or decode came
// back with no program and no error, so the verdicts saw an item they could
// neither count as running nor count as unchecked — and the owner it belonged
// to was an orphan with a job definition for it still installed.
func TestAnUnparseableLaunchItemCapsItsOwnersOrphan(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/Vivaldi")

	pf := newProbeFixture(t)
	agents := filepath.Join(pf.home, "Library", "LaunchAgents")
	writeFile(t, filepath.Join(agents, "com.vivaldi.Vivaldi.plist"), make([]byte, 220))
	p := pf.prober()
	p.launchItems()

	if len(p.f.LaunchItems) != 1 {
		t.Fatalf("launch items = %+v, want the one plist", p.f.LaunchItems)
	}
	if item := p.f.LaunchItems[0]; item.CheckErr == "" {
		t.Errorf("an unparseable plist carries no error: %+v", item)
	}

	v := verdictByLabel(t, vf.analyze(t, p.f, Options{}), "Vivaldi")
	if v.State != StateUnknown {
		t.Errorf("Vivaldi with an unreadable launch item = %v, want unknown", v.State)
	}
	if !strings.Contains(strings.Join(v.Evidence, " "), "com.vivaldi.Vivaldi") {
		t.Errorf("evidence = %v, want it to name the launch item", v.Evidence)
	}
}

// TestALaunchItemWithNoProgramIsUnchecked is the same rule for a plist that
// parsed but named no program this parser understands. Whether it still runs
// is unknown, and unknown is not absent.
func TestALaunchItemWithNoProgramIsUnchecked(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/Vivaldi")
	v := verdictByLabel(t, vf.analyze(t, &Facts{LaunchItems: []LaunchItem{{
		Path: "/Library/LaunchAgents/com.vivaldi.Vivaldi.plist", Label: "com.vivaldi.Vivaldi",
	}}}, Options{}), "Vivaldi")
	if v.State != StateUnknown {
		t.Errorf("Vivaldi with a program-less launch item = %v, want unknown", v.State)
	}
}
