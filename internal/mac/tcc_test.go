package mac

import (
	"os"
	"strings"
	"testing"
)

func TestProbeFullDiskAccess(t *testing.T) {
	p := ProbeFullDiskAccess()
	if p.Granted && p.Err != nil {
		t.Errorf("granted probe carries an error: %v", p.Err)
	}
	if !p.Granted && p.Err == nil {
		t.Error("denied probe carries no error")
	}
	if p.Path == "" {
		t.Error("probe records no path")
	}
	home, err := os.UserHomeDir()
	if err == nil && !strings.HasPrefix(p.Path, home) {
		t.Errorf("probe path %q is outside the home directory", p.Path)
	}
	t.Logf("full disk access: granted=%v path=%s err=%v", p.Granted, p.Path, p.Err)
}

func TestFullDiskAccessHint(t *testing.T) {
	h := FullDiskAccessHint("Ghostty")
	if !strings.Contains(h, "Ghostty") || !strings.Contains(h, "Full Disk Access") {
		t.Errorf("hint = %q", h)
	}
	if !strings.Contains(FullDiskAccessHint(""), "your terminal") {
		t.Error("empty app name should fall back to a generic phrase")
	}
}
