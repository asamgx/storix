// Package podman detects Podman's macOS virtual machines.
//
// Podman on macOS is a Linux virtual machine like the others, and the bytes
// that matter are its machine images under
// ~/.local/share/containers/podman/machine. The tool was not installed on the
// machine storix was written against, so its parsers come from Podman's
// documented JSON output.
package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "podman"

// machineDir and configDir are where Podman keeps its images and its settings.
const (
	machineDir = ".local/share/containers"
	configDir  = ".config/containers"
)

func init() { detect.Register(40, New()) }

// Detector finds Podman.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Unverified reports that Podman was not installed on the machine storix was
// written against.
func (*Detector) Unverified() bool { return true }

// Machine is one Podman virtual machine.
type Machine struct {
	Name    string `json:"name"`
	Running bool   `json:"running,omitempty"`
	// Disk is the size Podman says the machine's disk was given.
	Disk int64 `json:"disk,omitempty"`
}

// Facts are what the probe learned.
type Facts struct {
	Machines []Machine `json:"machines,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// Probe lists Podman's machines.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	installed := env.Has("podman")
	dirs := env.Exists(path.Join(env.Home, machineDir)) || env.Exists(path.Join(env.Home, configDir))
	if !installed && !dirs {
		return nil, detect.Missingf("`podman` is not on the path")
	}
	if !installed {
		return &Facts{}, detect.Degradedf("the container directories exist but `podman` is not on the path")
	}

	res := env.Runner.Run(ctx, probe.Cmd{Name: "podman", Args: []string{"machine", "list", "--format", "json"}})
	if !res.OK() {
		return &Facts{}, detect.Degradedf("podman machine list: %s", res.Reason())
	}
	return &Facts{Machines: parse(res.Stdout)}, nil
}

// rawMachine is one entry of `podman machine list --format json`. Podman has
// spelled the disk size several ways across versions, so each is accepted.
type rawMachine struct {
	Name     string `json:"Name"`
	Running  bool   `json:"Running"`
	DiskSize any    `json:"DiskSize"`
	Disk     any    `json:"Disk"`
}

// parse reads the machine listing, which is a JSON array.
func parse(out string) []Machine {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return nil
	}
	var raws []rawMachine
	if err := json.Unmarshal([]byte(out), &raws); err != nil {
		return nil
	}
	machines := make([]Machine, 0, len(raws))
	for _, raw := range raws {
		if raw.Name == "" {
			continue
		}
		size := anyBytes(raw.DiskSize)
		if size == 0 {
			size = anyBytes(raw.Disk)
		}
		machines = append(machines, Machine{Name: raw.Name, Running: raw.Running, Disk: size})
	}
	return machines
}

// anyBytes reads a size that may be a number, a number of gibibytes, or one
// of the human strings Podman also prints.
func anyBytes(v any) int64 {
	switch x := v.(type) {
	case float64:
		// Podman reports DiskSize in gibibytes as a bare number.
		if x > 0 && x < 1024 {
			return int64(x) << 30
		}
		return int64(x)
	case string:
		n, _ := probe.ParseHumanBytes(x)
		return n
	default:
		return 0
	}
}

// Classify claims Podman's two directories.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}

	evidence := evidenceLines(facts)
	targets := []detect.Target{
		{
			Path: path.Join(home, machineDir), Category: "Podman", Kind: "image",
			Reclaim: classify.ToolManaged,
			Explain: "Podman's machine images and container storage",
		},
		{
			Path: path.Join(home, configDir), Category: "Podman", Kind: "data",
			Reclaim: classify.UserData, Explain: "Podman's configuration",
		},
	}
	for i := range targets {
		targets[i].Bucket = classify.BucketContainers
		targets[i].Owner = "Podman"
		targets[i].OwnerKeys = []string{"cli:podman"}
		targets[i].Evidence = evidence
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}

	rt := detect.Runtime{Name: "Podman"}
	for _, tool := range tools {
		if tool.Kind == "image" {
			rt.HostImage = append(rt.HostImage, tool)
		}
	}
	if facts != nil {
		for _, m := range facts.Machines {
			rt.Machines = append(rt.Machines, machineLabel(m))
		}
	}
	rt.Note = "sizes are the blocks the host has given the machines' sparse disks"
	if len(rt.Machines) == 0 {
		rt.Note = "no machines are defined; the directory is configuration and cached images"
	}
	return claims, detect.Summary{Tools: tools, Runtimes: []detect.Runtime{rt}}
}

// machineLabel names one machine the way the containers view prints it.
func machineLabel(m Machine) string {
	label := m.Name
	if m.Running {
		label += " (running)"
	} else {
		label += " (stopped)"
	}
	if m.Disk > 0 {
		label += ", " + units.Decimal.Bytes(m.Disk) + " disk"
	}
	return label
}

// evidenceLines are the why-panel lines every claim carries.
func evidenceLines(f *Facts) []string {
	if f == nil || len(f.Machines) == 0 {
		return []string{"`podman machine list` reported no machines"}
	}
	out := make([]string, 0, len(f.Machines))
	for _, m := range f.Machines {
		out = append(out, fmt.Sprintf("`podman machine list` reported %s", machineLabel(m)))
	}
	return out
}
