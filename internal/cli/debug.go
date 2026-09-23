package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/asamgx/storix/internal/classify"
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
	printConflictKinds(out, res.Class.Conflicts)
	printUnmatchedDebug(out, res)
}

// conflictKindOrder is the pair order docs/03 names: same-tier disagreement
// among rules, then every tier beating a rule, then a detector beating the
// application inventory or another detector. Under the precedence rule
// (Detector > Apps > Rule) nothing else should occur; a pair outside this
// list is printed anyway, after it, rather than folded away, because that
// would be the interesting bug.
var conflictKindOrder = [][2]classify.SourceKind{
	{classify.SourceRule, classify.SourceRule},
	{classify.SourceDetector, classify.SourceRule},
	{classify.SourceApps, classify.SourceRule},
	{classify.SourceDetector, classify.SourceApps},
	{classify.SourceDetector, classify.SourceDetector},
}

// printConflictKinds prints how many claims lost to another on the same
// node, tallied by (winner kind, loser kind).
func printConflictKinds(out io.Writer, conflicts []classify.Conflict) {
	if len(conflicts) == 0 {
		_, _ = fmt.Fprintln(out, "\n  conflicts  none")
		return
	}
	counts := make(map[[2]classify.SourceKind]int, len(conflictKindOrder))
	for _, c := range conflicts {
		counts[[2]classify.SourceKind{c.Winner.Kind, c.Loser.Kind}]++
	}
	_, _ = fmt.Fprintf(out, "\n  conflicts  %d total\n", len(conflicts))

	seen := make(map[[2]classify.SourceKind]bool, len(conflictKindOrder))
	for _, key := range conflictKindOrder {
		seen[key] = true
		if n := counts[key]; n > 0 {
			_, _ = fmt.Fprintf(out, "    %s>%s  %d\n", key[0], key[1], n)
		}
	}
	var extra [][2]classify.SourceKind
	for key := range counts {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Slice(extra, func(i, j int) bool {
		if extra[i][0] != extra[j][0] {
			return extra[i][0] < extra[j][0]
		}
		return extra[i][1] < extra[j][1]
	})
	for _, key := range extra {
		_, _ = fmt.Fprintf(out, "    %s>%s  %d  (unexpected: a rule should never outrank a detector or the apps inventory)\n",
			key[0], key[1], counts[key])
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
