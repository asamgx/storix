package volume

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestVolumeFromStatfs(t *testing.T) {
	data := machineMounts[8]
	if data.on != "/System/Volumes/Data" {
		t.Fatalf("fixture order changed: %s", data.on)
	}
	st := data.statfs(t)
	v := VolumeFromStatfs(&st)

	if v.MountPoint != "/System/Volumes/Data" || v.Device != "/dev/disk3s5" || v.FSType != "apfs" {
		t.Fatalf("names not trimmed: %+v", v)
	}
	if v.Container != "disk3" {
		t.Errorf("Container = %q, want disk3", v.Container)
	}
	if want := int64(239362496) * 1024; v.Total != want {
		t.Errorf("Total = %d, want %d", v.Total, want)
	}
	if want := int64(36442196) * 1024; v.Avail != want {
		t.Errorf("Avail = %d, want %d", v.Avail, want)
	}
	if v.UsedSource != UsedFromStatfs {
		t.Errorf("UsedSource = %q, want %q", v.UsedSource, UsedFromStatfs)
	}
	// The statfs-derived number is the container's used, not the volume's:
	// that is the whole reason ApplyVolumeUsed exists.
	if want := v.Total - v.Free; v.UsedStatfs != want {
		t.Errorf("UsedStatfs = %d, want %d", v.UsedStatfs, want)
	}
	if !v.Local || v.ReadOnly {
		t.Errorf("flags misread: local=%v readonly=%v", v.Local, v.ReadOnly)
	}
}

func TestVolumeFromStatfsContainerParsing(t *testing.T) {
	cases := map[string]string{
		"/dev/disk3s5":       "disk3",
		"/dev/disk3s1s1":     "disk3",
		"/dev/disk1s2":       "disk1",
		"/dev/disk10s1":      "disk10",
		"devfs":              "",
		"map auto_home":      "",
		"OrbStack:/OrbStack": "",
		"/dev/disk3":         "", // a container node, not a volume
	}
	for from, want := range cases {
		st := unix.Statfs_t{Bsize: 4096}
		copyFixedName(st.Mntonname[:], "/x")
		copyFixedName(st.Mntfromname[:], from)
		copyFixedName(st.Fstypename[:], "apfs")
		if got := VolumeFromStatfs(&st).Container; got != want {
			t.Errorf("Container(%q) = %q, want %q", from, got, want)
		}
	}
}

func TestMountTableIsMountPoint(t *testing.T) {
	m := fixtureTable(t)
	if n := m.Len(); n != 11 {
		t.Fatalf("mount count = %d, want 11", n)
	}
	mounts := []string{"/", "/dev", "/System/Volumes/Data", "/System/Volumes/Data/home", "/Users/andrewsam/OrbStack"}
	for _, p := range mounts {
		if !m.IsMountPoint(p) {
			t.Errorf("IsMountPoint(%q) = false, want true", p)
		}
	}
	// The NFS mount is also a mount point at its data-volume path, which is
	// the path the walk will reach it by.
	if !m.IsMountPoint("/System/Volumes/Data/Users/andrewsam/OrbStack") {
		t.Error("data-volume alias of the NFS mount is not a mount point")
	}
	notMounts := []string{"/Users", "/System/Volumes/Data/Users", "/System/Volumes", "/Users/andrewsam", "/System/Volumes/Data/home/x"}
	for _, p := range notMounts {
		if m.IsMountPoint(p) {
			t.Errorf("IsMountPoint(%q) = true, want false", p)
		}
	}
	if (*MountTable)(nil).IsMountPoint("/") {
		t.Error("nil table must not claim mount points")
	}
}

func TestMountTableNested(t *testing.T) {
	m := fixtureTable(t)
	nested := m.Nested("/System/Volumes/Data")
	got := map[string]string{}
	for _, v := range nested {
		got[v.MountPoint] = v.FSType
	}
	want := map[string]string{
		"/System/Volumes/Data/home": "autofs",
		"/Users/andrewsam/OrbStack": "nfs",
	}
	if len(got) != len(want) {
		t.Fatalf("Nested = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Nested[%q] = %q, want %q", k, got[k], v)
		}
	}
	// The root itself is never nested inside itself.
	for _, v := range nested {
		if v.MountPoint == "/System/Volumes/Data" {
			t.Error("scan root listed as nested")
		}
	}
}

func TestMountTableOwning(t *testing.T) {
	m := fixtureTable(t)
	cases := map[string]string{
		"/System/Volumes/Data/Users/andrewsam/Library":         "/System/Volumes/Data",
		"/System/Volumes/Data":                                 "/System/Volumes/Data",
		"/System/Volumes/Data/home/x":                          "/System/Volumes/Data/home",
		"/Users/andrewsam/OrbStack/docker":                     "/Users/andrewsam/OrbStack",
		"/System/Volumes/Data/Users/andrewsam/OrbStack/docker": "/Users/andrewsam/OrbStack",
		"/usr/bin":                     "/",
		"/System/Volumes/VM/swapfile0": "/System/Volumes/VM",
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

func TestMountTableVolumesSorted(t *testing.T) {
	m := fixtureTable(t)
	vols := m.Volumes()
	for i := 1; i < len(vols); i++ {
		if vols[i-1].MountPoint > vols[i].MountPoint {
			t.Fatalf("not sorted: %q before %q", vols[i-1].MountPoint, vols[i].MountPoint)
		}
	}
	vols[0].MountPoint = "mutated"
	if m.Volumes()[0].MountPoint == "mutated" {
		t.Error("Volumes() exposes the table's own slice")
	}
}
