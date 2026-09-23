// Package ledger turns a walk and the volume facts around it into a
// reconciled account of a volume's used space.
//
// The ledger is honest rather than tidy: the bytes the walk could see, the
// purgeable space the system reports (or an explicit "unknown"), and the
// residual between them and what statfs calls used are all named lines, and
// the residual is displayed whichever way its sign points. Two identities are
// computed and shown so a reader can check the arithmetic:
//
//	used_after     = scanned + purgeable + residual
//	container_used = data_used + macOS volumes + container overhead
//
// Neither identity is an assertion about the machine: the first holds by
// construction because the residual absorbs the difference, and the second
// holds because the overhead is defined as the same difference at container
// level. What carries information is the size of those two gaps, which is why
// they are lines of their own rather than hidden in a total.
package ledger

import (
	"fmt"
	"time"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/volume"
	"github.com/asamgx/storix/internal/walk"
)

// Line is one named row of the ledger. A line with Known false carries no
// trustworthy byte count: it is shown as "unknown" and counted as zero, and
// Note says why.
type Line struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Bytes int64  `json:"bytes"`
	Known bool   `json:"known"`
	// Count is a number of items that belongs with the line, such as the
	// dataless files at a location. Zero when the line counts nothing.
	Count int64  `json:"count,omitempty"`
	Note  string `json:"note,omitempty"`
}

// VolumeLedger is the space reading of the scanned volume, taken twice.
//
// Used space drifts on an idle machine, so the reconciliation tolerance is
// the drift observed across this walk rather than a fixed number.
type VolumeLedger struct {
	MountPoint string `json:"mount_point"`
	UsedBefore int64  `json:"used_before"`
	UsedAfter  int64  `json:"used_after"`
	Drift      int64  `json:"drift"`
	Tolerance  int64  `json:"tolerance"`
	// Known is false when the volume could not be read, or when the closing
	// reading is missing and UsedAfter repeats UsedBefore.
	Known bool `json:"known"`
}

// ErrGroup is the unreadable paths of one error class.
type ErrGroup struct {
	Class walk.ErrClass `json:"-"`
	Name  string        `json:"class"`
	Count int           `json:"count"`
	Paths []string      `json:"paths"` // display paths, in walk order
}

// Container is the APFS container the scanned volume belongs to.
//
// It is a view of volume.Container rather than the type itself so that the
// ledger owns its own JSON shape: the ledger is written to the cache and read
// by the TUI, and its field names should not change when another package
// rearranges its structs. The member volumes are not repeated here; they are
// the Data and MacOS lines.
type Container struct {
	ID       string `json:"id"`
	Total    int64  `json:"total"`
	Free     int64  `json:"free"`
	Used     int64  `json:"used"`
	Overhead int64  `json:"overhead"`
	// Known is false when no APFS container owns the scanned volume, as
	// for a network mount.
	Known bool `json:"known"`
}

// Mount is a mount point inside the scan root that was not entered.
type Mount struct {
	Path   string `json:"path"` // display path
	FSType string `json:"fstype"`
	From   string `json:"from"`
	Reason string `json:"reason"`
}

// Snapshots is the local Time Machine snapshot list. Their blocks are used
// space that belongs to no file the walk can see, so they are named even
// though they cannot be sized.
type Snapshots struct {
	Names []string `json:"names"`
	Known bool     `json:"known"`
	Err   string   `json:"error,omitempty"`
}

// Dataless summarises the evicted cloud files the walk met. Their content is
// in the cloud and occupies no local blocks, so they add nothing to the
// scanned bytes; the apparent size is what would come down on a download.
type Dataless struct {
	Files int64 `json:"files"`
	// Apparent counts retained files only. Files small enough to be folded
	// into their parent's aggregate are counted but their logical size is
	// not separable from the aggregate.
	Apparent   int64  `json:"apparent"`
	ByLocation []Line `json:"by_location,omitempty"`
}

// Counters are the walk's own totals, carried so the report does not have to
// reach into the tree for them.
type Counters struct {
	Files          uint32        `json:"files"`
	Dirs           uint32        `json:"dirs"`
	Nodes          int           `json:"nodes"`
	LinkGroups     uint64        `json:"link_groups"`
	LinkBytesSaved uint64        `json:"link_bytes_saved"`
	Errors         int           `json:"errors"`
	Vanished       uint64        `json:"vanished"`
	SkippedMounts  int           `json:"skipped_mounts"`
	Incomplete     bool          `json:"incomplete"`
	Started        time.Time     `json:"started"`
	Finished       time.Time     `json:"finished"`
	Elapsed        time.Duration `json:"elapsed_ns"`
}

// Hint is a piece of advice about what the scan could not see.
type Hint struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Hint kinds.
const (
	HintFDA       = "full-disk-access"
	HintSudo      = "sudo"
	HintProtected = "protected"
	HintPartial   = "partial-root"
	HintPurgeable = "purgeable"
)

// Ledger is the reconciled account of one scan.
type Ledger struct {
	Units units.Format `json:"-"`

	// Root is the scanned root as a user sees it.
	Root string `json:"root"`
	// PartialRoot is set when the scan covered part of a volume rather than
	// a whole one. The volume identities are still computed, against the
	// volume that owns the root, but they are not expected to hold.
	PartialRoot bool `json:"partial_root"`
	// Reason explains the Reconciles verdict in one sentence.
	Reason string `json:"reason"`

	Volume    VolumeLedger `json:"volume"`
	Container Container    `json:"container"`

	Scanned   Line `json:"scanned"`
	Purgeable Line `json:"purgeable"`
	Residual  Line `json:"residual"`
	Overhead  Line `json:"overhead"`
	// Data is the scanned volume's own used bytes as the container reading
	// saw them. It is the Data row of the container table, and it is read
	// from the same snapshot as Container so that the container identity
	// holds to the byte even when the closing statfs is missing.
	Data Line `json:"data"`

	// Buckets are the twelve top-level buckets of docs/02, always present
	// and always in that order. They partition the volume's used space:
	// the walked ones come from the classification, the others from the
	// readings around the walk. A ledger built without a classification
	// still carries twelve buckets, with every walked byte in Other.
	Buckets []Bucket `json:"buckets,omitempty"`

	// MacOS is one line per non-Data volume of the same container: the
	// sealed system volume, Preboot, VM, Update, Recovery. They are never
	// walked; their sizes come from getattrlist.
	MacOS []Line `json:"macos"`
	// OtherContainers lists volumes on other physical containers. They are
	// informational and part of no sum.
	OtherContainers []Line `json:"other_containers,omitempty"`

	// Reconciles reports whether the residual is within the drift tolerance.
	// It is expected to be false on a real machine: APFS directory inodes
	// report zero blocks and clones are counted twice, which leaves a gap of
	// several gigabytes. Reason says so.
	Reconciles bool `json:"reconciles"`

	// Unreadable is the full per-path error list, kept for a caller that
	// wants every record; the report renders UnreadableByClass instead.
	Unreadable        []walk.PathError `json:"-"`
	UnreadableByClass []ErrGroup       `json:"unreadable"`
	Skipped           []Mount          `json:"skipped_mounts"`
	SkipListed        []string         `json:"skip_listed,omitempty"`
	Dataless          Dataless         `json:"dataless"`
	Snapshots         Snapshots        `json:"snapshots"`
	Hints             []Hint           `json:"hints,omitempty"`
	Counters          Counters         `json:"counters"`
}

// Build assembles the ledger. It never fails: a missing fact becomes a line
// marked unknown with the reason attached, because a scan that cannot read
// purgeable space or take a closing statfs still has something true to say.
//
// The bucket table is still twelve rows long without a classification, with
// every walked byte in Other and a note saying so, because a caller that
// cannot classify should get a shorter answer rather than a different shape.
func Build(f *volume.Facts, t *walk.Tree, u units.Format) *Ledger {
	l := build(f, t, u)
	l.fillBuckets(nil)
	return l
}

// build is the phase 1a ledger, without buckets.
func build(f *volume.Facts, t *walk.Tree, u units.Format) *Ledger {
	l := &Ledger{Units: u}
	if t == nil {
		return l
	}

	l.Root = mac.DisplayPath(t.Root.Path())
	l.fillCounters(t)
	l.fillErrors(t)
	l.Skipped = skippedMounts(t)
	l.SkipListed = displayPaths(t.SkipListed)
	l.Dataless = datalessSummary(t)

	scanned := t.Root.Bytes
	l.Scanned = Line{
		ID:    "scanned",
		Label: "scanned",
		Bytes: scanned,
		Known: true,
		Note:  scannedNote(t),
	}

	if f == nil {
		l.Reason = "no volume facts were collected, so there is nothing to reconcile against"
		return l
	}

	l.Snapshots = Snapshots{Names: f.Snapshots.Names, Known: f.Snapshots.Known, Err: f.Snapshots.Err}
	l.reconcileVolume(f, t)
	l.reconcileContainer(f)
	l.Hints = hints(f, t, l)
	return l
}

// skippedMounts converts the walk's skipped mounts to display paths.
func skippedMounts(t *walk.Tree) []Mount {
	if len(t.SkippedMounts) == 0 {
		return nil
	}
	out := make([]Mount, 0, len(t.SkippedMounts))
	for _, m := range t.SkippedMounts {
		out = append(out, Mount{
			Path:   mac.DisplayPath(m.Path),
			FSType: m.FSType,
			From:   m.From,
			Reason: m.Reason,
		})
	}
	return out
}

// displayPaths converts scan paths to the form a user recognises.
func displayPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, mac.DisplayPath(p))
	}
	return out
}

// fillCounters copies the walk's totals into the ledger.
func (l *Ledger) fillCounters(t *walk.Tree) {
	l.Counters = Counters{
		Files:          t.Root.Files,
		Dirs:           t.Root.Dirs,
		Nodes:          len(t.Nodes),
		LinkGroups:     t.LinkGroups,
		LinkBytesSaved: t.LinkBytesSaved,
		Errors:         len(t.Errors),
		Vanished:       t.Vanished,
		SkippedMounts:  len(t.SkippedMounts),
		Incomplete:     t.Incomplete,
		Started:        t.Started,
		Finished:       t.Finished,
		Elapsed:        t.Finished.Sub(t.Started),
	}
}

// scannedNote names the caveats that apply to the scanned total itself.
func scannedNote(t *walk.Tree) string {
	if t.Incomplete {
		return "interrupted: a lower bound"
	}
	if len(t.Errors) > 0 || len(t.SkippedMounts) > 0 {
		return "allocated bytes, hard links counted once"
	}
	return "allocated bytes"
}

// fillErrors groups the walk's per-path failures by what the user can do
// about them.
func (l *Ledger) fillErrors(t *walk.Tree) {
	l.Unreadable = t.Errors
	if len(t.Errors) == 0 {
		return
	}
	// Class order is the enum order, so the output is stable whatever order
	// the parallel walk recorded errors in.
	index := map[walk.ErrClass]int{}
	for _, e := range t.Errors {
		i, ok := index[e.Class]
		if !ok {
			i = len(l.UnreadableByClass)
			index[e.Class] = i
			l.UnreadableByClass = append(l.UnreadableByClass, ErrGroup{
				Class: e.Class,
				Name:  e.Class.String(),
			})
		}
		g := &l.UnreadableByClass[i]
		g.Count++
		g.Paths = append(g.Paths, mac.DisplayPath(e.Path))
	}
	sortGroups(l.UnreadableByClass)
}

// sortGroups orders error groups by class so two runs print the same report.
func sortGroups(gs []ErrGroup) {
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && gs[j].Class < gs[j-1].Class; j-- {
			gs[j], gs[j-1] = gs[j-1], gs[j]
		}
	}
}

// hints turns the permission context into advice.
func hints(f *volume.Facts, t *walk.Tree, l *Ledger) []Hint {
	var out []Hint
	if l.PartialRoot {
		out = append(out, Hint{
			Kind: HintPartial,
			Text: fmt.Sprintf("this scan covered %s, not the whole of the volume at %s: the volume identities are informational",
				l.Root, l.Volume.MountPoint),
		})
	}
	if !f.FDA.Granted {
		out = append(out, Hint{Kind: HintFDA, Text: mac.FullDiskAccessHint(terminalName(f))})
	}
	if f.Euid != 0 {
		out = append(out, Hint{
			Kind: HintSudo,
			Text: "run `sudo storix scan --system` to include the root-only system directories",
		})
	}
	if n := countClass(t.Errors, walk.ErrProtected); n > 0 {
		out = append(out, Hint{
			Kind: HintProtected,
			Text: fmt.Sprintf("%d %s protected by the system (data vaults); not even Full Disk Access opens %s",
				n, plural(n, "path is", "paths are"), plural(n, "it", "them")),
		})
	}
	if !f.Purgeable.Known {
		text := "purgeable space could not be read, so it is part of the residual rather than a line of its own"
		if f.Purgeable.Err != "" {
			text = "purgeable space could not be read (" + f.Purgeable.Err +
				"), so it is part of the residual rather than a line of its own"
		}
		out = append(out, Hint{Kind: HintPurgeable, Text: text})
	}
	return out
}

// terminalName is the best name for the app that needs Full Disk Access.
func terminalName(f *volume.Facts) string {
	if f.Terminal.AppName != "" {
		return f.Terminal.AppName
	}
	return f.Terminal.Program
}

// countClass counts the errors of one class.
func countClass(errs []walk.PathError, c walk.ErrClass) int {
	n := 0
	for _, e := range errs {
		if e.Class == c {
			n++
		}
	}
	return n
}

// plural picks the singular or plural form for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
