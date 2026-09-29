package apps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/asamgx/storix/internal/probe"
)

// fakeRunner answers codesign from a table and counts what it was asked.
type fakeRunner struct {
	mu sync.Mutex
	// byPath maps a bundle path suffix to the team id to report. A path
	// with no entry is reported as unsigned, which is what codesign does.
	byPath map[string]string
	calls  []string
	// fail makes every invocation report the command as missing.
	fail bool
}

func (f *fakeRunner) Run(_ context.Context, c probe.Cmd) probe.Result {
	f.mu.Lock()
	f.calls = append(f.calls, c.Key())
	f.mu.Unlock()
	if f.fail {
		return probe.Result{Missing: true}
	}
	team := ""
	for suffix, t := range f.byPath {
		if strings.HasSuffix(c.Key(), suffix) {
			team = t
			break
		}
	}
	// codesign writes its report to stderr, which is why the resolver
	// reads that stream and not stdout.
	out := "Executable=x\nIdentifier=y\n"
	if team == "" {
		out += "TeamIdentifier=not set\n"
	} else {
		out += "TeamIdentifier=" + team + "\n"
	}
	return probe.Result{Stderr: out}
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testBundles() []*Bundle {
	return []*Bundle{
		{BundleInfo: BundleInfo{Path: "/Applications/OrbStack.app", ID: "dev.kdrag0n.MacVirt", Version: "1.0"}, Source: SourceApplications},
		{BundleInfo: BundleInfo{Path: "/Applications/ChatGPT.app", ID: "com.openai.codex", Version: "2.0"}, Source: SourceApplications},
		{BundleInfo: BundleInfo{Path: "/Applications/Calculator.app", ID: "com.apple.calculator", Version: "1.0"}, Source: SourceApplications},
	}
}

func TestParseCodesignTeamID(t *testing.T) {
	t.Parallel()
	// Verbatim shape of "codesign -dv --verbose=4 /Applications/ChatGPT.app".
	const out = `Executable=/Applications/ChatGPT.app/Contents/MacOS/ChatGPT
Identifier=com.openai.codex
Format=app bundle with Mach-O thin (arm64)
Authority=Developer ID Application: OpenAI OpCo, LLC (2DC432GLL2)
TeamIdentifier=2DC432GLL2
Sealed Resources version=2
`
	if got := ParseCodesignTeamID(out); got != "2DC432GLL2" {
		t.Errorf("ParseCodesignTeamID = %q, want 2DC432GLL2", got)
	}
	if got := ParseCodesignTeamID("TeamIdentifier=not set\n"); got != "" {
		t.Errorf("an unsigned bundle yielded %q, want empty", got)
	}
	if got := ParseCodesignTeamID("nothing useful here"); got != "" {
		t.Errorf("unrecognised output yielded %q", got)
	}
}

func TestTeamResolverResolves(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{byPath: map[string]string{
		"OrbStack.app": "HUAQ24HBR6",
		"ChatGPT.app":  "2DC432GLL2",
	}}
	r := NewTeamResolver(runner, "")
	bundles := testBundles()

	byTeam := r.Resolve(context.Background(), bundles)
	if got := byTeam["HUAQ24HBR6"]; len(got) != 1 || got[0].ID != "dev.kdrag0n.MacVirt" {
		t.Errorf("HUAQ24HBR6 = %v", got)
	}
	if got := byTeam["2DC432GLL2"]; len(got) != 1 || got[0].ID != "com.openai.codex" {
		t.Errorf("2DC432GLL2 = %v", got)
	}
	// An unsigned bundle is asked about once and then left alone.
	if _, ok := byTeam[""]; ok {
		t.Error("the empty team id should not be a group")
	}
	if got := r.Calls(); got != 3 {
		t.Errorf("codesign ran %d times, want 3", got)
	}
}

// TestTeamResolverCacheRoundTrip is the acceptance criterion in one test: the
// first run pays for the machine, the second pays nothing.
func TestTeamResolverCacheRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "storix", "teamids.json")
	runner := &fakeRunner{byPath: map[string]string{"OrbStack.app": "HUAQ24HBR6"}}

	first := NewTeamResolver(runner, path)
	if err := first.Load(); err == nil {
		t.Log("no cache file yet, as expected on a first run")
	}
	first.Resolve(context.Background(), testBundles())
	if err := first.Save(); err != nil {
		t.Fatalf("saving the cache: %v", err)
	}
	firstCalls := runner.count()
	if firstCalls != 3 {
		t.Fatalf("first run invoked codesign %d times, want 3", firstCalls)
	}

	second := NewTeamResolver(runner, path)
	if err := second.Load(); err != nil {
		t.Fatalf("loading the cache: %v", err)
	}
	byTeam := second.Resolve(context.Background(), testBundles())
	if got := second.Calls(); got != 0 {
		t.Errorf("second run invoked codesign %d times, want 0", got)
	}
	if runner.count() != firstCalls {
		t.Errorf("the runner saw %d calls in total, want %d", runner.count(), firstCalls)
	}
	if got := byTeam["HUAQ24HBR6"]; len(got) != 1 {
		t.Errorf("the cached team id did not come back: %v", byTeam)
	}
}

// TestTeamResolverVersionChange makes sure an updated application is asked
// about again: the cache is keyed by identity and version together.
func TestTeamResolverVersionChange(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "teamids.json")
	runner := &fakeRunner{byPath: map[string]string{"OrbStack.app": "HUAQ24HBR6"}}

	r := NewTeamResolver(runner, path)
	r.Resolve(context.Background(), []*Bundle{
		{BundleInfo: BundleInfo{Path: "/Applications/OrbStack.app", ID: "dev.kdrag0n.MacVirt", Version: "1.0"}},
	})
	if err := r.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	next := NewTeamResolver(runner, path)
	if err := next.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	next.Resolve(context.Background(), []*Bundle{
		{BundleInfo: BundleInfo{Path: "/Applications/OrbStack.app", ID: "dev.kdrag0n.MacVirt", Version: "2.0"}},
	})
	if got := next.Calls(); got != 1 {
		t.Errorf("an updated bundle was asked about %d times, want 1", got)
	}
}

func TestTeamResolverDegraded(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{fail: true}
	r := NewTeamResolver(runner, "")
	byTeam := r.Resolve(context.Background(), testBundles())
	if len(byTeam) != 0 {
		t.Errorf("a failing codesign resolved %d teams, want none", len(byTeam))
	}
}

func TestTeamResolverNilRunner(t *testing.T) {
	t.Parallel()
	r := NewTeamResolver(nil, "")
	if byTeam := r.Resolve(context.Background(), testBundles()); len(byTeam) != 0 {
		t.Errorf("a nil runner resolved %d teams", len(byTeam))
	}
	if r.Calls() != 0 {
		t.Error("a nil runner must invoke nothing")
	}
}

// TestNeedsTeamIDs is the gate that keeps codesign from running at all. The
// names are this machine's real group containers.
func TestNeedsTeamIDs(t *testing.T) {
	t.Parallel()
	appleOnly := []string{
		"com.apple.bird", "group.com.apple.notes", "243LU875E5.groups.com.apple.podcasts",
		"74J34U3R6X.com.apple.iWork", "group.net.whatsapp.WhatsApp.shared",
		"--AppIdentifierPrefix-localsend.shared_group",
	}
	if NeedsTeamIDs(appleOnly) {
		t.Error("Apple's own team-id containers must not open the codesign gate")
	}
	withThirdParty := append(append([]string{}, appleOnly...), "HUAQ24HBR6.dev.orbstack")
	if !NeedsTeamIDs(withThirdParty) {
		t.Error("a third-party team-id container should open the gate")
	}
}

func TestTeamResolverSeed(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	r := NewTeamResolver(runner, "")
	r.Seed(map[string]string{"dev.kdrag0n.MacVirt@1.0": "HUAQ24HBR6"})

	byTeam := r.Resolve(context.Background(), []*Bundle{
		{BundleInfo: BundleInfo{Path: "/Applications/OrbStack.app", ID: "dev.kdrag0n.MacVirt", Version: "1.0"}, Source: SourceApplications},
	})
	if r.Calls() != 0 {
		t.Errorf("seeded facts still cost %d codesign runs", r.Calls())
	}
	if len(byTeam["HUAQ24HBR6"]) != 1 {
		t.Errorf("the seeded team id was not used: %v", byTeam)
	}
}

// TestTheTeamCacheIsNotWrittenThroughASymlink covers the one file this package
// writes, which it may be writing as root under sudo.
//
// The old write put the document in "<path>.tmp" with os.WriteFile. That name
// is predictable, so anyone who can create a file in the directory could get
// there first with a symlink and have a privileged storix write the cache
// through it, into a file of their choosing. A symlink left at the cache path
// itself is the same trick one step later.
func TestTheTeamCacheIsNotWrittenThroughASymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "teamids.json")
	victim := filepath.Join(dir, "victim")
	const untouched = "the contents of a file storix was never asked to write"
	if err := os.WriteFile(victim, []byte(untouched), 0o644); err != nil {
		t.Fatal(err)
	}

	// Both of the names the write could have been steered through.
	if err := os.Symlink(victim, cachePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, cachePath+".tmp"); err != nil {
		t.Fatal(err)
	}

	r := NewTeamResolver(nil, cachePath)
	r.cache["com.example.app@1.0"] = "ABCDE12345"
	r.dirty = true
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got, err := os.ReadFile(victim); err != nil || string(got) != untouched {
		t.Errorf("the linked-to file was written through: %q, %v", got, err)
	}
	fi, err := os.Lstat(cachePath)
	if err != nil {
		t.Fatalf("the cache was not written: %v", err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("the cache path is still a %v, so the rename followed the link", fi.Mode().Type())
	}
	if perm := fi.Mode().Perm(); perm != teamCacheMode {
		t.Errorf("mode = %o, want %o", perm, teamCacheMode)
	}

	// The written file is the cache and nothing else.
	reloaded := NewTeamResolver(nil, cachePath)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.cache["com.example.app@1.0"] != "ABCDE12345" {
		t.Errorf("the cache did not round-trip: %v", reloaded.cache)
	}
}

// TestTheTeamCacheIsNotReadThroughASymlink is the other direction. A link left
// where the cache belongs must not make a privileged reader open something
// else, however little it would learn from the contents.
func TestTheTeamCacheIsNotReadThroughASymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "teamids.json")
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte(`{"version":1,"teams":{"a@1":"PLANTED123"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, cachePath); err != nil {
		t.Fatal(err)
	}

	r := NewTeamResolver(nil, cachePath)
	err := r.Load()
	if err == nil {
		t.Fatal("Load followed a symlink at the cache path")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error = %v, want it to name the reason", err)
	}
	if len(r.cache) != 0 {
		t.Errorf("the planted file was loaded: %v", r.cache)
	}
}

// TestSaveLeavesNoTemporaryFileBehind keeps the directory clean whichever way
// the write went, so a half-written cache cannot be mistaken for one later.
func TestSaveLeavesNoTemporaryFileBehind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := NewTeamResolver(nil, filepath.Join(dir, "teamids.json"))
	r.cache["com.example.app@1.0"] = "ABCDE12345"
	r.dirty = true
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "teamids.json" {
			t.Errorf("Save left %q behind", e.Name())
		}
	}
}
