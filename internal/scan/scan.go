// Package scan runs a whole scan: the volume facts around a walk, the walk
// itself, and the ledger that reconciles the two.
//
// It exists so that the CLI and the TUI share one definition of what a scan
// is. Nothing here prints; the result is data and internal/report renders it.
package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/volume"
	"github.com/asamgx/storix/internal/walk"
)

// Config describes one scan.
type Config struct {
	// Roots are the directories to scan, as the user typed them; they are
	// translated to data-volume paths. Empty selects mac.DataRoot.
	//
	// Run scans one root, the first. The field is a slice because the set a
	// scan covered travels with it into the cache and the report, and
	// because later phases add opt-in volumes; a caller with several roots
	// calls Run once per root.
	Roots []string
	// Parallelism is the walker's worker count; zero selects its default.
	Parallelism int
	// SmallFileThreshold is the size below which a leaf is folded into its
	// parent; zero selects the walker's default.
	SmallFileThreshold int64
	// NoCache asks for a scan that leaves no trace: WithCache wires no
	// Persist hook, and Run skips the one it was given.
	NoCache bool
	// FromCache asks LoadLatest for the stored scan whatever its age. It
	// changes nothing about Run.
	FromCache bool
	// Version is the storix build. It is recorded in the cache file and
	// compared against it: a scan written by another build is not reused.
	Version string
	// System records that the user asked for root-only directories. In
	// phase 1a it changes no path: those directories are attempted either
	// way and land in the unreadable list when they are denied.
	System bool
	// CodeRoots are the directories holding the user's projects, as display
	// paths. Nil selects classify.DefaultCodeRoots; whichever list is used,
	// only the roots that exist in the walked tree are passed to the
	// classifier, so a machine without ~/Projects gets no rules for it.
	CodeRoots []string
	// Units selects decimal or binary formatting for the text the ledger
	// builds into its verdict.
	Units units.Format
	// Debug asks the caller for extra output; Run only records timings,
	// which it does anyway.
	Debug bool
	// Events optionally receives the walk's progress. The channel must be
	// drained until the walk finishes, because the closing DoneEvent is
	// sent blocking. Run neither creates nor closes it.
	Events chan<- walk.Event
	// Persist optionally stores the finished result, returning where it
	// went. internal/cache supplies it; a failure is recorded in the result
	// and never sinks the scan.
	Persist func(context.Context, *Result) (path string, err error)
}

// Timing is how long each stage took.
//
// There is no separate finalize figure: the walker's finalize pass runs
// inside walk.Walk and is not separable from it without instrumenting the
// walker for a number nobody acts on.
type Timing struct {
	Facts    time.Duration `json:"facts_ns"`
	Walk     time.Duration `json:"walk_ns"`
	Finish   time.Duration `json:"finish_ns"`
	Classify time.Duration `json:"classify_ns"`
	Ledger   time.Duration `json:"ledger_ns"`
	Persist  time.Duration `json:"persist_ns"`
	Total    time.Duration `json:"total_ns"`
}

// Result is a finished scan.
type Result struct {
	Config Config
	Facts  *volume.Facts
	Tree   *walk.Tree
	Ledger *ledger.Ledger

	// Class is the classification of the tree: which bucket every node's
	// bytes belong to and why. It is recomputed on every scan and on every
	// cache load rather than stored, because the catalog lives in the
	// binary and the answer must follow the binary, not the cache file.
	Class *classify.Classification

	// CachePath is where Persist stored the result, empty when it did not
	// run. FromCache and CacheAge are set by the loader, not by Run.
	CachePath string
	FromCache bool
	CacheAge  time.Duration

	Timing Timing

	// DatalessErr, FinishErr and PersistErr record the best-effort steps
	// that must never sink a scan: turning off dataless materialization
	// (unavailable in a build without cgo), the closing space reading, and
	// the cache write. A caller reports them; the result stands without
	// them.
	DatalessErr error
	FinishErr   error
	PersistErr  error
}

// Root returns the scanned root as a user sees it.
func (r *Result) Root() string {
	if r == nil || r.Tree == nil {
		return ""
	}
	return mac.DisplayPath(r.Tree.Root.Path())
}

// Run performs a scan: collect the facts, walk, take the closing reading,
// build the ledger, and persist if asked.
//
// It returns an error only for a configuration problem, such as a root that
// does not exist. Everything that can go wrong while reading the machine is
// data: unreadable directories live in the tree, unavailable facts become
// ledger lines marked unknown, and a cancelled walk returns the partial tree
// with Incomplete set, still reconciled and still persisted.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	start := time.Now()
	roots, err := resolveRoots(cfg.Roots)
	if err != nil {
		return nil, err
	}
	root := roots[0]

	res := &Result{Config: cfg}

	// The policy goes off before anything lists a directory: listing a
	// File Provider directory is itself a materialization trigger, and the
	// Full Disk Access probe inside Collect is a directory listing.
	res.DatalessErr = mac.SetDatalessMaterializationOff()

	t0 := time.Now()
	facts, err := volume.Collect(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("collecting volume facts: %w", err)
	}
	res.Facts = facts
	res.Timing.Facts = time.Since(t0)

	t0 = time.Now()
	tree, err := walk.Walk(ctx, walkOptions(cfg, root, facts))
	res.Timing.Walk = time.Since(t0)
	if err != nil {
		return nil, err
	}
	res.Tree = tree

	t0 = time.Now()
	res.FinishErr = facts.Finish()
	res.Timing.Finish = time.Since(t0)

	t0 = time.Now()
	res.Class = Classify(tree, cfg)
	res.Timing.Classify = time.Since(t0)

	t0 = time.Now()
	res.Ledger = ledger.BuildClassified(facts, tree, cfg.Units, res.Class)
	res.Timing.Ledger = time.Since(t0)

	if cfg.Persist != nil && !cfg.NoCache {
		t0 = time.Now()
		path, err := cfg.Persist(ctx, res)
		res.Timing.Persist = time.Since(t0)
		res.CachePath, res.PersistErr = path, err
	}

	res.Timing.Total = time.Since(start)
	return res, nil
}

// walkOptions turns the scan config into walker options.
//
// The mount guard is always on: a mount point inside any root would be
// counted twice without it, and the table is already in hand. The default
// exemption prefixes, on the other hand, are written relative to the data
// volume, so they mean nothing under a partial root and are switched off
// there rather than silently matching nothing.
func walkOptions(cfg Config, root string, f *volume.Facts) walk.Options {
	opts := walk.Options{
		Root:               root,
		Parallelism:        cfg.Parallelism,
		SmallFileThreshold: cfg.SmallFileThreshold,
		Events:             cfg.Events,
	}
	if f.Mounts != nil {
		opts.Mounts = mountGuard{f.Mounts}
	}
	if !isDataVolume(root, f) {
		opts.ExemptPrefixes = []string{}
	}
	return opts
}

// mountGuard adapts the mount table to the walker's two mount interfaces.
// The table can already name a mount, but under a different method name, and
// a skipped-mount line that cannot say "nfs, OrbStack:/OrbStack" leaves the
// reader guessing which volume the missing bytes went to.
type mountGuard struct{ *volume.MountTable }

// MountInfo implements walk.MountDescriber.
func (g mountGuard) MountInfo(path string) (fsType, from string, ok bool) {
	v, ok := g.Describe(path)
	if !ok {
		return "", "", false
	}
	return v.FSType, v.Device, true
}

// isDataVolume reports whether root is the data volume itself rather than a
// directory on it.
func isDataVolume(root string, f *volume.Facts) bool {
	if root == mac.DataRoot {
		return true
	}
	if f.Mounts == nil {
		return false
	}
	v, ok := f.Mounts.Owning(root)
	return ok && (v.MountPoint == root || v.DataPath == root)
}

// resolveRoots translates every root to a data-volume path and checks that it
// is a directory that exists. Every root is checked, not just the one that
// gets walked, so a typo in the second one is reported before the first costs
// twenty seconds.
func resolveRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		return []string{mac.DataRoot}, nil
	}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" {
			return nil, errors.New("empty scan root")
		}
		p := mac.ScanPath(r)
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = mac.ScanPath(resolved)
		}
		fi, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("scan root %s: %w", r, err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("scan root %s is not a directory", r)
		}
		out = append(out, p)
	}
	return out, nil
}
