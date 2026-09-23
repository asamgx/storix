package orbstack_test

import (
	"errors"
	"path"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/orbstack"
)

// home is where the fixture tree puts OrbStack's directories, in the display
// form Classify sees.
const home = detecttest.Home

// groupDir is the group container, image and swap as this machine has them:
// a sparse 245 GB image with 18.8 GB of real blocks. The fixture keeps the
// shape and shrinks the numbers, because what is being tested is that the
// allocated figure is the one reported, not that it is 18.8 GB.
var groupDir = home + "/Library/Group Containers/HUAQ24HBR6.dev.orbstack"

// tree is the machine every test here classifies.
func tree(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		groupDir + "/data/data.img.raw":                              -8_000_000,
		groupDir + "/data/swap.img":                                  -1_000_000,
		groupDir + "/Library/Caches/keep":                            2_000,
		home + "/.orbstack/run/docker.sock.placeholder":              1_000,
		home + "/Library/Caches/dev.kdrag0n.MacVirt/x":               3_000,
		home + "/Library/Application Support/OrbStack/settings.json": 500,
	})
}

func TestProbeThisMachine(t *testing.T) {
	f := tree(t)
	env := f.Env(t, "testdata/this-machine.json")

	facts, err := detecttest.Probe(t, orbstack.New(), env)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got, ok := facts.(*orbstack.Facts)
	if !ok {
		t.Fatalf("Probe returned %T", facts)
	}
	if len(got.Machines) != 0 {
		t.Errorf("machines = %v, want none: `orb list` printed []", got.Machines)
	}
	if got.Context != "orbstack" {
		t.Errorf("context = %q, want %q", got.Context, "orbstack")
	}
	if got.Endpoint != "unix:///Users/andrew/.orbstack/run/docker.sock" {
		t.Errorf("endpoint = %q", got.Endpoint)
	}

	want := []struct {
		kind        string
		size        int64
		reclaimable int64
		pct         int
		count       int
		active      int
	}{
		{"Images", 9_853_000_000, 5_379_000_000, 54, 36, 11},
		{"Containers", 135_700_000, 135_500_000, 99, 12, 2},
		{"Local Volumes", 4_755_000_000, 938_700_000, 19, 23, 3},
		{"Build Cache", 5_085_000_000, 4_638_000_000, 0, 72, 0},
	}
	if len(got.DF) != len(want) {
		t.Fatalf("df has %d rows, want %d", len(got.DF), len(want))
	}
	for i, w := range want {
		row := got.DF[i]
		if row.Type != w.kind || row.Size != w.size || row.Reclaimable != w.reclaimable ||
			row.Count != w.count || row.Active != w.active {
			t.Errorf("df[%d] = %+v, want %s %d/%d %d of %d", i, row, w.kind, w.size, w.reclaimable, w.active, w.count)
		}
		if w.pct > 0 && (!row.PercentOK || row.Percent != w.pct) {
			t.Errorf("df[%d] percent = %d (known %v), want %d", i, row.Percent, row.PercentOK, w.pct)
		}
		if w.pct == 0 && row.PercentOK {
			t.Errorf("df[%d] invented a percentage the daemon did not print", i)
		}
	}
	if got.Detail == "" {
		t.Error("the verbose table was not kept as evidence")
	}
}

func TestClassifyThisMachine(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, orbstack.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	claims, sum := orbstack.New().Classify(f.Tree, facts, f.Context)

	for _, p := range []string{
		groupDir,
		home + "/.orbstack",
		home + "/Library/Caches/dev.kdrag0n.MacVirt",
		home + "/Library/Application Support/OrbStack",
	} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Bucket != classify.BucketContainers {
			t.Errorf("%s is in bucket %s, want containers", p, c.Bucket)
		}
		if c.Owner != "OrbStack" {
			t.Errorf("%s owner = %q, want OrbStack", p, c.Owner)
		}
		if c.Source.Kind != classify.SourceDetector || c.Source.Detector != "orbstack" {
			t.Errorf("%s source = %s, want detector:orbstack", p, c.Source)
		}
		for _, key := range []string{"app:dev.kdrag0n.MacVirt", "team:HUAQ24HBR6"} {
			if !detecttest.HasKey(c, key) {
				t.Errorf("%s is missing owner key %s (has %v)", p, key, c.OwnerKeys)
			}
		}
	}

	// A path OrbStack does not have must not be claimed: a claim with no
	// bytes behind it would put an empty row in the containers view.
	if _, ok := detecttest.ClaimAt(claims, home+"/Library/Caches/dev.orbstack.OrbStack"); ok {
		t.Error("a directory that does not exist was claimed")
	}

	rt, ok := detecttest.Runtime(sum, "OrbStack")
	if !ok {
		t.Fatalf("no OrbStack runtime in the summary: %+v", sum)
	}
	if rt.Context != "orbstack" {
		t.Errorf("runtime context = %q", rt.Context)
	}
	if len(rt.HostImage) != 2 {
		t.Fatalf("host image rows = %d, want data.img.raw and swap.img", len(rt.HostImage))
	}
	if rt.HostImage[0].Name != "data.img.raw" || rt.HostImage[1].Name != "swap.img" {
		t.Errorf("host image rows = %q, %q", rt.HostImage[0].Name, rt.HostImage[1].Name)
	}
	// The host figure is allocated bytes. The fixture's image is sparse, so
	// a detector reporting apparent size would report eight megabytes for a
	// file that occupies none.
	if rt.HostBytes() >= 8_000_000 {
		t.Errorf("host bytes = %d; the sparse image's apparent size was reported instead of its blocks", rt.HostBytes())
	}
	if got, want := rt.GuestBytes(), int64(9_853_000_000+135_700_000+4_755_000_000+5_085_000_000); got != want {
		t.Errorf("guest bytes = %d, want %d", got, want)
	}
	if len(rt.GuestReported) != 4 {
		t.Errorf("guest rows = %d, want 4", len(rt.GuestReported))
	}
	for _, line := range rt.GuestReported {
		want := classify.ToolManaged
		if line.Type == "Local Volumes" {
			want = classify.UserData
		}
		if line.Reclaim != want {
			t.Errorf("%s reclaim = %s, want %s", line.Type, line.Reclaim, want)
		}
	}
	if len(rt.Machines) != 0 {
		t.Errorf("machines = %v, want none", rt.Machines)
	}
}

func TestProbeMissing(t *testing.T) {
	// An empty tree: no ~/.orbstack, no group container, and a fixture that
	// names no commands, so `orb` is not on the path either.
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	_, err := detecttest.Probe(t, orbstack.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
}

func TestClassifyWithoutFacts(t *testing.T) {
	// A degraded probe still has to bucket the bytes: the directories are
	// OrbStack's whether or not the daemon answered.
	f := tree(t)
	claims, sum := orbstack.New().Classify(f.Tree, nil, f.Context)
	c, ok := detecttest.ClaimAt(claims, groupDir)
	if !ok {
		t.Fatal("the group container was not claimed without facts")
	}
	if c.Bucket != classify.BucketContainers {
		t.Errorf("bucket = %s, want containers", c.Bucket)
	}
	rt, ok := detecttest.Runtime(sum, "OrbStack")
	if !ok {
		t.Fatal("no runtime without facts")
	}
	if len(rt.GuestReported) != 0 {
		t.Error("guest rows appeared without a daemon to report them")
	}
	if rt.Note == "" {
		t.Error("the runtime does not say why it has only one column")
	}
}

func TestProbeDaemonDown(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, orbstack.New(), f.Env(t, "testdata/no-daemon.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	got := facts.(*orbstack.Facts)
	if len(got.DF) != 0 {
		t.Error("df rows survived a daemon that refused the connection")
	}
	if got.Context != "orbstack" {
		t.Errorf("the context was lost along with the daemon: %q", got.Context)
	}

	// The host side is still measured, which is the whole point of
	// degrading rather than failing.
	_, sum := orbstack.New().Classify(f.Tree, facts, f.Context)
	rt, _ := detecttest.Runtime(sum, "OrbStack")
	if len(rt.HostImage) != 2 {
		t.Errorf("host image rows = %d with the daemon down, want 2", len(rt.HostImage))
	}
}

func TestProbeTimeout(t *testing.T) {
	f := tree(t)
	_, err := detecttest.Probe(t, orbstack.New(), f.Env(t, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
}

func TestProbeMalformedOutput(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, orbstack.New(), f.Env(t, "testdata/malformed.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	got := facts.(*orbstack.Facts)
	// JSON that does not parse falls back to the plain table, which does.
	if len(got.Machines) != 1 || got.Machines[0].Name != "ubuntu" {
		t.Errorf("machines = %+v, want the plain `orb list` table to have been read", got.Machines)
	}
	if got.Context != "" {
		t.Errorf("a context was read from unparseable output: %q", got.Context)
	}
}

func TestProbeWithMachines(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, orbstack.New(), f.Env(t, "testdata/machines.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*orbstack.Facts)
	if len(got.Machines) != 2 {
		t.Fatalf("machines = %+v, want two", got.Machines)
	}
	if got.Machines[0].Name != "ubuntu" || got.Machines[0].Image != "ubuntu 24.04" {
		t.Errorf("machine[0] = %+v", got.Machines[0])
	}

	_, sum := orbstack.New().Classify(f.Tree, facts, f.Context)
	rt, _ := detecttest.Runtime(sum, "OrbStack")
	if len(rt.Machines) != 2 || rt.Machines[1] != "alpine (stopped)" {
		t.Errorf("runtime machines = %v", rt.Machines)
	}
}

func TestStatusIsVerified(t *testing.T) {
	// OrbStack was the runtime storix was written against, so unlike the
	// others its detector is not marked unverified.
	if _, ok := any(orbstack.New()).(detect.Unverified); ok {
		t.Error("the orbstack detector claims to be unverified")
	}
}

func TestClaimsNeedAHome(t *testing.T) {
	f := tree(t)
	claims, sum := orbstack.New().Classify(f.Tree, nil, classify.Context{})
	if len(claims) != 0 || !sum.Empty() {
		t.Error("claims were made without knowing whose home to look in")
	}
}

func TestPathsAreTheDocumentedOnes(t *testing.T) {
	// A guard against a rename: the group container's name is OrbStack's
	// team id and bundle id, and a typo in it would silently stop claiming
	// eighteen gigabytes.
	f := tree(t)
	claims, _ := orbstack.New().Classify(f.Tree, nil, f.Context)
	if _, ok := detecttest.ClaimAt(claims, path.Join(home, "Library/Group Containers/HUAQ24HBR6.dev.orbstack")); !ok {
		t.Error("the group container HUAQ24HBR6.dev.orbstack was not claimed")
	}
}
