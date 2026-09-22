package volume

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/asamgx/storix/internal/mac"
)

// Facts is everything the ledger needs that does not come from walking files:
// the mount table, space readings before and after the walk, the container,
// purgeable space, snapshots, and the permission context the scan ran in.
type Facts struct {
	Root      string
	Mounts    *MountTable
	Before    Snapshot
	After     Snapshot // filled by Finish
	Container Container
	Purgeable Purgeable
	Snapshots Snapshots
	Terminal  mac.Terminal
	FDA       mac.TCCProbe
	Dataless  mac.DatalessPolicy
	Euid      int
}

// Collect gathers every fact except the closing snapshot. The purgeable and
// snapshot readings shell out or call Foundation, so they run concurrently
// under their own timeouts.
func Collect(ctx context.Context, root string) (*Facts, error) {
	if root == "" {
		root = mac.DataRoot
	}
	root = filepath.Clean(root)

	mounts, err := ReadMountTable()
	if err != nil {
		return nil, err
	}
	before, err := Capture(mounts)
	if err != nil {
		return nil, err
	}

	f := &Facts{
		Root:   root,
		Mounts: mounts,
		Before: before,
		Euid:   os.Geteuid(),
	}

	// The container of the root, falling back to the volume that owns the
	// root when the root is not itself a mount point.
	if c, ok := before.ContainerOf(root); ok {
		f.Container = c
	} else if owner, ok := mounts.Owning(root); ok {
		if c, ok := before.ContainerOf(owner.MountPoint); ok {
			f.Container = c
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); f.Purgeable = ReadPurgeable(ctx, root) }()
	go func() { defer wg.Done(); f.Snapshots = ListLocalSnapshots(ctx, root) }()

	f.Terminal = mac.DetectTerminal()
	f.FDA = mac.ProbeFullDiskAccess()
	policy, err := mac.GetDatalessPolicy()
	if err != nil {
		policy = mac.PolicyUnknown
	}
	f.Dataless = policy

	wg.Wait()
	return f, nil
}

// Finish takes the closing space reading, after the walk has completed.
func (f *Facts) Finish() error {
	if f == nil || f.Mounts == nil {
		return errors.New("volume: Finish on uncollected facts")
	}
	after, err := Capture(f.Mounts)
	if err != nil {
		return err
	}
	f.After = after
	return nil
}

// RootVolume returns the volume of the scan root from the opening snapshot.
func (f *Facts) RootVolume() (Volume, bool) {
	if f == nil {
		return Volume{}, false
	}
	if v, ok := f.Before.Find(f.Root); ok {
		return v, true
	}
	if owner, ok := f.Mounts.Owning(f.Root); ok {
		return f.Before.Find(owner.MountPoint)
	}
	return Volume{}, false
}

// Drift reports the change in the root volume's used bytes across the walk.
// The reconciliation tolerance is derived from it rather than fixed.
func (f *Facts) Drift() (before, after, drift int64, ok bool) {
	rv, ok1 := f.RootVolume()
	av, ok2 := f.After.Find(rv.MountPoint)
	if !ok1 || !ok2 {
		return 0, 0, 0, false
	}
	d := av.Used - rv.Used
	if d < 0 {
		d = -d
	}
	return rv.Used, av.Used, d, true
}
