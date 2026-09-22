package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

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
	e := &jsonWriter{w: bw}

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
	w     *bufio.Writer
	err   error
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
