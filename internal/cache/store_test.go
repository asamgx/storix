package cache

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/walk"
)

// tinyTree is a two-node tree: enough to exercise the store without walking a
// fixture for every case.
func tinyTree(name string) *walk.Tree {
	root := &walk.Node{Name: "/tmp/" + name, Kind: walk.KindDir, Files: 1, Bytes: 4096}
	child := &walk.Node{Name: "file.bin", Parent: root, Kind: walk.KindFile, Bytes: 4096, Apparent: 4000, Files: 1}
	root.Children = []*walk.Node{child}
	return &walk.Tree{
		Root:  root,
		Nodes: []*walk.Node{root, child},
		Opts:  walk.Options{Root: root.Name, Parallelism: 8, SmallFileThreshold: 64 << 10},
	}
}

func saveAt(t *testing.T, s Store, at time.Time, root string) string {
	t.Helper()
	meta := Meta{Storix: "1.0.0", Written: at, Root: "/tmp/" + root, Roots: []string{"/tmp/" + root}}
	path, err := s.Save(meta, tinyTree(root))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return path
}

func TestSaveLatestLoad(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "storix", "scans")}
	base := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)

	first := saveAt(t, s, base, "one")
	second := saveAt(t, s, base.Add(time.Minute), "two")

	if filepath.Base(first) != "scan-20260922-090000.strx" {
		t.Errorf("first file is named %s", filepath.Base(first))
	}
	if info, err := os.Stat(second); err != nil {
		t.Fatalf("stat: %v", err)
	} else if info.Mode().Perm() != fileMode {
		t.Errorf("file mode is %v, want %v", info.Mode().Perm(), os.FileMode(fileMode))
	}
	if info, err := os.Stat(s.Dir); err != nil {
		t.Fatalf("stat dir: %v", err)
	} else if info.Mode().Perm() != dirMode {
		t.Errorf("directory mode is %v, want %v", info.Mode().Perm(), os.FileMode(dirMode))
	}

	target, err := os.Readlink(filepath.Join(s.Dir, LatestLink))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != filepath.Base(second) {
		t.Errorf("latest points at %q, want %q", target, filepath.Base(second))
	}

	path, meta, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if path != second || meta.Root != "/tmp/two" {
		t.Errorf("Latest = %s %s", path, meta.Root)
	}

	_, tree, err := s.Load(first)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := tree.Root.Name; got != "/tmp/one" {
		t.Errorf("loaded root %s", got)
	}
}

func TestSaveSameSecondDoesNotOverwrite(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	at := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	a := saveAt(t, s, at, "one")
	b := saveAt(t, s, at, "two")
	if a == b {
		t.Fatalf("both saves used %s", a)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
}

func TestListOrderAndContents(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	base := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	for i := range 3 {
		saveAt(t, s, base.Add(time.Duration(i)*time.Hour), "root")
	}
	// A stray file and a damaged cache file: neither may break the listing.
	if err := os.WriteFile(filepath.Join(s.Dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(s.Dir, "scan-20260101-000000.strx")
	if err := os.WriteFile(broken, []byte("not a cache"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("List returned %d entries, want 4", len(entries))
	}
	for i := 1; i < 3; i++ {
		if !entries[i-1].Meta.Written.After(entries[i].Meta.Written) {
			t.Errorf("entries %d and %d are out of order", i-1, i)
		}
	}
	last := entries[len(entries)-1]
	if last.Path != broken || last.Err == nil {
		t.Errorf("the damaged file is not listed last with an error: %+v", last)
	}
	if entries[0].Size == 0 {
		t.Error("entry size is zero")
	}
}

func TestPruneKeepsTheNewestAndTheLatest(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	base := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	var paths []string
	for i := range 5 {
		paths = append(paths, saveAt(t, s, base.Add(time.Duration(i)*time.Hour), "root"))
	}

	removed, err := s.Prune(2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("Prune removed %v, want 3 files", removed)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d entries left, want 2", len(entries))
	}
	if entries[0].Path != paths[4] || entries[1].Path != paths[3] {
		t.Errorf("wrong survivors: %s %s", entries[0].Path, entries[1].Path)
	}
	if _, _, err := s.Latest(); err != nil {
		t.Errorf("Latest broken after prune: %v", err)
	}

	// keep 0 still spares whatever `latest` points at.
	removed, err = s.Prune(0)
	if err != nil {
		t.Fatalf("Prune(0): %v", err)
	}
	if len(removed) != 1 {
		t.Errorf("Prune(0) removed %v, want 1 file", removed)
	}
	if _, _, err := s.Latest(); err != nil {
		t.Errorf("Latest broken after Prune(0): %v", err)
	}
	if _, err := s.Prune(-1); err == nil {
		t.Error("Prune accepted a negative keep")
	}
}

func TestClearEmptiesTheStore(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	base := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	saveAt(t, s, base, "one")
	saveAt(t, s, base.Add(time.Hour), "two")

	removed, err := s.Clear()
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("Clear removed %v, want 2 files", removed)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d entries left after Clear", len(entries))
	}
	if _, err := os.Lstat(filepath.Join(s.Dir, LatestLink)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the latest symlink survived Clear: %v", err)
	}
	if _, _, err := s.Latest(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Latest on an empty store returned %v", err)
	}
}

func TestLatestFallsBackWhenTheLinkIsBroken(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	at := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	path := saveAt(t, s, at, "one")
	if err := os.Remove(filepath.Join(s.Dir, LatestLink)); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got != path {
		t.Errorf("Latest = %s, want %s", got, path)
	}
}

func TestListOnMissingDirectory(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "absent")}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List returned %d entries", len(entries))
	}
}

func TestDefaultStoreLocation(t *testing.T) {
	s, err := DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	want := filepath.Join("Library", "Application Support", "storix", "scans")
	if filepath.Base(filepath.Dir(s.Dir)) != "storix" || !filepath.IsAbs(s.Dir) {
		t.Errorf("DefaultStore = %s, want an absolute path ending in %s", s.Dir, want)
	}
}

func TestFresh(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	good := Meta{
		Schema:  SchemaVersion,
		Storix:  "1.0.0",
		Written: now.Add(-10 * time.Minute),
		Root:    "/System/Volumes/Data",
		Roots:   []string{"/System/Volumes/Data"},
	}
	roots := []string{"/System/Volumes/Data"}

	cases := []struct {
		name  string
		meta  func(Meta) Meta
		roots []string
		ok    bool
		want  string
	}{
		{"fresh", func(m Meta) Meta { return m }, roots, true, ""},
		{"no roots requested", func(m Meta) Meta { return m }, nil, true, ""},
		{"root order does not matter",
			func(m Meta) Meta { m.Roots = []string{"/b", "/a"}; return m },
			[]string{"/a", "/b"}, true, ""},
		{"too old",
			func(m Meta) Meta { m.Written = now.Add(-90 * time.Minute); return m },
			roots, false, "old"},
		{"exactly one hour old",
			func(m Meta) Meta { m.Written = now.Add(-time.Hour); return m },
			roots, false, "old"},
		{"other version",
			func(m Meta) Meta { m.Storix = "0.9.0"; return m },
			roots, false, "written by storix 0.9.0"},
		{"incomplete",
			func(m Meta) Meta { m.Incomplete = true; return m },
			roots, false, "interrupted"},
		{"other schema",
			func(m Meta) Meta { m.Schema = SchemaVersion + 1; return m },
			roots, false, "schema"},
		{"other roots",
			func(m Meta) Meta { return m },
			[]string{"/Users/andrewsam"}, false, "covers"},
		{"extra root",
			func(m Meta) Meta { return m },
			[]string{"/System/Volumes/Data", "/Volumes/ext"}, false, "covers"},
		{"no timestamp",
			func(m Meta) Meta { m.Written = time.Time{}; return m },
			roots, false, "timestamp"},
		{"dated in the future",
			func(m Meta) Meta { m.Written = now.Add(time.Minute); return m },
			roots, false, "future"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := Fresh(tc.meta(good), now, "1.0.0", tc.roots)
			if ok != tc.ok {
				t.Fatalf("Fresh = %v (%s), want %v", ok, reason, tc.ok)
			}
			if !ok && !strings.Contains(reason, tc.want) {
				t.Errorf("reason %q does not mention %q", reason, tc.want)
			}
			if ok && reason != "" {
				t.Errorf("fresh cache came with reason %q", reason)
			}
		})
	}
}

func TestAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{9 * time.Minute, "9m"},
		{90 * time.Minute, "1h30m"},
		{50 * time.Hour, "2d02h"},
		{-time.Second, "in the future"},
	}
	for _, tc := range cases {
		if got := Age(tc.d); got != tc.want {
			t.Errorf("Age(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
