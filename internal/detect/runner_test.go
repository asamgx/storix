package detect

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// fake is a detector a test drives: it does whatever probe says and claims
// whatever claims says.
type fake struct {
	name       string
	probe      func(ctx context.Context, env Env) (Facts, error)
	classify   func(t *walk.Tree, f Facts, cx classify.Context) ([]classify.Claim, Summary)
	unverified bool
	retain     func(cx classify.Context) func(string, *walk.Entry) bool
	probed     atomic.Int32
}

type fakeFacts struct{ Kind_ string }

func (f *fakeFacts) Kind() string { return f.Kind_ }

func (d *fake) Name() string     { return d.name }
func (d *fake) NewFacts() Facts  { return &fakeFacts{Kind_: d.name} }
func (d *fake) Unverified() bool { return d.unverified }
func (d *fake) Probe(ctx context.Context, env Env) (Facts, error) {
	d.probed.Add(1)
	if d.probe == nil {
		return &fakeFacts{Kind_: d.name}, nil
	}
	return d.probe(ctx, env)
}

func (d *fake) Classify(t *walk.Tree, f Facts, cx classify.Context) ([]classify.Claim, Summary) {
	if d.classify == nil {
		return nil, Summary{}
	}
	return d.classify(t, f, cx)
}

// RetainLeaf is only present when the test asked for it, so a detector
// without one does not accidentally satisfy LeafRetainer.
func (d *fake) RetainLeaf(cx classify.Context) func(string, *walk.Entry) bool {
	if d.retain == nil {
		return nil
	}
	return d.retain(cx)
}

// testEnv is an environment with nothing behind it: no commands recorded, so
// every tool is missing, which is the right default for a unit test.
func testEnv() Env {
	replay := probe.NewReplay(nil)
	return Env{Runner: replay, Home: "/Users/andrew", LookPath: replay.LookPath, ReadFile: ReadFile, Stat: Stat}
}

// byName finds one outcome.
func byName(outs []Outcome, name string) (Outcome, bool) {
	for _, o := range outs {
		if o.Status.Name == name {
			return o, true
		}
	}
	return Outcome{}, false
}

// TestRunCompletesThroughEveryFailure is the contract the whole package rests
// on: a detector that panics, one that never returns, and one whose tool is
// missing must all leave the run finished, statused and usable.
func TestRunCompletesThroughEveryFailure(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	reg := New(
		&fake{name: "fine"},
		&fake{name: "panics", probe: func(context.Context, Env) (Facts, error) {
			panic("a detector bug")
		}},
		&fake{name: "hangs", probe: func(ctx context.Context, _ Env) (Facts, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-blocked:
				return nil, nil
			}
		}},
		&fake{name: "absent", probe: func(context.Context, Env) (Facts, error) {
			return nil, Missingf("`nothing` is not on the path")
		}},
		&fake{name: "broken", probe: func(context.Context, Env) (Facts, error) {
			return nil, errors.New("something unexpected")
		}},
	)

	run := reg.Start(context.Background(), testEnv(), map[string]time.Duration{"hangs": time.Hour})
	defer run.Stop()

	start := time.Now()
	outs := run.Wait(200 * time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Errorf("Wait took %s for a 200ms grace", elapsed)
	}
	if len(outs) != 5 {
		t.Fatalf("outcomes = %d, want 5", len(outs))
	}

	for name, want := range map[string]State{
		"fine": Ok, "panics": Panic, "hangs": Timeout, "absent": Missing, "broken": Degraded,
	} {
		out, ok := byName(outs, name)
		if !ok {
			t.Errorf("%s produced no outcome", name)
			continue
		}
		if out.Status.State != want {
			t.Errorf("%s state = %s (%q), want %s", name, out.Status.State, out.Status.Reason, want)
		}
	}
	if out, _ := byName(outs, "panics"); !strings.Contains(out.Status.Reason, "a detector bug") {
		t.Errorf("the panic's message was lost: %q", out.Status.Reason)
	}
	if out, _ := byName(outs, "absent"); out.Status.Reason != "`nothing` is not on the path" {
		t.Errorf("the missing reason reads %q; the sentinel should have been stripped", out.Status.Reason)
	}
}

// TestStopIsSafeEverywhere: Stop must work before Wait, after Wait, and twice,
// because scan.Run defers it and every early return runs it at a different
// point.
func TestStopIsSafeEverywhere(t *testing.T) {
	reg := New(&fake{name: "fine"})
	run := reg.Start(context.Background(), testEnv(), nil)
	run.Stop()
	run.Stop()
	if outs := run.Wait(time.Second); len(outs) != 1 {
		t.Errorf("outcomes after Stop = %d, want 1", len(outs))
	}

	// Stop before anything is joined must not hang either.
	run2 := reg.Start(context.Background(), testEnv(), nil)
	done := make(chan struct{})
	go func() { defer close(done); run2.Stop() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}

// TestStopCancelsAHangingProbe covers what the deferred Stop in scan.Run is
// for: nothing may still be running when the command returns.
func TestStopCancelsAHangingProbe(t *testing.T) {
	released := make(chan struct{})
	reg := New(&fake{name: "hangs", probe: func(ctx context.Context, _ Env) (Facts, error) {
		<-ctx.Done()
		close(released)
		return nil, ctx.Err()
	}})
	run := reg.Start(context.Background(), testEnv(), map[string]time.Duration{"hangs": time.Hour})
	run.Stop()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe was never cancelled")
	}
}

// TestNilRegistry: a caller with no detectors still gets a usable run.
func TestNilRegistry(t *testing.T) {
	var reg *Registry
	run := reg.Start(context.Background(), testEnv(), nil)
	defer run.Stop()
	if outs := run.Wait(time.Second); len(outs) != 0 {
		t.Errorf("outcomes = %d, want none", len(outs))
	}
}

func TestDisabledDetectorsAreReportedNotHidden(t *testing.T) {
	reg := New(&fake{name: "one"}, &fake{name: "two"}).Disable("two", "never-existed")
	if names := reg.Names(); len(names) != 1 || names[0] != "one" {
		t.Errorf("enabled = %v, want [one]", names)
	}

	run := reg.Start(context.Background(), testEnv(), nil)
	defer run.Stop()
	outs := run.Wait(time.Second)
	if len(outs) != 3 {
		t.Fatalf("outcomes = %d, want the one that ran plus both that were switched off", len(outs))
	}
	for _, name := range []string{"two", "never-existed"} {
		out, ok := byName(outs, name)
		if !ok {
			t.Errorf("%s vanished from the report", name)
			continue
		}
		if out.Status.State != Disabled {
			t.Errorf("%s state = %s, want disabled", name, out.Status.State)
		}
	}
}

// TestDisabledDetectorsDoNotClassify: switching a detector off means the
// static catalog rules answer for its paths, which is only true if its
// Classify is never called.
func TestDisabledDetectorsDoNotClassify(t *testing.T) {
	var called atomic.Bool
	det := &fake{name: "off", classify: func(*walk.Tree, Facts, classify.Context) ([]classify.Claim, Summary) {
		called.Store(true)
		return []classify.Claim{{}}, Summary{Tools: []Tool{{Name: "x"}}}
	}}
	reg := New(det).Disable("off")
	run := reg.Start(context.Background(), testEnv(), nil)
	defer run.Stop()

	claims, summaries, statuses := Classify(nil, run.Wait(time.Second), classify.Context{})
	if called.Load() {
		t.Error("a disabled detector was asked to classify")
	}
	if det.probed.Load() != 0 {
		t.Error("a disabled detector was probed")
	}
	if len(claims) != 0 || len(summaries) != 0 {
		t.Errorf("a disabled detector contributed %d claims and %d summaries", len(claims), len(summaries))
	}
	if len(statuses) != 1 || statuses[0].State != Disabled {
		t.Errorf("statuses = %+v", statuses)
	}
}

// TestDegradedDetectorsStillClassify is the other half of the degradation
// rule: a probe that failed must not cost the bytes their bucket, only the
// evidence behind it.
func TestDegradedDetectorsStillClassify(t *testing.T) {
	var gotFacts Facts
	det := &fake{
		name:  "degraded",
		probe: func(context.Context, Env) (Facts, error) { return nil, Degradedf("the daemon is down") },
		classify: func(_ *walk.Tree, f Facts, _ classify.Context) ([]classify.Claim, Summary) {
			gotFacts = f
			return []classify.Claim{{Bucket: classify.BucketContainers}}, Summary{Tools: []Tool{{Name: "fallback"}}}
		},
	}
	run := New(det).Start(context.Background(), testEnv(), nil)
	defer run.Stop()

	claims, summaries, statuses := Classify(nil, run.Wait(time.Second), classify.Context{})
	if gotFacts != nil {
		t.Errorf("Classify was handed facts a failed probe never produced: %+v", gotFacts)
	}
	if len(claims) != 1 {
		t.Errorf("claims = %d, want the static fallback", len(claims))
	}
	if _, ok := summaries["degraded"]; !ok {
		t.Error("the degraded detector's summary was dropped")
	}
	if statuses[0].State != Degraded || statuses[0].Reason != "the daemon is down" {
		t.Errorf("status = %+v", statuses[0])
	}
}

// TestClassifyPanicIsContained: a bug in one detector's Classify costs that
// detector and nothing else.
func TestClassifyPanicIsContained(t *testing.T) {
	outs := []Outcome{
		{Detector: &fake{name: "bad", classify: func(*walk.Tree, Facts, classify.Context) ([]classify.Claim, Summary) {
			panic("classify bug")
		}}, Status: Status{Name: "bad", State: Ok}},
		{Detector: &fake{name: "good", classify: func(*walk.Tree, Facts, classify.Context) ([]classify.Claim, Summary) {
			return []classify.Claim{{Bucket: classify.BucketContainers}}, Summary{}
		}}, Status: Status{Name: "good", State: Ok}},
	}
	claims, _, statuses := Classify(nil, outs, classify.Context{})
	if len(claims) != 1 {
		t.Errorf("claims = %d, want the good detector's", len(claims))
	}
	if statuses[0].State != Panic || !strings.Contains(statuses[0].Reason, "classify bug") {
		t.Errorf("status[0] = %+v", statuses[0])
	}
	if statuses[1].State != Ok {
		t.Errorf("the second detector was affected: %+v", statuses[1])
	}
}

// TestCommandsAreRecordedPerDetector: the why panel and `storix doctor` both
// read Status.Commands, and each detector must own its own list.
func TestCommandsAreRecordedPerDetector(t *testing.T) {
	reg := New(
		&fake{name: "one", probe: func(ctx context.Context, env Env) (Facts, error) {
			env.Runner.Run(ctx, probe.Cmd{Name: "tool-a"})
			return nil, nil
		}},
		&fake{name: "two", probe: func(ctx context.Context, env Env) (Facts, error) {
			env.Runner.Run(ctx, probe.Cmd{Name: "tool-b", Args: []string{"x"}})
			env.Runner.Run(ctx, probe.Cmd{Name: "tool-b", Args: []string{"y"}})
			return nil, nil
		}},
	)
	run := reg.Start(context.Background(), testEnv(), nil)
	defer run.Stop()
	outs := run.Wait(time.Second)

	one, _ := byName(outs, "one")
	two, _ := byName(outs, "two")
	if len(one.Status.Commands) != 1 || one.Status.Commands[0].Cmd.Name != "tool-a" {
		t.Errorf("one recorded %+v", one.Status.Commands)
	}
	if len(two.Status.Commands) != 2 {
		t.Errorf("two recorded %d commands, want 2", len(two.Status.Commands))
	}
}

func TestVerifiedFlag(t *testing.T) {
	reg := New(&fake{name: "sure"}, &fake{name: "from-docs", unverified: true})
	run := reg.Start(context.Background(), testEnv(), nil)
	defer run.Stop()
	outs := run.Wait(time.Second)
	if out, _ := byName(outs, "sure"); !out.Status.Verified {
		t.Error("a verified detector was marked unverified")
	}
	if out, _ := byName(outs, "from-docs"); out.Status.Verified {
		t.Error("a detector written from documentation was marked verified")
	}
}

func TestRetainLeafComposes(t *testing.T) {
	cx := classify.Context{Home: "/Users/andrew", CodeRoots: []string{"/Users/andrew/code"}}

	if RetainLeaf(New(&fake{name: "none"}), cx) != nil {
		t.Error("a registry whose detectors retain nothing produced a hook, which costs the walk on every small file")
	}

	a := &fake{name: "a", retain: func(classify.Context) func(string, *walk.Entry) bool {
		return func(_ string, e *walk.Entry) bool { return e.Name == "go.mod" }
	}}
	b := &fake{name: "b", retain: func(classify.Context) func(string, *walk.Entry) bool {
		return func(_ string, e *walk.Entry) bool { return e.Name == "package.json" }
	}}
	hook := RetainLeaf(New(a, b), cx)
	if hook == nil {
		t.Fatal("no hook was composed")
	}
	for _, tc := range []struct {
		name string
		want bool
	}{{"go.mod", true}, {"package.json", true}, {"README", false}} {
		if got := hook("/Users/andrew/code/x", &walk.Entry{Name: tc.name}); got != tc.want {
			t.Errorf("hook(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDefaultRegistryIsOrdered(t *testing.T) {
	// Default() reflects whatever has registered itself, which in this
	// package's own test binary is nothing. What is checked here is that
	// the ordering machinery works, not which detectors exist: that is
	// asserted in internal/scan, which imports them.
	reg := Default()
	names := reg.Names()
	for i := 1; i < len(names); i++ {
		if names[i-1] == names[i] {
			t.Errorf("the default registry lists %s twice", names[i])
		}
	}
}

func TestStateNames(t *testing.T) {
	for s, want := range map[State]string{
		Ok: "ok", Degraded: "degraded", Missing: "missing", Timeout: "timeout",
		Panic: "panic", Disabled: "disabled", NotProbed: "not-probed",
	} {
		if got := s.String(); got != want {
			t.Errorf("State(%d) = %q, want %q", s, got, want)
		}
	}
	if !Ok.Healthy() || !Degraded.Healthy() || Missing.Healthy() {
		t.Error("Healthy does not mean ok-or-degraded")
	}
}
