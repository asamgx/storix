// Package nix detects the Nix store.
//
// Nix is here rather than under containers because it is a package manager:
// /nix/store is a content-addressed cache of every build output this machine
// has ever realised, and the way to shrink it is `nix-collect-garbage`, not
// `rm`. The store is read-only by design and deleting a path out of it
// corrupts the database, so the reclaim tag is tool-managed and the
// explanation names the command.
//
// The probe asks the store how big it thinks it is, which is the one number
// a walk cannot produce: a store on its own APFS volume, which is how the
// Determinate installer sets one up, is not on the volume being walked at
// all, and `nix store info` is then the only evidence there is.
package nix

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "nix"

func init() { detect.Register(270, New()) }

// storeRoot is where every Nix installation puts its store. The path is not
// configurable in practice: it is baked into every binary the store holds.
const storeRoot = "/nix"

// Detector finds the Nix store.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are what `nix store info` reported.
type Facts struct {
	// URL is the store the numbers describe, "local" for this machine's.
	URL string `json:"url,omitempty"`
	// Version is the nix version that answered.
	Version string `json:"version,omitempty"`
	// Trusted is whether this user is a trusted user of the store; it is
	// what decides whether `nix-collect-garbage` can do anything.
	Trusted bool `json:"trusted,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// Probe asks nix about its store.
//
// The gate is the store directory, not the binary: an installation whose
// profile was never sourced still has fifteen gigabytes in /nix, and
// reporting it as absent because the shell could not find `nix` would hide
// exactly the bytes the user is looking for.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	hasStore := env.Exists(storeRoot)
	hasBinary := env.Has("nix")
	if !hasStore && !hasBinary {
		return nil, detect.Missingf("no /nix store and no `nix` on the path")
	}
	if !hasBinary {
		return &Facts{}, detect.Degradedf("/nix exists but `nix` is not on the path; the store's own numbers are unavailable")
	}

	res := env.Runner.Run(ctx, probe.Cmd{Name: "nix", Args: []string{"store", "info", "--json"}})
	if !res.OK() {
		f := &Facts{}
		if !hasStore {
			return nil, detect.Missingf("`nix store info` failed and there is no /nix store: %s", res.Reason())
		}
		return f, detect.Degradedf("nix store info: %s", res.Reason())
	}

	f, err := parseInfo(res.Stdout)
	if err != nil {
		return &Facts{}, detect.Degradedf("nix store info printed something unreadable: %s", err)
	}
	return f, nil
}

// rawInfo is `nix store info --json`. The field names have changed across
// versions — the command itself was `nix store ping` until 2.19 — so every
// field is optional and an unreadable one costs its own line rather than the
// whole probe.
type rawInfo struct {
	URL     string `json:"url"`
	Version string `json:"version"`
	Trusted any    `json:"trusted"`
}

// parseInfo decodes the store's own description of itself.
func parseInfo(out string) (*Facts, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, errors.New("no output")
	}
	var raw rawInfo
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, err
	}
	f := &Facts{URL: raw.URL, Version: raw.Version}
	// "trusted" has been a bool and a 0/1 number in different releases.
	switch v := raw.Trusted.(type) {
	case bool:
		f.Trusted = v
	case float64:
		f.Trusted = v != 0
	}
	return f, nil
}

// Classify claims the store and its database.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, _ classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	evidence := evidenceLines(facts)

	targets := []detect.Target{
		{
			Path: storeRoot, Category: "Nix", Owner: "Nix", Reclaim: classify.ToolManaged, Kind: "data",
			Explain: "the Nix installation; everything under it is managed by nix itself",
		},
		{
			Path: storeRoot + "/store", Category: "Nix store", Owner: "Nix",
			Reclaim: classify.ToolManaged, Kind: "cache", Name: "Nix store",
			Explain: "every build output this machine has realised; `nix-collect-garbage -d` removes the unreferenced ones",
		},
		{
			Path: storeRoot + "/var", Category: "Nix", Owner: "Nix",
			Reclaim: classify.ToolManaged, Kind: "data", Name: "Nix database and profiles",
			Explain: "the store database and the profile generations that keep paths alive",
		},
	}
	for i := range targets {
		targets[i].Bucket = classify.BucketDeveloper
		targets[i].OwnerKeys = []string{"cli:nix"}
		targets[i].Evidence = evidence
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// evidenceLines are the why-panel lines every claim carries.
func evidenceLines(f *Facts) []string {
	if f == nil || (f.URL == "" && f.Version == "") {
		return []string{"nix did not describe its store; the paths come from the static catalog"}
	}
	line := "`nix store info`: " + f.URL
	if f.Version != "" {
		line += ", nix " + f.Version
	}
	if !f.Trusted {
		line += "; this user is not a trusted user, so `nix-collect-garbage` needs sudo"
	}
	return []string{line}
}
