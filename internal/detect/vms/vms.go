// Package vms detects desktop virtual machine managers: UTM, Parallels,
// VMware Fusion and VirtualBox.
//
// None of them has a query interface worth shelling out to, so there is no
// probe: the evidence is the directories themselves. What makes this a
// detector rather than four catalog rules is the reclaim tag. A virtual
// machine's disk is the user's data — it is an operating system they
// installed and files they put in it — while the caches and installer images
// the managers keep beside it are not, and telling the two apart needs the
// per-manager knowledge that lives here.
package vms

import (
	"path"

	"context"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "vms"

func init() { detect.Register(50, New()) }

// Detector finds desktop virtual machines.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are the directories that were there when the probe ran. They are
// recorded so that a cached scan can tell "no virtual machines" from "the
// detector never ran", which is the difference between a fact and a gap.
type Facts struct {
	Found []string `json:"found,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// location is one manager's directory.
type location struct {
	// rel is the path below the home directory.
	rel string
	// owner is the display label, key the footprint join key.
	owner   string
	key     string
	reclaim classify.Reclaim
	kind    string
	explain string
}

// locations are the four managers' directories, plus the two Parallels keeps
// its own caches in. The order is the order the report lists them in.
var locations = []location{
	{
		rel: "Library/Containers/com.utmapp.UTM", owner: "UTM", key: "app:com.utmapp.UTM",
		reclaim: classify.UserData, kind: "data",
		explain: "UTM's virtual machines; their disks are operating systems you installed, so they are your data",
	},
	{
		rel: "Parallels", owner: "Parallels", key: "app:com.parallels.desktop.console",
		reclaim: classify.UserData, kind: "data",
		explain: "Parallels virtual machines; each .pvm bundle is a guest's whole disk",
	},
	{
		rel: "Library/Parallels", owner: "Parallels", key: "app:com.parallels.desktop.console",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "Parallels' caches and downloaded installer images, which it re-downloads on demand",
	},
	{
		rel: "Virtual Machines.localized", owner: "VMware Fusion", key: "app:com.vmware.fusion",
		reclaim: classify.UserData, kind: "data",
		explain: "VMware Fusion virtual machines",
	},
	{
		rel: "VirtualBox VMs", owner: "VirtualBox", key: "app:org.virtualbox.app.VirtualBox",
		reclaim: classify.UserData, kind: "data",
		explain: "VirtualBox virtual machines",
	},
	{
		rel: ".vagrant.d", owner: "Vagrant", key: "cli:vagrant",
		reclaim: classify.ToolManaged, kind: "image",
		explain: "Vagrant boxes; `vagrant box prune` removes the superseded ones",
	},
}

// Probe looks for the managers' directories. There is nothing to run, so the
// whole probe is a handful of lstat calls and it never costs the walk
// anything.
func (*Detector) Probe(_ context.Context, env detect.Env) (detect.Facts, error) {
	if env.Home == "" {
		return nil, detect.Missingf("no home directory to look in")
	}
	f := &Facts{}
	for _, loc := range locations {
		if env.Exists(path.Join(env.Home, loc.rel)) {
			f.Found = append(f.Found, loc.rel)
		}
	}
	if len(f.Found) == 0 {
		return nil, detect.Missingf("no UTM, Parallels, VMware or VirtualBox directory exists")
	}
	return f, nil
}

// Classify claims whichever of the directories the walk found.
func (*Detector) Classify(t *walk.Tree, _ detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}
	targets := make([]detect.Target, 0, len(locations))
	for _, loc := range locations {
		targets = append(targets, detect.Target{
			Path: path.Join(home, loc.rel), Bucket: classify.BucketContainers,
			Category: "Virtual machines", Owner: loc.owner, OwnerKeys: []string{loc.key},
			Reclaim: loc.reclaim, Explain: loc.explain, Kind: loc.kind,
			Evidence: []string{loc.owner + " keeps its data in " + path.Join(home, loc.rel)},
		})
	}
	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools, Runtimes: runtimes(tools)}
}

// runtimes groups the tool rows into one runtime per manager, so the
// containers view shows "Parallels" once with both its directories rather
// than twice.
func runtimes(tools []detect.Tool) []detect.Runtime {
	var out []detect.Runtime
	for _, tool := range tools {
		found := false
		for i := range out {
			if out[i].Name == tool.Name {
				out[i].HostImage = append(out[i].HostImage, tool)
				found = true
				break
			}
		}
		if !found {
			out = append(out, detect.Runtime{
				Name:      tool.Name,
				HostImage: []detect.Tool{tool},
				Note:      "there is no daemon to ask, so the guest's own view of its disk is not available",
			})
		}
	}
	return out
}
