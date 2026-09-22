package walk

import (
	"strings"
	"syscall"

	"github.com/asamgx/storix/internal/mac"
)

// DefaultSkipNames are directory names never walked, whatever their location.
// They hold filesystem-internal data that is either unreadable or meaningless
// to the ledger.
var DefaultSkipNames = []string{
	".fseventsd",
	".Spotlight-V100",
	".DocumentRevisions-V100",
}

// DefaultSkipPaths are absolute scan paths never walked. Swap is sized from
// the VM volume's statfs instead.
var DefaultSkipPaths = []string{
	mac.DataRoot + "/private/var/vm",
}

// DefaultExemptPrefixes are path prefixes under which every leaf is retained,
// however small, because the app detectors in phase 1b key on those file
// names. They are relative to the scan root; a single "*" matches exactly one
// path segment (the user name).
var DefaultExemptPrefixes = []string{
	"Users/*/Library/Preferences",
	"Users/*/Library/Cookies",
	"Users/*/Library/LaunchAgents",
	"Users/*/Library/Saved Application State",
	"Library/LaunchAgents",
	"Library/LaunchDaemons",
	"private/var/db/receipts",
}

// prefixMatcher tests whether a path lies at or under one of a set of
// patterns. Patterns are absolute scan paths; "*" matches one segment.
type prefixMatcher struct{ patterns [][]string }

// newPrefixMatcher resolves patterns against root: an absolute pattern is
// taken as is, a relative one such as "Users/*/Library/Preferences" is
// resolved as root + "/" + pattern.
func newPrefixMatcher(root string, patterns []string) prefixMatcher {
	var m prefixMatcher
	rootSegs := splitPath(root)
	for _, p := range patterns {
		segs := splitPath(p)
		if len(segs) == 0 {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			segs = append(append([]string(nil), rootSegs...), segs...)
		}
		m.patterns = append(m.patterns, segs)
	}
	return m
}

// match reports whether path is at or under one of the patterns.
func (m prefixMatcher) match(path string) bool {
	if len(m.patterns) == 0 {
		return false
	}
	segs := splitPath(path)
	for _, pat := range m.patterns {
		if matchSegments(segs, pat) {
			return true
		}
	}
	return false
}

// matchSegments reports whether pat is a prefix of segs, with "*" matching any
// single segment.
func matchSegments(segs, pat []string) bool {
	if len(pat) > len(segs) {
		return false
	}
	for i, p := range pat {
		if p != "*" && p != segs[i] {
			return false
		}
	}
	return true
}

// splitPath splits an absolute path into its non-empty segments.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// dataVaultMarkers are paths that stay unreadable even with Full Disk Access.
// A match turns EPERM into ErrProtected, which the report presents as "the
// system protects this", not as a permission problem the user can fix.
var dataVaultMarkers = []string{
	"/Library/Caches/com.apple.ap.adprivacyd",
	"/.com.apple.containermanagerd.metadata.plist",
}

// isDataVault reports whether path is a known data vault.
func isDataVault(path string) bool {
	for _, m := range dataVaultMarkers {
		if strings.Contains(path, m) {
			return true
		}
	}
	return false
}

// classify maps an errno on a path to an ErrClass.
func classify(path string, errno syscall.Errno) ErrClass {
	switch errno {
	case syscall.EACCES:
		return ErrPermission
	case syscall.EPERM:
		if isDataVault(path) {
			return ErrProtected
		}
		return ErrTCC
	case syscall.ENOENT:
		return ErrVanished
	default:
		return ErrOther
	}
}
