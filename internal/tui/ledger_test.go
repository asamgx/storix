package tui

import (
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
)

// headlineLedger is a twelve-row ledger whose Developer bucket holds 1 GB
// that can be freed outright beside 7 GB only its tools can free, and whose
// Trash could not be read.
func headlineLedger() *ledger.Ledger {
	l := &ledger.Ledger{Units: units.Decimal, Scanned: ledger.Line{Bytes: 8_000_000_000, Known: true}}
	for _, b := range classify.Buckets() {
		l.Buckets = append(l.Buckets, ledger.Bucket{ID: b.ID(), Label: b.Label(), Known: true})
	}
	dev := &l.Buckets[int(classify.BucketDeveloper)-1]
	dev.Bytes, dev.Reclaimable, dev.ToolManaged = 8_000_000_000, 1_000_000_000, 7_000_000_000
	trash := &l.Buckets[int(classify.BucketTrash)-1]
	trash.Known, trash.Note = false, "could not be read without Full Disk Access"
	return l
}

// TestLedgerRowKeepsToolManagedOutOfReclaim is D43 in the TUI: the reclaim
// column and its bar show only what can be freed outright, and the
// tool-managed bytes have a column of their own.
func TestLedgerRowKeepsToolManagedOutOfReclaim(t *testing.T) {
	m := newLedger()
	m.setSize(160, 14)
	m.setResult(&scan.Result{Ledger: headlineLedger()})
	view := stripStyles(m.View(NewStyles(false, true), units.Decimal))

	var dev, trash, head string
	for _, line := range strings.Split(view, "\n") {
		switch {
		case strings.Contains(line, " 4 Developer"):
			dev = line
		case strings.Contains(line, " 9 Trash"):
			trash = line
		case strings.HasPrefix(strings.TrimSpace(line), "bucket"):
			head = line
		}
	}
	if strings.Contains(head, "free") || !strings.Contains(head, "reclaim") || !strings.Contains(head, "via tool") {
		t.Errorf("the column header does not name the two reclaim figures:\n%s", head)
	}
	f := strings.Fields(dev)
	// label (2), size (2), %, reclaim bar, reclaim (2), via tool (2)
	if len(f) < 10 || f[len(f)-4] != "1.0" || f[len(f)-2] != "7.0" {
		t.Errorf("the Developer row does not show 1 GB to reclaim and 7 GB via its tool apart:\n%s", dev)
	}
	if !strings.Contains(trash, "unknown") {
		t.Errorf("the unreadable Trash row does not say unknown:\n%s", trash)
	}
}
