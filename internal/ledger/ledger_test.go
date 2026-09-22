package ledger

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/volume"
	"github.com/asamgx/storix/internal/walk"
)

// Fixed instants so a fabricated scan reads the same on every machine.
var (
	before = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	after  = time.Date(2026, 9, 22, 10, 0, 20, 0, time.UTC)
)

// Space numbers modelled on the planning machine: a 245 GB container holding
// the data volume and the four small macOS volumes, with a few gigabytes of
// container structure belonging to none of them.
const (
	total    = 245_107_195_904
	avail    = 34_493_267_968
	dataUsed = 181_046_386_688
	sysUsed  = 12_639_330_304
	vmUsed   = 6_442_450_944
	preUsed  = 9_042_919_424
	updUsed  = 2_899_968
)

// vols builds the container's volumes with the data volume's used bytes set
// to used; every APFS volume of a container reports the same total and free.
func vols(used int64) []volume.Volume {
	mk := func(mount, device string, u int64) volume.Volume {
		return volume.Volume{
			MountPoint: mount, Device: device, FSType: "apfs", Container: "disk3",
			Bsize: 4096, Total: total, Free: avail, Avail: avail,
			Used: u, UsedStatfs: total - avail, UsedSource: volume.UsedFromGetattrlist,
			Local: true,
		}
	}
	return []volume.Volume{
		mk("/", "/dev/disk3s1s1", sysUsed),
		mk(mac.DataRoot, "/dev/disk3s5", used),
		mk("/System/Volumes/Preboot", "/dev/disk3s2", preUsed),
		mk("/System/Volumes/Update", "/dev/disk3s4", updUsed),
		mk("/System/Volumes/VM", "/dev/disk3s6", vmUsed),
	}
}

// fakeFacts fabricates the volume facts a scan would have collected.
func fakeFacts(root string, usedBefore, usedAfter int64, p volume.Purgeable) *volume.Facts { //nolint:unparam // usedAfter is the axis a future case will vary
	beforeVols, afterVols := vols(usedBefore), vols(usedAfter)
	return &volume.Facts{
		Root:      root,
		Mounts:    volume.NewMountTable(beforeVols),
		Before:    volume.Snapshot{At: before, Volumes: beforeVols},
		After:     volume.Snapshot{At: after, Volumes: afterVols},
		Purgeable: p,
		Snapshots: volume.Snapshots{Known: true},
		Terminal:  mac.Terminal{Program: "ghostty", AppName: "Ghostty"},
		FDA:       mac.TCCProbe{Path: "/Users/u/Library/Mail", Granted: true},
		Dataless:  mac.PolicyOff,
		Euid:      501,
	}
}

// known is a successful purgeable reading.
func known(n int64) volume.Purgeable {
	return volume.Purgeable{Bytes: n, Known: true, Source: "foundation"}
}

// tree builds a finished walk.Tree whose root holds the given allocated
// bytes, without touching a filesystem.
func tree(root string, bytes int64) *walk.Tree {
	r := &walk.Node{Name: root, Kind: walk.KindDir, Bytes: bytes, Apparent: bytes, Files: 3, Dirs: 1}
	return &walk.Tree{
		Root:     r,
		Nodes:    []*walk.Node{r},
		Started:  before,
		Finished: after,
	}
}

// child attaches a node to a tree's root and to its preorder index.
func child(t *walk.Tree, n *walk.Node) *walk.Node {
	n.Parent = t.Root
	t.Root.Children = append(t.Root.Children, n)
	t.Nodes = append(t.Nodes, n)
	return n
}

func TestVolumeIdentityHoldsToTheByte(t *testing.T) {
	const scanned = 170_000_000_000
	f := fakeFacts(mac.DataRoot, dataUsed-100_000_000, dataUsed, known(1_573_741_824))
	l := Build(f, tree(mac.DataRoot, scanned), units.Decimal)

	if l.Scanned.Bytes != scanned {
		t.Errorf("scanned = %d, want %d", l.Scanned.Bytes, scanned)
	}
	if got := l.Scanned.Bytes + l.Purgeable.Bytes + l.Residual.Bytes; got != l.Volume.UsedAfter {
		t.Errorf("scanned + purgeable + residual = %d, want used_after %d", got, l.Volume.UsedAfter)
	}
	if l.Volume.UsedAfter != dataUsed {
		t.Errorf("used_after = %d, want %d", l.Volume.UsedAfter, dataUsed)
	}
	if want := int64(100_000_000); l.Volume.Drift != want || l.Volume.Tolerance != want {
		t.Errorf("drift/tolerance = %d/%d, want %d", l.Volume.Drift, l.Volume.Tolerance, want)
	}
	if l.PartialRoot {
		t.Error("a scan of the whole data volume was reported as a partial root")
	}
}

func TestResidualLabelFlipsWithSign(t *testing.T) {
	cases := []struct {
		name    string
		scanned int64
		want    string
		sign    int
	}{
		{"positive", dataUsed - 20_000_000_000, "unaccounted", +1},
		{"negative", dataUsed + 13_000_000_000, "cloned", -1},
		{"zero", dataUsed, "nothing unaccounted", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, volume.Purgeable{})
			l := Build(f, tree(mac.DataRoot, c.scanned), units.Decimal)

			switch {
			case c.sign > 0 && l.Residual.Bytes <= 0:
				t.Fatalf("residual = %d, want positive", l.Residual.Bytes)
			case c.sign < 0 && l.Residual.Bytes >= 0:
				t.Fatalf("residual = %d, want negative", l.Residual.Bytes)
			case c.sign == 0 && l.Residual.Bytes != 0:
				t.Fatalf("residual = %d, want zero", l.Residual.Bytes)
			}
			if !strings.Contains(l.Residual.Label, c.want) {
				t.Errorf("residual label = %q, want it to mention %q", l.Residual.Label, c.want)
			}
			if got := l.Scanned.Bytes + l.Purgeable.Bytes + l.Residual.Bytes; got != l.Volume.UsedAfter {
				t.Errorf("identity broken: %d != %d", got, l.Volume.UsedAfter)
			}
		})
	}
}

func TestReconcilesWhenResidualIsWithinTheDrift(t *testing.T) {
	// The volume grew by 1 GB during the walk and the scan came up 1 byte
	// short: well inside the tolerance the drift sets.
	f := fakeFacts(mac.DataRoot, dataUsed-1_000_000_000, dataUsed, known(0))
	l := Build(f, tree(mac.DataRoot, dataUsed-1), units.Decimal)

	if !l.Reconciles {
		t.Errorf("Reconciles = false, want true; residual %d tolerance %d (%s)",
			l.Residual.Bytes, l.Volume.Tolerance, l.Reason)
	}
	if !strings.Contains(l.Reason, "drifted") {
		t.Errorf("reason = %q, want it to explain the drift", l.Reason)
	}
}

func TestDoesNotReconcileWhenResidualExceedsTheDrift(t *testing.T) {
	f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0))
	l := Build(f, tree(mac.DataRoot, dataUsed-20_000_000_000), units.Decimal)

	if l.Reconciles {
		t.Error("Reconciles = true, want false with a 20 GB residual and no drift")
	}
	if !strings.Contains(l.Reason, "metadata") {
		t.Errorf("reason = %q, want it to name filesystem metadata", l.Reason)
	}
}

func TestUnknownPurgeableCountsAsZeroAndSaysSo(t *testing.T) {
	p := volume.Purgeable{Known: false, Err: "mac: not available in this build (built without cgo)"}
	f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, p)
	l := Build(f, tree(mac.DataRoot, 170_000_000_000), units.Decimal)

	if l.Purgeable.Known || l.Purgeable.Bytes != 0 {
		t.Errorf("purgeable = %+v, want unknown and zero", l.Purgeable)
	}
	if !strings.Contains(l.Purgeable.Note, "residual") {
		t.Errorf("purgeable note = %q, want it to say the bytes are in the residual", l.Purgeable.Note)
	}
	if got := l.Scanned.Bytes + l.Purgeable.Bytes + l.Residual.Bytes; got != l.Volume.UsedAfter {
		t.Errorf("identity broken with unknown purgeable: %d != %d", got, l.Volume.UsedAfter)
	}
	if !hasHint(l, HintPurgeable) {
		t.Error("no hint about the unknown purgeable reading")
	}
}

func TestContainerIdentityHoldsToTheByte(t *testing.T) {
	f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0))
	l := Build(f, tree(mac.DataRoot, 170_000_000_000), units.Decimal)

	if !l.Container.Known || l.Container.ID != "disk3" {
		t.Fatalf("container = %+v, want disk3", l.Container)
	}
	sum := l.Data.Bytes + l.Overhead.Bytes
	for _, m := range l.MacOS {
		sum += m.Bytes
	}
	if sum != l.Container.Used {
		t.Errorf("data + macOS + overhead = %d, want container used %d", sum, l.Container.Used)
	}
	if l.Container.Used+l.Container.Free != l.Container.Total {
		t.Errorf("used + free = %d, want total %d", l.Container.Used+l.Container.Free, l.Container.Total)
	}
	if l.Data.Bytes != l.Volume.UsedAfter {
		t.Errorf("the container's data row (%d) and used_after (%d) came from different readings",
			l.Data.Bytes, l.Volume.UsedAfter)
	}
	if len(l.MacOS) != 4 {
		t.Errorf("macOS lines = %d, want 4 (system, preboot, update, vm)", len(l.MacOS))
	}
}

func TestPartialRootDegradesGracefully(t *testing.T) {
	root := mac.DataRoot + "/Users/u/Library"
	f := fakeFacts(root, dataUsed, dataUsed, known(0))
	l := Build(f, tree(root, 40_000_000_000), units.Decimal)

	if !l.PartialRoot {
		t.Fatal("PartialRoot = false, want true for a directory root")
	}
	if l.Reconciles {
		t.Error("Reconciles = true, want false under a partial root")
	}
	if !strings.Contains(l.Reason, "partial root") {
		t.Errorf("reason = %q, want it to start from the partial root", l.Reason)
	}
	// The identities are still computed against the owning volume.
	if l.Volume.MountPoint != mac.DataRoot {
		t.Errorf("volume = %q, want the owning volume %q", l.Volume.MountPoint, mac.DataRoot)
	}
	if got := l.Scanned.Bytes + l.Purgeable.Bytes + l.Residual.Bytes; got != l.Volume.UsedAfter {
		t.Errorf("identity broken under a partial root: %d != %d", got, l.Volume.UsedAfter)
	}
	if !hasHint(l, HintPartial) {
		t.Error("no hint about the partial root")
	}
}

func TestDatalessSummaryByLocation(t *testing.T) {
	tr := tree(mac.DataRoot, 1_000_000)
	users := child(tr, &walk.Node{Name: "Users", Kind: walk.KindDir})
	// Two retained evicted files and a directory of aggregated ones.
	mkdir := func(parent *walk.Node, name string) *walk.Node {
		n := &walk.Node{Name: name, Kind: walk.KindDir, Parent: parent}
		parent.Children = append(parent.Children, n)
		tr.Nodes = append(tr.Nodes, n)
		return n
	}
	u := mkdir(users, "u")
	lib := mkdir(u, "Library")
	mobile := mkdir(lib, "Mobile Documents")
	desktop := mkdir(u, "Desktop")

	big := &walk.Node{Name: "keynote.key", Kind: walk.KindFile, Parent: mobile,
		Apparent: 2_000_000_000, Flags: walk.FlagDataless}
	mobile.Children = append(mobile.Children, big)
	tr.Nodes = append(tr.Nodes, big)
	desktop.Small = walk.Small{Files: 12, Dataless: 5, Apparent: 400}

	d := datalessSummary(tr)
	if d.Files != 6 {
		t.Errorf("dataless files = %d, want 6 (1 retained + 5 aggregated)", d.Files)
	}
	if d.Apparent != 2_000_000_000 {
		t.Errorf("dataless apparent = %d, want the retained file's size", d.Apparent)
	}
	if len(d.ByLocation) != 2 {
		t.Fatalf("locations = %+v, want Mobile Documents and Desktop", d.ByLocation)
	}
	if d.ByLocation[0].Label != "~/Library/Mobile Documents" || d.ByLocation[0].Count != 1 {
		t.Errorf("first location = %+v", d.ByLocation[0])
	}
	if d.ByLocation[1].Label != "~/Desktop" || d.ByLocation[1].Count != 5 {
		t.Errorf("second location = %+v", d.ByLocation[1])
	}
}

func TestUnreadableGroupedByClass(t *testing.T) {
	tr := tree(mac.DataRoot, 1_000_000)
	tr.Errors = []walk.PathError{
		{Path: mac.DataRoot + "/private/var/db", Op: "readdir", Errno: syscall.EACCES, Class: walk.ErrPermission},
		{Path: mac.DataRoot + "/Users/u/Library/Mail", Op: "readdir", Errno: syscall.EPERM, Class: walk.ErrTCC},
		{Path: mac.DataRoot + "/Users/u/Library/Caches/com.apple.ap.adprivacyd", Op: "readdir", Errno: syscall.EPERM, Class: walk.ErrProtected},
		{Path: mac.DataRoot + "/private/var/folders/zz", Op: "readdir", Errno: syscall.EACCES, Class: walk.ErrPermission},
	}
	f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0))
	f.Euid = 501
	l := Build(f, tr, units.Decimal)

	if len(l.UnreadableByClass) != 3 {
		t.Fatalf("groups = %d, want 3", len(l.UnreadableByClass))
	}
	// Groups come out in class order, whatever order the walk found them in.
	if l.UnreadableByClass[0].Class != walk.ErrPermission || l.UnreadableByClass[0].Count != 2 {
		t.Errorf("first group = %+v, want 2 permission errors", l.UnreadableByClass[0])
	}
	if got := l.UnreadableByClass[0].Paths[0]; got != "/private/var/db" {
		t.Errorf("path = %q, want it stripped of the data volume prefix", got)
	}
	if !hasHint(l, HintProtected) {
		t.Error("no hint about the data vault")
	}
	if !hasHint(l, HintSudo) {
		t.Error("no sudo hint for a non-root scan")
	}
}

func TestHintsSilentWhenNothingIsWrong(t *testing.T) {
	f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0))
	f.Euid = 0
	l := Build(f, tree(mac.DataRoot, dataUsed), units.Decimal)
	if len(l.Hints) != 0 {
		t.Errorf("hints = %+v, want none when access is complete", l.Hints)
	}
}

func TestBuildWithoutFactsStillReportsTheWalk(t *testing.T) {
	l := Build(nil, tree(mac.DataRoot, 123), units.Decimal)
	if l.Scanned.Bytes != 123 {
		t.Errorf("scanned = %d, want 123", l.Scanned.Bytes)
	}
	if l.Reconciles {
		t.Error("Reconciles = true without any volume facts")
	}
}

func TestMissingClosingReadingLeavesTheToleranceAtZero(t *testing.T) {
	f := fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0))
	f.After = volume.Snapshot{} // Finish failed
	l := Build(f, tree(mac.DataRoot, dataUsed), units.Decimal)

	if l.Volume.Known {
		t.Error("Volume.Known = true without a closing reading")
	}
	if l.Volume.UsedAfter != dataUsed {
		t.Errorf("used_after = %d, want the opening reading %d", l.Volume.UsedAfter, dataUsed)
	}
	// The container falls back to the opening snapshot, so the identity
	// must still hold.
	sum := l.Data.Bytes + l.Overhead.Bytes
	for _, m := range l.MacOS {
		sum += m.Bytes
	}
	if sum != l.Container.Used {
		t.Errorf("container identity broken on the fallback reading: %d != %d", sum, l.Container.Used)
	}
	if !strings.Contains(l.Overhead.Note, "opening reading") {
		t.Errorf("overhead note = %q, want it to name the fallback", l.Overhead.Note)
	}
}

// hasHint reports whether the ledger carries a hint of the given kind.
func hasHint(l *Ledger, kind string) bool {
	for _, h := range l.Hints {
		if h.Kind == kind {
			return true
		}
	}
	return false
}
