package scan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/walk"
)

// ErrNoCache is returned when the store holds no scan at all.
var ErrNoCache = errors.New("scan: no stored scan")

// StaleError says why the newest stored scan was not reused. It is not a
// failure: the caller walks the disk instead, and prints the reason under the
// header when it wants to explain the wait.
type StaleError struct{ Reason string }

func (e *StaleError) Error() string { return "scan: the stored scan is stale: " + e.Reason }

// LoadLatest reads the newest stored scan for cfg's roots.
//
// It reports (nil, false, nil) when the store is empty or missing, and
// (nil, false, *StaleError) when the newest scan exists but the freshness
// rules (D25: under an hour old, same storix version, same roots, not
// interrupted) refuse it. Config.FromCache skips those rules: the user asked
// for the stored scan whatever its age, and only an empty store is an error
// then.
//
// The returned Result carries FromCache, CacheAge and CachePath, so the
// report header and the TUI can say where the numbers came from.
func LoadLatest(cfg Config) (*Result, bool, error) {
	roots, err := resolveRoots(cfg.Roots)
	if err != nil {
		return nil, false, err
	}
	store, err := cache.DefaultStore()
	if err != nil {
		return nil, false, err
	}
	path, meta, err := store.Latest()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if cfg.FromCache {
				return nil, false, fmt.Errorf("%w in %s", ErrNoCache, store.Dir)
			}
			return nil, false, nil
		}
		return nil, false, err
	}
	if !cfg.FromCache {
		if ok, reason := cache.Fresh(meta, time.Now(), cfg.Version, roots); !ok {
			return nil, false, &StaleError{Reason: reason}
		}
	}

	meta, tree, err := store.Load(path)
	if err != nil {
		return nil, false, err
	}
	res, err := resultFromCache(cfg, meta, tree, path)
	if err != nil {
		return nil, false, err
	}
	return res, true, nil
}

// resultFromCache rebuilds a Result from a decoded cache file.
func resultFromCache(cfg Config, meta cache.Meta, tree *walk.Tree, path string) (*Result, error) {
	res := &Result{
		Config:    cfg,
		Tree:      tree,
		CachePath: path,
		FromCache: true,
		CacheAge:  age(meta.Written),
	}

	var facts *factsDoc
	if err := section(meta, SectionFacts, &facts); err != nil {
		return nil, err
	}
	res.Facts = facts.facts()

	var l *ledger.Ledger
	if err := section(meta, SectionLedger, &l); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("scan: %s holds no ledger", path)
	}
	// Units is a rendering choice of this run, not of the cached one, and
	// the per-path error list and the error classes are dropped by the
	// ledger's JSON shape because they are already in the tree.
	l.Units = cfg.Units
	l.Unreadable = tree.Errors
	for i := range l.UnreadableByClass {
		l.UnreadableByClass[i].Class = errClassByName(l.UnreadableByClass[i].Name)
	}
	res.Ledger = l

	// The tree's own copies come from cache.TreeMeta; the sections are read
	// so that a file written with one and not the other still restores.
	// This happens before anything is rebuilt, because the ledger counts
	// the unreadable paths and the classification is built over the tree.
	if len(tree.Errors) == 0 {
		var errs []walk.PathError
		if err := section(meta, SectionErrors, &errs); err != nil {
			return nil, err
		}
		tree.Errors = errs
		l.Unreadable = errs
	}
	if len(tree.SkippedMounts) == 0 {
		var skipped []walk.SkippedMount
		if err := section(meta, SectionSkipped, &skipped); err != nil {
			return nil, err
		}
		tree.SkippedMounts = skipped
	}

	// A cache written before the application inventory existed simply has
	// no such section, and the field stays nil. Callers ask Apps() rather
	// than reading the field, and it tells them to rescan. The stored
	// report is the starting point; reclassify replaces it whenever the
	// file also carries the facts it was built from.
	var appsReport *apps.Report
	if err := section(meta, SectionApps, &appsReport); err != nil {
		return nil, err
	}
	res.Apps = appsReport

	if err := reclassify(cfg, res, meta); err != nil {
		return nil, err
	}
	if rebuilt := rebuildLedger(cfg, res, l); rebuilt != nil {
		res.Ledger = rebuilt
	}
	return res, nil
}

// section decodes one metadata section into v. A missing section leaves v
// untouched, which is how an older file without it still loads.
func section(meta cache.Meta, name string, v any) error {
	raw, ok := meta.Sections[name]
	if !ok || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("scan: decoding the %s section of the cache: %w", name, err)
	}
	return nil
}

// age is how long ago the cache was written, never negative: a file dated in
// the future is shown as brand new rather than as a negative duration.
func age(written time.Time) time.Duration {
	if written.IsZero() {
		return 0
	}
	d := time.Since(written)
	if d < 0 {
		return 0
	}
	return d
}

// errClasses lists every walk.ErrClass, so a class can be recovered from the
// name the ledger's JSON carries.
var errClasses = []walk.ErrClass{
	walk.ErrOther, walk.ErrPermission, walk.ErrTCC, walk.ErrProtected, walk.ErrVanished,
}

// errClassByName maps a class name back to its class; anything unknown is
// ErrOther, which is what an unnamed error already is.
func errClassByName(name string) walk.ErrClass {
	for _, c := range errClasses {
		if c.String() == name {
			return c
		}
	}
	return walk.ErrOther
}
