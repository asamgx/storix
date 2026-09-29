// Package ruby detects the Ruby toolchains: the gems RubyGems installed, the
// interpreters rbenv and rvm manage, and the bundles Bundler cached.
//
// macOS ships a Ruby of its own, so this detector always has something to
// report and the interesting question is which Ruby the gems belong to.
// `gem env gemdir` answers it for whichever ruby is first on the path, which
// on a machine with rbenv is the shimmed one and on a machine without it is
// /Library/Ruby. The directory it names is claimed even when it is somewhere
// none of the static rules would have looked.
//
// The version markers are read rather than executed. `rbenv version-name`
// would start a shell function; ~/.rbenv/version is one line of text, and
// reading it through the sanctioned reader costs one open.
package ruby

import (
	"context"
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "ruby"

func init() { detect.Register(240, New()) }

// Detector finds the Ruby toolchains.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are what the probe learned.
type Facts struct {
	// GemDir is what `gem env gemdir` reported, as a display path.
	GemDir string `json:"gem_dir,omitempty"`
	// Rbenv is the version ~/.rbenv/version names, the one a directory
	// without its own .ruby-version gets.
	Rbenv string `json:"rbenv_version,omitempty"`
	// Found are the home-relative directories that existed.
	Found []string `json:"found,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// location is one directory a Ruby toolchain uses.
type location struct {
	rel      string
	abs      string
	owner    string
	keys     []string
	category string
	reclaim  classify.Reclaim
	kind     string
	name     string
	explain  string
}

// locations are the directories docs/04 names.
var locations = []location{
	{
		rel: ".gem", owner: "RubyGems", keys: []string{"cli:gem"}, category: "Ruby gems",
		reclaim: classify.ToolManaged, kind: "data", name: "User gems",
		explain: "gems installed for this user; `gem uninstall` removes one",
	},
	{
		abs: "/Library/Ruby/Gems", owner: "RubyGems", keys: []string{"cli:gem"}, category: "Ruby gems",
		reclaim: classify.ToolManaged, kind: "data", name: "System gems",
		explain: "gems installed into the Ruby macOS ships",
	},
	{
		rel: ".rbenv", owner: "rbenv", keys: []string{"cli:rbenv"}, category: "Ruby versions",
		reclaim: classify.ToolManaged, kind: "data",
		explain: "rbenv's home: the interpreters it built and the shims that select them",
	},
	{
		rel: ".rbenv/versions", owner: "rbenv", keys: []string{"cli:rbenv"}, category: "Ruby versions",
		reclaim: classify.ToolManaged, kind: "versions", name: "rbenv versions",
		explain: "Ruby interpreters rbenv built; `rbenv uninstall` removes one",
	},
	{
		rel: ".rvm", owner: "rvm", keys: []string{"cli:rvm"}, category: "Ruby versions",
		reclaim: classify.ToolManaged, kind: "versions",
		explain: "rvm's interpreters and gemsets; `rvm remove` removes one",
	},
	{
		rel: ".bundle", owner: "Bundler", keys: []string{"cli:bundle"}, category: "Ruby gems",
		reclaim: classify.Unknown, kind: "data",
		explain: "Bundler's per-user configuration and cache",
	},
	{
		rel: ".bundle/cache", owner: "Bundler", keys: []string{"cli:bundle"}, category: "Ruby gems",
		reclaim: classify.Regenerable, kind: "cache", name: "Bundler cache",
		explain: "gem archives Bundler kept; `bundle install` downloads them again",
	},
}

// rbenvVersionFile is the marker naming the interpreter rbenv selects when
// nothing nearer does.
const rbenvVersionFile = ".rbenv/version"

// Probe asks RubyGems where its gems are and reads rbenv's version marker.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	f := &Facts{}
	for _, loc := range locations {
		if p := absolute(env.Home, loc); p != "" && env.Exists(p) {
			f.Found = append(f.Found, loc.key())
		}
	}

	hasGem := env.Has("gem")
	if !hasGem && len(f.Found) == 0 {
		return nil, detect.Missingf("no `gem` on the path and no ~/.gem, ~/.rbenv or ~/.rvm")
	}

	if env.Home != "" && env.ReadFile != nil {
		if data, err := env.ReadFile(path.Join(env.Home, rbenvVersionFile)); err == nil {
			f.Rbenv = strings.TrimSpace(string(data))
		}
	}

	if !hasGem {
		return f, detect.Degradedf("`gem` is not on the path; the gem directory comes from the static paths")
	}
	res := env.Runner.Run(ctx, probe.Cmd{Name: "gem", Args: []string{"env", "gemdir"}})
	if !res.OK() {
		return f, detect.Degradedf("gem env gemdir: %s", res.Reason())
	}
	f.GemDir = strings.TrimSpace(res.Stdout)
	if f.GemDir == "" {
		return f, detect.Degradedf("gem env gemdir printed nothing")
	}
	return f, nil
}

// Classify claims the directories, the interpreters rbenv keeps, and
// whatever `gem env gemdir` named.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	evidence := evidenceLines(facts)

	targets := make([]detect.Target, 0, len(locations)+8)
	for _, loc := range locations {
		p := absolute(cx.Home, loc)
		if p == "" {
			continue
		}
		name := loc.name
		if name == "" {
			name = loc.owner
		}
		targets = append(targets, detect.Target{
			Path: p, Bucket: classify.BucketDeveloper, Category: loc.category,
			Owner: loc.owner, OwnerKeys: loc.keys, Reclaim: loc.reclaim,
			Explain: loc.explain, Kind: loc.kind, Name: name, Evidence: evidence,
		})
	}
	targets = append(targets, versionTargets(t, cx.Home, facts, evidence)...)
	if gemDir := gemDirTarget(cx.Home, facts, evidence); gemDir != nil {
		targets = append(targets, *gemDir)
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// versionTargets are the interpreters under ~/.rbenv/versions, one row each,
// with the one rbenv selects marked current.
//
// The list comes from the tree rather than from a directory listing, because
// the tree is what a cached scan still has. A version whose directory the
// walk folded away is not reported at all, which is right: it had no bytes
// worth reporting.
func versionTargets(t *walk.Tree, home string, f *Facts, evidence []string) []detect.Target {
	if home == "" {
		return nil
	}
	root := path.Join(home, ".rbenv/versions")
	n, ok := detect.Lookup(t, root)
	if !ok {
		return nil
	}
	current := ""
	if f != nil {
		current = f.Rbenv
	}

	out := make([]detect.Target, 0, len(n.Children))
	for _, child := range n.Children {
		if !child.IsDir() {
			continue
		}
		note := ""
		if child.Name == current {
			note = "the version rbenv selects when nothing nearer does"
		}
		out = append(out, detect.Target{
			Path: path.Join(root, child.Name), Bucket: classify.BucketDeveloper,
			Category: "Ruby versions", Owner: "rbenv", OwnerKeys: []string{"cli:rbenv"},
			Reclaim: classify.ToolManaged, Kind: "toolchain", Name: "ruby " + child.Name,
			Version: child.Name, Current: child.Name == current, Note: note,
			Explain:  "the Ruby " + child.Name + " interpreter rbenv built; `rbenv uninstall " + child.Name + "` removes it",
			Evidence: evidence,
		})
	}
	return out
}

// gemDirTarget claims whatever `gem env gemdir` named.
//
// It is the one thing in this detector the tables cannot supply: the live gem
// directory is inside whichever Ruby is first on the path, which on a machine
// with rbenv is several levels down inside an interpreter and on a machine
// without it is a versioned directory under /Library/Ruby/Gems. Either way it
// is deeper and more specific than the table entry above it, so the claim
// stands beside that one rather than replacing it — unless the tool named
// exactly a path the table already has, in which case there is nothing to add.
func gemDirTarget(home string, f *Facts, evidence []string) *detect.Target {
	if f == nil || f.GemDir == "" {
		return nil
	}
	dir := path.Clean(f.GemDir)
	for _, loc := range locations {
		if p := absolute(home, loc); p != "" && dir == p {
			return nil
		}
	}
	return &detect.Target{
		Path: dir, Bucket: classify.BucketDeveloper, Category: "Ruby gems",
		Owner: "RubyGems", OwnerKeys: []string{"cli:gem"}, Reclaim: classify.ToolManaged,
		Kind: "data", Name: "Installed gems",
		Explain:  "where `gem env gemdir` says gems are installed; `gem cleanup` removes superseded versions",
		Evidence: evidence,
	}
}

// evidenceLines are the why-panel lines every claim carries.
func evidenceLines(f *Facts) []string {
	if f == nil {
		return []string{"ruby did not answer; the paths come from the static catalog"}
	}
	var out []string
	if f.GemDir != "" {
		out = append(out, "`gem env gemdir` → "+f.GemDir)
	}
	if f.Rbenv != "" {
		out = append(out, "~/.rbenv/version names "+f.Rbenv)
	}
	return out
}

// absolute resolves a location against a home.
func absolute(home string, loc location) string {
	if loc.abs != "" {
		return loc.abs
	}
	if home == "" {
		return ""
	}
	return path.Join(home, loc.rel)
}

// key is how a location is named in the facts.
func (loc location) key() string {
	if loc.abs != "" {
		return loc.abs
	}
	return loc.rel
}
