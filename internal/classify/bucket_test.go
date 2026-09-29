package classify

import (
	"encoding/json"
	"testing"
)

// TestReclaimZeroIsNotATag: the zero value is what a literal without the
// field has, and it must never read as a tag, least of all a reclaimable one.
func TestReclaimZeroIsNotATag(t *testing.T) {
	var zero Reclaim
	if zero.Valid() {
		t.Error("the zero Reclaim is valid")
	}
	if zero.Reclaimable() {
		t.Error("the zero Reclaim is reclaimable")
	}
	if Reclaim(99).Valid() {
		t.Error("an out-of-range Reclaim is valid")
	}
	for r := Regenerable; r <= Unknown; r++ {
		if !r.Valid() {
			t.Errorf("%s is not valid", r)
		}
	}
}

// TestReclaimEncodesByName: the tag travels by name, so reordering the
// constants cannot change what a stored file or a report says, and a file
// written when the tag was a number still reads back as the tag it meant.
func TestReclaimEncodesByName(t *testing.T) {
	type doc struct {
		R Reclaim `json:"r"`
	}
	for r := Regenerable; r <= Unknown; r++ {
		b, err := json.Marshal(doc{R: r})
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"r":"` + r.String() + `"}`; string(b) != want {
			t.Errorf("%s encodes as %s, want %s", r, b, want)
		}
		var back doc
		if err := json.Unmarshal(b, &back); err != nil || back.R != r {
			t.Errorf("%s round-trips to %s (%v)", r, back.R, err)
		}
	}

	// An invalid tag is written as unknown: whoever reads the file back
	// must not find a reclaimable tag the writer never had.
	for _, r := range []Reclaim{0, 99} {
		b, _ := json.Marshal(doc{R: r})
		if string(b) != `{"r":"unknown"}` {
			t.Errorf("Reclaim(%d) encodes as %s, want unknown", r, b)
		}
	}

	legacy := map[string]Reclaim{
		`0`: Regenerable, `1`: ToolManaged, `2`: Orphaned, `3`: UserData, `4`: System, `5`: Unknown,
		`6`: Unknown, `1.5`: Unknown, `-1`: Unknown,
		`"orphaned"`: Orphaned, `"unset"`: Unknown, `"nonsense"`: Unknown,
	}
	for in, want := range legacy {
		var back doc
		if err := json.Unmarshal([]byte(`{"r":`+in+`}`), &back); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if back.R != want {
			t.Errorf("%s decodes as %s, want %s", in, back.R, want)
		}
	}
}

// TestFreeableIsRegenerableAndOrphanedOnly: the ledger's headline figure
// counts only what storix could free itself without losing anything the
// owner needs (D43). Tool-managed bytes are freed by their tool, which may
// keep what it considers live, so they are reclaimable but not freeable.
func TestFreeableIsRegenerableAndOrphanedOnly(t *testing.T) {
	want := map[Reclaim]bool{Regenerable: true, Orphaned: true}
	for r := Reclaim(0); int(r) < numReclaim; r++ {
		if got := r.Freeable(); got != want[r] {
			t.Errorf("%s.Freeable() = %v, want %v", r, got, want[r])
		}
	}
	var b BucketTotal
	b.ByReclaim[Regenerable] = 1
	b.ByReclaim[ToolManaged] = 10
	b.ByReclaim[Orphaned] = 100
	b.ByReclaim[UserData] = 1000
	b.ByReclaim[Unknown] = 10000
	if got := b.Freeable(); got != 101 {
		t.Errorf("Freeable() = %d, want 101: regenerable plus orphaned, never tool-managed", got)
	}
	if got := b.Reclaimable(); got != 111 {
		t.Errorf("Reclaimable() = %d, want 111: D43 leaves the classify predicate alone", got)
	}
}
