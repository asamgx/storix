package xcode_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/xcode"
	"github.com/asamgx/storix/internal/probe"
)

const home = detecttest.Home

// tree is the set of Xcode directories a fixture gets. The paths are display
// paths, so "/Library/Developer" is the shared one and "/Users/andrew/..."
// the per-user ones.
func tree(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		home + "/Library/Developer/Xcode/DerivedData/App-abc/Build/Products/x":                        400_000,
		home + "/Library/Developer/Xcode/Archives/2026-01-01/App.xcarchive/dSYMs/x":                   300_000,
		home + "/Library/Developer/Xcode/iOS DeviceSupport/18.2/Symbols/x":                            200_000,
		home + "/Library/Developer/Xcode/UserData/Previews/Simulator Devices/x":                       100_000,
		home + "/Library/Developer/CoreSimulator/Devices/11111111-1111-1111-1111-111111111111/data/x": 90_000,
		home + "/Library/Developer/CoreSimulator/Devices/22222222-2222-2222-2222-222222222222/data/x": 80_000,
		home + "/Library/Developer/CoreSimulator/Caches/dyld/x":                                       70_000,
		home + "/Library/Developer/XCTestDevices/x":                                                   60_000,
		home + "/Library/Caches/com.apple.dt.Xcode/x":                                                 50_000,
		"/Library/Developer/CommandLineTools/usr/bin/x":                                               40_000,
		"/Library/Developer/CoreSimulator/Images/A1.dmg":                                              30_000,
		"/Library/Developer/CoreSimulator/Cryptex/x":                                                  20_000,
		"/Library/Developer/CoreSimulator/Profiles/Runtimes/x":                                        10_000,
		"/Library/Developer/CoreDevice/x":                                                             9_000,
		"/Library/Developer/DeveloperDiskImages/x":                                                    8_000,
		"/Library/Developer/DeviceKit/x":                                                              7_000,
		"/Library/Developer/PrivateFrameworks/x":                                                      6_000,
	})
}

// envFor builds an environment whose commands come from a fixture and whose
// filesystem answers only for the paths named.
//
// The Stat override is what makes the test independent of the machine it runs
// on: the detector asks whether /Applications/Xcode.app exists, and a test
// that consulted the real /Applications would pass on a developer's laptop
// and fail in CI for reasons that have nothing to do with the code.
func envFor(t *testing.T, f *detecttest.Fixture, fixture string, exists ...string) (detect.Env, *probe.Recorder) {
	t.Helper()
	env := f.Env(t, fixture)
	rec := probe.NewRecorder(env.Runner)
	env.Runner = rec

	set := make(map[string]bool, len(exists))
	for _, p := range exists {
		set[p] = true
	}
	env.Stat = func(p string) (detect.FileInfo, error) {
		if set[p] {
			return detect.FileInfo{Name: p, IsDir: true, Mode: fs.ModeDir}, nil
		}
		return detect.FileInfo{}, fs.ErrNotExist
	}
	return env, rec
}

// ranXcrun reports whether any recorded command was xcrun.
func ranXcrun(rec *probe.Recorder) bool {
	for _, r := range rec.Records() {
		if r.Cmd.Name == "xcrun" {
			return true
		}
	}
	return false
}

// TestSimctlIsNeverRunWhenTheGateFails is the test this detector exists for.
//
// Running simctl on a machine whose Xcode has never been opened installs the
// CoreSimulator components — gigabytes written by a program that promises
// only to measure. The gate is the whole defence, and the assertion is made
// on the recorded command list rather than on the facts, because what matters
// is not what the detector concluded but what it ran.
func TestSimctlIsNeverRunWhenTheGateFails(t *testing.T) {
	f := tree(t)
	env, rec := envFor(t, f, "testdata/gate-failed.json", "/Applications/Xcode.app")

	facts, err := detecttest.Probe(t, xcode.New(), env)
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if ranXcrun(rec) {
		t.Fatal("simctl was invoked although the first-launch gate failed; this installs components")
	}

	got := facts.(*xcode.Facts)
	if !got.GateRun {
		t.Error("the gate command was not run at all")
	}
	if got.GatePassed {
		t.Error("the gate was recorded as passed on a non-zero exit")
	}
	if !strings.Contains(err.Error(), "simctl skipped") {
		t.Errorf("the degradation reason does not say simctl was skipped: %v", err)
	}
	if !strings.Contains(got.Verdict(), "gate failed") {
		t.Errorf("verdict = %q, want it to report the failure", got.Verdict())
	}

	// The paths are still claimed: a failed gate costs the evidence, not
	// the bucket.
	claims, _ := xcode.New().Classify(f.Tree, facts, f.Context)
	if len(claims) == 0 {
		t.Error("no claims although the directories are there")
	}
}

// TestGateRunsUnderTheXcodeDeveloperDir: the gate is meaningless unless it is
// asked about the Xcode the simulators belong to, so the DEVELOPER_DIR is
// part of the contract and is asserted on the recorded command.
func TestGateRunsUnderTheXcodeDeveloperDir(t *testing.T) {
	f := tree(t)
	env, rec := envFor(t, f, "testdata/gate-failed.json", "/Applications/Xcode.app")
	if _, err := detecttest.Probe(t, xcode.New(), env); err == nil {
		t.Fatal("want a degradation")
	}

	var gate *probe.Record
	for i, r := range rec.Records() {
		if r.Cmd.Name == "xcodebuild" {
			gate = &rec.Records()[i]
		}
	}
	if gate == nil {
		t.Fatal("xcodebuild was never run")
	}
	if len(gate.Cmd.Args) != 1 || gate.Cmd.Args[0] != "-checkFirstLaunchStatus" {
		t.Errorf("gate args = %v, want only -checkFirstLaunchStatus", gate.Cmd.Args)
	}
	want := "DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer"
	if len(gate.Cmd.Env) != 1 || gate.Cmd.Env[0] != want {
		t.Errorf("gate env = %v, want %q", gate.Cmd.Env, want)
	}
}

// TestNoXcodebuildSkipsSimctlToo: without xcodebuild the gate cannot be asked,
// and an unaskable gate is a closed one.
func TestNoXcodebuildSkipsSimctl(t *testing.T) {
	f := tree(t)
	// The fixture names no commands, so LookPath fails for everything.
	env, rec := envFor(t, f, "testdata/missing.json", "/Applications/Xcode.app")

	facts, err := detecttest.Probe(t, xcode.New(), env)
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if ranXcrun(rec) {
		t.Fatal("simctl was invoked without the gate having been asked")
	}
	if got := facts.(*xcode.Facts); got.GateRun {
		t.Error("the gate is recorded as run although xcodebuild is absent")
	}
}

// TestGateTimeoutSkipsSimctl: a gate that did not answer has not said yes.
func TestGateTimeoutSkipsSimctl(t *testing.T) {
	f := tree(t)
	env, rec := envFor(t, f, "testdata/timeout.json", "/Applications/Xcode.app")

	facts, err := detecttest.Probe(t, xcode.New(), env)
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if ranXcrun(rec) {
		t.Fatal("simctl was invoked after the gate timed out")
	}
	if got := facts.(*xcode.Facts); got.GatePassed {
		t.Error("a timed-out gate was treated as passed")
	}
}

// TestNoXcodeApp: with only the command line tools installed there is nothing
// to gate, so the gate is not run and neither is simctl.
func TestNoXcodeApp(t *testing.T) {
	f := tree(t)
	env, rec := envFor(t, f, "testdata/gate-failed.json") // no Xcode.app exists

	facts, err := detecttest.Probe(t, xcode.New(), env)
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if ranXcrun(rec) {
		t.Fatal("simctl was invoked with no Xcode installed")
	}
	for _, r := range rec.Records() {
		if r.Cmd.Name == "xcodebuild" {
			t.Fatal("the gate was run with no Xcode to gate")
		}
	}
	if got := facts.(*xcode.Facts); got.App != "" {
		t.Errorf("App = %q, want none", got.App)
	}
}

// TestThisMachine replays what this machine answered: the command line tools
// are selected, Xcode is installed at the default location, and the gate
// passes, so simctl runs and reports no simulators.
func TestThisMachine(t *testing.T) {
	f := tree(t)
	env, rec := envFor(t, f, "testdata/this-machine.json", "/Applications/Xcode.app")

	facts, err := detecttest.Probe(t, xcode.New(), env)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*xcode.Facts)
	if got.Selected != "/Library/Developer/CommandLineTools" {
		t.Errorf("selected = %q", got.Selected)
	}
	if got.App != "/Applications/Xcode.app" {
		t.Errorf("app = %q, want the default location", got.App)
	}
	if !got.GatePassed {
		t.Fatalf("the gate failed on the recording that had it pass: %q", got.GateReason)
	}
	if !ranXcrun(rec) {
		t.Error("the gate passed but simctl was never asked")
	}
	if len(got.Devices) != 0 {
		t.Errorf("devices = %v, want none on this machine", got.Devices)
	}
}

// TestUnverified: the simctl half has never run against a machine with
// simulators, and the status has to say so.
func TestUnverified(t *testing.T) {
	u, ok := any(xcode.New()).(detect.Unverified)
	if !ok || !u.Unverified() {
		t.Error("the detector does not mark itself unverified")
	}
}

// TestSimulators covers the one thing simctl adds that the tree cannot: which
// devices have lost their runtime and are therefore recoverable space.
func TestSimulators(t *testing.T) {
	f := tree(t)
	env, _ := envFor(t, f, "testdata/simulators.json", "/Applications/Xcode.app")

	facts, err := detecttest.Probe(t, xcode.New(), env)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*xcode.Facts)
	if len(got.Devices) != 2 {
		t.Fatalf("devices = %+v, want two", got.Devices)
	}
	if !got.Devices[0].Available || got.Devices[1].Available {
		t.Errorf("availability = %v, %v; want the iOS 16 device unavailable",
			got.Devices[0].Available, got.Devices[1].Available)
	}
	if len(got.Runtimes) != 1 || got.Runtimes[0].Version != "18.2" {
		t.Errorf("runtimes = %+v, want the one 18.2 runtime", got.Runtimes)
	}

	claims, _ := xcode.New().Classify(f.Tree, facts, f.Context)
	const devices = home + "/Library/Developer/CoreSimulator/Devices/"
	live, ok := detecttest.ClaimAt(claims, devices+"11111111-1111-1111-1111-111111111111")
	if !ok {
		t.Fatal("the available simulator was not claimed")
	}
	if live.Reclaim != classify.ToolManaged {
		t.Errorf("available simulator reclaim = %s, want tool-managed", live.Reclaim)
	}
	dead, ok := detecttest.ClaimAt(claims, devices+"22222222-2222-2222-2222-222222222222")
	if !ok {
		t.Fatal("the unavailable simulator was not claimed")
	}
	if dead.Reclaim != classify.Regenerable {
		t.Errorf("unavailable simulator reclaim = %s, want regenerable", dead.Reclaim)
	}
	if !strings.Contains(dead.Category, "Simulator") {
		t.Errorf("category = %q", dead.Category)
	}
}

// TestMalformedSimctlOutput: unreadable JSON costs the simulator rows and
// nothing else.
func TestMalformedSimctlOutput(t *testing.T) {
	f := tree(t)
	env, _ := envFor(t, f, "testdata/malformed.json", "/Applications/Xcode.app")

	facts, err := detecttest.Probe(t, xcode.New(), env)
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	got := facts.(*xcode.Facts)
	if !got.GatePassed {
		t.Error("the gate passed in the fixture but not in the facts")
	}
	if len(got.Devices) != 0 {
		t.Errorf("devices = %+v, want none from unreadable output", got.Devices)
	}
	claims, sum := xcode.New().Classify(f.Tree, facts, f.Context)
	if len(claims) == 0 || sum.Empty() {
		t.Error("the directories stopped being claimed because the JSON was bad")
	}
}

// TestClassifyPaths walks the docs/04 list and asserts each path's bucket,
// owner key and reclaim tag.
func TestClassifyPaths(t *testing.T) {
	f := tree(t)
	env, _ := envFor(t, f, "testdata/this-machine.json", "/Applications/Xcode.app")
	facts, err := detecttest.Probe(t, xcode.New(), env)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := xcode.New().Classify(f.Tree, facts, f.Context)

	want := []struct {
		path    string
		reclaim classify.Reclaim
	}{
		{home + "/Library/Developer/Xcode/DerivedData", classify.Regenerable},
		{home + "/Library/Developer/Xcode/iOS DeviceSupport", classify.ToolManaged},
		{home + "/Library/Developer/Xcode/UserData/Previews", classify.Regenerable},
		{home + "/Library/Developer/CoreSimulator", classify.ToolManaged},
		{home + "/Library/Developer/CoreSimulator/Caches", classify.Regenerable},
		{home + "/Library/Developer/XCTestDevices", classify.Regenerable},
		{home + "/Library/Caches/com.apple.dt.Xcode", classify.Regenerable},
		{"/Library/Developer/CommandLineTools", classify.ToolManaged},
		{"/Library/Developer/CoreSimulator/Images", classify.ToolManaged},
		{"/Library/Developer/CoreSimulator/Cryptex", classify.ToolManaged},
		{"/Library/Developer/CoreSimulator/Profiles", classify.ToolManaged},
		{"/Library/Developer/CoreDevice", classify.ToolManaged},
		{"/Library/Developer/DeveloperDiskImages", classify.ToolManaged},
		{"/Library/Developer/DeviceKit", classify.ToolManaged},
		{"/Library/Developer/PrivateFrameworks", classify.ToolManaged},
	}
	for _, w := range want {
		c, ok := detecttest.ClaimAt(claims, w.path)
		if !ok {
			t.Errorf("no claim at %s", w.path)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", w.path, c.Bucket)
		}
		if c.Reclaim != w.reclaim {
			t.Errorf("%s reclaim = %s, want %s", w.path, c.Reclaim, w.reclaim)
		}
		if !detecttest.HasKey(c, "app:com.apple.dt.Xcode") {
			t.Errorf("%s has no Xcode owner key: %v", w.path, c.OwnerKeys)
		}
		if c.Source.Detector != "xcode" {
			t.Errorf("%s source = %s", w.path, c.Source)
		}
	}

	// Archives are a backup of a build and belong to the backups detector,
	// which is the one that puts them in the Backups bucket.
	if _, ok := detecttest.ClaimAt(claims, home+"/Library/Developer/Xcode/Archives"); ok {
		t.Error("xcode claimed the archives; they belong to the backups detector")
	}
	if _, ok := detecttest.Tool(sum, home+"/Library/Developer/Xcode/DerivedData"); !ok {
		t.Error("DerivedData has no summary row")
	}
}

// TestNothingInstalled: no Xcode, no tools, no /Library/Developer is Missing,
// which is an answer and not a failure.
func TestNothingInstalled(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	env, rec := envFor(t, f, "testdata/missing.json")

	if _, err := detecttest.Probe(t, xcode.New(), env); !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	if len(rec.Records()) != 0 {
		t.Errorf("commands were run with nothing installed: %v", rec.Records())
	}
}
