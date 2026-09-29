package tui

import "github.com/asamgx/storix/internal/report"

// newContainers builds the scrollable containers section: per runtime, what
// the host has given the disk image against what the runtime's own daemon
// believes it is using, and the table of detector states behind both.
func newContainers() sectionModel {
	return newSection(report.Containers, "no container or virtual-machine runtime was found")
}
