package apps

import (
	"bytes"
	"errors"
	"path"
	"strings"

	"howett.net/plist"
)

// BundleInfo is the part of an application's Info.plist this package uses. It
// is a plain struct with no tree references so it round-trips through the
// scan cache along with the rest of Facts.
type BundleInfo struct {
	// Path is the display path of the .app directory.
	Path string `json:"path"`
	// ID is CFBundleIdentifier. It may be empty: two bundles on the
	// reference machine have no readable identifier, and a name-only
	// bundle is still an installed application.
	ID string `json:"id,omitempty"`
	// Name is CFBundleName.
	Name string `json:"name,omitempty"`
	// DisplayName is CFBundleDisplayName, falling back to Name and then to
	// the basename without ".app".
	DisplayName string `json:"displayName,omitempty"`
	// Version is CFBundleShortVersionString, which keys the team id cache.
	Version string `json:"version,omitempty"`
	// MASReceipt records a Contents/_MASReceipt/receipt: the marker that
	// the application came from the App Store.
	MASReceipt bool `json:"masReceipt,omitempty"`
}

// infoPlist is the decoded subset. howett.net/plist reads both the XML form
// that most applications ship and the binary form, which is why the
// dependency exists at all.
type infoPlist struct {
	Identifier   string `plist:"CFBundleIdentifier"`
	Name         string `plist:"CFBundleName"`
	DisplayName  string `plist:"CFBundleDisplayName"`
	ShortVersion string `plist:"CFBundleShortVersionString"`
	Version      string `plist:"CFBundleVersion"`
	Executable   string `plist:"CFBundleExecutable"`
}

// ErrNotPlist reports data that is not a property list at all.
var ErrNotPlist = errors.New("apps: not a property list")

// ParseInfoPlist decodes an application's Info.plist.
//
// bundlePath is the .app directory the plist came from; it supplies the
// display name fallback, because an application whose plist names neither a
// bundle name nor a display name is still called by its folder.
func ParseInfoPlist(bundlePath string, data []byte) (BundleInfo, error) {
	info := BundleInfo{Path: bundlePath}
	info.DisplayName = BundleBaseName(bundlePath)
	if len(bytes.TrimSpace(data)) == 0 {
		return info, ErrNotPlist
	}
	var doc infoPlist
	if _, err := plist.Unmarshal(data, &doc); err != nil {
		return info, err
	}
	info.ID = strings.TrimSpace(doc.Identifier)
	info.Name = strings.TrimSpace(doc.Name)
	info.Version = strings.TrimSpace(doc.ShortVersion)
	if info.Version == "" {
		info.Version = strings.TrimSpace(doc.Version)
	}
	switch {
	case strings.TrimSpace(doc.DisplayName) != "":
		info.DisplayName = strings.TrimSpace(doc.DisplayName)
	case info.Name != "":
		info.DisplayName = info.Name
	}
	return info, nil
}

// BundleBaseName is a bundle path's folder name without the ".app" suffix.
func BundleBaseName(p string) string {
	base := path.Base(strings.TrimSuffix(p, "/"))
	if strings.EqualFold(path.Ext(base), ".app") {
		return base[:len(base)-len(".app")]
	}
	return base
}

// Names lists every name a bundle answers to: its display name, its bundle
// name and its folder name, deduplicated.
func (b BundleInfo) Names() []string {
	out := []string{b.DisplayName, b.Name, BundleBaseName(b.Path)}
	dedupeInPlace(&out)
	return out
}
