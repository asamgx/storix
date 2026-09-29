package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RecordEnv is the environment variable that turns a scan into a recording
// session: set it to a directory and every detector's commands are written to
// <dir>/<detector>.json, which is exactly the format LoadFixture reads.
//
// It is how the fixtures in internal/detect/*/testdata were made, and how
// they are remade when a tool changes its output.
const RecordEnv = "STORIX_RECORD_PROBES"

// Recorder wraps a Runner and remembers every command it ran.
//
// Every detector gets one, because the record is two things at once: the
// evidence the why panel shows for a claim ("this is what `docker system df`
// said"), and the fixture a test replays so the detector can be tested on a
// machine that does not have the tool.
type Recorder struct {
	// Inner is the runner doing the work. A nil Inner records nothing and
	// reports every command missing, which is what a disabled probe means.
	Inner Runner

	mu      sync.Mutex
	records []Record
}

// NewRecorder wraps a runner.
func NewRecorder(inner Runner) *Recorder { return &Recorder{Inner: inner} }

// Run runs the command and records it.
func (r *Recorder) Run(ctx context.Context, c Cmd) Result {
	if r.Inner == nil {
		res := Result{Missing: true, ErrText: "no probe runner configured"}
		res.Err = errors.New(res.ErrText)
		r.add(c, res)
		return res
	}
	res := r.Inner.Run(ctx, c)
	r.add(c, res)
	return res
}

// add appends one record.
func (r *Recorder) add(c Cmd, res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, Record{Cmd: c, Result: res})
}

// Records returns a copy of what was run, in order.
func (r *Recorder) Records() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Record(nil), r.records...)
}

// WriteFixture stores records as a fixture file, creating the directory.
// Recording is a developer action, so a failure is returned rather than
// swallowed: a scan that was asked to record and did not must say so.
func WriteFixture(path string, records []Record) error {
	if records == nil {
		records = []Record{}
	}
	for i := range records {
		if records[i].Result.Err != nil && records[i].Result.ErrText == "" {
			records[i].Result.ErrText = records[i].Result.Err.Error()
		}
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644) //nolint:gosec // a fixture is source, not a secret
}

// Replay answers from a recording instead of running anything.
//
// A command that is not in the recording is Missing rather than an error: a
// fixture names the tools a machine had, so anything it does not name is a
// tool that machine did not have, and a test that adds a probe without adding
// a fixture line sees the degradation path rather than a panic.
type Replay struct {
	// Records maps Cmd.Key to the result to return.
	Records map[string]Result
}

// NewReplay builds a Replay from a recording.
func NewReplay(records []Record) *Replay {
	m := make(map[string]Result, len(records))
	for _, rec := range records {
		res := rec.Result
		if res.Err == nil && res.ErrText != "" {
			res.Err = errors.New(res.ErrText)
		}
		m[rec.Cmd.Key()] = res
	}
	return &Replay{Records: m}
}

// LoadFixture reads a recording written by WriteFixture.
func LoadFixture(path string) (*Replay, error) {
	data, err := os.ReadFile(path) //nolint:gosec // fixtures are test data named by the test
	if err != nil {
		return nil, err
	}
	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("probe: %s: %w", path, err)
	}
	return NewReplay(records), nil
}

// Run answers from the recording.
func (r *Replay) Run(_ context.Context, c Cmd) Result {
	if res, ok := r.Records[c.Key()]; ok {
		return res
	}
	err := fmt.Errorf("probe: no fixture for %q", c.Key())
	return Result{Missing: true, Err: err, ErrText: err.Error()}
}

// LookPath reports whether the recording has any command for this executable,
// which is what "the machine had this tool" means to a replayed detector.
func (r *Replay) LookPath(name string) (string, error) {
	for key := range r.Records {
		if key == name || (len(key) > len(name) && key[:len(name)+1] == name+" ") {
			return "/replay/" + name, nil
		}
	}
	return "", fmt.Errorf("probe: %s is not in the fixture", name)
}
