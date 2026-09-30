package detect

import "strings"

// Scoped is one evidence line together with the path it is about.
//
// A detector that asks several questions — npm, pnpm, yarn and nvm for the
// node detector — gathers one answer per question, and attaching all of them
// to every directory it claims made `storix explain ~/Library/pnpm` recite
// yarn's version and nvm's default. Scoping each answer to the path it
// describes lets the claim for a directory carry only the answers about it.
type Scoped struct {
	// Path is the display path the line is about; empty applies it to
	// every claim, which is right for "the detector did not answer".
	Path string
	Text string
}

// EvidenceFor keeps the lines whose scope is the target, lies inside it, or
// contains it, plus the unscoped ones, in their original order.
func EvidenceFor(lines []Scoped, target string) []string {
	var out []string
	for _, l := range lines {
		if l.Path == "" || related(l.Path, target) {
			out = append(out, l.Text)
		}
	}
	return out
}

// related reports whether one path is the other or either lies inside the
// other.
func related(a, b string) bool {
	a, b = strings.TrimRight(a, "/"), strings.TrimRight(b, "/")
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// RebaseScoped moves each line's scope into Classify's home, so a scope measured
// against the probe's home still matches the targets, which are rebased the
// same way. A scope the measure refuses keeps its measured spelling.
func RebaseScoped(lines []Scoped, m *Measure) []Scoped {
	out := make([]Scoped, len(lines))
	for i, l := range lines {
		out[i] = l
		if l.Path != "" && m != nil {
			if p := m.Path(l.Path); p != "" {
				out[i].Path = p
			}
		}
	}
	return out
}

// ScopedFor is the evidence a target carries: the answers about its path,
// then the measure's notes, which are about the measurement as a whole.
func ScopedFor(lines []Scoped, m *Measure, target string) []string {
	out := EvidenceFor(lines, target)
	if m != nil {
		out = append(out, m.Notes...)
	}
	return out
}
