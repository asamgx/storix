package scan

import (
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/classify/catalog"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/walk"
)

// Classify runs the catalog over a finished tree.
//
// It runs on every scan and on every cache load rather than being stored,
// because the catalog lives in the binary: a scan read back by a newer storix
// should be classified by the newer rules, not by the ones that happened to
// be compiled in when the cache file was written. The pass costs a few
// hundred milliseconds on a full volume, which is nothing beside the walk.
//
// A catalog that does not compile is a programming error caught by the
// package's own tests, so the failure here is a nil classification rather than
// an error: a scan with no buckets is still a correct scan, and refusing to
// report anything because a rule is malformed would be the worse answer.
func Classify(t *walk.Tree, cfg Config) *classify.Classification {
	if t == nil || t.Root == nil {
		return nil
	}
	e, err := classify.New(catalog.Rules(), classifyContext(t, cfg))
	if err != nil {
		return nil
	}
	return e.Run(t, nil)
}

// classifyContext describes the machine to the catalog: whose home "~" means,
// which other homes the walk saw, and which code roots actually exist.
func classifyContext(t *walk.Tree, cfg Config) classify.Context {
	home := HomeDir()
	return classify.Context{
		Home:      home,
		Users:     otherHomes(t, home),
		CodeRoots: existingRoots(t, cfg.CodeRoots, home),
	}
}

// HomeDir is the invoking user's home as a display path, sudo-aware; see
// mac.InvokingHome. A failed lookup yields the empty string, which leaves the
// "~" rules anchored at "Users/*" alone: every home still classifies, just
// without one of them being singled out as the scan user's.
func HomeDir() string {
	_, home, _ := mac.InvokingHome()
	if home == "" {
		return ""
	}
	return mac.DisplayPath(home)
}

// otherHomes lists the homes under /Users that are not the scan user's. They
// are named explicitly so that another account's Library classifies by the
// same literal rules rather than only by the "Users/*" copies.
func otherHomes(t *walk.Tree, home string) []string {
	n, ok := t.Lookup(mac.ScanPath("/Users"))
	if !ok {
		return nil
	}
	var out []string
	for _, c := range n.Children {
		if !c.IsDir() || strings.HasPrefix(c.Name, ".") || c.Name == "Shared" {
			continue
		}
		if p := "/Users/" + c.Name; p != home {
			out = append(out, p)
		}
	}
	return out
}

// existingRoots filters the code roots to the ones the walk actually met. A
// rule for a directory that is not there costs nothing to match but does cost
// a reader's attention when the catalog is listed, and the filter keeps the
// rule count honest.
func existingRoots(t *walk.Tree, configured []string, home string) []string {
	want := configured
	if want == nil {
		want = classify.DefaultCodeRoots
	}
	out := make([]string, 0, len(want))
	for _, r := range want {
		display := expandHome(r, home)
		if display == "" {
			continue
		}
		if _, ok := t.Lookup(mac.DataRoot + display); ok {
			out = append(out, display)
		}
	}
	return out
}

// expandHome turns a configured code root into a display path.
func expandHome(root, home string) string {
	switch {
	case root == "":
		return ""
	case root == "~":
		return home
	case strings.HasPrefix(root, "~/"):
		if home == "" {
			return ""
		}
		return path.Join(home, root[2:])
	default:
		return mac.DisplayPath(root)
	}
}
