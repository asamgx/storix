package detect

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// DefaultTimeout is how long a detector's whole probe may take. The commands
// inside it have their own, shorter deadlines; this one bounds a detector that
// runs several of them or blocks somewhere else.
const DefaultTimeout = 20 * time.Second

// DefaultGrace is how long scan waits for probes still running when the walk
// has finished. The walk takes twenty seconds on a full volume and every probe
// here takes under two, so the grace is a backstop rather than a budget.
const DefaultGrace = 2 * time.Second

// registered is the detector table the detector packages fill in from their
// own init functions, the way internal/cli's commands register themselves.
//
// Self-registration is what keeps the dependency pointing one way: a detector
// package imports this one for the Detector interface, and this one must not
// import the detector packages back. The cost is that a program wanting the
// full set blank-imports them, which internal/scan does in one place.
var registered struct {
	mu   sync.Mutex
	dets []registration
}

type registration struct {
	order int
	det   Detector
}

// Register adds a detector to the default registry. It panics on a duplicate
// name, which is a programming error caught the first time the binary runs.
// Order sets the position in the default registry; ties break by name.
func Register(order int, det Detector) {
	registered.mu.Lock()
	defer registered.mu.Unlock()
	for _, r := range registered.dets {
		if r.det.Name() == det.Name() {
			panic("detect: duplicate detector " + det.Name())
		}
	}
	registered.dets = append(registered.dets, registration{order: order, det: det})
}

// Registry is an ordered set of detectors, with the ones the user switched
// off remembered so they can be reported as Disabled rather than silently
// vanishing from the table.
type Registry struct {
	dets     []Detector
	disabled []string
}

// New builds a registry from an explicit list, which is what tests use.
func New(dets ...Detector) *Registry {
	return &Registry{dets: append([]Detector(nil), dets...)}
}

// Default is every registered detector, in registration order.
func Default() *Registry {
	registered.mu.Lock()
	defer registered.mu.Unlock()
	list := append([]registration(nil), registered.dets...)
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].order != list[j].order {
			return list[i].order < list[j].order
		}
		return list[i].det.Name() < list[j].det.Name()
	})
	r := &Registry{dets: make([]Detector, len(list))}
	for i, reg := range list {
		r.dets[i] = reg.det
	}
	return r
}

// Detectors lists the enabled detectors in order.
func (r *Registry) Detectors() []Detector {
	if r == nil {
		return nil
	}
	return append([]Detector(nil), r.dets...)
}

// Names lists the enabled detectors' names, which is what a flag's help text
// and `storix doctor` print.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.dets))
	for i, d := range r.dets {
		out[i] = d.Name()
	}
	return out
}

// Disable returns a copy of the registry without the named detectors. An
// unknown name is kept in the disabled list so the caller can report it; a
// typo that silently disabled nothing would be worse than one that shows up
// in the detectors table.
func (r *Registry) Disable(names ...string) *Registry {
	if r == nil {
		return nil
	}
	off := make(map[string]bool, len(names))
	for _, n := range names {
		if n != "" {
			off[n] = true
		}
	}
	out := &Registry{disabled: append([]string(nil), r.disabled...)}
	matched := make(map[string]bool, len(off))
	for _, d := range r.dets {
		if off[d.Name()] {
			matched[d.Name()] = true
			out.disabled = append(out.disabled, d.Name())
			continue
		}
		out.dets = append(out.dets, d)
	}
	// A name that matched nothing is still listed. `--disable-detector
	// dokcer` that silently did nothing would leave the user reading a
	// report they believe is degraded and is not.
	for _, n := range names {
		if n == "" || matched[n] || contains(out.disabled, n) {
			continue
		}
		out.disabled = append(out.disabled, n)
	}
	return out
}

// contains reports whether a short list holds a string.
func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Outcome is one detector's probe: what it learned and what happened.
type Outcome struct {
	Detector Detector
	Facts    Facts
	Status   Status
}

// Run is a set of probes in flight.
type Run struct {
	reg    *Registry
	cancel context.CancelFunc
	wg     sync.WaitGroup
	done   chan struct{}

	mu       sync.Mutex
	outs     []Outcome
	finished []bool
}

// Start launches every enabled detector's probe on its own goroutine and
// returns immediately, so the probes run while the walk does and cost nothing
// on the clock.
//
// The caller must call Stop, normally with defer, so that an early return
// from the scan leaves no goroutine and no child process behind.
func (r *Registry) Start(ctx context.Context, env Env, timeouts map[string]time.Duration) *Run {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	run := &Run{reg: r, cancel: cancel, done: make(chan struct{})}
	if r == nil {
		close(run.done)
		return run
	}

	run.outs = make([]Outcome, len(r.dets))
	run.finished = make([]bool, len(r.dets))
	for i, det := range r.dets {
		run.outs[i] = Outcome{Detector: det, Status: Status{Name: det.Name(), Verified: verified(det)}}
		run.wg.Add(1)
		go func(i int, det Detector) {
			defer run.wg.Done()
			out := probeOne(runCtx, det, env, timeoutFor(det.Name(), timeouts))
			run.mu.Lock()
			run.outs[i], run.finished[i] = out, true
			run.mu.Unlock()
		}(i, det)
	}
	go func() {
		run.wg.Wait()
		close(run.done)
	}()
	return run
}

// timeoutFor is a detector's probe budget.
func timeoutFor(name string, timeouts map[string]time.Duration) time.Duration {
	if d, ok := timeouts[name]; ok && d > 0 {
		return d
	}
	return DefaultTimeout
}

// verified reports whether a detector has been run against a machine that had
// the tool.
func verified(det Detector) bool {
	u, ok := det.(Unverified)
	return !ok || !u.Unverified()
}

// probeOne runs one detector's probe, converting a panic into a status.
func probeOne(ctx context.Context, det Detector, env Env, timeout time.Duration) (out Outcome) {
	rec := probe.NewRecorder(env.Runner)
	env.Runner = rec
	start := time.Now()

	out = Outcome{Detector: det, Status: Status{Name: det.Name(), Verified: verified(det)}}
	defer func() {
		if r := recover(); r != nil {
			out.Facts = nil
			out.Status.State = Panic
			out.Status.Reason = fmt.Sprintf("panic: %v", r)
		}
		out.Status.Duration = time.Since(start)
		out.Status.Commands = rec.Records()
	}()

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	facts, err := det.Probe(probeCtx, env)
	out.Facts = facts
	out.Status.State, out.Status.Reason = stateOf(err)
	if out.Status.State == Degraded && facts == nil && probeCtx.Err() != nil && ctx.Err() == nil {
		out.Status.State = Timeout
		out.Status.Reason = "the probe did not finish within " + timeout.String()
	}
	return out
}

// Wait collects the probes, giving any still running the grace period before
// calling them timed out. It cancels the stragglers on its way out, so that
// nothing is still talking to the machine while the engine classifies.
func (run *Run) Wait(grace time.Duration) []Outcome {
	if run == nil {
		return nil
	}
	if grace <= 0 {
		grace = DefaultGrace
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-run.done:
	case <-timer.C:
	}
	run.cancel()

	run.mu.Lock()
	defer run.mu.Unlock()
	out := make([]Outcome, len(run.outs))
	for i := range run.outs {
		out[i] = run.outs[i]
		if !run.finished[i] {
			out[i].Facts = nil
			out[i].Status.State = Timeout
			out[i].Status.Reason = "still running when the walk finished"
			out[i].Status.Duration = grace
		}
	}
	if run.reg != nil {
		for _, name := range run.reg.disabled {
			out = append(out, Outcome{Status: Status{
				Name: name, State: Disabled, Reason: "switched off with --disable-detector", Verified: true,
			}})
		}
	}
	return out
}

// stopGrace bounds how long Stop waits for a detector that ignores its
// context. Leaking one goroutine is better than hanging the command.
const stopGrace = 3 * time.Second

// Stop cancels the probes and waits for them. It is safe to call more than
// once and safe to call after Wait, which is what lets scan.Run defer it and
// leave nothing behind on any of its early returns.
func (run *Run) Stop() {
	if run == nil {
		return
	}
	run.cancel()
	timer := time.NewTimer(stopGrace)
	defer timer.Stop()
	select {
	case <-run.done:
	case <-timer.C:
	}
}

// Classify turns the finished probes into claims, summaries and statuses.
//
// A detector's Classify is called even when its probe failed, because a
// degraded detector still knows which paths its tool uses and those paths
// still have to land in the right bucket. The exception is a detector the
// user switched off: they asked for the static rules, and running its
// Classify anyway would ignore them.
func Classify(t *walk.Tree, outs []Outcome, cx classify.Context) ([]classify.Claim, map[string]Summary, []Status) {
	var claims []classify.Claim
	summaries := make(map[string]Summary, len(outs))
	statuses := make([]Status, 0, len(outs))

	for _, out := range outs {
		st := out.Status
		if out.Detector != nil && st.State != Disabled {
			cs, sum := classifyOne(t, out, cx, &st)
			claims = append(claims, cs...)
			if !sum.Empty() {
				summaries[st.Name] = sum
			}
		}
		statuses = append(statuses, st)
	}
	return claims, summaries, statuses
}

// classifyOne runs one detector's Classify, converting a panic into a status
// so that a bug in a detector costs its own claims and nothing else.
func classifyOne(t *walk.Tree, out Outcome, cx classify.Context, st *Status) (claims []classify.Claim, sum Summary) {
	defer func() {
		if r := recover(); r != nil {
			claims, sum = nil, Summary{}
			st.State = Panic
			st.Reason = fmt.Sprintf("panic while classifying: %v", r)
		}
	}()
	return out.Detector.Classify(t, out.Facts, cx)
}

// RetainLeaf composes the leaf-retention hooks of every detector that has
// one. It returns nil when none does, which leaves walk.Options.RetainLeaf
// unset and costs the walk nothing.
func RetainLeaf(r *Registry, cx classify.Context) func(dir string, e *walk.Entry) bool {
	if r == nil {
		return nil
	}
	var hooks []func(string, *walk.Entry) bool
	for _, det := range r.dets {
		lr, ok := det.(LeafRetainer)
		if !ok {
			continue
		}
		if h := lr.RetainLeaf(cx); h != nil {
			hooks = append(hooks, h)
		}
	}
	switch len(hooks) {
	case 0:
		return nil
	case 1:
		return hooks[0]
	}
	return func(dir string, e *walk.Entry) bool {
		for _, h := range hooks {
			if h(dir, e) {
				return true
			}
		}
		return false
	}
}

// RecordFixtures writes every detector's commands to dir as a fixture file,
// which is what STORIX_RECORD_PROBES asks for. It is a developer action, so a
// failure is returned rather than swallowed.
func RecordFixtures(dir string, statuses []Status) error {
	if dir == "" {
		return nil
	}
	for _, st := range statuses {
		if len(st.Commands) == 0 {
			continue
		}
		if err := probe.WriteFixture(dir+string(os.PathSeparator)+st.Name+".json", st.Commands); err != nil {
			return err
		}
	}
	return nil
}
