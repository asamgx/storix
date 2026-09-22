package mac

import (
	"os"
	"testing"
)

func TestTerminalName(t *testing.T) {
	cases := []struct {
		comm, program, bundleID, want string
	}{
		{"ghostty", "tmux", "com.mitchellh.ghostty", "Ghostty"},
		{"", "tmux", "com.mitchellh.ghostty", "Ghostty"},
		{"", "Apple_Terminal", "", "Terminal"},
		{"iTerm2", "", "", "iTerm2"},
		{"Terminal", "", "com.apple.Terminal", "Terminal"},
		{"", "iTerm.app", "", "iTerm2"},
		{"", "", "com.googlecode.iterm2", "Iterm2"},
		{"", "", "net.kovidgoyal.kitty", "Kitty"},
		{"", "", "", "your terminal"},
		{"", "tmux", "", "your terminal"},
		{"login", "", "", "login"},
		{"WEZTERM-GUI", "", "", "WezTerm"},
		{"", "", "singleword", "singleword"},
	}
	for _, c := range cases {
		if got := terminalName(c.comm, c.program, c.bundleID); got != c.want {
			t.Errorf("terminalName(%q, %q, %q) = %q, want %q", c.comm, c.program, c.bundleID, got, c.want)
		}
	}
}

func TestIsMultiplexer(t *testing.T) {
	for _, c := range []string{"tmux", "tmux: server", "TMUX", "screen"} {
		if !isMultiplexer(c) {
			t.Errorf("isMultiplexer(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"zsh", "ghostty", "login", "", "tmuxinator"} {
		if isMultiplexer(c) {
			t.Errorf("isMultiplexer(%q) = true, want false", c)
		}
	}
}

func TestCapitalize(t *testing.T) {
	cases := map[string]string{"ghostty": "Ghostty", "Ghostty": "Ghostty", "": "", "1password": "1password"}
	for in, want := range cases {
		if got := capitalize(in); got != want {
			t.Errorf("capitalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectTerminalLive(t *testing.T) {
	term := DetectTerminal()
	if term.AppName == "" {
		t.Error("no terminal name produced")
	}
	if term.Program != os.Getenv("TERM_PROGRAM") {
		t.Errorf("Program = %q, want %q", term.Program, os.Getenv("TERM_PROGRAM"))
	}
	if os.Getenv("TMUX") != "" && !term.ViaTmux {
		t.Error("ViaTmux = false inside tmux")
	}
	// The ancestor walk must land on some process, and never on launchd or
	// on the test binary itself.
	if term.PID == 1 || term.PID == os.Getpid() {
		t.Errorf("terminal pid = %d, want a real ancestor", term.PID)
	}
	t.Logf("terminal: %+v", term)
}

func TestFindTerminalProcessStopsOnGarbage(t *testing.T) {
	// A pid that cannot be looked up must not hang or panic.
	pid, comm := findTerminalProcess(1 << 30)
	if pid != 0 || comm != "" {
		t.Errorf("findTerminalProcess(bogus) = %d, %q; want 0, \"\"", pid, comm)
	}
}
