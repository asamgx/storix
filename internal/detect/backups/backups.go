// Package backups detects backups of things that are not on this disk:
// iPhones and iPads backed up over Finder, and builds Xcode archived.
//
// A backup is the hardest thing on a disk survey to report honestly. It is
// large, it looks like a cache, and it is the only copy of something the user
// cannot get back — an iPhone backup is a phone's whole life and an Xcode
// archive is the only way to symbolicate a crash from a version that shipped
// a year ago. So everything here is user data, and the detector's job is not
// to find bytes to delete but to put a name on the ones a reader is about to
// wonder about: which device is this forty gigabytes, and when was it last
// written to.
//
// That name comes out of the backup's own Info.plist, which needs Full Disk
// Access. Without it the directory listing fails with EPERM, the detector
// degrades, and the claim still lands in the Backups bucket with the device
// identifier for a label — because "40 GB of iOS backups you cannot read
// without granting access" is a better answer than silence.
package backups

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/walk"

	"howett.net/plist"
)

// Name is the detector's identifier.
const Name = "backups"

func init() { detect.Register(280, New()) }

// mobileSync is where Finder and iTunes keep device backups, one directory
// per device, named by its identifier.
const mobileSync = "Library/Application Support/MobileSync/Backup"

// maxDevices bounds how many backup directories are described. A machine with
// more than this many backed-up devices does not exist; the cap is here so
// that a directory that is not what it claims to be cannot turn the probe
// into a plist-parsing marathon.
const maxDevices = 64

// Detector finds device backups and archived builds.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Device is one backed-up device.
type Device struct {
	// ID is the directory name: the device's identifier, either a 40-hex
	// UDID or the newer dash-separated form.
	ID string `json:"id"`
	// Name is what the user called the device, from Info.plist.
	Name string `json:"name,omitempty"`
	// Product is the model, "iPhone 15 Pro", and Version its iOS release.
	Product string `json:"product,omitempty"`
	Version string `json:"version,omitempty"`
	// LastBackup is when the backup was last written, from Info.plist.
	LastBackup time.Time `json:"last_backup,omitempty"`
	// Note explains why a device has no name, when it has none.
	Note string `json:"note,omitempty"`
}

// Facts are what the probe learned.
type Facts struct {
	Devices []Device `json:"devices,omitempty"`
	// Archives is whether Xcode's archive directory exists.
	Archives bool `json:"archives,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// xcodeArchives is where Xcode keeps archived builds. It is claimed here
// rather than by the xcode detector because an archive is a backup of a
// build: docs/04 puts it in the Backups bucket, and the bucket is what
// decides which detector owns a path.
const xcodeArchives = "Library/Developer/Xcode/Archives"

// timeMachineLocal is the volume local Time Machine snapshots are mounted
// under. The snapshots themselves are counted by internal/volume, which runs
// tmutil once during the scan; this detector claims the mount point and does
// not run tmutil again.
const timeMachineLocal = "/Volumes/com.apple.TimeMachine.localsnapshots"

// Probe lists the backup directories and reads each one's Info.plist.
func (*Detector) Probe(_ context.Context, env detect.Env) (detect.Facts, error) {
	if env.Home == "" {
		return nil, detect.Missingf("no home directory to look in")
	}
	root := path.Join(env.Home, mobileSync)
	f := &Facts{Archives: env.Exists(path.Join(env.Home, xcodeArchives))}

	if !env.Exists(root) {
		if !f.Archives {
			return nil, detect.Missingf("no device backups and no Xcode archives")
		}
		return f, nil
	}
	if env.ReadDir == nil {
		return f, detect.Degradedf("no directory reader; the backups are counted but not named")
	}

	entries, err := env.ReadDir(root)
	if err != nil {
		return f, detect.Degradedf(
			"%s could not be listed (%v); reading device backups needs Full Disk Access, so the backups are counted but not named",
			mobileSync, err)
	}

	var unread []string
	for _, e := range entries {
		if !e.IsDir() || len(f.Devices) >= maxDevices {
			continue
		}
		d := Device{ID: e.Name()}
		if err := readInfo(env, path.Join(root, e.Name()), &d); err != nil {
			d.Note = err.Error()
			unread = append(unread, e.Name())
		}
		f.Devices = append(f.Devices, d)
	}

	if len(unread) > 0 {
		return f, detect.Degradedf("%d of %d backups had no readable Info.plist; reading them needs Full Disk Access",
			len(unread), len(f.Devices))
	}
	return f, nil
}

// info is the part of a backup's Info.plist worth reading. The keys have had
// these names since iOS 4 and are the ones Finder itself shows.
type info struct {
	DeviceName     string    `plist:"Device Name"`
	DisplayName    string    `plist:"Display Name"`
	ProductName    string    `plist:"Product Name"`
	ProductType    string    `plist:"Product Type"`
	ProductVersion string    `plist:"Product Version"`
	LastBackupDate time.Time `plist:"Last Backup Date"`
}

// readInfo fills in a device from its Info.plist.
//
// The file goes through the sanctioned reader, which caps its size. A backup
// of a phone with hundreds of applications has an Info.plist over that cap,
// because it embeds each application's metadata; that is a refusal rather
// than a failure, and the device keeps its identifier for a label.
func readInfo(env detect.Env, dir string, d *Device) error {
	if env.ReadFile == nil {
		return errors.New("no file reader")
	}
	data, err := env.ReadFile(path.Join(dir, "Info.plist"))
	if err != nil {
		return errors.New("its Info.plist could not be read")
	}
	var in info
	if _, err := plist.Unmarshal(data, &in); err != nil {
		return errors.New("its Info.plist could not be decoded")
	}

	d.Name = firstNonEmpty(in.DeviceName, in.DisplayName)
	d.Product = firstNonEmpty(in.ProductName, in.ProductType)
	d.Version = in.ProductVersion
	d.LastBackup = in.LastBackupDate
	return nil
}

// firstNonEmpty is the first of its arguments that has anything in it.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Classify claims the backup directories, one per device, and the archives.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}
	facts, _ := f.(*Facts)

	targets := []detect.Target{
		{
			Path: path.Join(home, mobileSync), Category: "Device backups", Owner: "MobileSync",
			OwnerKeys: []string{"macos"}, Reclaim: classify.UserData, Kind: "data", Name: "Device backups",
			Explain: "backups of iPhones and iPads made over Finder; each one is the only copy of a device's data",
		},
		{
			Path: path.Join(home, xcodeArchives), Category: "Build archives", Owner: "Xcode",
			OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: classify.UserData,
			Kind: "data", Name: "Xcode archives",
			Explain: "archived builds with their debug symbols; without them a crash report from a shipped version cannot be symbolicated",
		},
		{
			Path: timeMachineLocal, Category: "Time Machine", Owner: "Time Machine",
			OwnerKeys: []string{"macos"}, Reclaim: classify.ToolManaged, Kind: "data",
			Name:    "Local snapshots",
			Explain: "where local Time Machine snapshots are mounted; `tmutil thinlocalsnapshots` reclaims them",
		},
	}
	targets = append(targets, deviceTargets(home, facts)...)
	for i := range targets {
		targets[i].Bucket = classify.BucketBackups
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	sort.SliceStable(tools, func(i, j int) bool { return tools[i].Bytes > tools[j].Bytes })
	return claims, detect.Summary{Tools: tools}
}

// deviceTargets is one claim per backed-up device, labelled with whatever the
// probe could read about it.
func deviceTargets(home string, f *Facts) []detect.Target {
	if f == nil {
		return nil
	}
	out := make([]detect.Target, 0, len(f.Devices))
	for _, d := range f.Devices {
		out = append(out, detect.Target{
			Path: path.Join(home, mobileSync, d.ID), Category: "Device backup",
			// A phone is not an application, a cask or a command, so
			// there is no key in the join vocabulary that names one.
			// The label carries the identity and nothing joins on it.
			Owner:   deviceLabel(d),
			Reclaim: classify.UserData, Kind: "data", Name: deviceLabel(d),
			Note:     backupNote(d),
			Explain:  "the backup of " + deviceLabel(d) + "; Finder can delete it, and nothing else has this data",
			Evidence: deviceEvidence(d),
		})
	}
	return out
}

// deviceLabel is what to call a device: its name when the plist could be
// read, its identifier otherwise. An unnamed device is still a device.
func deviceLabel(d Device) string {
	if d.Name != "" {
		return d.Name
	}
	return d.ID
}

// backupNote is the model and the date, which is what a reader deciding
// whether a backup is still wanted actually needs.
func backupNote(d Device) string {
	switch {
	case d.Product != "" && !d.LastBackup.IsZero():
		return fmt.Sprintf("%s, last backed up %s", d.Product, d.LastBackup.Format("2006-01-02"))
	case !d.LastBackup.IsZero():
		return "last backed up " + d.LastBackup.Format("2006-01-02")
	case d.Product != "":
		return d.Product
	case d.Note != "":
		return d.Note
	}
	return ""
}

// deviceEvidence are the why-panel lines for one device.
func deviceEvidence(d Device) []string {
	out := []string{"MobileSync backup directory " + d.ID}
	if d.Name != "" {
		line := "Info.plist names it " + d.Name
		if d.Product != "" {
			line += " (" + d.Product
			if d.Version != "" {
				line += ", iOS " + d.Version
			}
			line += ")"
		}
		out = append(out, line)
	}
	if !d.LastBackup.IsZero() {
		out = append(out, "last backed up "+d.LastBackup.Format(time.RFC3339))
	}
	if d.Note != "" {
		out = append(out, d.Note)
	}
	return out
}
