package apps

import (
	"bytes"
	"testing"

	"howett.net/plist"
)

func TestParseInfoPlistXML(t *testing.T) {
	t.Parallel()
	data := plistXML(`	<key>CFBundleIdentifier</key><string>com.openai.codex</string>
	<key>CFBundleName</key><string>ChatGPT</string>
	<key>CFBundleShortVersionString</key><string>1.2024.1</string>
	<key>CFBundleExecutable</key><string>ChatGPT</string>`)

	info, err := ParseInfoPlist("/Applications/ChatGPT.app", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.ID != "com.openai.codex" {
		t.Errorf("ID = %q", info.ID)
	}
	if info.Name != "ChatGPT" || info.DisplayName != "ChatGPT" {
		t.Errorf("Name = %q, DisplayName = %q", info.Name, info.DisplayName)
	}
	if info.Version != "1.2024.1" {
		t.Errorf("Version = %q", info.Version)
	}
}

// TestParseInfoPlistBinary is why the plist dependency exists: an application
// whose Info.plist is in the binary format must read exactly the same.
func TestParseInfoPlistBinary(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	enc := plist.NewBinaryEncoder(&buf)
	if err := enc.Encode(map[string]any{
		"CFBundleIdentifier":         "com.microsoft.VSCode",
		"CFBundleName":               "Code",
		"CFBundleDisplayName":        "Visual Studio Code",
		"CFBundleShortVersionString": "1.99.0",
	}); err != nil {
		t.Fatalf("encoding a binary plist: %v", err)
	}
	info, err := ParseInfoPlist("/Applications/Visual Studio Code.app", buf.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.ID != "com.microsoft.VSCode" {
		t.Errorf("ID = %q", info.ID)
	}
	if info.DisplayName != "Visual Studio Code" {
		t.Errorf("DisplayName = %q, want the display name to win over the bundle name", info.DisplayName)
	}
	if info.Name != "Code" {
		t.Errorf("Name = %q", info.Name)
	}
}

// TestParseInfoPlistNoIdentifier covers the two bundles on the reference
// machine with no readable CFBundleIdentifier. A name-only application is
// still installed, so the parse must succeed with an empty id.
func TestParseInfoPlistNoIdentifier(t *testing.T) {
	t.Parallel()
	data := plistXML(`	<key>CFBundlePackageType</key><string>APPL</string>`)
	info, err := ParseInfoPlist("/Applications/Utilities/Claude Code URL Handler.app", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.ID != "" {
		t.Errorf("ID = %q, want empty", info.ID)
	}
	if info.DisplayName != "Claude Code URL Handler" {
		t.Errorf("DisplayName = %q, want the folder name fallback", info.DisplayName)
	}
}

func TestParseInfoPlistGarbage(t *testing.T) {
	t.Parallel()
	info, err := ParseInfoPlist("/Applications/Broken.app", []byte("not a plist at all"))
	if err == nil {
		t.Fatal("garbage should report an error")
	}
	if info.DisplayName != "Broken" {
		t.Errorf("DisplayName = %q, want the folder name fallback", info.DisplayName)
	}
}

func TestBundleBaseName(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"/Applications/Google Chrome.app":                      "Google Chrome",
		"/Applications/Autodesk/AutoCAD 2027/AutoCAD 2027.app": "AutoCAD 2027",
		"/Applications/Autodesk":                               "Autodesk",
		"/Applications/Thing.APP":                              "Thing",
	}
	for in, want := range tests {
		if got := BundleBaseName(in); got != want {
			t.Errorf("BundleBaseName(%q) = %q, want %q", in, got, want)
		}
	}
}
