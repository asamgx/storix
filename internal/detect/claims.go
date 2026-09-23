package detect

import (
	"os"
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// DefaultEnv is the environment a real scan gives its detectors: commands run
// through runner, files read through the sanctioned reader, and the home of
// the invoking user rather than of root.
func DefaultEnv(runner probe.Runner, home string) Env {
	if runner == nil {
		runner = &probe.Exec{}
	}
	env := Env{
		Runner:   runner,
		Home:     home,
		Euid:     os.Geteuid(),
		ReadFile: ReadFile,
		Stat:     Stat,
	}
	if e, ok := runner.(*probe.Exec); ok {
		env.LookPath = e.LookPath
		if env.Home == "" {
			env.Home = e.HomeDir()
		}
	}
	if env.LookPath == nil {
		if lp, ok := runner.(interface {
			LookPath(string) (string, error)
		}); ok {
			env.LookPath = lp.LookPath
		}
	}
	if env.LookPath == nil {
		exec := &probe.Exec{}
		env.LookPath = exec.LookPath
	}
	return env
}

// Has reports whether the tool is on the probe path.
func (e Env) Has(tool string) bool {
	if e.LookPath == nil {
		return false
	}
	_, err := e.LookPath(tool)
	return err == nil
}

// Path joins a path onto the environment's home. It is how a detector names
// "~/.orbstack" without every detector re-deriving the home.
func (e Env) Path(rel ...string) string {
	if e.Home == "" {
		return ""
	}
	return path.Join(append([]string{e.Home}, rel...)...)
}

// Target is one path a detector wants to claim, described the way a catalog
// rule describes one. The detector names the path; this package looks it up in
// the tree, skips it when the walk never saw it, and builds both the claim and
// the summary row from the node's own size.
type Target struct {
	// Path is the display path, "/Users/andrew/.orbstack".
	Path     string
	Bucket   classify.Bucket
	Category string
	Owner    string
	// OwnerKeys are the prefixed join keys the application footprint uses.
	OwnerKeys []string
	Reclaim   classify.Reclaim
	Explain   string
	// Kind is the summary row's kind ("cache", "data", "image", …). An
	// empty Kind claims the path without listing it as a tool.
	Kind string
	// Name is the summary row's label; empty uses Owner.
	Name string
	// Version and Current describe an installed version, for the tools
	// that keep several.
	Version string
	Current bool
	Note    string
	// Evidence are extra why-panel lines, beyond the one naming the
	// detector and the path.
	Evidence []string
}

// Claims turns a detector's targets into claims and summary rows.
//
// A target whose path the walk never retained is dropped rather than guessed
// at: a claim with no node cannot be attributed to any bytes, and a tool row
// showing a path with no size would be worse than not showing it.
func Claims(t *walk.Tree, detector string, targets []Target) ([]classify.Claim, []Tool) {
	claims := make([]classify.Claim, 0, len(targets))
	tools := make([]Tool, 0, len(targets))
	for _, tg := range targets {
		n, ok := lookup(t, tg.Path)
		if !ok {
			continue
		}
		claims = append(claims, Claim(n, detector, tg))
		if tg.Kind != "" {
			tools = append(tools, ToolOf(n, tg))
		}
	}
	return claims, tools
}

// Claim builds one detector claim about one node.
func Claim(n *walk.Node, detector string, tg Target) classify.Claim {
	ev := make([]string, 0, len(tg.Evidence)+1)
	ev = append(ev, "detector "+detector+" claimed "+n.Display())
	ev = append(ev, tg.Evidence...)
	return classify.Claim{
		Node:      n,
		Bucket:    tg.Bucket,
		Category:  tg.Category,
		Owner:     tg.Owner,
		OwnerKeys: tg.OwnerKeys,
		Reclaim:   tg.Reclaim,
		Source:    classify.Source{Kind: classify.SourceDetector, ID: detector, Detector: detector},
		Evidence:  ev,
		Depth:     depthOf(tg.Path),
	}
}

// ToolOf builds the summary row for a claimed node.
func ToolOf(n *walk.Node, tg Target) Tool {
	name := tg.Name
	if name == "" {
		name = tg.Owner
	}
	return Tool{
		Name:    name,
		Kind:    tg.Kind,
		Path:    n.Display(),
		Node:    n.ID,
		Bytes:   n.Bytes,
		Reclaim: tg.Reclaim,
		Current: tg.Current,
		Version: tg.Version,
		Note:    tg.Note,
	}
}

// Lookup finds the node at a display path, or reports that the walk never
// retained it. It is exported because a detector sometimes needs the node
// itself — the size of a disk image, say — rather than a claim over it.
func Lookup(t *walk.Tree, display string) (*walk.Node, bool) { return lookup(t, display) }

// lookup is Lookup with the nil checks.
func lookup(t *walk.Tree, display string) (*walk.Node, bool) {
	if t == nil || display == "" {
		return nil, false
	}
	return t.Lookup(mac.ScanPath(display))
}

// depthOf is how many segments a display path has, which is the specificity
// a detector claim carries. Detector claims outrank every rule whatever their
// depth, so this only ever orders two detectors that named the same node.
func depthOf(display string) uint16 {
	trimmed := strings.Trim(path.Clean(display), "/")
	if trimmed == "" || trimmed == "." {
		return 0
	}
	return uint16(strings.Count(trimmed, "/") + 1)
}
