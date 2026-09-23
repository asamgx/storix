// Package python detects the Python toolchain: the version managers that
// install interpreters and the package managers that cache wheels.
//
// Every one of these tools will say where it keeps its bytes, and the answers
// disagree with each other's defaults often enough to be worth asking: uv
// splits its wheel cache (~/.cache/uv) from the interpreters it downloads
// (~/.local/share/uv/python), pip uses ~/Library/Caches/pip on macOS rather
// than the XDG directory, and pyenv's root moves with $PYENV_ROOT.
//
// `pyenv version-name` is asked as well as `pyenv root`, because "which
// interpreter is current" decides which of several hundred megabytes of
// installed versions is the one in use. On this machine the answer is
// "system", which means none of them is: that is worth saying rather than
// marking an arbitrary one current.
package python

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "python"

func init() { detect.Register(120, New()) }

// Detector finds the Python toolchain.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are what the probe learned.
type Facts struct {
	// Home is the home the probe ran against.
	Home string `json:"home,omitempty"`
	// PyenvRoot is `pyenv root` and PyenvVersion `pyenv version-name`,
	// which is "system" when pyenv is installed but not selecting an
	// interpreter of its own.
	PyenvRoot    string `json:"pyenv_root,omitempty"`
	PyenvVersion string `json:"pyenv_version,omitempty"`
	UvCache      string `json:"uv_cache,omitempty"`
	UvPython     string `json:"uv_python,omitempty"`
	PipCache     string `json:"pip_cache,omitempty"`
	PoetryCache  string `json:"poetry_cache,omitempty"`
	CondaBase    string `json:"conda_base,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// SystemPython reports whether pyenv is deferring to the system interpreter,
// in which case none of the versions it has installed is the current one.
func (f *Facts) SystemPython() bool {
	return f != nil && strings.EqualFold(f.PyenvVersion, "system")
}

// Probe asks each Python tool where it keeps its bytes.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	f := &Facts{Home: env.Home}
	var degraded []string
	found := false

	questions := []struct {
		tool string
		args []string
		dst  *string
	}{
		{"pyenv", []string{"root"}, &f.PyenvRoot},
		{"pyenv", []string{"version-name"}, &f.PyenvVersion},
		{"uv", []string{"cache", "dir"}, &f.UvCache},
		{"uv", []string{"python", "dir"}, &f.UvPython},
		{"pip3", []string{"cache", "dir"}, &f.PipCache},
		{"poetry", []string{"config", "cache-dir"}, &f.PoetryCache},
		{"conda", []string{"info", "--base"}, &f.CondaBase},
	}
	for _, q := range questions {
		if !env.Has(q.tool) {
			continue
		}
		found = true
		res := env.Runner.Run(ctx, probe.Cmd{Name: q.tool, Args: q.args})
		if !res.OK() {
			degraded = append(degraded,
				strings.Join(append([]string{q.tool}, q.args...), " ")+": "+res.Reason())
			continue
		}
		*q.dst = firstLine(res.Stdout)
	}

	if f.PipCache == "" && !env.Has("pip3") && env.Has("pip") {
		found = true
		if res := env.Runner.Run(ctx, probe.Cmd{Name: "pip", Args: []string{"cache", "dir"}}); res.OK() {
			f.PipCache = firstLine(res.Stdout)
		} else {
			degraded = append(degraded, "pip cache dir: "+res.Reason())
		}
	}

	if !found {
		return nil, detect.Missingf("none of pyenv, uv, pip, poetry or conda is present")
	}
	if len(degraded) > 0 {
		return f, detect.Degradedf("%s", strings.Join(degraded, "; "))
	}
	return f, nil
}

// firstLine is a command's answer without the trailing newline.
func firstLine(out string) string {
	out = strings.TrimSpace(out)
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		return strings.TrimSpace(out[:i])
	}
	return out
}

// Classify turns the facts and the tree into claims and tool rows.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}
	measured := func(m, fallback string) string {
		if facts == nil {
			return fallback
		}
		return detect.Prefer(t, detect.Rebase(m, facts.Home, home), fallback)
	}
	get := func(sel func(*Facts) string) string {
		if facts == nil {
			return ""
		}
		return sel(facts)
	}
	ev := evidence(facts)

	pyenv := measured(get(func(f *Facts) string { return f.PyenvRoot }), path.Join(home, ".pyenv"))
	// Which interpreters pyenv has built is something the walk has already
	// seen, so it is read out of the tree rather than listed again.
	installed := detect.ChildNames(t, path.Join(pyenv, "versions"))
	targets := []detect.Target{
		{
			Path: pyenv, Category: "Toolchain", Owner: "pyenv", OwnerKeys: []string{"cli:pyenv"},
			Reclaim: classify.ToolManaged, Kind: "toolchain", Name: "pyenv",
			Note:    pyenvNote(facts, len(installed)),
			Explain: "Python interpreters pyenv built; `pyenv uninstall <version>` removes one",
		},
		{
			Path:     measured(get(func(f *Facts) string { return f.UvCache }), path.Join(home, ".cache/uv")),
			Category: "Package cache", Owner: "uv", OwnerKeys: []string{"cli:uv"},
			Reclaim: classify.Regenerable, Kind: "cache", Name: "uv wheel cache",
			Explain: "wheels and source distributions uv unpacked; `uv cache clean` clears it",
		},
		{
			Path:     measured(get(func(f *Facts) string { return f.UvPython }), path.Join(home, ".local/share/uv/python")),
			Category: "Toolchain", Owner: "uv", OwnerKeys: []string{"cli:uv"},
			Reclaim: classify.ToolManaged, Kind: "toolchain", Name: "uv interpreters",
			Explain: "Python builds uv downloaded to run projects against",
		},
		{
			Path: path.Join(home, ".local/share/uv"), Category: "Toolchain", Owner: "uv",
			OwnerKeys: []string{"cli:uv"}, Reclaim: classify.ToolManaged,
			Explain: "uv's per-user data",
		},
		{
			Path:     measured(get(func(f *Facts) string { return f.PipCache }), path.Join(home, "Library/Caches/pip")),
			Category: "Package cache", Owner: "pip", OwnerKeys: []string{"cli:pip"},
			Reclaim: classify.Regenerable, Kind: "cache", Name: "pip cache",
			Explain: "wheels pip downloaded; `pip cache purge` clears it",
		},
		{
			Path:     measured(get(func(f *Facts) string { return f.PoetryCache }), path.Join(home, "Library/Caches/pypoetry")),
			Category: "Package cache", Owner: "Poetry", OwnerKeys: []string{"cli:poetry"},
			Reclaim: classify.Regenerable, Kind: "cache", Name: "Poetry cache",
			Note:    "Poetry keeps project virtual environments in here as well as downloads",
			Explain: "Poetry's download cache and its virtual environments",
		},
		{
			Path: path.Join(home, ".local/pipx"), Category: "Toolchain", Owner: "pipx",
			OwnerKeys: []string{"cli:pipx"}, Reclaim: classify.ToolManaged,
			Kind: "toolchain", Name: "pipx environments",
			Explain: "applications pipx installed, each in its own environment",
		},
		{
			Path: path.Join(home, ".cache/pre-commit"), Category: "Tool cache", Owner: "pre-commit",
			OwnerKeys: []string{"cli:pre-commit"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "pre-commit hooks",
			Explain: "hook environments pre-commit built; `pre-commit clean` removes them",
		},
	}
	targets = append(targets, condaTargets(facts, home)...)
	targets = append(targets, interpreters(facts, pyenv, installed)...)

	for i := range targets {
		targets[i].Bucket = classify.BucketDeveloper
		targets[i].Evidence = ev
	}
	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// condaTargets are the conda installation, wherever it is. The two default
// locations are claimed as well as the measured one, because a machine can
// carry a Miniconda that is no longer on the path.
func condaTargets(f *Facts, home string) []detect.Target {
	paths := []string{path.Join(home, "miniconda3"), path.Join(home, "anaconda3")}
	if f != nil && f.CondaBase != "" {
		if base := detect.Rebase(f.CondaBase, f.Home, home); base != "" {
			paths = append([]string{base}, paths...)
		}
	}
	seen := make(map[string]bool, len(paths))
	var out []detect.Target
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, detect.Target{
			Path: p, Category: "Toolchain", Owner: "Conda", OwnerKeys: []string{"cli:conda"},
			Reclaim: classify.ToolManaged, Kind: "toolchain", Name: "conda",
			Explain: "a conda installation: its environments and its package cache",
		})
	}
	return out
}

// interpreters lists the Python versions pyenv has installed, marking the
// current one.
//
// When pyenv is set to "system" none of them is current and every one of them
// is dead weight until a project asks for it, which is exactly what the note
// says rather than picking a version to flag.
func interpreters(f *Facts, root string, versions []string) []detect.Target {
	current := ""
	if f != nil && !f.SystemPython() {
		current = f.PyenvVersion
	}
	out := make([]detect.Target, 0, len(versions))
	for _, v := range versions {
		note := "installed, not selected"
		if v == current {
			note = "pyenv's current version"
		} else if f.SystemPython() {
			note = "installed; pyenv is set to the system interpreter"
		}
		out = append(out, detect.Target{
			Path: path.Join(root, "versions", v), Category: "Toolchain", Owner: "pyenv",
			OwnerKeys: []string{"cli:pyenv"}, Reclaim: classify.ToolManaged,
			Kind: "versions", Name: "python", Version: v, Current: v == current, Note: note,
			Explain: "the Python " + v + " interpreter pyenv built, and its site-packages",
		})
	}
	return out
}

// pyenvNote says what pyenv is currently selecting.
func pyenvNote(f *Facts, installed int) string {
	switch {
	case f == nil || f.PyenvVersion == "":
		return ""
	case f.SystemPython():
		return fmt.Sprintf("%d interpreters installed; pyenv is set to `system`, so none of them is in use",
			installed)
	default:
		return fmt.Sprintf("%d interpreters installed, %s current", installed, f.PyenvVersion)
	}
}

// evidence are the why-panel lines every Python claim carries.
func evidence(f *Facts) []string {
	if f == nil {
		return []string{"the python detector did not answer; the paths come from the static catalog"}
	}
	var out []string
	for _, q := range []struct{ cmd, answer string }{
		{"pyenv root", f.PyenvRoot},
		{"pyenv version-name", f.PyenvVersion},
		{"uv cache dir", f.UvCache},
		{"uv python dir", f.UvPython},
		{"pip cache dir", f.PipCache},
		{"poetry config cache-dir", f.PoetryCache},
		{"conda info --base", f.CondaBase},
	} {
		if q.answer != "" {
			out = append(out, "`"+q.cmd+"` → "+q.answer)
		}
	}
	return out
}
