package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/mac"
)

func TestRunDoctor(t *testing.T) {
	if _, err := os.Stat(mac.DataRoot); err != nil {
		t.Skipf("no %s on this machine: %v", mac.DataRoot, err)
	}
	var buf bytes.Buffer
	if err := runDoctor(context.Background(), &buf, mac.DataRoot); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	sections := []string{
		"build", "dataless materialization", "purgeable space",
		"getattrlist vs statfs", "permissions", "volumes", "container",
		"mounts nested inside the scan root", "local Time Machine snapshots",
		"paths",
	}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("doctor output has no %q section", s)
		}
	}
	for _, s := range []string{mac.DataRoot, "full disk access", "scan cache"} {
		if !strings.Contains(out, s) {
			t.Errorf("doctor output does not mention %q", s)
		}
	}
	if mac.CgoEnabled {
		if !strings.Contains(out, "cgo        yes") && !strings.Contains(out, "cgo  yes") {
			t.Error("cgo build does not report cgo yes")
		}
		if !strings.Contains(out, "foundation") {
			t.Error("cgo build does not report the purgeable source")
		}
	} else if !strings.Contains(out, "unavailable") {
		t.Error("build without cgo does not report anything as unavailable")
	}
	t.Log("\n" + out)
}

func TestDoctorCommandRegistered(t *testing.T) {
	var found bool
	for _, c := range Root().Commands() {
		if c.Name() == "doctor" {
			found = true
		}
	}
	if !found {
		t.Error("doctor is not registered on the root command")
	}
}
