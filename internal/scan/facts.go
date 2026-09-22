package scan

import (
	"errors"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/volume"
)

// factsDoc is the cached form of volume.Facts.
//
// volume.Facts is not itself a JSON document: its mount table keeps unexported
// fields and its Full Disk Access probe carries an error, which marshals to an
// empty object and will not unmarshal back. This shape holds everything the
// report and the TUI read, with the error as its message; the mount table is
// rebuilt from the opening space reading, which lists the same mounts.
type factsDoc struct {
	Root      string             `json:"root"`
	Euid      int                `json:"euid"`
	Terminal  mac.Terminal       `json:"terminal"`
	FDA       probeDoc           `json:"full_disk_access"`
	Dataless  mac.DatalessPolicy `json:"dataless_policy"`
	Purgeable volume.Purgeable   `json:"purgeable"`
	Snapshots volume.Snapshots   `json:"snapshots"`
	Container volume.Container   `json:"container"`
	Before    volume.Snapshot    `json:"before"`
	After     volume.Snapshot    `json:"after"`
}

// probeDoc is mac.TCCProbe with its error as text.
type probeDoc struct {
	Path    string `json:"path"`
	Granted bool   `json:"granted"`
	Err     string `json:"error,omitempty"`
}

// newFactsDoc copies the facts into their cached shape. A nil Facts is cached
// as null and comes back nil, which the report already renders.
func newFactsDoc(f *volume.Facts) *factsDoc {
	if f == nil {
		return nil
	}
	return &factsDoc{
		Root:      f.Root,
		Euid:      f.Euid,
		Terminal:  f.Terminal,
		FDA:       probeDoc{Path: f.FDA.Path, Granted: f.FDA.Granted, Err: errText(f.FDA.Err)},
		Dataless:  f.Dataless,
		Purgeable: f.Purgeable,
		Snapshots: f.Snapshots,
		Container: f.Container,
		Before:    f.Before,
		After:     f.After,
	}
}

// facts rebuilds volume.Facts from the cached document.
func (d *factsDoc) facts() *volume.Facts {
	if d == nil {
		return nil
	}
	f := &volume.Facts{
		Root:      d.Root,
		Mounts:    volume.NewMountTable(d.Before.Volumes),
		Before:    d.Before,
		After:     d.After,
		Container: d.Container,
		Purgeable: d.Purgeable,
		Snapshots: d.Snapshots,
		Terminal:  d.Terminal,
		Dataless:  d.Dataless,
		Euid:      d.Euid,
	}
	f.FDA = mac.TCCProbe{Path: d.FDA.Path, Granted: d.FDA.Granted}
	if d.FDA.Err != "" {
		f.FDA.Err = errors.New(d.FDA.Err)
	}
	return f
}

// errText renders an error as its message, empty when there is none.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
