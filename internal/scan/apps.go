package scan

import (
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
func appsReport(t *walk.Tree, outs []detect.Outcome, class *classify.Classification, cx classify.Context) *apps.Report {
	det, facts := appsOutcome(outs)
	if det == nil || facts == nil || class == nil {
		return nil
	}
	a := det.Analyze(t, facts, cx)
	if a == nil {
		return nil
	}
	return apps.BuildReport(a, class.Claims)
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
