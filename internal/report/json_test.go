package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
)

// scanWithApps is the golden scan plus an application inventory, which the
// fake scan does not build: the inventory comes from a probe of a real
// machine, and the golden describes a machine that does not exist.
func scanWithApps() *scan.Result {
	r := fakeScan()
	r.Apps = &apps.Report{
		Schema: apps.ReportSchema,
		Counts: apps.Counts{Bundles: 1, Owners: 1, Candidates: 2},
		Apps: []apps.Entry{{
			Owner:      "app:com.spotify.client",
			Label:      "Spotify",
			State:      "installed",
			Confidence: "strong",
			Footprint:  apps.Sizes{Data: 7_000_000_000, Total: 7_000_000_000},
			Components: []apps.ComponentRef{{
				Path:   "/Users/u/Library/Caches/com.spotify.client",
				Bytes:  7_000_000_000,
				Bucket: "app-data",
			}},
		}},
	}
	return r
}

// TestJSONCarriesEveryDocumentedKey is the schema in test form: phase 1b adds
// fields to schema 1 and removes none, so every key docs/03 names must be
// present in a document rendered from a scan that has the data for it.
func TestJSONCarriesEveryDocumentedKey(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, scanWithApps(), Options{Units: units.Decimal, Version: "v0.0.0-test"}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("the document does not parse: %v", err)
	}
	for _, key := range []string{"schema", "ledger", "classification", "apps", "facts", "counters", "tree"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the document has no %q", key)
		}
	}

	var ledgerDoc struct {
		Buckets []struct {
			ID          string          `json:"id"`
			Label       string          `json:"label"`
			Bytes       int64           `json:"bytes"`
			Files       int64           `json:"files"`
			Known       bool            `json:"known"`
			Reclaimable int64           `json:"reclaimable"`
			ByReclaim   json.RawMessage `json:"by_reclaim"`
			Categories  json.RawMessage `json:"categories"`
			Owners      json.RawMessage `json:"owners"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(doc["ledger"], &ledgerDoc); err != nil {
		t.Fatal(err)
	}
	if len(ledgerDoc.Buckets) != len(classify.Buckets()) {
		t.Errorf("ledger.buckets holds %d rows, want the %d of docs/02",
			len(ledgerDoc.Buckets), len(classify.Buckets()))
	}
	for i, b := range ledgerDoc.Buckets {
		if b.ID == "" || b.Label == "" {
			t.Errorf("bucket %d has no id or label: %+v", i, b)
		}
		if want := classify.Buckets()[i].ID(); b.ID != want {
			t.Errorf("bucket %d is %q, want %q: the rows are in docs/02 order", i, b.ID, want)
		}
	}

	var class map[string]json.RawMessage
	if err := json.Unmarshal(doc["classification"], &class); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"detectors", "developer", "containers", "conflicts", "owners", "unmatched", "timing_ns"} {
		if _, ok := class[key]; !ok {
			t.Errorf("classification has no %q", key)
		}
	}
	var conflicts map[string]json.RawMessage
	if err := json.Unmarshal(class["conflicts"], &conflicts); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"total", "by_kind", "top"} {
		if _, ok := conflicts[key]; !ok {
			t.Errorf("classification.conflicts has no %q", key)
		}
	}
}

// TestJSONNodesCarryTheirClaim checks the per-node fields: a claimed node says
// which bucket it is in and what decided that, and a node that only inherits
// its parent's claim says so.
func TestJSONNodesCarryTheirClaim(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, fakeScan(), Options{Full: true}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tree jsonNode `json:"tree"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]jsonNode{}
	var walkNodes func(jsonNode)
	walkNodes = func(n jsonNode) {
		byPath[n.Path] = n
		for _, c := range n.Children {
			walkNodes(c)
		}
	}
	walkNodes(doc.Tree)

	cases := []struct {
		path      string
		bucket    string
		source    string
		inherited bool
	}{
		{path: "/Applications/Arc.app", bucket: "apps", source: "rule:apps.system.bundle"},
		{path: "/Applications/Arc.app/Arc", bucket: "apps", source: "rule:apps.system.bundle", inherited: true},
		{path: "/Users/u/.Trash", bucket: "trash"},
		{path: "/Users/u/Library/Caches/go-build", bucket: "developer"},
	}
	for _, c := range cases {
		n, ok := byPath[c.path]
		if !ok {
			t.Errorf("%s is not in the document", c.path)
			continue
		}
		if n.Bucket != c.bucket {
			t.Errorf("%s: bucket = %q, want %q", c.path, n.Bucket, c.bucket)
		}
		if c.source != "" && n.Source != c.source {
			t.Errorf("%s: source = %q, want %q", c.path, n.Source, c.source)
		}
		if n.Reclaim == "" {
			t.Errorf("%s: no reclaim tag", c.path)
		}
		if n.Inherited != c.inherited {
			t.Errorf("%s: inherited = %v, want %v", c.path, n.Inherited, c.inherited)
		}
	}

	// A node no rule reached carries none of the fields, which is what
	// makes "no bucket" a searchable answer rather than a guess.
	if n, ok := byPath["/weird-vendor-drop"]; !ok {
		t.Error("the unclassified directory is not in the document")
	} else if n.Bucket != "" || n.Source != "" {
		t.Errorf("an unclassified node claims bucket %q from %q", n.Bucket, n.Source)
	}
}

// jsonNode is the tree node as the document writes it, read back.
type jsonNode struct {
	Path      string     `json:"path"`
	Bucket    string     `json:"bucket"`
	Owner     string     `json:"owner"`
	Reclaim   string     `json:"reclaim"`
	Source    string     `json:"source"`
	Inherited bool       `json:"inherited"`
	Children  []jsonNode `json:"children"`
}

// TestJSONWithoutAClassificationStillRenders: the classification is optional
// data, so a result without one drops the section and the per-node fields
// rather than failing.
func TestJSONWithoutAClassificationStillRenders(t *testing.T) {
	r := fakeScan()
	r.Class = nil
	var buf bytes.Buffer
	if err := JSON(&buf, r, Options{Full: true}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["classification"]; ok {
		t.Error("a scan with no classification still emitted the section")
	}
	if bytes.Contains(buf.Bytes(), []byte(`"bucket"`)) {
		t.Error("a node claimed a bucket although the scan has no classification")
	}
}
