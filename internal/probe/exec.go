package probe

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/asamgx/storix/internal/mac"
)

// DefaultLimit is how many probes may run at once. The walk is already using
// sixteen workers on the disk; four concurrent processes keep the probes off
// the critical path without letting them compete with it.
const DefaultLimit = 4

// extraPath are the directories a tool may live in that a process started
// from Finder, a launch agent or a TUI will not have on its PATH. They are
// appended to the inherited PATH rather than replacing it, so a user who has
// put a tool somewhere of their own still gets it found first.
var extraPath = []string{
	"/opt/homebrew/bin",
	"/opt/homebrew/sbin",
	"/usr/local/bin",
	"~/.cargo/bin",
	"~/go/bin",
	"~/.bun/bin",
	"~/.local/bin",
	"~/.pyenv/shims",
	"~/.orbstack/bin",
	"/usr/bin",
	"/bin",
	"/usr/sbin",
	"/sbin",
}

// quietEnv is set on every probe. Homebrew updates itself and phones home
// when it is run interactively, both of which a read-only survey must not
// trigger, and a tool that colours its output produces escape sequences a
// parser has to strip.
var quietEnv = []string{
	"HOMEBREW_NO_AUTO_UPDATE=1",
	"HOMEBREW_NO_ANALYTICS=1",
	"HOMEBREW_NO_ENV_HINTS=1",
	"NO_COLOR=1",
	"CLICOLOR=0",
	"TERM=dumb",
}

// DefaultPath returns the directories a probe looks for executables in: the
// inherited PATH first, then the package-manager directories a non-login
// process would otherwise miss. Duplicates are dropped and a leading "~" is
// resolved against the invoking user's home.
func DefaultPath() []string {
	_, _, home, _ := invoking()
	return buildPath(os.Getenv("PATH"), home)
}

// buildPath is DefaultPath with its two inputs named, so a test can pass a
// PATH and a home of its own.
func buildPath(pathEnv, home string) []string {
	out := make([]string, 0, len(extraPath)+8)
	seen := make(map[string]bool, len(extraPath)+8)
	add := func(dir string) {
		if dir == "" {
			return
		}
		if strings.HasPrefix(dir, "~/") {
			if home == "" {
				return
			}
			dir = filepath.Join(home, dir[2:])
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, dir)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		add(dir)
	}
	for _, dir := range extraPath {
		add(dir)
	}
	return out
}

// Exec runs commands for real.
//
// The zero value works: the path defaults to DefaultPath and the concurrency
// limit to DefaultLimit, both resolved once on first use so that a runner
// shared by every detector does not rebuild the search path per command.
type Exec struct {
	// Path is the executable search path; nil selects DefaultPath.
	Path []string
	// Limit is the maximum number of concurrent processes; zero selects
	// DefaultLimit.
	Limit int
	// Home is the HOME every child sees; empty selects the invoking user's.
	Home string

	once sync.Once
	path []string
	sem  chan struct{}
	uid  int
	gid  int
	home string
	// asUser is set when the process is root but was invoked through sudo,
	// in which case every child drops back to the invoking user (D39).
	asUser bool
	env    []string
}

// init resolves the search path, the credentials and the base environment.
func (e *Exec) init() {
	e.once.Do(func() {
		uid, gid, home, viaSudo := invoking()
		if e.Home != "" {
			home = e.Home
		}
		e.uid, e.gid, e.home = uid, gid, home
		e.asUser = viaSudo && os.Geteuid() == 0

		e.path = e.Path
		if e.path == nil {
			e.path = buildPath(os.Getenv("PATH"), home)
		}
		limit := e.Limit
		if limit <= 0 {
			limit = DefaultLimit
		}
		e.sem = make(chan struct{}, limit)
		e.env = baseEnv(strings.Join(e.path, string(filepath.ListSeparator)), home)
	})
}

// baseEnv is the process environment with PATH and HOME replaced and the
// quiet flags appended. The rest is inherited, because a probe that ran with
// an empty environment would lose the locale and the temporary directory and
// would behave unlike the same command typed into a shell.
func baseEnv(path, home string) []string {
	src := os.Environ()
	out := make([]string, 0, len(src)+len(quietEnv)+2)
	for _, kv := range src {
		if overridden(kv) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "PATH="+path)
	if home != "" {
		out = append(out, "HOME="+home)
	}
	return append(out, quietEnv...)
}

// overridden reports whether a variable is one probe sets itself.
func overridden(kv string) bool {
	key, _, ok := strings.Cut(kv, "=")
	if !ok {
		return true
	}
	if key == "PATH" || key == "HOME" {
		return true
	}
	for _, q := range quietEnv {
		if qk, _, _ := strings.Cut(q, "="); qk == key {
			return true
		}
	}
	return false
}

// LookPath finds an executable on the runner's path. It is exported because a
// detector decides whether to probe at all by asking this question, and
// "podman is not on the path" is a status it reports rather than a command it
// runs and throws away.
func (e *Exec) LookPath(name string) (string, error) {
	e.init()
	return lookPath(name, e.path)
}

// lookPath searches dirs for an executable named name.
func lookPath(name string, dirs []string) (string, error) {
	if name == "" {
		return "", exec.ErrNotFound
	}
	if strings.ContainsRune(name, filepath.Separator) {
		if err := executable(name); err != nil {
			return "", &exec.Error{Name: name, Err: err}
		}
		return name, nil
	}
	for _, dir := range dirs {
		full := filepath.Join(dir, name)
		if err := executable(full); err == nil {
			return full, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// executable reports whether path is a regular file anyone may execute. It
// stats, never opens: a probe must not be the thing that materializes a file.
func executable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() || fi.Mode()&0o111 == 0 {
		return fs.ErrPermission
	}
	return nil
}

// Run runs one command. It never returns an error: see Runner.
func (e *Exec) Run(ctx context.Context, c Cmd) Result {
	e.init()
	if ctx == nil {
		ctx = context.Background()
	}

	bin, err := lookPath(c.Name, e.path)
	if err != nil {
		return Result{Missing: true, Err: err, ErrText: err.Error()}
	}

	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-ctx.Done():
		return errResult(ctx.Err())
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, c.Args...) //nolint:gosec // the name comes from a detector, never from user input
	cmd.Dir = c.Dir
	cmd.Env = append(append([]string(nil), e.env...), c.Env...)
	// A probe reads; nothing should ever be waiting on its standard input,
	// and a tool that asks a question must see the answer "no terminal"
	// rather than block until the deadline.
	cmd.Stdin = nil
	if e.asUser {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: uint32(e.uid), Gid: uint32(e.gid)}, //nolint:gosec // ids come from the kernel via SUDO_UID
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &capped{w: &stdout, limit: MaxStdout}
	cmd.Stderr = &capped{w: &stderr, limit: MaxStderr}

	start := time.Now()
	err = cmd.Run()
	res := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}

	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	default:
		res.Err = err
	}
	if runCtx.Err() != nil && ctx.Err() == nil {
		res.TimedOut = true
		res.Err = nil
		res.ExitCode = 0
	} else if ctx.Err() != nil {
		res.Err = ctx.Err()
	}
	if res.Err != nil {
		res.ErrText = res.Err.Error()
	}
	return res
}

// errResult is a result for a command that never started.
func errResult(err error) Result {
	return Result{Err: err, ErrText: err.Error()}
}

// capped is a writer that keeps the first limit bytes and silently drops the
// rest. A command that decides to stream must not be able to exhaust memory,
// and truncating is a better answer than killing it: the head of the output
// is the part a parser reads.
type capped struct {
	w     *bytes.Buffer
	limit int
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if room := c.limit - c.w.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		if _, err := c.w.Write(p); err != nil {
			return 0, err
		}
	}
	// The command is told everything was written, because a short write
	// makes it fail with EPIPE and report an error that is ours, not its.
	return n, nil
}

// invoking returns the identity a probe's children run as: the invoking
// user's uid, gid and home, and whether the process reached here through
// sudo. Under sudo that is the user who ran it, never root, because Homebrew
// refuses to run as root and a tool asked for its cache as root answers with
// root's cache (D39).
func invoking() (uid, gid int, home string, viaSudo bool) {
	uid, gid, viaSudo = mac.InvokingUser()
	if _, h, err := mac.InvokingHome(); err == nil && h != "" {
		return uid, gid, h, viaSudo
	}
	h, _ := os.UserHomeDir()
	return uid, gid, h, viaSudo
}

// HomeDir is the home directory a probe's children see.
func (e *Exec) HomeDir() string {
	e.init()
	return e.home
}
