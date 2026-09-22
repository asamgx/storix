package mac

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGetVolAttrs(t *testing.T) {
	if _, err := os.Stat(DataRoot); err != nil {
		t.Skipf("no %s on this machine: %v", DataRoot, err)
	}
	attrs, err := GetVolAttrs(DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	var st unix.Statfs_t
	if err := unix.Statfs(DataRoot, &st); err != nil {
		t.Fatal(err)
	}
	if want := int64(st.Blocks) * int64(st.Bsize); attrs.Size != want {
		t.Errorf("Size = %d, want %d", attrs.Size, want)
	}
	// Free space is container-wide, so both calls see the same number; a
	// reading taken microseconds apart may differ by a few blocks.
	const tolerance = 1 << 20
	if d := attrs.SpaceAvail - int64(st.Bavail)*int64(st.Bsize); d > tolerance || d < -tolerance {
		t.Errorf("SpaceAvail differs from statfs by %d bytes", d)
	}
	if attrs.SpaceUsed <= 0 || attrs.SpaceUsed >= attrs.Size {
		t.Errorf("SpaceUsed = %d, want 0 < used < %d", attrs.SpaceUsed, attrs.Size)
	}
	if attrs.SpaceFree <= 0 {
		t.Errorf("SpaceFree = %d, want a positive number", attrs.SpaceFree)
	}
}

func TestGetVolAttrsErrors(t *testing.T) {
	if _, err := GetVolAttrs("/no/such/path/at/all"); err == nil {
		t.Error("GetVolAttrs on a missing path returned no error")
	}
	if _, err := GetVolAttrs("bad\x00path"); err == nil {
		t.Error("GetVolAttrs on a path with a NUL returned no error")
	}
}
