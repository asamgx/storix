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

func TestReadDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Zed.app", "Arc.app", "Numi.app"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name()
	}
	want := []string{".DS_Store", "Arc.app", "Numi.app", "Zed.app"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ReadDir = %v, want %v sorted by name", got, want)
	}
	if !entries[1].IsDir() {
		t.Error("a bundle directory was not reported as a directory")
	}
}

// TestReadDirRefusesASymlink keeps a detector inside the directory it was
// pointed at. A Caskroom entry replaced by a link to /Applications must not be
// listed as though it were the Caskroom's own contents.
func TestReadDirRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "Applications")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(link); err == nil {
		t.Fatal("a symlink to a directory was followed")
	} else if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error = %v, want it to name the reason", err)
	}
}

func TestReadDirRefusesAFileAndAMissingPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(file); err == nil {
		t.Error("a regular file was listed as a directory")
	}
	if _, err := ReadDir(filepath.Join(dir, "absent")); err == nil {
		t.Error("a missing directory listed without error")
	}
}

// TestReadDirCapIsAnErrorNotATruncation: a short listing would make the apps
// inventory call an installed application missing, so passing the cap has to
// be visible.
func TestReadDirCapIsAnErrorNotATruncation(t *testing.T) {
	if MaxReadDir < 1000 {
		t.Fatalf("MaxReadDir = %d, too small for the directories detectors list", MaxReadDir)
	}
	dir := t.TempDir()
	for i := range 5 {
		if err := os.Mkdir(filepath.Join(dir, "d"+string(rune('a'+i))), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := ReadDir(dir)
	if err != nil || len(entries) != 5 {
		t.Fatalf("ReadDir = %d entries, %v", len(entries), err)
	}
}

// TestDanglingAppSymlinkIsNotAnInstalledBundle is the case the apps inventory
// has to get right: a cask leaves Caskroom/<token>/<ver>/Cursor.app pointing
// at /Applications/Cursor.app, the application is deleted, and the link
// remains. Stat must report the link rather than follow it into nothing.
func TestDanglingAppSymlinkIsNotAnInstalledBundle(t *testing.T) {
	dir := t.TempDir()
	version := filepath.Join(dir, "Caskroom", "cursor", "1.2")
	if err := os.MkdirAll(version, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(version, "Cursor.app")
	if err := os.Symlink(filepath.Join(dir, "Applications", "Cursor.app"), bundle); err != nil {
		t.Fatal(err)
	}

	env := Env{Stat: Stat, ReadDir: ReadDir}
	if !env.Exists(bundle) {
		t.Fatal("the link itself was reported as absent; Stat followed it")
	}
	fi, err := Stat(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsSymlink() {
		t.Error("the dangling link was not reported as a symlink, so it would look like an installed bundle")
	}
	if fi.IsDir {
		t.Error("the dangling link was reported as a directory")
	}

	// The listing shows it too, and shows it as a link.
	entries, err := env.ReadDir(version)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "Cursor.app" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Type()&os.ModeSymlink == 0 {
		t.Error("the listing did not mark the entry as a symlink")
	}
}

// TestForbiddenPatternCatchesReadDir guards the guard: the lint test's own
// pattern has to reject the call it exists to reject.
func TestForbiddenPatternCatchesReadDir(t *testing.T) {
	for _, line := range []string{
		`	entries, err := os.ReadDir(dir)`,
		`	data, err := os.ReadFile(p)`,
		`	f, err := os.Open(p)`,
		`	fd, err := unix.Openat(dirfd, name, 0, 0)`,
	} {
		if !forbidden.MatchString(line) {
			t.Errorf("the lint pattern misses %q", strings.TrimSpace(line))
		}
	}
	if forbidden.MatchString(`	entries, err := env.ReadDir(dir)`) {
		t.Error("the lint pattern rejects the sanctioned Env.ReadDir call")
	}
}
