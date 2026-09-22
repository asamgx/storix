package volume

import (
	"testing"

	"github.com/asamgx/storix/internal/mac"
)

// A walk rooted at the data volume meets a nested mount under a path that
// starts with the scan root, while getfsstat reports it under its firmlink or
// symlink path. Missing the second spelling let the first full scan descend
// into the OrbStack NFS export and count its bytes twice.
func TestNestedMatchesFirmlinkSpelling(t *testing.T) {
	m := tableOf(t, firmlinkSpelledMounts)
	nested := m.Nested(mac.DataRoot)

	got := map[string]string{}
	for _, v := range nested {
		got[v.MountPoint] = v.FSType
	}
	want := map[string]string{
		"/home":                        "autofs",
		"/Users/andrewsam/OrbStack":    "nfs",
		"/Applications/Vault.app/data": "apfs",
	}
	if len(got) != len(want) {
		t.Fatalf("Nested(%s) = %v, want %v", mac.DataRoot, got, want)
	}
	for mp, fstype := range want {
		if got[mp] != fstype {
			t.Errorf("Nested[%q] = %q, want %q", mp, got[mp], fstype)
		}
	}
	for _, v := range nested {
		switch v.MountPoint {
		case "/", "/dev", "/System/Volumes/VM", mac.DataRoot:
			t.Errorf("%q is not inside the data volume but was reported as nested", v.MountPoint)
		}
	}
}

func TestIsMountPointMatchesBothSpellings(t *testing.T) {
	m := tableOf(t, firmlinkSpelledMounts)
	pairs := [][2]string{
		{"/home", mac.DataRoot + "/home"},
		{"/Users/andrewsam/OrbStack", mac.DataRoot + "/Users/andrewsam/OrbStack"},
		{"/Applications/Vault.app/data", mac.DataRoot + "/Applications/Vault.app/data"},
	}
	for _, p := range pairs {
		for _, spelling := range p {
			if !m.IsMountPoint(spelling) {
				t.Errorf("IsMountPoint(%q) = false, want true", spelling)
			}
		}
	}
	// The scan root itself is not a nested mount, and paths merely inside a
	// mount are not mount points.
	for _, p := range []string{
		mac.DataRoot + "/Users/andrewsam", mac.DataRoot + "/Users/andrewsam/OrbStack/docker",
		mac.DataRoot + "/dev", "/homework",
	} {
		if m.IsMountPoint(p) {
			t.Errorf("IsMountPoint(%q) = true, want false", p)
		}
	}
}

// Describe gives the walker the type and source of a mount it refused to
// descend into, under either spelling.
func TestDescribe(t *testing.T) {
	m := tableOf(t, firmlinkSpelledMounts)
	cases := []struct{ path, fstype, device string }{
		{"/Users/andrewsam/OrbStack", "nfs", "OrbStack:/OrbStack"},
		{mac.DataRoot + "/Users/andrewsam/OrbStack", "nfs", "OrbStack:/OrbStack"},
		{"/home", "autofs", "map auto_home"},
		{mac.DataRoot + "/home", "autofs", "map auto_home"},
		{mac.DataRoot + "/home/", "autofs", "map auto_home"},
		{"/dev", "devfs", "devfs"},
	}
	for _, c := range cases {
		v, ok := m.Describe(c.path)
		if !ok {
			t.Errorf("Describe(%q): not found", c.path)
			continue
		}
		if v.FSType != c.fstype || v.Device != c.device {
			t.Errorf("Describe(%q) = %q/%q, want %q/%q", c.path, v.FSType, v.Device, c.fstype, c.device)
		}
	}
	if _, ok := m.Describe(mac.DataRoot + "/Users/andrewsam"); ok {
		t.Error("Describe answered for a path that is not a mount point")
	}
	if _, ok := (*MountTable)(nil).Describe("/"); ok {
		t.Error("nil table described a mount point")
	}
}

func TestOwningMatchesBothSpellings(t *testing.T) {
	m := tableOf(t, firmlinkSpelledMounts)
	cases := map[string]string{
		mac.DataRoot + "/Users/andrewsam/OrbStack/docker/x": "/Users/andrewsam/OrbStack",
		"/Users/andrewsam/OrbStack/docker/x":                "/Users/andrewsam/OrbStack",
		mac.DataRoot + "/home/andrewsam":                    "/home",
		mac.DataRoot + "/Users/andrewsam/Library":           mac.DataRoot,
		"/usr/bin": "/",
	}
	for path, want := range cases {
		v, ok := m.Owning(path)
		if !ok {
			t.Errorf("Owning(%q): not found", path)
			continue
		}
		if v.MountPoint != want {
			t.Errorf("Owning(%q) = %q, want %q", path, v.MountPoint, want)
		}
	}
}
