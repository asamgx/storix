package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// developerDetectors are the toolchain detectors the golden files describe.
// They run after the container detectors, which is the order the report's
// sections read in.
func developerDetectors() []detect.Status {
	return []detect.Status{
		{Name: "homebrew", State: detect.Ok, Duration: 900 * time.Millisecond, Verified: true},
		{Name: "node", State: detect.Ok, Duration: 620 * time.Millisecond, Verified: true},
		{Name: "go", State: detect.Ok, Duration: 11 * time.Millisecond, Verified: true},
		{Name: "projects", State: detect.Ok, Duration: 340 * time.Millisecond, Verified: true},
	}
}

// developerSummaries are the tool rows and projects the golden files
// describe.
//
// Three cases the section exists for are all here: a figure the tool reported
// and storix could not have computed (`brew cleanup -n`), several versions of
// one thing with only one of them live (the pnpm store), and a project whose
// build artifacts cost more than its source.
func developerSummaries(tr *walk.Tree) map[string]detect.Summary {
	build := toolAt(tr, "/Users/u/Library/Caches/go-build", detect.Tool{
		Name: "build cache", Kind: "cache", Reclaim: classify.Regenerable,
	})
	artifacts := toolAt(tr, "/Users/u/code/storix/node_modules", detect.Tool{
		Name: "node_modules", Kind: "cache", Reclaim: classify.Regenerable,
	})
	project := detect.Project{
		Root: "/Users/u/code/storix", ArtifactBytes: artifacts.Bytes, VCS: true,
		LastActivity: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
		Artifacts:    []detect.Tool{artifacts},
	}
	if n := nodeAt(tr, "/Users/u/code/storix"); n != nil {
		project.Node = n.ID
	}

	return map[string]detect.Summary{
		"homebrew": {
			Reclaimable: 148_700_000,
			ReclaimNote: "`brew cleanup -n` would free 148.7 MB (148.6 MB across 5 entries)",
		},
		"go":       {Tools: []detect.Tool{build}},
		"projects": {Projects: []detect.Project{project}},
	}
}

// toolAt fills a tool row in from the node it covers, the way detect.Claims
// does on a real scan.
func toolAt(tr *walk.Tree, display string, tool detect.Tool) detect.Tool {
	tool.Path, tool.Node = display, -1
	if n := nodeAt(tr, display); n != nil {
		tool.Path, tool.Node, tool.Bytes = n.Display(), n.ID, n.Bytes
	}
	return tool
}

// renderDeveloper is the developer section alone, which is what the TUI's
// Developer view and `storix dev` will show.
func renderDeveloper(t *testing.T, r *scan.Result) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Developer(&buf, r, Options{Units: units.Decimal}); err != nil {
		t.Fatalf("Developer: %v", err)
	}
	return buf.String()
}

func TestDeveloperSection(t *testing.T) {
	got := renderDeveloper(t, fakeScan())

	for _, want := range []string{
		"DEVELOPER",
		"homebrew",
		"brew cleanup -n` would free 148.7 MB",
		"go",
		"build cache",
		"4.2 GB",
		"projects",
		"/Users/u/code/storix",
		"last touched 2026-06-14",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the developer section does not mention %q:\n%s", want, got)
		}
	}
}

// TestDeveloperSkipsContainerTools is what keeps the two sections apart: a
// detector's tool rows are not developer rows by virtue of being tool rows,
// and OrbStack's disk image belongs in the containers section.
func TestDeveloperSkipsContainerTools(t *testing.T) {
	got := renderDeveloper(t, fakeScan())
	if strings.Contains(got, "data.img.raw") {
		t.Errorf("OrbStack's disk image is in the developer section:\n%s", got)
	}
	if strings.Contains(got, "orbstack") {
		t.Errorf("the orbstack detector has a group in the developer section:\n%s", got)
	}
}

// TestDeveloperNothingToSay: a scan whose detectors found no tools renders no
// section at all rather than an empty heading.
func TestDeveloperNothingToSay(t *testing.T) {
	r := fakeScan()
	r.Summaries = map[string]detect.Summary{}
	if got := renderDeveloper(t, r); got != "" {
		t.Errorf("a scan with no developer tools rendered:\n%s", got)
	}
}

// TestReclaimLineCountsEachByteOnce is the arithmetic the section could most
// easily get wrong. A detector lists a directory and the interesting things
// inside it, and adding those rows up would promise more free space than the
// directory holds.
func TestReclaimLineCountsEachByteOnce(t *testing.T) {
	r := fakeScan()
	store := toolAt(r.Tree, "/Users/u/code/storix", detect.Tool{
		Name: "store root", Kind: "data", Reclaim: classify.Regenerable,
	})
	inner := toolAt(r.Tree, "/Users/u/code/storix/node_modules", detect.Tool{
		Name: "generation", Kind: "versions", Reclaim: classify.Regenerable,
	})
	r.Summaries = map[string]detect.Summary{"node": {Tools: []detect.Tool{store, inner}}}
	r.Detectors = []detect.Status{{Name: "node", State: detect.Ok, Verified: true}}

	got := renderDeveloper(t, r)
	want := units.Decimal.Bytes(store.Bytes) + " across the directories listed above"
	if !strings.Contains(got, want) {
		t.Errorf("reclaim line does not read %q; the nested row was counted again:\n%s", want, got)
	}
}
