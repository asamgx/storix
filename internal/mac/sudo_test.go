package mac

import (
	"os"
	"strings"
	"testing"
)

func TestInvokingUser(t *testing.T) {
	uid, gid, viaSudo := InvokingUser()
	if os.Geteuid() != 0 {
		if uid != os.Getuid() || gid != os.Getgid() {
			t.Errorf("InvokingUser() = %d, %d; want the current %d, %d", uid, gid, os.Getuid(), os.Getgid())
		}
		if viaSudo {
			t.Error("viaSudo = true without root privileges")
		}
		return
	}
	t.Setenv("SUDO_UID", "501")
	t.Setenv("SUDO_GID", "20")
	uid, gid, viaSudo = InvokingUser()
	if uid != 501 || gid != 20 || !viaSudo {
		t.Errorf("InvokingUser() under sudo = %d, %d, %v; want 501, 20, true", uid, gid, viaSudo)
	}
}

func TestUserCacheDir(t *testing.T) {
	dir, err := UserCacheDir()
	if err != nil {
		t.Skipf("getconf unavailable: %v", err)
	}
	if !strings.HasPrefix(dir, "/var/folders/") && !strings.HasPrefix(dir, "/private/var/folders/") {
		t.Errorf("UserCacheDir() = %q, want a /var/folders path", dir)
	}
	if strings.ContainsAny(dir, "\n\r") {
		t.Errorf("UserCacheDir() = %q, want trimmed output", dir)
	}
}
