package apps

import (
	"slices"
	"testing"
)

func TestParsePkgList(t *testing.T) {
	t.Parallel()
	// Trimmed from this machine's own "pkgutil --pkgs", Apple's hundreds
	// of receipts included so the filter is actually exercised.
	const out = `com.apple.pkg.CLTools_Executables
com.autodesk.autocad2027.lib.pkg
com.blackmagic-design.DiskSpeedTest
org.golang.go
com.apple.pkg.XProtectPayloads
com.paloaltonetworks.globalprotect.pkg
net.temurin.21.jdk
com.if.Amphetamine
com.autodesk.cer
com.bitwarden.desktop

com.cloudflare.1dot1dot1dot1.macos
`
	got := ParsePkgList(out)
	want := []string{
		"com.autodesk.autocad2027.lib.pkg", "com.blackmagic-design.DiskSpeedTest",
		"org.golang.go", "com.paloaltonetworks.globalprotect.pkg", "net.temurin.21.jdk",
		"com.if.Amphetamine", "com.autodesk.cer", "com.bitwarden.desktop",
		"com.cloudflare.1dot1dot1dot1.macos",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ParsePkgList =\n%v\nwant\n%v", got, want)
	}
	for _, id := range got {
		if id == "com.apple.pkg.CLTools_Executables" || id == "com.apple.pkg.XProtectPayloads" {
			t.Errorf("Apple receipt %q was kept", id)
		}
	}
}

func TestParsePkgInfo(t *testing.T) {
	t.Parallel()
	// Verbatim from "pkgutil --pkg-info com.paloaltonetworks.globalprotect.pkg".
	const out = `package-id: com.paloaltonetworks.globalprotect.pkg
version: 6.3.2-525
volume: /
location: Applications/GlobalProtect.app
install-time: 1761159020
`
	rec := ParsePkgInfo(out)
	if rec.PkgID != "com.paloaltonetworks.globalprotect.pkg" {
		t.Errorf("PkgID = %q", rec.PkgID)
	}
	if rec.Version != "6.3.2-525" {
		t.Errorf("Version = %q", rec.Version)
	}
	if got := rec.InstallPath(); got != "/Applications/GlobalProtect.app" {
		t.Errorf("InstallPath = %q", got)
	}
	if rec.InstallTime.IsZero() {
		t.Error("InstallTime was not parsed")
	}
	if got := rec.VendorPrefix(); got != "com.paloaltonetworks" {
		t.Errorf("VendorPrefix = %q", got)
	}
}

// TestParsePkgInfoFolderLocation covers AutoCAD, whose install location is a
// publisher folder rather than a bundle. The two shapes must both produce a
// usable path, because the existence of that path is the orphan evidence.
func TestParsePkgInfoFolderLocation(t *testing.T) {
	t.Parallel()
	const out = `package-id: com.autodesk.AutoCAD2027
version: 26.0.60.161
volume: /
location: Applications/Autodesk/AutoCAD 2027
install-time: 1779494033
`
	rec := ParsePkgInfo(out)
	if got := rec.InstallPath(); got != "/Applications/Autodesk/AutoCAD 2027" {
		t.Errorf("InstallPath = %q", got)
	}
}

func TestParsePkgInfoEmpty(t *testing.T) {
	t.Parallel()
	if rec := ParsePkgInfo("No receipt for 'com.example' found at '/'.\n"); rec.PkgID != "" {
		t.Errorf("a missing receipt yielded PkgID %q", rec.PkgID)
	}
}

func TestParsePkgFiles(t *testing.T) {
	t.Parallel()
	const out = `Applications/GlobalProtect.app
Applications/GlobalProtect.app/Contents
Applications/GlobalProtect.app/Contents/Info.plist

Library/Logs/PaloAltoNetworks
`
	got := ParsePkgFiles(out)
	if len(got) != 4 {
		t.Fatalf("ParsePkgFiles returned %d entries: %v", len(got), got)
	}
	if got[0] != "Applications/GlobalProtect.app" {
		t.Errorf("first entry = %q", got[0])
	}
}
