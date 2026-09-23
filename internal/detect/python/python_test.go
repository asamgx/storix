package python_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/python"
)

// home is the fixture user's home in the form Classify sees.
const home = detecttest.Home

// tree mirrors this machine's Python layout: two interpreters pyenv built, a
// uv cache that is the largest directory of the lot, and uv's interpreters in
// a different place from its cache.
func tree(t *testing.T) *detecttest.Fixture {
	t.Helper()
	return detecttest.Build(t, map[string]int64{
		home + "/.pyenv/versions/3.13.5/lib/python3.13/os.py":      30_000,
		home + "/.pyenv/versions/3.14.2/lib/python3.14/os.py":      28_000,
		home + "/.cache/uv/wheels/a.whl":                           50_000,
		home + "/.local/share/uv/python/cpython-3.13/bin/python3":  6_800,
		home + "/Library/Caches/pip/http-v2/a":                     18_000,
		home + "/Library/Caches/pypoetry/virtualenvs/a/bin/python": 9_000,
		home + "/.local/pipx/venvs/tool/bin/tool":                  1_200,
		home + "/.cache/pre-commit/repoabc/hook":                   2_400,
	})
}

func TestProbeThisMachine(t *testing.T) {
	f := tree(t)
	facts, err := detecttest.Probe(t, python.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got, ok := facts.(*python.Facts)
	if !ok {
		t.Fatalf("Probe returned %T", facts)
	}

	if got.PyenvRoot != home+"/.pyenv" {
		t.Errorf("pyenv root = %q", got.PyenvRoot)
	}
	// "system" means pyenv is not selecting any of the interpreters it has
	// built, which is what this machine reports and is worth saying.
	if !got.SystemPython() {
		t.Errorf("pyenv version = %q, want the system interpreter", got.PyenvVersion)
	}
	// uv's two directories are different questions with different answers,
	// which is the reason both are asked.
	if got.UvCache != home+"/.cache/uv" {
		t.Errorf("uv cache = %q", got.UvCache)
	}
	if got.UvPython != home+"/.local/share/uv/python" {
		t.Errorf("uv python dir = %q", got.UvPython)
	}
	if got.PipCache != home+"/Library/Caches/pip" {
		t.Errorf("pip cache = %q, want the macOS location rather than the XDG one", got.PipCache)
	}
	// Neither is installed here, and that is an answer rather than a gap.
	if got.PoetryCache != "" || got.CondaBase != "" {
		t.Errorf("poetry/conda = %q/%q, want nothing on a machine without them",
			got.PoetryCache, got.CondaBase)
	}
}

func TestClassifyThisMachine(t *testing.T) {
	f := tree(t)
	det := python.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/this-machine.json"))
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
		{home + "/.pyenv", "pyenv", "cli:pyenv", classify.ToolManaged},
		{home + "/.cache/uv", "uv", "cli:uv", classify.Regenerable},
		{home + "/.local/share/uv/python", "uv", "cli:uv", classify.ToolManaged},
		{home + "/Library/Caches/pip", "pip", "cli:pip", classify.Regenerable},
		{home + "/Library/Caches/pypoetry", "Poetry", "cli:poetry", classify.Regenerable},
		{home + "/.local/pipx", "pipx", "cli:pipx", classify.ToolManaged},
		{home + "/.cache/pre-commit", "pre-commit", "cli:pre-commit", classify.Regenerable},
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
		if c.Source.Kind != classify.SourceDetector || c.Source.Detector != "python" {
			t.Errorf("%s source = %s, want detector:python", tc.path, c.Source)
		}
	}

	// With pyenv set to `system`, no interpreter is current and the rows say
	// why rather than picking one.
	for _, v := range []string{"3.13.5", "3.14.2"} {
		tool, ok := detecttest.Tool(sum, home+"/.pyenv/versions/"+v)
		if !ok {
			t.Errorf("no row for the %s interpreter", v)
			continue
		}
		if tool.Current {
			t.Errorf("%s was marked current although pyenv is set to `system`", v)
		}
		if !strings.Contains(tool.Note, "system") {
			t.Errorf("%s note = %q, want it to explain the system setting", v, tool.Note)
		}
	}
	if root, ok := detecttest.Tool(sum, home+"/.pyenv"); !ok || !strings.Contains(root.Note, "2 interpreters") {
		t.Errorf("pyenv row note = %q, want the interpreter count read from the tree", root.Note)
	}
}

// TestPyenvCurrent: when pyenv is selecting one of its own interpreters, that
// one carries the marker.
func TestPyenvCurrent(t *testing.T) {
	f := tree(t)
	det := python.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/pyenv-current.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	_, sum := det.Classify(f.Tree, facts, f.Context)

	cur, ok := detecttest.Tool(sum, home+"/.pyenv/versions/3.14.2")
	if !ok {
		t.Fatal("no row for the selected interpreter")
	}
	if !cur.Current {
		t.Error("the interpreter `pyenv version-name` named is not marked current")
	}
	if other, ok := detecttest.Tool(sum, home+"/.pyenv/versions/3.13.5"); !ok || other.Current {
		t.Error("a second interpreter was marked current too")
	}
}

func TestMissing(t *testing.T) {
	f := tree(t)
	_, err := detecttest.Probe(t, python.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Errorf("err = %v, want missing when no Python tool is on the path", err)
	}
}

// TestTimeoutStillBuckets: pyenv and uv timed out and their directories are
// still in Developer, from the documented defaults.
func TestTimeoutStillBuckets(t *testing.T) {
	f := tree(t)
	det := python.New()
	facts, err := detecttest.Probe(t, det, f.Env(t, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Errorf("err = %v, want degraded", err)
	}

	claims, sum := det.Classify(f.Tree, facts, f.Context)
	for _, p := range []string{home + "/.pyenv", home + "/.cache/uv", home + "/Library/Caches/pip"} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s with every probe timed out", p)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", p, c.Bucket)
		}
	}
	// The interpreters are still listed, from the walk, and none is current.
	for _, v := range []string{"3.13.5", "3.14.2"} {
		tool, ok := detecttest.Tool(sum, home+"/.pyenv/versions/"+v)
		if !ok {
			t.Errorf("no row for %s", v)
			continue
		}
		if tool.Current {
			t.Errorf("%s was marked current although pyenv never answered", v)
		}
	}
}
