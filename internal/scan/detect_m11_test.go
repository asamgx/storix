package scan

import (
	"context"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
)

// TestRestOfTheDetectorsAreRegistered is the M11 half of what internal/detect
// cannot assert about itself: these nine register themselves from their init
// functions and only appear in a registry because detect_m11.go imports them.
//
// The assertion is on presence and relative order rather than on position,
// because the detectors ahead of them belong to other milestones and their
// count is not this test's business.
func TestRestOfTheDetectorsAreRegistered(t *testing.T) {
	names := detect.Default().Names()
	at := make(map[string]int, len(names))
	for i, n := range names {
		at[n] = i
	}

	want := []string{"xcode", "ide", "projects", "jvm", "ruby", "aimodels", "ecosystems", "nix", "backups"}
	for _, n := range want {
		if _, ok := at[n]; !ok {
			t.Errorf("the %s detector is not in the default registry: %v", n, names)
		}
	}
	for i := 1; i < len(want); i++ {
		prev, ok1 := at[want[i-1]]
		cur, ok2 := at[want[i]]
		if ok1 && ok2 && prev >= cur {
			t.Errorf("%s comes after %s; the registration order decides the report's order",
				want[i-1], want[i])
		}
	}

	// They register behind the container detectors, which the M9 test
	// asserts are the head of the list.
	if first, ok := at["xcode"]; ok && first < 6 {
		t.Errorf("xcode is at %d, ahead of the container detectors", first)
	}
}

// TestProjectsRetainsItsEvidenceThroughAScan is the wiring RetainLeaf needs:
// the hook is composed by detect.RetainLeaf from the registry and handed to
// the walker by scan.Run, and a manifest smaller than the walker's threshold
// only survives if all of that is connected.
func TestProjectsRetainsItsEvidenceThroughAScan(t *testing.T) {
	f := testutil.New(t)
	f.File("Users/andrew/code/thing/package.json", 200)
	f.File("Users/andrew/code/thing/node_modules/dep/index.js", 300_000)
	f.File("Users/andrew/code/thing/src/app.ts", 400)

	res, err := Run(context.Background(), Config{
		Roots:     []string{f.Root},
		Home:      f.Path("Users/andrew"),
		CodeRoots: []string{f.Path("Users/andrew/code")},
		NoCache:   true,
		Probe:     probe.NewReplay(nil),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	manifest := f.Path("Users/andrew/code/thing/package.json")
	if _, ok := res.Tree.Lookup(mac.ScanPath(manifest)); !ok {
		t.Fatal("the manifest was folded away, so no detector can see it")
	}
	if _, ok := res.Tree.Lookup(mac.ScanPath(f.Path("Users/andrew/code/thing/src/app.ts"))); ok {
		t.Error("an ordinary small file was retained too")
	}

	// And the detector used it: a directory with only a manifest is a
	// project, and its node_modules is build output.
	artifacts, ok := res.Tree.Lookup(mac.ScanPath(f.Path("Users/andrew/code/thing/node_modules")))
	if !ok {
		t.Fatal("node_modules is not in the tree")
	}
	cl, ok := res.Class.Of(artifacts.ID)
	if !ok {
		t.Fatal("node_modules was not classified")
	}
	if cl.Bucket != classify.BucketDeveloper || cl.Category != "Build artifacts" {
		t.Errorf("node_modules = %s / %q, want developer build artifacts", cl.Bucket, cl.Category)
	}
	if cl.Reclaim != classify.Regenerable {
		t.Errorf("node_modules reclaim = %s, want regenerable", cl.Reclaim)
	}
	if cl.Source.Detector != "projects" {
		t.Errorf("node_modules source = %s, want the projects detector", cl.Source)
	}

	sum, ok := res.Summaries["projects"]
	if !ok || len(sum.Projects) != 1 {
		t.Fatalf("projects summary = %+v, want the one project", sum.Projects)
	}
	if sum.Projects[0].ArtifactBytes < 300_000 {
		t.Errorf("artifact bytes = %d, want node_modules' own", sum.Projects[0].ArtifactBytes)
	}
}
