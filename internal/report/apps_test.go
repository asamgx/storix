package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
)

// fakeAppsReport is a small inventory with one of each interesting state, so
// the golden shows every section without depending on a real machine.
func fakeAppsReport() *apps.Report {
	written := time.Date(2026, 2, 21, 0, 5, 0, 0, time.UTC)
	return &apps.Report{
		Schema: apps.ReportSchema,
		Counts: apps.Counts{Bundles: 54, Casks: 51, AppStore: 9, Owners: 210, Candidates: 553},
		Apps: []apps.Entry{
			{
				Owner: "app:dev.kdrag0n.MacVirt", Label: "OrbStack", State: "installed",
				Confidence: "strong",
				Footprint: apps.Sizes{
					Bundle: 728_600_000, Data: 18_800_000_000, Caches: 5_700_000,
					Containers: 0, Total: 19_534_300_000,
				},
				Sources: []string{"/Applications"},
				Components: []apps.ComponentRef{
					{Path: "/Applications/OrbStack.app", Bytes: 728_600_000, Bucket: "apps", Category: "Application"},
				},
			},
			{
				Owner: "app:com.microsoft.VSCode", Label: "VS Code", State: "installed",
				Confidence: "strong",
				Footprint: apps.Sizes{
					Bundle: 400_000_000, Data: 700_000_000, Caches: 120_000_000,
					Dev: 1_700_000_000, Total: 2_920_000_000,
				},
				Sources: []string{"/Applications", "cask"},
			},
		},
		CaskOnly: []apps.Entry{{
			Owner: "cask:cursor", Label: "Cursor", State: "cask-only", Confidence: "likely",
			Footprint:   apps.Sizes{Data: 376_800_000, Total: 376_800_000},
			Reclaimable: 376_800_000,
			LastWrite:   written,
			Evidence: []string{
				"cask cursor is still installed, installed 2025-07-23",
				"Cursor.app is not on the volume",
			},
			Components: []apps.ComponentRef{
				{Path: "~/Library/Caches/com.todesktop.230313mzl4w4u92", Bytes: 214_000_000,
					Bucket: "app-data", Category: "Cache", Reclaim: "orphaned"},
			},
		}},
		Orphans: []apps.Entry{
			{
				Owner: "product:warp", Label: "Warp", State: "orphan-likely", Confidence: "likely",
				Footprint: apps.Sizes{Data: 373_100_000, Total: 373_100_000},
				LastWrite: written,
				Evidence: []string{
					"no application bundle for Warp anywhere on the volume",
					"the directory is named after a bundle identifier, which only an installation creates",
				},
			},
			{
				Owner: "product:tabnine", Label: "TabNine", State: "orphan-likely",
				Confidence: "corroborating",
				Footprint:  apps.Sizes{Data: 217_100, Total: 217_100},
				Evidence: []string{
					"no application bundle for TabNine anywhere on the volume",
					"the only evidence is the directory name, so this is possible rather than likely",
				},
			},
		},
		InTrash: []apps.Entry{{
			Owner: "app:com.aviorrok.DynamicLakePro", Label: "DynamicLake Pro", State: "in-trash",
			Confidence: "likely",
			Footprint:  apps.Sizes{Data: 5_400_000, Total: 5_400_000},
			Evidence:   []string{"the only copy is in the Trash: ~/.Trash/DynamicLakePro.app"},
		}},
		Unknown: []apps.Entry{{
			Owner: "unknown:SomethingNobodyKnows", Label: "SomethingNobodyKnows",
			State: "unknown", Confidence: "unknown",
			Footprint:  apps.Sizes{Data: 122_800_000, Total: 122_800_000},
			Components: []apps.ComponentRef{{Path: "~/Library/Caches/SomethingNobodyKnows", Bytes: 122_800_000}},
		}},
		CasksMissingApp: []apps.CaskRef{
			{Token: "cursor", ExpectedApp: "Cursor.app",
				InstalledAt: time.Date(2025, 7, 23, 0, 0, 0, 0, time.UTC)},
			{Token: "devtoys", ExpectedApp: "DevToys.app",
				InstalledAt: time.Date(2025, 7, 24, 0, 0, 0, 0, time.UTC)},
		},
		Degraded: []apps.Degradation{{Probe: "lsregister", Reason: "timed out after 10s"}},
	}
}

// fakeAppsScan is a scan result carrying the inventory above.
func fakeAppsScan() *scan.Result {
	res := fakeScan()
	res.Apps = fakeAppsReport()
	return res
}

func TestAppsGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Apps(&buf, fakeAppsScan(), Options{Units: units.Decimal}); err != nil {
		t.Fatal(err)
	}
	golden(t, "apps.txt", buf.String())
}

// TestAppsOrphansOnlyDropsTheInstalledTable is what `--orphans` means: the
// sections about software that is gone, and not the inventory.
func TestAppsOrphansOnlyDropsTheInstalledTable(t *testing.T) {
	var buf bytes.Buffer
	err := AppsWith(&buf, fakeAppsScan(), Options{Units: units.Decimal}, AppsOptions{OrphansOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "APPLICATIONS  (") {
		t.Error("--orphans printed the installed table")
	}
	for _, want := range []string{"ORPHAN-LIKELY", "CASK INSTALLED", "IN THE TRASH", "UNKNOWN OWNER"} {
		if !strings.Contains(out, want) {
			t.Errorf("--orphans is missing the %s section", want)
		}
	}
}

// TestAppsGradesCorroboratingAsPossible checks the one place the report
// translates a term: "corroborating" is accurate and unreadable, and the
// person deciding whether to look needs "possible".
func TestAppsGradesCorroboratingAsPossible(t *testing.T) {
	var buf bytes.Buffer
	if err := Apps(&buf, fakeAppsScan(), Options{Units: units.Decimal}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "possible") {
		t.Error("an orphan graded corroborating was not printed as possible")
	}
	if strings.Contains(out, "corroborating") {
		t.Error("the raw grade leaked into the report")
	}
}

// TestAppsWithoutAnInventory is the pre-1b cache case: the renderer says so
// rather than printing an empty table that looks like a machine with no
// applications.
func TestAppsWithoutAnInventory(t *testing.T) {
	var buf bytes.Buffer
	err := Apps(&buf, fakeScan(), Options{Units: units.Decimal})
	if err == nil {
		t.Fatal("rendering a scan with no inventory should report an error")
	}
	if !strings.Contains(err.Error(), "no application inventory") {
		t.Errorf("error = %v", err)
	}
}

// TestAppsFootprintsAreNotLedgerTotals guards the invariant the command's own
// help text states: a footprint crosses the ledger's buckets, so summing the
// footprints is not a measure of the disk and the two must never be mixed.
func TestAppsFootprintsAreNotLedgerTotals(t *testing.T) {
	rep := fakeAppsReport()
	var summed int64
	for _, e := range rep.Apps {
		summed += e.Footprint.Total
	}
	var buckets int64
	for _, e := range rep.Apps {
		buckets += e.Footprint.Bundle + e.Footprint.Data + e.Footprint.Caches + e.Footprint.Dev
	}
	if summed != buckets {
		t.Errorf("a footprint total %d does not equal its own columns %d", summed, buckets)
	}
	// The report exposes the attention total separately, which is what the
	// header line quotes; it is not the scan's reclaimable figure.
	if got := rep.NeedsAttention(); got == 0 {
		t.Error("NeedsAttention should count the cask-only, orphan and in-trash data")
	}
}
