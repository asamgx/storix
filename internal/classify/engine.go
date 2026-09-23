package classify

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/walk"
)

// DefaultCodeRoots are the directories a developer keeps projects in. They are
// display paths; a root that does not exist simply matches nothing.
var DefaultCodeRoots = []string{"~/code", "~/Developer", "~/Projects", "~/src", "~/dev", "~/work"}

// artifactDirs are build outputs that sit directly inside a project. Only the
// first level below a project is named here: an artifact nested deeper needs a
// "anywhere under" pattern, which the grammar deliberately lacks, so the
// projects detector of M11 claims those.
var artifactDirs = []string{"node_modules", "target", ".venv", "dist", "build", ".next", ".turbo"}

// Context is what the catalog needs to know about the machine being
// classified: whose home "~" means, which other homes exist, and where the
// user keeps source code.
type Context struct {
	// Home is the scan user's home as a display path, "/Users/andrewsam".
	// Under sudo it is the invoking user's, not root's.
	Home string
	// Users are other homes seen under /Users in the tree.
	Users []string
	// CodeRoots are display paths holding projects. Nil selects
	// DefaultCodeRoots.
	CodeRoots []string
}

// homes are the named anchors a "~" pattern expands to, without a leading
// slash: the scan user's home first, then every other home the tree showed.
// The "Users/*" catch-all is not one of them; see wildcardHome.
func (c Context) homes() []string {
	out := make([]string, 0, len(c.Users)+1)
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.Trim(path.Clean(p), "/")
		if p == "" || p == "." || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	if c.Home != "" {
		add(mac.DisplayPath(c.Home))
	}
	for _, u := range c.Users {
		add(mac.DisplayPath(u))
	}
	return out
}

// primaryUser is the scan user's account name, empty when no home is known.
// It is the one account whose bytes are reported unqualified, because on the
// overwhelmingly common single-account machine "Documents (andrew)" would be
// noise in every row.
func (c Context) primaryUser() string {
	if c.Home == "" {
		return ""
	}
	return path.Base(strings.Trim(path.Clean(mac.DisplayPath(c.Home)), "/"))
}

// codeRoots is CodeRoots with the default filled in and duplicates removed.
// A root named twice would otherwise compile to two rules per project, which
// match the same node with the same specificity and fill the conflict log
// with a rule disagreeing with its own copy.
func (c Context) codeRoots() []string {
	want := c.CodeRoots
	if want == nil {
		want = DefaultCodeRoots
	}
	out := make([]string, 0, len(want))
	seen := make(map[string]bool, len(want))
	for _, r := range want {
		r = strings.TrimRight(r, "/")
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// Engine matches a compiled catalog against a walked tree.
type Engine struct {
	rules []Rule
	root  *trieNode
	ctx   Context
	// fixedOwner marks the rules whose Owner is a label rather than a
	// capture, indexed the same way as rules. Only those are qualified
	// with an account name; an owner that already names an application is
	// the same owner in whichever home its bytes turned up.
	fixedOwner []bool
	// primary is the scan user's account name, the one left unqualified.
	primary string
}

// New compiles the rules against a context. It fails on a duplicate rule id,
// a pattern that does not parse, a rule whose Owner or OwnerKeys name a
// capture the pattern does not bind, and a rule with an invalid bucket: all
// four are catalog bugs, and a catalog bug should stop a build rather than
// quietly misfile a bucket's worth of bytes.
func New(rules []Rule, ctx Context) (*Engine, error) {
	seen := make(map[string]bool, len(rules))
	for i := range rules {
		r := &rules[i]
		if r.ID == "" {
			return nil, fmt.Errorf("classify: rule %d has no id", i)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("classify: duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		if !r.Bucket.Valid() {
			return nil, fmt.Errorf("classify: rule %q has an invalid bucket %d", r.ID, r.Bucket)
		}
	}

	e := &Engine{ctx: ctx, root: &trieNode{}, primary: ctx.primaryUser()}
	e.rules = make([]Rule, 0, len(rules)+16)
	e.rules = append(e.rules, rules...)
	e.rules = append(e.rules, projectRules(ctx)...)
	e.fixedOwner = make([]bool, len(e.rules))

	for i := range e.rules {
		if err := e.compile(int32(i)); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// compile parses and inserts every variant of one rule.
func (e *Engine) compile(idx int32) error {
	r := &e.rules[idx]
	e.fixedOwner[idx] = r.Owner != "" && len(references(r.Owner)) == 0
	vs, err := variantsOf(r, e.ctx)
	if err != nil {
		return fmt.Errorf("classify: rule %q: %w", r.ID, err)
	}
	bound := map[string]bool{}
	for _, v := range vs {
		for _, name := range v.pat.captureNames() {
			bound[name] = true
		}
		e.root.insert(v, idx)
	}
	for _, tmpl := range append([]string{r.Owner, r.Category, r.Explain}, r.OwnerKeys...) {
		for _, name := range references(tmpl) {
			if !bound[name] {
				return fmt.Errorf("classify: rule %q references capture {%s}, which its pattern does not bind", r.ID, name)
			}
		}
	}
	return nil
}

// Rules returns the compiled rule set, including the ones derived from the
// context. It exists for tests and for the rule listing in doctor.
func (e *Engine) Rules() []Rule { return e.rules }

// Match resolves one display path against the catalog alone, without a tree.
// It answers "which rule would claim this path", which is what the rule tests
// assert and what `storix explain` needs for a path the walk never retained.
// The returned claim carries no node.
func (e *Engine) Match(display string, isDir bool) (Claim, bool) {
	states := []state{{node: e.root}}
	trimmed := strings.Trim(path.Clean(display), "/")
	if trimmed == "" || trimmed == "." {
		return Claim{}, false
	}
	for _, seg := range strings.Split(trimmed, "/") {
		states = advance(states, seg)
		if len(states) == 0 {
			return Claim{}, false
		}
	}
	var cands []Claim
	for _, st := range states {
		for i := range st.node.terms {
			tm := &st.node.terms[i]
			if !isDir && !e.rules[tm.rule].Leaf {
				continue
			}
			cands = keepBest(cands, e.claimOf(tm, st.caps))
		}
	}
	if len(cands) == 0 {
		return Claim{}, false
	}
	win := 0
	for i := 1; i < len(cands); i++ {
		if better(&cands[i], &cands[win]) {
			win = i
		}
	}
	return cands[win], true
}

// projectRules turns the code roots into catalog rules. They cannot live in
// the static catalog because the roots come from the machine: a rule per root,
// a rule per project inside it, and rules for the build artifacts and the
// repository history directly inside a project (D34 / R7).
//
// The root goes in Anchor rather than into Match, so a code root really
// called "~/{work}" or "~/src*" anchors at that directory instead of being
// read as a capture or a glob. Context.codeRoots has already dropped the
// duplicates, which would otherwise compile to two identical rules per
// project and log every project as a conflict with itself.
func projectRules(ctx Context) []Rule {
	roots := ctx.codeRoots()
	out := make([]Rule, 0, len(roots)*(3+len(artifactDirs)))
	for i, root := range roots {
		n := fmt.Sprintf("%d", i)
		out = append(out,
			Rule{
				ID: "dev.projects.root." + n, Anchor: root,
				Bucket: BucketDeveloper, Category: "Project source", Owner: "Code root",
				Reclaim: UserData, Explain: "a code root: the projects under it are yours, their build output is not",
			},
			Rule{
				ID: "dev.projects.source." + n, Anchor: root, Match: "{project}",
				Bucket: BucketDeveloper, Category: "Project source", Owner: "{project}",
				OwnerKeys: []string{"project:{project}"}, Reclaim: UserData,
				Explain: "source of the project {project} under a code root",
			},
			Rule{
				ID: "dev.projects.git." + n, Anchor: root, Match: "{project}/.git",
				Bucket: BucketDeveloper, Category: "Repo history", Owner: "{project}",
				OwnerKeys: []string{"project:{project}"}, Reclaim: UserData,
				Explain: "git history of {project}; deleting it loses unpushed work",
			},
		)
		for j, dir := range artifactDirs {
			out = append(out, Rule{
				ID: fmt.Sprintf("dev.projects.artifacts.%d.%d", i, j), Anchor: root, Match: "{project}/" + dir,
				Bucket: BucketDeveloper, Category: "Build artifacts", Owner: "{project}",
				OwnerKeys: []string{"project:{project}"}, Reclaim: Regenerable,
				Explain: dir + " of {project}: rebuilt by the project's own tooling",
			})
		}
	}
	return out
}

// Run classifies a tree, resolving the catalog against the claims detectors
// and the apps inventory already made.
//
// One preorder pass assigns every node an effective claim, inheriting the
// parent's when nothing matches, and a second pass adds each node's own bytes
// (its total less its children's) to that claim's totals. Because every byte
// is owned by exactly one node and every node has exactly one effective claim
// or none, the bucket totals partition the scanned bytes to the byte.
func (e *Engine) Run(t *walk.Tree, extra []Claim) *Classification {
	c := &Classification{
		Owners: map[string]*OwnerTotal{},
		byKey:  map[string][]int32{},
	}
	if t == nil || t.Root == nil || len(t.Nodes) == 0 {
		return c
	}
	c.Effective = make([]int32, len(t.Nodes))
	for i := range c.Effective {
		c.Effective[i] = -1
	}

	extras, rejected := indexExtra(extra)
	c.Rejected = rejected
	e.assign(t, extras, c)
	e.aggregate(t, c)
	c.findUnmatched(t)
	c.finish()
	return c
}

// indexExtra groups detector and apps claims by the node they are about, and
// returns how many were dropped.
//
// A claim whose node is nil names a path the walk never retained; it is
// dropped rather than guessed at. A claim whose bucket is not one of the
// twelve is dropped too: New rejects such a rule outright, but a detector's
// claims are built at runtime and never see that check, so a zero Bucket —
// the value a struct literal that forgot the field has — would otherwise add
// its bytes to Buckets[0], which no bucket is and no report prints. That is
// the one way a byte can leave the partition without anything saying so, and
// counting the drops is what makes it visible.
func indexExtra(extra []Claim) (map[*walk.Node][]Claim, int) {
	if len(extra) == 0 {
		return nil, 0
	}
	m := make(map[*walk.Node][]Claim, len(extra))
	rejected := 0
	for i := range extra {
		if extra[i].Node == nil || !extra[i].Bucket.Valid() {
			rejected++
			continue
		}
		m[extra[i].Node] = append(m[extra[i].Node], extra[i])
	}
	return m, rejected
}

// frame is one open directory during the preorder pass.
type frame struct {
	n       *walk.Node
	states  []state
	id      int32 // the directory's own node id
	eff     int32 // the directory's own effective claim
	inherit int32 // what its children inherit
}

// assign walks the tree in preorder and gives every node its effective claim.
func (e *Engine) assign(t *walk.Tree, extras map[*walk.Node][]Claim, c *Classification) {
	initial := e.anchor(t)
	inherited := e.rootInherited(t, c)
	stack := make([]frame, 0, 64)
	var cands []Claim
	c.parent = make([]int32, len(t.Nodes))

	for i, n := range t.Nodes {
		// Node.ID is the node's index in this slice by definition, and
		// every consumer of the classification joins on it. A walk and a
		// cache read both stamp it; restamping here costs one store per
		// node and makes the invariant hold for a tree assembled by hand
		// in a test as well.
		n.ID = int32(i)
		for len(stack) > 0 && stack[len(stack)-1].n != n.Parent {
			stack = stack[:len(stack)-1]
		}
		states, parentEff, inherit := initial, int32(-1), inherited
		c.parent[i] = -1
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			states = advance(top.states, n.Name)
			parentEff, inherit = top.eff, top.inherit
			c.parent[i] = top.id
		}

		cands = e.candidates(cands[:0], n, states)
		if extras != nil {
			// A detector names its own paths, so its claims apply to
			// files as well as directories without a Leaf opt-in.
			cands = append(cands, extras[n]...)
		}

		eff := inherit
		childInherit := inherit
		if w := c.resolve(int32(i), n, cands); w >= 0 {
			eff = w
			childInherit = w
			if c.Claims[w].NoInherit {
				childInherit = inherit
			}
		}
		c.Effective[i] = eff
		c.noteRoot(int32(i), eff, parentEff)
		if n.IsDir() && len(n.Children) > 0 {
			stack = append(stack, frame{n: n, states: states, id: int32(i), eff: eff, inherit: childInherit})
		}
	}
}

// anchor returns the trie states the scan root itself sits at. A whole-volume
// scan starts at the trie root; a scan of ~/Library starts three segments in,
// so the same catalog classifies a partial root correctly.
//
// A root that walks off the trie — ~/Documents/Taxes, where the catalog has
// a rule for Documents and nothing below it — leaves no live state and an
// empty set is the honest answer: no rule matches the root or anything under
// it by name. It is not the same as no classification, because the rules the
// walk stepped over still apply; rootInherited is what carries those down.
func (e *Engine) anchor(t *walk.Tree) []state {
	states := []state{{node: e.root}}
	display := strings.Trim(mac.DisplayPath(t.Root.Path()), "/")
	if display == "" {
		return states
	}
	for _, seg := range strings.Split(display, "/") {
		if states = advance(states, seg); len(states) == 0 {
			return []state{}
		}
	}
	return states
}

// rootInherited is the claim the scan root inherits from the ancestors above
// it, recorded in c and returned as its claim index, or -1 when there is none.
//
// Scanning a subdirectory must not change what the bytes in it are. Without
// this, `storix --roots ~/Documents` reported Personal and `--roots
// ~/Documents/Taxes` reported the same bytes as Other, because the walk began
// below every rule that had anything to say about them. Resolving each
// ancestor prefix against the catalog reconstructs exactly the claim the
// preorder pass would have been holding when it reached the root: shallowest
// first, each ancestor's own claim replacing what it inherited, and a claim
// marked NoInherit passing on what it inherited rather than itself.
//
// The claim is recorded with no node of its own, because the node it was made
// about is above the scan root and outside the tree. That is what ExplicitAt
// and InheritedFrom report: no node here carries it, every node inherits it.
func (e *Engine) rootInherited(t *walk.Tree, c *Classification) int32 {
	display := strings.Trim(mac.DisplayPath(t.Root.Path()), "/")
	if display == "" {
		return -1
	}
	segs := strings.Split(display, "/")
	var found bool
	var best Claim
	for i := 1; i < len(segs); i++ {
		cl, ok := e.Match(strings.Join(segs[:i], "/"), true)
		if !ok || cl.NoInherit {
			continue
		}
		best, found = cl, true
	}
	if !found {
		return -1
	}
	c.Claims = append(c.Claims, best)
	c.claimNode = append(c.claimNode, -1)
	return int32(len(c.Claims) - 1)
}

// candidates builds the rule claims for one node.
//
// A "~" rule is compiled once per home, so the same rule can reach a node by
// two paths; only the most specific of them becomes a candidate, because a
// rule that beat itself would fill the conflict log with noise.
func (e *Engine) candidates(dst []Claim, n *walk.Node, states []state) []Claim {
	isDir := n.IsDir()
	for _, st := range states {
		for i := range st.node.terms {
			tm := &st.node.terms[i]
			r := &e.rules[tm.rule]
			if !isDir && !r.Leaf {
				continue
			}
			cl := e.claimOf(tm, st.caps)
			cl.Node = n
			cl.Evidence = []string{"rule " + r.ID + " matched " + n.Display()}
			dst = keepBest(dst, cl)
		}
	}
	return dst
}

// claimOf builds the claim one matched term makes. The caller adds the node
// and the evidence, which Match has neither of.
func (e *Engine) claimOf(tm *term, caps *capture) Claim {
	r := &e.rules[tm.rule]
	return Claim{
		Bucket:    r.Bucket,
		Category:  subst(r.Category, caps),
		Owner:     e.owner(tm, caps),
		OwnerKeys: substAll(r.OwnerKeys, caps),
		Reclaim:   r.Reclaim,
		Source:    Source{Kind: SourceRule, ID: r.ID},
		Depth:     tm.depth,
		Literals:  tm.literals,
		Shape:     tm.shape,
		Priority:  r.Priority,
	}
}

// owner is a rule's owner label for one match, qualified with the account
// name when the bytes are somebody else's.
//
// Owner totals are keyed by the label, so without this every account's
// Documents, Downloads and Mail add up into one row and a machine with three
// accounts reports a Documents folder nobody has. Only a fixed label is
// qualified: an owner that came from a capture already names an application
// or a project, and "com.spotify.client (bob)" would split one application's
// footprint across the accounts that happen to run it. The scan user is left
// unqualified so that the single-account machine, which is nearly every
// machine, reads exactly as it did before.
func (e *Engine) owner(tm *term, caps *capture) string {
	label := subst(e.rules[tm.rule].Owner, caps)
	if tm.user == "" || tm.user == e.primary || !e.fixedOwner[tm.rule] {
		return label
	}
	return label + " (" + tm.user + ")"
}

// keepBest adds a rule claim, replacing an earlier variant of the same rule
// when the new one is more specific.
func keepBest(dst []Claim, cl Claim) []Claim {
	for i := range dst {
		if dst[i].Source.ID != cl.Source.ID {
			continue
		}
		if better(&cl, &dst[i]) {
			dst[i] = cl
		}
		return dst
	}
	return append(dst, cl)
}

// substAll resolves the captures in every owner key.
func substAll(keys []string, caps *capture) []string {
	if len(keys) == 0 {
		return nil
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = subst(k, caps)
	}
	return out
}

// aggregate adds every node's own bytes to its effective claim's totals.
//
// A directory's own bytes are its total less its children's, which on APFS is
// exactly the aggregate of the leaves too small to have a node of their own.
// Summing own bytes rather than subtree bytes is what makes the buckets a
// partition instead of a set of overlapping totals.
func (e *Engine) aggregate(t *walk.Tree, c *Classification) {
	for i, n := range t.Nodes {
		own, files := n.Bytes, int64(1)
		if n.IsDir() {
			files = int64(n.Small.Files)
			for _, ch := range n.Children {
				own -= ch.Bytes
			}
		}
		b, cat, owner, rec := BucketOther, "", "", Unknown
		if eff := c.Effective[i]; eff >= 0 {
			cl := &c.Claims[eff]
			b, cat, owner, rec = cl.Bucket, cl.Category, cl.Owner, cl.Reclaim
		}
		bt := &c.Buckets[b]
		bt.Bytes += own
		bt.Files += files
		bt.ByReclaim[rec] += own
		if cat != "" {
			if bt.Categories == nil {
				bt.Categories = map[string]int64{}
			}
			bt.Categories[cat] += own
		}
		if owner != "" {
			c.addOwner(owner, b, own, c.Effective[i])
		}
	}
}

// sortConflicts orders the conflict log by the bytes at stake and trims it.
func sortConflicts(cs []Conflict) []Conflict {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Bytes > cs[j].Bytes })
	if len(cs) > maxConflicts {
		cs = cs[:maxConflicts]
	}
	return cs
}
