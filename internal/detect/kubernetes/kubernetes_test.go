package kubernetes_test

import (
	"errors"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/kubernetes"
)

const home = detecttest.Home

// TestThisMachine mirrors what is actually here: a ~/.kube with a config and
// a discovery cache, and none of the cluster tools.
func TestThisMachine(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{
		home + "/.kube/config":                       5_800,
		home + "/.kube/cache/discovery/api/v1.json":  40_000,
		home + "/.kube/gke_gcloud_auth_plugin_cache": 447,
	})
	facts, err := detecttest.Probe(t, kubernetes.New(), f.Env(t, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*kubernetes.Facts)
	if len(got.Found) != 2 {
		t.Errorf("found = %v, want ~/.kube and ~/.kube/cache", got.Found)
	}

	claims, sum := kubernetes.New().Classify(f.Tree, facts, f.Context)

	// The split this detector exists for: the kubeconfig holds credentials
	// and is user data, the discovery cache beside it is rebuilt on the next
	// command. The deeper claim has to win on the subdirectory.
	config, ok := detecttest.ClaimAt(claims, home+"/.kube")
	if !ok {
		t.Fatal("~/.kube was not claimed")
	}
	if config.Reclaim != classify.UserData {
		t.Errorf("~/.kube reclaim = %s, want user-data: it holds credentials", config.Reclaim)
	}
	cache, ok := detecttest.ClaimAt(claims, home+"/.kube/cache")
	if !ok {
		t.Fatal("~/.kube/cache was not claimed")
	}
	if cache.Reclaim != classify.Regenerable {
		t.Errorf("~/.kube/cache reclaim = %s, want regenerable", cache.Reclaim)
	}
	if cache.Depth <= config.Depth {
		t.Errorf("the cache claim is not more specific than its parent's (%d vs %d), so it would lose the node",
			cache.Depth, config.Depth)
	}
	for _, c := range []classify.Claim{config, cache} {
		if c.Bucket != classify.BucketContainers {
			t.Errorf("%s is in %s, want containers", c.Node.Display(), c.Bucket)
		}
		if !detecttest.HasKey(c, "cli:kubectl") {
			t.Errorf("%s is missing cli:kubectl", c.Node.Display())
		}
	}

	// Nothing that is not installed may be claimed.
	for _, absent := range []string{home + "/.minikube", home + "/.kind", home + "/.rd"} {
		if _, ok := detecttest.ClaimAt(claims, absent); ok {
			t.Errorf("%s was claimed although it does not exist", absent)
		}
	}
	if len(sum.Runtimes) != 0 {
		t.Error("a runtime was reported for a detector with no daemon to ask")
	}
	if len(sum.Tools) != 2 {
		t.Errorf("tools = %d, want one per claimed directory", len(sum.Tools))
	}
}

func TestClusterTools(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{
		home + "/.minikube/cache/iso/minikube.iso": 300_000,
		home + "/.kind/clusters/dev/state":         40_000,
		home + "/.rd/lima/0/diffdisk":              -5_000_000,
	})
	facts, err := detecttest.Probe(t, kubernetes.New(), f.Env(t, "testdata/missing.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, _ := kubernetes.New().Classify(f.Tree, facts, f.Context)
	want := map[string]string{
		home + "/.minikube": "minikube",
		home + "/.kind":     "kind",
		home + "/.rd":       "Rancher Desktop",
	}
	for p, owner := range want {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Owner != owner {
			t.Errorf("%s owner = %q, want %q", p, c.Owner, owner)
		}
		if c.Reclaim != classify.ToolManaged {
			t.Errorf("%s reclaim = %s, want tool-managed", p, c.Reclaim)
		}
	}
	if _, ok := detecttest.ClaimAt(claims, home+"/.rd"); ok {
		if c, _ := detecttest.ClaimAt(claims, home+"/.rd"); !detecttest.HasKey(c, "app:io.rancherdesktop.app") {
			t.Errorf("Rancher Desktop is missing its bundle id: %v", c.OwnerKeys)
		}
	}
}

func TestMissing(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, kubernetes.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	claims, sum := kubernetes.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims on a machine with no clusters", len(claims))
	}
}

func TestIsVerified(t *testing.T) {
	// It reads directories and runs nothing, and ~/.kube is on the machine
	// storix was written against, so there is nothing unverified about it.
	if _, ok := any(kubernetes.New()).(detect.Unverified); ok {
		t.Error("the kubernetes detector claims to be unverified")
	}
}
