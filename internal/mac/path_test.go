package mac

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cases := []struct{ in, want string }{
		{"/Users/u/Library/X", DataRoot + "/Users/u/Library/X"},
		{"/Users", DataRoot + "/Users"},
		{"/Applications/Safari.app", DataRoot + "/Applications/Safari.app"},
		{"/private/var/vm", DataRoot + "/private/var/vm"},
		{"/opt/homebrew", DataRoot + "/opt/homebrew"},
		{"/Library/Caches", DataRoot + "/Library/Caches"},
		{"/usr/local/bin", DataRoot + "/usr/local/bin"},
		{"/", DataRoot},
		{DataRoot, DataRoot},
		{DataRoot + "/Users/u", DataRoot + "/Users/u"},
		{DataRoot + "/Users/u/", DataRoot + "/Users/u"},
		// Not on the data volume: separate volumes and virtual filesystems.
		{"/System/Volumes/VM", "/System/Volumes/VM"},
		{"/System/Volumes/Preboot/x", "/System/Volumes/Preboot/x"},
		{"/dev", "/dev"},
		{"/usr/bin", "/usr/bin"},
		{"/System/Library/Fonts", "/System/Library/Fonts"},
		// Relative paths are cleaned only.
		{"a/../b", "b"},
		{"~", DataRoot + home},
		{"~/Library", DataRoot + filepath.Join(home, "Library")},
	}
	for _, c := range cases {
		if got := ScanPath(c.in); got != c.want {
			t.Errorf("ScanPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDisplayPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{DataRoot + "/Users/u/Library", "/Users/u/Library"},
		{DataRoot + "/Applications", "/Applications"},
		{DataRoot, "/"},
		{DataRoot + "/", "/"},
		{"/System/Volumes/VM/swapfile0", "/System/Volumes/VM/swapfile0"},
		{"/Users/u", "/Users/u"},
		{"/System/Volumes/DataX/y", "/System/Volumes/DataX/y"},
	}
	for _, c := range cases {
		if got := DisplayPath(c.in); got != c.want {
			t.Errorf("DisplayPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScanPathDisplayPathRoundTrip(t *testing.T) {
	for _, p := range []string{"/Users/u/Library/Caches", "/Applications/X.app", "/private/var/db"} {
		if got := DisplayPath(ScanPath(p)); got != p {
			t.Errorf("round trip of %q gave %q", p, got)
		}
	}
}
