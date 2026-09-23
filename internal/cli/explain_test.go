package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/classify/catalog"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// The classification tests build their tree by hand rather than walking a
// fixture, because the catalog is anchored at real paths: a rule for
// "~/Library/Caches/{bundleid}" cannot reach a directory under t.TempDir(),
// so a walked fixture can only ever exercise the plumbing. The tree below is
// the smallest one that puts a catalog rule, a detector claim and a conflict
// between them in front of the command.

// explainFixture is a hand-built scan holding one home with a cache
// directory a catalog rule claims and one a detector claims.
type explainFixture struct {
	res   *scan.Result
	cache string // the display path of ~/Library/Caches
	rule  string // the directory the catalog claims
	det   string // the directory the detector claims
}

func newExplainFixture(t *testing.T) explainFixture {
	t.Helper()

	root := &walk.Node{Name: mac.DataRoot, Kind: walk.KindDir}
	caches := dirAt(root, "Users", "andrew", "Library", "Caches")
	ruled := leafDir(caches, "com.example.app", 200<<20, 12)
	det := leafDir(caches, "pnpm", 500<<20, 40)

	tree := &walk.Tree{Root: root}
	finalize(tree, root)

	engine, err := classify.New(catalog.Rules(), classify.Context{Home: "/Users/andrew"})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	extra := []classify.Claim{{
		Node: det, Bucket: classify.BucketDeveloper, Category: "Package cache",
		Owner: "pnpm", OwnerKeys: []string{"cli:pnpm"}, Reclaim: classify.Regenerable,
		Source:   classify.Source{Kind: classify.SourceDetector, ID: "node", Detector: "node"},
		Evidence: []string{"`pnpm store path` → /Users/andrew/Library/pnpm/store/v10"},
	}}
	class := engine.Run(tree, extra)

	res := &scan.Result{
		Tree: tree, Class: class, Apps: explainReport(),
		FromCache: true, CacheAge: 90 * time.Minute,
	}
	return explainFixture{res: res, cache: caches.Display(), rule: ruled.Display(), det: det.Display()}
}

// explainReport is the application inventory the fixture carries: one
// installed owner whose footprint crosses three buckets.
func explainReport() *apps.Report {
	return &apps.Report{
		Schema: apps.ReportSchema,
		Apps: []apps.Entry{{
			Owner: "app:com.example.app", Label: "Example", State: "installed",
			Confidence: "strong",
			Footprint: apps.Sizes{
				Bundle: 300 << 20, Data: 100 << 20, Caches: 200 << 20,
				Dev: 50 << 20, Total: 650 << 20,
			},
			Bundles: []apps.BundleRef{{Path: "/Applications/Example.app", ID: "com.example.app"}},
			Components: []apps.ComponentRef{
				{Path: "/Applications/Example.app", Bytes: 300 << 20, Bucket: "apps", Source: "apps/bundle"},
				{Path: "/Users/andrew/Library/Caches/com.example.app", Bytes: 200 << 20,
					Bucket: "app-data", Category: "Cache", Source: "apps/bundle-id"},
				{Path: "/Users/andrew/.example", Bytes: 50 << 20, Bucket: "developer", Source: "detector:ide"},
			},
			Evidence: []string{"installed at /Applications/Example.app"},
		}},
	}
}

// dirAt creates a chain of directories under parent and returns the deepest.
func dirAt(parent *walk.Node, names ...string) *walk.Node {
	n := parent
	for _, name := range names {
		n = leafDir(n, name, 0, 0)
	}
	return n
}

// leafDir adds one directory of its own size to parent.
func leafDir(parent *walk.Node, name string, size int64, files uint32) *walk.Node {
	n := &walk.Node{
		Name: name, Parent: parent, Kind: walk.KindDir,
		Bytes: size, Files: files, Mtime: time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC).Unix(),
	}
	parent.Children = append(parent.Children, n)
	sort.Slice(parent.Children, func(i, j int) bool {
		return parent.Children[i].Name < parent.Children[j].Name
	})
	return n
}

// finalize totals the subtree sizes into the parents and fills Tree.Nodes in
// preorder, which is what a real walk's own finalize pass does.
func finalize(t *walk.Tree, n *walk.Node) {
	n.ID = int32(len(t.Nodes))
	t.Nodes = append(t.Nodes, n)
	for _, kid := range n.Children {
		finalize(t, kid)
		n.Bytes += kid.Bytes
		n.Files += kid.Files
	}
}

// explainText renders one answer the way the command prints it.
func explainText(t *testing.T, res *scan.Result, arg string) (string, error) {
	t.Helper()
	doc, err := explainOf(res, arg)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := doc.write(&out, units.Decimal); err != nil {
		t.Fatalf("write: %v", err)
	}
	return out.String(), nil
}

func TestLooksLikePathTellsPathsFromOwners(t *testing.T) {
	cases := map[string]bool{
		"~/Library/Caches":          true,
		"/Users/andrew":             true,
		"./Caches":                  true,
		"com.microsoft.VSCode":      false,
		"cask:cursor":               false,
		"cli:pnpm":                  false,
		"Visual Studio Code":        false,
		"app:dev.orbstack.OrbStack": false,
	}
	for arg, want := range cases {
		if got := looksLikePath(arg); got != want {
			t.Errorf("looksLikePath(%q) = %v, want %v", arg, got, want)
		}
	}
}

func TestExplainResolveRejectsBothCacheFlags(t *testing.T) {
	o := explainOptions{fromCache: true, scan: true}
	_, _, err := o.resolve()
	var ce *ConfigError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("resolve accepted --from-cache with --scan: %v", err)
	}
}

func TestExplainResolveDefaultsToTheStoredScan(t *testing.T) {
	_, cfg, err := (&explainOptions{}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.FromCache {
		t.Error("the default did not ask for the stored scan, so a stale one would be rescanned")
	}
	_, cfg, err = (&explainOptions{scan: true}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FromCache {
		t.Error("--scan still asked for the stored scan")
	}
}

func TestExplainPathPrintsTheBucketOwnerSourceAndEvidence(t *testing.T) {
	f := newExplainFixture(t)

	out, err := explainText(t, f.res, f.rule)
	if err != nil {
		t.Fatalf("explain %s: %v", f.rule, err)
	}
	for _, want := range []string{
		f.rule,                // the header names the directory
		"209.7 MB",            // and its size
		"3 App data",          // the bucket, by number and label
		"com.example.app",     // the owner the rule named
		"by         rule:",    // the rule that claimed it
		"evidence",            // and its evidence
		"footprint  Example:", // the owner's whole footprint
		"installed",           // with the inventory's verdict
		"from cache, 1h30m old",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the block does not mention %q:\n%s", want, out)
		}
	}
}

func TestExplainPathShowsTheDetectorClaimAndTheRuleThatLost(t *testing.T) {
	f := newExplainFixture(t)

	out, err := explainText(t, f.res, f.det)
	if err != nil {
		t.Fatalf("explain %s: %v", f.det, err)
	}
	if !strings.Contains(out, "by         detector:node") {
		t.Errorf("the detector is not named as the source:\n%s", out)
	}
	if !strings.Contains(out, "`pnpm store path`") {
		t.Errorf("the detector's evidence is missing:\n%s", out)
	}
	if !strings.Contains(out, "also claimed by") || !strings.Contains(out, "lost to detector:node") {
		t.Errorf("the losing catalog rule is not reported:\n%s", out)
	}
	if !strings.Contains(out, "4 Developer") {
		t.Errorf("the detector's bucket is missing:\n%s", out)
	}
}

func TestExplainPathBreaksDownASubtreeThatSpansBuckets(t *testing.T) {
	f := newExplainFixture(t)

	out, err := explainText(t, f.res, f.cache)
	if err != nil {
		t.Fatalf("explain %s: %v", f.cache, err)
	}
	if !strings.Contains(out, "below it") {
		t.Fatalf("a subtree spanning two buckets printed no breakdown:\n%s", out)
	}
	if !strings.Contains(out, "4 Developer") || !strings.Contains(out, "3 App data") {
		t.Errorf("the breakdown does not name both buckets:\n%s", out)
	}

	// A directory whose whole subtree is in one bucket says nothing extra.
	single, err := explainText(t, f.res, f.rule)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(single, "below it") {
		t.Errorf("a single-bucket subtree printed a breakdown:\n%s", single)
	}
}

func TestExplainPathNotInTheScanIsAConfigErrorWithTheFoldingHint(t *testing.T) {
	f := newExplainFixture(t)

	_, err := explainText(t, f.res, "/Users/andrew/Library/Caches/not-scanned")
	if err == nil {
		t.Fatal("explaining a path that was never scanned succeeded")
	}
	if got := ExitCode(err); got != ExitConfigError {
		t.Errorf("exit code = %d, want %d", got, ExitConfigError)
	}
	for _, want := range []string{"not-scanned", "stored scan", "64 KB", "1h30m old"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestExplainOwnerAnswersForAKeyABundleIDAndALabel(t *testing.T) {
	f := newExplainFixture(t)

	for _, arg := range []string{"app:com.example.app", "com.example.app", "Example"} {
		out, err := explainText(t, f.res, arg)
		if err != nil {
			t.Fatalf("explain %s: %v", arg, err)
		}
		if !strings.Contains(out, "Example (app:com.example.app) — installed") {
			t.Errorf("explain %s did not identify the owner:\n%s", arg, out)
		}
		if !strings.Contains(out, "components") || !strings.Contains(out, "/Applications/Example.app") {
			t.Errorf("explain %s listed no components:\n%s", arg, out)
		}
		for _, bucket := range []string{"2 Applications", "3 App data", "4 Developer"} {
			if !strings.Contains(out, bucket) {
				t.Errorf("explain %s does not cross %s:\n%s", arg, bucket, out)
			}
		}
	}
}

func TestExplainOwnerFallsBackToTheClassificationJoin(t *testing.T) {
	f := newExplainFixture(t)

	// pnpm is not in the application inventory: its footprint is the join
	// on the owner key the detector set.
	out, err := explainText(t, f.res, "cli:pnpm")
	if err != nil {
		t.Fatalf("explain cli:pnpm: %v", err)
	}
	if !strings.Contains(out, "pnpm") || !strings.Contains(out, "4 Developer") {
		t.Errorf("the toolchain owner was not resolved:\n%s", out)
	}
	if bare, err := explainText(t, f.res, "pnpm"); err != nil {
		t.Errorf("explain pnpm: %v", err)
	} else if !strings.Contains(bare, "4 Developer") {
		t.Errorf("the unprefixed owner key was not expanded:\n%s", bare)
	}
}

func TestExplainOwnerUnknownSuggestsByPrefixAndExitsTwo(t *testing.T) {
	f := newExplainFixture(t)

	_, err := explainText(t, f.res, "com.example")
	if err == nil {
		t.Fatal("an owner that does not exist was explained anyway")
	}
	if got := ExitCode(err); got != ExitConfigError {
		t.Errorf("exit code = %d, want %d", got, ExitConfigError)
	}
	if !strings.Contains(err.Error(), "did you mean") || !strings.Contains(err.Error(), "app:com.example.app") {
		t.Errorf("error %q suggests nothing", err)
	}
}

func TestExplainJSONMirrorsTheFields(t *testing.T) {
	f := newExplainFixture(t)

	doc, err := explainOf(f.res, f.rule)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Schema int    `json:"schema"`
		Mode   string `json:"mode"`
		Scan   struct {
			FromCache bool  `json:"fromCache"`
			AgeNS     int64 `json:"age_ns"`
		} `json:"scan"`
		Path struct {
			Path        string   `json:"path"`
			Bytes       int64    `json:"bytes"`
			Bucket      int      `json:"bucket"`
			BucketLabel string   `json:"bucketLabel"`
			Owner       string   `json:"owner"`
			OwnerKeys   []string `json:"ownerKeys"`
			Source      string   `json:"source"`
			Evidence    []string `json:"evidence"`
			Footprint   struct {
				Label     string `json:"label"`
				Footprint struct {
					Total int64 `json:"total"`
				} `json:"footprint"`
			} `json:"footprint"`
		} `json:"path"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the document is not valid JSON: %v\n%s", err, raw)
	}
	if got.Schema != apps.ReportSchema || got.Mode != "path" {
		t.Errorf("schema/mode = %d/%q", got.Schema, got.Mode)
	}
	if !got.Scan.FromCache || got.Scan.AgeNS == 0 {
		t.Error("the document does not say the answer came from the cache")
	}
	if got.Path.Path != f.rule || got.Path.Bytes != 200<<20 {
		t.Errorf("path = %q, %d bytes", got.Path.Path, got.Path.Bytes)
	}
	if got.Path.Bucket != int(classify.BucketAppData) || got.Path.BucketLabel != "App data" {
		t.Errorf("bucket = %d %q", got.Path.Bucket, got.Path.BucketLabel)
	}
	if got.Path.Owner == "" || len(got.Path.OwnerKeys) == 0 || got.Path.Source == "" {
		t.Errorf("owner, keys or source missing: %+v", got.Path)
	}
	if len(got.Path.Evidence) == 0 {
		t.Error("the document carries no evidence")
	}
	if got.Path.Footprint.Label != "Example" || got.Path.Footprint.Footprint.Total == 0 {
		t.Errorf("footprint = %+v", got.Path.Footprint)
	}
}

func TestExplainAgeUsesTheCoarsestUsefulUnit(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "under a minute"},
		{12 * time.Minute, "12m"},
		{90 * time.Minute, "1h30m"},
		{50 * time.Hour, "2d02h"},
	}
	for _, c := range cases {
		if got := explainAge(c.d); got != c.want {
			t.Errorf("explainAge(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestSourceTextDropsTheRepeatedAppsPrefix(t *testing.T) {
	cases := []struct {
		src  classify.Source
		want string
	}{
		{classify.Source{Kind: classify.SourceRule, ID: "cache.user.bundle"}, "rule:cache.user.bundle"},
		{classify.Source{Kind: classify.SourceApps, ID: "apps/cask-zap"}, "apps/cask-zap"},
		{classify.Source{Kind: classify.SourceDetector, ID: "node", Detector: "node"}, "detector:node"},
	}
	for _, c := range cases {
		if got := sourceText(c.src); got != c.want {
			t.Errorf("sourceText(%+v) = %q, want %q", c.src, got, c.want)
		}
	}
}

// TestRunExplainWalksAFixtureAndPrintsTheDirectory is the end-to-end path:
// no stored scan, so the command walks, finds the directory in the tree it
// just built and prints the block around it.
func TestRunExplainWalksAFixtureAndPrintsTheDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("Library/Caches/com.example.app/blob.bin", 200_000)

	var out, errOut bytes.Buffer
	o := &explainOptions{roots: []string{f.Root}, scan: true}
	arg := f.Path("Library/Caches/com.example.app")
	if err := runExplain(context.Background(), &out, &errOut, o, arg); err != nil {
		t.Fatalf("runExplain: %v (stderr %s)", err, errOut.String())
	}
	if !strings.Contains(out.String(), mac.DisplayPath(arg)) {
		t.Errorf("the block does not name the directory:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "bucket") || !strings.Contains(out.String(), "scan ") {
		t.Errorf("the block is missing its rows:\n%s", out.String())
	}
}

func TestRunExplainJSONOverAFixtureParses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("Library/Caches/com.example.app/blob.bin", 200_000)

	var out, errOut bytes.Buffer
	o := &explainOptions{roots: []string{f.Root}, scan: true, json: true}
	if err := runExplain(context.Background(), &out, &errOut, o, f.Root); err != nil {
		t.Fatalf("runExplain --json: %v (stderr %s)", err, errOut.String())
	}
	var doc struct {
		Schema int    `json:"schema"`
		Mode   string `json:"mode"`
		Path   struct {
			Path  string `json:"path"`
			Bytes int64  `json:"bytes"`
		} `json:"path"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("the output is not valid JSON: %v\n%s", err, out.String())
	}
	if doc.Mode != "path" || doc.Path.Bytes == 0 {
		t.Errorf("document = %+v", doc)
	}
}

// TestExplainScanWalksOnlyWhenItHasTo covers the freshness rule this command
// does not share with the others: an empty store is walked and said so, and
// the scan that walk stored is then reused whatever its age.
func TestExplainScanWalksOnlyWhenItHasTo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 200_000)

	var errOut bytes.Buffer
	cfg := scan.Config{Roots: []string{f.Root}, Version: BuildInfo(), FromCache: true}

	first, err := explainScan(context.Background(), &errOut, cfg, false, false)
	if err != nil {
		t.Fatalf("first explain: %v (stderr %s)", err, errOut.String())
	}
	if first.FromCache {
		t.Error("an empty store was reported as a cache hit")
	}
	if !strings.Contains(errOut.String(), "no stored scan") {
		t.Errorf("the walk was not explained on stderr: %q", errOut.String())
	}

	second, err := explainScan(context.Background(), &errOut, cfg, false, false)
	if err != nil {
		t.Fatalf("second explain: %v (stderr %s)", err, errOut.String())
	}
	if !second.FromCache {
		t.Error("the stored scan was walked again instead of being reused")
	}

	fresh, err := explainScan(context.Background(), &errOut, cfg, true, false)
	if err != nil {
		t.Fatalf("--scan: %v (stderr %s)", err, errOut.String())
	}
	if fresh.FromCache {
		t.Error("--scan reused the cache instead of walking")
	}
}

func TestExplainScanFromCacheRefusesToWalk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 1024)

	var errOut bytes.Buffer
	cfg := scan.Config{Roots: []string{f.Root}, Version: BuildInfo(), FromCache: true}
	_, err := explainScan(context.Background(), &errOut, cfg, false, true)
	if err == nil {
		t.Fatal("--from-cache walked the disk instead of failing on an empty store")
	}
	if got := ExitCode(err); got != ExitConfigError {
		t.Errorf("exit code = %d, want %d", got, ExitConfigError)
	}
}

// TestRunExplainRejectsAnOwnerNobodyHas guards the exit code of the owner
// lane end to end: a fixture has no applications, so any owner is unknown.
func TestRunExplainRejectsAnOwnerNobodyHas(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 200_000)

	var out, errOut bytes.Buffer
	o := &explainOptions{roots: []string{f.Root}, scan: true}
	err := runExplain(context.Background(), &out, &errOut, o, "com.nobody.Nothing")
	if err == nil {
		t.Fatal("an owner that cannot exist was explained anyway")
	}
	if got := ExitCode(err); got != ExitConfigError {
		t.Errorf("exit code = %d, want %d", got, ExitConfigError)
	}
}
