package detect

import (
	"encoding/json"
	"fmt"
)

// The codec is what makes a cached scan re-classifiable. A detector's facts
// are stored as its own JSON and come back through the registry: the cache
// file names the detector, this package finds it and asks it for an empty
// value of its fact type to decode into. Nothing here knows what any detector
// stores, which is why a new detector needs no edit in the cache reader.

// Find returns the enabled detector of that name.
func (r *Registry) Find(name string) (Detector, bool) {
	if r == nil || name == "" {
		return nil, false
	}
	for _, d := range r.dets {
		if d.Name() == name {
			return d, true
		}
	}
	return nil, false
}

// Disabled lists the detectors the user switched off, and the names a
// --disable-detector matched nothing with. A caller reading a cache file uses
// it to tell "you turned this one off on this run" from "this build has never
// heard of it".
func (r *Registry) Disabled() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.disabled...)
}

// DecodeFacts decodes a stored facts document into the named detector's own
// fact type, looking the detector up in the default registry.
//
// An unknown name is an error rather than a silently dropped section: a cache
// written by a build with a detector this one does not have is a fact the
// reader should report, not hide.
func DecodeFacts(name string, raw json.RawMessage) (Facts, error) {
	det, ok := Default().Find(name)
	if !ok {
		return nil, fmt.Errorf("detect: no detector named %q in this build", name)
	}
	return DecodeFactsFor(det, raw)
}

// DecodeFactsFor is DecodeFacts for a caller that already holds the detector,
// which is the case when it is walking a registry that the scan configured.
//
// An empty or null document decodes to nil facts, which is what a detector
// whose probe found nothing stored: its Classify falls back to the static
// paths, exactly as it does on a live scan.
func DecodeFactsFor(det Detector, raw json.RawMessage) (Facts, error) {
	if det == nil {
		return nil, fmt.Errorf("detect: no detector to decode into")
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	f := det.NewFacts()
	if f == nil {
		return nil, nil
	}
	if err := json.Unmarshal(raw, f); err != nil {
		return nil, fmt.Errorf("detect: decoding the %s facts: %w", det.Name(), err)
	}
	return f, nil
}

// ParseState maps a state name back to its State. It is the inverse of
// State.String, which is how a status survives the cache: the number would
// silently change meaning if a state were ever inserted in the middle of the
// list, and the name cannot.
func ParseState(name string) (State, bool) {
	for i, s := range stateNames {
		if s == name {
			return State(i), true
		}
	}
	return NotProbed, false
}

// Verified reports whether a detector has been run against a machine that had
// the tool. It is the exported form of the test the runner applies, for a
// caller building a status for a detector that was never probed at all.
func Verified(det Detector) bool { return verified(det) }
