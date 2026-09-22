package mac

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// InvokingUser returns the uid and gid that files created by this process
// should belong to. Under sudo that is the user who ran sudo, taken from
// SUDO_UID/SUDO_GID, so the scan cache does not end up owned by root.
func InvokingUser() (uid, gid int, viaSudo bool) {
	uid, gid = os.Getuid(), os.Getgid()
	if os.Geteuid() != 0 {
		return uid, gid, false
	}
	su, errU := strconv.Atoi(os.Getenv("SUDO_UID"))
	sg, errG := strconv.Atoi(os.Getenv("SUDO_GID"))
	if errU != nil || errG != nil || su == 0 {
		return uid, gid, false
	}
	return su, sg, true
}

// UserCacheDir returns the per-user darwin cache directory
// (/var/folders/xx/…/C), the parent of the per-user cache and temp trees the
// classifier needs to name. It shells out to getconf because no syscall
// exposes the value.
func UserCacheDir() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "getconf", "DARWIN_USER_CACHE_DIR").Output()
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", errors.New("getconf DARWIN_USER_CACHE_DIR returned nothing")
	}
	return dir, nil
}
