package probe

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecorderReplayRoundTrip is the contract the detector fixtures rest on:
// what a recording captured on a real machine, a replay reproduces exactly.
func TestRecorderReplayRoundTrip(t *testing.T) {
	rec := NewRecorder(testExec())
	ctx := context.Background()
	rec.Run(ctx, Cmd{Name: "echo", Args: []string{"one", "two"}})
	rec.Run(ctx, Cmd{Name: "false"})
	rec.Run(ctx, Cmd{Name: "storix-no-such-tool"})

	records := rec.Records()
	if len(records) != 3 {
		t.Fatalf("recorded %d commands, want 3", len(records))
	}

	path := filepath.Join(t.TempDir(), "echo.json")
	if err := WriteFixture(path, records); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadFixture(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, rec := range records {
		got := replay.Run(ctx, rec.Cmd)
		want := rec.Result
		if got.Stdout != want.Stdout || got.ExitCode != want.ExitCode ||
			got.Missing != want.Missing || got.TimedOut != want.TimedOut {
			t.Errorf("%s replayed as %+v, want %+v", rec.Cmd.Key(), got, want)
		}
	}
	if got := replay.Run(ctx, Cmd{Name: "echo", Args: []string{"one", "two"}}); strings.TrimSpace(got.Stdout) != "one two" {
		t.Errorf("stdout did not survive the round trip: %q", got.Stdout)
	}
}

func TestReplayUnknownCommandIsMissing(t *testing.T) {
	replay := NewReplay(nil)
	res := replay.Run(context.Background(), Cmd{Name: "podman", Args: []string{"machine", "list"}})
	if !res.Missing {
		t.Errorf("an unrecorded command was not reported missing: %+v", res)
	}
	if _, err := replay.LookPath("podman"); err == nil {
		t.Error("LookPath found a tool the fixture never names")
	}
}

func TestReplayLookPath(t *testing.T) {
	replay := NewReplay([]Record{{
		Cmd:    Cmd{Name: "orb", Args: []string{"list", "-f", "json"}},
		Result: Result{Stdout: "[]\n"},
	}})
	if _, err := replay.LookPath("orb"); err != nil {
		t.Errorf("LookPath(orb) = %v, want it found", err)
	}
	if _, err := replay.LookPath("or"); err == nil {
		t.Error("LookPath matched a prefix of a recorded name")
	}
}

func TestRecorderWithoutInner(t *testing.T) {
	rec := &Recorder{}
	res := rec.Run(context.Background(), Cmd{Name: "echo"})
	if !res.Missing {
		t.Errorf("a recorder with no runner returned %+v, want Missing", res)
	}
	if len(rec.Records()) != 1 {
		t.Error("the attempt was not recorded")
	}
}

func TestCmdKey(t *testing.T) {
	for _, tc := range []struct {
		cmd  Cmd
		want string
	}{
		{Cmd{Name: "orb"}, "orb"},
		{Cmd{Name: "orb", Args: []string{"list", "-f", "json"}}, "orb list -f json"},
		{Cmd{Name: "docker", Args: []string{"--context", "orbstack", "system", "df"}}, "docker --context orbstack system df"},
	} {
		if got := tc.cmd.Key(); got != tc.want {
			t.Errorf("Key() = %q, want %q", got, tc.want)
		}
	}
}
