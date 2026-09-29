package mac

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvokingHomeWithoutSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("this case is about a plain, unprivileged run")
	}
	t.Setenv("SUDO_UID", "")
	t.Setenv("SUDO_GID", "")
	t.Setenv("SUDO_USER", "")

	name, home, err := InvokingHome()
	if err != nil {
		t.Fatalf("InvokingHome: %v", err)
	}
	if home == "" || !filepath.IsAbs(home) {
		t.Fatalf("home = %q, want an absolute path", home)
	}
	if name == "" {
		t.Error("the invoking user has no name")
	}
	want, err := os.UserHomeDir()
	if err == nil && filepath.Clean(want) != home {
		t.Errorf("home = %q, want %q", home, want)
	}
}

// TestInvokingHomeUnderSudo checks the case the field exists for. It can only
// run as root, because InvokingUser ignores SUDO_UID otherwise — which is
// itself the behaviour that stops a plain user from being told they are
// someone else by an environment variable.
func TestInvokingHomeUnderSudo(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("run as root to exercise the sudo path")
	}
	me, err := user.Current()
	if err != nil {
		t.Skip("no current user to impersonate")
	}
	t.Setenv("SUDO_UID", me.Uid)
	t.Setenv("SUDO_GID", me.Gid)
	t.Setenv("SUDO_USER", me.Username)

	name, home, err := InvokingHome()
	if err != nil {
		t.Fatalf("InvokingHome: %v", err)
	}
	if name != me.Username {
		t.Errorf("username = %q, want %q", name, me.Username)
	}
	if strings.HasPrefix(home, "/var/root") || strings.HasPrefix(home, "/private/var/root") {
		t.Errorf("home = %q: a sudo scan must classify the invoking user's home, not root's", home)
	}
}

// TestInvokingHomeFallsBackToUsersDirectory covers the CGO_ENABLED=0 case: a
// directory service lookup can fail on macOS, and the answer must still be a
// usable path rather than an empty string.
func TestInvokingHomeFallsBackToUsersDirectory(t *testing.T) {
	name, home, err := InvokingHome()
	if err != nil && home == "" {
		t.Skipf("no home is resolvable in this environment: %v", err)
	}
	if home == "" {
		t.Fatal("InvokingHome returned no home and no error")
	}
	if name != "" && !strings.Contains(home, "/") {
		t.Errorf("home = %q, want a path", home)
	}
}
