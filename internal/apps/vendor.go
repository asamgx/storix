package apps

import (
	"regexp"
	"strings"
)

// teamIDPattern is an Apple Developer team identifier: ten upper-case
// alphanumerics. It appears as the first component of a group container name
// ("HUAQ24HBR6.dev.orbstack") and as codesign's TeamIdentifier.
var teamIDPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// appleGroupRemainder matches the part of a group container name that macOS
// owns even though the container is namespaced by a team id, such as
// "243LU875E5.groups.com.apple.podcasts". Apple's own team ids must not drag
// the codesign gate open, and their containers are the rule catalog's job.
var appleGroupRemainder = regexp.MustCompile(`^groups?\.com\.apple\.`)

// platformPrefixes are two-label prefixes that name a hosting platform rather
// than a vendor. Two applications sharing "com.electron" have nothing to do
// with each other, so for these the vendor is the first three labels and only
// exact or alias matching applies: com.electron.kontena-lens (Lens) must
// never attach to com.electron.ollama (Ollama).
var platformPrefixes = map[string]bool{
	"com.github":      true,
	"io.github":       true,
	"com.electron":    true,
	"com.todesktop":   true,
	"org.sourceforge": true,
	"net.sourceforge": true,
	"com.apple":       true,
}

// ReverseDNS is a parsed reverse-DNS identifier.
type ReverseDNS struct {
	// Name is the identifier after the "group." prefix and any team id
	// have been stripped: what steps 1, 4, 5 and 7 of resolution match on.
	Name string
	// Labels are Name's dot-separated components.
	Labels []string
	// Vendor is the prefix that identifies the publisher: the first two
	// labels, or the first three when the first two name a platform. A
	// two-label identifier is its own vendor, so that "notion.id" and
	// "notion.id.ShipIt" agree on one vendor.
	Vendor string
	// Platform is true when the vendor prefix was widened because the
	// first two labels name a hosting platform.
	Platform bool
	// TeamID is set when the original name began with a team identifier.
	TeamID string
	// Grouped is true when the original name began with "group.".
	Grouped bool
}

// Suffix is the labels after the vendor, joined: the part that distinguishes
// one of a vendor's products from another.
func (r ReverseDNS) Suffix() string {
	if len(r.Vendor) >= len(r.Name) {
		return ""
	}
	return r.Name[len(r.Vendor)+1:]
}

// IsApple reports whether the identifier belongs to macOS itself.
func (r ReverseDNS) IsApple() bool {
	return r.Name == "com.apple" || strings.HasPrefix(r.Name, "com.apple.")
}

// ParseReverseDNS splits a directory or container name into a reverse-DNS
// identifier. It reports false for anything that is a display name rather
// than an identifier, which is the distinction the whole resolution order
// rests on: "Smart Code ltd" and "Code" are names, "com.microsoft.VSCode" is
// an id, and the two are matched by different steps.
//
// A leading "group." and a leading team identifier are stripped first and
// recorded on the result, so the remainder can be matched as an ordinary id.
func ParseReverseDNS(name string) (ReverseDNS, bool) {
	var out ReverseDNS
	s := strings.TrimSpace(name)
	if s == "" || strings.ContainsAny(s, " \t/") {
		return out, false
	}
	// A file extension means the caller handed over a file name rather
	// than an identifier; Location normalisation strips those first.
	switch {
	case strings.HasSuffix(s, ".plist"), strings.HasSuffix(s, ".app"),
		strings.HasSuffix(s, ".savedState"), strings.HasSuffix(s, ".binarycookies"):
		return out, false
	}
	if rest, ok := strings.CutPrefix(s, "group."); ok {
		out.Grouped = true
		s = rest
	}
	labels := strings.Split(s, ".")
	if teamIDPattern.MatchString(labels[0]) && len(labels) > 1 {
		out.TeamID = labels[0]
		labels = labels[1:]
		s = strings.Join(labels, ".")
	}
	if len(labels) < 2 {
		return out, false
	}
	for _, l := range labels {
		if l == "" {
			return out, false
		}
	}
	// The first label is the top-level domain of the identifier and is
	// lower case by convention; requiring it is what keeps "Smart Code
	// ltd" and "Beyond Compare 5" out of the identifier path.
	if labels[0] != strings.ToLower(labels[0]) {
		return out, false
	}
	out.Name, out.Labels = s, labels
	out.Vendor = strings.Join(labels[:2], ".")
	if platformPrefixes[out.Vendor] && len(labels) >= 3 {
		out.Vendor = strings.Join(labels[:3], ".")
		out.Platform = true
	} else if platformPrefixes[out.Vendor] {
		out.Platform = true
	}
	return out, true
}

// IsTeamID reports whether s is an Apple Developer team identifier.
func IsTeamID(s string) bool { return teamIDPattern.MatchString(s) }

// TeamIDPrefix returns the team identifier a group container name begins
// with, and the rest of the name. Containers Apple owns through one of its
// own team ids report false: their bytes belong to the rule catalog, and
// letting them through would run codesign over every bundle on the machine
// for no attribution at all.
func TeamIDPrefix(name string) (team, rest string, ok bool) {
	i := strings.IndexByte(name, '.')
	if i <= 0 || !teamIDPattern.MatchString(name[:i]) {
		return "", "", false
	}
	rest = name[i+1:]
	if rest == "" || appleGroupRemainder.MatchString(rest) ||
		rest == "com.apple" || strings.HasPrefix(rest, "com.apple.") {
		return "", "", false
	}
	return name[:i], rest, true
}

// stripGroupPrefixes removes the wrappers a group container name can carry
// around an ordinary identifier: "group." and the "--AppIdentifierPrefix-"
// marker macOS writes for containers declared without a team id.
func stripGroupPrefixes(name string) string {
	s := name
	if rest, ok := strings.CutPrefix(s, "--AppIdentifierPrefix-"); ok {
		s = rest
	}
	if rest, ok := strings.CutPrefix(s, "group."); ok {
		s = rest
	}
	return s
}
