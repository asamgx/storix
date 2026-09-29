package report

import (
	"bytes"
	"strings"
	"syscall"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/classify/catalog"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// headlineScan is a volume whose Developer bucket holds 1 GB of build cache
// (regenerable) beside 7 GB of Homebrew Cellar (tool-managed), and whose
// Trash and device backups could not be read without Full Disk Access.
func headlineScan(t *testing.T) *scan.Result {
	t.Helper()
	root := dir(mac.DataRoot,
		dir("opt", dir("homebrew", dir("Cellar", dir("go", node("go", 7_000_000_000, 7_000_000_000))))),
		dir("Users", dir("u",
			dir(".Trash"),
			dir("Documents", node("thesis.pdf", 2_000_000, 2_000_000)),
			dir("Library",
				dir("Caches", dir("go-build", node("trim.txt", 1_000_000_000, 1_000_000_000))),
				dir("Application Support", dir("MobileSync")),
			),
		)),
	)
	tr := fillTree(root)
	tr.Errors = []walk.PathError{
		{Path: mac.DataRoot + "/Users/u/.Trash", Op: "readdir", Errno: syscall.EPERM, Class: walk.ErrTCC},
		{Path: mac.DataRoot + "/Users/u/Library/Application Support/MobileSync", Op: "readdir", Errno: syscall.EPERM, Class: walk.ErrTCC},
	}
	e, err := classify.New(catalog.Rules(), classify.Context{Home: "/Users/u", CodeRoots: []string{}})
	if err != nil {
		t.Fatalf("the catalog does not compile: %v", err)
	}
	class := e.Run(tr, nil)
	f := fakeFacts()
	return &scan.Result{Facts: f, Tree: tr, Class: class, Ledger: ledger.BuildClassified(f, tr, units.Decimal, class)}
}

// ledgerLine is the LEDGER row whose label ends the line.
func ledgerLine(t *testing.T, text, label string) string {
	t.Helper()
	_, section, ok := strings.Cut(text, "LEDGER")
	section, _, found := strings.Cut(section, "identity")
	if !ok || !found {
		t.Fatalf("no LEDGER section in:\n%s", text)
	}
	for _, line := range strings.Split(section, "\n") {
		if strings.HasSuffix(line, "  "+label) {
			return line
		}
	}
	t.Fatalf("no LEDGER row for %q in:\n%s", label, section)
	return ""
}

// TestLedgerHeadlineNeverCountsToolManaged is D43: the reclaimable figure is
// what could be freed outright. The Cellar is freed by `brew cleanup`, which
// keeps every formula still installed, so its bytes are printed as a figure
// of their own and never added to the regenerable ones.
func TestLedgerHeadlineNeverCountsToolManaged(t *testing.T) {
	r := headlineScan(t)
	dev := r.Ledger.Buckets[int(classify.BucketDeveloper)-1]
	if dev.Reclaimable != 1_000_000_000 || dev.ToolManaged != 7_000_000_000 {
		t.Fatalf("developer reclaimable = %d, tool-managed = %d; want 1 GB and 7 GB", dev.Reclaimable, dev.ToolManaged)
	}

	var buf bytes.Buffer
	if err := Text(&buf, r, Options{Units: units.Decimal}); err != nil {
		t.Fatal(err)
	}
	row := ledgerLine(t, buf.String(), "Developer")
	if strings.Contains(row, "free") {
		t.Errorf("the Developer row adds tool-managed bytes into its headline:\n%s", row)
	}
	fields := strings.Fields(row)
	// bar, size, unit, %, reclaimable, unit, via tool, unit, number, label
	if len(fields) < 9 || fields[4] != "1" || fields[6] != "7" {
		t.Errorf("the Developer row does not show 1 GB reclaimable and 7 GB via its tool apart:\n%s", row)
	}
	if !strings.Contains(buf.String(), "reclaimable") || !strings.Contains(buf.String(), "via tool") {
		t.Errorf("the LEDGER table does not name its reclaim columns:\n%s", buf.String())
	}
}

// TestLedgerUnreadableBucketsReadUnknown: a Trash the walk could not open is
// not an empty Trash, and the row says so rather than "0 bytes".
func TestLedgerUnreadableBucketsReadUnknown(t *testing.T) {
	var buf bytes.Buffer
	if err := Text(&buf, headlineScan(t), Options{Units: units.Decimal}); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Trash", "Backups"} {
		row := ledgerLine(t, buf.String(), label)
		if !strings.Contains(row, "unknown") || strings.Contains(row, "0 bytes") {
			t.Errorf("the %s row claims a size the walk never saw:\n%s", label, row)
		}
	}
	flat := strings.Join(strings.Fields(buf.String()), " ")
	if !strings.Contains(flat, "Trash — unknown") || !strings.Contains(flat, "without Full Disk Access") {
		t.Errorf("the BUCKETS section does not explain the unknown Trash:\n%s", buf.String())
	}
}
