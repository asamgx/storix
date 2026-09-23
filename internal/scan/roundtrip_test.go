// The round trip lives in the external test package because it renders the
// result with internal/report, which imports internal/scan.
package scan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/units"
)

// TestAScanRendersTheSameFromTheCache is the promise the cache makes: what
// comes back out of it is the scan that went in, down to the JSON document.
//
// Everything that is genuinely about this run rather than about the scan is
// excluded: where the file is, how old it is, and how long each stage took.
// The fixture is built under a home of its own, and Config.Home points at it.
// Without that, nothing in the tree is anchored under "~" and the whole
// classification is empty: the round trip would compare one document with no
// claims in it against another, and pass whatever the engine did.
func TestAScanRendersTheSameFromTheCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("Users/andrew/Library/Caches/pkg/blob", 400_000)
	f.File("Users/andrew/Library/Preferences/tiny.plist", 120)
	f.File("Users/andrew/Library/Application Support/Slack/data", 300_000)
	f.File("Users/andrew/Movies/holiday.mov", 2_000_000)
	f.File("Users/andrew/Documents/notes.txt", 150_000)
	f.File("Applications/Thing.app/Contents/MacOS/thing", 900_000)
	f.Sparse("Users/andrew/Images/disk.img", 8<<20)
	f.Hardlink("Users/andrew/Movies/holiday.mov", "Users/andrew/Movies/holiday-link.mov")
	f.Symlink("holiday.mov", "Users/andrew/Movies/latest.mov")

	cfg := scan.Config{
		Roots:       []string{f.Root},
		Home:        f.Path("Users/andrew"),
		Units:       units.Decimal,
		Version:     "test-1.0",
		Parallelism: 4,
	}
	cached, err := scan.WithCache(cfg)
	if err != nil {
		t.Fatalf("WithCache: %v", err)
	}
	live, err := scan.Run(context.Background(), cached)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if live.Class == nil || len(live.Class.Claims) == 0 {
		t.Fatal("the fixture produced no claims, so the round trip would compare two empty classifications")
	}
	if live.PersistErr != nil {
		t.Fatalf("the scan was not cached: %v", live.PersistErr)
	}
	if live.CachePath == "" {
		t.Fatal("Run left no cache path")
	}

	loaded, ok, err := scan.LoadLatest(cfg)
	if err != nil || !ok {
		t.Fatalf("LoadLatest = %v, %v", ok, err)
	}
	if !loaded.FromCache || loaded.CachePath != live.CachePath {
		t.Errorf("loaded from %q, FromCache=%v", loaded.CachePath, loaded.FromCache)
	}

	o := report.Options{Units: units.Decimal, Full: true, Version: "test-1.0"}
	if diff := jsonDiff(t, live, loaded, o); diff != "" {
		t.Errorf("the cached scan renders differently:\n%s", diff)
	}
}

// jsonDiff renders both results and returns the first key whose value
// differs, or "" when they match.
func jsonDiff(t *testing.T, a, b *scan.Result, o report.Options) string {
	t.Helper()
	da, db := renderJSON(t, a, o), renderJSON(t, b, o)
	for _, k := range keys(da, db) {
		va, _ := json.Marshal(da[k])
		vb, _ := json.Marshal(db[k])
		if !bytes.Equal(va, vb) {
			return truncateDiff(k, string(va), string(vb))
		}
	}
	return ""
}

// renderJSON writes the report and parses it back, dropping the fields that
// describe this run rather than the scan.
func renderJSON(t *testing.T, r *scan.Result, o report.Options) map[string]json.RawMessage {
	t.Helper()
	var b bytes.Buffer
	if err := report.JSON(&b, r, o); err != nil {
		t.Fatalf("report.JSON: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("parse the report: %v", err)
	}
	for _, k := range []string{"from_cache", "cache_age_ns", "cache_path", "timing"} {
		delete(doc, k)
	}
	doc["classification"] = untimed(t, doc["classification"])
	return doc
}

// untimed drops the classification's own timing, which is the one number in
// the section that describes this run rather than the scan: on a live scan it
// includes the wait for the probes, and on a cached one it is how long the
// reclassification took.
func untimed(t *testing.T, section json.RawMessage) json.RawMessage {
	t.Helper()
	if len(section) == 0 {
		return section
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(section, &m); err != nil {
		t.Fatalf("parse the classification: %v", err)
	}
	delete(m, "timing_ns")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func keys(a, b map[string]json.RawMessage) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]json.RawMessage{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

// truncateDiff keeps the report of a mismatch readable when the value is a
// whole tree.
func truncateDiff(key, a, b string) string {
	const maxLen = 400
	clip := func(s string) string {
		if len(s) > maxLen {
			return s[:maxLen] + "…"
		}
		return s
	}
	return "  " + key + "\n   live:   " + clip(a) + "\n   cached: " + clip(b)
}
