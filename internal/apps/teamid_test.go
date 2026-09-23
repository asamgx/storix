package apps

import (
	"context"
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
