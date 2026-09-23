package classify

import (
	"context"
	"math/rand"
	"path"
	"sort"
	"strconv"
	"testing"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/walk"
)

// testHome is the home the fixture trees are built under. Fixtures live in a
// temp dir; renaming the walked root to the data volume makes every node's
// display path read exactly as it would on a real machine.
const testHome = "/Users/andrew"

// fixtureTree walks a fixture and re-anchors it at the data volume root.
func fixtureTree(t *testing.T, f *testutil.Fixture) *walk.Tree {
	t.Helper()
	tree, err := walk.Walk(context.Background(), walk.Options{
		Root:           f.Root,
		ExemptPrefixes: []string{},
		SkipNames:      []string{},
		SkipPaths:      []string{},
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	tree.Root.Name = mac.DataRoot
	return tree
}

// testContext is the context every engine test classifies against.
func testContext() Context {
	return Context{Home: testHome, CodeRoots: []string{testHome + "/code"}}
}

// run compiles rules and classifies a tree, failing the test on a bad rule.
func run(t *testing.T, rules []Rule, tree *walk.Tree, extra ...Claim) *Classification {
	t.Helper()
	e, err := New(rules, testContext())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e.Run(tree, extra)
}

// at looks a node up by display path and returns its id.
func at(t *testing.T, tree *walk.Tree, display string) int32 {
	t.Helper()
	n := node(t, tree, display)
	if tree.Nodes[n.ID] != n {
		t.Fatalf("node %s carries id %d, which belongs to another node", display, n.ID)
	}
	return n.ID
}

// node looks a node up by display path.
func node(t *testing.T, tree *walk.Tree, display string) *walk.Node {
	t.Helper()
	n, ok := tree.Lookup(mac.DataRoot + display)
	if !ok {
		t.Fatalf("no node at %s", display)
	}
	return n
}

// claimAt is the effective claim at a display path.
func claimAt(t *testing.T, c *Classification, tree *walk.Tree, display string) Claim {
	t.Helper()
	cl, ok := c.Of(at(t, tree, display))
	if !ok {
		t.Fatalf("no effective claim at %s", display)
	}
	return cl
}

func TestEngineInheritance(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Library/Caches/com.example.app/data/blob", 200_000)

	tree := fixtureTree(t, f)
	c := run(t, []Rule{{
		ID: "cache.user", Match: "~/Library/Caches/{bundleid}",
		Bucket: BucketAppData, Category: "Cache", Owner: "{bundleid}",
		OwnerKeys: []string{"app:{bundleid}"}, Reclaim: Regenerable,
	}}, tree)

	for _, p := range []string{
		"/Users/andrew/Library/Caches/com.example.app",
		"/Users/andrew/Library/Caches/com.example.app/data",
		"/Users/andrew/Library/Caches/com.example.app/data/blob",
	} {
		cl := claimAt(t, c, tree, p)
		if cl.Bucket != BucketAppData || cl.Owner != "com.example.app" {
			t.Errorf("%s: got bucket %s owner %q", p, cl.Bucket, cl.Owner)
		}
	}
	if _, ok := c.ExplicitAt(at(t, tree, "/Users/andrew/Library/Caches/com.example.app")); !ok {
		t.Error("the matched directory should carry its own claim")
	}
	if _, ok := c.ExplicitAt(at(t, tree, "/Users/andrew/Library/Caches/com.example.app/data")); ok {
		t.Error("a descendant should only inherit")
	}
	leaf := node(t, tree, "/Users/andrew/Library/Caches/com.example.app/data/blob")
	from := c.InheritedFromNode(leaf)
	if from == nil || from.Display() != "/Users/andrew/Library/Caches/com.example.app" {
		t.Errorf("InheritedFrom = %v", from)
	}
	if byNode, ok := c.OfNode(leaf); !ok || byNode.Owner != "com.example.app" {
		t.Errorf("OfNode = %+v, want the same claim the id lookup gives", byNode)
	}
	if c.InheritedFromNode(from) != nil {
		t.Error("the node carrying the claim inherited nothing")
	}
	if got := c.ByOwnerKey("app:com.example.app"); len(got) != 1 {
		t.Errorf("ByOwnerKey returned %d nodes, want 1", len(got))
	}
}

func TestEngineDeeperRuleWins(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Library/Application Support/Code/CachedData/x", 300_000)
	f.File("Users/andrew/Library/Application Support/Code/User/settings.json", 100_000)

	tree := fixtureTree(t, f)
	c := run(t, []Rule{
		{ID: "appdata.support", Match: "~/Library/Application Support/{name}",
			Bucket: BucketAppData, Category: "Application support", Owner: "{name}", Reclaim: Unknown},
		{ID: "dev.vscode.cache", Match: "~/Library/Application Support/Code/CachedData",
			Bucket: BucketDeveloper, Category: "IDE cache", Owner: "VS Code", Reclaim: Regenerable},
	}, tree)

	if got := claimAt(t, c, tree, "/Users/andrew/Library/Application Support/Code/CachedData").Bucket; got != BucketDeveloper {
		t.Errorf("CachedData bucket = %s, want developer", got)
	}
	if got := claimAt(t, c, tree, "/Users/andrew/Library/Application Support/Code/User").Bucket; got != BucketAppData {
		t.Errorf("User bucket = %s, want app-data", got)
	}
}

func TestEngineDetectorBeatsRuleOnSameNode(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Library/Application Support/Code/blob", 500_000)

	tree := fixtureTree(t, f)
	node, _ := tree.Lookup(mac.DataRoot + "/Users/andrew/Library/Application Support/Code")
	c := run(t, []Rule{{
		ID: "appdata.support", Match: "~/Library/Application Support/{name}",
		Bucket: BucketAppData, Category: "Application support", Owner: "{name}", Reclaim: Unknown,
	}}, tree, Claim{
		Node: node, Bucket: BucketDeveloper, Category: "IDE data", Owner: "VS Code",
		OwnerKeys: []string{"app:com.microsoft.VSCode"}, Reclaim: UserData,
		Source: Source{Kind: SourceDetector, ID: "ide", Detector: "ide"},
	})

	cl := claimAt(t, c, tree, "/Users/andrew/Library/Application Support/Code")
	if cl.Bucket != BucketDeveloper || cl.Source.Detector != "ide" {
		t.Errorf("claim = %+v, want the detector's", cl)
	}
	if len(c.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(c.Conflicts))
	}
	if c.Conflicts[0].Winner.Detector != "ide" || c.Conflicts[0].Loser.ID != "appdata.support" {
		t.Errorf("conflict = %+v", c.Conflicts[0])
	}
}

func TestEngineRuleWinsBelowDetectorClaim(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Library/Application Support/Code/logs/a", 200_000)

	tree := fixtureTree(t, f)
	node, _ := tree.Lookup(mac.DataRoot + "/Users/andrew/Library/Application Support/Code")
	c := run(t, []Rule{{
		ID: "dev.vscode.logs", Match: "~/Library/Application Support/Code/logs",
		Bucket: BucketSystemCaches, Category: "Logs", Owner: "VS Code", Reclaim: Regenerable,
	}}, tree, Claim{
		Node: node, Bucket: BucketDeveloper, Category: "IDE data", Owner: "VS Code",
		Reclaim: UserData, Source: Source{Kind: SourceDetector, ID: "ide", Detector: "ide"},
	})

	if got := claimAt(t, c, tree, "/Users/andrew/Library/Application Support/Code/logs").Bucket; got != BucketSystemCaches {
		t.Errorf("logs bucket = %s, want system-caches", got)
	}
	if len(c.Conflicts) != 0 {
		t.Errorf("a rule on a descendant is inheritance, not a conflict: %+v", c.Conflicts)
	}
}

func TestEngineCodeRoots(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/code/mochi/main.go", 100_000)
	f.File("Users/andrew/code/mochi/node_modules/pkg/index.js", 400_000)
	f.File("Users/andrew/code/mochi/.git/objects/pack/p", 250_000)

	tree := fixtureTree(t, f)
	c := run(t, nil, tree)

	cases := map[string]struct {
		bucket   Bucket
		category string
		reclaim  Reclaim
	}{
		"/Users/andrew/code/mochi":              {BucketDeveloper, "Project source", UserData},
		"/Users/andrew/code/mochi/main.go":      {BucketDeveloper, "Project source", UserData},
		"/Users/andrew/code/mochi/node_modules": {BucketDeveloper, "Build artifacts", Regenerable},
		"/Users/andrew/code/mochi/.git":         {BucketDeveloper, "Repo history", UserData},
	}
	for p, want := range cases {
		cl := claimAt(t, c, tree, p)
		if cl.Bucket != want.bucket || cl.Category != want.category || cl.Reclaim != want.reclaim {
			t.Errorf("%s: got %s/%s/%s", p, cl.Bucket, cl.Category, cl.Reclaim)
		}
		if cl.Owner != "mochi" && p != "/Users/andrew/code" {
			t.Errorf("%s: owner = %q, want mochi", p, cl.Owner)
		}
	}
}

func TestEngineOtherUsersHome(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/someone/Library/Caches/com.example.app/blob", 100_000)

	tree := fixtureTree(t, f)
	c := run(t, []Rule{{
		ID: "cache.user", Match: "~/Library/Caches/{bundleid}",
		Bucket: BucketAppData, Owner: "{bundleid}", Reclaim: Regenerable,
	}}, tree)

	cl := claimAt(t, c, tree, "/Users/someone/Library/Caches/com.example.app")
	if cl.Bucket != BucketAppData || cl.Owner != "com.example.app" {
		t.Errorf("another user's cache = %+v", cl)
	}
}

func TestEngineRootsAndUnmatched(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Library/Caches/com.a/blob", 300_000)
	f.File("Users/andrew/Documents/notes.txt", 100_000)
	f.File("opt/unknown-thing/big", 900_000)

	tree := fixtureTree(t, f)
	c := run(t, []Rule{
		{ID: "cache.user", Match: "~/Library/Caches/{bundleid}", Bucket: BucketAppData, Owner: "{bundleid}", Reclaim: Regenerable},
		{ID: "personal.documents", Match: "~/Documents", Bucket: BucketPersonal, Owner: "You", Reclaim: UserData},
	}, tree)

	roots := c.Roots(BucketAppData)
	if len(roots) != 1 || tree.Nodes[roots[0]].Display() != "/Users/andrew/Library/Caches/com.a" {
		t.Errorf("app-data roots = %v", roots)
	}
	var found bool
	for _, id := range c.Unmatched {
		if tree.Nodes[id].Display() == "/opt" {
			found = true
		}
	}
	if !found {
		t.Errorf("unmatched roots %v do not include /opt", displayAll(tree, c.Unmatched))
	}
}

func displayAll(tree *walk.Tree, ids []int32) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = tree.Nodes[id].Display()
	}
	return out
}

func TestEngineBucketsPartitionRandomTrees(t *testing.T) {
	rules := []Rule{
		{ID: "a", Match: "~/Library/Caches/{name}", Bucket: BucketAppData, Owner: "{name}", Reclaim: Regenerable},
		{ID: "b", Match: "~/Documents", Bucket: BucketPersonal, Owner: "You", Reclaim: UserData},
		{ID: "c", Match: "~/Library", Bucket: BucketAppData, Owner: "Library", Reclaim: Unknown},
		{ID: "d", Match: "/opt/{name}", Bucket: BucketDeveloper, Owner: "{name}", Reclaim: ToolManaged},
	}
	dirs := []string{
		"Users/andrew/Library/Caches/com.a", "Users/andrew/Library/Caches/com.b",
		"Users/andrew/Library/Preferences", "Users/andrew/Documents/x",
		"opt/homebrew/Cellar", "private/var/db", "Applications",
	}
	for seed := range 12 {
		rng := rand.New(rand.NewSource(int64(seed))) //nolint:gosec // a test needs a reproducible generator, not a secure one
		f := testutil.New(t)
		for i := range 25 {
			d := dirs[rng.Intn(len(dirs))]
			for range rng.Intn(3) {
				d = path.Join(d, "n"+strconv.Itoa(rng.Intn(3)))
			}
			f.File(path.Join(d, "f"+strconv.Itoa(i)), rng.Intn(70_000)+1)
		}
		tree := fixtureTree(t, f)
		c := run(t, rules, tree)
		if got, want := c.Total(), tree.Root.Bytes; got != want {
			t.Fatalf("seed %d: buckets sum to %d, tree root is %d", seed, got, want)
		}
	}
}

func TestEngineConflictCap(t *testing.T) {
	f := testutil.New(t)
	for i := range 40 {
		f.File("Users/andrew/Library/Caches/c"+strconv.Itoa(i)+"/f", 1_000)
	}
	tree := fixtureTree(t, f)
	rules := make([]Rule, 0, 6)
	for i := range 6 {
		rules = append(rules, Rule{
			ID: "r" + strconv.Itoa(i), Match: "~/Library/Caches/{name}",
			Bucket: BucketAppData, Owner: "{name}", Reclaim: Regenerable, Priority: int8(i),
		})
	}
	c := run(t, rules, tree)
	if len(c.Conflicts) == 0 {
		t.Fatal("six rules on one node should conflict")
	}
	if len(c.Conflicts) > maxConflicts {
		t.Errorf("conflicts = %d, over the cap", len(c.Conflicts))
	}
	if got := claimAt(t, c, tree, "/Users/andrew/Library/Caches/c0").Source.ID; got != "r5" {
		t.Errorf("highest priority should win, got %s", got)
	}
}

func TestEnginePartialRoot(t *testing.T) {
	f := testutil.New(t)
	f.File("Caches/com.example.app/blob", 100_000)

	tree, err := walk.Walk(context.Background(), walk.Options{
		Root: f.Root, ExemptPrefixes: []string{}, SkipNames: []string{}, SkipPaths: []string{},
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	tree.Root.Name = mac.DataRoot + testHome + "/Library"

	e, err := New([]Rule{{
		ID: "cache.user", Match: "~/Library/Caches/{bundleid}",
		Bucket: BucketAppData, Owner: "{bundleid}", Reclaim: Regenerable,
	}}, testContext())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := e.Run(tree, nil)
	cl := claimAt(t, c, tree, "/Users/andrew/Library/Caches/com.example.app")
	if cl.Owner != "com.example.app" {
		t.Errorf("a partial root should classify the same way: %+v", cl)
	}
}

// TestEngineRootBelowARule is the inverse of TestEnginePartialRoot, which
// anchors the scan above a rule's terminal segment. Here the scan begins
// below it: every rule that has anything to say about these bytes was
// stepped over before the walk started, and the classification has to
// reconstruct it. Without that, `storix --roots ~/Documents` reported
// Personal and `--roots ~/Documents/Taxes` reported the same bytes as Other.
func TestEngineRootBelowARule(t *testing.T) {
	f := testutil.New(t)
	f.File("2024/return.pdf", 300_000)
	f.File("receipts/a.pdf", 150_000)

	tree, err := walk.Walk(context.Background(), walk.Options{
		Root: f.Root, ExemptPrefixes: []string{}, SkipNames: []string{}, SkipPaths: []string{},
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	tree.Root.Name = mac.DataRoot + testHome + "/Documents/Taxes"

	e, err := New([]Rule{{
		ID: "personal.documents", Match: "~/Documents",
		Bucket: BucketPersonal, Category: "Documents", Owner: "Documents", Reclaim: UserData,
	}}, testContext())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := e.Run(tree, nil)

	for _, p := range []string{
		"/Users/andrew/Documents/Taxes",
		"/Users/andrew/Documents/Taxes/2024",
		"/Users/andrew/Documents/Taxes/receipts",
	} {
		cl := claimAt(t, c, tree, p)
		if cl.Bucket != BucketPersonal || cl.Owner != "Documents" || cl.Source.ID != "personal.documents" {
			t.Errorf("%s: got %s/%q from %s, want the ancestor rule's claim", p, cl.Bucket, cl.Owner, cl.Source.ID)
		}
	}
	if got, want := c.Buckets[BucketPersonal].Bytes, tree.Root.Bytes; got != want {
		t.Errorf("Personal holds %d of the %d scanned bytes: a scan root below a rule must not change what the bytes are", got, want)
	}
	if got := c.Buckets[BucketOther].Bytes; got != 0 {
		t.Errorf("Other holds %d bytes, want none", got)
	}
	if len(c.Unmatched) != 0 {
		t.Errorf("unmatched roots %v, want none: every node inherited the ancestor's claim", displayAll(tree, c.Unmatched))
	}
	// No node in this tree made the claim, so nothing carries it explicitly
	// and nothing names the node it was inherited from: that node is above
	// the scan root and outside the tree.
	if _, ok := c.ExplicitAtNode(tree.Root); ok {
		t.Error("the scan root carries the ancestor's claim as its own")
	}
	if from := c.InheritedFromNode(tree.Root); from != nil {
		t.Errorf("InheritedFrom = %v, want nil: the claiming node is outside the tree", from)
	}
}

// TestEngineSharedIsNotAHome: /Users/Shared is a drop folder every account
// can write to, not somebody's home, so the "~" rules must not anchor at it.
func TestEngineSharedIsNotAHome(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/Shared/Library/Caches/com.example.app/blob", 300_000)
	f.File("Users/someone/Library/Caches/com.example.app/blob", 300_000)

	tree := fixtureTree(t, f)
	c := run(t, []Rule{
		{ID: "cache.user", Match: "~/Library/Caches/{bundleid}",
			Bucket: BucketAppData, Owner: "{bundleid}", Reclaim: Regenerable},
		{ID: "personal.shared", Match: "/Users/Shared",
			Bucket: BucketPersonal, Category: "Shared", Owner: "Shared", Reclaim: UserData},
	}, tree)

	cl := claimAt(t, c, tree, "/Users/Shared/Library/Caches/com.example.app")
	if cl.Source.ID != "personal.shared" || cl.Bucket != BucketPersonal {
		t.Errorf("a cache under /Users/Shared resolved to %s/%s, want the shared-folder rule", cl.Source.ID, cl.Bucket)
	}
	// A home nobody named still classifies: the exclusion is one name, not
	// the whole catch-all.
	other := claimAt(t, c, tree, "/Users/someone/Library/Caches/com.example.app")
	if other.Source.ID != "cache.user" || other.Bucket != BucketAppData {
		t.Errorf("an unnamed home resolved to %s/%s, want the cache rule", other.Source.ID, other.Bucket)
	}
}

// TestEngineOwnerLabelsNameTheAccount: owner totals are keyed by the label,
// so a fixed label under the home has to say whose home it was or three
// accounts' Documents folders add up into one row nobody has.
func TestEngineOwnerLabelsNameTheAccount(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Documents/mine.txt", 300_000)
	f.File("Users/bob/Documents/theirs.txt", 500_000)
	f.File("Users/bob/Library/Caches/com.example.app/blob", 700_000)

	tree := fixtureTree(t, f)
	e, err := New([]Rule{
		{ID: "personal.documents", Match: "~/Documents",
			Bucket: BucketPersonal, Category: "Documents", Owner: "Documents", Reclaim: UserData},
		{ID: "cache.user", Match: "~/Library/Caches/{bundleid}",
			Bucket: BucketAppData, Owner: "{bundleid}", Reclaim: Regenerable},
	}, Context{Home: testHome, Users: []string{"/Users/bob"}, CodeRoots: []string{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := e.Run(tree, nil)

	cases := map[string]string{
		"/Users/andrew/Documents":                   "Documents",
		"/Users/bob/Documents":                      "Documents (bob)",
		"/Users/bob/Library/Caches/com.example.app": "com.example.app",
	}
	for p, want := range cases {
		if got := claimAt(t, c, tree, p).Owner; got != want {
			t.Errorf("%s: owner %q, want %q", p, got, want)
		}
	}
	mine, theirs := c.Owners["Documents"], c.Owners["Documents (bob)"]
	if mine == nil || theirs == nil {
		t.Fatalf("owners = %v, want one row per account", ownerNames(c))
	}
	if mine.Bytes >= theirs.Bytes {
		t.Errorf("Documents = %d bytes and Documents (bob) = %d: the two accounts' totals still merge",
			mine.Bytes, theirs.Bytes)
	}
}

func ownerNames(c *Classification) []string {
	out := make([]string, 0, len(c.Owners))
	for k := range c.Owners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestRunRejectsUnusableExtraClaims: New checks every rule's bucket, but a
// detector builds its claims at runtime and never meets that check. A claim
// with the zero bucket would land in Buckets[0], which no bucket is and no
// report prints, and one with a bucket past the end would panic the pass.
func TestRunRejectsUnusableExtraClaims(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/Library/Application Support/Code/blob", 500_000)

	tree := fixtureTree(t, f)
	n := node(t, tree, "/Users/andrew/Library/Application Support/Code")
	src := Source{Kind: SourceDetector, ID: "ide", Detector: "ide"}
	c := run(t, nil, tree,
		Claim{Node: n, Owner: "no bucket at all", Source: src},
		Claim{Node: n, Bucket: Bucket(99), Owner: "a bucket past the end", Source: src},
		Claim{Node: nil, Bucket: BucketDeveloper, Owner: "a path the walk never retained", Source: src},
	)

	if c.Rejected != 3 {
		t.Errorf("Rejected = %d, want 3", c.Rejected)
	}
	if got := c.Buckets[0].Bytes; got != 0 {
		t.Errorf("Buckets[0] holds %d bytes, which no report prints", got)
	}
	if _, ok := c.ExplicitAtNode(n); ok {
		t.Error("a rejected claim still won its node")
	}
	if got, want := c.Total(), tree.Root.Bytes; got != want {
		t.Errorf("the buckets sum to %d, the tree to %d", got, want)
	}

	// The same three claims with a bucket that exists are kept, so the
	// rejection is about the bucket and not about the shape of the test.
	ok := run(t, nil, tree, Claim{Node: n, Bucket: BucketDeveloper, Owner: "VS Code", Source: src})
	if ok.Rejected != 0 {
		t.Errorf("Rejected = %d for a well-formed claim", ok.Rejected)
	}
}

// TestEngineDuplicateCodeRoots: a root named twice used to compile to two
// identical rules per project, which match the same node with the same
// specificity, so every project was logged as a conflict between a rule and
// its own copy.
func TestEngineDuplicateCodeRoots(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/code/mochi/main.go", 300_000)

	tree := fixtureTree(t, f)
	e, err := New(nil, Context{Home: testHome, CodeRoots: []string{
		testHome + "/code", testHome + "/code/", testHome + "/code",
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := e.Run(tree, nil)

	if len(c.Conflicts) != 0 {
		t.Errorf("a code root named three times conflicted with itself: %+v", c.Conflicts)
	}
	if got := claimAt(t, c, tree, "/Users/andrew/code/mochi").Owner; got != "mochi" {
		t.Errorf("owner = %q, want mochi", got)
	}
}

// TestEngineMachinePathsAreLiteral: a home and a code root are names read off
// a disk, not patterns somebody wrote. A directory genuinely called "w*rk" is
// a legal directory, and reading it as a glob would anchor every project rule
// at whatever else the glob happened to cover.
func TestEngineMachinePathsAreLiteral(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/w*rk/proj/main.go", 300_000)
	f.File("Users/andrew/work/other/main.go", 300_000)

	tree := fixtureTree(t, f)
	e, err := New(nil, Context{Home: testHome, CodeRoots: []string{testHome + "/w*rk"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := e.Run(tree, nil)

	if got := claimAt(t, c, tree, "/Users/andrew/w*rk/proj").Owner; got != "proj" {
		t.Errorf("the code root did not match the directory it names: owner = %q", got)
	}
	if cl, ok := c.Of(at(t, tree, "/Users/andrew/work/other")); ok {
		t.Errorf("a code root spelled w*rk also claimed /Users/andrew/work/other as %s/%q",
			cl.Bucket, cl.Owner)
	}

	// The home is anchored the same way. It has no end-to-end tell of its
	// own, because "Users/*" would reach another account's home anyway, so
	// the anchor's segments are asserted directly.
	r := Rule{ID: "x", Match: "~/Documents", Bucket: BucketPersonal}
	vs, err := variantsOf(&r, Context{Home: "/Users/h{x}*me"})
	if err != nil {
		t.Fatalf("variantsOf: %v", err)
	}
	seg := vs[0].pat.segs[1]
	if seg.kind != segLiteral || seg.text != "h{x}*me" {
		t.Fatalf("the home compiled to %+v, want one literal segment", seg)
	}
	for _, name := range []string{"home", "hme", "hxme", "h{x}me"} {
		if _, ok := seg.match(name); ok {
			t.Errorf("a home spelled h{x}*me also matched the directory %q", name)
		}
	}
	if _, ok := seg.match("h{x}*me"); !ok {
		t.Error("a home spelled h{x}*me did not match itself")
	}
}

func TestNewRejectsBadRules(t *testing.T) {
	cases := map[string][]Rule{
		"duplicate id": {
			{ID: "x", Match: "/A", Bucket: BucketOther},
			{ID: "x", Match: "/B", Bucket: BucketOther},
		},
		"unknown capture": {
			{ID: "x", Match: "/A/{name}", Bucket: BucketOther, Owner: "{other}"},
		},
		"bad bucket": {
			{ID: "x", Match: "/A", Bucket: Bucket(99)},
		},
		"root pattern": {
			{ID: "x", Match: "/", Bucket: BucketOther},
		},
		"two captures in a segment": {
			{ID: "x", Match: "/A/{a}-{b}", Bucket: BucketOther},
		},
	}
	for name, rules := range cases {
		if _, err := New(rules, testContext()); err == nil {
			t.Errorf("%s: New accepted the catalog", name)
		}
	}
}
