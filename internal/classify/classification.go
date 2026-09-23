package classify

import (
	"sort"

	"github.com/asamgx/storix/internal/walk"
)

// BucketTotal is one bucket's share of the scanned bytes.
type BucketTotal struct {
	// Bytes is the sum of the own bytes of every node in the bucket: a
	// directory's total less its children's, which on APFS is exactly the
	// leaves too small to have a node of their own. Summing own bytes
	// rather than subtree totals is what makes the buckets a partition.
	Bytes int64
	// Files counts the same way: a directory contributes the leaves folded
	// into it (n.Files less its children's, which is Small.Files) and a
	// retained leaf contributes one. The bucket file counts therefore
	// partition the walk's own file count exactly as the bytes partition
	// the scanned bytes.
	Files int64
	// ByReclaim splits the bytes by reclaimability tag, indexed by Reclaim.
	ByReclaim [numReclaim]int64
	// Categories is the bucket's own sub-split, by category label.
	Categories map[string]int64
}

// Reclaimable is the bytes in the bucket that could be freed.
func (b BucketTotal) Reclaimable() int64 {
	var n int64
	for r := Reclaim(0); int(r) < numReclaim; r++ {
		if r.Reclaimable() {
			n += b.ByReclaim[r]
		}
	}
	return n
}

// OwnerTotal is one owner's bytes across the buckets. An application's data
// is spread over Applications, App data and often Developer, and the point of
// the owner key is that those three add up to one number.
type OwnerTotal struct {
	Bytes int64
	// ByBucket is the split, indexed by Bucket.
	ByBucket [numBuckets]int64
	// Keys are the join keys seen for this owner, deduplicated.
	Keys []string
}

// Classification is the finished assignment of every node to a claim.
type Classification struct {
	// Claims are the explicit claims that won their node, in node order.
	Claims []Claim
	// Effective maps a node id to its claim, -1 for a node no rule and no
	// detector reached, which the ledger counts as Other.
	Effective []int32
	// Buckets are the totals, indexed by Bucket; index 0 is unused.
	Buckets [numBuckets]BucketTotal
	// Owners are the per-owner totals, keyed by the owner's display label.
	Owners map[string]*OwnerTotal
	// Conflicts are the claims that lost their node, largest first, and
	// truncated to maxConflicts. Every loser is collected before the sort,
	// so the list really is the biggest disagreements rather than the
	// biggest among whichever were seen first.
	Conflicts []Conflict
	// Unmatched are the roots of the largest wholly unclassified subtrees,
	// which is the list that drives the next round of catalog rules.
	Unmatched []int32
	// Rejected counts the detector and apps claims this run refused: one
	// naming a path the walk never retained, or carrying a bucket that is
	// not one of the twelve. It is zero on a healthy run and a number a
	// test can assert on, rather than bytes quietly going missing.
	Rejected int

	roots [numBuckets][]int32
	byKey map[string][]int32
	// parent is the parent node id per node, -1 for the root.
	parent []int32
	// claimNode is the node id each claim was made about.
	claimNode []int32
	// unmatchedBytes parallels Unmatched, kept for the debug listing.
	unmatchedBytes []int64
}

// Parent is the node id of a node's parent, -1 for the scan root.
func (c *Classification) Parent(nodeID int32) int32 {
	if c == nil || nodeID < 0 || int(nodeID) >= len(c.parent) {
		return -1
	}
	return c.parent[nodeID]
}

// resolve picks the winner among a node's candidates, records the losers and
// appends the winner to Claims. It returns the winner's claim index, or -1
// when the node had no candidate at all.
func (c *Classification) resolve(id int32, n *walk.Node, cands []Claim) int32 {
	if len(cands) == 0 {
		return -1
	}
	win := 0
	for i := 1; i < len(cands); i++ {
		if better(&cands[i], &cands[win]) {
			win = i
		}
	}
	// Every loser is recorded, and sortConflicts trims the list once the
	// pass is over. Collecting a prefix and trimming that would make
	// "largest first" a claim about whatever the preorder reached first,
	// which is the order the walk happened to take. Measured on this
	// machine over a 338 k-node home: 1006 conflicts collected, 20 ms for
	// the whole pass. A catalog bug that made several rules tie on every
	// node is the only way the list grows with the tree, and there the
	// biggest disagreements are exactly what a reader needs to see.
	if len(cands) > 1 {
		for i := range cands {
			if i == win {
				continue
			}
			c.Conflicts = append(c.Conflicts, Conflict{
				Node:   id,
				Path:   n.Display(),
				Winner: cands[win].Source,
				Loser:  cands[i].Source,
				Bytes:  n.Bytes,
			})
		}
	}
	c.Claims = append(c.Claims, cands[win])
	c.claimNode = append(c.claimNode, id)
	idx := int32(len(c.Claims) - 1)
	for _, k := range cands[win].OwnerKeys {
		c.byKey[k] = append(c.byKey[k], id)
	}
	return idx
}

// noteRoot records a node whose bucket differs from its parent's, which is
// the drill-down list the Ledger view opens.
func (c *Classification) noteRoot(id, eff, parentEff int32) {
	b, pb := c.bucketAt(eff), c.bucketAt(parentEff)
	if b == pb {
		return
	}
	c.roots[b] = append(c.roots[b], id)
}

// bucketAt is the bucket of a claim index, with Other for no claim.
func (c *Classification) bucketAt(eff int32) Bucket {
	if eff < 0 || int(eff) >= len(c.Claims) {
		return BucketOther
	}
	return c.Claims[eff].Bucket
}

// addOwner folds one node's own bytes into its owner's totals.
func (c *Classification) addOwner(owner string, b Bucket, own int64, eff int32) {
	t := c.Owners[owner]
	if t == nil {
		t = &OwnerTotal{}
		c.Owners[owner] = t
	}
	t.Bytes += own
	t.ByBucket[b] += own
	if eff >= 0 {
		t.Keys = mergeKeys(t.Keys, c.Claims[eff].OwnerKeys)
	}
}

// mergeKeys adds keys that are not already there. The lists are a handful of
// entries long, so a linear scan beats a map per owner.
func mergeKeys(dst, add []string) []string {
	for _, k := range add {
		found := false
		for _, have := range dst {
			if have == k {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, k)
		}
	}
	return dst
}

// maxUnmatched is how many unclassified subtree roots are kept for tuning.
const maxUnmatched = 200

// findUnmatched collects the roots of the wholly unclassified subtrees: the
// top 200 by subtree bytes, which is the list the acceptance script prints the
// head of and the list the next round of catalog rules is written from.
//
// The rule, stated precisely, is this. A node is unmatched when it has no
// effective claim. Because an unclaimed node inherits its parent's claim, an
// unmatched node always has an unmatched parent, so "unmatched whose parent is
// unmatched too" describes every unmatched node and would list a directory
// together with all of its unmatched descendants. An unmatched *root* is
// therefore the topmost node of a region the catalog never touched: its whole
// subtree carries no claim, and its parent's subtree does carry one somewhere.
// That is what this computes, bottom up over the preorder index.
//
// The distinction matters on a real machine. /private carries no rule of its
// own but nearly everything under it does, so listing /private would report
// 2.7 GB as unclassified when the true figure is a few hundred kilobytes;
// listing /private/tftpboot instead names the directory a rule is missing for.
func (c *Classification) findUnmatched(t *walk.Tree) {
	// untouched[i] is true while no node of i's subtree carries a claim.
	// Preorder puts every parent before its children, so one backward pass
	// propagates a claimed child up to its parent before the parent is read.
	untouched := make([]bool, len(t.Nodes))
	for i := range untouched {
		untouched[i] = c.Effective[i] < 0
	}
	for i := len(t.Nodes) - 1; i > 0; i-- {
		if p := c.parent[i]; p >= 0 && !untouched[i] {
			untouched[p] = false
		}
	}

	type cand struct {
		id    int32
		bytes int64
	}
	var cands []cand
	for i, n := range t.Nodes {
		p := c.parent[i]
		if !untouched[i] || (p >= 0 && untouched[p]) {
			continue
		}
		cands = append(cands, cand{int32(i), n.Bytes})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].bytes != cands[j].bytes {
			return cands[i].bytes > cands[j].bytes
		}
		return cands[i].id < cands[j].id
	})
	if len(cands) > maxUnmatched {
		cands = cands[:maxUnmatched]
	}
	c.Unmatched = make([]int32, len(cands))
	c.unmatchedBytes = make([]int64, len(cands))
	for i, cd := range cands {
		c.Unmatched[i], c.unmatchedBytes[i] = cd.id, cd.bytes
	}
}

// finish sorts the by-size lists once the passes are done.
func (c *Classification) finish() {
	c.Conflicts = sortConflicts(c.Conflicts)
}

// OfNode is Of for a caller holding the node rather than its id. Every
// consumer downstream of the engine — the TUI's rows, the JSON encoder,
// explain, the application footprints — has a *walk.Node in hand, and
// Node.ID makes the lookup a single index rather than a search.
func (c *Classification) OfNode(n *walk.Node) (Claim, bool) {
	if n == nil {
		return Claim{}, false
	}
	return c.Of(n.ID)
}

// ExplicitAtNode is ExplicitAt for a caller holding the node.
func (c *Classification) ExplicitAtNode(n *walk.Node) (Claim, bool) {
	if n == nil {
		return Claim{}, false
	}
	return c.ExplicitAt(n.ID)
}

// InheritedFromNode is InheritedFrom for a caller holding the node.
func (c *Classification) InheritedFromNode(n *walk.Node) *walk.Node {
	if n == nil {
		return nil
	}
	return c.InheritedFrom(n.ID)
}

// Of returns the effective claim of a node: the one it carries itself, or the
// nearest ancestor's. The second result is false for a node in Other.
func (c *Classification) Of(nodeID int32) (Claim, bool) {
	if c == nil || nodeID < 0 || int(nodeID) >= len(c.Effective) {
		return Claim{}, false
	}
	eff := c.Effective[nodeID]
	if eff < 0 {
		return Claim{}, false
	}
	return c.Claims[eff], true
}

// ExplicitAt returns the claim made about this node itself. The second result
// is false when the node only inherits one.
func (c *Classification) ExplicitAt(nodeID int32) (Claim, bool) {
	cl, ok := c.Of(nodeID)
	if !ok {
		return Claim{}, false
	}
	if c.NodeOf(c.Effective[nodeID]) != nodeID {
		return Claim{}, false
	}
	return cl, true
}

// NodeOf is the node a claim was made about.
func (c *Classification) NodeOf(claim int32) int32 {
	if c == nil || claim < 0 || int(claim) >= len(c.claimNode) {
		return -1
	}
	return c.claimNode[claim]
}

// InheritedFrom returns the node whose claim this node inherited, or nil when
// the node carries its own claim or none.
func (c *Classification) InheritedFrom(nodeID int32) *walk.Node {
	cl, ok := c.Of(nodeID)
	if !ok || c.NodeOf(c.Effective[nodeID]) == nodeID {
		return nil
	}
	return cl.Node
}

// Roots lists the nodes where a bucket begins: the node's effective bucket is
// b and its parent's is not. It is the drill-down list for one bucket.
func (c *Classification) Roots(b Bucket) []int32 {
	if c == nil || !b.Valid() {
		return nil
	}
	out := make([]int32, len(c.roots[b]))
	copy(out, c.roots[b])
	return out
}

// ByOwnerKey lists the nodes whose winning claim carries the key. It is the
// join the application footprint view is built on.
func (c *Classification) ByOwnerKey(key string) []int32 {
	if c == nil {
		return nil
	}
	out := make([]int32, len(c.byKey[key]))
	copy(out, c.byKey[key])
	return out
}

// UnmatchedBytes is the size of the i-th unmatched subtree root.
func (c *Classification) UnmatchedBytes(i int) int64 {
	if c == nil || i < 0 || i >= len(c.unmatchedBytes) {
		return 0
	}
	return c.unmatchedBytes[i]
}

// Total is the sum of every bucket, which equals the scanned bytes.
func (c *Classification) Total() int64 {
	if c == nil {
		return 0
	}
	var n int64
	for i := range c.Buckets {
		n += c.Buckets[i].Bytes
	}
	return n
}
