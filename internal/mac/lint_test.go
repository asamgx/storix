package mac

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbidden matches calls that read file contents. A scan must never
// materialize an evicted iCloud file, and the only defence that works in
// every build is not opening files at all.
var forbidden = regexp.MustCompile(`\bos\.(Open|OpenFile|ReadFile|Create|WriteFile)\(|\bioutil\.|\bunix\.Open\(|\bos\.ReadDir\(`)

// allowed lists the file and call each exception covers.
var allowed = map[string]*regexp.Regexp{
	// The Full Disk Access probe must list a protected directory; that is
	// what it tests. It never opens a file.
	"tcc.go": regexp.MustCompile(`\bos\.ReadDir\(`),
	// The firmlink table is a few hundred bytes on the sealed, read-only
	// system volume, so it can never be a dataless file.
	"firmlinks.go": regexp.MustCompile(`\bos\.ReadFile\(firmlinksFile\)`),
}

func TestNoFileReads(t *testing.T) {
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
			t.Errorf("%s:%d reads file contents, which can materialize a dataless file:\n\t%s",
				name, i+1, strings.TrimSpace(line))
		}
	}
}
