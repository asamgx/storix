package apps

import (
	"path"
	"strconv"
	"strings"
	"time"
)

// Receipt is one installer package receipt as pkgutil reports it.
//
// A receipt outlives whatever it installed: removing an application leaves
// its receipt behind. That is exactly what makes it evidence. A receipt whose
// install location no longer exists and whose files are gone is the strongest
// orphan signal this package has, and it is the one that reclassifies
// GlobalProtect from installed to orphan-likely.
type Receipt struct {
	PkgID   string `json:"pkgId"`
	Version string `json:"version,omitempty"`
	// Volume and Location are the two halves of the install path, kept
	// apart because pkgutil reports them apart and the volume is "/" on
	// every machine that has never had a second disk.
	Volume   string `json:"volume,omitempty"`
	Location string `json:"location,omitempty"`
	// InstallTime is when the package was installed.
	InstallTime time.Time `json:"installTime,omitempty"`
	// LocationExists is the result of an lstat on the install path, filled
	// in by Probe; the parser never touches the filesystem. It is false
	// both for a location that is gone and for one that could not be
	// checked, so a caller reading absence as evidence that the software
	// was removed reads CheckErr first.
	LocationExists bool `json:"locationExists"`
	// CheckErr is why the install location could not be stat'd, empty when
	// it could. A location that could not be checked is not a location that
	// is missing: see [detect.Env.Lookup].
	CheckErr string `json:"checkErr,omitempty"`
	// FilesPresent and FilesTotal are the presence ratio from
	// "pkgutil --files", run only for receipts whose location is missing.
	FilesPresent int `json:"filesPresent,omitempty"`
	FilesTotal   int `json:"filesTotal,omitempty"`
	// FilesChecked records that the ratio was measured, so a receipt with
	// zero of zero files is distinguishable from one never looked at.
	FilesChecked bool `json:"filesChecked,omitempty"`
}

// InstallPath is the absolute display path the package installed to, or empty
// when the receipt names no location.
func (r Receipt) InstallPath() string {
	if r.Location == "" {
		return ""
	}
	vol := r.Volume
	if vol == "" {
		vol = "/"
	}
	return path.Join(vol, r.Location)
}

// ParsePkgList reads "pkgutil --pkgs" and drops Apple's own receipts, which
// number in the hundreds and never describe an application the user installed.
func ParsePkgList(stdout string) []string {
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		id := strings.TrimSpace(line)
		if id == "" || id == "com.apple" || strings.HasPrefix(id, "com.apple.") {
			continue
		}
		out = append(out, id)
	}
	dedupeInPlace(&out)
	return out
}

// ParsePkgInfo reads "pkgutil --pkg-info <id>", whose output is a handful of
// "key: value" lines. An empty or unrecognised block yields a receipt with no
// package id, which the caller drops.
func ParsePkgInfo(stdout string) Receipt {
	var r Receipt
	for _, line := range strings.Split(stdout, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "package-id":
			r.PkgID = val
		case "version":
			r.Version = val
		case "volume":
			r.Volume = val
		case "location":
			r.Location = val
		case "install-time":
			if secs, err := strconv.ParseInt(val, 10, 64); err == nil && secs > 0 {
				r.InstallTime = time.Unix(secs, 0).UTC()
			}
		}
	}
	return r
}

// ParsePkgFiles reads "pkgutil --files <id>": one package-relative path per
// line. The paths are relative to the receipt's volume, not its location.
func ParsePkgFiles(stdout string) []string {
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// VendorPrefix is the receipt's reverse-DNS vendor, used to attach a receipt
// to a candidate directory that shares its publisher, such as the seven
// com.autodesk.* receipts and the Autodesk folder.
func (r Receipt) VendorPrefix() string {
	rdns, ok := ParseReverseDNS(strings.TrimSuffix(r.PkgID, ".pkg"))
	if !ok {
		return ""
	}
	return rdns.Vendor
}
