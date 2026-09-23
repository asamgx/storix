// Package probe runs the external commands storix asks the machine about
// itself with: `brew --cellar`, `docker system df`, `go env`.
//
// It exists so that every detector shells out the same way. A probe has a
// timeout, a bounded PATH, an environment that cannot inherit a surprise from
// the user's shell, and a result that is data rather than an error: a tool
// that is not installed is a [Result] with Missing set, not a failure, because
// "you do not have podman" is an answer and not a problem.
//
// Nothing here opens a file the machine did not name on a command line, and
// every command a scan ran can be replayed from a fixture, which is what makes
// the detectors testable without the tools they describe.
package probe

import (
	"context"
	"strings"
	"time"
)

// DefaultTimeout is how long a command may run when Cmd.Timeout is zero. Five
// seconds is well past what a local query costs and well under what a user
// will wait before deciding storix has hung.
const DefaultTimeout = 5 * time.Second

// MaxStdout is how much of a command's output is kept. Eight megabytes is
// larger than any listing a detector asks for and small enough that a command
// that decides to stream cannot exhaust memory.
const MaxStdout = 8 << 20

// MaxStderr is how much of a command's error output is kept. It is only ever
// shown to a person as a reason, so a few kilobytes is the whole budget.
const MaxStderr = 8 << 10

// Cmd is one command to run.
type Cmd struct {
	// Name is the executable, looked up on the runner's path. A name with
	// a slash in it is used as given.
	Name string
	// Args are the arguments, not including the name.
	Args []string
	// Env are extra environment variables, "K=V". They are appended to a
	// filtered copy of the process environment rather than replacing it:
	// a probe that dropped the whole environment would lose the locale,
	// the temporary directory and the proxy settings the tool needs.
	Env []string
	// Dir is the working directory; empty means the process's own.
	Dir string
	// Timeout bounds the run; zero selects DefaultTimeout.
	Timeout time.Duration
}

// Key identifies a command in a fixture file. It is the name and the
// arguments joined by spaces, which is how a person writing a fixture by hand
// would name it and how a recording reads back.
func (c Cmd) Key() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}

// Result is what running a command produced.
//
// Every field is filled in for every outcome, so a caller that only wants
// stdout can read it without asking what went wrong first, and a caller that
// cares reads Missing, TimedOut and ExitCode in that order.
type Result struct {
	Stdout   string        `json:"stdout,omitempty"`
	Stderr   string        `json:"stderr,omitempty"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration_ns"`
	// TimedOut is set when the command was killed at its deadline.
	TimedOut bool `json:"timed_out,omitempty"`
	// Missing is set when the executable was not found on the path. It is
	// the ordinary answer for a tool the machine does not have.
	Missing bool `json:"missing,omitempty"`
	// Err is why the command could not be run or did not finish. A
	// non-zero exit is not an error: the exit code carries that.
	Err error `json:"-"`
	// ErrText is Err as a string, which is what survives a fixture round
	// trip. Run sets both; LoadFixture sets this one and rebuilds Err.
	ErrText string `json:"error,omitempty"`
}

// OK reports whether the command ran to a zero exit.
func (r Result) OK() bool {
	return !r.Missing && !r.TimedOut && r.Err == nil && r.ExitCode == 0
}

// Reason is a one-line description of a result that is not OK, for a status
// line or a why panel. It is empty when the command succeeded.
func (r Result) Reason() string {
	switch {
	case r.OK():
		return ""
	case r.Missing:
		return "not installed"
	case r.TimedOut:
		return "timed out after " + r.Duration.Round(time.Millisecond).String()
	case r.Err != nil:
		return r.Err.Error()
	}
	if line := firstLine(r.Stderr); line != "" {
		return line
	}
	return "exit status " + itoa(r.ExitCode)
}

// Runner runs commands. Exec is the real one; Replay is the fixture one.
//
// An implementation must not return an error: everything that can go wrong
// belongs in the Result, because a detector that has to branch on err before
// it can branch on Missing ends up treating "not installed" as a failure.
type Runner interface {
	Run(ctx context.Context, c Cmd) Result
}

// Record is one command and what it produced. A recording is a slice of them
// and is exactly what LoadFixture reads back.
type Record struct {
	Cmd    Cmd    `json:"cmd"`
	Result Result `json:"result"`
}

// firstLine is the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// itoa formats a small int without pulling in strconv for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
