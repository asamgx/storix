package volume

import (
	"testing"

	"golang.org/x/sys/unix"
)

// The fixtures mirror the planning machine on 2026-09-22 as df -k reported
// it. df prints 1 KiB units; statfs reports 4 KiB blocks, so the counts are
// divided by four. Every APFS volume of a container reports the container's
// free space, so f_bfree equals f_bavail on all of them and the per-volume
// used numbers below are the getattrlist ATTR_VOL_SPACEUSED values, which is
// where real per-volume usage comes from.
const fixtureBsize = 4096

type fixtureVol struct {
	on        string
	from      string
	fstype    string
	blocksKiB int64 // df "1024-blocks"
	usedKiB   int64 // df "Used", i.e. the volume's own usage
	availKiB  int64 // df "Available"
	flags     uint32
}

// machineMounts is the 11-entry mount table of the planning machine.
var machineMounts = []fixtureVol{
	{on: "/", from: "/dev/disk3s1s1", fstype: "apfs", blocksKiB: 239362496, usedKiB: 12347036, availKiB: 36442196, flags: unix.MNT_LOCAL | unix.MNT_RDONLY},
	{on: "/dev", from: "devfs", fstype: "devfs", blocksKiB: 216, usedKiB: 216, availKiB: 0, flags: unix.MNT_LOCAL | unix.MNT_DONTBROWSE},
	{on: "/System/Volumes/VM", from: "/dev/disk3s6", fstype: "apfs", blocksKiB: 239362496, usedKiB: 5245132, availKiB: 36442196, flags: unix.MNT_LOCAL | unix.MNT_DONTBROWSE},
	{on: "/System/Volumes/Preboot", from: "/dev/disk3s2", fstype: "apfs", blocksKiB: 239362496, usedKiB: 8831872, availKiB: 36442196, flags: unix.MNT_LOCAL | unix.MNT_DONTBROWSE},
	{on: "/System/Volumes/Update", from: "/dev/disk3s4", fstype: "apfs", blocksKiB: 239362496, usedKiB: 2704, availKiB: 36442196, flags: unix.MNT_LOCAL | unix.MNT_DONTBROWSE},
	{on: "/System/Volumes/xarts", from: "/dev/disk1s2", fstype: "apfs", blocksKiB: 512000, usedKiB: 6164, availKiB: 493292, flags: unix.MNT_LOCAL | unix.MNT_DONTBROWSE},
	{on: "/System/Volumes/iSCPreboot", from: "/dev/disk1s1", fstype: "apfs", blocksKiB: 512000, usedKiB: 6112, availKiB: 493292, flags: unix.MNT_LOCAL | unix.MNT_DONTBROWSE},
	{on: "/System/Volumes/Hardware", from: "/dev/disk1s3", fstype: "apfs", blocksKiB: 512000, usedKiB: 1572, availKiB: 493292, flags: unix.MNT_LOCAL | unix.MNT_DONTBROWSE},
	{on: "/System/Volumes/Data", from: "/dev/disk3s5", fstype: "apfs", blocksKiB: 239362496, usedKiB: 175087672, availKiB: 36442196, flags: unix.MNT_LOCAL | unix.MNT_DONTBROWSE},
	{on: "/System/Volumes/Data/home", from: "map auto_home", fstype: "autofs", blocksKiB: 0, usedKiB: 0, availKiB: 0, flags: unix.MNT_AUTOMOUNTED | unix.MNT_DONTBROWSE},
	{on: "/Users/andrewsam/OrbStack", from: "OrbStack:/OrbStack", fstype: "nfs", blocksKiB: 51380224, usedKiB: 18239620, availKiB: 33140604},
}

// statfs renders the fixture the way the kernel would.
func (f fixtureVol) statfs(t *testing.T) unix.Statfs_t {
	t.Helper()
	const kib = 1024
	if f.blocksKiB%4 != 0 || f.availKiB%4 != 0 {
		t.Fatalf("fixture %s: KiB counts must be whole 4 KiB blocks", f.on)
	}
	st := unix.Statfs_t{
		Bsize:  fixtureBsize,
		Blocks: uint64(f.blocksKiB * kib / fixtureBsize),
		Bavail: uint64(f.availKiB * kib / fixtureBsize),
		Flags:  f.flags,
	}
	// Free space belongs to the container, so every volume reports the same
	// f_bfree, equal to f_bavail here.
	st.Bfree = st.Bavail
	copyFixedName(st.Mntonname[:], f.on)
	copyFixedName(st.Mntfromname[:], f.from)
	copyFixedName(st.Fstypename[:], f.fstype)
	return st
}

func copyFixedName(dst []byte, s string) {
	n := copy(dst, s)
	if n < len(dst) {
		dst[n] = 0
	}
}

// fixtureTable builds a MountTable with per-volume used filled in the way
// getattrlist fills it on a live system.
func fixtureTable(t *testing.T) *MountTable {
	t.Helper()
	vols := make([]Volume, 0, len(machineMounts))
	for _, f := range machineMounts {
		st := f.statfs(t)
		v := VolumeFromStatfs(&st)
		v.Used = f.usedKiB * 1024
		v.UsedSource = UsedFromGetattrlist
		vols = append(vols, v)
	}
	return NewMountTable(vols)
}

// fixtureSnapshot builds a Snapshot from the same numbers.
func fixtureSnapshot(t *testing.T) Snapshot {
	t.Helper()
	return Snapshot{Volumes: fixtureTable(t).Volumes()}
}
