package mac

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// TCCProbe is the result of testing whether the process can read a
// TCC-protected location, which is what Full Disk Access grants.
type TCCProbe struct {
	Path    string // the location that was probed
	Granted bool
	Err     error // why the probe failed, nil when granted
}

// fdaCandidates are TCC-protected directories, in probe order. The first one
// that exists decides the answer.
var fdaCandidates = []string{"Library/Mail", "Library/Safari"}

// ProbeFullDiskAccess reports whether this process can read TCC-protected
// user data. It lists a protected directory: EPERM means Full Disk Access has
// not been granted to the terminal application.
//
// This is the only directory listing in package mac. Nothing here opens a
// file, so no iCloud content is ever materialized.
func ProbeFullDiskAccess() TCCProbe {
	home, err := os.UserHomeDir()
	if err != nil {
		return TCCProbe{Err: err}
	}
	var last TCCProbe
	for _, rel := range fdaCandidates {
		p := filepath.Join(home, rel)
		if _, err := os.ReadDir(p); err == nil {
			return TCCProbe{Path: p, Granted: true}
		} else if errors.Is(err, fs.ErrNotExist) {
			last = TCCProbe{Path: p, Err: err}
			continue // nothing to learn from a location that is not there
		} else {
			return TCCProbe{Path: p, Err: err}
		}
	}
	if last.Path == "" {
		last.Err = errors.New("no TCC-protected probe location found")
	}
	return last
}

// FullDiskAccessHint is the advice to print when the probe failed.
func FullDiskAccessHint(app string) string {
	if app == "" {
		app = "your terminal"
	}
	return "grant Full Disk Access to " + app + " in System Settings → Privacy & Security"
}
