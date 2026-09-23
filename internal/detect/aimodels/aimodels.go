// Package aimodels detects downloaded machine-learning model weights:
// Ollama's, Hugging Face's hub cache, torch's, LM Studio's and Whisper's.
//
// Model weights are the one kind of developer data where the size of a single
// file is the whole story. A Hugging Face hub cache with four checkpoints in
// it is thirty gigabytes of files that can be downloaded again, and nothing
// in the directory name says so. That is why the reclaim tag here is
// tool-managed rather than regenerable: the bytes come back, but only over a
// network connection and only after a long wait, so "safe to delete" would be
// the wrong thing to tell someone on a metered link.
//
// The probe asks ollama for its model list. A stopped daemon is a degradation
// and not an absence: the weights are still on the disk whether or not
// anything is serving them, so the paths are claimed either way and only the
// per-model rows are lost.
package aimodels

import (
	"context"
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "aimodels"

func init() { detect.Register(250, New()) }

// ollamaKeys are the identifiers Ollama's bytes join on: the desktop
// application, the cask it usually arrives in, and the command.
var ollamaKeys = []string{"app:com.electron.ollama", "cask:ollama", "cli:ollama"}

// Detector finds downloaded model weights.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Model is one model `ollama list` reported.
type Model struct {
	Name string `json:"name"`
	// Size is what ollama printed, parsed from its human formatting.
	Size int64 `json:"size,omitempty"`
	// ID is ollama's short digest.
	ID string `json:"id,omitempty"`
}

// Facts are what the probe learned.
type Facts struct {
	// Models are what ollama is serving.
	Models []Model `json:"models,omitempty"`
	// Found are the home-relative directories that existed.
	Found []string `json:"found,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// location is one directory that holds weights.
type location struct {
	rel      string
	owner    string
	keys     []string
	category string
	reclaim  classify.Reclaim
	kind     string
	name     string
	explain  string
}

// locations are the directories docs/04 names.
var locations = []location{
	{
		rel: ".ollama", owner: "Ollama", keys: ollamaKeys, category: "Ollama",
		reclaim: classify.Unknown, kind: "data",
		explain: "Ollama's home: the models it pulled and its keys",
	},
	{
		rel: ".ollama/models", owner: "Ollama", keys: ollamaKeys, category: "Ollama",
		reclaim: classify.ToolManaged, kind: "data", name: "Ollama models",
		explain: "model weights ollama pulled; `ollama rm <model>` removes one and `ollama pull` fetches it again",
	},
	{
		rel: ".cache/huggingface", owner: "Hugging Face", keys: []string{"cli:huggingface-cli"},
		category: "Hugging Face", reclaim: classify.ToolManaged, kind: "cache",
		explain: "the Hugging Face cache: model weights, datasets and tokenizers",
	},
	{
		rel: ".cache/huggingface/hub", owner: "Hugging Face", keys: []string{"cli:huggingface-cli"},
		category: "Hugging Face", reclaim: classify.ToolManaged, kind: "cache", name: "Hugging Face hub",
		explain: "downloaded model checkpoints; `huggingface-cli delete-cache` removes them and the next run re-downloads",
	},
	{
		rel: ".cache/torch", owner: "PyTorch", keys: []string{"cli:python3"}, category: "PyTorch",
		reclaim: classify.ToolManaged, kind: "cache", name: "torch hub cache",
		explain: "pretrained weights torch.hub downloaded, fetched again on demand",
	},
	{
		rel: ".lmstudio", owner: "LM Studio", keys: []string{"app:ai.elementlabs.lmstudio", "cask:lm-studio"},
		category: "LM Studio", reclaim: classify.Unknown, kind: "data",
		explain: "LM Studio's home",
	},
	{
		rel: ".lmstudio/models", owner: "LM Studio", keys: []string{"app:ai.elementlabs.lmstudio", "cask:lm-studio"},
		category: "LM Studio", reclaim: classify.ToolManaged, kind: "data", name: "LM Studio models",
		explain: "model weights LM Studio downloaded; its own model manager removes them",
	},
	{
		rel: "Library/Application Support/LM Studio", owner: "LM Studio",
		keys:     []string{"app:ai.elementlabs.lmstudio", "cask:lm-studio"},
		category: "LM Studio", reclaim: classify.Unknown, kind: "data",
		explain: "LM Studio's application data",
	},
	{
		rel: ".cache/whisper", owner: "Whisper", keys: []string{"cli:whisper"}, category: "Whisper",
		reclaim: classify.ToolManaged, kind: "cache", name: "Whisper models",
		explain: "Whisper checkpoints, downloaded again the next time a model is loaded",
	},
}

// Probe asks ollama what it is serving.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	if env.Home == "" {
		return nil, detect.Missingf("no home directory to look in")
	}
	f := &Facts{}
	for _, loc := range locations {
		if env.Exists(path.Join(env.Home, loc.rel)) {
			f.Found = append(f.Found, loc.rel)
		}
	}

	hasOllama := env.Has("ollama")
	if !hasOllama && len(f.Found) == 0 {
		return nil, detect.Missingf("no `ollama` on the path and no model directory under %s", env.Home)
	}
	if !hasOllama {
		return f, nil
	}

	res := env.Runner.Run(ctx, probe.Cmd{Name: "ollama", Args: []string{"list"}})
	if !res.OK() {
		return f, detect.Degradedf("ollama list: %s; the weights on disk are still counted", res.Reason())
	}
	f.Models = parseList(res.Stdout)
	return f, nil
}

// parseList reads `ollama list`, which prints a header and then one model per
// line: NAME, ID, SIZE (two fields, "4.7 GB"), MODIFIED.
//
// It is parsed loosely on purpose. The columns have been reordered between
// releases and the size has been printed with and without a space; a line
// whose size cannot be read still contributes its name, because the name is
// what the report shows and the bytes come from the walk anyway.
func parseList(out string) []Model {
	var models []Model
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if i == 0 && strings.EqualFold(fields[0], "NAME") {
			continue
		}
		m := Model{Name: fields[0], ID: fields[1]}
		if len(fields) >= 4 {
			if n, ok := probe.ParseHumanBytes(fields[2] + fields[3]); ok {
				m.Size = n
			}
		}
		if m.Size == 0 && len(fields) >= 3 {
			if n, ok := probe.ParseHumanBytes(fields[2]); ok {
				m.Size = n
			}
		}
		models = append(models, m)
	}
	return models
}

// Classify claims the directories that hold weights.
//
// The models ollama reported become evidence rather than claims: ollama's
// store is content-addressed, so a model name does not correspond to a
// directory and claiming per model would mean inventing paths. The names and
// sizes go in the why panel, where they answer the question a reader of a
// twenty-gigabyte directory actually has.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}
	facts, _ := f.(*Facts)
	ollamaEvidence := evidenceLines(facts)

	targets := make([]detect.Target, 0, len(locations))
	for _, loc := range locations {
		name := loc.name
		if name == "" {
			name = loc.owner
		}
		tg := detect.Target{
			Path: path.Join(home, loc.rel), Bucket: classify.BucketDeveloper,
			Category: loc.category, Owner: loc.owner, OwnerKeys: loc.keys,
			Reclaim: loc.reclaim, Explain: loc.explain, Kind: loc.kind, Name: name,
		}
		if loc.owner == "Ollama" {
			tg.Evidence = ollamaEvidence
			tg.Note = modelNote(facts)
		}
		targets = append(targets, tg)
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// modelNote is the one-line summary of what ollama is serving.
func modelNote(f *Facts) string {
	if f == nil || len(f.Models) == 0 {
		return ""
	}
	var total int64
	for _, m := range f.Models {
		total += m.Size
	}
	if total == 0 {
		return plural(len(f.Models))
	}
	return plural(len(f.Models)) + ", " + units.Decimal.Bytes(total) + " by ollama's own count"
}

// plural renders a model count.
func plural(n int) string {
	if n == 1 {
		return "1 model"
	}
	return itoa(n) + " models"
}

// evidenceLines are the why-panel lines Ollama's claims carry.
func evidenceLines(f *Facts) []string {
	if f == nil {
		return []string{"ollama did not answer; the paths come from the static catalog"}
	}
	if len(f.Models) == 0 {
		return []string{"`ollama list` reported no models"}
	}
	out := make([]string, 0, len(f.Models)+1)
	out = append(out, "`ollama list` reported "+plural(len(f.Models)))
	for _, m := range f.Models {
		line := "  " + m.Name
		if m.Size > 0 {
			line += " " + units.Decimal.Bytes(m.Size)
		}
		out = append(out, line)
	}
	return out
}

// itoa formats a small count without reaching for strconv for one call site.
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
