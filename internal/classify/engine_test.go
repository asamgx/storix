package classify

import (
	"context"
	"math/rand"
	"path"
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
