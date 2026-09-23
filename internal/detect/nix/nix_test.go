package nix_test

import (
	"context"
	"errors"
	"io/fs"

	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/nix"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/walk"
)

// env builds an environment whose commands come from a fixture and whose
// /nix exists or does not, as asked.
func env(t *testing.T, fixture string, store bool) detect.Env {
	t.Helper()
	replay, err := probe.LoadFixture(fixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	return detect.Env{
		Runner: replay, Home: detecttest.Home, Euid: 501,
		LookPath: replay.LookPath,
		ReadFile: detect.ReadFile, ReadDir: detect.ReadDir,
		Stat: func(p string) (detect.FileInfo, error) {
			if store && (p == "/nix" || strings.HasPrefix(p, "/nix/")) {
				return detect.FileInfo{Name: p, IsDir: true, Mode: fs.ModeDir}, nil
			}
			return detect.FileInfo{}, fs.ErrNotExist
		},
	}
}

// TestThisMachineIsMissing: there is no /nix here and no nix on the path.
func TestThisMachineIsMissing(t *testing.T) {
	_, err := detecttest.Probe(t, nix.New(), env(t, "testdata/this-machine.json", false))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
}

// TestStore: nix describes its own store, and the description becomes the
// evidence the why panel shows.
func TestStore(t *testing.T) {
	facts, err := detecttest.Probe(t, nix.New(), env(t, "testdata/store.json", true))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*nix.Facts)
	if got.URL != "daemon" || got.Version != "2.24.9" {
		t.Errorf("facts = %+v", got)
	}
	// "trusted" comes back as a number in some releases and a boolean in
	// others; both have to mean the same thing.
	if !got.Trusted {
		t.Error("trusted:1 was not read as trusted")
	}
}

// TestStoreWithoutTheBinary: a store whose profile was never sourced still
// holds gigabytes, so it is a degradation rather than an absence.
func TestStoreWithoutTheBinary(t *testing.T) {
	facts, err := detecttest.Probe(t, nix.New(), env(t, "testdata/missing.json", true))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if facts == nil {
		t.Error("no facts at all, so the store cannot be classified")
	}
}

// TestMalformed: output that is not JSON costs the store's own numbers and
// nothing else.
func TestMalformed(t *testing.T) {
	facts, err := detecttest.Probe(t, nix.New(), env(t, "testdata/malformed.json", true))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if got := facts.(*nix.Facts); got.Version != "" {
		t.Errorf("version = %q from unreadable output", got.Version)
	}
}

// TestTimeout: a daemon that did not answer is a degradation.
func TestTimeout(t *testing.T) {
	if _, err := detecttest.Probe(t, nix.New(), env(t, "testdata/timeout.json", true)); !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
}

// TestClassify claims the store and the database.
//
// The tree is rooted at "/" rather than at the data volume, which is the one
// place in these tests that matters: /nix is not a firmlink, so a store that
// is a real directory rather than its own volume is reached by its own
// absolute path. On the machines where nix puts the store on a separate APFS
// volume, the walk's mount guard skips it and these claims match nothing,
// which is correct — those bytes belong to another volume.
func TestClassify(t *testing.T) {
	f := testutil.New(t)
	f.File("nix/store/abc-hello-2.12/bin/hello", 900_000)
	f.File("nix/var/nix/db/db.sqlite", 200_000)

	tree, err := walk.Walk(context.Background(), walk.Options{
		Root: f.Root, ExemptPrefixes: []string{}, SkipNames: []string{}, SkipPaths: []string{},
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	tree.Root.Name = "/"

	facts, err := detecttest.Probe(t, nix.New(), env(t, "testdata/store.json", true))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := nix.New().Classify(tree, facts, classify.Context{Home: detecttest.Home})

	for _, p := range []string{"/nix", "/nix/store", "/nix/var"} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", p, c.Bucket)
		}
		if c.Reclaim != classify.ToolManaged {
			t.Errorf("%s reclaim = %s, want tool-managed: deleting a store path corrupts the database", p, c.Reclaim)
		}
		if !detecttest.HasKey(c, "cli:nix") {
			t.Errorf("%s keys = %v", p, c.OwnerKeys)
		}
		if !strings.Contains(strings.Join(c.Evidence, "\n"), "2.24.9") {
			t.Errorf("%s evidence does not name the version: %v", p, c.Evidence)
		}
	}
	if _, ok := detecttest.Tool(sum, "/nix/store"); !ok {
		t.Error("the store has no summary row")
	}
	// The explanation has to name the command, because it is the only way
	// to reclaim any of this.
	c, _ := detecttest.ClaimAt(claims, "/nix/store")
	if !strings.Contains(strings.Join(c.Evidence, "\n"), "nix-collect-garbage") {
		t.Error("nothing tells the reader how to reclaim the store")
	}
}

// TestClassifyWithoutAStore: a tree with no /nix in it yields no claims.
func TestClassifyWithoutAStore(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{detecttest.Home + "/Documents/note.txt": 10})
	claims, sum := nix.New().Classify(f.Tree, &nix.Facts{}, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims with no store in the tree", len(claims))
	}

}
