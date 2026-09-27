package apps

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
)

// dataVolumeHome is the home a real scan hands the detectors: scan.detectEnv
// passes mac.ScanPath(home), which on a machine with a sealed system volume is
// the data volume's own path to it.
const dataVolumeHome = "/System/Volumes/Data/Users/x"

// dataVolumeProber is a prober rooted the way a real scan roots it, with a
// stat that answers from a fixed set of paths because none of them exist on
// the machine running the test.
func dataVolumeProber(records map[string]probe.Result, present ...string) *prober {
	there := make(map[string]bool, len(present))
	for _, p := range present {
		there[p] = true
	}
	env := detect.Env{
		Runner: &probe.Replay{Records: records},
		Home:   dataVolumeHome,
		ReadFile: func(p string) ([]byte, error) {
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
		},
		ReadDir: func(p string) ([]os.DirEntry, error) {
			return nil, &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrNotExist}
		},
		Stat: func(p string) (detect.FileInfo, error) {
			if there[p] {
				return detect.FileInfo{IsDir: true}, nil
			}
			return detect.FileInfo{}, &fs.PathError{Op: "lstat", Path: p, Err: fs.ErrNotExist}
		},
	}
	return &prober{
		d: &Detector{}, env: env, f: &Facts{TeamIDs: map[string]string{}},
		paths: Paths{Root: volumeRootOf(dataVolumeHome), Home: dataVolumeHome, User: path.Base(dataVolumeHome)},
	}
}

// TestSpotlightOnTheDataVolumeKeepsItsOwnHits is the Spotlight backstop on a
// real scan. The probe is rooted at /System/Volumes/Data while every fact it
// records is in display form, and asking whether a display path lay under the
// data volume's root answered no for every hit — so Spotlight never found
// anything, and an application in ~/Downloads looked uninstalled.
func TestSpotlightOnTheDataVolumeKeepsItsOwnHits(t *testing.T) {
	t.Parallel()
	scanForm := dataVolumeHome + "/Downloads/Foo.app"
	displayForm := "/Users/x/Desktop/Bar.app"
	p := dataVolumeProber(map[string]probe.Result{
		"mdfind kMDItemContentType == 'com.apple.application-bundle'": {Stdout: strings.Join([]string{
			scanForm,
			displayForm,
			"/Volumes/Time Machine/Applications/Backed Up.app",
		}, "\n") + "\n"},
	}, scanForm, displayForm, "/Volumes/Time Machine/Applications/Backed Up.app")

	p.spotlight(context.Background(), map[string]bool{})

	var got []string
	for _, b := range p.f.Spotlight {
		got = append(got, b.Path)
	}
	want := []string{"/Users/x/Downloads/Foo.app", displayForm}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("spotlight bundles = %v, want %v", got, want)
	}
}

// TestUserApplicationsOnTheDataVolumeAreSigned is the same coordinate mismatch
// in the team id pass. A bundle in ~/Applications is recorded as
// "/Users/x/Applications/Foo.app", and comparing that with a home of
// "/System/Volumes/Data/Users/x" classified it as a nested copy, which the
// codesign pass never reads.
func TestUserApplicationsOnTheDataVolumeAreSigned(t *testing.T) {
	t.Parallel()
	bundle := "/Users/x/Applications/OrbStack.app"
	p := dataVolumeProber(map[string]probe.Result{
		"codesign -dv --verbose=4 " + bundle: {Stderr: "Identifier=dev.kdrag0n.MacVirt\nTeamIdentifier=HUAQ24HBR6\n"},
	})
	p.d.TeamCachePath = filepath.Join(t.TempDir(), "teamids.json")
	p.f.GroupContainerNames = []string{"HUAQ24HBR6.dev.orbstack"}
	p.f.AppDirBundles = []BundleInfo{{Path: bundle, ID: "dev.kdrag0n.MacVirt", Version: "1.0"}}

	p.teamIDs(context.Background())

	if p.f.CodesignCalls != 1 {
		t.Errorf("codesign ran %d times, want once for the bundle in ~/Applications", p.f.CodesignCalls)
	}
	if got := p.f.TeamIDs["dev.kdrag0n.MacVirt@1.0"]; got != "HUAQ24HBR6" {
		t.Errorf("team id = %q, want HUAQ24HBR6; degradations: %s", got, joinDegradations(p.f))
	}
}
