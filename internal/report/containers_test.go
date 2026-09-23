package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// fakeDetectors is the detectors table the golden files describe: the run
// this machine really produces, with one of each interesting state.
func fakeDetectors() []detect.Status {
	return []detect.Status{
		{
			Name: "orbstack", State: detect.Ok, Duration: 1_750 * time.Millisecond, Verified: true,
			Commands: []probe.Record{
				{Cmd: probe.Cmd{Name: "orb", Args: []string{"list", "-f", "json"}}, Result: probe.Result{Stdout: "[]\n"}},
				{Cmd: probe.Cmd{Name: "docker", Args: []string{"context", "ls", "--format", "json"}}},
				{Cmd: probe.Cmd{Name: "docker", Args: []string{"--context", "orbstack", "system", "df", "--format", "json"}}},
			},
		},
		{
			Name: "docker", State: detect.Missing, Verified: false,
			Reason: "no /Applications/Docker.app and no com.docker.docker container",
		},
		{Name: "colima", State: detect.Missing, Verified: false, Reason: "neither `colima` nor `limactl` is on the path"},
		{Name: "podman", State: detect.Missing, Verified: false, Reason: "`podman` is not on the path"},
		{Name: "vms", State: detect.Missing, Verified: true, Reason: "no UTM, Parallels, VMware or VirtualBox directory exists"},
		{Name: "kubernetes", State: detect.Ok, Duration: 2 * time.Millisecond, Verified: true},
	}
}

// fakeSummaries is the containers view the golden files describe: OrbStack's
// host image beside what its daemon reports, which are the four rows
// `docker system df` printed on the machine storix was written against.
func fakeSummaries(tr *walk.Tree) map[string]detect.Summary {
	// The golden fixture is hand-built and its child lists are in the order
	// they were written rather than sorted, so the tree's own binary-search
	// lookup does not apply; the node index is scanned instead.
	image := nodeAt(tr, "/Users/u/Library/Group Containers/HUAQ24HBR6.dev.orbstack/data/data.img.raw")
	host := detect.Tool{
		Name: "data.img.raw", Kind: "image", Reclaim: classify.ToolManaged,
		Note: "245 GB apparent, 18.7 GB allocated",
	}
	if image != nil {
		host.Path, host.Node, host.Bytes = image.Display(), image.ID, image.Bytes
	}
	return map[string]detect.Summary{
		"orbstack": {
			Tools: []detect.Tool{host},
			Runtimes: []detect.Runtime{{
				Name:      "OrbStack",
				Context:   "orbstack",
				HostImage: []detect.Tool{host},
				GuestReported: []detect.Line{
					{Type: "Images", Size: 9_853_000_000, Reclaimable: 5_379_000_000, Percent: 54, Known: true, Count: 36, Active: 11, Reclaim: classify.ToolManaged},
					{Type: "Containers", Size: 135_700_000, Reclaimable: 135_500_000, Percent: 99, Known: true, Count: 12, Active: 2, Reclaim: classify.ToolManaged},
					{Type: "Local Volumes", Size: 4_755_000_000, Reclaimable: 938_700_000, Percent: 19, Known: true, Count: 23, Active: 3, Reclaim: classify.UserData},
					{Type: "Build Cache", Size: 5_085_000_000, Reclaimable: 4_638_000_000, Count: 72, Reclaim: classify.ToolManaged},
				},
				Note: "the daemon accounts for 19.83 GB inside an image the host has given 18.7 GB; " +
					"the image is sparse and its contents compress, so the guest's figure is the larger one",
			}},
		},
	}
}

// nodeAt finds a node by display path by scanning the preorder index.
func nodeAt(tr *walk.Tree, display string) *walk.Node {
	for _, n := range tr.Nodes {
		if n.Display() == display {
			return n
		}
	}
	return nil
}

// render is the containers section alone, which is what the TUI view and
// `storix dev` will show.
func render(t *testing.T, r *scan.Result) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Containers(&buf, r, Options{Units: units.Decimal}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestContainersShowsBothColumns is the claim docs/04 makes about this
// section: the host's number and the guest's number are both printed, because
// the gap between them is the thing a reader has to understand.
func TestContainersShowsBothColumns(t *testing.T) {
	got := render(t, fakeScan())
	for _, want := range []string{
		"OrbStack",
		"data.img.raw",
		"18.7 GB", // what the host has allocated
		"9.85 GB", // what the daemon says its images cost
		"19.83 GB",
		"Local Volumes",
		"orbstack",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the containers section does not mention %q:\n%s", want, got)
		}
	}
}

// TestContainersIsEmptyWithoutRuntimes: a machine with no container runtime
// gets no section at all rather than an empty heading.
func TestContainersIsEmptyWithoutRuntimes(t *testing.T) {
	r := fakeScan()
	r.Summaries = nil
	if got := render(t, r); got != "" {
		t.Errorf("a machine with no runtimes printed:\n%s", got)
	}
}

// TestVolumesAreNotAdvertisedAsFree: a named volume holds a database, so its
// reclaimable figure must not be styled as something safe to delete.
func TestVolumesAreNotAdvertisedAsFree(t *testing.T) {
	line := detect.Line{Type: "Local Volumes", Reclaimable: 1, Reclaim: classify.UserData}
	tr := &textReport{st: newStyles(false)}
	if got := tr.reclaimStyle(line).Render("x"); got != tr.st.note.Render("x") {
		t.Error("a user-data row was styled as reclaimable")
	}
	image := detect.Line{Type: "Images", Reclaimable: 1, Reclaim: classify.ToolManaged}
	if got := tr.reclaimStyle(image).Render("x"); got != tr.st.good.Render("x") {
		t.Error("a tool-managed row was not styled as reclaimable")
	}
}

// TestDetectorsTableNamesEveryState covers the section that tells a reader
// why a number is what it is: a tool that was not found changes what the
// ledger can say, so it is reported rather than left out.
func TestDetectorsTableNamesEveryState(t *testing.T) {
	var buf bytes.Buffer
	r := fakeScan()
	tr := &textReport{w: &buf, r: r, l: r.Ledger, o: Options{}.withDefaults(), st: newStyles(false), u: units.Decimal}
	tr.detectors()
	got := buf.String()

	for _, want := range []string{
		"orbstack", "ok", "3 commands",
		"docker", "missing", "no /Applications/Docker.app and no com.docker.docker container",
		"colima", "podman", "vms", "kubernetes",
		"* written from the tool's documentation",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the detectors table does not mention %q:\n%s", want, got)
		}
	}
	if strings.Count(got, " *") < 3 {
		t.Errorf("the three unverified detectors are not all marked:\n%s", got)
	}
}

func TestDetectorReason(t *testing.T) {
	for _, tc := range []struct {
		st   detect.Status
		want string
	}{
		{detect.Status{Reason: "the daemon is down"}, "the daemon is down"},
		{detect.Status{}, ""},
		{detect.Status{Commands: make([]probe.Record, 1)}, "1 command"},
		{detect.Status{Commands: make([]probe.Record, 4)}, "4 commands"},
	} {
		if got := detectorReason(tc.st); got != tc.want {
			t.Errorf("detectorReason(%+v) = %q, want %q", tc.st, got, tc.want)
		}
	}
}
