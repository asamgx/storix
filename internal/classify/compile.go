package classify

// The rule trie.
//
// Every pattern is anchored, so matching a tree node means advancing a small
// set of trie states by one segment. Literal segments go in a map and cost one
// lookup; the handful of glob and capture segments at each level are tried in
// turn. In practice the live state set is one or two: the literal spine of the
// path, plus a capture edge under a directory like ~/Library/Caches.

// term is a rule that ends at a trie node, with the specificity of the path
// that reached it: how many segments it took, the packed classes of those
// segments (see pattern.shape), and how much literal text they pin down.
type term struct {
	rule     int32
	depth    uint16
	literals uint16
	shape    uint64
}

// trieEdge is a non-literal transition.
type trieEdge struct {
	seg   segment
	child *trieNode
}

// trieNode is one level of the trie.
type trieNode struct {
	lits  map[string]*trieNode
	edges []trieEdge
	terms []term
}

// child returns the node reached by seg, creating it on the first insert.
func (n *trieNode) child(seg segment) *trieNode {
	if seg.kind == segLiteral {
		if n.lits == nil {
			n.lits = make(map[string]*trieNode, 4)
		}
		c, ok := n.lits[seg.text]
		if !ok {
			c = &trieNode{}
			n.lits[seg.text] = c
		}
		return c
	}
	key := seg.key()
	for i := range n.edges {
		if n.edges[i].seg.key() == key {
			return n.edges[i].child
		}
	}
	c := &trieNode{}
	n.edges = append(n.edges, trieEdge{seg: seg, child: c})
	return c
}

// insert adds one compiled pattern for rule.
func (n *trieNode) insert(p pattern, rule int32) {
	cur := n
	for _, seg := range p.segs {
		cur = cur.child(seg)
	}
	cur.terms = append(cur.terms, term{
		rule: rule, depth: p.depth(), literals: p.literals, shape: p.shape(),
	})
}

// state is one live position in the trie, with the captures bound on the way.
type state struct {
	node *trieNode
	caps *capture
}

// advance steps every state by one path segment. It returns nil rather than
// an empty slice when nothing matches, so a subtree that matches no rule
// costs no allocation at all.
func advance(states []state, name string) []state {
	var next []state
	for _, st := range states {
		if c, ok := st.node.lits[name]; ok {
			next = append(next, state{node: c, caps: st.caps})
		}
		for i := range st.node.edges {
			e := &st.node.edges[i]
			val, ok := e.seg.match(name)
			if !ok {
				continue
			}
			caps := st.caps
			if e.seg.kind == segTemplate {
				caps = &capture{name: e.seg.name, value: val, prev: st.caps}
			}
			next = append(next, state{node: e.child, caps: caps})
		}
	}
	return next
}
