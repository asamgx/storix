package detect

import (
	"encoding/json"
	"testing"
)

// TestDecodeFactsForRoundTripsThroughTheDetector is the contract the cache
// relies on: the file names a detector, the detector supplies the empty value
// of its own fact type, and the stored document decodes into it. Nothing in
// the cache reader knows what any detector stores.
func TestDecodeFactsForRoundTripsThroughTheDetector(t *testing.T) {
	det := &fake{name: "codec"}
	f, err := DecodeFactsFor(det, json.RawMessage(`{"Kind_":"stored"}`))
	if err != nil {
		t.Fatalf("DecodeFactsFor: %v", err)
	}
	got, ok := f.(*fakeFacts)
	if !ok {
		t.Fatalf("decoded into %T, want the detector's own type", f)
	}
	if got.Kind() != "stored" {
		t.Errorf("kind = %q, want the stored one", got.Kind())
	}
}

// TestDecodeFactsForAcceptsAnAbsentDocument: a detector whose probe learned
// nothing stored nothing, and comes back with nil facts rather than an error.
// Its Classify then falls back to the static paths, exactly as on a live scan.
func TestDecodeFactsForAcceptsAnAbsentDocument(t *testing.T) {
	for _, raw := range []string{"", "null"} {
		f, err := DecodeFactsFor(&fake{name: "codec"}, json.RawMessage(raw))
		if err != nil {
			t.Errorf("DecodeFactsFor(%q): %v", raw, err)
		}
		if f != nil {
			t.Errorf("DecodeFactsFor(%q) = %v, want nil facts", raw, f)
		}
	}
}

func TestDecodeFactsForRejectsAMalformedDocument(t *testing.T) {
	if _, err := DecodeFactsFor(&fake{name: "codec"}, json.RawMessage(`{`)); err == nil {
		t.Error("a truncated document decoded without complaint")
	}
}

func TestDecodeFactsRefusesAnUnknownDetector(t *testing.T) {
	if _, err := DecodeFacts("no-such-detector", json.RawMessage(`{}`)); err == nil {
		t.Error("an unknown detector decoded without complaint")
	}
}

// TestParseStateIsTheInverseOfString is what keeps a cached status honest:
// the file carries the name, and every name this build prints must come back
// as the state it printed.
func TestParseStateIsTheInverseOfString(t *testing.T) {
	for i := range stateNames {
		s := State(i)
		got, ok := ParseState(s.String())
		if !ok || got != s {
			t.Errorf("ParseState(%q) = %s, %v, want %s", s.String(), got, ok, s)
		}
	}
	if _, ok := ParseState("wobbly"); ok {
		t.Error("an unknown state name parsed")
	}
}

func TestRegistryFindAndDisabled(t *testing.T) {
	reg := New(&fake{name: "one"}, &fake{name: "two"})
	if _, ok := reg.Find("one"); !ok {
		t.Error("Find did not find an enabled detector")
	}
	off := reg.Disable("two", "typo")
	if _, ok := off.Find("two"); ok {
		t.Error("Find returned a disabled detector")
	}
	want := map[string]bool{"two": true, "typo": true}
	got := off.Disabled()
	if len(got) != len(want) {
		t.Fatalf("Disabled() = %v, want %v", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("Disabled() holds %q", name)
		}
	}
}

// TestVerifiedIsExported keeps the exported form in step with the runner's
// own test, which is what a cache loader uses for a detector the file never
// mentioned.
func TestVerifiedIsExported(t *testing.T) {
	if !Verified(&fake{name: "seen"}) {
		t.Error("an ordinary detector is reported unverified")
	}
	if Verified(&fake{name: "unseen", unverified: true}) {
		t.Error("a detector written from documentation is reported verified")
	}
}
