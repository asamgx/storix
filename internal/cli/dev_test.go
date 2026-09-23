package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/units"
)

func TestDevResolveRejectsBothCacheFlags(t *testing.T) {
	o := devOptions{fromCache: true, scan: true}
	_, _, err := o.resolve()
	var ce *ConfigError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("resolve accepted --from-cache with --scan: %v", err)
	}
}

func TestDevResolveMapsFlagsOntoOptions(t *testing.T) {
	o := devOptions{binary: true, roots: []string{"/tmp"}, disableDet: []string{"homebrew"}}
	ro, cfg, err := o.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ro.Units != units.Binary || cfg.Units != units.Binary {
		t.Error("--binary did not select binary units")
	}
	if len(cfg.Roots) != 1 || cfg.Roots[0] != "/tmp" {
		t.Errorf("roots = %v", cfg.Roots)
	}
	if len(cfg.DisabledDetectors) != 1 || cfg.DisabledDetectors[0] != "homebrew" {
		t.Errorf("disabled detectors = %v", cfg.DisabledDetectors)
	}
}

func TestRunDevWritesTheDeveloperSectionWithoutError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 200_000)

	var out, errOut bytes.Buffer
	o := &devOptions{roots: []string{f.Root}}
	if err := runDev(context.Background(), &out, &errOut, o); err != nil {
		t.Fatalf("runDev: %v (stderr %s)", err, errOut.String())
	}
}

func TestRunDevJSONHasSchemaAndOneRowPerDetector(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 200_000)

	var out, errOut bytes.Buffer
	o := &devOptions{roots: []string{f.Root}, json: true}
	if err := runDev(context.Background(), &out, &errOut, o); err != nil {
		t.Fatalf("runDev --json: %v (stderr %s)", err, errOut.String())
	}

	var doc struct {
		Schema    int `json:"schema"`
		Developer struct {
			Detectors []struct {
				Name       string `json:"name"`
				State      string `json:"state"`
				DurationNS int64  `json:"duration_ns"`
			} `json:"detectors"`
			Projects []detect.Project `json:"projects"`
		} `json:"developer"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("the output is not valid JSON: %v\n%s", err, out.String())
	}
	if doc.Schema != report.SchemaVersion {
		t.Errorf("schema = %d, want %d", doc.Schema, report.SchemaVersion)
	}
	want := len(detect.Default().Detectors())
	if got := len(doc.Developer.Detectors); got != want {
		t.Errorf("detectors = %d rows, want one per registered detector (%d)", got, want)
	}
	for _, d := range doc.Developer.Detectors {
		if d.Name == "" {
			t.Error("a detector row has no name")
		}
		if d.State == "" {
			t.Errorf("detector %q has no state", d.Name)
		}
	}
}

func TestRunDevProjectsSaysWhenThereAreNone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 1024)

	var out, errOut bytes.Buffer
	o := &devOptions{roots: []string{f.Root}, projects: true}
	if err := runDev(context.Background(), &out, &errOut, o); err != nil {
		t.Fatalf("runDev --projects: %v (stderr %s)", err, errOut.String())
	}
	if !strings.Contains(out.String(), "no projects found") {
		t.Errorf("output = %q, want it to say no projects were found", out.String())
	}
}

func TestDevScanCachesThenFromCacheReusesIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 200_000)

	var errOut bytes.Buffer
	cfg := scan.Config{Roots: []string{f.Root}, Version: BuildInfo()}

	first, err := devScan(context.Background(), &errOut, cfg, false)
	if err != nil {
		t.Fatalf("first scan: %v (stderr %s)", err, errOut.String())
	}
	if first.CachePath == "" {
		t.Error("the first scan was not cached")
	}

	cfg.FromCache = true
	second, err := devScan(context.Background(), &errOut, cfg, false)
	if err != nil {
		t.Fatalf("--from-cache: %v (stderr %s)", err, errOut.String())
	}
	if !second.FromCache {
		t.Error("the second scan did not come from the cache")
	}
}

func TestDevScanForceScanSkipsTheCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 200_000)

	var errOut bytes.Buffer
	cfg := scan.Config{Roots: []string{f.Root}, Version: BuildInfo()}
	if _, err := devScan(context.Background(), &errOut, cfg, false); err != nil {
		t.Fatalf("priming scan: %v", err)
	}

	fresh, err := devScan(context.Background(), &errOut, cfg, true)
	if err != nil {
		t.Fatalf("--scan: %v (stderr %s)", err, errOut.String())
	}
	if fresh.FromCache {
		t.Error("--scan reused the cache instead of walking again")
	}
}

// TestCodeRootsDefaultFiltersToExistingDirectories guards --code-roots'
// default against listing directories this machine does not have: it should
// name only the ones that exist under $HOME.
func TestCodeRootsDefaultFiltersToExistingDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "code"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := codeRootsDefault()
	if len(got) != 1 || got[0] != "~/code" {
		t.Errorf("codeRootsDefault() = %v, want just [\"~/code\"]", got)
	}
}

// TestScanResolveCarriesCodeRoots guards the --code-roots flag plumbing: it
// must reach scan.Config.CodeRoots unchanged, since existingRoots (in
// internal/scan) is what filters it against the walked tree.
func TestScanResolveCarriesCodeRoots(t *testing.T) {
	o := scanOptions{threshold: "64KB", minSize: "10MB", codeRoots: []string{"~/code", "~/work"}}
	_, cfg, err := o.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CodeRoots) != 2 || cfg.CodeRoots[0] != "~/code" || cfg.CodeRoots[1] != "~/work" {
		t.Errorf("code roots = %v", cfg.CodeRoots)
	}
}
