package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "Info.plist")
	if err := os.WriteFile(plain, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := ReadFile(plain)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "<plist/>" {
		t.Errorf("read %q", data)
	}
}

// TestReadFileRefusesASymlink is the rule that keeps a detector inside the
// directory it was pointed at: a receipt replaced by a link to somewhere else
// must not be followed.
func TestReadFileRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret")
	if err := os.WriteFile(target, []byte("elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(link); err == nil {
		t.Fatal("a symlink was followed")
	} else if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error = %v, want it to name the reason", err)
	}
}

func TestReadFileRefusesADirectory(t *testing.T) {
	if _, err := ReadFile(t.TempDir()); err == nil {
		t.Error("a directory was read as a file")
	}
}

// TestReadFileCapsSize: a detector's files are plists and version markers,
// and something enormous where one was expected is a reason to stop rather
// than to allocate.
func TestReadFileCapsSize(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileLimit(big, 1024); err == nil {
		t.Fatal("a file over the limit was read")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want it to name the limit", err)
	}
	if data, err := ReadFileLimit(big, 8192); err != nil || len(data) != 4096 {
		t.Errorf("ReadFileLimit under the limit = %d bytes, %v", len(data), err)
	}
}

func TestReadFileMissing(t *testing.T) {
	if _, err := ReadFile(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing file read without error")
	}
}

func TestStatAndExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := Env{Stat: Stat}

	if !env.Exists(file) || !env.Exists(dir) {
		t.Error("Exists does not find what is there")
	}
	if env.Exists(filepath.Join(dir, "absent")) {
		t.Error("Exists found what is not there")
	}
	if env.Exists("") {
		t.Error("Exists said yes to the empty path")
	}
	if (Env{}).Exists(file) {
		t.Error("an environment with no Stat still answered")
	}

	// A dangling symlink exists as a link even though its target does not,
	// which is what lstat semantics mean and what a detector needs: the
	// link itself occupies the directory entry.
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "gone"), link); err != nil {
		t.Fatal(err)
	}
	if !env.Exists(link) {
		t.Error("a dangling symlink was reported as absent; Stat followed it")
	}

	fi, err := Stat(dir)
	if err != nil || !fi.IsDir {
		t.Errorf("Stat(dir) = %+v, %v", fi, err)
	}
}

func TestEnvPathAndHas(t *testing.T) {
	env := Env{Home: "/Users/andrew"}
	if got := env.Path("Library", "Caches"); got != "/Users/andrew/Library/Caches" {
		t.Errorf("Path = %q", got)
	}
	if got := (Env{}).Path("Library"); got != "" {
		t.Errorf("Path without a home = %q, want empty", got)
	}
	if env.Has("anything") {
		t.Error("an environment with no LookPath found a tool")
	}
}
