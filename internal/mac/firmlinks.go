package mac

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// firmlinksFile is the system's own list of firmlinks: directories that
// appear on the sealed system volume but whose contents live on the data
// volume. It is a few hundred bytes on the read-only system volume.
const firmlinksFile = "/usr/share/firmlinks"

// defaultFirmlinks is the macOS 26 list, used when firmlinksFile cannot be
// read. Every entry maps "/X" on the system volume to "X" on the data volume,
// so the translation is a plain DataRoot prefix; the list only says which
// paths are firmlinked at all. /dev, /System/Volumes/* and / itself are not.
var defaultFirmlinks = []string{
	"/AppleInternal",
	"/Applications",
	"/Library",
	"/System/Library/Assets",
	"/System/Library/AssetsV2",
	"/System/Library/Caches",
	"/System/Library/CoreServices/CoreTypes.bundle/Contents/Library",
	"/System/Library/PreinstalledAssets",
	"/System/Library/PreinstalledAssetsV2",
	"/System/Library/Speech",
	"/Users",
	"/Volumes",
	"/cores",
	"/opt",
	"/pkg",
	"/private",
	"/usr/libexec/cups",
	"/usr/local",
	"/usr/share/snmp",
}

// extraDataVolumeRoots are paths that reach the data volume without being
// firmlinks. macOS ships /home as a symlink on the sealed system volume
// pointing at DataRoot + "/home", where autofs mounts auto_home, so a mount
// table can report that mount under either spelling.
var extraDataVolumeRoots = []string{"/home"}

// firmlinks returns the data-volume roots, longest first, read once.
var firmlinks = sync.OnceValue(loadFirmlinks)

func loadFirmlinks() []string {
	out := parseFirmlinks(readFirmlinksFile())
	if len(out) == 0 {
		out = append([]string(nil), defaultFirmlinks...)
	}
	out = append(out, extraDataVolumeRoots...)
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// readFirmlinksFile reads the system firmlink table. This is the only file
// package mac reads; it lives on the sealed system volume, so it can never be
// a dataless (cloud-evicted) file.
func readFirmlinksFile() []byte {
	data, err := os.ReadFile(firmlinksFile)
	if err != nil {
		return nil
	}
	return data
}

// parseFirmlinks reads the tab-separated "system path\tdata-relative path"
// table. Entries whose two sides disagree are dropped: the DataRoot-prefix
// translation would be wrong for them.
func parseFirmlinks(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) != 2 {
			continue
		}
		sys := filepath.Clean(parts[0])
		rel := strings.Trim(strings.TrimSpace(parts[1]), "/")
		if !strings.HasPrefix(sys, "/") || rel == "" || sys != "/"+rel {
			continue
		}
		out = append(out, sys)
	}
	return out
}

// DataVolumePath maps a system-volume path that crosses a firmlink, or one of
// the extra data-volume roots, to the same directory's path under DataRoot.
// It reports false for paths that are not on the data volume, such as "/",
// "/dev" and "/System/Volumes/VM". It touches no filesystem.
func DataVolumePath(p string) (string, bool) {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) || p == "/" {
		return "", false
	}
	if p == DataRoot || strings.HasPrefix(p, DataRoot+"/") {
		return p, true
	}
	for _, f := range firmlinks() {
		if p == f || strings.HasPrefix(p, f+"/") {
			return DataRoot + p, true
		}
	}
	return "", false
}
