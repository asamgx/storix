package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/volume"
	"github.com/asamgx/storix/internal/walk"
)

// SchemaVersion is the version of the --json document. It changes whenever a
// field is removed or its meaning changes; adding a field does not.
const SchemaVersion = 1

// JSON writes the machine-readable report.
//
// The tree is streamed rather than built as a second data structure: with
// --full it is the whole scan, and a million intermediate structs would cost
// more memory than the walk itself.
func JSON(w io.Writer, r *scan.Result, o Options) error {
	if r == nil || r.Tree == nil || r.Ledger == nil {
		return fmt.Errorf("report: nothing to render")
	}
	o = o.withDefaults()
	bw := bufio.NewWriterSize(w, 64<<10)
	e := &jsonWriter{w: bw, class: r.Class}

	e.begin("{")
	e.field("schema", SchemaVersion)
	e.field("storix", o.Version)
	e.field("root", r.Ledger.Root)
	e.field("units", unitName(o.Units))
	e.field("incomplete", r.Tree.Incomplete)
	e.field("started", r.Tree.Started.UTC().Format(time.RFC3339))
	e.field("finished", r.Tree.Finished.UTC().Format(time.RFC3339))
	e.field("from_cache", r.FromCache)
	if r.FromCache {
		e.field("cache_age_ns", r.CacheAge)
	}
	if r.CachePath != "" {
		e.field("cache_path", r.CachePath)
	}
	e.field("timing", r.Timing)
	e.field("ledger", r.Ledger)
	if c := classificationJSON(r); c != nil {
		e.field("classification", c)
	}
	// The application inventory is the apps lane's own document, emitted
	// whole rather than summarised: `storix apps --json` prints the same
	// shape, and a consumer that has the scan document should not have to
	// run a second command to get the applications out of it.
	if inventory, ok := scan.Apps(r); ok {
		e.field("apps", inventory)
	}
	e.field("facts", factsJSON(r.Facts))
	e.field("counters", r.Ledger.Counters)
	e.field("errors", errorsJSON(r.Tree.Errors))
	e.field("skipped_mounts", r.Ledger.Skipped)
	e.key("tree")
	e.node(r.Tree.Root, 0, o)
	e.end("}")
	e.raw("\n")

	if e.err != nil {
		return e.err
	}
	return bw.Flush()
}

// unitName records which formatting the text report would have used. The
// JSON numbers themselves are always plain bytes.
func unitName(u units.Format) string {
	if u == units.Binary {
		return "binary"
	}
	return "decimal"
}

// displayPath is the path as a user sees it, with the data volume prefix
// stripped.
func displayPath(p string) string { return mac.DisplayPath(p) }

// jsonWriter streams indented JSON. encoding/json indents a value it already
// holds; here the tree is produced as it is written, so the indentation is
// tracked by hand and every leaf value still goes through encoding/json to
// get the escaping right.
type jsonWriter struct {
	w   *bufio.Writer
	err error
	// class is the classification the per-node fields come from, nil when
	// the scan has none: a tree without buckets is still a tree.
	class *classify.Classification
	stack []bool // per level: nothing written at this level yet
}

func (e *jsonWriter) raw(s string) {
	if e.err == nil {
		_, e.err = e.w.WriteString(s)
	}
}

func (e *jsonWriter) indent() string { return strings.Repeat("  ", len(e.stack)) }

// begin opens an object or array.
func (e *jsonWriter) begin(open string) {
	e.raw(open)
	e.stack = append(e.stack, true)
}

// end closes an object or array, putting the brace on its own line unless
// nothing was written inside.
func (e *jsonWriter) end(close string) {
	empty := e.stack[len(e.stack)-1]
	e.stack = e.stack[:len(e.stack)-1]
	if !empty {
		e.raw("\n" + e.indent())
	}
	e.raw(close)
}

// item starts the next element at the current level.
func (e *jsonWriter) item() {
	i := len(e.stack) - 1
	if !e.stack[i] {
		e.raw(",")
	}
	e.stack[i] = false
	e.raw("\n" + e.indent())
}

// key writes an object key and the colon after it.
func (e *jsonWriter) key(k string) {
	e.item()
	e.value(k)
	e.raw(": ")
}

// value writes any value, indented to the current level.
func (e *jsonWriter) value(v any) {
	if e.err != nil {
		return
	}
	b, err := json.MarshalIndent(v, e.indent(), "  ")
	if err != nil {
		e.err = err
		return
	}
	e.raw(string(b))
}

// field writes a key and its value.
func (e *jsonWriter) field(k string, v any) {
	e.key(k)
	e.value(v)
}

// node streams one tree node and, within the limits, its children.
func (e *jsonWriter) node(n *walk.Node, depth int, o Options) {
	e.begin("{")
	e.field("path", n.Display())
	e.field("kind", n.Kind.String())
	e.field("bytes", n.Bytes)
	e.field("apparent", n.Apparent)
	e.field("files", n.Files)
	e.field("dirs", n.Dirs)
	e.field("mtime", time.Unix(n.Mtime, 0).UTC().Format(time.RFC3339))
	if names := flagNames(n.Flags); len(names) > 0 {
		e.field("flags", names)
	}
	if n.Errno != 0 {
		e.field("errno", n.Errno)
	}
	if n.Small != (walk.Small{}) {
		e.field("small", smallJSON{
			Files:    n.Small.Files,
			Dataless: n.Small.Dataless,
			Bytes:    n.Small.Bytes,
			Apparent: n.Small.Apparent,
		})
	}
	e.claim(n)

	kept, dropped := children(n, depth, o)
	if len(kept) > 0 {
		e.key("children")
		e.begin("[")
		for _, c := range kept {
			e.item()
			e.node(c, depth+1, o)
		}
		e.end("]")
	}
	if dropped > 0 {
		e.field("children_omitted", dropped)
	}
	e.end("}")
}

// claim writes the node's place in the classification: which bucket its bytes
// belong to, whose they are, how safely they could be freed, and what decided
// it.
//
// A node that carries no claim of its own gets its nearest ancestor's, marked
// inherited, rather than nothing: the whole point of the inheritance is that
// every byte under a claimed directory belongs to that claim, and a consumer
// filtering the tree by bucket would otherwise lose most of the disk. A node
// in no bucket at all — Other — is left without the fields, which is what
// makes "no bucket key" searchable.
func (e *jsonWriter) claim(n *walk.Node) {
	cl, ok := e.class.OfNode(n)
	if !ok {
		return
	}
	e.field("bucket", cl.Bucket.ID())
	if cl.Owner != "" {
		e.field("owner", cl.Owner)
	}
	e.field("reclaim", cl.Reclaim.String())
	e.field("source", cl.Source.String())
	if _, explicit := e.class.ExplicitAtNode(n); !explicit {
		e.field("inherited", true)
	}
}

// children applies the depth and size limits, returning the children to emit
// and how many were left out. Without limits a full scan is hundreds of
// megabytes of JSON, which is why they are the default.
func children(n *walk.Node, depth int, o Options) (kept []*walk.Node, dropped int) {
	if len(n.Children) == 0 {
		return nil, 0
	}
	if o.Full {
		return n.Children, 0
	}
	if depth >= o.Depth {
		return nil, len(n.Children)
	}
	for _, c := range n.Children {
		if c.Bytes >= o.MinSize {
			kept = append(kept, c)
			continue
		}
		dropped++
	}
	return kept, dropped
}

// smallJSON is a directory's aggregate of leaves too small to keep.
type smallJSON struct {
	Files    uint32 `json:"files"`
	Dataless uint32 `json:"dataless"`
	Bytes    int64  `json:"bytes"`
	Apparent int64  `json:"apparent"`
}

// flagOrder is the flag set in a fixed order, so two runs list them the same
// way. The names are the ones the documentation uses.
var flagOrder = []struct {
	flag walk.Flags
	name string
}{
	{walk.FlagUnreadable, "unreadable"},
	{walk.FlagPartial, "partial"},
	{walk.FlagDataless, "dataless"},
	{walk.FlagCompressed, "compressed"},
	{walk.FlagBundle, "bundle"},
	{walk.FlagMountSkipped, "mount-skipped"},
	{walk.FlagSkipListed, "skip-listed"},
	{walk.FlagLinkOwner, "link-owner"},
	{walk.FlagLinkAlias, "link-alias"},
	{walk.FlagExempt, "exempt"},
}

// flagNames turns a flag set into its names.
func flagNames(f walk.Flags) []string {
	var out []string
	for _, e := range flagOrder {
		if f&e.flag != 0 {
			out = append(out, e.name)
		}
	}
	return out
}

// jsonClassification is the classification half of the document: what the
// detectors found, what the engine concluded, and where the two disagreed.
//
// It is a summary and says so by its caps. The per-node truth is on the tree
// nodes themselves, the bucket totals are under "ledger", and this section
// answers the questions a consumer asks without walking either: which tools
// were probed, what they hold, who owns the most bytes, and which directories
// no rule has reached yet.
type jsonClassification struct {
	Detectors  []jsonDetector      `json:"detectors"`
	Developer  []jsonDevGroup      `json:"developer"`
	Containers []jsonRuntime       `json:"containers"`
	Conflicts  jsonConflicts       `json:"conflicts"`
	Owners     []scan.OwnerRow     `json:"owners"`
	Unmatched  []scan.UnmatchedRow `json:"unmatched"`
	// Timing is how long the engine took, which on a cached scan is how
	// long the reclassification on load took.
	Timing time.Duration `json:"timing_ns"`
}

// jsonDetector is one detector's status row. The probe commands are left out:
// they run to megabytes for the inventory probes, and the evidence a reader
// wants is on the claims.
type jsonDetector struct {
	Name     string        `json:"name"`
	State    string        `json:"state"`
	Reason   string        `json:"reason,omitempty"`
	Duration time.Duration `json:"duration_ns"`
	Verified bool          `json:"verified"`
}

// jsonDevGroup is one detector's developer rows.
type jsonDevGroup struct {
	Detector string           `json:"detector"`
	Tools    []detect.Tool    `json:"tools,omitempty"`
	Projects []detect.Project `json:"projects,omitempty"`
	// Reclaimable is what the tool itself says it would free, which is not
	// storix's arithmetic and is never summed into the ledger.
	Reclaimable int64  `json:"reclaimable,omitempty"`
	ReclaimNote string `json:"reclaim_note,omitempty"`
}

// jsonRuntime is one container runtime, tagged with the detector that found
// it. The embedded runtime carries the two columns of docs/04: what the host
// allocated and what the daemon believes it is using.
type jsonRuntime struct {
	Detector string `json:"detector"`
	detect.Runtime
}

// jsonConflicts is where two sources claimed the same node.
type jsonConflicts struct {
	Total  int                  `json:"total"`
	ByKind []scan.ConflictCount `json:"by_kind"`
	Top    []jsonConflict       `json:"top"`
}

// jsonConflict is one disagreement, with both sources rendered the way the
// why panel prints them.
type jsonConflict struct {
	Node   int32  `json:"node"`
	Path   string `json:"path"`
	Winner string `json:"winner"`
	Loser  string `json:"loser"`
	Bytes  int64  `json:"bytes"`
}

// How much of each list the document carries. The owners are the top 50 of
// docs/03; the conflicts and unmatched roots are a sample for tuning, and the
// full lists are in the scan, not in the report.
const (
	maxJSONOwners    = 50
	maxJSONConflicts = 20
	maxJSONUnmatched = 20
)

// classificationJSON assembles the section, nil when the scan has no
// classification at all.
func classificationJSON(r *scan.Result) *jsonClassification {
	if r == nil || r.Class == nil {
		return nil
	}
	out := &jsonClassification{
		Detectors:  detectorsJSON(r),
		Developer:  developerJSON(r),
		Containers: runtimesJSON(r),
		Conflicts:  conflictsJSON(r),
		Owners:     scan.Owners(r.Class, maxJSONOwners),
		Unmatched:  scan.Unmatched(r, maxJSONUnmatched),
		Timing:     r.Timing.Classify,
	}
	if out.Owners == nil {
		out.Owners = []scan.OwnerRow{}
	}
	if out.Unmatched == nil {
		out.Unmatched = []scan.UnmatchedRow{}
	}
	return out
}

// detectorsJSON is the detectors table, in registry order.
func detectorsJSON(r *scan.Result) []jsonDetector {
	out := make([]jsonDetector, 0, len(r.Detectors))
	for _, st := range r.Detectors {
		out = append(out, jsonDetector{
			Name:     st.Name,
			State:    st.State.String(),
			Reason:   st.Reason,
			Duration: st.Duration,
			Verified: st.Verified,
		})
	}
	return out
}

// developerJSON is every detector's tool and project rows, in registry order.
// Unlike the text section it is not filtered against the classification: a
// consumer of the document can join on the node id itself, and dropping rows
// here would hide a detector's own answer behind the engine's.
func developerJSON(r *scan.Result) []jsonDevGroup {
	out := make([]jsonDevGroup, 0, len(r.Detectors))
	for _, st := range r.Detectors {
		sum := r.Summaries[st.Name]
		if len(sum.Tools) == 0 && len(sum.Projects) == 0 && sum.Reclaimable == 0 {
			continue
		}
		out = append(out, jsonDevGroup{
			Detector:    st.Name,
			Tools:       sum.Tools,
			Projects:    sum.Projects,
			Reclaimable: sum.Reclaimable,
			ReclaimNote: sum.ReclaimNote,
		})
	}
	return out
}

// runtimesJSON is every container runtime the detectors reported.
func runtimesJSON(r *scan.Result) []jsonRuntime {
	out := make([]jsonRuntime, 0, 4)
	for _, st := range r.Detectors {
		for _, rt := range r.Summaries[st.Name].Runtimes {
			out = append(out, jsonRuntime{Detector: st.Name, Runtime: rt})
		}
	}
	return out
}

// conflictsJSON counts the disagreements and lists the largest few.
func conflictsJSON(r *scan.Result) jsonConflicts {
	out := jsonConflicts{
		Total:  len(r.Class.Conflicts),
		ByKind: scan.ConflictCounts(r.Class),
		Top:    make([]jsonConflict, 0, maxJSONConflicts),
	}
	if out.ByKind == nil {
		out.ByKind = []scan.ConflictCount{}
	}
	for i, c := range r.Class.Conflicts {
		if i >= maxJSONConflicts {
			break
		}
		out.Top = append(out.Top, jsonConflict{
			Node:   c.Node,
			Path:   c.Path,
			Winner: c.Winner.String(),
			Loser:  c.Loser.String(),
			Bytes:  c.Bytes,
		})
	}
	return out
}

// jsonError is one unreadable path.
type jsonError struct {
	Path  string `json:"path"`
	Op    string `json:"op"`
	Errno int    `json:"errno"`
	Error string `json:"error"`
	Class string `json:"class"`
}

// errorsJSON converts the walk's per-path failures.
func errorsJSON(errs []walk.PathError) []jsonError {
	out := make([]jsonError, 0, len(errs))
	for _, e := range errs {
		out = append(out, jsonError{
			Path:  displayPath(e.Path),
			Op:    e.Op,
			Errno: int(e.Errno),
			Error: e.Errno.Error(),
			Class: e.Class.String(),
		})
	}
	return out
}

// jsonFacts is the JSON-friendly copy of the volume facts: the mount table
// has unexported fields, the probe carries an error value and the dataless
// policy is an integer, none of which survive a direct marshal.
type jsonFacts struct {
	Root           string           `json:"root"`
	Euid           int              `json:"euid"`
	Terminal       jsonTerminal     `json:"terminal"`
	FullDiskAccess jsonProbe        `json:"full_disk_access"`
	DatalessPolicy string           `json:"dataless_policy"`
	Purgeable      jsonPurgeable    `json:"purgeable"`
	Snapshots      ledger.Snapshots `json:"snapshots"`
	Before         jsonSnapshot     `json:"before"`
	After          jsonSnapshot     `json:"after"`
}

type jsonTerminal struct {
	Program  string `json:"program"`
	BundleID string `json:"bundle_id"`
	AppName  string `json:"app_name"`
	ViaTmux  bool   `json:"via_tmux"`
	PID      int    `json:"pid"`
}

type jsonProbe struct {
	Path    string `json:"path"`
	Granted bool   `json:"granted"`
	Error   string `json:"error,omitempty"`
}

type jsonPurgeable struct {
	Bytes          int64  `json:"bytes"`
	Known          bool   `json:"known"`
	Source         string `json:"source,omitempty"`
	Error          string `json:"error,omitempty"`
	ImportantUsage int64  `json:"important_usage"`
	StatfsAvail    int64  `json:"statfs_avail"`
}

type jsonSnapshot struct {
	At      time.Time         `json:"at"`
	Volumes []jsonVolume      `json:"volumes"`
	Errors  []jsonVolumeError `json:"errors,omitempty"`
}

type jsonVolumeError struct {
	MountPoint string `json:"mount_point"`
	Error      string `json:"error"`
}

type jsonVolume struct {
	MountPoint string `json:"mount_point"`
	DataPath   string `json:"data_path,omitempty"`
	Device     string `json:"device"`
	FSType     string `json:"fstype"`
	Container  string `json:"container,omitempty"`
	Total      int64  `json:"total"`
	Free       int64  `json:"free"`
	Avail      int64  `json:"avail"`
	Used       int64  `json:"used"`
	UsedStatfs int64  `json:"used_statfs"`
	UsedSource string `json:"used_source"`
	Local      bool   `json:"local"`
	ReadOnly   bool   `json:"read_only"`
}

// factsJSON copies the facts into their JSON shape.
func factsJSON(f *volume.Facts) *jsonFacts {
	if f == nil {
		return nil
	}
	out := &jsonFacts{
		Root: displayPath(f.Root),
		Euid: f.Euid,
		Terminal: jsonTerminal{
			Program:  f.Terminal.Program,
			BundleID: f.Terminal.BundleID,
			AppName:  f.Terminal.AppName,
			ViaTmux:  f.Terminal.ViaTmux,
			PID:      f.Terminal.PID,
		},
		FullDiskAccess: jsonProbe{Path: f.FDA.Path, Granted: f.FDA.Granted, Error: errText(f.FDA.Err)},
		DatalessPolicy: f.Dataless.String(),
		Purgeable: jsonPurgeable{
			Bytes:          f.Purgeable.Bytes,
			Known:          f.Purgeable.Known,
			Source:         f.Purgeable.Source,
			Error:          f.Purgeable.Err,
			ImportantUsage: f.Purgeable.ImportantUsage,
			StatfsAvail:    f.Purgeable.StatfsAvail,
		},
		Snapshots: ledger.Snapshots{Names: f.Snapshots.Names, Known: f.Snapshots.Known, Err: f.Snapshots.Err},
		Before:    snapshotJSON(f.Before),
		After:     snapshotJSON(f.After),
	}
	return out
}

// snapshotJSON copies one space reading.
func snapshotJSON(s volume.Snapshot) jsonSnapshot {
	out := jsonSnapshot{At: s.At.UTC(), Volumes: make([]jsonVolume, 0, len(s.Volumes))}
	for _, v := range s.Volumes {
		out.Volumes = append(out.Volumes, jsonVolume{
			MountPoint: v.MountPoint,
			DataPath:   v.DataPath,
			Device:     v.Device,
			FSType:     v.FSType,
			Container:  v.Container,
			Total:      v.Total,
			Free:       v.Free,
			Avail:      v.Avail,
			Used:       v.Used,
			UsedStatfs: v.UsedStatfs,
			UsedSource: v.UsedSource,
			Local:      v.Local,
			ReadOnly:   v.ReadOnly,
		})
	}
	for _, e := range s.Errors {
		out.Errors = append(out.Errors, jsonVolumeError{MountPoint: e.MountPoint, Error: e.Err})
	}
	return out
}

// errText renders an error as a string, empty when there is none.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
