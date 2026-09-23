package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// classifiedResult is a fixture the classifier recognises.
//
// The browse fixture is deliberately anonymous — it lives under "/fixture" so
// no rule can match it — which is right for testing navigation and wrong for
// testing the views that exist to show what the rules decided. This one is
// laid out the way a real volume is, so the catalog matches it, the buckets
// fill, and the owner and reclaim columns have something to say.
func classifiedResult(t *testing.T) *scan.Result {
	t.Helper()
	f := testutil.New(t)
	f.File("Users/someone/Library/Caches/pnpm/store/blob-a", 4_000_000)
	f.File("Users/someone/Library/Caches/com.example.editor/blob", 1_200_000)
	f.File("Users/someone/Library/Application Support/Example/data.db", 2_200_000)
	f.File("Users/someone/Library/Preferences/com.example.editor.plist", 400)
	f.File("Users/someone/Movies/holiday.mov", 3_500_000)
	f.File("Users/someone/.Trash/old.dmg", 900_000)
	f.File("Applications/Example.app/Contents/MacOS/example", 2_800_000)
	f.File("Library/Logs/system.log", 150_000)

	tree, err := walk.Walk(context.Background(), walk.Options{Root: f.Root, Parallelism: 2})
	if err != nil {
		t.Fatalf("walk the fixture: %v", err)
	}
	// The walk rooted the tree in a temporary directory whose name changes
	// every run. Renaming the root to the data volume is what makes the
	// display paths — and so the rules, and so the goldens — stable.
	tree.Root.Name = mac.DataRoot
	tree.Started = time.Unix(1_759_000_000, 0).UTC()
	tree.Finished = tree.Started.Add(2 * time.Second)

	cfg := scan.Config{
		Roots:   []string{f.Root},
		Units:   units.Decimal,
		Version: "test",
		Home:    mac.DataRoot + "/Users/someone",
	}
	class := scan.Classify(tree, cfg)
	res := &scan.Result{
		Config: cfg,
		Tree:   tree,
		Class:  class,
		Ledger: ledger.BuildClassified(nil, tree, units.Decimal, class),
	}
	res.Detectors, res.Summaries = fixtureDetectors()
	res.Apps = fixtureApps()
	return res
}

// fixtureDetectors are two detectors with fixed facts: one toolchain for the
// developer view and one runtime for the containers view. They are built by
// hand rather than probed so the goldens do not depend on what is installed
// on the machine running the tests.
func fixtureDetectors() ([]detect.Status, map[string]detect.Summary) {
	statuses := []detect.Status{
		{Name: "node", State: detect.Ok, Duration: 12 * time.Millisecond, Verified: true},
		{Name: "orbstack", State: detect.Degraded, Reason: "the daemon is not running",
			Duration: 40 * time.Millisecond, Verified: true},
		{Name: "docker", State: detect.Missing, Reason: "no docker on PATH", Duration: 2 * time.Millisecond},
	}
	summaries := map[string]detect.Summary{
		"node": {
			Tools: []detect.Tool{
				{Name: "pnpm store", Kind: "cache", Path: "~/Library/Caches/pnpm",
					Node: -1, Bytes: 4_000_000, Reclaim: classify.ToolManaged, Version: "v10"},
			},
			Reclaimable: 4_000_000,
			ReclaimNote: "pnpm store prune",
		},
		"orbstack": {
			Runtimes: []detect.Runtime{{
				Name:          "OrbStack",
				HostImage:     []detect.Tool{{Name: "data image", Kind: "image", Path: "~/.orbstack/data.img", Node: -1, Bytes: 18_000_000}},
				GuestReported: []detect.Line{{Type: "Images", Size: 12_000_000, Reclaimable: 5_000_000, Count: 9, Known: true}},
				Machines:      []string{"docker"},
				Note:          "the image is sparse; the daemon counts what is inside it",
			}},
		},
	}
	return statuses, summaries
}

// fixtureApps is a small inventory with one of each state the Apps view draws.
func fixtureApps() *apps.Report {
	return &apps.Report{
		Schema: apps.ReportSchema,
		Counts: apps.Counts{Bundles: 2, Casks: 2, Owners: 4, Candidates: 3},
		Apps: []apps.Entry{
			{
				Owner: "app:com.example.editor", Label: "Example Editor", State: "installed",
				Confidence: "strong",
				Footprint:  apps.Sizes{Bundle: 2_800_000, Data: 2_200_000, Caches: 1_200_000, Total: 6_200_000},
				Sources:    []string{"bundle", "cask"},
				Evidence:   []string{"/Applications/Example.app has bundle id com.example.editor"},
				Components: []apps.ComponentRef{
					{Path: "/Applications/Example.app", Bytes: 2_800_000, Bucket: "apps"},
					{Path: "/Users/someone/Library/Application Support/Example", Bytes: 2_200_000, Bucket: "app-data"},
				},
			},
		},
		CaskOnly: []apps.Entry{{
			Owner: "cask:ghostwriter", Label: "Ghostwriter", State: "cask-only", Confidence: "likely",
			Footprint: apps.Sizes{Total: 0},
			Evidence:  []string{"the cask receipt expects /Applications/Ghostwriter.app, which is not there"},
		}},
		Orphans: []apps.Entry{{
			Owner: "vendor:com.gone", Label: "Gone Software", State: "orphan-likely", Confidence: "likely",
			Footprint: apps.Sizes{Data: 900_000, Total: 900_000},
			LastWrite: time.Unix(1_700_000_000, 0).UTC(),
			Evidence:  []string{"no bundle with this vendor prefix is on the volume"},
			Keep:      []string{"a launch agent still references it"},
			Components: []apps.ComponentRef{
				{Path: "/Users/someone/Library/Application Support/Gone", Bytes: 900_000, Bucket: "app-data"},
			},
		}},
		Unknown: []apps.Entry{{
			Owner: "unknown:com.todesktop.230313", Label: "com.todesktop.230313", State: "unknown",
			Confidence: "unknown",
			Footprint:  apps.Sizes{Caches: 1_200_000, Total: 1_200_000},
			Components: []apps.ComponentRef{
				{Path: "/Users/someone/Library/Caches/com.example.editor", Bytes: 1_200_000, Bucket: "app-data"},
			},
		}},
	}
}

// newClassifiedModel builds the interface over the classified fixture.
func newClassifiedModel(t *testing.T) *Model {
	t.Helper()
	return newTestModel(t, classifiedResult(t))
}

// TestAFinishedScanLandsOnTheLedger is R6's first sentence: the view a reader
// is given is the one that answers "what is on this disk".
func TestAFinishedScanLandsOnTheLedger(t *testing.T) {
	m := newClassifiedModel(t)
	if m.view != viewLedger {
		t.Errorf("a result opened on view %d, want the ledger", m.view)
	}
}

// TestLedgerViewShowsTheTwelveBuckets goldens the bucket table.
func TestLedgerViewShowsTheTwelveBuckets(t *testing.T) {
	m := newClassifiedModel(t)
	tm := start(t, m)
	waitFor(t, tm, "LEDGER")

	final := finish(t, tm)
	if got := len(final.ledger.buckets()); got != 12 {
		t.Errorf("the ledger view has %d rows, want the twelve buckets", got)
	}
	teatest.RequireEqualOutput(t, screen(final))
}

// TestLedgerDrillsIntoABucketAndJumpsToBrowse is the path from a bucket to a
// directory: enter opens the bucket's roots, enter again opens the browser at
// the one under the cursor, and backspace comes back.
func TestLedgerDrillsIntoABucketAndJumpsToBrowse(t *testing.T) {
	m := newClassifiedModel(t)
	m.w, m.h = termWidth, termHeight
	m.resize()

	bucket, ok := findBucket(t, &m.ledger, classify.BucketAppData)
	if !ok {
		t.Skip("the fixture produced no application-data bucket to drill into")
	}
	m.ledger.moveTo(bucket)
	if _, jumped := m.ledger.open(); jumped {
		t.Fatal("the first enter on a bucket opened the browser rather than the bucket")
	}
	if !m.ledger.drilling() {
		t.Fatal("enter on a bucket did not open its roots")
	}
	if len(m.ledger.roots) == 0 {
		t.Fatal("the application-data bucket drilled into an empty list")
	}
	teatest.RequireEqualOutput(t, []byte(m.ledger.View(m.st, units.Decimal)))

	n, jumped := m.ledger.open()
	if !jumped || n == nil {
		t.Fatal("enter on a root did not offer a node to open")
	}
	m.openInBrowse(n)
	if m.view != viewBrowse {
		t.Errorf("opening a root left the interface on view %d, want the browser", m.view)
	}
	if m.browse.dir != n.Parent {
		t.Errorf("the browser opened %v, want the directory holding %s", m.browse.dir, n.Name)
	}
	m.view = viewLedger
	// The why panel answers for whichever list is in front: the bucket in
	// the table, the path in the drill-down.
	if c := m.whyContent(); c.title != mac.DisplayPath(m.ledger.roots[0].node.Path()) {
		t.Errorf("over the drill list the panel is headed %q, want the selected path", c.title)
	}
	if !m.ledger.back() {
		t.Error("backspace did not return to the buckets")
	}
	if m.ledger.drilling() {
		t.Error("the drill-down survived backspace")
	}
	b, _, _ := m.ledger.selectedBucket()
	if c := m.whyContent(); c.title != b.Label {
		t.Errorf("over the bucket table the panel is headed %q, want %q", c.title, b.Label)
	}
}

// findBucket is the row index of one bucket in the ledger table.
func findBucket(t *testing.T, m *ledgerModel, want classify.Bucket) (int, bool) {
	t.Helper()
	for i := range m.buckets() {
		if bucketAt(i) == want && m.buckets()[i].Bytes > 0 {
			return i, true
		}
	}
	return 0, false
}

// TestDeveloperViewRendersTheSection goldens the developer viewport.
func TestDeveloperViewRendersTheSection(t *testing.T) {
	m := newClassifiedModel(t)
	tm := start(t, m)
	waitFor(t, tm, "LEDGER")
	press(tm, "4")
	waitFor(t, tm, "DEVELOPER")

	final := finish(t, tm)
	if final.view != viewDeveloper {
		t.Fatalf("4 left the interface on view %d", final.view)
	}
	teatest.RequireEqualOutput(t, screen(final))
}

// TestContainersViewRendersTheSection goldens the containers viewport.
func TestContainersViewRendersTheSection(t *testing.T) {
	m := newClassifiedModel(t)
	tm := start(t, m)
	waitFor(t, tm, "LEDGER")
	press(tm, "5")
	waitFor(t, tm, "CONTAINERS")

	final := finish(t, tm)
	if final.view != viewContainers {
		t.Fatalf("5 left the interface on view %d", final.view)
	}
	teatest.RequireEqualOutput(t, screen(final))
}

// TestAppsViewListsFootprintsAndOrphans goldens the application inventory:
// the installed table and the owners whose software storix cannot find.
func TestAppsViewListsFootprintsAndOrphans(t *testing.T) {
	m := newClassifiedModel(t)
	tm := start(t, m)
	waitFor(t, tm, "LEDGER")
	press(tm, "3")
	waitFor(t, tm, "NEEDS ATTENTION")

	final := finish(t, tm)
	if final.view != viewApps {
		t.Fatalf("3 left the interface on view %d", final.view)
	}
	teatest.RequireEqualOutput(t, screen(final))
}

// TestAppsEnterOpensTheLargestComponent is A6's navigation rule.
func TestAppsEnterOpensTheLargestComponent(t *testing.T) {
	m := newClassifiedModel(t)
	m.w, m.h = termWidth, termHeight
	m.view = viewApps
	m.resize()

	e, ok := m.apps.selected()
	if !ok {
		t.Fatal("the applications view has no selectable row")
	}
	if e.Label != "Example Editor" {
		t.Fatalf("the cursor opened on %q, want the largest application", e.Label)
	}
	n, ok := m.apps.open()
	if !ok {
		t.Fatal("enter found no node for the largest component")
	}
	if got := mac.DisplayPath(n.Path()); got != "/Applications/Example.app" {
		t.Errorf("enter opened %s, want the application bundle", got)
	}
}

// TestWhyPanelShowsEvidenceInBrowse goldens the panel beside the browser and
// checks the two facts it exists to state: which rule or detector claimed the
// path, and where an inherited claim came from.
func TestWhyPanelShowsEvidenceInBrowse(t *testing.T) {
	m := newClassifiedModel(t)
	tm := start(t, m)
	waitFor(t, tm, "LEDGER")
	press(tm, "2")
	waitFor(t, tm, "Users/")
	press(tm, "w")
	waitFor(t, tm, "WHY")

	final := finish(t, tm)
	if !final.why.on {
		t.Fatal("w did not open the why panel")
	}
	if !final.whyBeside() {
		t.Fatalf("at %d columns the panel should sit beside the view", final.w)
	}
	teatest.RequireEqualOutput(t, screen(final))
}

// TestWhyPanelNamesTheSourceAndTheInheritance is the panel's content rather
// than its pixels: a claimed node names its source, and a child that carries
// no claim of its own says whose it inherited.
func TestWhyPanelNamesTheSourceAndTheInheritance(t *testing.T) {
	res := classifiedResult(t)
	n, ok := res.Tree.Lookup(mac.ScanPath("/Users/someone/Library/Caches/pnpm"))
	if !ok {
		t.Fatal("the fixture has no pnpm cache")
	}
	cl, ok := res.Class.OfNode(n)
	if !ok {
		t.Skip("the catalog claimed nothing at the pnpm cache")
	}
	c := nodeWhy(res, n, units.Decimal)
	if !hasField(c, "source", cl.Source.String()) {
		t.Errorf("the panel does not name the source %q: %+v", cl.Source.String(), c.fields)
	}
	child := n.Children
	if len(child) == 0 {
		t.Skip("the pnpm cache has no child to inherit from")
	}
	cc := nodeWhy(res, child[0], units.Decimal)
	if from := res.Class.InheritedFromNode(child[0]); from != nil {
		if !hasFieldLabel(cc, "inherited") {
			t.Errorf("a child inheriting from %s has no inherited line: %+v", from.Path(), cc.fields)
		}
	}
}

// TestWhyPanelWarnsAboutADegradedDetector is the panel's honesty rule: a
// claim from a detector that did not run cleanly says so.
func TestWhyPanelWarnsAboutADegradedDetector(t *testing.T) {
	res := classifiedResult(t)
	note, ok := detectorNote(res, "orbstack")
	if !ok {
		t.Fatal("a degraded detector produced no note")
	}
	if want := "the daemon is not running"; !strings.Contains(note, want) {
		t.Errorf("the note is %q, want it to carry %q", note, want)
	}
	if _, ok := detectorNote(res, "node"); ok {
		t.Error("a healthy detector produced a warning")
	}
}

// TestWhyPanelSitsUnderANarrowView is the other half of the layout rule.
func TestWhyPanelSitsUnderANarrowView(t *testing.T) {
	m := newClassifiedModel(t)
	m.w, m.h = 90, termHeight
	m.why.on = true
	m.resize()
	if m.whyBeside() {
		t.Error("at 90 columns the panel should sit under the view, not beside it")
	}
	if m.bodyWidth() != 90 {
		t.Errorf("the view is %d columns wide, want the whole 90", m.bodyWidth())
	}
	if m.bodyHeight() >= termHeight-3 {
		t.Errorf("the view kept %d lines; the panel under it should have taken some", m.bodyHeight())
	}
}

// TestViewKeysReachEveryView is the renumbering of R6, plus the alias and the
// cycle.
func TestViewKeysReachEveryView(t *testing.T) {
	m := newClassifiedModel(t)
	m.w, m.h = termWidth, termHeight
	m.resize()

	for _, c := range []struct {
		key  string
		want view
	}{
		{"1", viewLedger}, {"2", viewBrowse}, {"3", viewApps},
		{"4", viewDeveloper}, {"5", viewContainers}, {"6", viewUnaccounted},
		{"u", viewUnaccounted}, {"1", viewLedger},
	} {
		sendKey(m, c.key)
		if m.view != c.want {
			t.Errorf("%q reached view %d, want %d", c.key, m.view, c.want)
		}
	}

	// tab cycles the six in order and wraps.
	m.view = viewLedger
	for _, want := range append(resultViews[1:], resultViews[0]) {
		m.toggleView()
		if m.view != want {
			t.Fatalf("tab reached view %d, want %d", m.view, want)
		}
	}
}

// TestChipsDropBelowOneHundredColumns is the layout rule the browse table
// follows: owner and reclaimability are shown when there is room and left to
// the why panel when there is not.
func TestChipsDropBelowOneHundredColumns(t *testing.T) {
	wide := layout(120)
	if wide.owner != ownerWidth || wide.reclaim != reclaimWidth {
		t.Errorf("at 120 columns the layout has owner=%d reclaim=%d, want both", wide.owner, wide.reclaim)
	}
	narrow := layout(90)
	if narrow.owner != 0 || narrow.reclaim != 0 {
		t.Errorf("at 90 columns the layout kept owner=%d reclaim=%d, want neither", narrow.owner, narrow.reclaim)
	}
	// One column either side of the threshold: the same terminal keeps the
	// width the chips would have taken and gives it to the name.
	with, without := layout(chipsMinWidth), layout(chipsMinWidth-1)
	if without.name <= with.name {
		t.Errorf("dropping the chips did not give the name column back its width: %d vs %d", without.name, with.name)
	}
}

// TestBrowseRowsCarryTheClassification is the join the chips depend on.
func TestBrowseRowsCarryTheClassification(t *testing.T) {
	res := classifiedResult(t)
	b := newBrowse(res.Tree.Root)
	b.setClass(res.Class)
	b.setSize(termWidth, termHeight-4)

	var claimed int
	for _, r := range b.visible() {
		if r.classified {
			claimed++
		}
	}
	if claimed == 0 {
		t.Error("no row at the root carries a claim; the owner and tag columns would be empty")
	}
}

// TestTheRowCacheFollowsANewClassification is the cache rule: rowOpts is
// still the key, so a new classification has to drop the rows itself.
func TestTheRowCacheFollowsANewClassification(t *testing.T) {
	res := classifiedResult(t)
	b := newBrowse(res.Tree.Root)
	b.setSize(termWidth, termHeight-4)
	before := b.visible()
	for _, r := range before {
		if r.classified {
			t.Fatal("a browser with no classification produced a classified row")
		}
	}
	b.setClass(res.Class)
	if b.valid {
		t.Fatal("a new classification left the cached rows in place")
	}
	var claimed int
	for _, r := range b.visible() {
		if r.classified {
			claimed++
		}
	}
	if claimed == 0 {
		t.Error("the rows were not rebuilt against the new classification")
	}
}

// sendKey drives one key straight into the model, for the tests that check
// state rather than pixels.
func sendKey(m *Model, s string) {
	for _, r := range s {
		m.key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func hasField(c whyContent, label, value string) bool {
	for _, f := range c.fields {
		if f.label == label && f.value == value {
			return true
		}
	}
	return false
}

func hasFieldLabel(c whyContent, label string) bool {
	for _, f := range c.fields {
		if f.label == label {
			return true
		}
	}
	return false
}

// TestAppsHeaderFollowsTheCursor is the column-name rule of the two-table
// view: the header does not scroll, so it has to say which table it names.
func TestAppsHeaderFollowsTheCursor(t *testing.T) {
	m := newClassifiedModel(t)
	m.w, m.h = termWidth, termHeight
	m.view = viewApps
	m.resize()
	c := appsLayout(m.apps.width)

	if got := stripStyles(m.apps.columnsLine(m.st, c)); !strings.Contains(got, "bundle") {
		t.Errorf("over the installed table the header reads %q, want the footprint columns", got)
	}
	for m.apps.cursorSection() != sectionAttention {
		before := m.apps.cursor
		m.apps.move(1)
		if m.apps.cursor == before {
			t.Fatal("the cursor never reached the attention table")
		}
	}
	got := stripStyles(m.apps.columnsLine(m.st, c))
	if !strings.Contains(got, "last write") || !strings.Contains(got, "state") {
		t.Errorf("over the attention table the header reads %q, want its own columns", got)
	}
}

// TestWhyPanelExplainsAnApplication is the Apps half of the shared panel:
// owner, state, confidence, evidence, the signals that argue for keeping the
// data, and the components with the buckets they were counted in.
func TestWhyPanelExplainsAnApplication(t *testing.T) {
	m := newClassifiedModel(t)
	m.w, m.h = termWidth, termHeight
	m.view = viewApps
	m.why.on = true
	m.resize()

	// The orphan is the row with something to argue about.
	for {
		e, ok := m.apps.selected()
		if !ok {
			t.Fatal("no row in the applications view")
		}
		if e.State == "orphan-likely" {
			break
		}
		before := m.apps.cursor
		m.apps.move(1)
		if m.apps.cursor == before {
			t.Fatal("the fixture has no orphan to explain")
		}
	}

	c := m.whyContent()
	if c.title != "Gone Software" {
		t.Errorf("the panel is headed %q, want the owner's label", c.title)
	}
	for _, f := range []whyField{
		{"state", "orphan-likely"},
		{"confidence", "likely"},
	} {
		if !hasField(c, f.label, f.value) {
			t.Errorf("the panel has no %s = %q: %+v", f.label, f.value, c.fields)
		}
	}
	if len(c.evidence) == 0 {
		t.Error("the panel states an accusation with no evidence under it")
	}
	if !hasSection(c, "keep signals") {
		t.Error("the panel drops the signals that argue for keeping the data")
	}
	if !hasSection(c, "components") {
		t.Fatal("the panel lists no components")
	}
	got := sectionEntries(c, "components")
	if len(got) == 0 || !strings.Contains(got[0].text, classify.BucketAppData.Label()) {
		t.Errorf("a component reads %v, want the bucket's label", got)
	}
	if len(got) > 0 && strings.Contains(got[0].text, classify.BucketAppData.ID()) {
		t.Errorf("a component reads %q, want the label rather than the raw id", got[0].text)
	}
	if len(got) > 0 && got[0].path == "" {
		t.Error("a component carries no path")
	}
}

func hasSection(c whyContent, title string) bool {
	for _, s := range c.sections {
		if s.title == title {
			return true
		}
	}
	return false
}

func sectionEntries(c whyContent, title string) []whyEntry {
	for _, s := range c.sections {
		if s.title == title {
			return s.entries
		}
	}
	return nil
}
