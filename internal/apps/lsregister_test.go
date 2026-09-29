package apps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadDump(t *testing.T) []RegistryEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "probes", "lsregister-dump.txt"))
	if err != nil {
		t.Fatalf("reading the dump fixture: %v", err)
	}
	return ParseLSRegisterDump(strings.NewReader(string(data)))
}

func TestParseLSRegisterDump(t *testing.T) {
	t.Parallel()
	entries := loadDump(t)
	if len(entries) < 15 {
		t.Fatalf("parsed only %d entries", len(entries))
	}

	byPath := make(map[string]RegistryEntry, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}

	// The handle suffix LaunchServices appends to every path is stripped.
	chrome, ok := byPath["/Applications/Google Chrome.app"]
	if !ok {
		t.Fatalf("Chrome is missing; got %d entries", len(entries))
	}
	if chrome.ID != "com.google.Chrome" {
		t.Errorf("Chrome id = %q", chrome.ID)
	}
	if chrome.TeamID != "EQHXZ8M8AV" {
		t.Errorf("Chrome team = %q", chrome.TeamID)
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app (0x1a2c)",
		"/Applications/Google Chrome.app ",
	} {
		if _, bad := byPath[p]; bad {
			t.Errorf("path %q kept its handle suffix", p)
		}
	}
}

// TestLSRegisterFindsStaleCopies is the reason the register is read at all.
// A bundle in the Trash and a staged update under Application Support both
// register as the application, and treating either as an installation would
// hide every orphan on the machine.
func TestLSRegisterFindsStaleCopies(t *testing.T) {
	t.Parallel()
	entries := loadDump(t)

	var trashed, staged, sparkle int
	for _, e := range entries {
		switch {
		case e.InTrash:
			trashed++
		case strings.Contains(e.Path, "/Updates/"):
			staged++
		case strings.Contains(e.Path, "org.sparkle-project.Sparkle"):
			sparkle++
		}
	}
	if trashed < 2 {
		t.Errorf("found %d Trash entries, want the DynamicLakePro bundle and its helper", trashed)
	}
	if staged < 2 {
		t.Errorf("found %d staged update copies, want the two Raycast ones", staged)
	}
	if sparkle < 2 {
		t.Errorf("found %d Sparkle updater copies, want two", sparkle)
	}
}

func TestPathInTrash(t *testing.T) {
	t.Parallel()
	in := []string{
		"/Users/andrewsam/.Trash/DynamicLakePro.app",
		"/Users/andrewsam/.Trash/DynamicLakePro.app/Contents/Info.plist",
		"/Volumes/Data/.Trashes/501/Old.app",
	}
	for _, p := range in {
		if !PathInTrash(p) {
			t.Errorf("PathInTrash(%q) = false", p)
		}
	}
	out := []string{"/Applications/Trash Can.app", "/Users/andrewsam/Library/Caches/Trashy"}
	for _, p := range out {
		if PathInTrash(p) {
			t.Errorf("PathInTrash(%q) = true", p)
		}
	}
}

func TestNestedInBundle(t *testing.T) {
	t.Parallel()
	nested := []string{
		"/Applications/Brave Browser.app/Contents/Frameworks",
		"/Applications/X.app/Contents/Library/LoginItems",
		"/Applications/Y.framework/Versions/A",
	}
	for _, p := range nested {
		if !NestedInBundle(p) {
			t.Errorf("NestedInBundle(%q) = false", p)
		}
	}
	if NestedInBundle("/Applications/Autodesk/AutoCAD 2027") {
		t.Error("a publisher folder is not inside a bundle")
	}
}

func TestIsAppPath(t *testing.T) {
	t.Parallel()
	if !IsAppPath("/Applications/Google Chrome.app") {
		t.Error("a top-level bundle should be an app path")
	}
	if IsAppPath("/Applications/Brave Browser.app/Contents/Helpers/Helper.app") {
		t.Error("a bundle inside a bundle is not an installation")
	}
	if IsAppPath("/Applications/Autodesk") {
		t.Error("a folder is not an app path")
	}
}
