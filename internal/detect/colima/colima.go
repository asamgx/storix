// Package colima detects Colima and the Lima virtual machines underneath it.
//
// The two are one detector because they are one stack: Colima is a Docker
// front end over Lima, a machine created by Colima shows up in `limactl list`
// as well, and their disk images sit in sibling directories. Reporting them
// separately would show the same virtual machine twice.
package colima

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
const Name = "colima"

func init() { detect.Register(30, New()) }

// Detector finds Colima and Lima.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Unverified reports that neither tool was installed on the machine storix
// was written against, so the parsers are written from documented output.
func (*Detector) Unverified() bool { return true }

// Instance is one virtual machine.
type Instance struct {
	Name string `json:"name"`
	// Runtime is what Colima calls the instance's container runtime
	// ("docker", "containerd"); empty for a plain Lima machine.
	Runtime string `json:"runtime,omitempty"`
	Status  string `json:"status,omitempty"`
	// Disk is the size the tool says the instance's disk was given, which
	// is its apparent size rather than its allocated blocks.
	Disk int64 `json:"disk,omitempty"`
	// Tool is "colima" or "limactl", so the report can say which listing
	// an instance came from.
	Tool string `json:"tool"`
}

// Facts are what the probe learned.
type Facts struct {
	Instances []Instance `json:"instances,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// Probe lists whatever of the two tools is installed.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	hasColima, hasLima := env.Has("colima"), env.Has("limactl")
	dirs := env.Exists(path.Join(env.Home, ".colima")) || env.Exists(path.Join(env.Home, ".lima"))
	if !hasColima && !hasLima && !dirs {
		return nil, detect.Missingf("neither `colima` nor `limactl` is on the path")
	}

	f := &Facts{}
	var degraded []string
	if hasColima {
		list, err := run(ctx, env.Runner, "colima", []string{"list", "--json"})
		if err != nil {
			degraded = append(degraded, err.Error())
		}
		f.Instances = append(f.Instances, list...)
	}
	if hasLima {
		list, err := run(ctx, env.Runner, "limactl", []string{"list", "--json"})
		if err != nil {
			degraded = append(degraded, err.Error())
		}
		f.Instances = append(f.Instances, merge(f.Instances, list)...)
	}
	if len(degraded) > 0 {
		return f, detect.Degradedf("%s", strings.Join(degraded, "; "))
	}
	if !hasColima && !hasLima {
		return f, detect.Degradedf("the directories exist but neither tool is on the path")
	}
	return f, nil
}

// rawInstance covers both listings. Colima and limactl print one JSON object
// per line with overlapping field names, and both spell the size differently
// across versions, so every shape is accepted and the missing ones stay zero.
type rawInstance struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Runtime  string `json:"runtime"`
	Disk     any    `json:"disk"`
	DiskSize any    `json:"diskSize"`
}

// run lists one tool's instances.
func run(ctx context.Context, r probe.Runner, tool string, args []string) ([]Instance, error) {
	res := r.Run(ctx, probe.Cmd{Name: tool, Args: args})
	if !res.OK() {
		return nil, fmt.Errorf("%s %s: %s", tool, strings.Join(args, " "), res.Reason())
	}
	return parse(res.Stdout, tool), nil
}

// parse reads the one-object-per-line listing, or the array some versions
// print instead.
func parse(out, tool string) []Instance {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	var raws []rawInstance
	if strings.HasPrefix(out, "[") {
		if json.Unmarshal([]byte(out), &raws) != nil {
			return nil
		}
	} else {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "{") {
				continue
			}
			var raw rawInstance
			if json.Unmarshal([]byte(line), &raw) == nil {
				raws = append(raws, raw)
			}
		}
	}
	out2 := make([]Instance, 0, len(raws))
	for _, raw := range raws {
		if raw.Name == "" {
			continue
		}
		size := anyBytes(raw.Disk)
		if size == 0 {
			size = anyBytes(raw.DiskSize)
		}
		out2 = append(out2, Instance{
			Name: raw.Name, Runtime: raw.Runtime, Status: raw.Status, Disk: size, Tool: tool,
		})
	}
	return out2
}

// anyBytes reads a size that may be a number or one of the human strings
// these tools also print.
func anyBytes(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := probe.ParseHumanBytes(x)
		return n
	default:
		return 0
	}
}

// limaName is the Lima instance a Colima profile runs on. Colima calls its
// default profile's machine "colima" and every other profile's
// "colima-<profile>", so the two listings name the same virtual machine
// differently and a merge that compared names directly would report it twice.
func limaName(profile string) string {
	if profile == "" || profile == "default" {
		return "colima"
	}
	return "colima-" + profile
}

// merge drops the limactl entries already listed by colima, which manages its
// machines through Lima and so appears in both listings.
func merge(have, add []Instance) []Instance {
	var out []Instance
	for _, inst := range add {
		dup := false
		for _, h := range have {
			if h.Tool == "colima" && limaName(h.Name) == inst.Name {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, inst)
		}
	}
	return out
}

// dirs are the directories the two tools keep their machines in.
var dirs = []struct {
	rel, owner, key, category, explain string
	reclaim                            classify.Reclaim
}{
	{".colima", "Colima", "cli:colima", "Colima", "Colima's virtual machine disks and configuration", classify.ToolManaged},
	{".lima", "Lima", "cli:limactl", "Lima", "Lima's virtual machine disks", classify.ToolManaged},
}

// Classify claims the two directories and reports one runtime per tool that
// has one.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}

	evidence := evidenceLines(facts)
	targets := make([]detect.Target, 0, len(dirs))
	for _, d := range dirs {
		targets = append(targets, detect.Target{
			Path: path.Join(home, d.rel), Bucket: classify.BucketContainers,
			Category: d.category, Owner: d.owner, OwnerKeys: []string{d.key},
			Reclaim: d.reclaim, Explain: d.explain, Kind: "image", Evidence: evidence,
		})
	}
	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}

	sum := detect.Summary{Tools: tools}
	for _, tool := range tools {
		rt := detect.Runtime{Name: tool.Name, HostImage: []detect.Tool{tool}}
		for _, inst := range instancesOf(facts, tool.Name) {
			rt.Machines = append(rt.Machines, machineLabel(inst))
		}
		rt.Note = runtimeNote(rt)
		sum.Runtimes = append(sum.Runtimes, rt)
	}
	return claims, sum
}

// instancesOf is the instances belonging to one of the two tools.
func instancesOf(f *Facts, owner string) []Instance {
	if f == nil {
		return nil
	}
	want := "colima"
	if owner == "Lima" {
		want = "limactl"
	}
	var out []Instance
	for _, inst := range f.Instances {
		if inst.Tool == want {
			out = append(out, inst)
		}
	}
	return out
}

// machineLabel names one instance the way the containers view prints it.
func machineLabel(inst Instance) string {
	label := inst.Name
	if inst.Status != "" {
		label += " (" + strings.ToLower(inst.Status) + ")"
	}
	if inst.Disk > 0 {
		label += ", " + units.Decimal.Bytes(inst.Disk) + " disk"
	}
	return label
}

// runtimeNote says what the host number means when no daemon was asked.
func runtimeNote(rt detect.Runtime) string {
	if len(rt.Machines) == 0 {
		return "no machines are defined; the directory is configuration and cached images"
	}
	return "sizes are the blocks the host has given the machines' sparse disks, not the sizes the guests were configured with"
}

// evidenceLines are the why-panel lines every claim carries.
func evidenceLines(f *Facts) []string {
	if f == nil || len(f.Instances) == 0 {
		return []string{"no colima or lima machines were listed"}
	}
	out := make([]string, 0, len(f.Instances))
	for _, inst := range f.Instances {
		out = append(out, "`"+inst.Tool+" list` reported "+machineLabel(inst))
	}
	return out
}
