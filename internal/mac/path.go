// Package mac holds the macOS-specific primitives storix needs: translation
// between data-volume scan paths and user-visible display paths, bundle
// recognition, the dataless-materialization IO policy, purgeable space,
// volume attributes via getattrlist, terminal identity, a Full Disk Access
// probe and sudo helpers.
//
// Everything that needs cgo lives in the *_cgo.go files behind the
// "darwin && cgo" build tag; the *_nocgo.go twins return [ErrUnavailable] so
// a CGO_ENABLED=0 build still compiles and reports honestly.
//
// This package never opens a file for reading. The single directory listing
// it performs is the Full Disk Access probe in tcc.go.
package mac

import (
	"os"
	"path/filepath"
	"strings"
)

// DataRoot is the mount point of the APFS data volume. It is the single scan
// root: walking "/" instead would cross firmlinks and double count, because
// the sealed system volume and the data volume share st_dev.
const DataRoot = "/System/Volumes/Data"

// ScanPath translates a user-visible path into its path on the data volume:
// "~/Library/X" and "/Users/u/Library/X" both become
// "/System/Volumes/Data/Users/u/Library/X".
//
// Paths that already live under DataRoot are returned cleaned. Paths that do
// not cross a firmlink are returned unchanged: "/dev", "/System/Volumes/VM"
// and the like are separate volumes, not views onto the data volume. A
// relative path is returned cleaned and untouched. See firmlinks.go.
func ScanPath(display string) string {
	p := expandHome(display)
	if !filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	p = filepath.Clean(p)
	if p == "/" {
		return DataRoot
	}
	if dp, ok := DataVolumePath(p); ok {
		return dp
	}
	return p
}

// DisplayPath strips the DataRoot prefix so paths read the way Finder and the
// shell show them ("/Users/u/Library", "/Applications/Safari.app"). Paths
// outside the data volume are returned cleaned and unchanged.
func DisplayPath(scanPath string) string {
	p := filepath.Clean(scanPath)
	switch {
	case p == DataRoot:
		return "/"
	case strings.HasPrefix(p, DataRoot+"/"):
		return p[len(DataRoot):]
	}
	return p
}

// expandHome replaces a leading "~" with the current user's home directory.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
