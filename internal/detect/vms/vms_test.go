package vms_test

import (
	"errors"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/vms"
)

const home = detecttest.Home

// TestThisMachineIsMissing: none of the four managers is installed here.
func TestThisMachineIsMissing(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, vms.New(), f.Env(t, "testdata/this-machine.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	claims, sum := vms.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims on a machine with no virtual machines", len(claims))
	}
}

// TestClassify covers the distinction this detector exists for: a guest's
// disk is the user's data, and the manager's own caches beside it are not.
func TestClassify(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{
		home + "/Library/Containers/com.utmapp.UTM/Data/Documents/debian.utm/Images/disk.qcow2": 4_000,
		home + "/Parallels/Windows 11.pvm/harddisk.hdd/disk":                                    5_000,
		home + "/Library/Parallels/Updates/installer.dmg":                                       6_000,
		home + "/Virtual Machines.localized/Ubuntu.vmwarevm/Ubuntu.vmdk":                        7_000,
		home + "/VirtualBox VMs/dev/dev.vdi":                                                    8_000,
		home + "/.vagrant.d/boxes/hashicorp-bionic/box.img":                                     9_000,
	})

	facts, err := detecttest.Probe(t, vms.New(), f.Env(t, "testdata/missing.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got := facts.(*vms.Facts); len(got.Found) != 6 {
		t.Errorf("found = %v, want all six directories", got.Found)
	}

	claims, sum := vms.New().Classify(f.Tree, facts, f.Context)
	want := []struct {
		path    string
		owner   string
		key     string
		reclaim classify.Reclaim
	}{
		{home + "/Library/Containers/com.utmapp.UTM", "UTM", "app:com.utmapp.UTM", classify.UserData},
		{home + "/Parallels", "Parallels", "app:com.parallels.desktop.console", classify.UserData},
		{home + "/Library/Parallels", "Parallels", "app:com.parallels.desktop.console", classify.Regenerable},
		{home + "/Virtual Machines.localized", "VMware Fusion", "app:com.vmware.fusion", classify.UserData},
		{home + "/VirtualBox VMs", "VirtualBox", "app:org.virtualbox.app.VirtualBox", classify.UserData},
		{home + "/.vagrant.d", "Vagrant", "cli:vagrant", classify.ToolManaged},
	}
	if len(claims) != len(want) {
		t.Fatalf("claims = %d, want %d", len(claims), len(want))
	}
	for _, w := range want {
		c, ok := detecttest.ClaimAt(claims, w.path)
		if !ok {
			t.Errorf("no claim at %s", w.path)
			continue
		}
		if c.Bucket != classify.BucketContainers {
			t.Errorf("%s bucket = %s, want containers", w.path, c.Bucket)
		}
		if c.Owner != w.owner {
			t.Errorf("%s owner = %q, want %q", w.path, c.Owner, w.owner)
		}
		if c.Reclaim != w.reclaim {
			t.Errorf("%s reclaim = %s, want %s", w.path, c.Reclaim, w.reclaim)
		}
		if !detecttest.HasKey(c, w.key) {
			t.Errorf("%s is missing %s (has %v)", w.path, w.key, c.OwnerKeys)
		}
		if c.Source.Detector != "vms" {
			t.Errorf("%s source = %s", w.path, c.Source)
		}
	}

	// Parallels keeps two directories and must appear once, with both.
	rt, ok := detecttest.Runtime(sum, "Parallels")
	if !ok {
		t.Fatal("no Parallels runtime")
	}
	if len(rt.HostImage) != 2 {
		t.Errorf("Parallels rows = %d, want its machines and its caches", len(rt.HostImage))
	}
	if len(sum.Runtimes) != 5 {
		t.Errorf("runtimes = %d, want one per manager", len(sum.Runtimes))
	}
}

// TestPartialInstall: only one manager is present, and only its directory is
// claimed.
func TestPartialInstall(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{
		home + "/VirtualBox VMs/dev/dev.vdi": 8_000,
	})
	facts, err := detecttest.Probe(t, vms.New(), f.Env(t, "testdata/missing.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, _ := vms.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 1 {
		t.Fatalf("claims = %d, want only VirtualBox", len(claims))
	}
	if claims[0].Owner != "VirtualBox" {
		t.Errorf("owner = %q", claims[0].Owner)
	}
}
