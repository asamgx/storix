package walk

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// forbidden matches every way this package could end up opening a file.
// Opening a dataless file downloads it; opening any file changes atime and
// defeats the promise that a scan is free. os.ReadDir is allowed because it
// opens the directory, not its contents, and an O_DIRECTORY open is allowed
// for the future bulk reader.
var forbidden = regexp.MustCompile(`\bos\.(Open|OpenFile|ReadFile|Create)\(|\bioutil\.|\bunix\.Open\(`)

// TestNoFileOpens fails when a non-test file in this package opens a file.
func TestNoFileOpens(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := os.Open(name) //nolint:gosec // test code reads package sources
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		for line := 1; sc.Scan(); line++ {
			text := sc.Text()
			if !forbidden.MatchString(text) {
				continue
			}
			if strings.Contains(text, "O_DIRECTORY") {
				continue
			}
			t.Errorf("%s:%s opens a file: %s", name, strconv.Itoa(line), strings.TrimSpace(text))
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	if checked == 0 {
		t.Fatal("no source files checked")
	}
}
