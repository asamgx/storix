package scan

import (
	"time"

	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/ledger"
)

// A cache file stores what the machine said, not what storix concluded from
// it. The classification, the detector summaries, the application report and
// the bucket ledger are all rebuilt here from the stored facts and the
// catalog compiled into this binary.
//
// The reason is that the catalog is code. A scan taken this morning and read
// back by a build with three more rules in it should show the three new
// rules' answer, not the old one; and a node id is an index into a preorder
// walk, which the flat cache format is free to renumber. Recomputing costs a
// few hundred milliseconds against a walk of twenty seconds, and it is what
// keeps `--from-cache` honest.
//
// The context the catalog is compiled against comes from this invocation, not
// from the file. The home is the one storix is running as now, and the code
// roots are --code-roots as passed on this command line, filtered against the
// stored tree. So a scan taken under one account and read back under another
// classifies as the reader's machine, and adding --code-roots to a
// --from-cache run reclassifies the projects under it without a rescan. What
// the file supplies is the tree and the detector facts; everything derived
// from them is derived here.

// reclassify rebuilds the derived halves of a cached result: the detector
// statuses and summaries, the classification, and the application report.
//
// It never fails on a file that simply lacks a section. A scan written before
// the detectors existed has none, and comes back with every detector in state
// NotProbed and a classification from the catalog alone, which is the correct
// account of what that file knows.
func reclassify(cfg Config, res *Result, meta cache.Meta) error {
	if res.Tree == nil {
		return nil
	}
	start := time.Now()

	docs, err := detectorSection(meta)
	if err != nil {
		return err
	}
	reg := registry(cfg)
	outs := cachedOutcomes(reg, docs)

	cx := classifyContext(res.Tree, cfg)
	claims, summaries, statuses := detect.Classify(res.Tree, outs, cx)
	res.Detectors, res.Summaries = statuses, summaries
	res.Class = classifyWith(res.Tree, cfg, claims)
	res.Timing.Classify = time.Since(start)

	// The application report is rebuilt from the stored facts for the same
	// reason as the classification: a footprint is made of the claims that
	// won their node, and those claims have just changed. The stored
	// report stays as the fallback for a file whose apps facts are missing
	// — one written before the detectors section existed, or by a build
	// whose apps probe found nothing.
	if rep := appsReport(res.Tree, outs, res.Class, cx); rep != nil {
		res.Apps = rep
	}
	return nil
}

// rebuildLedger recomputes the ledger from the restored volume facts and the
// fresh classification.
//
// It returns nil when the file carries no facts, which is the one case where
// recomputation would lose information rather than add it: without the space
// readings the volume and container lines cannot be rebuilt, and the stored
// ledger is then the better answer. Everywhere else the recomputed 1a lines
// are identical to the stored ones by construction — they are a pure function
// of the same facts and the same tree — and the recomputed ledger also
// carries the twelve buckets a pre-1b file has never heard of.
func rebuildLedger(cfg Config, res *Result, stored *ledger.Ledger) *ledger.Ledger {
	if res.Facts == nil || res.Tree == nil {
		return nil
	}
	start := time.Now()
	l := ledger.BuildClassified(res.Facts, res.Tree, cfg.Units, res.Class)
	l.Unreadable = res.Tree.Errors
	carryWalkClock(l, stored)
	res.Timing.Ledger = time.Since(start)
	return l
}

// carryWalkClock copies the walk's own timings from the stored ledger.
//
// It is the one number the rebuild cannot reproduce. A live tree's start and
// finish instants carry Go's monotonic reading, so their difference is the
// elapsed time to the nanosecond; a restored tree has only the wall-clock
// instants the file stores, whose resolution is a microsecond. Recomputing
// would therefore round the walk's duration, and the duration is a
// measurement the scan took rather than something derivable from the tree.
func carryWalkClock(l, stored *ledger.Ledger) {
	if l == nil || stored == nil {
		return
	}
	l.Counters.Started = stored.Counters.Started
	l.Counters.Finished = stored.Counters.Finished
	l.Counters.Elapsed = stored.Counters.Elapsed
}

// detectorSection decodes the detectors section, nil when the file has none.
func detectorSection(meta cache.Meta) ([]detectorDoc, error) {
	var docs []detectorDoc
	if err := section(meta, SectionDetectors, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// cachedOutcomes turns the stored rows into probe outcomes for
// detect.Classify, in the order the report prints them.
//
// The stored order is the registry order of the build that wrote the file,
// with the disabled detectors after it, which is the order a live scan
// produces. Detectors this build has and the file does not are appended, so
// a new detector shows up as NotProbed rather than vanishing.
func cachedOutcomes(reg *detect.Registry, docs []detectorDoc) []detect.Outcome {
	off := make(map[string]bool, 4)
	for _, name := range reg.Disabled() {
		off[name] = true
	}
	seen := make(map[string]bool, len(docs))
	out := make([]detect.Outcome, 0, len(docs)+len(reg.Names()))
	for _, doc := range docs {
		if seen[doc.Name] {
			continue
		}
		seen[doc.Name] = true
		out = append(out, cachedOutcome(reg, doc, off[doc.Name]))
	}
	for _, det := range reg.Detectors() {
		if seen[det.Name()] {
			continue
		}
		out = append(out, detect.Outcome{Status: detect.Status{
			Name:     det.Name(),
			State:    detect.NotProbed,
			Reason:   "the stored scan was taken before this detector existed",
			Verified: detect.Verified(det),
		}})
	}
	for _, name := range reg.Disabled() {
		if seen[name] {
			continue
		}
		out = append(out, detect.Outcome{Status: detect.Status{
			Name: name, State: detect.Disabled, Verified: true,
			Reason: "switched off with --disable-detector",
		}})
	}
	return out
}

// cachedOutcome turns one stored row into an outcome.
//
// An outcome with no detector is one nothing will be classified from: the
// status is reported and the catalog's static rules answer for those paths.
// That is the right degradation in all three cases it covers — a detector
// switched off on this run, one this build does not have, and one the stored
// scan never ran.
func cachedOutcome(reg *detect.Registry, doc detectorDoc, disabled bool) detect.Outcome {
	st := detect.Status{
		Name:     doc.Name,
		Reason:   doc.Reason,
		Duration: doc.Duration,
		Verified: doc.Verified,
	}
	if disabled {
		st.State, st.Reason = detect.Disabled, "switched off with --disable-detector"
		return detect.Outcome{Status: st}
	}
	det, ok := reg.Find(doc.Name)
	if !ok {
		st.State = detect.NotProbed
		st.Reason = "this build has no detector of that name, so its stored facts were left unread"
		return detect.Outcome{Status: st}
	}

	state, ok := detect.ParseState(doc.State)
	switch {
	case !ok:
		st.State = detect.NotProbed
		st.Reason = "the stored scan recorded the unknown state " + doc.State
		return detect.Outcome{Status: st}
	case state == detect.Disabled:
		// Switched off when the scan was taken and on now: there is
		// nothing stored to classify from, and saying Disabled would
		// blame a flag this run never passed.
		st.State = detect.NotProbed
		st.Reason = "switched off when the stored scan was taken"
		return detect.Outcome{Status: st}
	}
	st.State = state

	facts, err := detect.DecodeFactsFor(det, doc.Facts)
	if err != nil {
		// The facts are unreadable but the detector is here: it still
		// knows which paths its tool uses, so it classifies from them.
		st.State, st.Reason = detect.Degraded, err.Error()
		return detect.Outcome{Detector: det, Status: st}
	}
	return detect.Outcome{Detector: det, Facts: facts, Status: st}
}
