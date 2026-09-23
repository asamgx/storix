package tui

import "github.com/asamgx/storix/internal/report"

// newDeveloper builds the scrollable developer section: what each toolchain
// keeps on disk, grouped by the detector that found it, and what the code
// roots on this machine have built.
func newDeveloper() sectionModel {
	return newSection(report.Developer, "no toolchain or code root was found on this machine")
}
