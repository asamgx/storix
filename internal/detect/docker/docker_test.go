package docker_test

import (
	"errors"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/docker"
)

const home = detecttest.Home

// containerDir is Docker Desktop's sandbox container, where the guest's whole
// disk lives.
const containerDir = home + "/Library/Containers/com.docker.docker"

// desktop is a machine with Docker Desktop installed.
func desktop(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		containerDir + "/Data/vms/0/data/Docker.raw":                      -9_000_000,
		home + "/Library/Group Containers/group.com.docker/settings.json": 800,
		home + "/.docker/config.json":                                     400,
	})
}

// orbstackOnly is this machine: a docker CLI and a docker socket, both
// OrbStack's, and no Docker Desktop anywhere.
func orbstackOnly(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		home + "/.docker/config.json":                   400,
		home + "/.orbstack/run/docker.sock.placeholder": 100,
	})
}

// TestThisMachineIsMissing is the point of the gate. OrbStack owns the docker
// CLI, /var/run/docker.sock and a context named "default" here. Docker
// Desktop is still not installed, and a detector that read any of those as
// evidence would invent a second runtime over the same disk image.
func TestThisMachineIsMissing(t *testing.T) {
	f := orbstackOnly(t)
	for _, fixture := range []string{"testdata/this-machine.json", "testdata/orbstack-contexts.json"} {
		facts, err := detecttest.Probe(t, docker.New(), f.Env(t, fixture))
		if !errors.Is(err, detect.ErrMissing) {
			t.Errorf("%s: Probe error = %v, want ErrMissing", fixture, err)
		}
		if facts != nil {
			t.Errorf("%s: facts = %+v, want none", fixture, facts)
		}
		claims, sum := docker.New().Classify(f.Tree, facts, f.Context)
		if len(claims) != 0 {
			t.Errorf("%s: %d claims on a machine without Docker Desktop", fixture, len(claims))
		}
		if !sum.Empty() {
			t.Errorf("%s: a runtime was reported for a runtime that is not installed", fixture)
		}
	}
	// ~/.docker exists and belongs to whichever CLI is installed; the
	// catalog rule buckets it, not this detector.
	if _, ok := detecttest.ClaimAt(nil, home+"/.docker"); ok {
		t.Error("~/.docker was claimed by the Docker Desktop detector")
	}
}

func TestProbeDesktop(t *testing.T) {
	f := desktop(t)
	facts, err := detecttest.Probe(t, docker.New(), f.Env(t, "testdata/desktop.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*docker.Facts)
	if got.Context != "desktop-linux" {
		t.Errorf("context = %q, want desktop-linux", got.Context)
	}
	if len(got.DF) != 4 {
		t.Fatalf("df rows = %d, want 4", len(got.DF))
	}
	if got.DF[0].Size != 6_300_000_000 || got.DF[0].Reclaimable != 2_100_000_000 || got.DF[0].Percent != 33 {
		t.Errorf("images row = %+v", got.DF[0])
	}

	claims, sum := docker.New().Classify(f.Tree, facts, f.Context)
	for _, p := range []string{
		containerDir,
		home + "/Library/Group Containers/group.com.docker",
		home + "/.docker",
	} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Bucket != classify.BucketContainers || c.Owner != "Docker Desktop" {
			t.Errorf("%s = %s / %q", p, c.Bucket, c.Owner)
		}
		if !detecttest.HasKey(c, "app:com.docker.docker") {
			t.Errorf("%s is missing app:com.docker.docker (has %v)", p, c.OwnerKeys)
		}
	}

	rt, ok := detecttest.Runtime(sum, "Docker Desktop")
	if !ok {
		t.Fatal("no Docker Desktop runtime")
	}
	if len(rt.HostImage) != 1 || rt.HostImage[0].Name != "Docker.raw" {
		t.Fatalf("host image = %+v, want Docker.raw", rt.HostImage)
	}
	if rt.HostBytes() >= 9_000_000 {
		t.Errorf("host bytes = %d; the sparse image's apparent size was reported", rt.HostBytes())
	}
	if rt.GuestBytes() != 6_300_000_000+473_100_000+2_480_000_000+3_400_000_000 {
		t.Errorf("guest bytes = %d", rt.GuestBytes())
	}
	for _, line := range rt.GuestReported {
		want := classify.ToolManaged
		if line.Type == "Local Volumes" {
			want = classify.UserData
		}
		if line.Reclaim != want {
			t.Errorf("%s reclaim = %s, want %s", line.Type, line.Reclaim, want)
		}
	}
}

func TestProbeDaemonDown(t *testing.T) {
	f := desktop(t)
	facts, err := detecttest.Probe(t, docker.New(), f.Env(t, "testdata/daemon-down.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if got := facts.(*docker.Facts); len(got.DF) != 0 {
		t.Error("df rows survived a daemon that refused the connection")
	}
	_, sum := docker.New().Classify(f.Tree, facts, f.Context)
	rt, _ := detecttest.Runtime(sum, "Docker Desktop")
	if len(rt.HostImage) != 1 {
		t.Errorf("the host image was lost with the daemon: %+v", rt.HostImage)
	}
	if rt.Note == "" {
		t.Error("the runtime does not say the daemon is down")
	}
}

func TestProbeMalformedOutput(t *testing.T) {
	f := desktop(t)
	_, err := detecttest.Probe(t, docker.New(), f.Env(t, "testdata/malformed.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded: a table where JSON was asked for is not usable", err)
	}
}

// TestGateOnDataAlone covers the case the gate exists for: the application
// was dragged to the trash and its sandbox container, with the disk image in
// it, was left behind.
func TestGateOnDataAlone(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{
		containerDir + "/Data/vms/0/data/Docker.raw": 5_000,
	})
	facts, err := detecttest.Probe(t, docker.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if got := facts.(*docker.Facts); got.Bundle != "" {
		t.Errorf("bundle = %q, want empty: the application is gone", got.Bundle)
	}
	claims, _ := docker.New().Classify(f.Tree, facts, f.Context)
	if _, ok := detecttest.ClaimAt(claims, containerDir); !ok {
		t.Error("the leftover container was not claimed")
	}
}

func TestShipsUnverified(t *testing.T) {
	u, ok := any(docker.New()).(detect.Unverified)
	if !ok || !u.Unverified() {
		t.Error("the docker detector must declare itself unverified until it is run against Docker Desktop")
	}
}
