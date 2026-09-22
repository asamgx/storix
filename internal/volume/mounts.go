// Package volume reads the macOS mount table and the per-volume and
// container-level space numbers the ledger reconciles against.
//
// Two macOS facts shape this package. First, the sealed system volume and the
// data volume share an st_dev, so mount boundaries must be detected by
// comparing paths against the mount table, never by device number. Second,
// every APFS volume in a container reports the same total and the same free
// space, because free space is container-wide; only per-volume used is
// meaningful, and the gap between the sum of per-volume used and the
// container's own used is the container overhead line in the ledger.
package volume

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/asamgx/storix/internal/mac"
)

// Volume is one mounted filesystem.
//
// On APFS every field except Used describes the whole container: each volume
// of a container reports the same f_blocks, f_bfree and f_bavail (verified on
// macOS 26, where all five disk3 volumes report bfree == bavail == the
// container free space). Per-volume usage therefore cannot come from statfs;
// it comes from getattrlist ATTR_VOL_SPACEUSED, which matches df to the byte.
type Volume struct {
	MountPoint string // f_mntonname, e.g. "/System/Volumes/Data"
	// DataPath is the same mount point seen from the data volume, set when
	// the mount point crosses a firmlink: the NFS mount the kernel reports
	// at /Users/u/OrbStack is also reachable, and is met by the walk, at
	// /System/Volumes/Data/Users/u/OrbStack. Empty when the two coincide.
	DataPath  string
	Device    string // f_mntfromname, e.g. "/dev/disk3s5" or "OrbStack:/OrbStack"
	FSType    string // f_fstypename, e.g. "apfs", "autofs", "nfs"
	Container string // "disk3" parsed from /dev/disk3s5; "" for non-/dev devices
	Bsize     int64

	Total int64 // f_blocks * f_bsize: container size for APFS
	Free  int64 // f_bfree * f_bsize: container free space for APFS
	Avail int64 // f_bavail * f_bsize: free space available to an unprivileged writer

	// Used is this volume's own usage, from getattrlist when available.
	Used int64
	// UsedStatfs is (f_blocks - f_bfree) * f_bsize, which on APFS is the
	// container's used bytes, not the volume's. Kept for the doctor
	// cross-check and as the fallback value.
	UsedStatfs int64
	// UsedSource is "getattrlist" or "statfs".
	UsedSource string

	Flags    uint32 // MNT_LOCAL, MNT_RDONLY, MNT_AUTOMOUNTED, MNT_DONTBROWSE…
	Local    bool
	ReadOnly bool
}

// containerRe extracts the APFS container from a device node: /dev/disk3s5
// belongs to container disk3.
var containerRe = regexp.MustCompile(`^/dev/(disk\d+)s`)

// VolumeFromStatfs converts one statfs result into a Volume.
func VolumeFromStatfs(st *unix.Statfs_t) Volume {
	bsize := int64(st.Bsize)
	used := int64(st.Blocks-st.Bfree) * bsize
	v := Volume{
		MountPoint: unix.ByteSliceToString(st.Mntonname[:]),
		Device:     unix.ByteSliceToString(st.Mntfromname[:]),
		FSType:     unix.ByteSliceToString(st.Fstypename[:]),
		Bsize:      bsize,
		Total:      int64(st.Blocks) * bsize,
		Free:       int64(st.Bfree) * bsize,
		Avail:      int64(st.Bavail) * bsize,
		Used:       used,
		UsedStatfs: used,
		UsedSource: UsedFromStatfs,
		Flags:      st.Flags,
		Local:      st.Flags&unix.MNT_LOCAL != 0,
		ReadOnly:   st.Flags&unix.MNT_RDONLY != 0,
	}
	if m := containerRe.FindStringSubmatch(v.Device); m != nil {
		v.Container = m[1]
	}
	v.DataPath = dataVolumeAlias(v.MountPoint)
	return v
}

// dataVolumeAlias returns the mount point's path as the walk will meet it,
// under DataRoot, or "" when the two coincide or the mount is not on the data
// volume. The firmlink table answers for nearly everything; a top-level
// symlink such as /home is resolved as a fallback, which touches the
// filesystem only for mounts the table does not classify.
func dataVolumeAlias(mountPoint string) string {
	if dp, ok := mac.DataVolumePath(mountPoint); ok {
		if dp == mountPoint {
			return ""
		}
		return dp
	}
	resolved, err := filepath.EvalSymlinks(mountPoint)
	if err != nil || resolved == mountPoint {
		return ""
	}
	if dp, ok := mac.DataVolumePath(resolved); ok && dp != mountPoint {
		return dp
	}
	return ""
}

// Sources of a Volume's Used value.
const (
	UsedFromStatfs      = "statfs"
	UsedFromGetattrlist = "getattrlist"
)

// ApplyVolumeUsed replaces the statfs-derived Used with the volume's own
// usage from getattrlist. It reports whether the attribute was available;
// when it is not, Used keeps the statfs value, which is correct for
// non-APFS mounts such as NFS.
func ApplyVolumeUsed(v *Volume) bool {
	attrs, err := mac.GetVolAttrs(v.MountPoint)
	if err != nil {
		return false
	}
	v.Used = attrs.SpaceUsed
	v.UsedSource = UsedFromGetattrlist
	return true
}

// MountTable is the set of mounted filesystems, sorted by mount point.
type MountTable struct {
	volumes []Volume
	byMount map[string]int
}

// NewMountTable builds a table from volumes. Exported for tests, which build
// tables from recorded statfs fixtures.
func NewMountTable(vols []Volume) *MountTable {
	m := &MountTable{
		volumes: append([]Volume(nil), vols...),
		byMount: make(map[string]int, len(vols)),
	}
	sort.Slice(m.volumes, func(i, j int) bool { return m.volumes[i].MountPoint < m.volumes[j].MountPoint })
	for i, v := range m.volumes {
		m.byMount[v.MountPoint] = i
		if v.DataPath != "" {
			m.byMount[v.DataPath] = i
		}
	}
	return m
}

// ReadMountTable reads the live mount table. MNT_NOWAIT keeps a stale network
// mount from blocking the call: cached numbers are returned instead.
func ReadMountTable() (*MountTable, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, fmt.Errorf("getfsstat (count): %w", err)
	}
	// Mounts can appear between the two calls; ask for a few extra slots.
	buf := make([]unix.Statfs_t, n+4)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, fmt.Errorf("getfsstat: %w", err)
	}
	vols := make([]Volume, 0, n)
	for i := range buf[:n] {
		v := VolumeFromStatfs(&buf[i])
		ApplyVolumeUsed(&v)
		vols = append(vols, v)
	}
	return NewMountTable(vols), nil
}

// Volumes returns the mounted volumes, sorted by mount point.
func (m *MountTable) Volumes() []Volume {
	if m == nil {
		return nil
	}
	return append([]Volume(nil), m.volumes...)
}

// Len returns the number of mounts.
func (m *MountTable) Len() int {
	if m == nil {
		return 0
	}
	return len(m.volumes)
}

// Describe returns the volume mounted exactly at path, so a caller that has
// refused to descend into a mount point can name its type and source. Either
// spelling of a data-volume mount point works: the firmlink path the kernel
// reports and the DataRoot path the walk meets it by.
func (m *MountTable) Describe(path string) (Volume, bool) {
	if m == nil {
		return Volume{}, false
	}
	i, ok := m.byMount[filepath.Clean(path)]
	if !ok {
		return Volume{}, false
	}
	return m.volumes[i], true
}

// IsMountPoint reports whether path is itself a mount point. The walker calls
// this for every directory it is about to descend into and refuses any mount
// point other than the scan root.
func (m *MountTable) IsMountPoint(path string) bool {
	_, ok := m.Describe(path)
	return ok
}

// Nested returns the volumes mounted strictly below root, such as the autofs
// mount at /System/Volumes/Data/home and an NFS mount under a home directory.
// A mount point is matched by either of its paths, so an NFS mount the kernel
// reports at /Users/u/OrbStack is nested inside the data-volume scan root.
func (m *MountTable) Nested(root string) []Volume {
	if m == nil {
		return nil
	}
	prefix := filepath.Clean(root)
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	var out []Volume
	for _, v := range m.volumes {
		if strings.HasPrefix(v.MountPoint, prefix) ||
			(v.DataPath != "" && strings.HasPrefix(v.DataPath, prefix)) {
			out = append(out, v)
		}
	}
	return out
}

// Owning returns the volume whose mount point is the longest prefix of path,
// which is the volume the path's bytes live on.
func (m *MountTable) Owning(path string) (Volume, bool) {
	if m == nil {
		return Volume{}, false
	}
	p := filepath.Clean(path)
	best, bestLen := Volume{}, -1
	covers := func(mp string) bool {
		return mp != "" && (p == mp || mp == "/" || strings.HasPrefix(p, mp+"/"))
	}
	for _, v := range m.volumes {
		for _, mp := range [2]string{v.MountPoint, v.DataPath} {
			if covers(mp) && len(mp) > bestLen {
				best, bestLen = v, len(mp)
			}
		}
	}
	return best, bestLen >= 0
}
