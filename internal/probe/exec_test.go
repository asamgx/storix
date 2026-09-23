package probe

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testExec is a runner confined to the directories the test's commands live
// in, so the result does not depend on what is installed on the machine.
func testExec() *Exec {
	return &Exec{Path: []string{"/bin", "/usr/bin"}, Limit: 2}
}

func TestExecEcho(t *testing.T) {
	res := testExec().Run(context.Background(), Cmd{Name: "echo", Args: []string{"hello", "world"}})
	if !res.OK() {
		t.Fatalf("echo: %+v", res)
	}
	if got := strings.TrimSpace(res.Stdout); got != "hello world" {
		t.Errorf("stdout = %q, want %q", got, "hello world")
	}
	if res.Duration <= 0 {
		t.Error("duration was not measured")
	}
	if res.Reason() != "" {
		t.Errorf("Reason() = %q on a successful command", res.Reason())
	}
}

func TestExecNonZeroExit(t *testing.T) {
	res := testExec().Run(context.Background(), Cmd{Name: "false"})
	switch {
	case res.Missing:
		t.Fatal("false was not found")
	case res.Err != nil:
		t.Fatalf("a non-zero exit became an error: %v", res.Err)
	case res.ExitCode != 1:
		t.Errorf("exit code = %d, want 1", res.ExitCode)
	case res.OK():
		t.Error("OK() is true for a failing command")
	}
	if res.Reason() == "" {
		t.Error("a failing command has no reason")
	}
}

func TestExecTimeout(t *testing.T) {
	start := time.Now()
	res := testExec().Run(context.Background(), Cmd{
		Name: "sleep", Args: []string{"30"}, Timeout: 150 * time.Millisecond,
	})
	if !res.TimedOut {
		t.Fatalf("sleep 30 did not time out: %+v", res)
	}
	if res.Err != nil {
		t.Errorf("a timeout became an error: %v", res.Err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the deadline took %s to take effect", elapsed)
	}
	if !strings.Contains(res.Reason(), "timed out") {
		t.Errorf("Reason() = %q, want it to mention the timeout", res.Reason())
	}
}

func TestExecMissing(t *testing.T) {
	res := testExec().Run(context.Background(), Cmd{Name: "storix-no-such-tool"})
	if !res.Missing {
		t.Fatalf("a missing binary was not reported missing: %+v", res)
	}
	if res.OK() {
		t.Error("OK() is true for a missing binary")
	}
	if res.Reason() != "not installed" {
		t.Errorf("Reason() = %q, want %q", res.Reason(), "not installed")
	}
}

func TestExecCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := testExec().Run(ctx, Cmd{Name: "echo", Args: []string{"hi"}})
	if res.OK() {
		t.Errorf("a cancelled context still ran the command: %+v", res)
	}
}

// TestExecEnvIsAppended checks the rule that matters most for correctness:
// Cmd.Env adds to the process environment, it does not replace it, and PATH
// and HOME are the runner's rather than the caller's.
func TestExecEnvIsAppended(t *testing.T) {
	t.Setenv("STORIX_TEST_INHERITED", "inherited")
	e := &Exec{Path: []string{"/bin", "/usr/bin"}, Home: "/tmp/storix-home"}
	res := e.Run(context.Background(), Cmd{
		Name: "env",
		Env:  []string{"STORIX_TEST_EXTRA=extra"},
	})
	if !res.OK() {
		t.Fatalf("env: %+v", res)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			vars[k] = v
		}
	}
	for key, want := range map[string]string{
		"STORIX_TEST_INHERITED":   "inherited",
		"STORIX_TEST_EXTRA":       "extra",
		"HOME":                    "/tmp/storix-home",
		"HOMEBREW_NO_AUTO_UPDATE": "1",
		"HOMEBREW_NO_ANALYTICS":   "1",
		"NO_COLOR":                "1",
	} {
		if vars[key] != want {
			t.Errorf("%s = %q, want %q", key, vars[key], want)
		}
	}
	if vars["PATH"] != "/bin:/usr/bin" {
		t.Errorf("PATH = %q, want the runner's own", vars["PATH"])
	}
}

// TestExecStdoutCap checks that a command producing more than the cap is
// truncated rather than allowed to exhaust memory, and that the command still
// sees a successful write.
func TestExecStdoutCap(t *testing.T) {
	buf := capped{w: &bytes.Buffer{}, limit: 8}
	n, err := buf.Write([]byte("0123456789"))
	if err != nil || n != 10 {
		t.Fatalf("Write = %d, %v; want 10, nil", n, err)
	}
	if got := buf.w.String(); got != "01234567" {
		t.Errorf("kept %q, want the first 8 bytes", got)
	}
}

func TestLookPath(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "storix-fake-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // a fixture script must be executable
		t.Fatal(err)
	}
	notExec := filepath.Join(dir, "storix-not-exec")
	if err := os.WriteFile(notExec, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := &Exec{Path: []string{dir}}
	if got, err := e.LookPath("storix-fake-tool"); err != nil || got != tool {
		t.Errorf("LookPath = %q, %v; want %q, nil", got, err, tool)
	}
	if _, err := e.LookPath("storix-not-exec"); err == nil {
		t.Error("a non-executable file was reported as a tool")
	}
	if _, err := e.LookPath("storix-absent"); err == nil {
		t.Error("an absent tool was found")
	}
}

func TestBuildPath(t *testing.T) {
	got := buildPath("/usr/bin:/bin:/usr/bin", "/Users/andrew")
	if got[0] != "/usr/bin" || got[1] != "/bin" {
		t.Errorf("the inherited PATH did not come first: %v", got[:2])
	}
	seen := map[string]int{}
	for _, dir := range got {
		seen[dir]++
	}
	for dir, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times", dir, n)
		}
	}
	if !contains(got, "/opt/homebrew/bin") {
		t.Error("the augmented path is missing /opt/homebrew/bin")
	}
	if !contains(got, "/Users/andrew/.orbstack/bin") {
		t.Errorf("~ was not expanded: %v", got)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
