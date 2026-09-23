package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
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
	// SectionDetectors is what each detector learned, in the order the
	// registry ran them: its status and its own facts as JSON. It is the
	// input to the reclassification on load, which is what lets the
	// catalog live in the binary rather than in the cache file.
	SectionDetectors = "detectors"
	// SectionClassification is a summary of what the engine concluded:
	// bucket totals, the largest owners, the conflict counts and the
	// unmatched roots. Nothing reads it back — the classification is
	// recomputed from the facts above — and it is written so that a cache
	// file describes its own scan to anyone who opens it.
	SectionClassification = "classification"
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
	docs, err := detectorDocs(res)
	if err != nil {
		return nil, err
	}
	if docs != nil {
		if err := add(SectionDetectors, docs); err != nil {
			return nil, err
		}
	}
	if res.Class != nil {
		if err := add(SectionClassification, newClassificationDoc(res)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// detectorDoc is one detector's cached row: what happened to it and what it
// learned, the facts kept as the detector's own JSON.
//
// The state travels as its name rather than as its number, so that inserting
// a state in the middle of the list cannot silently turn a missing tool into
// a panic. The probe commands are deliberately not stored: one of them is an
// lsregister dump measured in megabytes, and the evidence a reader wants is
// on the claims, which are recomputed.
type detectorDoc struct {
	Name     string          `json:"name"`
	State    string          `json:"state"`
	Reason   string          `json:"reason,omitempty"`
	Duration time.Duration   `json:"duration_ns"`
	Verified bool            `json:"verified"`
	Facts    json.RawMessage `json:"facts,omitempty"`
}

// detectorDocs encodes the detectors section, or nil when the scan ran none
// at all. A file with no section is a pre-1b file to the loader, and a scan
// that genuinely had no detectors classifies from the catalog either way.
func detectorDocs(res *Result) ([]detectorDoc, error) {
	if len(res.Detectors) == 0 {
		return nil, nil
	}
	facts := make(map[string]detect.Facts, len(res.probes))
	for _, out := range res.probes {
		if out.Facts != nil {
			facts[out.Status.Name] = out.Facts
		}
	}
	docs := make([]detectorDoc, 0, len(res.Detectors))
	for _, st := range res.Detectors {
		doc := detectorDoc{
			Name:     st.Name,
			State:    st.State.String(),
			Reason:   st.Reason,
			Duration: st.Duration,
			Verified: st.Verified,
		}
		if f := facts[st.Name]; f != nil {
			b, err := json.Marshal(f)
			if err != nil {
				return nil, fmt.Errorf("scan: encoding the %s facts: %w", st.Name, err)
			}
			doc.Facts = b
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// ConflictCount is how often two sources disagreed in the same way, and over
// how many bytes. It is the summary `--debug` prints and the acceptance
// script reads: the individual conflicts are noise, the shape of them is the
// signal that the catalog and a detector are fighting over a directory.
type ConflictCount struct {
	// Kind is "<winner>-over-<loser>", by source kind: "detector-over-rule".
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
}

// ConflictKind names a disagreement by the two source kinds involved.
func ConflictKind(c classify.Conflict) string {
	return c.Winner.Kind.String() + "-over-" + c.Loser.Kind.String()
}

// ConflictCounts summarises a classification's conflicts by kind, most
// frequent first and ties broken by name so two runs agree.
func ConflictCounts(class *classify.Classification) []ConflictCount {
	if class == nil {
		return nil
	}
	index := make(map[string]int, 8)
	var out []ConflictCount
	for _, c := range class.Conflicts {
		kind := ConflictKind(c)
		i, ok := index[kind]
		if !ok {
			i = len(out)
			index[kind] = i
			out = append(out, ConflictCount{Kind: kind})
		}
		out[i].Count++
		out[i].Bytes += c.Bytes
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// UnmatchedRow is one subtree the catalog never reached.
type UnmatchedRow struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Node  int32  `json:"node"`
}

// Unmatched lists the largest unclassified subtree roots, at most n of them.
// It is the list a new catalog rule is written from, printed by `--debug` and
// carried in the JSON report.
func Unmatched(res *Result, n int) []UnmatchedRow {
	if res == nil || res.Class == nil || res.Tree == nil || n <= 0 {
		return nil
	}
	ids := res.Class.Unmatched
	if len(ids) > n {
		ids = ids[:n]
	}
	out := make([]UnmatchedRow, 0, len(ids))
	for i, id := range ids {
		if int(id) >= len(res.Tree.Nodes) {
			continue
		}
		out = append(out, UnmatchedRow{
			Path:  res.Tree.Nodes[id].Display(),
			Bytes: res.Class.UnmatchedBytes(i),
			Node:  id,
		})
	}
	return out
}

// classificationDoc is the summary of the engine's answer that the cache
// file carries. It is written and never read: the loader recomputes the
// classification from the catalog in the binary, and this section is here so
// that the file can be inspected without one.
type classificationDoc struct {
	Buckets   []bucketDoc     `json:"buckets"`
	Owners    []OwnerRow      `json:"owners,omitempty"`
	Conflicts []ConflictCount `json:"conflicts,omitempty"`
	Unmatched []UnmatchedRow  `json:"unmatched,omitempty"`
	Timing    time.Duration   `json:"timing_ns"`
}

// bucketDoc is one bucket's totals with its reclaim split.
type bucketDoc struct {
	ID          string           `json:"id"`
	Label       string           `json:"label"`
	Bytes       int64            `json:"bytes"`
	Files       int64            `json:"files"`
	Reclaimable int64            `json:"reclaimable"`
	ByReclaim   map[string]int64 `json:"by_reclaim,omitempty"`
}

// OwnerRow is one owner's bytes across the buckets. An application's data is
// spread over Applications, App data and often Developer, and the owner row
// is where those add up to the one number a person asks for.
type OwnerRow struct {
	Label    string           `json:"label"`
	Keys     []string         `json:"keys,omitempty"`
	Bytes    int64            `json:"bytes"`
	ByBucket map[string]int64 `json:"by_bucket,omitempty"`
}

// maxCachedOwners and maxCachedUnmatched bound the summary. The cache file is
// not a report: fifty owners name every application on this machine, and
// twenty unmatched roots are as many as anyone reads.
const (
	maxCachedOwners    = 50
	maxCachedUnmatched = 20
)

// newClassificationDoc summarises the classification for the cache file.
func newClassificationDoc(res *Result) classificationDoc {
	class := res.Class
	doc := classificationDoc{
		Buckets:   bucketDocs(class),
		Owners:    Owners(class, maxCachedOwners),
		Conflicts: ConflictCounts(class),
		Unmatched: Unmatched(res, maxCachedUnmatched),
		Timing:    res.Timing.Classify,
	}
	return doc
}

// bucketDocs is the twelve bucket totals, in docs/02 order.
func bucketDocs(class *classify.Classification) []bucketDoc {
	out := make([]bucketDoc, 0, len(classify.Buckets()))
	for _, b := range classify.Buckets() {
		t := class.Buckets[b]
		out = append(out, bucketDoc{
			ID:          b.ID(),
			Label:       b.Label(),
			Bytes:       t.Bytes,
			Files:       t.Files,
			Reclaimable: t.Reclaimable(),
			ByReclaim:   reclaimMap(t),
		})
	}
	return out
}

// reclaimMap is a bucket's non-zero reclaim tags.
func reclaimMap(t classify.BucketTotal) map[string]int64 {
	var out map[string]int64
	for r := classify.Reclaim(0); int(r) < len(t.ByReclaim); r++ {
		if t.ByReclaim[r] == 0 {
			continue
		}
		if out == nil {
			out = make(map[string]int64, 4)
		}
		out[r.String()] = t.ByReclaim[r]
	}
	return out
}

// Owners is the largest owners of a classification, biggest first and at most
// n of them.
func Owners(class *classify.Classification, n int) []OwnerRow {
	if class == nil || n <= 0 {
		return nil
	}
	out := make([]OwnerRow, 0, len(class.Owners))
	for label, o := range class.Owners {
		if o == nil || o.Bytes == 0 {
			continue
		}
		var byBucket map[string]int64
		for _, b := range classify.Buckets() {
			if o.ByBucket[b] == 0 {
				continue
			}
			if byBucket == nil {
				byBucket = make(map[string]int64, 4)
			}
			byBucket[b.ID()] = o.ByBucket[b]
		}
		out = append(out, OwnerRow{
			Label: label, Keys: o.Keys, Bytes: o.Bytes, ByBucket: byBucket,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
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
