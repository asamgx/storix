package volume

// Container is an APFS container and the volumes sharing it.
//
// Free space belongs to the container, not to a volume, so Total and Free are
// read from any one member volume and Used is the difference. The sum of the
// members' own used bytes falls short of the container's used bytes by a few
// gigabytes of container-level structure; that gap is Overhead and the ledger
// shows it as its own line rather than hiding it in a residual.
type Container struct {
	ID       string
	Total    int64 // f_blocks * f_bsize of any member volume
	Free     int64 // f_bavail * f_bsize: space available to an unprivileged writer
	Used     int64 // Total - Free
	Overhead int64 // Used - sum of member volumes' Used
	Volumes  []Volume
}

// ContainerOf returns the container of the volume mounted at mountPoint,
// together with every volume of that container present in the snapshot.
// It reports false when the mount point is unknown or its device is not an
// APFS container member (network and virtual mounts).
func (s Snapshot) ContainerOf(mountPoint string) (Container, bool) {
	v, ok := s.Find(mountPoint)
	if !ok || v.Container == "" {
		return Container{}, false
	}
	c := Container{ID: v.Container, Total: v.Total, Free: v.Avail}
	c.Used = c.Total - c.Free
	var sum int64
	for _, m := range s.Volumes {
		if m.Container != c.ID {
			continue
		}
		c.Volumes = append(c.Volumes, m)
		sum += m.Used
	}
	c.Overhead = c.Used - sum
	return c, true
}

// Containers groups every volume in the snapshot by container ID.
func (s Snapshot) Containers() []Container {
	var order []string
	seen := map[string]bool{}
	for _, v := range s.Volumes {
		if v.Container == "" || seen[v.Container] {
			continue
		}
		seen[v.Container] = true
		order = append(order, v.Container)
	}
	out := make([]Container, 0, len(order))
	for _, id := range order {
		for _, v := range s.Volumes {
			if v.Container != id {
				continue
			}
			if c, ok := s.ContainerOf(v.MountPoint); ok {
				out = append(out, c)
			}
			break
		}
	}
	return out
}
