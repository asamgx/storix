package ruby_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/ruby"
)

const home = detecttest.Home

// rootedStat scopes the detector's filesystem questions to the fixture, so
// that /Library/Ruby/Gems means the fixture's and not the one macOS ships on
// whatever machine the test runs on.
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

// corpus is a machine with rbenv, three interpreters and the system Ruby,
// which is more than this one has: here only /Library/Ruby/Gems exists.
func corpus() map[string]int64 {
	return map[string]int64{
		home + "/.gem/ruby/3.3.0/gems/x":         300_000,
		"/Library/Ruby/Gems/2.6.0/gems/x":        200_000,
		home + "/.rbenv/versions/3.3.6/bin/ruby": 400_000,
		home + "/.rbenv/versions/3.2.2/bin/ruby": 350_000,
		home + "/.rbenv/versions/3.1.4/bin/ruby": 250_000,
		home + "/.rbenv/version":                 20,
		home + "/.rvm/rubies/x":                  150_000,
		home + "/.bundle/cache/x":                100_000,
	}
}

// writeVersion puts a real rbenv version marker in the fixture, which the
// probe reads through the sanctioned reader.
func writeVersion(t *testing.T, f *detecttest.Fixture, version string) {
	t.Helper()
	p := filepath.Join(f.Real, ".rbenv", "version")
	if err := os.WriteFile(p, []byte(version+"\n"), 0o644); err != nil {
		t.Fatalf("write version marker: %v", err)
	}
}

// TestNothingInstalled: no gem on the path and no Ruby directory is Missing.
func TestNothingInstalled(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, ruby.New(), fixtureEnv(t, f, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	claims, sum := ruby.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims with no Ruby at all", len(claims))
	}
}

// TestThisMachine replays what this machine answered: the system Ruby, whose
// gem directory is inside /Library/Ruby and therefore already covered.
func TestThisMachine(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, err := detecttest.Probe(t, ruby.New(), fixtureEnv(t, f, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*ruby.Facts)
	if got.GemDir != "/Library/Ruby/Gems/2.6.0" {
		t.Errorf("gem dir = %q", got.GemDir)
	}

	claims, _ := ruby.New().Classify(f.Tree, facts, f.Context)
	if _, ok := detecttest.ClaimAt(claims, "/Library/Ruby/Gems"); !ok {
		t.Error("the system gems were not claimed")
	}
	// The versioned directory the tool named is deeper than the table
	// entry above it, so it gets a claim of its own.
	c, ok := detecttest.ClaimAt(claims, got.GemDir)
	if !ok {
		t.Fatalf("the gem directory %s was not claimed", got.GemDir)
	}
	if c.Owner != "RubyGems" {
		t.Errorf("gem dir owner = %q", c.Owner)
	}
}

// TestGemDirAlreadyInTheTable: when the tool names a path the table already
// has, there is nothing to add and no second claim is made.
func TestGemDirAlreadyInTheTable(t *testing.T) {
	f := detecttest.Build(t, corpus())
	claims, _ := ruby.New().Classify(f.Tree, &ruby.Facts{GemDir: "/Library/Ruby/Gems"}, f.Context)
	seen := 0
	for _, c := range claims {
		if c.Node != nil && c.Node.Display() == "/Library/Ruby/Gems" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("/Library/Ruby/Gems was claimed %d times, want once", seen)
	}
}

// TestGemDirOutsideTheTable: a gem directory inside an rbenv version is
// somewhere no static rule looks, and claiming it is what the probe is for.
func TestGemDirOutsideTheTable(t *testing.T) {
	c := corpus()
	c[home+"/.rbenv/versions/3.3.6/lib/ruby/gems/3.3.0/gems/rails/x"] = 500_000
	f := detecttest.Build(t, c)
	writeVersion(t, f, "3.3.6")

	facts, err := detecttest.Probe(t, ruby.New(), fixtureEnv(t, f, "testdata/rbenv.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*ruby.Facts)
	if got.Rbenv != "3.3.6" {
		t.Errorf("rbenv version = %q, want 3.3.6 read from the marker", got.Rbenv)
	}

	claims, sum := ruby.New().Classify(f.Tree, facts, f.Context)
	gems := home + "/.rbenv/versions/3.3.6/lib/ruby/gems/3.3.0"
	c2, ok := detecttest.ClaimAt(claims, gems)
	if !ok {
		t.Fatalf("the gem directory %s was not claimed", gems)
	}
	if c2.Reclaim != classify.ToolManaged || c2.Owner != "RubyGems" {
		t.Errorf("gem dir = %q / %s", c2.Owner, c2.Reclaim)
	}
	if _, ok := detecttest.Tool(sum, gems); !ok {
		t.Error("the gem directory has no summary row")
	}
}

// TestVersionsAndCurrentMarker: one row per installed interpreter, with the
// one rbenv selects marked.
func TestVersionsAndCurrentMarker(t *testing.T) {
	f := detecttest.Build(t, corpus())
	writeVersion(t, f, "3.2.2")

	facts, err := detecttest.Probe(t, ruby.New(), fixtureEnv(t, f, "testdata/rbenv.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := ruby.New().Classify(f.Tree, facts, f.Context)

	for _, v := range []string{"3.3.6", "3.2.2", "3.1.4"} {
		p := home + "/.rbenv/versions/" + v
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Reclaim != classify.ToolManaged || c.Owner != "rbenv" {
			t.Errorf("%s = %q / %s", p, c.Owner, c.Reclaim)
		}
	}

	current := 0
	for _, tool := range sum.Tools {
		if tool.Current {
			current++
			if tool.Version != "3.2.2" {
				t.Errorf("the current interpreter is %q, want 3.2.2", tool.Version)
			}
		}
	}
	if current != 1 {
		t.Errorf("%d interpreters are marked current, want exactly one", current)
	}
}

// TestGemTimeout: a probe that did not finish costs the gem directory and
// nothing else; the static paths are still claimed.
func TestGemTimeout(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, err := detecttest.Probe(t, ruby.New(), fixtureEnv(t, f, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if got := facts.(*ruby.Facts); got.GemDir != "" {
		t.Errorf("gem dir = %q from a timed-out command", got.GemDir)
	}
	claims, _ := ruby.New().Classify(f.Tree, facts, f.Context)
	for _, p := range []string{home + "/.gem", home + "/.rvm", home + "/.bundle/cache", "/Library/Ruby/Gems"} {
		if _, ok := detecttest.ClaimAt(claims, p); !ok {
			t.Errorf("%s stopped being claimed because gem timed out", p)
		}
	}
}

// TestBundlerCacheIsRegenerable: the one row in this detector that is not
// tool-managed, because a gem archive comes back from a `bundle install`.
func TestBundlerCacheIsRegenerable(t *testing.T) {
	f := detecttest.Build(t, corpus())
	claims, _ := ruby.New().Classify(f.Tree, &ruby.Facts{}, f.Context)
	c, ok := detecttest.ClaimAt(claims, home+"/.bundle/cache")
	if !ok {
		t.Fatal("the Bundler cache was not claimed")
	}
	if c.Reclaim != classify.Regenerable {
		t.Errorf("Bundler cache reclaim = %s, want regenerable", c.Reclaim)
	}
}
