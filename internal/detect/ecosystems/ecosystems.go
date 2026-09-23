// Package ecosystems detects the language toolchains that are not big enough
// on a typical machine to deserve a detector each: Flutter and Dart, .NET,
// Haskell, Bazel, ccache, Conan, CocoaPods, Elixir, PHP and Zig.
//
// None of them is installed on the machine this was written against, and that
// is the point. A machine with none of these still has to bucket their
// directories correctly the day one of them appears, and the cost of knowing
// about a toolchain that is absent is one lstat: the probe returns Missing,
// the detector claims nothing, and the catalog's own fallback rules never
// even get the chance to be wrong.
//
// Every entry here is a directory whose name is fixed by the tool and whose
// reclaimability is not obvious from the name. `~/.pub-cache` and `~/.ghcup`
// look alike and are not alike: one is a download cache a build refills, the
// other is a set of installed compilers. That judgement is what the table
// carries.
package ecosystems

import (
	"context"
	"path"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
//
// The plan's table calls this row "other-ecosystems"; the detector, its
// package and its cache section all use "ecosystems", because the name is
// what a user types after --disable-detector and every other detector's name
// is its package name.
const Name = "ecosystems"

func init() { detect.Register(260, New()) }

// Detector finds the smaller language ecosystems.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Facts are the directories that existed when the probe ran.
type Facts struct {
	Found []string `json:"found,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// location is one ecosystem's directory.
type location struct {
	// rel is the path below the home directory. A location outside the
	// home is built by [systemTargets] instead, because the only one is
	// Bazel's and it needs the user name.
	rel      string
	owner    string
	keys     []string
	category string
	reclaim  classify.Reclaim
	kind     string
	name     string
	explain  string
}

// locations are the per-user directories, grouped by ecosystem in the order
// the report lists them.
var locations = []location{
	// Flutter and Dart.
	{
		rel: ".pub-cache", owner: "Dart", keys: []string{"cli:dart", "cli:flutter"}, category: "Flutter & Dart",
		reclaim: classify.Regenerable, kind: "cache", name: "pub cache",
		explain: "every Dart package ever resolved; `dart pub get` downloads them again",
	},
	{
		rel: "flutter", owner: "Flutter", keys: []string{"cli:flutter"}, category: "Flutter & Dart",
		reclaim: classify.ToolManaged, kind: "toolchain",
		explain: "a Flutter checkout used as the SDK",
	},
	{
		rel: "flutter/bin/cache", owner: "Flutter", keys: []string{"cli:flutter"}, category: "Flutter & Dart",
		reclaim: classify.Regenerable, kind: "cache", name: "Flutter engine cache",
		explain: "the downloaded engine artifacts; `flutter precache` fetches them again",
	},
	{
		rel: "development/flutter", owner: "Flutter", keys: []string{"cli:flutter"}, category: "Flutter & Dart",
		reclaim: classify.ToolManaged, kind: "toolchain", name: "Flutter SDK",
		explain: "the location the Flutter install guide suggests",
	},
	{
		rel: ".dartServer", owner: "Dart", keys: []string{"cli:dart"}, category: "Flutter & Dart",
		reclaim: classify.Regenerable, kind: "cache", name: "Dart analysis server",
		explain: "the analysis server's index, rebuilt when a project is opened",
	},

	// .NET.
	{
		rel: ".nuget", owner: "NuGet", keys: []string{"cli:dotnet"}, category: ".NET",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "NuGet's package cache; a restore downloads what it needs again",
	},
	{
		rel: ".dotnet", owner: ".NET", keys: []string{"cli:dotnet"}, category: ".NET",
		reclaim: classify.ToolManaged, kind: "toolchain",
		explain: ".NET's per-user SDK state and installed tools",
	},

	// Haskell.
	{
		rel: ".ghcup", owner: "GHCup", keys: []string{"cli:ghcup"}, category: "Haskell",
		reclaim: classify.ToolManaged, kind: "versions",
		explain: "installed GHC compilers and tools; `ghcup rm` removes one",
	},
	{
		rel: ".stack", owner: "Stack", keys: []string{"cli:stack"}, category: "Haskell",
		reclaim: classify.ToolManaged, kind: "data",
		explain: "Stack's compilers and package databases",
	},
	{
		rel: ".cabal", owner: "Cabal", keys: []string{"cli:cabal"}, category: "Haskell",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "Cabal's package index and downloaded sources",
	},

	// Compiler and dependency caches.
	{
		rel: ".ccache", owner: "ccache", keys: []string{"cli:ccache"}, category: "Compiler caches",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "cached C and C++ object files; `ccache -C` clears it and builds refill it",
	},
	{
		rel: ".conan2", owner: "Conan", keys: []string{"cli:conan"}, category: "Compiler caches",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "Conan's package cache; `conan cache clean` reclaims it",
	},

	// Apple's third-party dependency manager.
	{
		rel: ".cocoapods", owner: "CocoaPods", keys: []string{"cli:pod"}, category: "CocoaPods",
		reclaim: classify.Regenerable, kind: "data",
		explain: "CocoaPods' home, mostly the specs repository",
	},
	{
		rel: ".cocoapods/repos", owner: "CocoaPods", keys: []string{"cli:pod"}, category: "CocoaPods",
		reclaim: classify.Regenerable, kind: "cache", name: "CocoaPods specs",
		explain: "the specs repository; `pod repo update` fetches it again",
	},
	{
		rel: "Library/Caches/CocoaPods", owner: "CocoaPods", keys: []string{"cli:pod"}, category: "CocoaPods",
		reclaim: classify.Regenerable, kind: "cache", name: "CocoaPods cache",
		explain: "downloaded pod sources, re-downloaded on the next install",
	},

	// Elixir.
	{
		rel: ".mix", owner: "Mix", keys: []string{"cli:mix"}, category: "Elixir",
		reclaim: classify.ToolManaged, kind: "data",
		explain: "Mix's archives and installed escripts",
	},
	{
		rel: ".hex", owner: "Hex", keys: []string{"cli:mix"}, category: "Elixir",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "Hex's package cache and registry",
	},

	// PHP.
	{
		rel: ".composer", owner: "Composer", keys: []string{"cli:composer"}, category: "PHP",
		reclaim: classify.ToolManaged, kind: "data",
		explain: "Composer's home: globally installed packages and its cache",
	},
	{
		rel: ".composer/cache", owner: "Composer", keys: []string{"cli:composer"}, category: "PHP",
		reclaim: classify.Regenerable, kind: "cache", name: "Composer cache",
		explain: "downloaded package archives; `composer clear-cache` reclaims them",
	},

	// Zig.
	{
		rel: ".cache/zig", owner: "Zig", keys: []string{"cli:zig"}, category: "Zig",
		reclaim: classify.Regenerable, kind: "cache",
		explain: "Zig's global build cache, rebuilt by the next build",
	},
	{
		rel: ".zvm", owner: "zvm", keys: []string{"cli:zvm"}, category: "Zig",
		reclaim: classify.ToolManaged, kind: "versions",
		explain: "Zig versions zvm installed",
	},
}

// bazelRoot is where Bazel puts its output bases, one directory per user.
// It is outside the home, so the user name has to be recovered from the home
// path: Bazel names the directory after the account, and the account is the
// last segment of its home directory on every macOS machine.
const bazelRoot = "/private/var/tmp/_bazel_"

// Probe looks for the directories; there is nothing to run.
func (*Detector) Probe(_ context.Context, env detect.Env) (detect.Facts, error) {
	if env.Home == "" {
		return nil, detect.Missingf("no home directory to look in")
	}
	f := &Facts{}
	for _, loc := range locations {
		if env.Exists(path.Join(env.Home, loc.rel)) {
			f.Found = append(f.Found, loc.rel)
		}
	}
	if b := bazelPath(env.Home); b != "" && env.Exists(b) {
		f.Found = append(f.Found, b)
	}
	if len(f.Found) == 0 {
		return nil, detect.Missingf("none of Flutter, .NET, Haskell, Bazel, ccache, Conan, CocoaPods, Elixir, PHP or Zig has a directory here")
	}
	return f, nil
}

// Classify claims whichever of the directories the walk found.
func (*Detector) Classify(t *walk.Tree, _ detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}

	targets := make([]detect.Target, 0, len(locations)+1)
	for _, loc := range locations {
		name := loc.name
		if name == "" {
			name = loc.owner
		}
		targets = append(targets, detect.Target{
			Path: path.Join(home, loc.rel), Bucket: classify.BucketDeveloper,
			Category: loc.category, Owner: loc.owner, OwnerKeys: loc.keys,
			Reclaim: loc.reclaim, Explain: loc.explain, Kind: loc.kind, Name: name,
		})
	}
	if b := bazelPath(home); b != "" {
		targets = append(targets, detect.Target{
			Path: b, Bucket: classify.BucketDeveloper, Category: "Bazel",
			Owner: "Bazel", OwnerKeys: []string{"cli:bazel"}, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "Bazel output base",
			Explain: "Bazel's output base for this user; `bazel clean --expunge` reclaims it and the next build refills it",
		})
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools}
}

// bazelPath is this user's Bazel output base, or empty when the home does not
// name a user.
func bazelPath(home string) string {
	user := path.Base(path.Clean(home))
	if home == "" || user == "." || user == "/" || user == "" {
		return ""
	}
	return bazelRoot + user
}
