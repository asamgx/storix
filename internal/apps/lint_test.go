package apps

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbidden matches calls that read file contents or list a directory
// directly.
//
// This package does read files: property lists, install receipts, launchd
// jobs. Every one of those reads goes through detect.Env.ReadFile, which
// lstats first and refuses a file carrying SF_DATALESS, because opening an
// evicted iCloud file asks the File Provider to download it and a disk survey
// that downloads gigabytes to measure them has done the user real harm.
//
// The rule is mechanical rather than a convention, for the same reason the
// walker's is: a convention survives until someone in a hurry adds one
// os.ReadFile, and the failure is silent.
var forbidden = regexp.MustCompile(`\bos\.(Open|OpenFile|ReadFile|Create|WriteFile|MkdirAll|Rename|Remove|Chown)\(|\bioutil\.|\bunix\.Open\(|\bos\.ReadDir\(`)

// allowed lists the file and the calls each exception covers.
var allowed = map[string]*regexp.Regexp{
	// The team id cache is the one file this package writes. Reading and
	// writing it cannot touch a dataless file: it is a few hundred bytes
	// that storix itself created, and the read lstats the path first so a
	// symlink left there is refused rather than followed.
	//
	// os.WriteFile is not among these. The write goes through os.CreateTemp
	// and a rename, because a temporary name another user can predict is a
	// name they can plant a symlink at, and this probe can be running as
	// root under sudo.
	"teamid.go": regexp.MustCompile(`\bos\.(ReadFile|MkdirAll|Rename|Remove)\(`),
}

func TestNoUnsanctionedFileAccess(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !forbidden.MatchString(line) {
				continue
			}
			if ex, ok := allowed[filepath.Base(name)]; ok && ex.MatchString(line) {
				continue
			}
			t.Errorf("%s:%d reads or writes the filesystem outside the sanctioned reader:\n\t%s",
				name, i+1, strings.TrimSpace(line))
		}
	}
}

// TestNoDirectExecution checks the other half of the read-only posture: every
// command goes through the probe runner, so that a scan's commands are
// recorded, replayable and visible in the why panel and in doctor.
func TestNoDirectExecution(t *testing.T) {
	t.Parallel()
	banned := regexp.MustCompile(`\bexec\.(Command|CommandContext)\(`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if banned.MatchString(line) {
				t.Errorf("%s:%d runs a command outside the probe runner:\n\t%s",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestNoInstallingCommands is a blunt guard on the one thing this package
// must never do. Every command it runs is named here, and a command that
// installs, updates or downloads anything is not among them.
func TestNoInstallingCommands(t *testing.T) {
	t.Parallel()
	dangerous := []string{
		`"brew", "install"`, `"brew", "update"`, `"brew", "upgrade"`, `"brew", "cleanup"`,
		`"xcrun"`, `"xcodebuild"`, `"simctl"`, `"softwareupdate"`, `"curl"`, `"pip"`,
		`"rm"`, `"mv"`, `"ditto"`,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, bad := range dangerous {
			if strings.Contains(text, bad) {
				t.Errorf("%s names %s, which can modify the machine", name, bad)
			}
		}
	}
}
