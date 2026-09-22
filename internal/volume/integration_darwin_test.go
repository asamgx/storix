package volume

import (
	"context"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/asamgx/storix/internal/mac"
)

// requireDataVolume skips when the test is not running on a Mac with the
// usual volume layout.
func requireDataVolume(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(mac.DataRoot); err != nil {
		t.Skipf("no %s on this machine: %v", mac.DataRoot, err)
	}
}

func TestReadMountTableLive(t *testing.T) {
	requireDataVolume(t)
	m, err := ReadMountTable()
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsMountPoint(mac.DataRoot) {
		t.Fatalf("%s missing from the mount table", mac.DataRoot)
	}
	data, ok := m.Describe(mac.DataRoot)
	if !ok {
		t.Fatal("data volume not found")
	}
	if data.FSType != "apfs" {
		t.Errorf("data volume fstype = %q, want apfs", data.FSType)
	}
	if !strings.HasPrefix(data.Device, "/dev/disk") {
		t.Errorf("data volume device = %q, want a /dev/diskNsM node", data.Device)
	}
	if data.Container == "" {
		t.Errorf("no container parsed from %q", data.Device)
	}
	if data.Used <= 0 || data.Total <= 0 {
		t.Errorf("empty space numbers: %+v", data)
	}
	if data.UsedSource != UsedFromGetattrlist {
		t.Errorf("UsedSource = %q, want %q", data.UsedSource, UsedFromGetattrlist)
	}
	// Per-volume used must be below the container's used; the statfs-derived
	// value is the container's and would be equal to it.
	if data.Used >= data.UsedStatfs {
		t.Errorf("per-volume used %d is not below the container used %d", data.Used, data.UsedStatfs)
	}
	if _, ok := m.Describe("/"); !ok {
		t.Error("/ missing from the mount table")
	}
}

func TestGetVolAttrsMatchesStatfsSize(t *testing.T) {
	requireDataVolume(t)
	var st unix.Statfs_t
	if err := unix.Statfs(mac.DataRoot, &st); err != nil {
		t.Fatal(err)
	}
	attrs, err := mac.GetVolAttrs(mac.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(st.Blocks) * int64(st.Bsize); attrs.Size != want {
		t.Errorf("VolAttrs.Size = %d, want statfs blocks*bsize = %d", attrs.Size, want)
	}
	if attrs.SpaceUsed <= 0 {
		t.Errorf("VolAttrs.SpaceUsed = %d, want a positive volume usage", attrs.SpaceUsed)
	}
	if attrs.SpaceUsed >= attrs.Size {
		t.Errorf("VolAttrs.SpaceUsed = %d, want less than the container size %d", attrs.SpaceUsed, attrs.Size)
	}
}

func TestCaptureLive(t *testing.T) {
	requireDataVolume(t)
	m, err := ReadMountTable()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(m)
	if err != nil {
		t.Fatal(err)
	}
	if s.At.IsZero() {
		t.Error("snapshot has no timestamp")
	}
	if len(s.Volumes)+len(s.Errors) != m.Len() {
		t.Errorf("captured %d volumes + %d errors, want %d mounts", len(s.Volumes), len(s.Errors), m.Len())
	}
	c, ok := s.ContainerOf(mac.DataRoot)
	if !ok {
		t.Fatal("no container for the data volume")
	}
	if c.Used != c.Total-c.Free {
		t.Errorf("container used %d != total %d - free %d", c.Used, c.Total, c.Free)
	}
	var sum int64
	for _, v := range c.Volumes {
		sum += v.Used
	}
	if c.Overhead != c.Used-sum {
		t.Errorf("Overhead = %d, want %d", c.Overhead, c.Used-sum)
	}
	// A few GB of container structure is expected; a wildly negative number
	// means per-volume used came from the wrong source.
	if c.Overhead < 0 || c.Overhead > 20<<30 {
		t.Errorf("Overhead = %d bytes, want a small positive number", c.Overhead)
	}
}

func TestCollectLive(t *testing.T) {
	requireDataVolume(t)
	f, err := Collect(context.Background(), mac.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if f.Mounts == nil || f.Mounts.Len() == 0 {
		t.Fatal("no mounts collected")
	}
	if f.Container.ID == "" {
		t.Error("no container for the scan root")
	}
	rv, ok := f.RootVolume()
	if !ok || rv.MountPoint != mac.DataRoot {
		t.Fatalf("RootVolume = %+v, %v", rv, ok)
	}
	if f.Euid != os.Geteuid() {
		t.Errorf("Euid = %d, want %d", f.Euid, os.Geteuid())
	}
	if _, _, _, ok := f.Drift(); ok {
		t.Error("Drift reported before Finish")
	}
	if err := f.Finish(); err != nil {
		t.Fatal(err)
	}
	before, after, drift, ok := f.Drift()
	if !ok {
		t.Fatal("Drift not available after Finish")
	}
	if before <= 0 || after <= 0 {
		t.Errorf("drift inputs look wrong: before=%d after=%d", before, after)
	}
	if drift < 0 {
		t.Errorf("drift = %d, want an absolute value", drift)
	}

	if mac.CgoEnabled {
		if !f.Purgeable.Known {
			t.Errorf("purgeable unknown in a cgo build: %s", f.Purgeable.Err)
		}
		if f.Purgeable.Source != "foundation" {
			t.Errorf("purgeable source = %q, want foundation", f.Purgeable.Source)
		}
		if f.Purgeable.ImportantUsage <= 0 {
			t.Error("no important-usage capacity recorded")
		}
	} else if f.Purgeable.Known {
		t.Error("purgeable reported as known without cgo")
	}

	if !f.Snapshots.Known {
		t.Logf("tmutil unavailable: %s", f.Snapshots.Err)
	}
}

func TestReadPurgeableCancelled(t *testing.T) {
	requireDataVolume(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := ReadPurgeable(ctx, mac.DataRoot)
	if p.Known {
		t.Error("purgeable reported as known despite a cancelled context")
	}
	if p.Err == "" {
		t.Error("no reason recorded")
	}
}
