package ledger

import (
	"fmt"
	"path/filepath"

	"github.com/asamgx/storix/internal/volume"
	"github.com/asamgx/storix/internal/walk"
)

// reconcileVolume computes the volume-level identity
//
//	used_after = scanned + purgeable + residual
//
// and the verdict on it. The residual is whatever is left over, so the
// identity is arithmetic rather than a claim; the claim is Reconciles, which
// asks whether the leftover is no larger than the drift the volume showed
// while the walk was running.
func (l *Ledger) reconcileVolume(f *volume.Facts, t *walk.Tree) {
	rv, ok := f.RootVolume()
	if !ok {
		l.Reason = "the volume holding the scan root could not be read, so there is nothing to reconcile against"
		l.Residual = Line{ID: "residual", Label: "residual", Note: "no volume reading"}
		l.Purgeable = purgeableLine(f)
		return
	}

	l.Volume.MountPoint = rv.MountPoint
	l.PartialRoot = !coversVolume(f.Root, rv)

	before, after, drift, hasAfter := f.Drift()
	if !hasAfter {
		before, after, drift = rv.Used, rv.Used, 0
	}
	l.Volume.UsedBefore = before
	l.Volume.UsedAfter = after
	l.Volume.Drift = drift
	l.Volume.Tolerance = drift
	l.Volume.Known = hasAfter

	l.Purgeable = purgeableLine(f)
	residual := after - l.Scanned.Bytes - l.Purgeable.Bytes
	l.Residual = Line{
		ID:    "residual",
		Label: residualLabel(residual, len(t.Errors)),
		Bytes: residual,
		Known: true,
	}
	if !l.Purgeable.Known {
		l.Residual.Note = "includes purgeable space, which could not be read"
	}

	l.Reconciles = !l.PartialRoot && abs(residual) <= l.Volume.Tolerance
	l.Reason = verdict(l, residual)
}

// coversVolume reports whether the scan root is the whole of the volume that
// owns it. A mount point crossing a firmlink answers to two paths, and the
// walk meets it by the data-volume one.
func coversVolume(root string, v volume.Volume) bool {
	root = filepath.Clean(root)
	return root == v.MountPoint || (v.DataPath != "" && root == v.DataPath)
}

// purgeableLine turns the purgeable reading into a line. An unreadable
// reading contributes zero bytes and says so: treating it as zero silently
// would move real space into the residual without a word.
func purgeableLine(f *volume.Facts) Line {
	p := f.Purgeable
	if !p.Known {
		// The reason a reading failed can be a sentence; it belongs in the
		// hint, which has a whole line, not in a table cell.
		return Line{ID: "purgeable", Label: "purgeable", Known: false,
			Note: "unknown; folded into the residual below"}
	}
	return Line{
		ID:    "purgeable",
		Label: "purgeable",
		Bytes: p.Bytes,
		Known: true,
		Note:  "freed when space runs short; may overlap scanned bytes",
	}
}

// residualLabel names the residual according to its sign. A positive residual
// is space the walk could not see; a negative one is space the walk saw twice,
// which is what APFS clones do to an lstat-based sum.
func residualLabel(residual int64, unreadable int) string {
	switch {
	case residual > 0:
		return fmt.Sprintf("filesystem metadata, content of %d unreadable %s, and unaccounted",
			unreadable, plural(unreadable, "directory", "directories"))
	case residual < 0:
		return "shared/cloned blocks (APFS clones counted twice by the scan)"
	default:
		return "nothing unaccounted"
	}
}

// verdict explains the Reconciles flag in one sentence.
func verdict(l *Ledger, residual int64) string {
	u := l.Units
	switch {
	case l.PartialRoot:
		return fmt.Sprintf("partial root: identities are not expected to hold, because %s is part of the volume at %s rather than the whole of it",
			l.Root, l.Volume.MountPoint)
	case !l.Volume.Known:
		return fmt.Sprintf("no closing space reading, so the tolerance is zero and the %s residual cannot be judged",
			u.Bytes(abs(residual)))
	case l.Reconciles:
		return fmt.Sprintf("the %s residual is within the %s the volume drifted while the walk ran",
			u.Bytes(abs(residual)), u.Bytes(l.Volume.Tolerance))
	case residual < 0:
		return fmt.Sprintf("the scan counted %s more than the volume reports as used, beyond the %s of drift; APFS clones report their full size on every copy, so a negative residual is expected here",
			u.Bytes(-residual), u.Bytes(l.Volume.Tolerance))
	default:
		return fmt.Sprintf("the %s residual exceeds the %s of drift; APFS directory inodes report zero blocks, so several gigabytes of filesystem metadata are invisible to any lstat-based sum and land here",
			u.Bytes(residual), u.Bytes(l.Volume.Tolerance))
	}
}

// reconcileContainer computes the container-level identity
//
//	container_used = data + macOS volumes + container overhead
//
// The closing snapshot is preferred so that the data volume's row here is the
// same reading as used_after above; mixing the two would leave the identity
// off by the drift.
func (l *Ledger) reconcileContainer(f *volume.Facts) {
	if l.Volume.MountPoint == "" {
		return
	}
	c, fromAfter := containerReading(f, l.Volume.MountPoint)
	l.Container = Container{
		ID:       c.ID,
		Total:    c.Total,
		Free:     c.Free,
		Used:     c.Used,
		Overhead: c.Overhead,
		Known:    c.ID != "",
	}

	dataUsed := l.Volume.UsedAfter
	for _, v := range c.Volumes {
		if v.MountPoint == l.Volume.MountPoint {
			dataUsed = v.Used
			continue
		}
		l.MacOS = append(l.MacOS, Line{
			ID:    v.MountPoint,
			Label: v.MountPoint,
			Bytes: v.Used,
			Known: true,
			Note:  v.Device,
		})
	}
	l.Data = Line{
		ID:    "data",
		Label: l.Volume.MountPoint,
		Bytes: dataUsed,
		Known: l.Container.Known || l.Volume.Known,
	}
	l.Overhead = Line{
		ID:    "overhead",
		Label: "container overhead",
		Bytes: c.Overhead,
		Known: l.Container.Known,
		Note:  "container structure outside any volume",
	}
	if l.Container.Known && !fromAfter {
		l.Overhead.Note += " (from the opening reading)"
	}

	l.OtherContainers = otherContainers(f, c.ID, fromAfter)
}

// containerReading returns the container of mountPoint, preferring the
// closing snapshot and falling back to the one taken before the walk.
func containerReading(f *volume.Facts, mountPoint string) (volume.Container, bool) {
	if len(f.After.Volumes) > 0 {
		if c, ok := f.After.ContainerOf(mountPoint); ok {
			return c, true
		}
	}
	if c, ok := f.Before.ContainerOf(mountPoint); ok {
		return c, false
	}
	return f.Container, false
}

// otherContainers lists the volumes of every other physical container. They
// are shown because a reader wonders where they went, and excluded from every
// sum because they are not on the same disk.
func otherContainers(f *volume.Facts, id string, fromAfter bool) []Line {
	snap := f.Before
	if fromAfter {
		snap = f.After
	}
	var out []Line
	for _, c := range snap.Containers() {
		if c.ID == id {
			continue
		}
		for _, v := range c.Volumes {
			out = append(out, Line{
				ID:    v.MountPoint,
				Label: v.MountPoint,
				Bytes: v.Used,
				Known: true,
				Note:  "container " + c.ID + ", not part of the sums above",
			})
		}
	}
	return out
}

// abs is the absolute value of an int64.
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
