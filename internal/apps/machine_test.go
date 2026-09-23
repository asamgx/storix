package apps

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// teamCachePathForMachineTest is a temporary cache by default, or the path in
// STORIX_APPS_TEAMCACHE so two runs can share one and the second can be shown
// to invoke codesign zero times.
func teamCachePathForMachineTest(t *testing.T) string {
	if p := os.Getenv("STORIX_APPS_TEAMCACHE"); p != "" {
		return p
	}
	return filepath.Join(t.TempDir(), "teamids.json")
}

// TestMachineProbe runs the probe against the machine it is running on and
// prints what it found. It is skipped unless STORIX_APPS_MACHINE is set,
// because it needs a real macOS install with Homebrew and it is a smoke test
// rather than an assertion: what it prints is checked by a person against the
// corpus in the plan.
//
// It is read-only. The commands it runs are brew --caskroom, pkgutil,
// lsregister -dump, mdfind and codesign; the only thing it writes is the team
// id cache, which it points at the test's own temporary directory rather than
// the user's Application Support.
func TestMachineProbe(t *testing.T) {
	if os.Getenv("STORIX_APPS_MACHINE") == "" {
		t.Skip("set STORIX_APPS_MACHINE=1 to probe this machine")
	}
	user, home, err := mac.InvokingHome()
	if err != nil {
		t.Fatalf("resolving the invoking user: %v", err)
	}

	runner := &probe.Exec{}
	d := &Detector{
		TeamCachePath: teamCachePathForMachineTest(t),
	}
	env := detect.Env{
		Runner:   runner,
		Home:     home,
		Euid:     os.Geteuid(),
		ReadFile: detect.ReadFile,
		ReadDir:  detect.ReadDir,
		Stat:     detect.Stat,
		LookPath: runner.LookPath,
	}

	start := time.Now()
	raw, probeErr := d.Probe(context.Background(), env)
	elapsed := time.Since(start)
	f, ok := raw.(*Facts)
	if !ok {
		t.Fatalf("Probe returned %T", raw)
	}

	t.Logf("probe took %s (err: %v)", elapsed.Round(time.Millisecond), probeErr)
	t.Logf("caskroom     %s", f.CaskroomDir)
	t.Logf("casks        %d", len(f.Casks))
	t.Logf("receipts     %d", len(f.Receipts))
	t.Logf("registry     %d", len(f.Registry))
	t.Logf("launch items %d", len(f.LaunchItems))
	t.Logf("bundles      %d (spotlight %d)", len(f.AppDirBundles), len(f.Spotlight))
	t.Logf("group conts  %d", len(f.GroupContainerNames))

	resolved := 0
	for _, v := range f.TeamIDs {
		if v != "" {
			resolved++
		}
	}
	t.Logf("team ids     %d resolved in %d codesign calls", resolved, f.CodesignCalls)

	inv := BuildInventory(nil, f, Paths{Home: home, User: user})
	var missing []string
	for i := range f.Casks {
		c := &f.Casks[i]
		if !c.HasApp() {
			continue
		}
		found := false
		for _, n := range c.AppNames() {
			if _, ok := inv.InstalledByName(n); ok {
				found = true
			}
		}
		if !found {
			missing = append(missing, c.Token+" expects "+c.Apps[0])
			continue
		}
		if os.Getenv("STORIX_APPS_VERBOSE") != "" {
			for _, n := range c.AppNames() {
				if b, ok := inv.InstalledByName(n); ok {
					t.Logf("cask %s matched %q at %s (source %v)", c.Token, n, b.Path, b.Source)
				}
			}
		}
	}
	sort.Strings(missing)
	t.Logf("cask installed, application missing (%d): %v", len(missing), missing)

	for _, deg := range f.Degraded {
		t.Logf("degraded: %s: %s", deg.Probe, deg.Reason)
	}

	// The floor the rest of the milestone rests on: without an inventory
	// there is nothing to attribute anything to.
	if len(f.AppDirBundles) == 0 {
		t.Error("no application bundles were found")
	}
	if len(f.Casks) == 0 {
		t.Error("no casks were found")
	}

	if os.Getenv("STORIX_APPS_WALK") == "" {
		t.Log("set STORIX_APPS_WALK=1 to also walk the volume and print the verdicts")
		return
	}
	machineVerdicts(t, d, f, home)
}

// machineVerdicts walks the data volume and prints the verdict table, which
// is what the acceptance list in the plan is checked against by a person.
func machineVerdicts(t *testing.T, d *Detector, f *Facts, home string) {
	t.Helper()
	start := time.Now()
	tree, err := walk.Walk(t.Context(), walk.Options{Root: mac.DataRoot})
	if err != nil {
		t.Fatalf("walking the volume: %v", err)
	}
	t.Logf("walk took %s, %d nodes", time.Since(start).Round(time.Millisecond), len(tree.Nodes))

	analyzeStart := time.Now()
	a := d.Analyze(tree, f, classify.Context{Home: home})
	t.Logf("analysis took %s: %d candidates, %d owners",
		time.Since(analyzeStart).Round(time.Millisecond), len(a.Candidates), len(a.Owners))

	for _, state := range []State{
		StateOrphanLikely, StateCaskOnly, StateInTrash, StateUnknown,
		StateOwnBuild, StateNonApp, StateVendor,
	} {
		keys := a.OwnersInState(state)
		var rows []string
		for _, key := range keys {
			o, v := a.Owners[key], a.Verdicts[key]
			rows = append(rows, o.Owner.Label+" ["+key+"] "+
				v.Confidence.String()+" "+humanBytes(o.Bytes))
		}
		sort.Strings(rows)
		t.Logf("%s (%d):\n  %s", state, len(keys), strings.Join(rows, "\n  "))
	}

	for _, label := range strings.Split(os.Getenv("STORIX_APPS_EXPLAIN"), ",") {
		if label == "" {
			continue
		}
		for _, key := range a.OwnerKeys() {
			o := a.Owners[key]
			if o.Owner.Label != label {
				continue
			}
			v := a.Verdicts[key]
			t.Logf("%s [%s] state=%v conf=%v lastWrite=%s\n  ids=%v\n  evidence=%v\n  keep=%v",
				label, key, v.State, v.Confidence, v.LastWrite.Format(time.RFC3339),
				o.IDs, v.Evidence, v.Keep)
			for _, i := range o.Members {
				t.Logf("    member %s rule=%s", a.Candidates[i].Path, a.Matches[i].Rule)
			}
		}
	}

	var tokens []string
	for _, ck := range a.CaskOnlyCasks() {
		tokens = append(tokens, ck.Token)
	}
	t.Logf("casks whose application is missing (%d): %v", len(tokens), tokens)

	fps := Footprints(a, a.Claims())
	t.Logf("footprints: %d", len(fps))
	for i, fp := range fps {
		if i >= 12 {
			break
		}
		t.Logf("  %-22s %10s  bundle %8s data %8s caches %8s  %s",
			fp.Owner.Label, humanBytes(fp.Total), humanBytes(fp.Bundle),
			humanBytes(fp.Data), humanBytes(fp.Caches), fp.State)
	}
}

// humanBytes is a compact size for a log line.
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + "kMGTP"[exp:exp+1] + "B"
}
