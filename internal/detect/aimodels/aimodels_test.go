package aimodels_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/aimodels"
	"github.com/asamgx/storix/internal/detect/detecttest"
)

const home = detecttest.Home

// corpus is a machine that has pulled weights from all five sources.
func corpus() map[string]int64 {
	return map[string]int64{
		home + "/.ollama/models/blobs/sha256-aaa":                       900_000,
		home + "/.cache/huggingface/hub/models--meta--llama/x":          800_000,
		home + "/.cache/torch/hub/checkpoints/x":                        300_000,
		home + "/.lmstudio/models/lmstudio-community/x":                 400_000,
		home + "/Library/Application Support/LM Studio/conversations/x": 50_000,
		home + "/.cache/whisper/large-v3.pt":                            200_000,
	}
}

// TestThisMachineDegradesWithoutTheDaemon replays what this machine answered:
// ollama is installed, its server is not running, and the answer must still
// be the directories rather than nothing.
func TestThisMachineDegradesWithoutTheDaemon(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, err := detecttest.Probe(t, aimodels.New(), f.Env(t, "testdata/this-machine.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if !strings.Contains(err.Error(), "still counted") {
		t.Errorf("the reason does not say the weights are still counted: %v", err)
	}
	if got := facts.(*aimodels.Facts); len(got.Models) != 0 {
		t.Errorf("models = %+v, want none from a daemon that did not answer", got.Models)
	}

	claims, sum := aimodels.New().Classify(f.Tree, facts, f.Context)
	if _, ok := detecttest.ClaimAt(claims, home+"/.ollama/models"); !ok {
		t.Error("the weights stopped being claimed because the daemon was down")
	}
	if sum.Empty() {
		t.Error("no summary rows although the directories are there")
	}
}

// TestModels covers the one thing the probe adds: the names and sizes ollama
// reports, which a content-addressed store cannot show from its directories.
func TestModels(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, err := detecttest.Probe(t, aimodels.New(), f.Env(t, "testdata/models.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	got := facts.(*aimodels.Facts)
	if len(got.Models) != 2 {
		t.Fatalf("models = %+v, want two", got.Models)
	}
	if got.Models[0].Name != "llama3.2:latest" || got.Models[0].Size != 2_000_000_000 {
		t.Errorf("model 0 = %+v", got.Models[0])
	}
	if got.Models[1].Name != "qwen2.5-coder:14b" || got.Models[1].Size != 9_000_000_000 {
		t.Errorf("model 1 = %+v", got.Models[1])
	}

	claims, _ := aimodels.New().Classify(f.Tree, facts, f.Context)
	c, ok := detecttest.ClaimAt(claims, home+"/.ollama/models")
	if !ok {
		t.Fatal("the ollama store was not claimed")
	}
	joined := strings.Join(c.Evidence, "\n")
	if !strings.Contains(joined, "llama3.2:latest") || !strings.Contains(joined, "qwen2.5-coder:14b") {
		t.Errorf("the model names are not in the evidence:\n%s", joined)
	}
}

// TestMalformedOutput: a listing that is not a table costs the model rows and
// nothing else.
func TestMalformedOutput(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, err := detecttest.Probe(t, aimodels.New(), f.Env(t, "testdata/malformed.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, _ := aimodels.New().Classify(f.Tree, facts, f.Context)
	if len(claims) == 0 {
		t.Error("the directories stopped being claimed because the listing was odd")
	}
}

// TestTimeout: a daemon that did not answer in time is a degradation.
func TestTimeout(t *testing.T) {
	f := detecttest.Build(t, corpus())
	_, err := detecttest.Probe(t, aimodels.New(), f.Env(t, "testdata/timeout.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
}

// TestNothingInstalled: no ollama and no weights anywhere is Missing.
func TestNothingInstalled(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, aimodels.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	claims, sum := aimodels.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims with no model weights anywhere", len(claims))
	}
}

// TestWeightsAreToolManagedNotRegenerable is the judgement this detector
// carries. The bytes come back, but only over a network and only after a long
// wait, so calling them safe to delete would be the wrong advice.
func TestWeightsAreToolManaged(t *testing.T) {
	f := detecttest.Build(t, corpus())
	claims, _ := aimodels.New().Classify(f.Tree, &aimodels.Facts{}, f.Context)

	want := []struct {
		path  string
		owner string
		key   string
	}{
		{home + "/.ollama/models", "Ollama", "app:com.electron.ollama"},
		{home + "/.cache/huggingface/hub", "Hugging Face", "cli:huggingface-cli"},
		{home + "/.cache/torch", "PyTorch", "cli:python3"},
		{home + "/.lmstudio/models", "LM Studio", "app:ai.elementlabs.lmstudio"},
		{home + "/.cache/whisper", "Whisper", "cli:whisper"},
	}
	for _, w := range want {
		c, ok := detecttest.ClaimAt(claims, w.path)
		if !ok {
			t.Errorf("no claim at %s", w.path)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", w.path, c.Bucket)
		}
		if c.Reclaim != classify.ToolManaged {
			t.Errorf("%s reclaim = %s, want tool-managed", w.path, c.Reclaim)
		}
		if c.Owner != w.owner {
			t.Errorf("%s owner = %q, want %q", w.path, c.Owner, w.owner)
		}
		if !detecttest.HasKey(c, w.key) {
			t.Errorf("%s keys = %v, want %q", w.path, c.OwnerKeys, w.key)
		}
	}
}
