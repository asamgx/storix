package catalog

import "github.com/asamgx/storix/internal/classify"

// developerRules are the static fallbacks for every tool in docs/04. A
// detector that ran its probe knows more — the real cache directory, which
// toolchain is current, how much brew cleanup would free — and outranks these.
// What these rules guarantee is that the bytes are in the Developer bucket
// with a sane reclaim tag even when the tool is not installed, its probe
// failed, or the detector was switched off.
var developerRules = []classify.Rule{
	// Home dot-directories. A developer's home is mostly tool state, so the
	// generic rule sends it to Developer rather than Personal; the named
	// rules below, and Trash, win over it on specificity and priority.
	{
		ID: "dev.dotdir.generic", Match: "~/.{name}", Bucket: developer,
		Category: "Tool data", Owner: "{name}", OwnerKeys: []string{"cli:{name}"},
		Reclaim: unsure, Priority: generic,
		Explain: "a dot-directory in the home folder, usually one command-line tool's state",
	},

	// Homebrew.
	{
		ID: "dev.homebrew.prefix", Match: "/opt/homebrew", Bucket: developer,
		Category: "Homebrew", Owner: "Homebrew", OwnerKeys: []string{"cli:brew"}, Reclaim: tool,
		Explain: "the Homebrew prefix; reclaim through brew itself",
	},
	{
		ID: "dev.homebrew.cellar", Match: "/opt/homebrew/Cellar", Bucket: developer,
		Category: "Homebrew formulae", Owner: "Homebrew", OwnerKeys: []string{"cli:brew"}, Reclaim: tool,
		Explain: "installed formulae; `brew cleanup` removes superseded versions",
	},
	{
		ID: "dev.homebrew.formula", Match: "/opt/homebrew/Cellar/{formula}", Bucket: developer,
		Category: "Homebrew formulae", Owner: "{formula}", OwnerKeys: []string{"cli:{formula}"}, Reclaim: tool,
		Explain: "the formula {formula}; `brew uninstall {formula}` removes it",
	},
	{
		ID: "dev.homebrew.taps", Match: "/opt/homebrew/Library/Taps", Bucket: developer,
		Category: "Homebrew metadata", Owner: "Homebrew", OwnerKeys: []string{"cli:brew"}, Reclaim: regen,
		Explain: "tap clones; `brew untap` or a re-tap restores them",
	},
	{
		ID: "dev.homebrew.other", Match: "/opt/homebrew/{name}", Bucket: developer,
		Category: "Homebrew", Owner: "Homebrew", OwnerKeys: []string{"cli:brew"},
		Reclaim: tool, Priority: generic,
		Explain: "{name} under the Homebrew prefix",
	},
	{
		ID: "dev.homebrew.cache", Match: "~/Library/Caches/Homebrew", Bucket: developer,
		Category: "Package cache", Owner: "Homebrew", OwnerKeys: []string{"cli:brew"}, Reclaim: regen,
		Explain: "downloaded bottles and casks; `brew cleanup` clears them",
	},
	{
		ID: "dev.homebrew.logs", Match: "~/Library/Logs/Homebrew", Bucket: developer,
		Category: "Homebrew metadata", Owner: "Homebrew", OwnerKeys: []string{"cli:brew"}, Reclaim: regen,
		Explain: "brew build logs",
	},
	{
		ID: "dev.homebrew.intel-prefix", Match: "/usr/local/Homebrew", Bucket: developer,
		Category: "Homebrew", Owner: "Homebrew (Intel)", OwnerKeys: []string{"cli:brew"}, Reclaim: tool,
		Explain: "the Intel Homebrew installation under Rosetta",
	},
	{
		ID: "dev.homebrew.intel-cellar", Match: "/usr/local/Cellar", Bucket: developer,
		Category: "Homebrew formulae", Owner: "Homebrew (Intel)", OwnerKeys: []string{"cli:brew"}, Reclaim: tool,
		Explain: "Intel formulae installed under Rosetta",
	},
	{
		ID: "dev.local.prefix", Match: "/usr/local", Bucket: developer,
		Category: "Optional software", Owner: "/usr/local", Reclaim: unsure,
		Explain: "software installed outside the package managers",
	},
	{
		ID: "dev.local.other", Match: "/usr/local/{name}", Bucket: developer,
		Category: "Optional software", Owner: "{name}", OwnerKeys: []string{"cli:{name}"},
		Reclaim: unsure, Priority: generic,
		Explain: "{name} installed into /usr/local",
	},

	// Node.
	{
		ID: "dev.node.npm", Match: "~/.npm", Bucket: developer,
		Category: "Package cache", Owner: "npm", OwnerKeys: []string{"cli:npm"}, Reclaim: regen,
		Explain: "the npm cache; `npm cache clean --force` clears it",
	},
	{
		ID: "dev.node.npx", Match: "~/.npm/_npx", Bucket: developer,
		Category: "Package cache", Owner: "npx", OwnerKeys: []string{"cli:npx"}, Reclaim: regen,
		Explain: "packages npx downloaded to run once",
	},
	{
		ID: "dev.node.pnpm-store", Match: "~/Library/pnpm", Bucket: developer,
		Category: "Package store", Owner: "pnpm", OwnerKeys: []string{"cli:pnpm"}, Reclaim: tool,
		Explain: "the pnpm content-addressed store; node_modules hard-link into it, so its bytes are counted here once",
	},
	{
		ID: "dev.node.pnpm-store-alt", Match: "~/.pnpm-store", Bucket: developer,
		Category: "Package store", Owner: "pnpm", OwnerKeys: []string{"cli:pnpm"}, Reclaim: tool,
		Explain: "an older pnpm store location",
	},
	{
		ID: "dev.node.pnpm-cache", Match: "~/Library/Caches/pnpm", Bucket: developer,
		Category: "Package cache", Owner: "pnpm", OwnerKeys: []string{"cli:pnpm"}, Reclaim: regen,
		Explain: "pnpm's metadata cache, separate from the store",
	},
	{
		ID: "dev.node.yarn-cache", Match: "~/Library/Caches/Yarn", Bucket: developer,
		Category: "Package cache", Owner: "Yarn", OwnerKeys: []string{"cli:yarn"}, Reclaim: regen,
		Explain: "the Yarn 1.x cache",
	},
	{
		ID: "dev.node.yarn", Match: "~/.yarn", Bucket: developer,
		Category: "Package cache", Owner: "Yarn", OwnerKeys: []string{"cli:yarn"}, Reclaim: regen,
		Explain: "Yarn Berry's global folder and cache",
	},
	{
		ID: "dev.node.bun", Match: "~/.bun", Bucket: developer,
		Category: "Toolchain", Owner: "Bun", OwnerKeys: []string{"cli:bun"}, Reclaim: tool,
		Explain: "the Bun runtime and its install cache",
	},
	{
		ID: "dev.node.bun-cache", Match: "~/.bun/install/cache", Bucket: developer,
		Category: "Package cache", Owner: "Bun", OwnerKeys: []string{"cli:bun"}, Reclaim: regen,
		Explain: "Bun's package cache",
	},
	{
		ID: "dev.node.nvm", Match: "~/.nvm", Bucket: developer,
		Category: "Toolchain", Owner: "nvm", OwnerKeys: []string{"cli:nvm"}, Reclaim: tool,
		Explain: "node versions installed by nvm; `nvm uninstall` removes one",
	},
	{
		ID: "dev.node.nvm-cache", Match: "~/.nvm/.cache", Bucket: developer,
		Category: "Package cache", Owner: "nvm", OwnerKeys: []string{"cli:nvm"}, Reclaim: regen,
		Explain: "node tarballs nvm downloaded",
	},
	{
		ID: "dev.node.volta", Match: "~/.volta", Bucket: developer,
		Category: "Toolchain", Owner: "Volta", OwnerKeys: []string{"cli:volta"}, Reclaim: tool,
		Explain: "toolchains managed by Volta",
	},
	{
		ID: "dev.node.fnm", Match: "~/.local/share/fnm", Bucket: developer,
		Category: "Toolchain", Owner: "fnm", OwnerKeys: []string{"cli:fnm"}, Reclaim: tool,
		Explain: "node versions installed by fnm",
	},
	{
		ID: "dev.node.corepack", Match: "~/Library/Caches/node", Bucket: developer,
		Category: "Package cache", Owner: "Node", OwnerKeys: []string{"cli:node"}, Reclaim: regen,
		Explain: "corepack's package manager downloads",
	},
	{
		ID: "dev.node.gyp", Match: "~/Library/Caches/node-gyp", Bucket: developer,
		Category: "Package cache", Owner: "node-gyp", OwnerKeys: []string{"cli:node-gyp"}, Reclaim: regen,
		Explain: "node headers node-gyp builds native modules against",
	},
	{
		ID: "dev.node.typescript", Match: "~/Library/Caches/typescript", Bucket: developer,
		Category: "Tool cache", Owner: "TypeScript", OwnerKeys: []string{"cli:tsc"}, Reclaim: regen,
		Explain: "type definitions the TypeScript server downloaded",
	},

	// Python.
	{
		ID: "dev.python.pyenv", Match: "~/.pyenv", Bucket: developer,
		Category: "Toolchain", Owner: "pyenv", OwnerKeys: []string{"cli:pyenv"}, Reclaim: tool,
		Explain: "Python interpreters installed by pyenv",
	},
	{
		ID: "dev.python.pip-cache", Match: "~/Library/Caches/pip", Bucket: developer,
		Category: "Package cache", Owner: "pip", OwnerKeys: []string{"cli:pip"}, Reclaim: regen,
		Explain: "wheels pip downloaded; `pip cache purge` clears them",
	},
	{
		ID: "dev.python.poetry-cache", Match: "~/Library/Caches/pypoetry", Bucket: developer,
		Category: "Package cache", Owner: "Poetry", OwnerKeys: []string{"cli:poetry"}, Reclaim: regen,
		Explain: "Poetry's download cache and virtual environments",
	},
	{
		ID: "dev.python.uv-cache", Match: "~/.cache/uv", Bucket: developer,
		Category: "Package cache", Owner: "uv", OwnerKeys: []string{"cli:uv"}, Reclaim: regen,
		Explain: "uv's wheel cache; `uv cache clean` clears it",
	},
	{
		ID: "dev.python.uv-python", Match: "~/.local/share/uv", Bucket: developer,
		Category: "Toolchain", Owner: "uv", OwnerKeys: []string{"cli:uv"}, Reclaim: tool,
		Explain: "Python interpreters uv downloaded",
	},
	{
		ID: "dev.python.pipx", Match: "~/.local/pipx", Bucket: developer,
		Category: "Toolchain", Owner: "pipx", OwnerKeys: []string{"cli:pipx"}, Reclaim: tool,
		Explain: "applications pipx installed in their own environments",
	},
	{
		ID: "dev.python.precommit", Match: "~/.cache/pre-commit", Bucket: developer,
		Category: "Tool cache", Owner: "pre-commit", OwnerKeys: []string{"cli:pre-commit"}, Reclaim: regen,
		Explain: "hook environments pre-commit built",
	},
	{
		ID: "dev.python.conda", Match: "~/miniconda3", Bucket: developer,
		Category: "Toolchain", Owner: "conda", OwnerKeys: []string{"cli:conda"}, Reclaim: tool,
		Explain: "the Miniconda installation and its package cache",
	},
	{
		ID: "dev.python.anaconda", Match: "~/anaconda3", Bucket: developer,
		Category: "Toolchain", Owner: "conda", OwnerKeys: []string{"cli:conda"}, Reclaim: tool,
		Explain: "the Anaconda installation and its package cache",
	},

	// Go.
	{
		ID: "dev.go.gopath", Match: "~/go", Bucket: developer,
		Category: "Toolchain", Owner: "Go", OwnerKeys: []string{"cli:go"}, Reclaim: tool,
		Explain: "the Go workspace: the module cache and installed binaries",
	},
	{
		ID: "dev.go.modcache", Match: "~/go/pkg/mod", Bucket: developer,
		Category: "Package cache", Owner: "Go", OwnerKeys: []string{"cli:go"}, Reclaim: regen,
		Explain: "the Go module cache; `go clean -modcache` clears it",
	},
	{
		ID: "dev.go.bin", Match: "~/go/bin", Bucket: developer,
		Category: "Toolchain", Owner: "Go", OwnerKeys: []string{"cli:go"}, Reclaim: tool,
		Explain: "binaries installed with `go install`",
	},
	{
		ID: "dev.go.buildcache", Match: "~/Library/Caches/go-build", Bucket: developer,
		Category: "Build cache", Owner: "Go", OwnerKeys: []string{"cli:go"}, Reclaim: regen,
		Explain: "the Go build cache; `go clean -cache` clears it",
	},
	{
		ID: "dev.go.tools", Match: "~/Library/Caches/go", Bucket: developer,
		Category: "Tool cache", Owner: "Go", OwnerKeys: []string{"cli:go"}, Reclaim: regen,
		Explain: "caches written by the Go toolchain",
	},
	{
		ID: "dev.go.gopls", Match: "~/Library/Caches/gopls", Bucket: developer,
		Category: "Tool cache", Owner: "gopls", OwnerKeys: []string{"cli:gopls"}, Reclaim: regen,
		Explain: "the Go language server's index",
	},
	{
		ID: "dev.go.golangci", Match: "~/Library/Caches/golangci-lint", Bucket: developer,
		Category: "Tool cache", Owner: "golangci-lint", OwnerKeys: []string{"cli:golangci-lint"}, Reclaim: regen,
		Explain: "golangci-lint's analysis cache",
	},
	{
		ID: "dev.go.goimports", Match: "~/Library/Caches/goimports", Bucket: developer,
		Category: "Tool cache", Owner: "goimports", OwnerKeys: []string{"cli:goimports"}, Reclaim: regen,
		Explain: "goimports' module index",
	},
	{
		ID: "dev.go.staticcheck", Match: "~/Library/Caches/staticcheck", Bucket: developer,
		Category: "Tool cache", Owner: "staticcheck", OwnerKeys: []string{"cli:staticcheck"}, Reclaim: regen,
		Explain: "staticcheck's analysis cache",
	},

	// Rust.
	{
		ID: "dev.rust.cargo", Match: "~/.cargo", Bucket: developer,
		Category: "Toolchain", Owner: "Cargo", OwnerKeys: []string{"cli:cargo"}, Reclaim: tool,
		Explain: "Cargo's home: the registry cache, git checkouts and installed binaries",
	},
	{
		ID: "dev.rust.registry", Match: "~/.cargo/registry", Bucket: developer,
		Category: "Package cache", Owner: "Cargo", OwnerKeys: []string{"cli:cargo"}, Reclaim: regen,
		Explain: "crates.io downloads; Cargo refetches them on demand",
	},
	{
		ID: "dev.rust.git", Match: "~/.cargo/git", Bucket: developer,
		Category: "Package cache", Owner: "Cargo", OwnerKeys: []string{"cli:cargo"}, Reclaim: regen,
		Explain: "git dependencies Cargo checked out",
	},
	{
		ID: "dev.rust.rustup", Match: "~/.rustup", Bucket: developer,
		Category: "Toolchain", Owner: "rustup", OwnerKeys: []string{"cli:rustup"}, Reclaim: tool,
		Explain: "Rust toolchains; `rustup toolchain uninstall` removes one",
	},
	{
		ID: "dev.rust.downloads", Match: "~/.rustup/downloads", Bucket: developer,
		Category: "Package cache", Owner: "rustup", OwnerKeys: []string{"cli:rustup"}, Reclaim: regen,
		Explain: "toolchain archives rustup downloaded",
	},

	// Ruby.
	{
		ID: "dev.ruby.gem", Match: "~/.gem", Bucket: developer,
		Category: "Package cache", Owner: "RubyGems", OwnerKeys: []string{"cli:gem"}, Reclaim: tool,
		Explain: "gems installed for this user",
	},
	{
		ID: "dev.ruby.rbenv", Match: "~/.rbenv", Bucket: developer,
		Category: "Toolchain", Owner: "rbenv", OwnerKeys: []string{"cli:rbenv"}, Reclaim: tool,
		Explain: "Ruby versions installed by rbenv",
	},
	{
		ID: "dev.ruby.rvm", Match: "~/.rvm", Bucket: developer,
		Category: "Toolchain", Owner: "rvm", OwnerKeys: []string{"cli:rvm"}, Reclaim: tool,
		Explain: "Ruby versions installed by rvm",
	},
	{
		ID: "dev.ruby.bundle", Match: "~/.bundle", Bucket: developer,
		Category: "Package cache", Owner: "Bundler", OwnerKeys: []string{"cli:bundle"}, Reclaim: regen,
		Explain: "Bundler's download cache",
	},
	{
		ID: "dev.ruby.system", Match: "/Library/Ruby", Bucket: developer,
		Category: "Toolchain", Owner: "Ruby", OwnerKeys: []string{"cli:ruby"}, Reclaim: system,
		Explain: "the system Ruby's gems",
	},

	// JVM and Android.
	{
		ID: "dev.jvm.gradle", Match: "~/.gradle", Bucket: developer,
		Category: "Build cache", Owner: "Gradle", OwnerKeys: []string{"cli:gradle"}, Reclaim: regen,
		Explain: "Gradle's caches, wrappers and daemon state",
	},
	{
		ID: "dev.jvm.maven", Match: "~/.m2", Bucket: developer,
		Category: "Package cache", Owner: "Maven", OwnerKeys: []string{"cli:mvn"}, Reclaim: regen,
		Explain: "the local Maven repository",
	},
	{
		ID: "dev.jvm.sdkman", Match: "~/.sdkman", Bucket: developer,
		Category: "Toolchain", Owner: "SDKMAN", OwnerKeys: []string{"cli:sdk"}, Reclaim: tool,
		Explain: "JVM toolchains installed by SDKMAN",
	},
	{
		ID: "dev.jvm.user-jdks", Match: "~/Library/Java", Bucket: developer,
		Category: "Toolchain", Owner: "Java", OwnerKeys: []string{"cli:java"}, Reclaim: tool,
		Explain: "JDKs installed for this user",
	},
	{
		ID: "dev.jvm.system-jdks", Match: "/Library/Java", Bucket: developer,
		Category: "Toolchain", Owner: "Java", OwnerKeys: []string{"cli:java"}, Reclaim: tool,
		Explain: "machine-wide JDKs",
	},
	{
		ID: "dev.jvm.jna", Match: "~/Library/Caches/JNA", Bucket: developer,
		Category: "Tool cache", Owner: "Java", OwnerKeys: []string{"cli:java"}, Reclaim: regen,
		Explain: "native libraries JNA unpacked",
	},
	{
		ID: "dev.android.sdk", Match: "~/Library/Android", Bucket: developer,
		Category: "SDK", Owner: "Android Studio", OwnerKeys: []string{"app:com.google.android.studio"}, Reclaim: tool,
		Explain: "the Android SDK: platforms, system images and the emulator",
	},
	{
		ID: "dev.android.avd", Match: "~/.android", Bucket: developer,
		Category: "SDK", Owner: "Android Studio", OwnerKeys: []string{"app:com.google.android.studio"}, Reclaim: tool,
		Explain: "Android virtual devices and the adb key",
	},

	// Xcode and the Apple toolchain.
	{
		ID: "dev.xcode.developer", Match: "~/Library/Developer", Bucket: developer,
		Category: "Xcode", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: tool,
		Explain: "Xcode's per-user data: derived data, device support, simulators",
	},
	{
		ID: "dev.xcode.deriveddata", Match: "~/Library/Developer/Xcode/DerivedData", Bucket: developer,
		Category: "Build cache", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: regen,
		Explain: "build products and indexes; Xcode rebuilds them",
	},
	{
		ID: "dev.xcode.devicesupport", Match: "~/Library/Developer/Xcode/iOS DeviceSupport", Bucket: developer,
		Category: "Device support", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: tool,
		Explain: "symbols for every iOS version you ever debugged against",
	},
	{
		ID: "dev.xcode.watchsupport", Match: "~/Library/Developer/Xcode/watchOS DeviceSupport", Bucket: developer,
		Category: "Device support", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: tool,
		Explain: "symbols for watchOS devices",
	},
	{
		ID: "dev.xcode.tvsupport", Match: "~/Library/Developer/Xcode/tvOS DeviceSupport", Bucket: developer,
		Category: "Device support", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: tool,
		Explain: "symbols for tvOS devices",
	},
	{
		ID: "dev.xcode.simulators", Match: "~/Library/Developer/CoreSimulator", Bucket: developer,
		Category: "Simulators", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: tool,
		Explain: "simulator devices and their runtimes; `xcrun simctl delete unavailable` prunes them",
	},
	{
		ID: "dev.xcode.xctestdevices", Match: "~/Library/Developer/XCTestDevices", Bucket: developer,
		Category: "Simulators", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: tool,
		Explain: "test-only simulator devices",
	},
	{
		ID: "dev.xcode.cache", Match: "~/Library/Caches/com.apple.dt.Xcode", Bucket: developer,
		Category: "Tool cache", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"},
		Reclaim: regen, Priority: 1,
		Explain: "Xcode's own cache",
	},
	{
		ID: "dev.xcode.shared", Match: "/Library/Developer", Bucket: developer,
		Category: "Xcode", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: tool,
		Explain: "machine-wide developer components: command line tools, simulator images",
	},
	{
		ID: "dev.xcode.clt", Match: "/Library/Developer/CommandLineTools", Bucket: developer,
		Category: "Toolchain", Owner: "Command Line Tools", OwnerKeys: []string{"cli:clang"}, Reclaim: tool,
		Explain: "the Command Line Tools; reinstall with `xcode-select --install`",
	},
	{
		ID: "dev.xcode.sim-images", Match: "/Library/Developer/CoreSimulator", Bucket: developer,
		Category: "Simulators", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: tool,
		Explain: "simulator runtime images shared by every user",
	},
	{
		ID: "dev.swift.spm", Match: "~/Library/Caches/org.swift.swiftpm", Bucket: developer,
		Category: "Package cache", Owner: "SwiftPM", OwnerKeys: []string{"cli:swift"}, Reclaim: regen,
		Explain: "Swift Package Manager's shared dependency cache",
	},
	{
		ID: "dev.swift.home", Match: "~/.swiftpm", Bucket: developer,
		Category: "Package cache", Owner: "SwiftPM", OwnerKeys: []string{"cli:swift"}, Reclaim: regen,
		Explain: "Swift Package Manager's per-user state",
	},
	{
		ID: "dev.cocoapods.repos", Match: "~/.cocoapods", Bucket: developer,
		Category: "Package cache", Owner: "CocoaPods", OwnerKeys: []string{"cli:pod"}, Reclaim: regen,
		Explain: "the CocoaPods spec repositories",
	},
	{
		ID: "dev.cocoapods.cache", Match: "~/Library/Caches/CocoaPods", Bucket: developer,
		Category: "Package cache", Owner: "CocoaPods", OwnerKeys: []string{"cli:pod"}, Reclaim: regen,
		Explain: "pods CocoaPods downloaded",
	},

	// Editors, IDEs and coding agents.
	{
		ID: "dev.ide.vscode", Match: "~/.vscode", Bucket: developer,
		Category: "IDE data", Owner: "VS Code", OwnerKeys: []string{"app:com.microsoft.VSCode"}, Reclaim: tool,
		Explain: "VS Code extensions; the editor reinstalls them from the marketplace",
	},
	{
		ID: "dev.ide.vscode-support", Match: "~/Library/Application Support/Code", Bucket: developer,
		Category: "IDE data", Owner: "VS Code", OwnerKeys: []string{"app:com.microsoft.VSCode"}, Reclaim: unsure,
		Explain: "VS Code's profile: settings, workspace storage and caches",
	},
	{
		ID: "dev.ide.cursor", Match: "~/.cursor", Bucket: developer,
		Category: "IDE data", Owner: "Cursor", OwnerKeys: []string{"app:com.todesktop.230313mzl4w4u92", "cask:cursor"}, Reclaim: tool,
		Explain: "Cursor's extensions and agent state",
	},
	{
		ID: "dev.ide.cursor-support", Match: "~/Library/Application Support/Cursor", Bucket: developer,
		Category: "IDE data", Owner: "Cursor", OwnerKeys: []string{"app:com.todesktop.230313mzl4w4u92", "cask:cursor"}, Reclaim: unsure,
		Explain: "Cursor's profile and caches",
	},
	{
		ID: "dev.ide.antigravity", Match: "~/.antigravity", Bucket: developer,
		Category: "IDE data", Owner: "Antigravity", OwnerKeys: []string{"app:com.google.antigravity"}, Reclaim: tool,
		Explain: "Antigravity's extensions and workspace state",
	},
	{
		ID: "dev.ide.antigravity-support", Match: "~/Library/Application Support/Antigravity", Bucket: developer,
		Category: "IDE data", Owner: "Antigravity", OwnerKeys: []string{"app:com.google.antigravity"}, Reclaim: unsure,
		Explain: "Antigravity's profile and caches",
	},
	{
		ID: "dev.ide.zed", Match: "~/Library/Application Support/Zed", Bucket: developer,
		Category: "IDE data", Owner: "Zed", OwnerKeys: []string{"app:dev.zed.Zed"}, Reclaim: unsure,
		Explain: "Zed's profile, language servers and extensions",
	},
	{
		ID: "dev.ide.zed-cache", Match: "~/Library/Caches/Zed", Bucket: developer,
		Category: "Tool cache", Owner: "Zed", OwnerKeys: []string{"app:dev.zed.Zed"}, Reclaim: regen,
		Explain: "Zed's cache",
	},
	{
		ID: "dev.ide.warp", Match: "~/Library/Application Support/dev.warp.Warp-Stable", Bucket: developer,
		Category: "IDE data", Owner: "Warp", OwnerKeys: []string{"app:dev.warp.Warp-Stable"}, Reclaim: unsure,
		Explain: "Warp's profile and history",
	},
	{
		ID: "dev.ide.jetbrains-support", Match: "~/Library/Application Support/JetBrains", Bucket: developer,
		Category: "IDE data", Owner: "JetBrains", OwnerKeys: []string{"vendor:com.jetbrains"}, Reclaim: unsure,
		Explain: "JetBrains IDEs' settings, plugins and Toolbox applications",
	},
	{
		ID: "dev.ide.jetbrains-cache", Match: "~/Library/Caches/JetBrains", Bucket: developer,
		Category: "Tool cache", Owner: "JetBrains", OwnerKeys: []string{"vendor:com.jetbrains"}, Reclaim: regen,
		Explain: "JetBrains indexes and compiler caches; the IDE rebuilds them",
	},
	{
		ID: "dev.ide.jetbrains-logs", Match: "~/Library/Logs/JetBrains", Bucket: developer,
		Category: "Tool cache", Owner: "JetBrains", OwnerKeys: []string{"vendor:com.jetbrains"}, Reclaim: regen,
		Explain: "JetBrains IDE logs",
	},
	{
		ID: "dev.ide.nvim-share", Match: "~/.local/share/nvim", Bucket: developer,
		Category: "IDE data", Owner: "Neovim", OwnerKeys: []string{"cli:nvim"}, Reclaim: tool,
		Explain: "Neovim plugins and Mason's language servers",
	},
	{
		ID: "dev.ide.nvim-cache", Match: "~/.cache/nvim", Bucket: developer,
		Category: "Tool cache", Owner: "Neovim", OwnerKeys: []string{"cli:nvim"}, Reclaim: regen,
		Explain: "Neovim's cache",
	},
	{
		ID: "dev.agent.claude", Match: "~/.claude", Bucket: developer,
		Category: "Coding agent", Owner: "Claude Code", OwnerKeys: []string{"cli:claude"}, Reclaim: unsure,
		Explain: "Claude Code's projects, sessions and configuration",
	},
	{
		ID: "dev.agent.codex", Match: "~/.codex", Bucket: developer,
		Category: "Coding agent", Owner: "Codex", OwnerKeys: []string{"cli:codex", "cask:codex"}, Reclaim: unsure,
		Explain: "Codex's sessions and configuration",
	},
	{
		ID: "dev.agent.codex-runtimes", Match: "~/.cache/codex-runtimes", Bucket: developer,
		Category: "Coding agent", Owner: "Codex", OwnerKeys: []string{"cli:codex"}, Reclaim: regen,
		Explain: "language runtimes Codex downloaded to run tools",
	},
	{
		ID: "dev.agent.codex-support", Match: "~/Library/Application Support/Codex", Bucket: developer,
		Category: "Coding agent", Owner: "Codex", OwnerKeys: []string{"cli:codex"}, Reclaim: unsure,
		Explain: "the Codex application's data",
	},
	{
		ID: "dev.agent.gemini", Match: "~/.gemini", Bucket: developer,
		Category: "Coding agent", Owner: "Gemini CLI", OwnerKeys: []string{"cli:gemini"}, Reclaim: unsure,
		Explain: "the Gemini CLI's sessions and configuration",
	},

	// AI model caches.
	{
		ID: "dev.ai.ollama", Match: "~/.ollama", Bucket: developer,
		Category: "Model cache", Owner: "Ollama", OwnerKeys: []string{"cli:ollama"}, Reclaim: tool,
		Explain: "models Ollama pulled; `ollama rm` removes one",
	},
	{
		ID: "dev.ai.huggingface", Match: "~/.cache/huggingface", Bucket: developer,
		Category: "Model cache", Owner: "Hugging Face", OwnerKeys: []string{"cli:huggingface-cli"}, Reclaim: tool,
		Explain: "models and datasets the Hugging Face hub client downloaded",
	},
	{
		ID: "dev.ai.torch", Match: "~/.cache/torch", Bucket: developer,
		Category: "Model cache", Owner: "PyTorch", OwnerKeys: []string{"cli:torch"}, Reclaim: tool,
		Explain: "model weights PyTorch downloaded",
	},
	{
		ID: "dev.ai.whisper", Match: "~/.cache/whisper", Bucket: developer,
		Category: "Model cache", Owner: "Whisper", OwnerKeys: []string{"cli:whisper"}, Reclaim: tool,
		Explain: "Whisper model weights",
	},
	{
		ID: "dev.ai.lmstudio", Match: "~/.lmstudio", Bucket: developer,
		Category: "Model cache", Owner: "LM Studio", OwnerKeys: []string{"app:ai.elementlabs.lmstudio"}, Reclaim: tool,
		Explain: "models LM Studio downloaded",
	},
	{
		ID: "dev.ai.lmstudio-support", Match: "~/Library/Application Support/LM Studio", Bucket: developer,
		Category: "Model cache", Owner: "LM Studio", OwnerKeys: []string{"app:ai.elementlabs.lmstudio"}, Reclaim: tool,
		Explain: "LM Studio's application data and models",
	},

	// Generic command-line tool state.
	{
		ID: "dev.cli.cache-root", Match: "~/.cache", Bucket: developer,
		Category: "Tool cache", Owner: "Tool caches", Reclaim: regen,
		Explain: "the XDG cache directory: one subdirectory per tool, all rebuildable",
	},
	{
		ID: "dev.cli.cache-owner", Match: "~/.cache/{name}", Bucket: developer,
		Category: "Tool cache", Owner: "{name}", OwnerKeys: []string{"cli:{name}"},
		Reclaim: regen, Priority: generic,
		Explain: "{name}'s cache; the tool refills it on demand",
	},
	{
		ID: "dev.cli.local", Match: "~/.local", Bucket: developer,
		Category: "Tool data", Owner: "User-local tools", Reclaim: unsure,
		Explain: "the XDG data and binary directories for tools installed per user",
	},
	{
		ID: "dev.cli.local-share", Match: "~/.local/share/{name}", Bucket: developer,
		Category: "Tool data", Owner: "{name}", OwnerKeys: []string{"cli:{name}"},
		Reclaim: unsure, Priority: generic,
		Explain: "{name}'s per-user data",
	},
	{
		ID: "dev.cli.local-bin", Match: "~/.local/bin", Bucket: developer,
		Category: "Toolchain", Owner: "User-local tools", Reclaim: tool,
		Explain: "binaries installed into the user's own bin directory",
	},
	{
		ID: "dev.cli.config", Match: "~/.config", Bucket: developer,
		Category: "Tool configuration", Owner: "Tool configuration", Reclaim: user,
		Explain: "the XDG configuration directory; small and worth keeping",
	},
	{
		ID: "dev.cli.config-owner", Match: "~/.config/{name}", Bucket: developer,
		Category: "Tool configuration", Owner: "{name}", OwnerKeys: []string{"cli:{name}"},
		Reclaim: user, Priority: generic,
		Explain: "{name}'s configuration",
	},
	{
		ID: "dev.cli.playwright", Match: "~/Library/Caches/ms-playwright", Bucket: developer,
		Category: "Tool cache", Owner: "Playwright", OwnerKeys: []string{"cli:playwright"}, Reclaim: tool,
		Explain: "browsers Playwright downloaded; `playwright uninstall` removes them",
	},
	{
		ID: "dev.cli.playwright-go", Match: "~/Library/Caches/ms-playwright-go", Bucket: developer,
		Category: "Tool cache", Owner: "Playwright", OwnerKeys: []string{"cli:playwright"}, Reclaim: tool,
		Explain: "browsers the Go Playwright driver downloaded",
	},
	{
		ID: "dev.cli.cypress", Match: "~/Library/Caches/Cypress", Bucket: developer,
		Category: "Tool cache", Owner: "Cypress", OwnerKeys: []string{"cli:cypress"}, Reclaim: tool,
		Explain: "Cypress binaries, one per version used",
	},
	{
		ID: "dev.cli.puppeteer", Match: "~/Library/Caches/puppeteer", Bucket: developer,
		Category: "Tool cache", Owner: "Puppeteer", OwnerKeys: []string{"cli:puppeteer"}, Reclaim: tool,
		Explain: "Chromium builds Puppeteer downloaded",
	},
	{
		ID: "dev.cli.prisma", Match: "~/Library/Caches/prisma-nodejs", Bucket: developer,
		Category: "Tool cache", Owner: "Prisma", OwnerKeys: []string{"cli:prisma"}, Reclaim: regen,
		Explain: "Prisma engine binaries",
	},
	{
		ID: "dev.cli.biome", Match: "~/Library/Caches/dev.biomejs.biome", Bucket: developer,
		Category: "Tool cache", Owner: "Biome", OwnerKeys: []string{"cli:biome"}, Reclaim: regen,
		Explain: "Biome's cache",
	},
	{
		ID: "dev.cli.helm", Match: "~/Library/Caches/helm", Bucket: developer,
		Category: "Tool cache", Owner: "Helm", OwnerKeys: []string{"cli:helm"}, Reclaim: regen,
		Explain: "Helm's chart repository cache",
	},
	{
		ID: "dev.cli.turborepo", Match: "~/Library/Application Support/turborepo", Bucket: developer,
		Category: "Build cache", Owner: "Turborepo", OwnerKeys: []string{"cli:turbo"}, Reclaim: regen,
		Explain: "Turborepo's global cache and telemetry state",
	},
	{
		ID: "dev.cli.terraform", Match: "~/.terraform.d", Bucket: developer,
		Category: "Package cache", Owner: "Terraform", OwnerKeys: []string{"cli:terraform"}, Reclaim: regen,
		Explain: "Terraform's provider plugin cache",
	},
	{
		ID: "dev.cli.pulumi", Match: "~/.pulumi", Bucket: developer,
		Category: "Package cache", Owner: "Pulumi", OwnerKeys: []string{"cli:pulumi"}, Reclaim: regen,
		Explain: "Pulumi plugins and state",
	},
	{
		ID: "dev.cli.deno", Match: "~/.deno", Bucket: developer,
		Category: "Toolchain", Owner: "Deno", OwnerKeys: []string{"cli:deno"}, Reclaim: tool,
		Explain: "the Deno runtime and its dependency cache",
	},
	{
		ID: "dev.cli.wasmtime", Match: "~/.wasmtime", Bucket: developer,
		Category: "Toolchain", Owner: "Wasmtime", OwnerKeys: []string{"cli:wasmtime"}, Reclaim: tool,
		Explain: "the Wasmtime runtime",
	},

	// Other ecosystems.
	{
		ID: "dev.other.pub-cache", Match: "~/.pub-cache", Bucket: developer,
		Category: "Package cache", Owner: "Dart", OwnerKeys: []string{"cli:dart"}, Reclaim: regen,
		Explain: "Dart and Flutter packages",
	},
	{
		ID: "dev.other.dartserver", Match: "~/.dartServer", Bucket: developer,
		Category: "Tool cache", Owner: "Dart", OwnerKeys: []string{"cli:dart"}, Reclaim: regen,
		Explain: "the Dart analysis server's cache",
	},
	{
		ID: "dev.other.flutter", Match: "~/flutter", Bucket: developer,
		Category: "Toolchain", Owner: "Flutter", OwnerKeys: []string{"cli:flutter"}, Reclaim: tool,
		Explain: "the Flutter SDK and its downloaded artefacts",
	},
	{
		ID: "dev.other.nuget", Match: "~/.nuget", Bucket: developer,
		Category: "Package cache", Owner: "NuGet", OwnerKeys: []string{"cli:dotnet"}, Reclaim: regen,
		Explain: "NuGet packages",
	},
	{
		ID: "dev.other.dotnet", Match: "~/.dotnet", Bucket: developer,
		Category: "Toolchain", Owner: ".NET", OwnerKeys: []string{"cli:dotnet"}, Reclaim: tool,
		Explain: "the .NET SDK's per-user state",
	},
	{
		ID: "dev.other.ghcup", Match: "~/.ghcup", Bucket: developer,
		Category: "Toolchain", Owner: "GHCup", OwnerKeys: []string{"cli:ghcup"}, Reclaim: tool,
		Explain: "Haskell toolchains installed by GHCup",
	},
	{
		ID: "dev.other.stack", Match: "~/.stack", Bucket: developer,
		Category: "Package cache", Owner: "Stack", OwnerKeys: []string{"cli:stack"}, Reclaim: regen,
		Explain: "Stack's package index and snapshots",
	},
	{
		ID: "dev.other.cabal", Match: "~/.cabal", Bucket: developer,
		Category: "Package cache", Owner: "Cabal", OwnerKeys: []string{"cli:cabal"}, Reclaim: regen,
		Explain: "Cabal's package store",
	},
	{
		ID: "dev.other.ccache", Match: "~/.ccache", Bucket: developer,
		Category: "Build cache", Owner: "ccache", OwnerKeys: []string{"cli:ccache"}, Reclaim: regen,
		Explain: "the C compiler cache",
	},
	{
		ID: "dev.other.conan", Match: "~/.conan2", Bucket: developer,
		Category: "Package cache", Owner: "Conan", OwnerKeys: []string{"cli:conan"}, Reclaim: regen,
		Explain: "Conan's C++ package cache",
	},
	{
		ID: "dev.other.mix", Match: "~/.mix", Bucket: developer,
		Category: "Package cache", Owner: "Mix", OwnerKeys: []string{"cli:mix"}, Reclaim: regen,
		Explain: "Elixir's build tool archives",
	},
	{
		ID: "dev.other.hex", Match: "~/.hex", Bucket: developer,
		Category: "Package cache", Owner: "Hex", OwnerKeys: []string{"cli:hex"}, Reclaim: regen,
		Explain: "the Hex package cache",
	},
	{
		ID: "dev.other.composer", Match: "~/.composer", Bucket: developer,
		Category: "Package cache", Owner: "Composer", OwnerKeys: []string{"cli:composer"}, Reclaim: regen,
		Explain: "Composer's PHP package cache",
	},
	{
		ID: "dev.other.zig", Match: "~/.cache/zig", Bucket: developer,
		Category: "Build cache", Owner: "Zig", OwnerKeys: []string{"cli:zig"}, Reclaim: regen,
		Explain: "Zig's global build cache",
	},
	{
		ID: "dev.other.bazel", Match: "/private/var/tmp/_bazel_{user}", Bucket: developer,
		Category: "Build cache", Owner: "Bazel", OwnerKeys: []string{"cli:bazel"}, Reclaim: regen,
		Explain: "Bazel's output base for {user}; `bazel clean --expunge` removes it",
	},
	{
		ID: "dev.nix.store", Match: "/nix", Bucket: developer,
		Category: "Package store", Owner: "Nix", OwnerKeys: []string{"cli:nix"}, Reclaim: tool,
		Explain: "the Nix store; `nix-collect-garbage -d` reclaims what no profile references",
	},
}
