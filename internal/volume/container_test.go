package volume

import "testing"

func TestContainerOf(t *testing.T) {
	s := fixtureSnapshot(t)
	c, ok := s.ContainerOf("/System/Volumes/Data")
	if !ok {
		t.Fatal("no container for the data volume")
	}
	if c.ID != "disk3" {
		t.Fatalf("container ID = %q, want disk3", c.ID)
	}

	const kib = 1024
	wantTotal := int64(239362496) * kib
	wantFree := int64(36442196) * kib
	if c.Total != wantTotal {
		t.Errorf("Total = %d, want %d", c.Total, wantTotal)
	}
	if c.Free != wantFree {
		t.Errorf("Free = %d, want %d", c.Free, wantFree)
	}
	if want := wantTotal - wantFree; c.Used != want {
		t.Errorf("Used = %d, want %d", c.Used, want)
	}

	// The five disk3 volumes, and only those.
	wantMembers := map[string]bool{
		"/": true, "/System/Volumes/Data": true, "/System/Volumes/Preboot": true,
		"/System/Volumes/Update": true, "/System/Volumes/VM": true,
	}
	if len(c.Volumes) != len(wantMembers) {
		t.Fatalf("members = %d, want %d", len(c.Volumes), len(wantMembers))
	}
	var sum int64
	for _, v := range c.Volumes {
		if !wantMembers[v.MountPoint] {
			t.Errorf("unexpected member %q", v.MountPoint)
		}
		sum += v.Used
	}

	// Overhead is what the container uses beyond the sum of its volumes:
	// total - free - sum of per-volume used.
	wantOverhead := wantTotal - wantFree - sum
	if c.Overhead != wantOverhead {
		t.Errorf("Overhead = %d, want %d", c.Overhead, wantOverhead)
	}
	if c.Overhead <= 0 || c.Overhead > 10<<30 {
		t.Errorf("Overhead = %d bytes, want a positive value of a few GB", c.Overhead)
	}
	if got := int64(1439625216); c.Overhead != got {
		t.Errorf("Overhead = %d, want the machine's %d", c.Overhead, got)
	}
}

func TestContainerOfOtherContainer(t *testing.T) {
	s := fixtureSnapshot(t)
	c, ok := s.ContainerOf("/System/Volumes/xarts")
	if !ok {
		t.Fatal("no container for xarts")
	}
	if c.ID != "disk1" {
		t.Fatalf("container ID = %q, want disk1", c.ID)
	}
	if len(c.Volumes) != 3 {
		t.Fatalf("disk1 members = %d, want 3", len(c.Volumes))
	}
	if c.Total != int64(512000)*1024 {
		t.Errorf("Total = %d, want the disk1 size, not disk3's", c.Total)
	}
}

func TestContainerOfNonContainerMounts(t *testing.T) {
	s := fixtureSnapshot(t)
	for _, mp := range []string{"/Users/andrewsam/OrbStack", "/System/Volumes/Data/home", "/dev", "/nope"} {
		if _, ok := s.ContainerOf(mp); ok {
			t.Errorf("ContainerOf(%q) reported a container", mp)
		}
	}
}

func TestContainers(t *testing.T) {
	s := fixtureSnapshot(t)
	cs := s.Containers()
	if len(cs) != 2 {
		t.Fatalf("containers = %d, want 2", len(cs))
	}
	ids := map[string]bool{}
	for _, c := range cs {
		ids[c.ID] = true
	}
	if !ids["disk1"] || !ids["disk3"] {
		t.Errorf("containers = %v, want disk1 and disk3", ids)
	}
}

func TestStatfsIsContainerWideOnAPFS(t *testing.T) {
	// Documents the fact that forced per-volume used off statfs: every APFS
	// volume of a container reports the same free space, so the
	// statfs-derived used is identical across them and is the container's.
	s := fixtureSnapshot(t)
	var first int64
	for _, v := range s.Volumes {
		if v.Container != "disk3" {
			continue
		}
		if first == 0 {
			first = v.UsedStatfs
			continue
		}
		if v.UsedStatfs != first {
			t.Fatalf("%s: UsedStatfs = %d, want %d for every disk3 volume", v.MountPoint, v.UsedStatfs, first)
		}
	}
	c, _ := s.ContainerOf("/System/Volumes/Data")
	if first != c.Used {
		t.Errorf("statfs-derived used = %d, want the container used %d", first, c.Used)
	}
}
