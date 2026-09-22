package volume

import (
	"errors"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// VolumeError records a mount point whose statfs failed, so a stale network
// mount degrades one line of the report instead of the whole scan.
type VolumeError struct {
	MountPoint string
	Err        string
}

// Snapshot is the space reading of every mount at one instant. Used space
// drifts by hundreds of megabytes on an idle machine, so the scan takes one
// snapshot before the walk and one after and derives its tolerance from the
// difference.
type Snapshot struct {
	At      time.Time
	Volumes []Volume
	Errors  []VolumeError
}

// Capture calls statfs on every mount point in the table. A mount whose
// statfs fails is left out of Volumes and recorded in Errors.
func Capture(m *MountTable) (Snapshot, error) {
	if m == nil {
		return Snapshot{}, errors.New("volume: nil mount table")
	}
	s := Snapshot{At: time.Now(), Volumes: make([]Volume, 0, m.Len())}
	for _, known := range m.Volumes() {
		var st unix.Statfs_t
		if err := unix.Statfs(known.MountPoint, &st); err != nil {
			s.Errors = append(s.Errors, VolumeError{MountPoint: known.MountPoint, Err: err.Error()})
			continue
		}
		v := VolumeFromStatfs(&st)
		ApplyVolumeUsed(&v)
		s.Volumes = append(s.Volumes, v)
	}
	return s, nil
}

// Find returns the volume mounted at mountPoint in this snapshot.
func (s Snapshot) Find(mountPoint string) (Volume, bool) {
	p := filepath.Clean(mountPoint)
	for _, v := range s.Volumes {
		if v.MountPoint == p {
			return v, true
		}
	}
	return Volume{}, false
}
