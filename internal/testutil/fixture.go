// Package testutil builds real on-disk trees for walker tests.
//
// Fixtures live in t.TempDir(), which on macOS is on the APFS data volume, so
// st_blocks, hard links, sparse files and du all behave exactly as they do in
// a real scan. Unlike internal/walk, this package is allowed to open files.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Fixture is a temporary directory tree.
type Fixture struct {
	Root string
	t    *testing.T
}

// New creates an empty fixture under t.TempDir(). The root is resolved through
// any symlink (/var → /private/var) so paths match what a scan would see.
func New(t *testing.T) *Fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("testutil: resolve temp dir: %v", err)
	}
	return &Fixture{Root: root, t: t}
}

// Path returns the absolute path of a fixture-relative path.
func (f *Fixture) Path(rel string) string { return filepath.Join(f.Root, rel) }

// Dir creates a directory and its parents.
func (f *Fixture) Dir(rel string) string {
	f.t.Helper()
	p := f.Path(rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatalf("testutil: mkdir %s: %v", rel, err)
	}
	return p
}

// File creates a file of exactly size bytes of real data.
func (f *Fixture) File(rel string, size int) string {
	f.t.Helper()
	p := f.Path(rel)
	f.Dir(filepath.Dir(rel))
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		f.t.Fatalf("testutil: write %s: %v", rel, err)
	}
	return p
}

// Sparse creates a file with the given apparent size and no allocated blocks.
func (f *Fixture) Sparse(rel string, apparent int64) string {
	f.t.Helper()
	p := f.File(rel, 0)
	if err := os.Truncate(p, apparent); err != nil {
		f.t.Fatalf("testutil: truncate %s: %v", rel, err)
	}
	return p
}

// Hardlink creates newRel as a hard link to existingRel.
func (f *Fixture) Hardlink(existingRel, newRel string) string {
	f.t.Helper()
	p := f.Path(newRel)
	f.Dir(filepath.Dir(newRel))
	if err := os.Link(f.Path(existingRel), p); err != nil {
		f.t.Fatalf("testutil: link %s -> %s: %v", newRel, existingRel, err)
	}
	return p
}

// Symlink creates rel as a symlink to target, which is used verbatim.
func (f *Fixture) Symlink(target, rel string) string {
	f.t.Helper()
	p := f.Path(rel)
	f.Dir(filepath.Dir(rel))
	if err := os.Symlink(target, p); err != nil {
		f.t.Fatalf("testutil: symlink %s -> %s: %v", rel, target, err)
	}
	return p
}

// Deep creates a chain of nested directories of the given depth under rel and
// returns the deepest one. Each level holds one small file.
func (f *Fixture) Deep(rel string, depth int) string {
	f.t.Helper()
	cur := rel
	for i := range depth {
		cur = filepath.Join(cur, "d"+strconv.Itoa(i))
		f.Dir(cur)
		f.File(filepath.Join(cur, "f"), 1)
	}
	return f.Path(cur)
}

// Wide creates a directory holding n small files.
func (f *Fixture) Wide(rel string, n int) string {
	f.t.Helper()
	p := f.Dir(rel)
	for i := range n {
		name := filepath.Join(rel, "f"+strconv.Itoa(i))
		if err := os.WriteFile(f.Path(name), []byte{byte(i)}, 0o644); err != nil {
			f.t.Fatalf("testutil: write %s: %v", name, err)
		}
	}
	return p
}

// Unreadable creates a directory with mode 0 and restores it at cleanup.
// Running as root defeats the point, so the test is skipped there.
func (f *Fixture) Unreadable(rel string) string {
	f.t.Helper()
	if os.Geteuid() == 0 {
		f.t.Skip("testutil: mode 0 does not stop root")
	}
	p := f.Dir(rel)
	if err := os.Chmod(p, 0); err != nil {
		f.t.Fatalf("testutil: chmod 0 %s: %v", rel, err)
	}
	f.t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
	return p
}

// DuKB returns `du -sk Root`, the kibibytes du charges the fixture. du counts
// allocated blocks and dedupes hard links, so it is the reference the walker's
// root total has to match exactly.
func (f *Fixture) DuKB(t *testing.T) int64 {
	t.Helper()
	out, err := exec.Command("du", "-sk", f.Root).Output()
	if err != nil {
		t.Fatalf("testutil: du -sk %s: %v", f.Root, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		t.Fatalf("testutil: du -sk %s: empty output", f.Root)
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		t.Fatalf("testutil: du -sk %s: %q: %v", f.Root, fields[0], err)
	}
	return kb
}
