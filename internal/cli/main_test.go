package cli

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the scan cache at a scratch home for the whole package.
//
// Every scan the CLI runs is cached, and the store is resolved from the home
// directory, so without this a test run would leave fixture scans in the
// developer's own ~/Library/Application Support/storix/scans. t.Setenv cannot
// be used here, and a per-test call would be forgotten by the next test that
// scans something.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "storix-cli-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cli tests: no scratch home:", err)
		os.Exit(1)
	}
	if err := os.Setenv("HOME", dir); err != nil {
		fmt.Fprintln(os.Stderr, "cli tests: HOME:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
