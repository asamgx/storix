package apps

import (
	"path"
	"strings"

	"howett.net/plist"
)

// LaunchItem is one launchd job definition from a LaunchAgents or
// LaunchDaemons directory.
//
// A launch item whose program still exists is a keep signal: something on
// this machine is still configured to run, so the software behind it is not
// gone however absent its bundle is. An item whose program cannot be
// determined is deliberately not a keep signal. Google's keystone agent plist
// names neither Program nor ProgramArguments in the form a naive parse
// expects, and treating "I could not tell" as "it is alive" would suppress
// real orphans.
type LaunchItem struct {
	// Path is the display path of the plist.
	Path string `json:"path"`
	// Label is the job's label, falling back to the file name.
	Label string `json:"label,omitempty"`
	// Program is the executable, from Program, ProgramArguments[0] or
	// BundleProgram. Empty means the plist named none this parser
	// understood, which the report prints as "program unknown".
	Program string `json:"program,omitempty"`
	// ProgramExists is the result of an lstat on Program, filled in by
	// Probe. False covers both "the program is gone" and "the program
	// could not be checked"; CheckErr tells them apart.
	ProgramExists bool `json:"programExists,omitempty"`
	// CheckErr is why Program could not be stat'd, empty when it could.
	CheckErr string `json:"checkErr,omitempty"`
	// BundleIDs are AssociatedBundleIdentifiers, the modern way a helper
	// names the application it belongs to.
	BundleIDs []string `json:"bundleIds,omitempty"`
	// System is true for an item under /Library rather than the user's.
	System bool `json:"system,omitempty"`
}

// launchdPlist is the decoded subset of a job definition. Every field is
// optional: a launchd plist is valid with nothing but a Label.
type launchdPlist struct {
	Label                       string   `plist:"Label"`
	Program                     string   `plist:"Program"`
	ProgramArguments            []string `plist:"ProgramArguments"`
	BundleProgram               string   `plist:"BundleProgram"`
	AssociatedBundleIdentifiers []string `plist:"AssociatedBundleIdentifiers"`
	AssociatedBundleIdentifier  string   `plist:"AssociatedBundleIdentifier"`
}

// ParseLaunchPlist decodes one launchd job definition. A plist that will not
// decode still yields an item with the label taken from its file name, so a
// job the parser cannot read is visible rather than invisible.
func ParseLaunchPlist(plistPath string, data []byte, system bool) (LaunchItem, error) {
	item := LaunchItem{
		Path:   plistPath,
		Label:  strings.TrimSuffix(path.Base(plistPath), ".plist"),
		System: system,
	}
	var doc launchdPlist
	if _, err := plist.Unmarshal(data, &doc); err != nil {
		return item, err
	}
	if strings.TrimSpace(doc.Label) != "" {
		item.Label = strings.TrimSpace(doc.Label)
	}
	switch {
	case strings.TrimSpace(doc.Program) != "":
		item.Program = strings.TrimSpace(doc.Program)
	case len(doc.ProgramArguments) > 0 && strings.TrimSpace(doc.ProgramArguments[0]) != "":
		item.Program = strings.TrimSpace(doc.ProgramArguments[0])
	case strings.TrimSpace(doc.BundleProgram) != "":
		item.Program = strings.TrimSpace(doc.BundleProgram)
	}
	item.BundleIDs = append(item.BundleIDs, doc.AssociatedBundleIdentifiers...)
	if strings.TrimSpace(doc.AssociatedBundleIdentifier) != "" {
		item.BundleIDs = append(item.BundleIDs, strings.TrimSpace(doc.AssociatedBundleIdentifier))
	}
	dedupeInPlace(&item.BundleIDs)
	return item, nil
}

// Describe is the one line the why panel prints for a launch item.
func (l LaunchItem) Describe() string {
	switch {
	case l.Program == "":
		return "launch item " + l.Label + " present, program unknown"
	case l.ProgramExists:
		return "launch item " + l.Label + " runs " + l.Program
	case l.CheckErr != "":
		return "launch item " + l.Label + " points at " + l.Program + ", which could not be checked"
	default:
		return "launch item " + l.Label + " points at " + l.Program + ", which is missing"
	}
}

// Owns reports whether a launch item belongs to an identifier or a vendor
// prefix: its label or one of its associated bundle ids matches, or its label
// is a suffixed form of the id, as "com.google.keystone.agent" is of the
// Keystone family.
func (l LaunchItem) Owns(id string) bool {
	if id == "" {
		return false
	}
	label, want := strings.ToLower(l.Label), strings.ToLower(id)
	if label == want || strings.HasPrefix(label+".", want+".") {
		return true
	}
	for _, b := range l.BundleIDs {
		if strings.EqualFold(b, id) {
			return true
		}
	}
	return false
}
