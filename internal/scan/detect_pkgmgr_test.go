package scan

import (
	"context"
	"testing"

	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
)

// TestPackageManagerDetectorsAreRegistered is what internal/detect cannot
// assert about itself: the detector packages register themselves from their
// init functions, and whether a scan knows about them depends on this
// package's blank imports. A detector whose import was dropped would
// otherwise vanish silently and take its claims with it.
func TestPackageManagerDetectorsAreRegistered(t *testing.T) {
	have := make(map[string]int, 32)
	for i, name := range detect.Default().Names() {
		have[name] = i
	}
	for _, name := range []string{"homebrew", "node", "python", "go", "rust", "cli-tools"} {
		if _, ok := have[name]; !ok {
			t.Errorf("the %s detector is not registered", name)
		}
	}

	// The container detectors registered first, so the toolchain detectors
	// come after them and the report's sections read in that order.
	for _, name := range []string{"homebrew", "node", "python", "go", "rust", "cli-tools"} {
		if have[name] < have["kubernetes"] {
			t.Errorf("%s is ordered before the container detectors", name)
		}
	}
	// cli-tools is the catch-all and runs last of this set, so a tool with a
	// detector of its own is named by that detector rather than by a
	// directory name.
	for _, name := range []string{"homebrew", "node", "python", "go", "rust"} {
		if have["cli-tools"] < have[name] {
			t.Errorf("cli-tools is ordered before %s", name)
		}
	}
}

// TestPackageManagerDetectorsAreMissingWithoutTools is this milestone's half
// of the degradation gate.
//
// It lives here rather than in the shared all-detectors loop because that
// loop had to be narrowed: xcode, jvm and ruby look at /Library/Developer,
// /Library/Java and /Library/Ruby, which exist on any real macOS install
// whatever a fixture says, so they cannot be Missing. Every detector in this
// milestone is per-user — it asks LookPath and looks under the configured
// home and nowhere else — so all six can still be held to the stricter
// promise, and a future one that grows a machine-wide path will fail here
// and have to say so.
func TestPackageManagerDetectorsAreMissingWithoutTools(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Documents/note.txt", 100)

	res, err := Run(context.Background(), Config{
		Roots:   []string{f.Root},
		Home:    f.Path("Users/andrew"),
		NoCache: true,
		Probe:   probe.NewReplay(nil),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	mine := map[string]bool{
		"homebrew": true, "node": true, "python": true,
		"go": true, "rust": true, "cli-tools": true,
	}
	seen := 0
	for _, st := range res.Detectors {
		if !mine[st.Name] {
			continue
		}
		seen++
		if st.State != detect.Missing {
			t.Errorf("%s = %s (%q), want missing: it looks only under the configured home",
				st.Name, st.State, st.Reason)
		}
		if st.Reason == "" {
			t.Errorf("%s is missing without saying which tool or directory it looked for", st.Name)
		}
	}
	if seen != len(mine) {
		t.Errorf("%d of the %d package-manager detectors ran", seen, len(mine))
	}
}
