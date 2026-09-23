package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/asamgx/storix/internal/scan"
)

// printClassifyDebug adds the classifier's own --debug output: conflict
// counts by kind, and the ten largest directories no rule or detector
// reached. It is a no-op when the scan carries no classification, which is
// what a nil Class means: a catalog that failed to compile still leaves a
// correct scan, just one --debug has nothing more to say about.
func printClassifyDebug(out io.Writer, res *scan.Result) {
	if res == nil || res.Class == nil {
		return
	}
	printConflictKinds(out, res)
	printUnmatchedDebug(out, res)
}

// printConflictKinds prints how many claims lost to another on the same
// node, tallied by (winner kind, loser kind) through scan.ConflictCounts so
// --debug, the JSON and the acceptance script share one definition. Under
// the precedence rule (Detector > Apps > Rule) a rule never outranks a
// detector or the apps inventory; such a pair is printed and flagged rather
// than folded away, because that would be the interesting bug.
func printConflictKinds(out io.Writer, res *scan.Result) {
	counts := scan.ConflictCounts(res.Class)
	if len(counts) == 0 {
		_, _ = fmt.Fprintln(out, "\n  conflicts  none")
		return
	}
	total := 0
	for _, c := range counts {
		total += c.Count
	}
	_, _ = fmt.Fprintf(out, "\n  conflicts  %d total\n", total)
	for _, c := range counts {
		note := ""
		if strings.HasPrefix(c.Kind, "rule-over-") && c.Kind != "rule-over-rule" {
			note = "  (unexpected: a rule should never outrank a detector or the apps inventory)"
		}
		_, _ = fmt.Fprintf(out, "    %-24s %d%s\n", c.Kind, c.Count, note)
	}
}

// unmatchedDebugLines caps the listing the same way report's own UNMATCHED
// section does, so --debug and the text report agree on what "top" means.
const unmatchedDebugLines = 10

// printUnmatchedDebug prints the largest directories no rule or detector
// reached, as display paths: the list the next round of catalog rules is
// written from.
func printUnmatchedDebug(out io.Writer, res *scan.Result) {
	ids := res.Class.Unmatched
	if len(ids) == 0 {
		return
	}
	if len(ids) > unmatchedDebugLines {
		ids = ids[:unmatchedDebugLines]
	}
	_, _ = fmt.Fprintf(out, "\n  unmatched  top %d largest directories no rule reached\n", len(ids))
	for i, id := range ids {
		if int(id) >= len(res.Tree.Nodes) {
			continue
		}
		_, _ = fmt.Fprintf(out, "    %10s  %s\n",
			res.Config.Units.Bytes(res.Class.UnmatchedBytes(i)), res.Tree.Nodes[id].Display())
	}
}
