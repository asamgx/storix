package mac

import (
	"path/filepath"
	"strings"
)

// bundleExts are the directory extensions macOS treats as a single object.
// Source: docs/02-storage-model.md "Bundles as leaves". Keys are lowercase;
// lookups are case-insensitive because ".savedState" and ".prefPane" are
// conventionally mixed case on disk.
var bundleExts = map[string]struct{}{
	".app":           {},
	".framework":     {},
	".photoslibrary": {},
	".musiclibrary":  {},
	".tvlibrary":     {},
	".xcodeproj":     {},
	".xcworkspace":   {},
	".playground":    {},
	".pvm":           {},
	".utm":           {},
	".vmwarevm":      {},
	".sparsebundle":  {},
	".sparseimage":   {},
	".dmg":           {},
	".pkg":           {},
	".savedstate":    {},
	".download":      {},
	".appex":         {},
	".qlgenerator":   {},
	".kext":          {},
	".bundle":        {},
	".plugin":        {},
	".prefpane":      {},
	".lproj":         {},
}

// IsBundleName reports whether a directory with this name is a bundle that
// should be sized fully but shown as one row. The argument is a base name,
// not a path; a path is reduced to its base name first.
func IsBundleName(name string) bool {
	base := filepath.Base(name)
	ext := filepath.Ext(base)
	if ext == "" || ext == base {
		return false
	}
	_, ok := bundleExts[strings.ToLower(ext)]
	return ok
}
