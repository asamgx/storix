// Package xcode detects Xcode's caches, device support and simulators.
//
// # The gate
//
// This detector has one rule that matters more than everything else in it:
// `xcrun simctl` is never run unless `xcodebuild -checkFirstLaunchStatus`
// has exited zero first, under the same DEVELOPER_DIR.
//
// That is not caution. During the planning of this milestone, running
// `xcrun simctl list devices` on a machine whose Xcode had never been opened
// installed the CoreSimulator components — several gigabytes written to the
// disk by a program whose entire promise is that it only measures. simctl
// does not ask and does not warn; it treats the missing components as
// something to fix rather than something to report. `-checkFirstLaunchStatus`
// is the one command that asks the question without answering it: it exits
// zero when the components are already installed and non-zero when they are
// not, and it installs nothing either way.
//
// So the gate is mandatory, its verdict is printed, and a machine that fails
// it gets Degraded plus the path rules — which is nearly all of the bytes
// anyway, because DerivedData and the device support directories are found by
// walking, not by asking. Decision D35.
//
// # What simctl adds
//
// Only one thing the tree cannot supply: which simulator devices are
// *unavailable*, meaning their runtime has been removed and the directory
// under CoreSimulator/Devices is dead weight. Everything else is a directory
// with a name.
package xcode

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "xcode"

func init() { detect.Register(200, New()) }

// defaultApp is where Xcode installs itself. When `xcode-select -p` points at
// the command line tools instead — which is the ordinary state on a machine
// that has both — this is where the application is looked for.
const defaultApp = "/Applications/Xcode.app"

// developerSuffix is what a selected directory ends in when it belongs to an
// Xcode application rather than to the command line tools.
const developerSuffix = "/Contents/Developer"

// gateFailed is the reason a machine that has Xcode but has never opened it
// gets. It is worded for a person reading the detectors table, because that
// is where it is shown.
const gateFailed = "Xcode first-launch components not installed; simctl skipped (it would trigger an install)"

// xcodeKeys are the identifiers Xcode's bytes join on.
var xcodeKeys = []string{"app:com.apple.dt.Xcode"}

// Detector finds Xcode's directories.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Unverified implements detect.Unverified.
//
// The gate itself is verified: `xcode-select -p` and
// `xcodebuild -checkFirstLaunchStatus` were both run on the machine this was
// written against, and their outputs are the recorded fixture. The simctl
// half is not, because the gate failed there and running simctl anyway is the
// thing this detector exists to prevent. One bit cannot say "half verified",
// so it says the honest half.
func (*Detector) Unverified() bool { return true }

// Device is one simulator device.
type Device struct {
	UDID string `json:"udid"`
	Name string `json:"name,omitempty"`
	// Runtime is the runtime identifier the device belongs to.
	Runtime string `json:"runtime,omitempty"`
	// Available is false when the device's runtime is gone, which makes
	// its directory recoverable space and nothing else.
	Available bool `json:"available"`
	// Reason is simctl's explanation for an unavailable device.
	Reason string `json:"reason,omitempty"`
}

// Runtime is one installed simulator runtime.
type Runtime struct {
	Identifier string `json:"identifier"`
	Version    string `json:"version,omitempty"`
	Build      string `json:"build,omitempty"`
	// Path is where the runtime's image lives, when simctl names one.
	Path string `json:"path,omitempty"`
}

// Facts are what the probe learned.
type Facts struct {
	// Selected is what `xcode-select -p` printed.
	Selected string `json:"selected,omitempty"`
	// App is the Xcode application the gate ran against, empty when none
	// was found.
	App string `json:"app,omitempty"`
	// Developer is the DEVELOPER_DIR the gate and simctl were given.
	Developer string `json:"developer,omitempty"`
	// GateRun is whether the gate command was executed at all.
	GateRun bool `json:"gate_run"`
	// GatePassed is whether `xcodebuild -checkFirstLaunchStatus` exited
	// zero. simctl runs if and only if this is true.
	GatePassed bool `json:"gate_passed"`
	// GateReason is what happened, verbatim enough to print.
	GateReason string    `json:"gate_reason,omitempty"`
	Devices    []Device  `json:"devices,omitempty"`
	Runtimes   []Runtime `json:"runtimes,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// Verdict is the one line `storix doctor` and the detectors table print about
// the gate.
func (f *Facts) Verdict() string {
	switch {
	case f == nil:
		return "xcode did not run"
	case f.App == "":
		return "no Xcode application found; command line tools only"
	case !f.GateRun:
		return "gate not run: " + f.GateReason
	case f.GatePassed:
		return "gate passed: first-launch components are installed, simctl was queried"
	default:
		return "gate failed: " + gateFailed
	}
}

// Probe finds Xcode, runs the gate, and only then asks simctl anything.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	f := &Facts{}

	if env.Has("xcode-select") {
		res := env.Runner.Run(ctx, probe.Cmd{Name: "xcode-select", Args: []string{"-p"}})
		if res.OK() {
			f.Selected = strings.TrimSpace(res.Stdout)
		}
	}

	f.App = locateApp(env, f.Selected)
	if f.App == "" && f.Selected == "" && !env.Exists("/Library/Developer") {
		return nil, detect.Missingf("no Xcode, no command line tools and no /Library/Developer")
	}
	if f.App == "" {
		return f, detect.Degradedf("no Xcode application found; the command line tools' paths only")
	}
	f.Developer = f.App + developerSuffix

	if !env.Has("xcodebuild") {
		f.GateReason = "xcodebuild is not on the path, so the first-launch status could not be checked"
		return f, detect.Degradedf("%s; simctl skipped (it would trigger an install)", f.GateReason)
	}

	// The gate. Nothing below this point may run without it.
	f.GateRun = true
	gate := env.Runner.Run(ctx, probe.Cmd{
		Name: "xcodebuild", Args: []string{"-checkFirstLaunchStatus"},
		Env: []string{"DEVELOPER_DIR=" + f.Developer},
	})
	f.GatePassed = gate.OK()
	if !f.GatePassed {
		f.GateReason = gate.Reason()
		return f, detect.Degradedf("%s", gateFailed)
	}
	f.GateReason = "first-launch components are installed"

	var degraded []string
	devices, err := listDevices(ctx, env, f.Developer)
	if err != nil {
		degraded = append(degraded, err.Error())
	} else {
		f.Devices = devices
	}
	runtimes, err := listRuntimes(ctx, env, f.Developer)
	if err != nil {
		degraded = append(degraded, err.Error())
	} else {
		f.Runtimes = runtimes
	}
	if len(degraded) > 0 {
		return f, detect.Degradedf("%s", strings.Join(degraded, "; "))
	}
	return f, nil
}

// locateApp finds the Xcode application, preferring the selected developer
// directory when it belongs to one.
//
// The selected directory is the authority when it names an Xcode: a machine
// with Xcode 16 and an Xcode 26 beta has two, and the one `xcode-select`
// points at is the one whose simulators are in use. When it names the command
// line tools instead — which is the state on the machine this was written
// against — the default install location is the only other place to look.
func locateApp(env detect.Env, selected string) string {
	if strings.HasSuffix(selected, developerSuffix) {
		app := strings.TrimSuffix(selected, developerSuffix)
		if strings.HasSuffix(app, ".app") && env.Exists(app) {
			return app
		}
	}
	if env.Exists(defaultApp) {
		return defaultApp
	}
	return ""
}

// simctl runs one simctl subcommand under the gated developer directory.
func simctl(ctx context.Context, env detect.Env, developer string, args ...string) probe.Result {
	return env.Runner.Run(ctx, probe.Cmd{
		Name: "xcrun", Args: append([]string{"simctl"}, args...),
		Env: []string{"DEVELOPER_DIR=" + developer},
	})
}

// listDevices asks simctl which simulator devices exist and which of them
// have lost their runtime.
func listDevices(ctx context.Context, env detect.Env, developer string) ([]Device, error) {
	if !env.Has("xcrun") {
		return nil, errNoXcrun
	}
	res := simctl(ctx, env, developer, "list", "devices", "-j")
	if !res.OK() {
		return nil, wrap("simctl list devices", res.Reason())
	}
	return parseDevices(res.Stdout)
}

// listRuntimes asks simctl which runtime images are installed.
func listRuntimes(ctx context.Context, env detect.Env, developer string) ([]Runtime, error) {
	if !env.Has("xcrun") {
		return nil, errNoXcrun
	}
	res := simctl(ctx, env, developer, "runtime", "list", "-j")
	if !res.OK() {
		return nil, wrap("simctl runtime list", res.Reason())
	}
	return parseRuntimes(res.Stdout)
}

// rawDevice is one entry of `simctl list devices -j`. The availability field
// has been spelled three ways across Xcode releases — a boolean
// "isAvailable", an older "availability" string containing "(unavailable)",
// and an "availabilityError" — so all three are read and any one of them is
// enough to mark a device dead.
type rawDevice struct {
	UDID              string `json:"udid"`
	Name              string `json:"name"`
	IsAvailable       *bool  `json:"isAvailable"`
	Availability      string `json:"availability"`
	AvailabilityError string `json:"availabilityError"`
}

// parseDevices reads the devices listing, which is an object keyed by runtime
// identifier with an array of devices under each.
func parseDevices(out string) ([]Device, error) {
	var doc struct {
		Devices map[string][]rawDevice `json:"devices"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); err != nil {
		return nil, err
	}
	var devices []Device
	for runtime, list := range doc.Devices {
		for _, raw := range list {
			if raw.UDID == "" {
				continue
			}
			d := Device{UDID: raw.UDID, Name: raw.Name, Runtime: runtime, Available: true}
			switch {
			case raw.IsAvailable != nil && !*raw.IsAvailable:
				d.Available = false
			case strings.Contains(raw.Availability, "unavailable"):
				d.Available = false
			case raw.AvailabilityError != "":
				d.Available = false
			}
			d.Reason = strings.TrimSpace(raw.AvailabilityError)
			devices = append(devices, d)
		}
	}
	sortDevices(devices)
	return devices, nil
}

// sortDevices orders devices by UDID so two runs agree and a fixture can hold
// the result. The map iteration above is otherwise random.
func sortDevices(devices []Device) {
	for i := 1; i < len(devices); i++ {
		for j := i; j > 0 && devices[j].UDID < devices[j-1].UDID; j-- {
			devices[j], devices[j-1] = devices[j-1], devices[j]
		}
	}
}

// parseRuntimes reads `simctl runtime list -j`, an object keyed by an opaque
// identifier.
func parseRuntimes(out string) ([]Runtime, error) {
	var doc map[string]struct {
		Identifier string `json:"identifier"`
		Version    string `json:"version"`
		Build      string `json:"build"`
		Path       string `json:"path"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); err != nil {
		return nil, err
	}
	out2 := make([]Runtime, 0, len(doc))
	for key, r := range doc {
		id := r.Identifier
		if id == "" {
			id = key
		}
		out2 = append(out2, Runtime{Identifier: id, Version: r.Version, Build: r.Build, Path: r.Path})
	}
	for i := 1; i < len(out2); i++ {
		for j := i; j > 0 && out2[j].Identifier < out2[j-1].Identifier; j-- {
			out2[j], out2[j-1] = out2[j-1], out2[j]
		}
	}
	return out2, nil
}

// errNoXcrun is the answer when the gate passed but xcrun is not on the path,
// which should not happen and is reported rather than ignored.
var errNoXcrun = wrap("simctl", "xcrun is not on the path")

// wrap builds the small errors this package reports as degradations.
func wrap(what, why string) error { return &probeError{what: what, why: why} }

type probeError struct{ what, why string }

func (e *probeError) Error() string { return e.what + ": " + e.why }

// location is one of Xcode's directories.
type location struct {
	rel      string
	abs      string
	category string
	owner    string
	keys     []string
	reclaim  classify.Reclaim
	kind     string
	name     string
	explain  string
}

// locations are the directories docs/04 names.
//
// ~/Library/Developer/Xcode/Archives is deliberately absent: an archive is a
// backup of a build and belongs in the Backups bucket, so the backups
// detector claims it.
var locations = []location{
	{
		rel: "Library/Developer", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.Unknown, kind: "data", name: "Developer data",
		explain: "everything Xcode keeps for this user",
	},
	{
		rel: "Library/Developer/Xcode/DerivedData", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.Regenerable, kind: "cache", name: "DerivedData",
		explain: "build products and indexes for every project ever opened; Xcode rebuilds them",
	},
	{
		rel: "Library/Developer/Xcode/iOS DeviceSupport", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "iOS device support",
		explain: "symbols copied off each iOS device and version ever attached; Xcode copies them again when needed",
	},
	{
		rel: "Library/Developer/Xcode/watchOS DeviceSupport", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "watchOS device support",
		explain: "symbols from attached watches, re-copied on demand",
	},
	{
		rel: "Library/Developer/Xcode/tvOS DeviceSupport", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "tvOS device support",
		explain: "symbols from attached Apple TVs, re-copied on demand",
	},
	{
		rel: "Library/Developer/Xcode/UserData/Previews", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.Regenerable, kind: "cache", name: "SwiftUI previews",
		explain: "the preview simulator's own build products, rebuilt on the next preview",
	},
	{
		rel: "Library/Developer/CoreSimulator", category: "Simulators", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "Simulators",
		explain: "the simulator devices this user has created and their data",
	},
	{
		rel: "Library/Developer/CoreSimulator/Devices", category: "Simulators", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "Simulator devices",
		explain: "one directory per simulator; `xcrun simctl delete unavailable` removes the dead ones",
	},
	{
		rel: "Library/Developer/CoreSimulator/Caches", category: "Simulators", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.Regenerable, kind: "cache", name: "Simulator caches",
		explain: "downloaded simulator runtime disk images and their caches",
	},
	{
		rel: "Library/Developer/XCTestDevices", category: "Simulators", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.Regenerable, kind: "data", name: "XCTest devices",
		explain: "simulators created for test runs; they are recreated by the next run",
	},
	{
		rel: "Library/Caches/com.apple.dt.Xcode", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.Regenerable, kind: "cache", name: "Xcode cache",
		explain: "Xcode's own cache, rebuilt on demand",
	},
	{
		abs: "/Library/Developer", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.Unknown, kind: "data", name: "Shared developer data",
		explain: "developer tooling installed for every user on this machine",
	},
	{
		abs: "/Library/Developer/CommandLineTools", category: "Xcode", owner: "Command Line Tools",
		keys: xcodeKeys, reclaim: classify.ToolManaged, kind: "toolchain", name: "Command line tools",
		explain: "the standalone toolchain; `xcode-select --install` reinstalls it",
	},
	{
		abs: "/Library/Developer/CoreSimulator/Images", category: "Simulators", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "image", name: "Simulator runtime images",
		explain: "the simulator runtime disk images themselves; Xcode's Platforms pane deletes and re-downloads them",
	},
	{
		abs: "/Library/Developer/CoreSimulator/Cryptex", category: "Simulators", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "image", name: "Simulator cryptexes",
		explain: "signed runtime bundles the simulator mounts; removed with their runtime",
	},
	{
		abs: "/Library/Developer/CoreSimulator/Profiles", category: "Simulators", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "Simulator profiles",
		explain: "device type and runtime descriptions installed with Xcode",
	},
	{
		abs: "/Library/Developer/CoreDevice", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "CoreDevice",
		explain: "the device-connection service's data for attached hardware",
	},
	{
		abs: "/Library/Developer/DeveloperDiskImages", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "image", name: "Developer disk images",
		explain: "the images Xcode mounts on an attached device to debug it",
	},
	{
		abs: "/Library/Developer/DeviceKit", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "DeviceKit",
		explain: "support files for attached devices",
	},
	{
		abs: "/Library/Developer/PrivateFrameworks", category: "Xcode", owner: "Xcode", keys: xcodeKeys,
		reclaim: classify.ToolManaged, kind: "data", name: "Developer frameworks",
		explain: "frameworks the developer tools install outside the application bundle",
	},
}

// Classify claims Xcode's directories, and marks the simulator devices whose
// runtime is gone when the gate let simctl answer.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	evidence := evidenceLines(facts)

	targets := make([]detect.Target, 0, len(locations)+16)
	for _, loc := range locations {
		p := loc.abs
		if p == "" {
			if cx.Home == "" {
				continue
			}
			p = path.Join(cx.Home, loc.rel)
		}
		targets = append(targets, detect.Target{
			Path: p, Bucket: classify.BucketDeveloper, Category: loc.category,
			Owner: loc.owner, OwnerKeys: loc.keys, Reclaim: loc.reclaim,
			Explain: loc.explain, Kind: loc.kind, Name: loc.name, Evidence: evidence,
		})
	}
	targets = append(targets, deviceTargets(cx.Home, facts, evidence)...)

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// deviceTargets are the per-simulator claims, which exist only to mark the
// unavailable ones. A device whose runtime is gone cannot be booted and its
// directory is pure recoverable space; one that still works is the user's
// simulator with their data in it.
func deviceTargets(home string, f *Facts, evidence []string) []detect.Target {
	if home == "" || f == nil || len(f.Devices) == 0 {
		return nil
	}
	root := path.Join(home, "Library/Developer/CoreSimulator/Devices")
	out := make([]detect.Target, 0, len(f.Devices))
	for _, d := range f.Devices {
		tg := detect.Target{
			Path: path.Join(root, d.UDID), Bucket: classify.BucketDeveloper,
			Category: "Simulators", Owner: "Xcode", OwnerKeys: xcodeKeys,
			Reclaim: classify.ToolManaged, Kind: "data", Evidence: evidence,
		}
		if d.Available {
			tg.Name = d.Name
			tg.Explain = "the " + d.Name + " simulator and its data"
			tg.Note = d.Runtime
		} else {
			tg.Name = d.Name + " (unavailable)"
			tg.Reclaim = classify.Regenerable
			tg.Explain = "a simulator whose runtime is gone, so it cannot be booted; " +
				"`xcrun simctl delete unavailable` removes it"
			tg.Note = firstNonEmpty(d.Reason, "runtime "+d.Runtime+" is not installed")
		}
		out = append(out, tg)
	}
	return out
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

// evidenceLines are the why-panel lines every Xcode claim carries. The gate
// verdict is first, because it is the line that explains why the rest of the
// evidence is thin on a machine that failed it.
func evidenceLines(f *Facts) []string {
	if f == nil {
		return []string{"xcode did not answer; the paths come from the static catalog"}
	}
	out := []string{"`xcodebuild -checkFirstLaunchStatus`: " + f.Verdict()}
	if f.Selected != "" {
		out = append(out, "`xcode-select -p` → "+f.Selected)
	}
	if f.App != "" {
		out = append(out, "Xcode at "+f.App)
	}
	if !f.GatePassed {
		return out
	}
	if n := countUnavailable(f.Devices); n > 0 {
		out = append(out, itoa(n)+" of "+itoa(len(f.Devices))+" simulators have lost their runtime")
	} else if len(f.Devices) > 0 {
		out = append(out, "`simctl list devices` reported "+itoa(len(f.Devices))+" simulators, all available")
	}
	for _, r := range f.Runtimes {
		out = append(out, "`simctl runtime list`: "+r.Identifier+" "+r.Version+" ("+r.Build+")")
	}
	return out
}

// countUnavailable is how many simulators have lost their runtime.
func countUnavailable(devices []Device) int {
	n := 0
	for _, d := range devices {
		if !d.Available {
			n++
		}
	}
	return n
}

// itoa formats a small count without reaching for strconv for one call site.
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
