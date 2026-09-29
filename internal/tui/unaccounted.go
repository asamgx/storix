package tui

import "github.com/asamgx/storix/internal/report"

// newUnaccounted builds the scrollable account of what the scan could not
// see: the ledger identities, the skipped mounts, the unreadable paths, the
// cloud files, the snapshots and the hints.
func newUnaccounted() sectionModel {
	return newSection(report.Unaccounted, "the scan accounted for everything it walked")
}
