// Package rust detects the Rust toolchain.
//
// The interesting question here is which toolchains are installed and which
// one is in use: a rustup toolchain is around a gigabyte, they accumulate
// (stable, nightly, a pinned 1.7x, a cross target), and only the default one
// is doing anything. `rustup toolchain list` marks it, in one of two ways
// depending on the rustup version: "(default)" on its own, or "(active,
// default)" since rustup 1.28. Both are accepted.
package rust

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
const Name = "rust"

func init() { detect.Register(140, New()) }

// Detector finds the Rust toolchain.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Toolchain is one installed Rust toolchain.
type Toolchain struct {
	Name string `json:"name"`
	// Default is set for the toolchain rustup would use outside a project
	// that pins one.
	Default bool `json:"default,omitempty"`
	// Active is set when rustup also called it active, which newer
	// versions print alongside the default marker.
	Active bool `json:"active,omitempty"`
}

// Facts are what the probe learned.
type Facts struct {
	// Home is the home the probe ran against.
	Home       string      `json:"home,omitempty"`
	Toolchains []Toolchain `json:"toolchains,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// Probe asks rustup which toolchains are installed.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	cargo := env.Path(".cargo")
	rustup := env.Path(".rustup")
	if !env.Has("rustup") && !env.Has("cargo") && !env.Exists(cargo) && !env.Exists(rustup) {
		return nil, detect.Missingf("neither rustup nor cargo is present and ~/.cargo does not exist")
	}

	f := &Facts{Home: env.Home}
	if !env.Has("rustup") {
		return f, detect.Degradedf("`rustup` is not on the probe path; the toolchain directories only")
	}
	res := env.Runner.Run(ctx, probe.Cmd{Name: "rustup", Args: []string{"toolchain", "list"}})
	if !res.OK() {
		return f, detect.Degradedf("rustup toolchain list: %s", res.Reason())
	}
	f.Toolchains = parseToolchains(res.Stdout)
	if len(f.Toolchains) == 0 {
		return f, detect.Degradedf("rustup listed no toolchains")
	}
	return f, nil
}

// parseToolchains reads the listing: one toolchain per line, with the markers
// in parentheses after it.
//
//	stable-aarch64-apple-darwin (active, default)
//	nightly-aarch64-apple-darwin
//
// "no installed toolchains" is a sentence rather than a toolchain, so a line
// with a space in the name part is ignored.
func parseToolchains(out string) []Toolchain {
	var list []Toolchain
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, markers := line, ""
		if i := strings.IndexByte(line, '('); i >= 0 && strings.HasSuffix(line, ")") {
			name = strings.TrimSpace(line[:i])
			markers = line[i+1 : len(line)-1]
		}
		if name == "" || strings.ContainsAny(name, " \t") {
			continue
		}
		tc := Toolchain{Name: name}
		for _, m := range strings.Split(markers, ",") {
			switch strings.TrimSpace(m) {
			case "default":
				tc.Default = true
			case "active":
				tc.Active = true
			}
		}
		list = append(list, tc)
	}
	return list
}

// Classify turns the facts and the tree into claims and tool rows.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}
	cargo := path.Join(home, ".cargo")
	rustup := path.Join(home, ".rustup")
	ev := evidence(facts)

	targets := []detect.Target{
		{
			Path: cargo, Category: "Toolchain", Owner: "Cargo", OwnerKeys: []string{"cli:cargo"},
			Reclaim: classify.ToolManaged,
			Explain: "Cargo's home: downloaded crates, git checkouts and installed binaries",
		},
		{
			Path: path.Join(cargo, "registry"), Category: "Package cache", Owner: "Cargo",
			OwnerKeys: []string{"cli:cargo"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "crate registry",
			Explain: "crates.io downloads and their unpacked sources; Cargo refetches them",
		},
		{
			Path: path.Join(cargo, "git"), Category: "Package cache", Owner: "Cargo",
			OwnerKeys: []string{"cli:cargo"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "git dependencies",
			Explain: "git dependencies Cargo cloned",
		},
		{
			Path: path.Join(cargo, "bin"), Category: "Toolchain", Owner: "Cargo",
			OwnerKeys: []string{"cli:cargo"}, Reclaim: classify.ToolManaged,
			Kind: "toolchain", Name: "installed binaries",
			Explain: "binaries `cargo install` built",
		},
		{
			Path: rustup, Category: "Toolchain", Owner: "rustup", OwnerKeys: []string{"cli:rustup"},
			Reclaim: classify.ToolManaged, Note: toolchainNote(facts),
			Explain: "Rust toolchains; `rustup toolchain uninstall <name>` removes one",
		},
		{
			Path: path.Join(rustup, "downloads"), Category: "Package cache", Owner: "rustup",
			OwnerKeys: []string{"cli:rustup"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "toolchain downloads",
			Explain: "archives rustup downloaded and has already unpacked",
		},
		{
			Path: path.Join(rustup, "tmp"), Category: "Package cache", Owner: "rustup",
			OwnerKeys: []string{"cli:rustup"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "rustup scratch",
			Explain: "rustup's working directory, left behind by interrupted installs",
		},
	}
	targets = append(targets, toolchains(t, facts, rustup)...)

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

// toolchains lists each installed toolchain, marking the default one.
func toolchains(t *walk.Tree, f *Facts, rustup string) []detect.Target {
	names := []string(nil)
	defaults := map[string]bool{}
	if f != nil {
		for _, tc := range f.Toolchains {
			names = append(names, tc.Name)
			defaults[tc.Name] = tc.Default
		}
	}
	if len(names) == 0 {
		names = detect.ChildNames(t, path.Join(rustup, "toolchains"))
	}
	out := make([]detect.Target, 0, len(names))
	for _, name := range names {
		note := "installed, not the default"
		if defaults[name] {
			note = "rustup's default toolchain"
		}
		out = append(out, detect.Target{
			Path: path.Join(rustup, "toolchains", name), Category: "Toolchain", Owner: "rustup",
			OwnerKeys: []string{"cli:rustup"}, Reclaim: classify.ToolManaged,
			Kind: "versions", Name: "toolchain", Version: name, Current: defaults[name], Note: note,
			Explain: "the " + name + " toolchain: rustc, cargo and the standard library",
		})
	}
	return out
}

// toolchainNote says how many toolchains are installed and which is default.
func toolchainNote(f *Facts) string {
	if f == nil || len(f.Toolchains) == 0 {
		return ""
	}
	for _, tc := range f.Toolchains {
		if tc.Default {
			return fmt.Sprintf("%d toolchain(s) installed, %s default", len(f.Toolchains), tc.Name)
		}
	}
	return fmt.Sprintf("%d toolchain(s) installed, none marked default", len(f.Toolchains))
}

// evidence are the why-panel lines every Rust claim carries.
func evidence(f *Facts) []string {
	if f == nil || len(f.Toolchains) == 0 {
		return []string{"rustup did not answer; the paths come from the static catalog"}
	}
	parts := make([]string, 0, len(f.Toolchains))
	for _, tc := range f.Toolchains {
		label := tc.Name
		switch {
		case tc.Default && tc.Active:
			label += " (active, default)"
		case tc.Default:
			label += " (default)"
		}
		parts = append(parts, label)
	}
	return []string{"`rustup toolchain list` → " + strings.Join(parts, ", ")}
}
