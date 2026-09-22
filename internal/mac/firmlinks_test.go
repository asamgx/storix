package mac

import (
	"os"
	"reflect"
	"testing"
)

func TestParseFirmlinks(t *testing.T) {
	in := []byte("/Applications\tApplications\n" +
		"/Users\tUsers\n" +
		"/usr/local\tusr/local\n" +
		"\n" +
		"malformed line\n" +
		"/Weird\tsomewhere/else\n" + // two sides disagree: dropped
		"relative\trelative\n")
	got := parseFirmlinks(in)
	want := []string{"/Applications", "/Users", "/usr/local"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseFirmlinks() = %v, want %v", got, want)
	}
	if got := parseFirmlinks(nil); got != nil {
		t.Errorf("parseFirmlinks(nil) = %v, want nil", got)
	}
}

func TestDataVolumePath(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/Users/andrewsam/OrbStack", DataRoot + "/Users/andrewsam/OrbStack", true},
		{"/Users", DataRoot + "/Users", true},
		{"/private/var/folders", DataRoot + "/private/var/folders", true},
		{DataRoot, DataRoot, true},
		{DataRoot + "/Users", DataRoot + "/Users", true},
		{"/", "", false},
		{"/dev", "", false},
		{"/System/Volumes/VM", "", false},
		{"/System/Volumes", "", false},
		{"/UsersX", "", false},
		{"relative/path", "", false},
	}
	for _, c := range cases {
		got, ok := DataVolumePath(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("DataVolumePath(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFirmlinksLongestFirst(t *testing.T) {
	fl := firmlinks()
	if len(fl) == 0 {
		t.Fatal("no firmlinks")
	}
	for i := 1; i < len(fl); i++ {
		if len(fl[i-1]) < len(fl[i]) {
			t.Fatalf("not sorted longest first: %q before %q", fl[i-1], fl[i])
		}
	}
	// The system table, when present, must at least cover /Users.
	if _, err := os.Stat(firmlinksFile); err == nil {
		found := false
		for _, f := range fl {
			if f == "/Users" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no /Users entry: %v", firmlinksFile, fl)
		}
	}
}
