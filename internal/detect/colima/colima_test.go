package colima_test

import (
	"errors"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/colima"
	"github.com/asamgx/storix/internal/detect/detecttest"
)

const home = detecttest.Home

// installed is a machine with both tools and both directories.
func installed(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		home + "/.colima/default/colima.sock": 100,
		home + "/.colima/default/diskfile":    -6_000_000,
		home + "/.lima/colima/basedisk":       -4_000_000,
		home + "/.lima/fedora/diffdisk":       -2_000_000,
	})
}

// TestThisMachineIsMissing: neither colima nor limactl is installed here and
// neither directory exists, which is the ordinary answer rather than a
// failure.
func TestThisMachineIsMissing(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, colima.New(), f.Env(t, "testdata/this-machine.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	if facts != nil {
		t.Errorf("facts = %+v, want none", facts)
	}
	claims, sum := colima.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims and a summary on a machine with neither tool", len(claims))
	}
}

func TestProbeAndClassify(t *testing.T) {
	f := installed(t)
	facts, err := detecttest.Probe(t, colima.New(), f.Env(t, "testdata/colima.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*colima.Facts)

	// colima's own "default" and limactl's "colima" are the same machine
	// seen through two tools; merging them is what stops the report showing
	// one virtual machine twice.
	names := map[string]string{}
	for _, inst := range got.Instances {
		names[inst.Name] = inst.Tool
	}
	if names["default"] != "colima" {
		t.Errorf("colima's own instance is missing: %+v", got.Instances)
	}
	if _, dup := names["colima"]; dup {
		t.Errorf("limactl's view of the colima machine was listed a second time: %+v", got.Instances)
	}
	if names["fedora"] != "limactl" {
		t.Errorf("the plain Lima machine is missing: %+v", got.Instances)
	}
	if got.Instances[0].Disk != 64_424_509_440 {
		t.Errorf("disk = %d, want the 60 GiB colima reported", got.Instances[0].Disk)
	}

	claims, sum := colima.New().Classify(f.Tree, facts, f.Context)
	for _, tc := range []struct{ path, owner, key string }{
		{home + "/.colima", "Colima", "cli:colima"},
		{home + "/.lima", "Lima", "cli:limactl"},
	} {
		c, ok := detecttest.ClaimAt(claims, tc.path)
		if !ok {
			t.Errorf("no claim at %s", tc.path)
			continue
		}
		if c.Bucket != classify.BucketContainers || c.Owner != tc.owner {
			t.Errorf("%s = %s / %q", tc.path, c.Bucket, c.Owner)
		}
		if c.Reclaim != classify.ToolManaged {
			t.Errorf("%s reclaim = %s, want tool-managed", tc.path, c.Reclaim)
		}
		if !detecttest.HasKey(c, tc.key) {
			t.Errorf("%s is missing %s (has %v)", tc.path, tc.key, c.OwnerKeys)
		}
	}
	if len(sum.Runtimes) != 2 {
		t.Fatalf("runtimes = %d, want one each for Colima and Lima", len(sum.Runtimes))
	}
	rt, ok := detecttest.Runtime(sum, "Colima")
	if !ok || len(rt.Machines) != 1 {
		t.Fatalf("Colima runtime = %+v", rt)
	}
	if rt.Machines[0] != "default (running), 64.42 GB disk" {
		t.Errorf("machine label = %q", rt.Machines[0])
	}
}

func TestProbeTimeout(t *testing.T) {
	f := installed(t)
	facts, err := detecttest.Probe(t, colima.New(), f.Env(t, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if got := facts.(*colima.Facts); len(got.Instances) != 0 {
		t.Errorf("instances survived a timeout: %+v", got.Instances)
	}

	// The directories are still bucketed: a timeout costs the evidence, not
	// the bytes.
	claims, _ := colima.New().Classify(f.Tree, facts, f.Context)
	if _, ok := detecttest.ClaimAt(claims, home+"/.colima"); !ok {
		t.Error("~/.colima was not claimed after a timeout")
	}
}

func TestProbeMalformedOutput(t *testing.T) {
	f := installed(t)
	facts, err := detecttest.Probe(t, colima.New(), f.Env(t, "testdata/malformed.json"))
	if err != nil {
		t.Fatalf("Probe: %v, want output that does not parse to yield no instances rather than an error", err)
	}
	if got := facts.(*colima.Facts); len(got.Instances) != 0 {
		t.Errorf("instances = %+v, want none from unparseable output", got.Instances)
	}
}

func TestDirectoriesWithoutTools(t *testing.T) {
	// The tools were uninstalled and their machines left behind, which is
	// precisely the case a disk survey exists to surface.
	f := installed(t)
	facts, err := detecttest.Probe(t, colima.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	claims, _ := colima.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 2 {
		t.Errorf("claims = %d, want both leftover directories", len(claims))
	}
}

func TestShipsUnverified(t *testing.T) {
	u, ok := any(colima.New()).(detect.Unverified)
	if !ok || !u.Unverified() {
		t.Error("the colima detector must declare itself unverified until it is run against the tools")
	}
}
