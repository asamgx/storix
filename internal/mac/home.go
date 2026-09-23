package mac

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// InvokingHome returns the name and home directory of the user the scan is
// being run for.
//
// Under sudo that is the user who ran sudo rather than root, for the same
// reason the cache is written with their ownership: a scan run with sudo is
// still a scan of someone's machine, and treating /var/root as "the home
// folder" would put every one of their caches and application data in the
// unclassified bucket. The home is returned as an absolute path, not a
// display path; the caller converts if it wants one.
//
// The lookup goes by uid first, because that is the authoritative answer and
// survives a SUDO_USER that has been tampered with. It falls back to
// SUDO_USER, then to $HOME, then to /Users/<name>, so a directory service
// that is unavailable in a CGO_ENABLED=0 build still yields the right path on
// a machine with ordinary home directories. The error is returned alongside a
// best-effort answer rather than instead of one: a caller that only wants a
// home can ignore it, and one reporting on the environment can print it.
func InvokingHome() (username, home string, err error) {
	uid, _, viaSudo := InvokingUser()

	if u, lookupErr := user.LookupId(strconv.Itoa(uid)); lookupErr == nil {
		if u.HomeDir != "" {
			return u.Username, filepath.Clean(u.HomeDir), nil
		}
		username = u.Username
	} else {
		err = lookupErr
	}

	if name := os.Getenv("SUDO_USER"); viaSudo && name != "" {
		if username == "" {
			username = name
		}
		if u, lookupErr := user.Lookup(name); lookupErr == nil && u.HomeDir != "" {
			return u.Username, filepath.Clean(u.HomeDir), nil
		}
		return username, "/Users/" + name, err
	}

	if !viaSudo {
		if h, homeErr := os.UserHomeDir(); homeErr == nil && h != "" {
			if username == "" {
				username = filepath.Base(h)
			}
			return username, filepath.Clean(h), err
		}
	}

	if username != "" {
		return username, "/Users/" + username, err
	}
	if err == nil {
		err = errors.New("mac: the invoking user has no home directory")
	}
	return "", "", err
}
