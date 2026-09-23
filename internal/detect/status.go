package detect

import (
	"errors"
	"fmt"
	"time"

	"github.com/asamgx/storix/internal/probe"
)

// State is how a detector's probe ended.
type State uint8

const (
	// Ok is a probe that ran and produced complete facts.
	Ok State = iota
	// Degraded is a probe that ran partially: the tool is there but the
	// daemon is down, or one of several commands failed. The claims fall
	// back to the static paths and the buckets stay correct.
	Degraded
	// Missing is a tool the machine does not have. It is the ordinary
	// answer, not a failure.
	Missing
	// Timeout is a probe still running when the walk finished.
	Timeout
	// Panic is a bug in a detector, caught so that it costs one detector
	// rather than the scan.
	Panic
	// Disabled is a detector the user switched off with --disable-detector.
	Disabled
	// NotProbed is a detector whose facts came from a cache written before
	// it existed, so nothing was asked of the machine.
	NotProbed
)

// stateNames are the names, indexed by the state.
var stateNames = [...]string{"ok", "degraded", "missing", "timeout", "panic", "disabled", "not-probed"}

func (s State) String() string {
	if int(s) >= len(stateNames) {
		return "invalid"
	}
	return stateNames[s]
}

// Healthy reports whether the detector produced usable evidence.
func (s State) Healthy() bool { return s == Ok || s == Degraded }

// Status is what happened to one detector, and the evidence it gathered.
type Status struct {
	Name     string        `json:"name"`
	State    State         `json:"-"`
	Reason   string        `json:"reason,omitempty"`
	Duration time.Duration `json:"duration_ns"`
	// Commands is every probe the detector ran, in order. It is the why
	// panel's evidence and `storix doctor`'s tool listing.
	Commands []probe.Record `json:"commands,omitempty"`
	// Verified is false for a detector written from documentation rather
	// than against a machine that had the tool.
	Verified bool `json:"verified"`
}

// ErrMissing is returned by Probe when the tool is not installed. It is not a
// failure: "you do not have podman" is the answer to the question.
var ErrMissing = errors.New("not installed")

// ErrDegraded is returned by Probe, optionally alongside usable facts, when
// some of the evidence could not be gathered.
var ErrDegraded = errors.New("degraded")

// Missingf reports a tool the machine does not have, with the reason a person
// would want: which binaries were looked for, which directories were not
// there.
func Missingf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrMissing, fmt.Sprintf(format, a...))
}

// Degradedf reports a probe that ran partially.
func Degradedf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrDegraded, fmt.Sprintf(format, a...))
}

// stateOf maps a Probe error onto a state and a reason.
//
// Any error that is neither sentinel is a degradation rather than a failure:
// a detector that returns an unexpected error has still not earned the right
// to stop the scan, and the static rules still bucket its paths.
func stateOf(err error) (State, string) {
	switch {
	case err == nil:
		return Ok, ""
	case errors.Is(err, ErrMissing):
		return Missing, trimSentinel(err.Error(), ErrMissing.Error())
	case errors.Is(err, ErrDegraded):
		return Degraded, trimSentinel(err.Error(), ErrDegraded.Error())
	default:
		return Degraded, err.Error()
	}
}

// trimSentinel strips the sentinel prefix a wrapped error carries, so a
// status line reads "daemon not running" rather than "degraded: daemon not
// running" next to a column that already says "degraded".
func trimSentinel(msg, sentinel string) string {
	if len(msg) > len(sentinel)+2 && msg[:len(sentinel)] == sentinel && msg[len(sentinel):len(sentinel)+2] == ": " {
		return msg[len(sentinel)+2:]
	}
	if msg == sentinel {
		return ""
	}
	return msg
}
