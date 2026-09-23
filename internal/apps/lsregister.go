package apps

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

// RegistryEntry is one LaunchServices registration: a path and the bundle id
// macOS believes lives there.
//
// The register is corroborating evidence only, because it is full of paths
// that no longer exist and copies that were never installed: Sparkle updater
// bundles under ~/Library/Caches, Raycast's own update copies under
// Application Support, and bundles dragged to the Trash. Those stale entries
// are the point rather than the noise. Treating an update copy as an
// installed application would hide every orphan on the machine, so the parser
// keeps the path and lets the caller decide what the location means.
type RegistryEntry struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// TeamID is the signing team, when the dump records one.
	TeamID string `json:"teamId,omitempty"`
	// Exists is the result of an lstat, filled in by Probe.
	Exists bool `json:"exists"`
	// InTrash is true for a path under a Trash directory.
	InTrash bool `json:"inTrash,omitempty"`
}

// lsHandleSuffix is the " (0x2160)" LaunchServices appends to every path.
var lsHandleSuffix = regexp.MustCompile(` \(0x[0-9a-fA-F]+\)\s*$`)

// ParseLSRegisterDump reads "lsregister -dump" and returns one entry per
// registered bundle.
//
// The dump is a flat sequence of "key:  value" lines in which a "path:" line
// opens a record and the "identifier:" line that follows closes it, with a
// dozen other keys in between and no delimiter of any kind. The parser is
// therefore deliberately forgiving: it tracks the most recent path, attaches
// the next identifier to it, and ignores everything it does not recognise, so
// a format change costs a field rather than the whole listing.
func ParseLSRegisterDump(r io.Reader) []RegistryEntry {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var out []RegistryEntry
	seen := make(map[string]bool)
	var curPath, curTeam string
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "path":
			curPath, curTeam = lsHandleSuffix.ReplaceAllString(val, ""), ""
		case "teamID":
			curTeam = val
		case "identifier":
			if curPath == "" || val == "" {
				continue
			}
			k := val + "\x00" + curPath
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, RegistryEntry{
				ID:      val,
				Path:    curPath,
				TeamID:  curTeam,
				InTrash: PathInTrash(curPath),
			})
		}
	}
	return out
}

// PathInTrash reports whether a path lies under a user or volume Trash.
func PathInTrash(p string) bool {
	return strings.Contains(p, "/.Trash/") || strings.Contains(p, "/.Trashes/") ||
		strings.HasSuffix(p, "/.Trash") || strings.HasSuffix(p, "/.Trashes")
}

// IsAppPath reports whether a path names an application bundle rather than
// something inside one. LaunchServices registers helpers, XPC services and
// framework-embedded bundles by the thousand, and none of them is an install.
func IsAppPath(p string) bool {
	if !strings.HasSuffix(strings.ToLower(p), ".app") {
		return false
	}
	trimmed := strings.TrimSuffix(p, "/")
	i := strings.LastIndex(strings.ToLower(trimmed), ".app/")
	return i < 0
}

// NestedInBundle reports whether a path lies inside another bundle, which is
// what marks a helper or a framework copy rather than an installation.
func NestedInBundle(p string) bool {
	lower := strings.ToLower(p)
	for _, marker := range []string{".app/", ".framework/", ".xpc/", ".appex/"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
