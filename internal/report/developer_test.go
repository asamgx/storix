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
	want := units.Decimal.Bytes(store.Bytes) + " can be freed outright, across the directories listed above"
	if !strings.Contains(got, want) {
		t.Errorf("reclaim line does not read %q; the nested row was counted again:\n%s", want, got)
	}
}

// TestReclaimLineKeepsToolManagedApart is D43 in the developer section. A pnpm
// store root is tool-managed as a whole, its live generation too, and only a
// superseded generation inside it can be freed outright. The line summed every
// reclaimable-tagged outermost row, so the live store — and a default Rust
// toolchain — read as space to give back.
func TestReclaimLineKeepsToolManagedApart(t *testing.T) {
	r := fakeScan()
	root := toolAt(r.Tree, "/Users/u/code/storix", detect.Tool{
		Name: "store root", Kind: "data", Reclaim: classify.ToolManaged,
	})
	old := toolAt(r.Tree, "/Users/u/code/storix/node_modules", detect.Tool{
		Name: "superseded generation", Kind: "versions", Reclaim: classify.Regenerable,
	})
	r.Summaries = map[string]detect.Summary{"node": {Tools: []detect.Tool{root, old}}}
	r.Detectors = []detect.Status{{Name: "node", State: detect.Ok, Verified: true}}

	got := renderDeveloper(t, r)
	u := units.Decimal
	want := u.Bytes(old.Bytes) + " can be freed outright and " + u.Bytes(root.Bytes-old.Bytes) +
		" more only through the tool"
	if !strings.Contains(got, want) {
		t.Errorf("reclaim line does not read %q:\n%s", want, got)
	}
}

func TestReclaimSharesCountEachByteAtItsDeepestRow(t *testing.T) {
	tools := []detect.Tool{
		{Path: "/a", Bytes: 100, Reclaim: classify.ToolManaged},
		{Path: "/a/live", Bytes: 60, Reclaim: classify.ToolManaged},
		{Path: "/a/old", Bytes: 30, Reclaim: classify.Regenerable},
		{Path: "/a/old", Bytes: 30, Reclaim: classify.Regenerable},
		{Path: "/ab", Bytes: 7, Reclaim: classify.Orphaned},
		{Path: "/c", Bytes: 5, Reclaim: classify.UserData},
	}
	free, viaTool := reclaimShares(tools)
	if free != 37 || viaTool != 70 {
		t.Errorf("reclaimShares = %d free, %d via tool; want 37 and 70", free, viaTool)
	}
}

// TestProjectsWithoutArtifactsAreCountedNotListed: on the reference machine
// fifteen of twenty-two project rows read "0 bytes build artifacts" and
// pushed the ones worth acting on apart. They are summed into one line.
func TestProjectsWithoutArtifactsAreCountedNotListed(t *testing.T) {
	r := fakeScan()
	sum := r.Summaries["projects"]
	for _, name := range []string{"clean-one", "clean-two"} {
		sum.Projects = append(sum.Projects, detect.Project{Root: "/Users/u/code/" + name, VCS: true})
	}
	r.Summaries["projects"] = sum

	got := renderDeveloper(t, r)
	if strings.Contains(got, "/Users/u/code/clean-one") {
		t.Errorf("a project with no build output is listed:\n%s", got)
	}
	if !strings.Contains(got, "2 more projects have no build output") {
		t.Errorf("the projects without build output are not counted:\n%s", got)
	}
	if !strings.Contains(got, "/Users/u/code/storix") {
		t.Errorf("the project with build artifacts is missing:\n%s", got)
	}
}
