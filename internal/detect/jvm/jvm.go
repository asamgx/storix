// Package jvm detects the Java and Android toolchains: Gradle, Maven,
// SDKMAN, the installed JDKs, and the Android SDK with its emulator images.
//
// There is nothing to probe. Gradle and Maven have no cheap query for their
// cache locations — `gradle --version` starts a JVM, and starting a JVM to
// ask where a directory is would cost more than the whole walk — and the
// locations have not moved in a decade. The Android SDK's location is
// configurable, but the directory it defaults to is where Android Studio put
// it, and a detector that guessed wrong would be corrected by the next
// release rather than by a probe.
//
// What earns the detector its place is the split between a cache and a
// toolchain. Gradle's caches and Maven's repository are downloads that the
// build reproduces; the JDKs, the SDKMAN candidates and the Android system
// images are installations, and deleting one of those breaks a build until
// the tool is asked to reinstall it. The tags carry that difference.
package jvm

import (
	"context"
	"path"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "jvm"

func init() { detect.Register(230, New()) }

// androidKeys are the identifiers Android's directories join on. The SDK is
// Android Studio's even when it was installed by the command-line tools,
// because Studio is what a user would uninstall to be rid of it.
var androidKeys = []string{"app:com.google.android.studio", "cask:android-studio"}

// Detector finds the Java and Android toolchains.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are the directories that existed when the probe ran, recorded so a
// cached scan can tell "no JVM toolchain here" from "never looked".
type Facts struct {
	Found []string `json:"found,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// location is one directory the toolchains use.
type location struct {
	// rel is the path below the home directory; abs is an absolute path.
	// Exactly one of the two is set.
	rel      string
	abs      string
	owner    string
	keys     []string
	category string
	reclaim  classify.Reclaim
	kind     string
	name     string
	explain  string
}

// locations are the directories docs/04 names, deepest-first within each
// tool so that the specific tags win over the general one on the parent.
var locations = []location{
	// Gradle. The caches directory is the one that grows: it holds every
	// dependency and every compiled build script of every project ever
	// built on this machine.
	{
		rel: ".gradle", owner: "Gradle", keys: []string{"cli:gradle"}, category: "Gradle",
		reclaim: classify.ToolManaged, kind: "data",
		explain: "Gradle's home: caches, wrappers and daemon state",
	},
	{
		rel: ".gradle/caches", owner: "Gradle", keys: []string{"cli:gradle"}, category: "Gradle",
		reclaim: classify.Regenerable, kind: "cache", name: "Gradle caches",
		explain: "downloaded dependencies and compiled build scripts; the next build fetches them again",
	},
	{
		rel: ".gradle/wrapper", owner: "Gradle", keys: []string{"cli:gradle"}, category: "Gradle",
		reclaim: classify.ToolManaged, kind: "versions", name: "Gradle wrapper distributions",
		explain: "the Gradle distributions projects pinned; a wrapper build downloads the one it needs again",
	},
	{
		rel: ".gradle/daemon", owner: "Gradle", keys: []string{"cli:gradle"}, category: "Gradle",
		reclaim: classify.Regenerable, kind: "data", name: "Gradle daemon logs",
		explain: "logs from past daemon processes",
	},
	{
		rel: ".gradle/native", owner: "Gradle", keys: []string{"cli:gradle"}, category: "Gradle",
		reclaim: classify.Regenerable, kind: "cache", name: "Gradle native libraries",
		explain: "native helpers Gradle unpacked, re-extracted on demand",
	},

	// Maven.
	{
		rel: ".m2", owner: "Maven", keys: []string{"cli:mvn"}, category: "Maven",
		reclaim: classify.ToolManaged, kind: "data",
		explain: "Maven's home: the local repository and settings",
	},
	{
		rel: ".m2/repository", owner: "Maven", keys: []string{"cli:mvn"}, category: "Maven",
		reclaim: classify.Regenerable, kind: "cache", name: "Maven repository",
		explain: "every artifact Maven has downloaded; a build re-resolves what it needs",
	},

	// Version managers and JDKs.
	{
		rel: ".sdkman", owner: "SDKMAN", keys: []string{"cli:sdk"}, category: "JDKs",
		reclaim: classify.ToolManaged, kind: "versions",
		explain: "JVM toolchains SDKMAN installed; `sdk uninstall` removes one",
	},
	{
		rel: "Library/Java/JavaVirtualMachines", owner: "Java", keys: []string{"cli:java"}, category: "JDKs",
		reclaim: classify.ToolManaged, kind: "toolchain", name: "User JDKs",
		explain: "JDKs installed for this user only",
	},
	{
		abs: "/Library/Java", owner: "Java", keys: []string{"cli:java"}, category: "JDKs",
		reclaim: classify.ToolManaged, kind: "toolchain", name: "System JDKs",
		explain: "JDKs installed for every user; their own uninstallers remove them",
	},

	// Android.
	{
		rel: "Library/Android", owner: "Android SDK", keys: androidKeys, category: "Android",
		reclaim: classify.ToolManaged, kind: "data",
		explain: "where Android Studio keeps the SDK by default",
	},
	{
		rel: "Library/Android/sdk", owner: "Android SDK", keys: androidKeys, category: "Android",
		reclaim: classify.ToolManaged, kind: "toolchain",
		explain: "the Android SDK; the SDK Manager installs and removes its components",
	},
	{
		rel: "Library/Android/sdk/system-images", owner: "Android SDK", keys: androidKeys, category: "Android",
		reclaim: classify.ToolManaged, kind: "image", name: "Android system images",
		explain: "emulator system images, the largest part of the SDK; the SDK Manager re-downloads them",
	},
	{
		rel: "Library/Android/sdk/platforms", owner: "Android SDK", keys: androidKeys, category: "Android",
		reclaim: classify.ToolManaged, kind: "toolchain", name: "Android platforms",
		explain: "the API levels this machine can build against",
	},
	{
		rel: "Library/Android/sdk/build-tools", owner: "Android SDK", keys: androidKeys, category: "Android",
		reclaim: classify.ToolManaged, kind: "toolchain", name: "Android build tools",
		explain: "build tool versions; a project pins the one it needs",
	},
	{
		rel: "Library/Android/sdk/ndk", owner: "Android SDK", keys: androidKeys, category: "Android",
		reclaim: classify.ToolManaged, kind: "toolchain", name: "Android NDK",
		explain: "native development kits, one per installed version",
	},
	{
		rel: "Library/Android/sdk/emulator", owner: "Android SDK", keys: androidKeys, category: "Android",
		reclaim: classify.ToolManaged, kind: "toolchain", name: "Android emulator",
		explain: "the emulator itself, without the system images it runs",
	},
	{
		rel: "Library/Android/sdk/sources", owner: "Android SDK", keys: androidKeys, category: "Android",
		reclaim: classify.Regenerable, kind: "data", name: "Android platform sources",
		explain: "platform sources for the debugger; the SDK Manager downloads them again",
	},
	{
		rel: ".android", owner: "Android", keys: androidKeys, category: "Android",
		reclaim: classify.Unknown, kind: "data",
		explain: "Android's per-user state: the debug keystore, the AVDs and the ADB cache",
	},
	{
		rel: ".android/avd", owner: "Android", keys: androidKeys, category: "Android",
		reclaim: classify.ToolManaged, kind: "image", name: "Android virtual devices",
		explain: "emulator disks, one per virtual device; the Device Manager deletes them",
	},

	// Caches the JVM ecosystem leaves in ~/Library/Caches.
	{
		rel: "Library/Caches/JNA", owner: "JNA", keys: []string{"cli:java"}, category: "JVM caches",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "native stubs JNA unpacked from jars, re-extracted on demand",
	},
	{
		rel: "Library/Caches/JetPackCache", owner: "Android", keys: androidKeys, category: "Android",
		reclaim: classify.Regenerable, kind: "cache", name: "Jetpack cache",
		explain: "Jetpack component metadata, re-fetched on demand",
	},
}

// Probe looks for the directories; there is nothing to run.
func (*Detector) Probe(_ context.Context, env detect.Env) (detect.Facts, error) {
	f := &Facts{}
	for _, loc := range locations {
		if p := absolute(env.Home, loc); p != "" && env.Exists(p) {
			f.Found = append(f.Found, loc.key())
		}
	}
	if len(f.Found) == 0 {
		return nil, detect.Missingf("no Gradle, Maven, SDKMAN, JDK or Android SDK directory exists")
	}
	return f, nil
}

// Classify claims whichever of the directories the walk found.
func (*Detector) Classify(t *walk.Tree, _ detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	targets := make([]detect.Target, 0, len(locations))
	for _, loc := range locations {
		p := absolute(cx.Home, loc)
		if p == "" {
			continue
		}
		name := loc.name
		if name == "" {
			name = loc.owner
		}
		targets = append(targets, detect.Target{
			Path: p, Bucket: classify.BucketDeveloper, Category: loc.category,
			Owner: loc.owner, OwnerKeys: loc.keys, Reclaim: loc.reclaim,
			Explain: loc.explain, Kind: loc.kind, Name: name, Priority: priority,
		})
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// priority lifts this detector's claims above another detector's generic
// ones on the same node, the way the ide detector's do.
//
// Two of these directories are also reached by cli-tools, which attributes
// anything under ~/Library/Caches to a name taken from the directory itself:
// JNA and JetPackCache. Both claims are detector claims over the same node at
// the same depth, so without this the resolver falls through to comparing the
// detector names and the alphabet decides. A detector that names a directory
// outranks one that reads its name off the filesystem.
//
// cli-tools demotes its own generic expansions by the same amount in the
// other direction; both are kept, for the reason the ide detector gives.
const priority = 1

// absolute resolves a location against a home, which is the fixture's during
// a probe and the display home during classification. A relative location
// with no home resolves to nothing rather than to the root.
func absolute(home string, loc location) string {
	if loc.abs != "" {
		return loc.abs
	}
	if home == "" {
		return ""
	}
	return path.Join(home, loc.rel)
}

// key is how a location is named in the facts: the absolute path for a system
// one, the home-relative path for a per-user one.
func (loc location) key() string {
	if loc.abs != "" {
		return loc.abs
	}
	return loc.rel
}
