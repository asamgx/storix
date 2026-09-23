package apps

import (
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/walk"
)

// verdictFixture builds a tiny tree with one data directory and runs the
// analysis over it, so a verdict can be exercised without the whole corpus.
type verdictFixture struct {
	tree *walk.Tree
	home string
	root string
}

func newVerdictFixture(t *testing.T, dirs ...string) *verdictFixture {
	t.Helper()
	f := testutil.New(t)
	for _, d := range dirs {
		f.File(d+"/data.bin", 4096)
	}
	tree, err := walk.Walk(t.Context(), walk.Options{Root: f.Root})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return &verdictFixture{tree: tree, home: f.Root + "/Users/andrewsam", root: f.Root}
}

// verdictFor finds an owner's verdict by the label the report prints, which
// keeps a test readable when the owner key depends on whether a bundle for
// the product happened to be found.
func verdictByLabel(t *testing.T, a *Analysis, label string) *Verdict {
	t.Helper()
	for _, key := range a.OwnerKeys() {
		if a.Owners[key].Owner.Label == label {
			return a.Verdicts[key]
		}
	}
	t.Fatalf("no owner labelled %q; owners are %v", label, a.OwnerKeys())
	return nil
}

func (vf *verdictFixture) analyze(t *testing.T, f *Facts, opts Options) *Analysis {
	t.Helper()
	opts.Root = vf.root
	if opts.Now.IsZero() {
		opts.Now = time.Now().Add(365 * 24 * time.Hour)
	}
	return Analyze(vf.tree, f, classify.Context{Home: vf.home}, opts)
}

// TestVerdictRecentWriteBlocksOrphan is the keep signal that protects a tool
// a background agent still writes to. Data written yesterday is not orphaned
// however absent its bundle is, and the report says so rather than staying
// silent about why.
func TestVerdictRecentWriteBlocksOrphan(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/dev.warp.Warp-Stable")

	// The fixture was written moments ago, so a clock of "now" leaves the
	// data inside the recency window.
	a := vf.analyze(t, &Facts{}, Options{Now: time.Now()})
	v := verdictByLabel(t, a, "Warp")
	if v.State != StateUnknown {
		t.Errorf("State = %v, want unknown while the data is fresh", v.State)
	}
	if len(v.Keep) == 0 {
		t.Error("the recency keep signal was not recorded")
	}
	if !strings.Contains(strings.Join(v.Keep, " "), "written") {
		t.Errorf("keep signals = %v", v.Keep)
	}

	// The same tree a year on is an orphan, which is the point of the
	// window being a window rather than a rule.
	later := vf.analyze(t, &Facts{}, Options{Now: time.Now().Add(365 * 24 * time.Hour)})
	if got := verdictByLabel(t, later, "Warp").State; got != StateOrphanLikely {
		t.Errorf("State after the window = %v, want orphan-likely", got)
	}
}

// TestVerdictLiveLaunchItemBlocksOrphan covers the Autodesk shape: the
// application is gone but a daemon is still configured to run, so something
// is installed even though no bundle carries the name.
func TestVerdictLiveLaunchItemBlocksOrphan(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/dev.warp.Warp-Stable")

	a := vf.analyze(t, &Facts{LaunchItems: []LaunchItem{{
		Path:          "/Library/LaunchDaemons/dev.warp.Warp-Stable.plist",
		Label:         "dev.warp.Warp-Stable",
		Program:       "/Library/PrivilegedHelperTools/warp",
		ProgramExists: true,
		System:        true,
	}}}, Options{})

	v := verdictByLabel(t, a, "Warp")
	if v.State != StateInstalled {
		t.Fatalf("State = %v, want installed; keep %v", v.State, v.Keep)
	}
	if !strings.Contains(strings.Join(v.Keep, " "), "runs /Library/PrivilegedHelperTools/warp") {
		t.Errorf("keep signals = %v", v.Keep)
	}
}

// TestVerdictDeadLaunchItemIsNotAKeepSignal is the keystone case. An item
// whose program cannot be determined, or points at something that is gone,
// must not protect an orphan: treating "I could not tell" as "it is alive"
// would suppress every real orphan on a machine with stale launch agents.
func TestVerdictDeadLaunchItemIsNotAKeepSignal(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/dev.warp.Warp-Stable")

	a := vf.analyze(t, &Facts{LaunchItems: []LaunchItem{
		{Path: "/Library/LaunchAgents/dev.warp.Warp-Stable.plist", Label: "dev.warp.Warp-Stable"},
		{Path: "/Library/LaunchAgents/dev.warp.other.plist", Label: "dev.warp.other",
			Program: "/gone/binary", ProgramExists: false},
	}}, Options{})

	if got := verdictByLabel(t, a, "Warp").State; got != StateOrphanLikely {
		t.Errorf("State = %v, want orphan-likely", got)
	}
}

// TestVerdictLiveReceiptBlocksOrphan is the other half of the GlobalProtect
// reasoning: a receipt whose install location still exists means the software
// is there, whatever the directory name suggests.
func TestVerdictLiveReceiptBlocksOrphan(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/PaloAltoNetworks")

	a := vf.analyze(t, &Facts{Receipts: []Receipt{{
		PkgID:          "com.paloaltonetworks.globalprotect.pkg",
		Volume:         "/",
		Location:       "Applications/GlobalProtect.app",
		LocationExists: true,
	}}}, Options{})

	v := verdictByLabel(t, a, "GlobalProtect")
	if v.State != StateInstalled {
		t.Fatalf("State = %v, want installed; keep %v", v.State, v.Keep)
	}
}

// TestVerdictStagedUpdateCopyIsStillAnOrphan is the precondition R9 states.
// A self-updating application stages a copy of itself inside its own cache;
// counting that copy as an installation would hide the orphaned data of every
// application that updates this way.
func TestVerdictStagedUpdateCopyIsStillAnOrphan(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/dev.warp.Warp-Stable")

	a := vf.analyze(t, &Facts{Spotlight: []BundleInfo{{
		Path:        vf.home + "/Library/Caches/dev.warp.Warp-Stable/Updates/1.0/Warp.app",
		ID:          "dev.warp.Warp-Stable",
		DisplayName: "Warp",
	}}}, Options{})

	v := verdictByLabel(t, a, "Warp")
	if v.State != StateOrphanLikely {
		t.Fatalf("State = %v, want orphan-likely", v.State)
	}
	if !strings.Contains(strings.Join(v.Evidence, " "), "staged update") {
		t.Errorf("evidence does not name the staged copy: %v", v.Evidence)
	}
}

// TestVerdictUnconventionalInstallIsNotAnOrphan is the same backstop from the
// other side: a bundle in a JetBrains Toolbox directory or a Downloads folder
// is an installation, just not a tidy one.
func TestVerdictUnconventionalInstallIsNotAnOrphan(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/dev.warp.Warp-Stable")

	a := vf.analyze(t, &Facts{Spotlight: []BundleInfo{{
		Path:        vf.home + "/Library/Application Support/JetBrains/Toolbox/apps/Warp.app",
		ID:          "dev.warp.Warp-Stable",
		DisplayName: "Warp",
	}}}, Options{})

	if got := verdictByLabel(t, a, "Warp").State; got != StateInstalled {
		t.Errorf("State = %v, want installed", got)
	}
}

// TestVerdictOrphanConfidence pins the difference the report prints as
// "likely" against "possible".
func TestVerdictOrphanConfidence(t *testing.T) {
	t.Parallel()

	// A directory named after a bundle identifier: only an installation
	// creates one, so the orphan is likely.
	byID := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/so.cap.desktop")
	if got := verdictByLabel(t, byID.analyze(t, &Facts{}, Options{}), "Cap"); got.Confidence != classify.Likely {
		t.Errorf("identifier-named directory gave %v, want likely", got.Confidence)
	}

	// A directory named after a product: a guess, labelled as one.
	byName := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/TabNine")
	if got := verdictByLabel(t, byName.analyze(t, &Facts{}, Options{}), "TabNine"); got.Confidence != classify.Corroborating {
		t.Errorf("name-only directory gave %v, want corroborating", got.Confidence)
	}
}

// TestVerdictStaleRegistryMakesItLikely covers the third upgrade path: macOS
// still lists the application at a path that is gone.
func TestVerdictStaleRegistryMakesItLikely(t *testing.T) {
	t.Parallel()
	vf := newVerdictFixture(t, "Users/andrewsam/Library/Application Support/TabNine")

	a := vf.analyze(t, &Facts{Registry: []RegistryEntry{{
		ID: "com.tabnine.TabNine", Path: "/Applications/TabNine.app", Exists: false,
	}}}, Options{})

	v := verdictByLabel(t, a, "TabNine")
	if v.Confidence != classify.Likely {
		t.Errorf("Confidence = %v, want likely", v.Confidence)
	}
	if !strings.Contains(strings.Join(v.Evidence, " "), "LaunchServices") {
		t.Errorf("evidence does not cite the stale registration: %v", v.Evidence)
	}
}

// TestTuningLogGoesToTheInjectedWriter checks both halves of the contract:
// the large unknowns are recorded, and nothing is written when no writer was
// supplied, so being tested never touches the user's home.
func TestTuningLogGoesToTheInjectedWriter(t *testing.T) {
	t.Parallel()
	f := testutil.New(t)
	f.File("Users/andrewsam/Library/Caches/SomethingNobodyKnows/big.bin", 200<<10)
	f.File("Users/andrewsam/Library/Caches/AlsoUnknownButTiny/small.bin", 64)
	tree, err := walk.Walk(t.Context(), walk.Options{Root: f.Root})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	var log strings.Builder
	opts := Options{
		Root:            f.Root,
		Now:             time.Now().Add(365 * 24 * time.Hour),
		TuningLog:       &log,
		TuningThreshold: 100 << 10,
	}
	Analyze(tree, &Facts{}, classify.Context{Home: f.Root + "/Users/andrewsam"}, opts)

	out := log.String()
	if !strings.Contains(out, "unknown:SomethingNobodyKnows") {
		t.Errorf("the large unknown was not logged:\n%s", out)
	}
	if strings.Contains(out, "AlsoUnknownButTiny") {
		t.Errorf("an unknown below the threshold was logged:\n%s", out)
	}

	// Without a writer nothing is recorded anywhere.
	opts.TuningLog = nil
	Analyze(tree, &Facts{}, classify.Context{Home: f.Root + "/Users/andrewsam"}, opts)
}

func TestStateReclaimable(t *testing.T) {
	t.Parallel()
	for _, s := range []State{StateOrphanLikely, StateCaskOnly, StateInTrash} {
		if !s.Reclaimable() {
			t.Errorf("%v should be reclaimable", s)
		}
	}
	for _, s := range []State{StateInstalled, StateNonApp, StateOwnBuild, StateVendor, StateUnknown} {
		if s.Reclaimable() {
			t.Errorf("%v must not be reclaimable", s)
		}
	}
}

func TestHumanAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		at   time.Time
		want string
	}{
		{now.Add(-30 * time.Minute), "less than an hour"},
		{now.Add(-5 * time.Hour), "5 hours"},
		{now.Add(-90 * 24 * time.Hour), "90 days"},
		{time.Time{}, "an unknown time"},
	}
	for _, tc := range tests {
		if got := humanAge(tc.at, now); got != tc.want {
			t.Errorf("humanAge(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}
