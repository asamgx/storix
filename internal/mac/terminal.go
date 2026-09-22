package mac

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Terminal identifies the application the process is running under, so hints
// can name the app that needs Full Disk Access.
type Terminal struct {
	Program  string // TERM_PROGRAM, e.g. "ghostty", "Apple_Terminal", "tmux"
	BundleID string // __CFBundleIdentifier, e.g. "com.mitchellh.ghostty"
	AppName  string // human name for hint text, e.g. "Ghostty"
	ViaTmux  bool   // running inside tmux or screen
	PID      int    // pid the ancestor walk ended on; 0 when unknown
}

// maxAncestors caps the parent-process walk.
const maxAncestors = 24

// terminalApps maps a lowercased process name (kinfo_proc p_comm, truncated to
// 16 bytes by the kernel) to a display name.
var terminalApps = map[string]string{
	"ghostty":      "Ghostty",
	"iterm2":       "iTerm2",
	"terminal":     "Terminal",
	"alacritty":    "Alacritty",
	"kitty":        "kitty",
	"wezterm-gui":  "WezTerm",
	"wezterm":      "WezTerm",
	"hyper":        "Hyper",
	"warp":         "Warp",
	"rio":          "Rio",
	"tabby":        "Tabby",
	"contour":      "Contour",
	"wave":         "Wave",
	"code":         "Visual Studio Code",
	"code helper":  "Visual Studio Code",
	"cursor":       "Cursor",
	"windsurf":     "Windsurf",
	"electron":     "Electron",
	"sshd":         "sshd (remote session)",
	"sshd-session": "sshd (remote session)",
}

// termProgramNames maps TERM_PROGRAM values to display names.
var termProgramNames = map[string]string{
	"Apple_Terminal": "Terminal",
	"iTerm.app":      "iTerm2",
	"ghostty":        "Ghostty",
	"WarpTerminal":   "Warp",
	"vscode":         "Visual Studio Code",
	"WezTerm":        "WezTerm",
	"Hyper":          "Hyper",
	"rio":            "Rio",
	"Tabby":          "Tabby",
	"alacritty":      "Alacritty",
	"kitty":          "kitty",
}

// DetectTerminal identifies the terminal application. It reads TERM_PROGRAM
// and __CFBundleIdentifier, then walks the parent-process chain. Inside tmux
// the chain leads to the tmux server, whose parent is launchd, so the walk
// restarts from the attached tmux client, whose ancestors are the real
// terminal application.
func DetectTerminal() Terminal {
	t := Terminal{
		Program:  os.Getenv("TERM_PROGRAM"),
		BundleID: os.Getenv("__CFBundleIdentifier"),
	}
	t.ViaTmux = os.Getenv("TMUX") != "" || os.Getenv("STY") != "" ||
		strings.EqualFold(t.Program, "tmux") || strings.EqualFold(t.Program, "screen")

	pid, comm := findTerminalProcess(os.Getppid())
	t.PID = pid
	t.AppName = terminalName(comm, t.Program, t.BundleID)
	return t
}

// findTerminalProcess walks up from start and returns the first ancestor that
// looks like a terminal application, or the last ancestor below launchd.
func findTerminalProcess(start int) (pid int, comm string) {
	cur := start
	lastPID, lastComm := 0, ""
	redirected := false
	for i := 0; i < maxAncestors && cur > 1; i++ {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", cur)
		if err != nil || kp == nil {
			break
		}
		name := unix.ByteSliceToString(kp.Proc.P_comm[:])
		lastPID, lastComm = cur, name

		if !redirected && isMultiplexer(name) {
			if client, ok := tmuxClientPID(); ok && client > 1 && client != cur {
				redirected = true
				cur = client
				continue
			}
		}
		if _, ok := terminalApps[strings.ToLower(name)]; ok {
			return cur, name
		}
		next := int(kp.Eproc.Ppid)
		if next == cur {
			break
		}
		cur = next
	}
	return lastPID, lastComm
}

// isMultiplexer reports whether a process name is a terminal multiplexer,
// which detaches from the terminal that started it.
func isMultiplexer(comm string) bool {
	c := strings.ToLower(comm)
	return c == "tmux" || strings.HasPrefix(c, "tmux:") || c == "screen" || c == "screen-256color"
}

// tmuxClientPID asks tmux for the pid of the attached client. The client is a
// direct child of the terminal application, unlike the tmux server.
func tmuxClientPID() (int, bool) {
	if os.Getenv("TMUX") == "" {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_pid}").Output()
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// terminalName picks the friendliest available name for the terminal.
func terminalName(comm, program, bundleID string) string {
	if n, ok := terminalApps[strings.ToLower(comm)]; ok {
		return n
	}
	if n, ok := termProgramNames[program]; ok {
		return n
	}
	if bundleID != "" {
		if i := strings.LastIndex(bundleID, "."); i >= 0 && i+1 < len(bundleID) {
			return capitalize(bundleID[i+1:])
		}
		return bundleID
	}
	if comm != "" {
		return comm
	}
	if program != "" && !strings.EqualFold(program, "tmux") {
		return program
	}
	return "your terminal"
}

// capitalize upper-cases the first ASCII letter of s.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	if c := s[0]; c >= 'a' && c <= 'z' {
		return string(c-32) + s[1:]
	}
	return s
}
