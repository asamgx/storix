package scan

import (
	"os"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/classify/catalog"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"

	// The detector packages register themselves, the way internal/cli's
	// commands do. They are imported for that effect alone: internal/detect
	// defines the interface they implement and must not import them back,
	// so the list of what a scan knows about lives here, in the one place
	// that assembles a scan.
	_ "github.com/asamgx/storix/internal/detect/colima"
	_ "github.com/asamgx/storix/internal/detect/docker"
	_ "github.com/asamgx/storix/internal/detect/kubernetes"
	_ "github.com/asamgx/storix/internal/detect/orbstack"
	_ "github.com/asamgx/storix/internal/detect/podman"
	_ "github.com/asamgx/storix/internal/detect/vms"
)

// registry is the detector set a scan runs, with the disabled ones remembered
// so they appear in the detectors table as Disabled rather than vanishing.
func registry(cfg Config) *detect.Registry {
	reg := cfg.Detectors
	if reg == nil {
		reg = detect.Default()
	}
	if len(cfg.DisabledDetectors) > 0 {
		reg = reg.Disable(cfg.DisabledDetectors...)
	}
	return reg
}

// detectEnv is what the detectors are allowed to touch.
//
// The home is the invoking user's, not root's: under sudo a probe that asked
// Homebrew for its cache as root would be told about root's cache, and
// Homebrew refuses to run as root at all (D39).
func detectEnv(cfg Config) detect.Env {
	runner := cfg.Probe
	if runner == nil {
		runner = &probe.Exec{}
	}
	return detect.DefaultEnv(runner, mac.ScanPath(homeOf(cfg)))
}

// detectContext is the classification context the detectors are given before
// the walk has happened, for their leaf-retention hooks. It names the home and
// the configured code roots without filtering them against a tree, because
// there is no tree yet.
func detectContext(cfg Config) classify.Context {
	home := homeOf(cfg)
	roots := cfg.CodeRoots
	if roots == nil {
		roots = classify.DefaultCodeRoots
	}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if display := expandHome(r, home); display != "" {
			out = append(out, display)
		}
	}
	return classify.Context{Home: home, CodeRoots: out}
}

// homeOf is the home the classifier and the detectors work against, as a
// display path: the configured one, else the invoking user's.
func homeOf(cfg Config) string {
	if cfg.Home != "" {
		return mac.DisplayPath(cfg.Home)
	}
	return HomeDir()
}

// classifyWith runs the catalog over a finished tree together with the claims
// the detectors made.
//
// It is Classify with the detector claims folded in; the two are one function
// with one behaviour, and the exported one stays for callers that have no
// detectors to offer.
func classifyWith(t *walk.Tree, cfg Config, claims []classify.Claim) *classify.Classification {
	if t == nil || t.Root == nil {
		return nil
	}
	e, err := classify.New(catalog.Rules(), classifyContext(t, cfg))
	if err != nil {
		return nil
	}
	return e.Run(t, claims)
}

// slowestProbe is the wall time of the probe that took longest, which is the
// number that answers "did the detectors cost the scan anything": they run
// beside the walk, so what matters is whether the slowest one finished before
// it did.
func slowestProbe(statuses []detect.Status) time.Duration {
	var d time.Duration
	for _, st := range statuses {
		if st.Duration > d {
			d = st.Duration
		}
	}
	return d
}

// recordProbes writes the detectors' commands out as fixtures when
// STORIX_RECORD_PROBES names a directory. It is how the detector fixtures are
// made; a failure is reported and never sinks the scan.
func recordProbes(statuses []detect.Status) error {
	dir := os.Getenv(probe.RecordEnv)
	if dir == "" {
		return nil
	}
	return detect.RecordFixtures(dir, statuses)
}
