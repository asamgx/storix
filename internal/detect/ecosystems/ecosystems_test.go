package ecosystems_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/ecosystems"
)

const home = detecttest.Home

// rootedStat scopes the detector's filesystem questions to the fixture.
//
// Some of these directories are absolute — /Library/Java belongs to the
// machine, not to a user — and detecttest's environment stats the real
// filesystem. Without this, a test asserting "nothing is installed" passes or
// fails according to whether the developer running it has a JDK.
func rootedStat(f *detecttest.Fixture) func(string) (detect.FileInfo, error) {
	root := strings.TrimSuffix(f.Real, detecttest.Home)
	return func(p string) (detect.FileInfo, error) {
		if !strings.HasPrefix(p, root) {
			p = filepath.Join(root, p)
		}
		return detect.Stat(p)
	}
}

// fixtureEnv is detecttest's environment with the filesystem scoped.
func fixtureEnv(t *testing.T, f *detecttest.Fixture, fixture string) detect.Env {
	t.Helper()
	env := f.Env(t, fixture)
	env.Stat = rootedStat(f)
	return env
}

// TestThisMachineIsMissing: none of these ecosystems is installed here, which
// is the state this detector is written against and the one it has to handle
// without inventing anything.
func TestThisMachineIsMissing(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, ecosystems.New(), fixtureEnv(t, f, "testdata/this-machine.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	claims, sum := ecosystems.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims on a machine with none of these toolchains", len(claims))
	}
}

// TestClassify covers one directory per ecosystem, and in particular the
// pairs that look alike and are not: a download cache beside an installed
// compiler.
func TestClassify(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{
		home + "/.pub-cache/hosted/pub.dev/x":     500_000,
		home + "/flutter/bin/cache/artifacts/x":   400_000,
		home + "/development/flutter/packages/x":  300_000,
		home + "/.dartServer/x":                   50_000,
		home + "/.nuget/packages/newtonsoft/x":    200_000,
		home + "/.dotnet/tools/x":                 150_000,
		home + "/.ghcup/ghc/9.6.6/x":              600_000,
		home + "/.stack/programs/x":               250_000,
		home + "/.cabal/packages/x":               120_000,
		home + "/.ccache/0/x":                     110_000,
		home + "/.conan2/p/x":                     100_000,
		home + "/.cocoapods/repos/trunk/x":        90_000,
		home + "/Library/Caches/CocoaPods/Pods/x": 80_000,
		home + "/.mix/archives/x":                 70_000,
		home + "/.hex/packages/x":                 65_000,
		home + "/.composer/cache/files/x":         60_000,
		home + "/.cache/zig/o/x":                  55_000,
		home + "/.zvm/0.13.0/x":                   45_000,
		"/private/var/tmp/_bazel_andrew/abc/x":    700_000,
	})

	facts, err := detecttest.Probe(t, ecosystems.New(), fixtureEnv(t, f, "testdata/missing.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := ecosystems.New().Classify(f.Tree, facts, f.Context)

	want := []struct {
		path    string
		owner   string
		reclaim classify.Reclaim
	}{
		{home + "/.pub-cache", "Dart", classify.Regenerable},
		{home + "/flutter", "Flutter", classify.ToolManaged},
		{home + "/flutter/bin/cache", "Flutter", classify.Regenerable},
		{home + "/development/flutter", "Flutter", classify.ToolManaged},
		{home + "/.dartServer", "Dart", classify.Regenerable},
		{home + "/.nuget", "NuGet", classify.Regenerable},
		{home + "/.dotnet", ".NET", classify.ToolManaged},
		{home + "/.ghcup", "GHCup", classify.ToolManaged},
		{home + "/.stack", "Stack", classify.ToolManaged},
		{home + "/.cabal", "Cabal", classify.Regenerable},
		{home + "/.ccache", "ccache", classify.Regenerable},
		{home + "/.conan2", "Conan", classify.Regenerable},
		{home + "/.cocoapods/repos", "CocoaPods", classify.Regenerable},
		{home + "/Library/Caches/CocoaPods", "CocoaPods", classify.Regenerable},
		{home + "/.mix", "Mix", classify.ToolManaged},
		{home + "/.hex", "Hex", classify.Regenerable},
		{home + "/.composer/cache", "Composer", classify.Regenerable},
		{home + "/.cache/zig", "Zig", classify.Regenerable},
		{home + "/.zvm", "zvm", classify.ToolManaged},
		{"/private/var/tmp/_bazel_andrew", "Bazel", classify.Regenerable},
	}
	for _, w := range want {
		c, ok := detecttest.ClaimAt(claims, w.path)
		if !ok {
			t.Errorf("no claim at %s", w.path)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", w.path, c.Bucket)
		}
		if c.Owner != w.owner {
			t.Errorf("%s owner = %q, want %q", w.path, c.Owner, w.owner)
		}
		if c.Reclaim != w.reclaim {
			t.Errorf("%s reclaim = %s, want %s", w.path, c.Reclaim, w.reclaim)
		}
		if c.Source.Detector != "ecosystems" {
			t.Errorf("%s source = %s", w.path, c.Source)
		}
	}
	if _, ok := detecttest.Tool(sum, "/private/var/tmp/_bazel_andrew"); !ok {
		t.Error("the Bazel output base has no summary row")
	}
}

// TestBazelIsNamedAfterTheUser: the output base is outside the home and
// carries the account name, which has to come from the home path.
func TestBazelIsNamedAfterTheUser(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{"/private/var/tmp/_bazel_andrew/x": 1_000})
	claims, _ := ecosystems.New().Classify(f.Tree, &ecosystems.Facts{}, f.Context)
	if _, ok := detecttest.ClaimAt(claims, "/private/var/tmp/_bazel_andrew"); !ok {
		t.Error("the output base for the fixture's user was not claimed")
	}

	// A different user's output base is not this user's.
	claims, _ = ecosystems.New().Classify(f.Tree, &ecosystems.Facts{}, classify.Context{Home: "/Users/someone"})
	if _, ok := detecttest.ClaimAt(claims, "/private/var/tmp/_bazel_andrew"); ok {
		t.Error("another user's Bazel output base was claimed")
	}
}
