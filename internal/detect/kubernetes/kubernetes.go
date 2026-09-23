// Package kubernetes detects the local Kubernetes clusters a developer
// machine accumulates: kubectl's discovery cache, minikube, kind and Rancher
// Desktop.
//
// There is nothing to probe. A cluster that is not running still occupies the
// disk, and asking a cluster that is running would mean talking to whatever
// the current kubeconfig points at, which on a work machine is a production
// cluster. So the detector reads directories and nothing else.
//
// The distinction it draws is between kubectl's cache, which is pure
// regenerable metadata, and the cluster tools' own directories, which hold
// virtual machine disks and container images their own commands delete.
package kubernetes

import (
	"context"
	"path"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "kubernetes"

func init() { detect.Register(60, New()) }

// Detector finds local Kubernetes clusters.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are the directories that existed when the probe ran, recorded so a
// cached scan can tell "no clusters" from "never looked".
type Facts struct {
	Found []string `json:"found,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// location is one tool's directory.
type location struct {
	rel     string
	owner   string
	key     string
	reclaim classify.Reclaim
	kind    string
	explain string
}

// locations are the four directories docs/04 names. The kubectl cache is
// listed after ~/.kube so the more specific claim wins on the subdirectory
// and the configuration above it stays user data.
var locations = []location{
	{
		rel: ".kube", owner: "kubectl", key: "cli:kubectl",
		reclaim: classify.UserData, kind: "data",
		explain: "your kubeconfig: credentials and cluster addresses, not something to delete",
	},
	{
		rel: ".kube/cache", owner: "kubectl", key: "cli:kubectl",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "kubectl's API discovery cache, rebuilt on the next command",
	},
	{
		rel: ".minikube", owner: "minikube", key: "cli:minikube",
		reclaim: classify.ToolManaged, kind: "image",
		explain: "minikube's cluster disks and cached images; `minikube delete --all` reclaims them",
	},
	{
		rel: ".kind", owner: "kind", key: "cli:kind",
		reclaim: classify.ToolManaged, kind: "image",
		explain: "kind's cluster state; `kind delete clusters --all` reclaims it",
	},
	{
		rel: ".rd", owner: "Rancher Desktop", key: "app:io.rancherdesktop.app",
		reclaim: classify.ToolManaged, kind: "image",
		explain: "Rancher Desktop's virtual machine and container images",
	},
}

// Probe looks for the directories.
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
		return nil, detect.Missingf("no kubectl, minikube, kind or Rancher Desktop directory exists")
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
			Category: "Kubernetes", Owner: loc.owner, OwnerKeys: []string{loc.key},
			Reclaim: loc.reclaim, Explain: loc.explain, Kind: loc.kind,
			Name:     loc.owner,
			Evidence: []string{loc.owner + " keeps its data in " + path.Join(home, loc.rel)},
		})
	}
	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}
