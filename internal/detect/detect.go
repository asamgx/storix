// Package detect asks the machine's own tools where they keep their bytes.
//
// A catalog rule in internal/classify/catalog says "~/Library/Caches/Homebrew
// is Homebrew's cache". A detector says "I ran `brew --cache` and it said this
// directory, it holds 247 formulae, and `brew cleanup -n` would free 1.2 GB of
// it". The rule is the floor and the detector is the evidence: a detector that
// cannot run degrades to the rule's answer, so the buckets stay right and only
// the explanation is lost.
//
// A detector never fails. Everything that can go wrong — the tool is not
// installed, the daemon is down, the probe took too long, the code panicked —
// becomes a [Status] beside the claims, because a scan that refused to report
// on a disk because podman was absent would be useless on most machines.
package detect

import (
	"context"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// Facts are what one detector learned about the machine. Every implementation
// is a plain struct that round-trips through JSON, because the facts are
// stored in the scan cache and re-classified on load: the catalog lives in the
// binary and must be free to change, but re-running `brew list` on every cache
// read would not be.
type Facts interface {
	// Kind names the detector the facts belong to; it is the discriminator
	// in the cache section.
	Kind() string
}

// Detector is one tool, runtime or ecosystem storix knows about.
//
// The two halves are deliberately separate. Probe talks to the machine and
// runs while the walk is running, so its cost is hidden. Classify talks only
// to the finished tree and the facts, so it is pure, fast, and re-runnable
// from a cache without touching the disk again.
type Detector interface {
	// Name is the stable identifier: the key in the cache section, the
	// value in "detector:<name>" provenance, and what --disable-detector
	// takes.
	Name() string
	// Probe interrogates the machine. Returning [ErrMissing] means the
	// tool is not installed, which is an answer; returning [ErrDegraded]
	// with usable facts means some of the evidence is missing. Any other
	// error is treated as a degradation too, never as a scan failure.
	Probe(ctx context.Context, env Env) (Facts, error)
	// Classify turns the facts and the tree into claims and a summary. It
	// must tolerate a nil or partial Facts by falling back to the static
	// paths the catalog names, so that a degraded probe still buckets the
	// bytes correctly.
	Classify(t *walk.Tree, f Facts, cx classify.Context) ([]classify.Claim, Summary)
	// NewFacts returns an empty value of the detector's fact type, for
	// decoding the cached section into.
	NewFacts() Facts
}

// LeafRetainer is implemented by a detector that needs small files the walker
// would otherwise fold into their parent — the project manifests the projects
// detector reads, for instance. The returned hook runs on every worker
// concurrently and must not allocate; a nil return means the detector wants
// nothing extra on this machine.
//
// This takes the context the plan's sketch left out: the hook depends on the
// code roots, which come from the machine rather than from the detector.
type LeafRetainer interface {
	RetainLeaf(cx classify.Context) func(dir string, e *walk.Entry) bool
}

// Unverified is implemented by a detector written from documentation rather
// than from a machine that had the tool. It marks its Status so a reader
// knows the evidence has never been seen working, which is honest about
// Docker Desktop and the Xcode simulator probes.
type Unverified interface {
	Unverified() bool
}

// Env is what a detector is allowed to touch. Everything a detector does to
// the outside world goes through one of these fields, which is what makes a
// detector testable: a test supplies a [probe.Replay], a fixture home and a
// map-backed reader, and the detector cannot tell the difference.
type Env struct {
	// Runner runs the detector's commands.
	Runner probe.Runner
	// Home is the invoking user's home directory as an absolute path,
	// which under sudo is the user's and not root's.
	Home string
	// Euid is the effective uid the scan is running as.
	Euid int
	// LookPath reports where a tool is, or an error when it is absent.
	LookPath func(string) (string, error)
	// ReadFile is the only sanctioned way for a detector to read a file:
	// it refuses dataless files, refuses symlinks and caps the size. See
	// readfile.go.
	ReadFile func(string) ([]byte, error)
	// Stat is how a detector asks whether a path exists without reading
	// it. It never follows a final symlink.
	Stat func(string) (FileInfo, error)
}

// Tool is one directory a detector recognised: a cache, a toolchain, a set of
// installed versions, or a tool's own data.
type Tool struct {
	Name string `json:"name"`
	// Kind is "cache", "toolchain", "versions", "data" or "image".
	Kind string `json:"kind"`
	// Path is the display path.
	Path string `json:"path"`
	// Node is the tree node id, -1 when the walk never saw the path.
	Node    int32            `json:"node"`
	Bytes   int64            `json:"bytes"`
	Reclaim classify.Reclaim `json:"reclaim"`
	// Current marks the version in use among several installed.
	Current bool   `json:"current,omitempty"`
	Version string `json:"version,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Project is one source project under a code root, with what its build
// artifacts cost. It is filled in by the projects detector of M11.
type Project struct {
	Root          string    `json:"root"`
	Node          int32     `json:"node"`
	ArtifactBytes int64     `json:"artifact_bytes"`
	Artifacts     []Tool    `json:"artifacts,omitempty"`
	LastActivity  time.Time `json:"last_activity,omitempty"`
	VCS           bool      `json:"vcs,omitempty"`
}

// Line is one row of what a container runtime's daemon says it is using.
// The numbers are the guest's, not the host's: a daemon reporting 20 GB of
// images inside a disk image the host has given 18.8 GB of blocks is normal,
// and showing both is the whole point of the containers view.
type Line struct {
	// Type is the daemon's own name for the row: "Images", "Containers",
	// "Local Volumes", "Build Cache".
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	Reclaimable int64  `json:"reclaimable"`
	// Percent is the share the daemon calls reclaimable, when it prints
	// one; Known is false when it does not.
	Percent int  `json:"percent,omitempty"`
	Known   bool `json:"known,omitempty"`
	// Count is how many objects of this type exist, Active how many are in
	// use.
	Count  int `json:"count"`
	Active int `json:"active"`
	// Reclaim is how safely the row's reclaimable share could be freed:
	// images, stopped containers and build cache are tool-managed, named
	// volumes are user data because they hold databases.
	Reclaim classify.Reclaim `json:"reclaim"`
}

// Runtime is one container runtime or virtual machine manager, reported as
// the two numbers docs/04 asks for: what the host has given it, and what it
// believes it is using.
type Runtime struct {
	Name string `json:"name"`
	// HostImage are the disk images and swap files as the host sees them,
	// in allocated bytes.
	HostImage []Tool `json:"host_image,omitempty"`
	// GuestReported is the daemon's own accounting.
	GuestReported []Line `json:"guest_reported,omitempty"`
	// Machines are the named guests, empty when there are none.
	Machines []string `json:"machines,omitempty"`
	// Context is the docker context the numbers came from.
	Context string `json:"context,omitempty"`
	// Note explains the gap between the two columns.
	Note string `json:"note,omitempty"`
}

// HostBytes is the total the host has allocated to the runtime.
func (r Runtime) HostBytes() int64 {
	var n int64
	for _, t := range r.HostImage {
		n += t.Bytes
	}
	return n
}

// GuestBytes is the total the daemon believes it is using.
func (r Runtime) GuestBytes() int64 {
	var n int64
	for _, l := range r.GuestReported {
		n += l.Size
	}
	return n
}

// GuestReclaimable is what the daemon says it could free.
func (r Runtime) GuestReclaimable() int64 {
	var n int64
	for _, l := range r.GuestReported {
		n += l.Reclaimable
	}
	return n
}

// Summary is what a detector wants rendered, as typed rows. It computes no
// totals beyond node sizes: the arithmetic of the ledger belongs to
// internal/ledger, and a detector that added its own would let two parts of
// the report disagree about a number.
type Summary struct {
	Tools    []Tool    `json:"tools,omitempty"`
	Projects []Project `json:"projects,omitempty"`
	Runtimes []Runtime `json:"runtimes,omitempty"`
}

// Empty reports whether the summary has nothing to render.
func (s Summary) Empty() bool {
	return len(s.Tools) == 0 && len(s.Projects) == 0 && len(s.Runtimes) == 0
}
