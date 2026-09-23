// Package detecttest builds the two halves a detector test needs: a real
// directory tree that mirrors the paths a detector looks for, and an
// environment whose commands are answered from a recorded fixture.
//
// It exists so that a detector can be tested on a machine that does not have
// the tool. The tree is real — the walker's numbers come from lstat, so a
// fixture has to be on a disk — while everything the detector would learn by
// running a command comes from a file recorded on a machine that did have it.
//
// The home directory is in two forms and the difference matters. A probe runs
// before the walk and looks at the real filesystem, so it is given the
// fixture's own absolute path. Classify runs against the finished tree, whose
// root has been renamed to the data volume, so it is given the display path
// the tree shows. [Home] and [Real] name the two.
package detecttest

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/walk"
)

// Home is the display path of the fixture user's home, the one Classify sees.
const Home = "/Users/andrew"

// Fixture is a built tree together with the paths into it.
type Fixture struct {
	// Tree is the walked tree, re-anchored at the data volume so that every
	// node's display path reads as it would on a real machine.
	Tree *walk.Tree
	// Real is the fixture home as an absolute path on the test's disk, which
	// is what a probe stats.
	Real string
	// Context is the classification context Classify is given.
	Context classify.Context
}

// Build creates a tree from a set of display paths and their sizes.
//
// A key ending in "/" is a directory; anything else is a file of that many
// bytes. A negative size makes a sparse file of that apparent size and no
// allocated blocks, which is how the container disk images are written: their
// whole point is that the two numbers differ.
func Build(t *testing.T, files map[string]int64) *Fixture {
	t.Helper()
	f := testutil.New(t)
	for display, size := range files {
		rel := strings.TrimPrefix(path.Clean(display), "/")
		switch {
		case strings.HasSuffix(display, "/") || size == 0:
			f.Dir(rel)
		case size < 0:
			f.Sparse(rel, -size)
		default:
			f.File(rel, int(size))
		}
	}

	tree, err := walk.Walk(context.Background(), walk.Options{
		Root:           f.Root,
		ExemptPrefixes: []string{},
		SkipNames:      []string{},
		SkipPaths:      []string{},
	})
	if err != nil {
		t.Fatalf("detecttest: walk: %v", err)
	}
	tree.Root.Name = mac.DataRoot

	return &Fixture{
		Tree:    tree,
		Real:    filepath.Join(f.Root, strings.TrimPrefix(Home, "/")),
		Context: classify.Context{Home: Home},
	}
}

// Env returns an environment whose commands come from a fixture file and
// whose filesystem is the built tree.
//
// The fixture also decides which tools the machine has: a detector's
// LookPath answers yes for exactly the executables the recording names, so a
// recording made on a machine without podman reproduces a machine without
// podman without the test saying so twice.
func (f *Fixture) Env(t *testing.T, fixture string) detect.Env {
	t.Helper()
	replay, err := probe.LoadFixture(fixture)
	if err != nil {
		t.Fatalf("detecttest: %v", err)
	}
	return detect.Env{
		Runner:   replay,
		Home:     f.Real,
		Euid:     501,
		LookPath: replay.LookPath,
		ReadFile: detect.ReadFile,
		Stat:     detect.Stat,
	}
}

// Node is the size of a node at a display path, or -1 when the tree has none.
// It is how a test asserts that a claim covers the bytes it should.
func (f *Fixture) Node(display string) int64 {
	n, ok := detect.Lookup(f.Tree, display)
	if !ok {
		return -1
	}
	return n.Bytes
}

// Probe runs a detector's probe against a fixture and returns what it learned
// and the state it ended in, which is the pair nearly every detector test
// asserts on.
func Probe(t *testing.T, det detect.Detector, env detect.Env) (detect.Facts, error) {
	t.Helper()
	facts, err := det.Probe(context.Background(), env)
	return facts, err
}

// ClaimAt finds the claim a detector made about one display path.
func ClaimAt(claims []classify.Claim, display string) (classify.Claim, bool) {
	for _, c := range claims {
		if c.Node != nil && c.Node.Display() == display {
			return c, true
		}
	}
	return classify.Claim{}, false
}

// HasKey reports whether a claim carries an owner key.
func HasKey(c classify.Claim, key string) bool {
	for _, k := range c.OwnerKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Runtime finds a summary's runtime by name.
func Runtime(s detect.Summary, name string) (detect.Runtime, bool) {
	for _, rt := range s.Runtimes {
		if rt.Name == name {
			return rt, true
		}
	}
	return detect.Runtime{}, false
}

// Tool finds a summary's tool row by the path it covers.
func Tool(s detect.Summary, display string) (detect.Tool, bool) {
	for _, tool := range s.Tools {
		if tool.Path == display {
			return tool, true
		}
	}
	return detect.Tool{}, false
}
