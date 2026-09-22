package cli

import (
	"strings"
	"testing"
)

func TestCstr(t *testing.T) {
	buf := [8]byte{'a', 'p', 'f', 's', 0, 'x'}
	if got := cstr(buf[:]); got != "apfs" {
		t.Errorf("cstr = %q, want %q", got, "apfs")
	}
	full := [4]byte{'a', 'b', 'c', 'd'}
	if got := cstr(full[:]); got != "abcd" {
		t.Errorf("cstr without a NUL = %q, want %q", got, "abcd")
	}
}

// TestReadMountsCoversFirmlinkedPaths guards the bug that made the first full
// scan count the OrbStack NFS export twice: getfsstat names data-volume mounts
// through their firmlink, so a walk rooted at the data volume never matches
// them unless both spellings are in the set.
func TestReadMountsCoversFirmlinkedPaths(t *testing.T) {
	set, err := readMounts()
	if err != nil {
		t.Fatalf("readMounts: %v", err)
	}
	if !set.IsMountPoint("/") {
		t.Error("the root filesystem is not in the mount set")
	}
	for path := range set {
		if path == "/" || strings.HasPrefix(path, dataRoot) {
			continue
		}
		if !set.IsMountPoint(dataRoot + path) {
			t.Errorf("%s has no %s twin", path, dataRoot+path)
		}
	}
	if _, _, ok := set.MountInfo("/"); !ok {
		t.Error("MountInfo does not describe the root mount")
	}
	if _, _, ok := set.MountInfo("/definitely/not/a/mount"); ok {
		t.Error("MountInfo described a path that is not a mount point")
	}
}
