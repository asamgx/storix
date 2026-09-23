package apps

import (
	"slices"
	"testing"
)

// plistXML wraps a dict body in the header a real property list carries.
func plistXML(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
` + body + `
</dict>
</plist>
`)
}

func TestParseLaunchPlistProgram(t *testing.T) {
	t.Parallel()
	// The OrbStack privileged helper, which names both Program and
	// ProgramArguments as well as the bundle it belongs to.
	data := plistXML(`	<key>Label</key><string>dev.orbstack.OrbStack.privhelper</string>
	<key>Program</key><string>/Library/PrivilegedHelperTools/dev.orbstack.OrbStack.privhelper</string>
	<key>ProgramArguments</key><array><string>/Library/PrivilegedHelperTools/dev.orbstack.OrbStack.privhelper</string></array>
	<key>AssociatedBundleIdentifiers</key><array><string>dev.kdrag0n.MacVirt</string></array>`)

	item, err := ParseLaunchPlist("/Library/LaunchDaemons/dev.orbstack.OrbStack.privhelper.plist", data, true)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if item.Label != "dev.orbstack.OrbStack.privhelper" {
		t.Errorf("Label = %q", item.Label)
	}
	if item.Program != "/Library/PrivilegedHelperTools/dev.orbstack.OrbStack.privhelper" {
		t.Errorf("Program = %q", item.Program)
	}
	if !slices.Contains(item.BundleIDs, "dev.kdrag0n.MacVirt") {
		t.Errorf("BundleIDs = %v", item.BundleIDs)
	}
	if !item.System {
		t.Error("a daemon under /Library is a system item")
	}
	if !item.Owns("dev.kdrag0n.MacVirt") {
		t.Error("Owns should match the associated bundle id")
	}
}

// TestParseLaunchPlistProgramArgumentsOnly covers the Autodesk daemons, which
// name no Program at all: the executable is the first argument.
func TestParseLaunchPlistProgramArgumentsOnly(t *testing.T) {
	t.Parallel()
	data := plistXML(`	<key>Label</key><string>com.autodesk.cer</string>
	<key>KeepAlive</key><true/>
	<key>ProgramArguments</key><array>
		<string>/Library/Application Support/Autodesk/AdskCER/service/cer_service</string>
	</array>`)

	item, err := ParseLaunchPlist("/Library/LaunchDaemons/com.autodesk.cer.plist", data, true)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if item.Program != "/Library/Application Support/Autodesk/AdskCER/service/cer_service" {
		t.Errorf("Program = %q", item.Program)
	}
}

func TestParseLaunchPlistBundleProgram(t *testing.T) {
	t.Parallel()
	data := plistXML(`	<key>Label</key><string>com.example.helper</string>
	<key>BundleProgram</key><string>Contents/MacOS/Helper</string>`)
	item, err := ParseLaunchPlist("/Library/LaunchAgents/com.example.helper.plist", data, true)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if item.Program != "Contents/MacOS/Helper" {
		t.Errorf("Program = %q", item.Program)
	}
}

// TestParseLaunchPlistNoProgram is the keystone case. Google's agent plist
// names no executable this parser understands, and "I could not tell" must
// not become a keep signal: treating it as one would suppress real orphans.
func TestParseLaunchPlistNoProgram(t *testing.T) {
	t.Parallel()
	data := plistXML(`	<key>Label</key><string>com.google.keystone.agent</string>`)
	item, err := ParseLaunchPlist("/Users/u/Library/LaunchAgents/com.google.keystone.agent.plist", data, false)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if item.Program != "" {
		t.Errorf("Program = %q, want empty", item.Program)
	}
	if item.ProgramExists {
		t.Error("an item with no program cannot have an existing program")
	}
	const want = "launch item com.google.keystone.agent present, program unknown"
	if got := item.Describe(); got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}

// TestParseLaunchPlistUnreadable makes sure a plist that will not decode is
// still visible: it keeps the label from its file name.
func TestParseLaunchPlistUnreadable(t *testing.T) {
	t.Parallel()
	item, err := ParseLaunchPlist("/Library/LaunchDaemons/com.broken.service.plist", []byte("not a plist"), true)
	if err == nil {
		t.Fatal("a broken plist should report an error")
	}
	if item.Label != "com.broken.service" {
		t.Errorf("Label = %q, want the file name fallback", item.Label)
	}
}

func TestLaunchItemOwns(t *testing.T) {
	t.Parallel()
	item := LaunchItem{Label: "com.google.keystone.agent"}
	if !item.Owns("com.google.keystone") {
		t.Error("a label should match the id family it extends")
	}
	if item.Owns("com.google.Chrome") {
		t.Error("a label must not match an unrelated sibling id")
	}
	if item.Owns("") {
		t.Error("an empty id matches nothing")
	}
}
