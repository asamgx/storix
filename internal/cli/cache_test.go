package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/cache"
	"github.com/asamgx/storix/internal/walk"
)

func cliTestStore(t *testing.T, saves int, written time.Time) cache.Store {
	t.Helper()
	s := cache.Store{Dir: t.TempDir()}
	for i := range saves {
		root := &walk.Node{Name: "/System/Volumes/Data", Kind: walk.KindDir, Bytes: 4096}
		child := &walk.Node{Name: "file.bin", Parent: root, Kind: walk.KindFile, Bytes: 4096, Files: 1}
		root.Children = []*walk.Node{child}
		tree := &walk.Tree{Root: root, Nodes: []*walk.Node{root, child}}
		meta := cache.Meta{
			Storix:  "1.0.0",
			Written: written.Add(time.Duration(i) * time.Hour),
			Root:    root.Name,
			Roots:   []string{root.Name},
		}
		if i == 0 {
			meta.Incomplete = true
		}
		if _, err := s.Save(meta, tree); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	return s
}

func TestRunCacheListEmpty(t *testing.T) {
	s := cache.Store{Dir: filepath.Join(t.TempDir(), "absent")}
	var out bytes.Buffer
	if err := runCacheList(&out, s, time.Now()); err != nil {
		t.Fatalf("runCacheList: %v", err)
	}
	if !strings.Contains(out.String(), "no stored scans") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunCacheList(t *testing.T) {
	written := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	s := cliTestStore(t, 3, written)
	now := written.Add(3 * time.Hour)

	var out bytes.Buffer
	if err := runCacheList(&out, s, now); err != nil {
		t.Fatalf("runCacheList: %v", err)
	}
	got := out.String()
	for _, want := range []string{"WRITTEN", "AGE", "ROOT", "SIZE", "STATE", "PATH",
		"/System/Volumes/Data", "incomplete", "latest", "1h", "3h00m"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing does not mention %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "scan-"); n < 3 {
		t.Errorf("listing shows %d scan files, want 3:\n%s", n, got)
	}
}

func TestRunCachePrune(t *testing.T) {
	s := cliTestStore(t, 4, time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC))
	var out bytes.Buffer
	if err := runCachePrune(&out, s, 2); err != nil {
		t.Fatalf("runCachePrune: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "removed 2 scans") {
		t.Errorf("output = %q", got)
	}
	if strings.Count(got, "removed /") != 2 {
		t.Errorf("the removed files are not listed:\n%s", got)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("%d scans left, want 2", len(entries))
	}
}

func TestRunCacheClear(t *testing.T) {
	s := cliTestStore(t, 2, time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC))
	var out bytes.Buffer
	if err := runCacheClear(&out, s); err != nil {
		t.Fatalf("runCacheClear: %v", err)
	}
	if !strings.Contains(out.String(), "removed 2 scans") {
		t.Errorf("output = %q", out.String())
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d scans left after clear", len(entries))
	}
}

func TestCacheCommandTree(t *testing.T) {
	cmd := newCacheCmd()
	want := map[string]bool{"list": false, "prune": false, "clear": false, "path": false}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; !ok {
			t.Errorf("unexpected subcommand %q", c.Name())
			continue
		}
		want[c.Name()] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("cache has no %q subcommand", name)
		}
	}
	prune, _, err := cmd.Find([]string{"prune"})
	if err != nil {
		t.Fatalf("find prune: %v", err)
	}
	if f := prune.Flags().Lookup("keep"); f == nil {
		t.Fatal("prune has no --keep flag")
	} else if f.DefValue != "10" {
		t.Errorf("--keep defaults to %q, want 10", f.DefValue)
	}
}
