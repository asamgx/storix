package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/units"
)

func TestResolveRejectsContradictoryFlags(t *testing.T) {
	cases := []struct {
		name string
		opts scanOptions
		want string
	}{
		{"both outputs", scanOptions{report: true, json: true}, "not both"},
		{"bad threshold", scanOptions{threshold: "banana", minSize: "10MB"}, "--threshold"},
		{"bad min size", scanOptions{threshold: "64KB", minSize: "banana"}, "--min-size"},
		{"negative top", scanOptions{threshold: "64KB", minSize: "10MB", top: -1}, "--top"},
		{"negative depth", scanOptions{threshold: "64KB", minSize: "10MB", depth: -1}, "--depth"},
		{"negative parallelism", scanOptions{threshold: "64KB", minSize: "10MB", parallelism: -1}, "--parallelism"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := c.opts.resolve()
			if err == nil {
				t.Fatalf("resolve accepted %+v", c.opts)
			}
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Errorf("error %v is not a ConfigError, so the exit code would be wrong", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestResolveMapsFlagsOntoOptions(t *testing.T) {
	o := scanOptions{threshold: "1MB", minSize: "5MB", top: 10, depth: 2, binary: true, roots: []string{"/tmp"}}
	ro, cfg, err := o.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ro.Units != units.Binary || cfg.Units != units.Binary {
		t.Error("--binary did not select binary units")
	}
	if cfg.SmallFileThreshold != 1_000_000 {
		t.Errorf("threshold = %d, want 1 MB", cfg.SmallFileThreshold)
	}
	if ro.MinSize != 5_000_000 || ro.Top != 10 || ro.Depth != 2 {
		t.Errorf("report options = %+v", ro)
	}
	if ro.Version == "" {
		t.Error("the JSON document would carry no version")
	}
}

// TestZeroMeansNoneNotDefault guards the one place the CLI and the report
// package disagree about zero: to a user "--top 0" means none, while an unset
// report.Options field has to mean the default.
func TestZeroMeansNoneNotDefault(t *testing.T) {
	o := scanOptions{threshold: "64KB", minSize: "0", top: 0, depth: 0}
	ro, _, err := o.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ro.Top >= 0 {
		t.Errorf("Top = %d, want a negative value so the section is dropped", ro.Top)
	}
	if ro.Depth >= 0 {
		t.Errorf("Depth = %d, want a negative value so only the root is emitted", ro.Depth)
	}
	if ro.MinSize >= 0 {
		t.Errorf("MinSize = %d, want a negative value so nothing is filtered out", ro.MinSize)
	}
}

func TestRunScanWritesAReport(t *testing.T) {
	f := testutil.New(t)
	f.File("big.bin", 300_000)
	f.Dir("sub")
	f.File("sub/a.bin", 100_000)

	var out, errOut bytes.Buffer
	o := &scanOptions{roots: []string{f.Root}, threshold: "64KB", minSize: "10MB", top: report.DefaultTop, depth: report.DefaultDepth}
	if err := runScan(context.Background(), &out, &errOut, o); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"storix scan", "VOLUME", "scanned", "residual", "verdict", "WALK"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report has no %q section:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Error("the report is colored although it was written to a buffer")
	}
}

func TestRunScanWritesJSON(t *testing.T) {
	f := testutil.New(t)
	f.File("big.bin", 300_000)

	var out, errOut bytes.Buffer
	o := &scanOptions{roots: []string{f.Root}, threshold: "64KB", minSize: "10MB", json: true, depth: 3, top: 25}
	if err := runScan(context.Background(), &out, &errOut, o); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema int    `json:"schema"`
		Root   string `json:"root"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("the output is not valid JSON: %v\n%s", err, out.String())
	}
	if doc.Schema != report.SchemaVersion {
		t.Errorf("schema = %d, want %d", doc.Schema, report.SchemaVersion)
	}
	if doc.Root == "" {
		t.Error("the document names no root")
	}
}

func TestRunScanRejectsAMissingRoot(t *testing.T) {
	f := testutil.New(t)
	var out, errOut bytes.Buffer
	o := &scanOptions{roots: []string{filepath.Join(f.Root, "nope")}, threshold: "64KB", minSize: "10MB", top: 25, depth: 3}
	err := runScan(context.Background(), &out, &errOut, o)
	if err == nil {
		t.Fatal("a missing root was accepted")
	}
	if got := ExitCode(err); got != ExitConfigError {
		t.Errorf("exit code = %d, want %d", got, ExitConfigError)
	}
}

func TestRunScanWarnsWhenSystemIsAskedForWithoutRoot(t *testing.T) {
	f := testutil.New(t)
	var out, errOut bytes.Buffer
	o := &scanOptions{roots: []string{f.Root}, threshold: "64KB", minSize: "10MB", system: true, top: 25, depth: 3}
	if err := runScan(context.Background(), &out, &errOut, o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "sudo") {
		t.Errorf("stderr = %q, want the sudo advice", errOut.String())
	}
}

func TestCancelledScanExitsAsInterrupted(t *testing.T) {
	f := testutil.New(t)
	f.Wide("many", 100)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out, errOut bytes.Buffer
	o := &scanOptions{roots: []string{f.Root}, threshold: "64KB", minSize: "10MB", top: 25, depth: 3}
	err := runScan(ctx, &out, &errOut, o)
	if got := ExitCode(err); got != ExitInterrupted {
		t.Errorf("exit code = %d, want %d (err %v)", got, ExitInterrupted, err)
	}
	if !strings.Contains(out.String(), "INCOMPLETE") {
		t.Error("the partial report does not say it is incomplete")
	}
}

func TestFmtCountGroupsDigits(t *testing.T) {
	cases := map[uint64]string{0: "0", 12: "12", 1234: "1,234", 1234567: "1,234,567"}
	for in, want := range cases {
		if got := fmtCount(in); got != want {
			t.Errorf("fmtCount(%d) = %q, want %q", in, got, want)
		}
	}
}
