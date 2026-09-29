package backups_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/backups"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/probe"
)

const home = detecttest.Home

// mobileSync is where Finder keeps device backups.
const mobileSync = "/Library/Application Support/MobileSync/Backup"

// infoPlist is what Finder writes beside a device's backup. It is the XML
// form rather than the binary one because both are read by the same decoder
// and this one can be read by a person looking at the fixture.
const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Device Name</key>
	<string>Andrew's iPhone</string>
	<key>Display Name</key>
	<string>Andrew's iPhone</string>
	<key>Product Name</key>
	<string>iPhone 15 Pro</string>
	<key>Product Type</key>
	<string>iPhone16,1</string>
	<key>Product Version</key>
	<string>18.2</string>
	<key>Last Backup Date</key>
	<date>2026-02-14T09:30:00Z</date>
</dict>
</plist>
`

const (
	namedDevice   = "00008130-000A1B2C3D4E5F6G"
	unnamedDevice = "11112222-333344445555666677778888"
)

// corpus is a machine with two device backups and a set of Xcode archives.
func corpus() map[string]int64 {
	return map[string]int64{
		home + mobileSync + "/" + namedDevice + "/Info.plist":                 2_000,
		home + mobileSync + "/" + namedDevice + "/Manifest.db":                900_000,
		home + mobileSync + "/" + unnamedDevice + "/Manifest.db":              400_000,
		home + "/Library/Developer/Xcode/Archives/2026-01-01/App.xcarchive/x": 500_000,
	}
}

// writePlist puts the real Info.plist into the fixture, over the placeholder
// the tree was built from. The tree only ever recorded its size, so replacing
// the contents afterwards changes nothing it holds.
func writePlist(t *testing.T, f *detecttest.Fixture, device, content string) {
	t.Helper()
	p := filepath.Join(f.Real, "Library/Application Support/MobileSync/Backup", device, "Info.plist")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write Info.plist: %v", err)
	}
}

// TestNothingToBackUp: no device backups and no archives is Missing.
func TestNothingToBackUp(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, backups.New(), f.Env(t, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	claims, sum := backups.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims with nothing backed up", len(claims))
	}
}

// TestDeviceIsNamedFromItsPlist is what the probe is for: the directory name
// is an identifier nobody recognises, and the plist beside it is the only
// place the device's own name exists.
func TestDeviceIsNamedFromItsPlist(t *testing.T) {
	f := detecttest.Build(t, corpus())
	writePlist(t, f, namedDevice, infoPlist)

	facts, err := detecttest.Probe(t, backups.New(), f.Env(t, "testdata/this-machine.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		// One of the two devices has no plist, so a degradation naming
		// it is the expected outcome.
		t.Fatalf("Probe error = %v, want a degradation naming the unreadable backup", err)
	}
	got := facts.(*backups.Facts)
	if len(got.Devices) != 2 {
		t.Fatalf("devices = %+v, want two", got.Devices)
	}
	var named, unnamed backups.Device
	for _, d := range got.Devices {
		if d.ID == namedDevice {
			named = d
		} else {
			unnamed = d
		}
	}
	if named.Name != "Andrew's iPhone" {
		t.Errorf("name = %q, want the one in the plist", named.Name)
	}
	if named.Product != "iPhone 15 Pro" || named.Version != "18.2" {
		t.Errorf("model = %q / %q", named.Product, named.Version)
	}
	if named.LastBackup.IsZero() || named.LastBackup.Format("2006-01-02") != "2026-02-14" {
		t.Errorf("last backup = %s", named.LastBackup)
	}
	if unnamed.Name != "" || unnamed.Note == "" {
		t.Errorf("a backup with no readable plist should be unnamed and say why: %+v", unnamed)
	}

	claims, sum := backups.New().Classify(f.Tree, facts, f.Context)
	c, ok := detecttest.ClaimAt(claims, home+mobileSync+"/"+namedDevice)
	if !ok {
		t.Fatal("the named device's backup was not claimed")
	}
	if c.Owner != "Andrew's iPhone" {
		t.Errorf("owner = %q, want the device name", c.Owner)
	}
	if c.Bucket != classify.BucketBackups {
		t.Errorf("bucket = %s, want backups", c.Bucket)
	}
	if c.Reclaim != classify.UserData {
		t.Errorf("reclaim = %s; a device backup is the only copy of a phone", c.Reclaim)
	}
	if !strings.Contains(strings.Join(c.Evidence, "\n"), "iPhone 15 Pro") {
		t.Errorf("the evidence does not name the model: %v", c.Evidence)
	}

	// A device with no readable plist keeps its identifier for a label
	// rather than disappearing.
	u, ok := detecttest.ClaimAt(claims, home+mobileSync+"/"+unnamedDevice)
	if !ok {
		t.Fatal("the unnamed device's backup was not claimed")
	}
	if u.Owner != unnamedDevice {
		t.Errorf("owner = %q, want the identifier", u.Owner)
	}
	if _, ok := detecttest.Tool(sum, home+mobileSync+"/"+namedDevice); !ok {
		t.Error("the backup has no summary row")
	}
}

// TestUnreadableBackupDirectory is what happens without Full Disk Access on a
// real machine, which is what happened on this one: the listing fails with
// EPERM and the answer has to be the bytes plus an explanation.
func TestUnreadableBackupDirectory(t *testing.T) {
	f := detecttest.Build(t, corpus())
	env := f.Env(t, "testdata/this-machine.json")
	env.ReadDir = func(string) ([]os.DirEntry, error) { return nil, os.ErrPermission }

	facts, err := detecttest.Probe(t, backups.New(), env)
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	if !strings.Contains(err.Error(), "Full Disk Access") {
		t.Errorf("the reason does not say what would fix it: %v", err)
	}
	claims, _ := backups.New().Classify(f.Tree, facts, f.Context)
	c, ok := detecttest.ClaimAt(claims, home+mobileSync)
	if !ok {
		t.Fatal("the backup directory stopped being claimed because it could not be listed")
	}
	if c.Bucket != classify.BucketBackups {
		t.Errorf("bucket = %s, want backups", c.Bucket)
	}
}

// TestArchivesBelongHere: an Xcode archive is a backup of a build, so it is
// claimed by this detector and lands in the Backups bucket, not in Developer
// with the rest of Xcode.
func TestArchivesBelongHere(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, _ := detecttest.Probe(t, backups.New(), f.Env(t, "testdata/this-machine.json"))
	claims, _ := backups.New().Classify(f.Tree, facts, f.Context)

	c, ok := detecttest.ClaimAt(claims, home+"/Library/Developer/Xcode/Archives")
	if !ok {
		t.Fatal("the Xcode archives were not claimed")
	}
	if c.Bucket != classify.BucketBackups {
		t.Errorf("bucket = %s, want backups", c.Bucket)
	}
	if c.Reclaim != classify.UserData {
		t.Errorf("reclaim = %s; an archive is how an old crash gets symbolicated", c.Reclaim)
	}
	if !detecttest.HasKey(c, "app:com.apple.dt.Xcode") {
		t.Errorf("keys = %v", c.OwnerKeys)
	}
}

// TestSnapshotsAreNotProbedAgain: local Time Machine snapshots are counted
// once by internal/volume during the scan, so this detector claims the mount
// point and runs nothing.
func TestSnapshotsAreNotProbedAgain(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{
		"/Volumes/com.apple.TimeMachine.localsnapshots/x": 1_000,
		home + "/Library/Developer/Xcode/Archives/x":      1_000,
	})
	env := f.Env(t, "testdata/missing.json")
	rec := recordingRunner{}
	env.Runner = &rec

	facts, err := detecttest.Probe(t, backups.New(), env)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(rec.cmds) != 0 {
		t.Errorf("the detector ran %v; tmutil is the scan's job, not this detector's", rec.cmds)
	}

	claims, _ := backups.New().Classify(f.Tree, facts, f.Context)
	c, ok := detecttest.ClaimAt(claims, "/Volumes/com.apple.TimeMachine.localsnapshots")
	if !ok {
		t.Fatal("the snapshot mount point was not claimed")
	}
	if c.Reclaim != classify.ToolManaged {
		t.Errorf("reclaim = %s, want tool-managed: tmutil thins them", c.Reclaim)
	}
}

// TestMalformedPlist: a plist that is not a plist costs the device its name
// and nothing else.
func TestMalformedPlist(t *testing.T) {
	f := detecttest.Build(t, corpus())
	writePlist(t, f, namedDevice, "this is not a plist at all")

	facts, err := detecttest.Probe(t, backups.New(), f.Env(t, "testdata/this-machine.json"))
	if !errors.Is(err, detect.ErrDegraded) {
		t.Fatalf("Probe error = %v, want ErrDegraded", err)
	}
	claims, _ := backups.New().Classify(f.Tree, facts, f.Context)
	c, ok := detecttest.ClaimAt(claims, home+mobileSync+"/"+namedDevice)
	if !ok {
		t.Fatal("the backup was dropped because its plist was unreadable")
	}
	if c.Owner != namedDevice {
		t.Errorf("owner = %q, want the identifier as a fallback label", c.Owner)
	}
}

// recordingRunner remembers what it was asked to run and runs nothing.
type recordingRunner struct{ cmds []string }

func (r *recordingRunner) Run(_ context.Context, c probe.Cmd) probe.Result {
	r.cmds = append(r.cmds, c.Key())
	return probe.Result{Missing: true, ErrText: "nothing should be run here"}
}
