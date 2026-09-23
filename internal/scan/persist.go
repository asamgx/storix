package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/walk"
)

// Section names of cache.Meta.Sections. The cache package stores them as
// opaque JSON so that it depends on neither internal/volume nor
// internal/ledger; the names are this package's contract with itself, read
// back by LoadLatest.
const (
	SectionFacts   = "facts"
	SectionLedger  = "ledger"
	SectionErrors  = "errors"
	SectionSkipped = "skipped"
	// SectionApps is the application inventory's report. It is stored
	// rather than recomputed because rebuilding it needs the detector's
	// probe results, and a cached scan has not probed anything: the point
	// of the cache is that `storix apps --from-cache` answers without
	// running codesign, pkgutil and lsregister again.
	SectionApps = "apps"
)

// Cacher saves finished scans to a store and is the Persist hook Run calls.
type Cacher struct {
	// Store is where scans are written.
	Store cache.Store
	// Version is the storix build recorded in the file. A cache written by
	// another build is not reused (cache.Fresh).
	Version string
}

// DefaultCacher returns a Cacher over the per-user store.
func DefaultCacher(version string) (Cacher, error) {
	s, err := cache.DefaultStore()
	if err != nil {
		return Cacher{}, err
	}
	return Cacher{Store: s, Version: version}, nil
}

// WithCache returns cfg with its Persist hook pointed at the default store,
// so a scan run from it is cached.
//
// It is a no-op when the caller has already set a hook, and when NoCache asks
// for a scan that leaves no trace. Run does not wire this itself: a scan is
// defined by its configuration, and a test that walks a fixture should not
// write to the user's cache because it called Run.
func WithCache(cfg Config) (Config, error) {
	if cfg.NoCache || cfg.Persist != nil {
		return cfg, nil
	}
	c, err := DefaultCacher(cfg.Version)
	if err != nil {
		return cfg, err
	}
	cfg.Persist = c.Persist
	return cfg, nil
}

// Persist writes a finished scan to the store and returns the file it wrote.
// Its signature is Config.Persist.
//
// An interrupted scan is saved like any other, with Incomplete set: a partial
// tree is worth keeping (it is what the TUI goes on showing), and the
// freshness rules refuse to reuse it for a fresh render.
func (c Cacher) Persist(_ context.Context, res *Result) (string, error) {
	if res == nil || res.Tree == nil {
		return "", fmt.Errorf("scan: nothing to cache")
	}
	if res.Config.NoCache {
		return "", nil
	}
	meta, err := c.meta(res)
	if err != nil {
		return "", err
	}
	return c.Store.Save(meta, res.Tree)
}

// meta assembles the JSON side of the cache file.
func (c Cacher) meta(res *Result) (cache.Meta, error) {
	roots, err := resolveRoots(res.Config.Roots)
	if err != nil {
		return cache.Meta{}, err
	}
	sections, err := sections(res)
	if err != nil {
		return cache.Meta{}, err
	}
	return cache.Meta{
		Storix:             c.Version,
		Written:            time.Now(),
		Root:               res.Tree.Root.Path(),
		Roots:              roots,
		Incomplete:         res.Tree.Incomplete,
		Sudo:               os.Geteuid() == 0,
		Parallelism:        res.Tree.Opts.Parallelism,
		SmallFileThreshold: res.Tree.Opts.SmallFileThreshold,
		Sections:           sections,
	}, nil
}

// sections encodes the parts of a Result that live outside the node arrays.
//
// The errors and skipped mounts are also carried by cache.TreeMeta, which
// restores them onto the tree; they are repeated here as sections of their
// own because they are what the ledger's unaccounted view is built from, and
// a reader of the file should not have to know which of the two places is
// authoritative. They are a few hundred entries even on a full volume.
func sections(res *Result) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, 5)
	add := func(name string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("scan: encoding the %s section: %w", name, err)
		}
		out[name] = b
		return nil
	}
	if err := add(SectionFacts, newFactsDoc(res.Facts)); err != nil {
		return nil, err
	}
	if err := add(SectionLedger, res.Ledger); err != nil {
		return nil, err
	}
	if err := add(SectionErrors, orEmptyErrors(res.Tree.Errors)); err != nil {
		return nil, err
	}
	if err := add(SectionSkipped, orEmptyMounts(res.Tree.SkippedMounts)); err != nil {
		return nil, err
	}
	if res.Apps != nil {
		if err := add(SectionApps, res.Apps); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func orEmptyErrors(in []walk.PathError) []walk.PathError {
	if in == nil {
		return []walk.PathError{}
	}
	return in
}

func orEmptyMounts(in []walk.SkippedMount) []walk.SkippedMount {
	if in == nil {
		return []walk.SkippedMount{}
	}
	return in
}
