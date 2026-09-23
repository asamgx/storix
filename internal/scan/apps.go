package scan

import (
	"time"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/walk"
)

// Apps returns the application inventory's report.
//
// The second result is false when the scan has none, which happens in exactly
// one case worth distinguishing: a cache file written before the inventory
// existed. A caller that wants the report then rescans and says why, rather
// than showing an empty table that looks like a machine with no applications.
//
// The accessor lives here rather than in internal/apps because that package
// cannot import this one: a scan runs the detector, so the dependency only
// goes one way.
func Apps(res *Result) (*apps.Report, bool) {
	if res == nil || res.Apps == nil || res.Apps.Empty() {
		return nil, false
	}
	return res.Apps, true
}

// appsReport builds the report from the finished classification.
//
// It runs after the engine rather than inside the detector because a
// footprint is made of the claims that *won* their node. The ide detector
// beats this package on "~/Library/Application Support/Code", and VS Code's
// footprint should carry the ide detector's bucket and category for that
// directory, not the one apps would have given it. Only the resolved
// classification knows which claim won.
//
// The analysis is re-run rather than carried over from Classify. It is pure
// and costs about sixty milliseconds on a full volume, against a walk of
// twenty seconds, and the alternative is a detector holding state between two
// calls that are meant to be independent.
//
// It is run against the scan's own clock rather than the wall clock, because
// the report is rebuilt on every cache load and an inventory that quietly
// re-dates itself each time it is read is not the inventory that was stored.
// The verdicts are full of ages — "written 3 hours ago", and a thirty-day
// window deciding whether a directory is an orphan — so reading a scan four
// hours after taking it produced a different document from the same facts, and
// a scan reread a month later could reclassify data as orphaned that the scan
// itself had found fresh. The walk's finish time is the instant the facts
// describe, it is stored in the cache file, and it is therefore the only clock
// that makes the report a function of the scan.
func appsReport(t *walk.Tree, outs []detect.Outcome, class *classify.Classification, cx classify.Context) *apps.Report {
	det, facts := appsOutcome(outs)
	if det == nil || facts == nil || class == nil {
		return nil
	}
	a := det.AnalyzeAt(t, facts, cx, scanClock(t))
	if a == nil {
		return nil
	}
	return apps.BuildReport(a, class.Claims)
}

// scanClock is the instant a scan's own facts describe: when its walk ended.
// A tree that carries no finish time — one hand-built by a test — leaves the
// choice to the analysis, which falls back to time.Now.
func scanClock(t *walk.Tree) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Finished
}

// appsOutcome finds the application inventory among the probe results.
func appsOutcome(outs []detect.Outcome) (*apps.Detector, detect.Facts) {
	for _, out := range outs {
		det, ok := out.Detector.(*apps.Detector)
		if ok && out.Facts != nil {
			return det, out.Facts
		}
	}
	return nil, nil
}
