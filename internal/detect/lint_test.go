package detect

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbidden matches calls that read file contents or list a directory. A scan
// must never materialize an evicted iCloud file, and the only defence that
// works in every build is not opening files at all. internal/walk and
// internal/mac enforce the same rule over their own directories; this test
// extends it over every detector.
var forbidden = regexp.MustCompile(
	`\bos\.(Open|OpenFile|ReadFile|Create|WriteFile|ReadDir)\(|\bioutil\.|\bunix\.Open(at)?\(|\bos\.DirFS\(`)

// allowed lists the file and the call each exception covers.
//
// There is one. Detectors do have to read a handful of small files — a
// plist, an INSTALL_RECEIPT.json, a version marker — and readfile.go is the
// single place where that happens, behind the lstat, the dataless check, the
// size cap and the O_NOFOLLOW open that make it safe. Everything else goes
// through Env.ReadFile and inherits those guarantees.
var allowed = map[string]*regexp.Regexp{
	"readfile.go": regexp.MustCompile(`\bunix\.Open\(path, unix\.O_RDONLY\|unix\.O_NOFOLLOW`),
}

// skipDirs are directories the rule does not cover: recorded fixtures, and
// the test-support package, which builds fixture trees on a temporary disk
// and is never linked into the binary a user runs.
var skipDirs = map[string]bool{"testdata": true, "detecttest": true}

func TestNoFileReads(t *testing.T) {
	root := "."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // the linter reads its own source tree
		if err != nil {
			return err
		}
		ex := allowed[name]
		for i, line := range strings.Split(string(data), "\n") {
			if !forbidden.MatchString(line) {
				continue
			}
			if ex != nil && ex.MatchString(line) {
				continue
			}
			t.Errorf("%s:%d reads file contents directly; detectors must use Env.ReadFile:\n\t%s",
				path, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEveryDetectorPackageIsCovered guards the guard: the walk above is
// recursive, so a detector added in a new subdirectory is linted without
// anyone remembering to add it. This checks that the recursion is really
// reaching them, which a refactor to a flat os.ReadDir would silently break.
func TestEveryDetectorPackageIsCovered(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if e.IsDir() && !skipDirs[e.Name()] {
			found++
		}
	}
	if found < 6 {
		t.Errorf("only %d detector packages were found; the containers milestone ships six", found)
	}
}
