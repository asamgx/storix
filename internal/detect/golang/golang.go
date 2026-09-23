// Package golang detects the Go toolchain.
//
// Go is the cheapest detector to run and one of the most worthwhile: a single
// `go env GOCACHE GOMODCACHE GOPATH GOROOT` answers four questions in one
// process, and two of those answers are build and module caches that grow
// without bound and are safe to delete. The package is named golang rather
// than go because "go" is a keyword; the detector's own name is "go".
package golang

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
const Name = "go"

func init() { detect.Register(130, New()) }

// Detector finds the Go toolchain.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are what the probe learned: the four directories `go env` names, in
// the order they were asked for.
type Facts struct {
	// Home is the home the probe ran against.
	Home string `json:"home,omitempty"`
	// GoCache is the build cache and GoModCache the module cache; both are
	// safe to delete and both are measured in gigabytes on a working
	// machine.
	GoCache    string `json:"gocache,omitempty"`
	GoModCache string `json:"gomodcache,omitempty"`
	GoPath     string `json:"gopath,omitempty"`
	// GoRoot is the toolchain itself. It is recorded as evidence and not
	// claimed: on this machine it is a symlink into Homebrew's Cellar, and
	// claiming it would have two detectors arguing over bytes Homebrew
	// already owns.
	GoRoot string `json:"goroot,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// envVars are the variables asked for, in the order `go env` prints them.
var envVars = []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT"}

// Probe runs one `go env` and reads the four lines back.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	if !env.Has("go") {
		return nil, detect.Missingf("`go` is not on the probe path")
	}
	res := env.Runner.Run(ctx, probe.Cmd{Name: "go", Args: append([]string{"env"}, envVars...)})
	if !res.OK() {
		return &Facts{Home: env.Home}, detect.Degradedf("go env: %s", res.Reason())
	}

	f := &Facts{Home: env.Home}
	dst := []*string{&f.GoCache, &f.GoModCache, &f.GoPath, &f.GoRoot}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	for i, line := range lines {
		if i >= len(dst) {
			break
		}
		*dst[i] = strings.TrimSpace(line)
	}
	if len(lines) < len(envVars) {
		return f, detect.Degradedf("go env printed %d of %d lines", len(lines), len(envVars))
	}
	return f, nil
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

	gopath := measured(get(func(f *Facts) string { return f.GoPath }), path.Join(home, "go"))
	targets := []detect.Target{
		{
			Path: gopath, Category: "Toolchain", Owner: "Go", Reclaim: classify.ToolManaged,
			Explain: "the Go workspace: the module cache and the binaries `go install` wrote",
		},
		{
			Path:     measured(get(func(f *Facts) string { return f.GoCache }), path.Join(home, "Library/Caches/go-build")),
			Category: "Build cache", Owner: "Go", Reclaim: classify.Regenerable,
			Kind: "cache", Name: "build cache",
			Explain: "compiled package objects; `go clean -cache` clears it and the next build refills it",
		},
		{
			Path:     measured(get(func(f *Facts) string { return f.GoModCache }), path.Join(home, "go/pkg/mod")),
			Category: "Package cache", Owner: "Go", Reclaim: classify.Regenerable,
			Kind: "cache", Name: "module cache",
			Note:    "read-only on disk; `go clean -modcache` is the only way to remove it",
			Explain: "every version of every module any build has needed",
		},
		{
			Path: path.Join(gopath, "bin"), Category: "Toolchain", Owner: "Go",
			Reclaim: classify.ToolManaged, Kind: "toolchain", Name: "installed binaries",
			Explain: "binaries `go install` wrote; reinstalling rebuilds them",
		},
	}
	for i := range targets {
		targets[i].Bucket = classify.BucketDeveloper
		targets[i].OwnerKeys = []string{"cli:go"}
		targets[i].Evidence = evidence(facts)
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// evidence are the why-panel lines every Go claim carries.
func evidence(f *Facts) []string {
	if f == nil {
		return []string{"the go detector did not answer; the paths come from the static catalog"}
	}
	var out []string
	for i, v := range []string{f.GoCache, f.GoModCache, f.GoPath, f.GoRoot} {
		if v != "" {
			out = append(out, "`go env "+envVars[i]+"` → "+v)
		}
	}
	return out
}
