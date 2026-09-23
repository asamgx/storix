package rust_test

import (
	"errors"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/rust"
)

// home is the fixture user's home in the form Classify sees.
const home = detecttest.Home

// stable is the toolchain this machine has, named the way rustup names one.
const stable = "stable-aarch64-apple-darwin"

// tree mirrors this machine's Rust layout: a small Cargo home and a
// gigabyte-scale rustup, which is the usual shape and the reason the
// toolchain list matters more than the registry does.
func tree(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		home + "/.cargo/bin/cargo":                                          11_000,
		home + "/.cargo/registry/cache/crates.io/serde.crate":               4_000,
		home + "/.cargo/git/db/x/HEAD":                                      600,
		home + "/.rustup/toolchains/" + stable + "/bin/rustc":               90_000,
		home + "/.rustup/toolchains/nightly-aarch64-apple-darwin/bin/rustc": 70_000,
		home + "/.rustup/downloads/keep":                                    200,
	})
}

func TestProbeThisMachine(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, rust.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got, ok := facts.(*rust.Facts)
	if !ok {
		t.Fatalf("Probe returned %T", facts)
	}
	if len(got.Toolchains) != 1 {
		t.Fatalf("toolchains = %+v, want the one this machine has", got.Toolchains)
	}
	tc := got.Toolchains[0]
	if tc.Name != stable {
		t.Errorf("toolchain = %q, want %q", tc.Name, stable)
	}
	// rustup 1.28 prints "(active, default)"; both words are markers and
	// both are read, because only the default one is the fallback.
	if !tc.Default || !tc.Active {
		t.Errorf("markers = default %v active %v, want both from `(active, default)`", tc.Default, tc.Active)
	}
}

// TestOlderRustupMarker: before 1.28 rustup printed "(default)" alone.
func TestOlderRustupMarker(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, rust.New(), f.Env(t, "testdata/two-toolchains.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*rust.Facts)
	if len(got.Toolchains) != 2 {
		t.Fatalf("toolchains = %+v, want two", got.Toolchains)
	}
	if !got.Toolchains[0].Default || got.Toolchains[0].Active {
		t.Errorf("stable = %+v, want default without the active marker", got.Toolchains[0])
	}
	if got.Toolchains[1].Default {
		t.Errorf("nightly = %+v, want no markers", got.Toolchains[1])
	}
}

func TestClassifyThisMachine(t *testing.T) {
	f := tree(t)
	det := rust.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/two-toolchains.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := det.Classify(f.Tree, facts, f.Context)

	for _, tc := range []struct {
		path    string
		owner   string
		key     string
		reclaim classify.Reclaim
	}{
		{home + "/.cargo", "Cargo", "cli:cargo", classify.ToolManaged},
		{home + "/.cargo/registry", "Cargo", "cli:cargo", classify.Regenerable},
		{home + "/.cargo/git", "Cargo", "cli:cargo", classify.Regenerable},
		{home + "/.cargo/bin", "Cargo", "cli:cargo", classify.ToolManaged},
		{home + "/.rustup", "rustup", "cli:rustup", classify.ToolManaged},
		{home + "/.rustup/downloads", "rustup", "cli:rustup", classify.Regenerable},
	} {
		c, ok := detecttest.ClaimAt(claims, tc.path)
		if !ok {
			t.Errorf("no claim at %s", tc.path)
			continue
		}
		if c.Owner != tc.owner || !detecttest.HasKey(c, tc.key) {
			t.Errorf("%s owner = %q %v, want %q %s", tc.path, c.Owner, c.OwnerKeys, tc.owner, tc.key)
		}
		if c.Reclaim != tc.reclaim {
			t.Errorf("%s reclaim = %s, want %s", tc.path, c.Reclaim, tc.reclaim)
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", tc.path, c.Bucket)
		}
	}

	// The default toolchain carries the marker and the other does not.
	def, ok := detecttest.Tool(sum, home+"/.rustup/toolchains/"+stable)
	if !ok {
		t.Fatal("no row for the default toolchain")
	}
	if !def.Current || def.Version != stable {
		t.Errorf("default row = %+v, want it current and named", def)
	}
	night, ok := detecttest.Tool(sum, home+"/.rustup/toolchains/nightly-aarch64-apple-darwin")
	if !ok {
		t.Fatal("no row for the nightly toolchain")
	}
	if night.Current {
		t.Error("nightly was marked current although rustup calls stable the default")
	}
}

func TestMissing(t *testing.T) {
	// No rustup, no cargo, and no ~/.cargo or ~/.rustup on disk.
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 100})
	_, err := detecttest.Probe(t, rust.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Errorf("err = %v, want missing", err)
	}
}

// TestDirectoriesWithoutRustup: the toolchain directories are on disk and
// rustup is not on the path. That is a degradation, not a missing tool: the
// bytes are still there and still Rust's.
func TestDirectoriesWithoutRustup(t *testing.T) {
	f := tree(t)
	det := rust.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Errorf("err = %v, want degraded with the directories present", err)
	}
	claims, sum := det.Classify(f.Tree, facts, f.Context)
	if _, ok := detecttest.ClaimAt(claims, home+"/.rustup"); !ok {
		t.Error("no claim on ~/.rustup")
	}
	// The toolchains are recovered from the walk, and none is called
	// default, because nothing said which is.
	for _, name := range []string{stable, "nightly-aarch64-apple-darwin"} {
		tool, ok := detecttest.Tool(sum, home+"/.rustup/toolchains/"+name)
		if !ok {
			t.Errorf("no row for %s", name)
			continue
		}
		if tool.Current {
			t.Errorf("%s was marked default although rustup never answered", name)
		}
	}
}

func TestTimeout(t *testing.T) {
	f := tree(t)
	det := rust.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Errorf("err = %v, want degraded", err)
	}
	claims, _ := det.Classify(f.Tree, facts, f.Context)
	if _, ok := detecttest.ClaimAt(claims, home+"/.cargo/registry"); !ok {
		t.Error("the registry cache lost its claim when rustup timed out")
	}
}

// TestMalformedOutput: rustup printed a sentence rather than a listing.
func TestMalformedOutput(t *testing.T) {
	f := tree(t)
	det := rust.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/malformed.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Errorf("err = %v, want degraded", err)
	}
	if got := facts.(*rust.Facts); len(got.Toolchains) != 0 {
		t.Errorf("toolchains = %+v, want none parsed out of a sentence", got.Toolchains)
	}
}
