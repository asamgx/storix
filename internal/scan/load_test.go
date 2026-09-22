package scan

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// storeVersion is the storix version the freshness tests pretend to be.
const storeVersion = "test-1.0"

// homedStore points the default store at a temporary home, so a test never
// reads or writes the user's own scans.
func homedStore(t *testing.T) cache.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s, err := cache.DefaultStore()
	if err != nil {
		t.Fatalf("resolve the store: %v", err)
	}
	return s
}

// fixtureTree walks a small fixture and returns it with the roots it covers.
func fixtureTree(t *testing.T) (*walk.Tree, []string) {
	t.Helper()
	f := testutil.New(t)
	f.File("a/big.bin", 200_000)
	f.File("b/small.txt", 10)
	tree, err := walk.Walk(context.Background(), walk.Options{Root: f.Root, Parallelism: 2})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	roots, err := resolveRoots([]string{f.Root})
	if err != nil {
		t.Fatalf("resolve roots: %v", err)
	}
	return tree, roots
}

// storedMeta is the metadata of a saved scan, complete and current unless a
// case changes it.
func storedMeta(t *testing.T, tree *walk.Tree, roots []string) cache.Meta {
	t.Helper()
	res := &Result{
		Config: Config{Roots: roots, Units: units.Decimal, Version: storeVersion},
		Tree:   tree,
		Ledger: ledger.Build(nil, tree, units.Decimal),
	}
	secs, err := sections(res)
	if err != nil {
		t.Fatalf("encode the sections: %v", err)
	}
	return cache.Meta{
		Storix:   storeVersion,
		Written:  time.Now(),
		Root:     tree.Root.Path(),
		Roots:    roots,
		Sections: secs,
	}
}

func TestLoadLatestReturnsTheStoredScan(t *testing.T) {
	store := homedStore(t)
	tree, roots := fixtureTree(t)
	path, err := store.Save(storedMeta(t, tree, roots), tree)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	cfg := Config{Roots: roots, Units: units.Binary, Version: storeVersion}
	res, ok, err := LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v, want a scan", ok, err)
	}
	switch {
	case !res.FromCache:
		t.Error("the loaded scan does not say it came from the cache")
	case res.CachePath != path:
		t.Errorf("CachePath = %s, want %s", res.CachePath, path)
	case res.CacheAge < 0 || res.CacheAge > time.Minute:
		t.Errorf("CacheAge = %s, want a few milliseconds", res.CacheAge)
	case res.Tree.Root.Bytes != tree.Root.Bytes:
		t.Errorf("the loaded tree holds %d bytes, want %d", res.Tree.Root.Bytes, tree.Root.Bytes)
	case res.Ledger == nil || res.Ledger.Scanned.Bytes != tree.Root.Bytes:
		t.Errorf("the loaded ledger does not carry the scanned bytes")
	case res.Ledger.Units != units.Binary:
		t.Error("the ledger kept the stored units rather than this run's")
	}
}

func TestLoadLatestAppliesTheFreshnessRules(t *testing.T) {
	cases := []struct {
		name   string
		meta   func(cache.Meta) cache.Meta
		cfg    func(Config) Config
		reason string
	}{
		{
			name:   "too old",
			meta:   func(m cache.Meta) cache.Meta { m.Written = time.Now().Add(-2 * time.Hour); return m },
			reason: "old",
		},
		{
			name:   "another version",
			meta:   func(m cache.Meta) cache.Meta { m.Storix = "test-0.9"; return m },
			reason: "written by storix test-0.9",
		},
		{
			name:   "another root",
			cfg:    func(c Config) Config { c.Roots = []string{t.TempDir()}; return c },
			reason: "the cache covers",
		},
		{
			name:   "interrupted",
			meta:   func(m cache.Meta) cache.Meta { m.Incomplete = true; return m },
			reason: "interrupted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := homedStore(t)
			tree, roots := fixtureTree(t)
			meta := storedMeta(t, tree, roots)
			if tc.meta != nil {
				meta = tc.meta(meta)
			}
			if _, err := store.Save(meta, tree); err != nil {
				t.Fatalf("save: %v", err)
			}
			cfg := Config{Roots: roots, Units: units.Decimal, Version: storeVersion}
			if tc.cfg != nil {
				cfg = tc.cfg(cfg)
			}

			res, ok, err := LoadLatest(cfg)
			if ok || res != nil {
				t.Fatalf("LoadLatest reused a cache it should have refused")
			}
			var stale *StaleError
			if !errors.As(err, &stale) {
				t.Fatalf("error = %v, want a StaleError", err)
			}
			if !strings.Contains(stale.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to mention %q", stale.Reason, tc.reason)
			}

			// --from-cache overrides every rule but an empty store.
			cfg.FromCache = true
			if _, ok, err := LoadLatest(cfg); !ok || err != nil {
				t.Errorf("with --from-cache: %v, %v, want the stored scan anyway", ok, err)
			}
		})
	}
}

func TestLoadLatestOnAnEmptyStore(t *testing.T) {
	homedStore(t)
	_, roots := fixtureTree(t)
	cfg := Config{Roots: roots, Version: storeVersion}

	res, ok, err := LoadLatest(cfg)
	if ok || res != nil || err != nil {
		t.Fatalf("LoadLatest = %v, %v, %v; an empty store is not an error", res, ok, err)
	}

	cfg.FromCache = true
	if _, ok, err := LoadLatest(cfg); ok || !errors.Is(err, ErrNoCache) {
		t.Fatalf("with --from-cache: %v, %v, want ErrNoCache", ok, err)
	}
}

func TestWithCacheHonoursNoCache(t *testing.T) {
	homedStore(t)
	if cfg, err := WithCache(Config{NoCache: true}); err != nil || cfg.Persist != nil {
		t.Errorf("WithCache wired a persist hook for a --no-cache scan (err %v)", err)
	}
	cfg, err := WithCache(Config{Version: storeVersion})
	if err != nil || cfg.Persist == nil {
		t.Fatalf("WithCache = %v, want a persist hook", err)
	}
}

func TestRunSkipsThePersistHookWithNoCache(t *testing.T) {
	homedStore(t)
	f := testutil.New(t)
	f.File("a/big.bin", 100_000)
	called := false
	cfg := Config{
		Roots:   []string{f.Root},
		NoCache: true,
		Persist: func(context.Context, *Result) (string, error) { called = true; return "somewhere", nil },
	}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called || res.CachePath != "" {
		t.Errorf("--no-cache still wrote a cache (called=%v path=%q)", called, res.CachePath)
	}
}

func TestPersistSavesAnInterruptedScan(t *testing.T) {
	store := homedStore(t)
	f := testutil.New(t)
	f.File("a/big.bin", 100_000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg, err := WithCache(Config{Roots: []string{f.Root}, Version: storeVersion})
	if err != nil {
		t.Fatalf("WithCache: %v", err)
	}
	res, err := Run(ctx, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PersistErr != nil {
		t.Fatalf("the interrupted scan was not cached: %v", res.PersistErr)
	}
	if !res.Tree.Incomplete {
		t.Skip("the walk finished before the cancellation reached it")
	}
	_, meta, err := store.Latest()
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if !meta.Incomplete {
		t.Error("the stored scan does not say it was interrupted")
	}
	if _, ok, err := LoadLatest(Config{Roots: []string{f.Root}, Version: storeVersion}); ok || err == nil {
		t.Error("an interrupted scan must not be reused as a fresh one")
	}
}

// TestSectionsCarryEverythingTheReportNeeds guards the section names, which
// are the contract between Persist and LoadLatest.
func TestSectionsCarryEverythingTheReportNeeds(t *testing.T) {
	tree, roots := fixtureTree(t)
	res := &Result{
		Config: Config{Roots: roots},
		Tree:   tree,
		Ledger: ledger.Build(nil, tree, units.Decimal),
	}
	secs, err := sections(res)
	if err != nil {
		t.Fatalf("sections: %v", err)
	}
	for _, name := range []string{SectionFacts, SectionLedger, SectionErrors, SectionSkipped} {
		if len(secs[name]) == 0 {
			t.Errorf("the %s section is missing", name)
		}
	}
}
