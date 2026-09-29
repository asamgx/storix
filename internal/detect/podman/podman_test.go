package podman_test

import (
	"errors"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/podman"
)

const home = detecttest.Home

// installed is a machine with Podman and a machine image.
func installed(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		home + "/.local/share/containers/podman/machine/applehv/podman.raw": -7_000_000,
		home + "/.config/containers/containers.conf":                        300,
	})
}

// TestThisMachineIsMissing: podman is not installed here and neither
// directory exists.
func TestThisMachineIsMissing(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, podman.New(), f.Env(t, "testdata/this-machine.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	if facts != nil {
		t.Errorf("facts = %+v, want none", facts)
	}
	claims, sum := podman.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims on a machine without podman", len(claims))
	}
}

func TestProbeAndClassify(t *testing.T) {
	f := installed(t)
	facts, err := detecttest.Probe(t, podman.New(), f.Env(t, "testdata/podman.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*podman.Facts)
	if len(got.Machines) != 1 {
		t.Fatalf("machines = %+v, want one", got.Machines)
	}
	m := got.Machines[0]
	if m.Name != "podman-machine-default" || !m.Running {
		t.Errorf("machine = %+v", m)
	}
	// Podman reports DiskSize as a bare number of gibibytes.
	if m.Disk != 100<<30 {
		t.Errorf("disk = %d, want 100 GiB", m.Disk)
	}

	claims, sum := podman.New().Classify(f.Tree, facts, f.Context)
	for _, tc := range []struct {
		path    string
		reclaim classify.Reclaim
	}{
		{home + "/.local/share/containers", classify.ToolManaged},
		{home + "/.config/containers", classify.UserData},
	} {
		c, ok := detecttest.ClaimAt(claims, tc.path)
		if !ok {
			t.Errorf("no claim at %s", tc.path)
			continue
		}
		if c.Bucket != classify.BucketContainers || c.Owner != "Podman" {
			t.Errorf("%s = %s / %q", tc.path, c.Bucket, c.Owner)
		}
		if c.Reclaim != tc.reclaim {
			t.Errorf("%s reclaim = %s, want %s", tc.path, c.Reclaim, tc.reclaim)
		}
		if !detecttest.HasKey(c, "cli:podman") {
			t.Errorf("%s is missing cli:podman (has %v)", tc.path, c.OwnerKeys)
		}
	}

	rt, ok := detecttest.Runtime(sum, "Podman")
	if !ok {
		t.Fatal("no Podman runtime")
	}
	if len(rt.HostImage) != 1 {
		t.Errorf("host image rows = %d, want the machine directory only", len(rt.HostImage))
	}
	// The host figure is allocated blocks: the fixture's image is sparse and
	// the machine claims a hundred gibibytes.
	if rt.HostBytes() >= 7_000_000 {
		t.Errorf("host bytes = %d; the apparent size was reported", rt.HostBytes())
	}
	if len(rt.Machines) != 1 || rt.Machines[0] != "podman-machine-default (running), 107.37 GB disk" {
		t.Errorf("machines = %v", rt.Machines)
	}
}

func TestProbeTimeout(t *testing.T) {
	f := installed(t)
	facts, err := detecttest.Probe(t, podman.New(), f.Env(t, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	claims, _ := podman.New().Classify(f.Tree, facts, f.Context)
	if _, ok := detecttest.ClaimAt(claims, home+"/.local/share/containers"); !ok {
		t.Error("the machine directory was not claimed after a timeout")
	}
}

func TestProbeMalformedOutput(t *testing.T) {
	f := installed(t)
	facts, err := detecttest.Probe(t, podman.New(), f.Env(t, "testdata/malformed.json"))
	if err != nil {
		t.Fatalf("Probe: %v, want unparseable output to yield no machines rather than an error", err)
	}
	if got := facts.(*podman.Facts); len(got.Machines) != 0 {
		t.Errorf("machines = %+v, want none", got.Machines)
	}
}

func TestDirectoriesWithoutTheTool(t *testing.T) {
	f := installed(t)
	_, err := detecttest.Probe(t, podman.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded for leftover directories", err)
	}
}

func TestShipsUnverified(t *testing.T) {
	u, ok := any(podman.New()).(detect.Unverified)
	if !ok || !u.Unverified() {
		t.Error("the podman detector must declare itself unverified until it is run against podman")
	}
}
