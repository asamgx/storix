# 04 — Detectors

Detectors turn a size tree into attributed storage. Each one probes external facts (shell
commands, plists) concurrently with the walk, then classifies nodes with provenance. All
detectors must degrade gracefully when their tool is missing.

## Apps: inventory, footprint, orphans

### Installed app inventory
Sources, merged and de-duplicated by bundle identifier:

| Source | How | Notes |
|---|---|---|
| `/Applications`, `~/Applications`, `/Applications/Utilities`, `/Applications/Setapp` | Walk one level for `*.app`; read `Contents/Info.plist` | `CFBundleIdentifier`, `CFBundleName`/`CFBundleDisplayName`, `CFBundleShortVersionString` |
| **Any `.app` on the volume** (backstop) | The walker already sees every `.app` bundle: JetBrains Toolbox apps under Application Support, Input Methods, PreferencePanes, apps nested in other bundles, apps left in Downloads or on the Desktop. Also `mdfind "kMDItemContentType == 'com.apple.application-bundle'"` and `system_profiler SPApplicationsDataType -json` as cross-checks | **"No bundle anywhere on the volume" is the precondition for orphan-likely.** Without this backstop every unconventional install location is a false orphan |
| Homebrew casks | List `brew --caskroom` directories directly; `brew list --cask --versions` and `brew info --cask --json=v2` as enrichment | **Verified:** `brew list --cask` and `brew info --cask` abort entirely when any cask comes from an untrusted third-party tap ("Refusing to load cask ... from untrusted tap"). The Caskroom directory listing is the reliable source; brew commands are best-effort |
| Mac App Store | presence of `Contents/_MASReceipt/receipt` | Marks source = App Store |
| LaunchServices registry | `/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -dump` | Lists every registered bundle including **stale entries for deleted apps**: a useful orphan signal and a way to recover the bundle id of an app that is gone. **Verified:** works unprivileged, ~560 bundles on the planning machine. Record format: `bundle id:` is a display name plus hex handle, `path:` is the bundle path, `identifier:` is the actual CFBundleIdentifier. Parse `path`/`identifier` pairs; ignore `bundle id` |
| Team identifier | `codesign -dv --verbose=4 <app>` → `TeamIdentifier=` | Needed to attribute Group Containers (`<TeamID>.<group>`). **Verified:** ~26 ms per app, so running it for every installed app each scan is fine; cache anyway |
| `/System/Applications` | Skipped for orphan logic; sized into bucket 1 | Apple apps live on the sealed volume |

### App data locations (per-user unless noted)
Keyed by **bundle id**, **vendor prefix** (`com.microsoft.*`), **display name**, or **team id**:

- `~/Library/Containers/<bundleid>` (sandboxed app data). The directory name is the bundle id.
  The `.com.apple.containermanagerd.metadata.plist` inside would confirm it, but **verified:**
  it is unreadable even with Full Disk Access for third-party containers (TCC data vault).
  Treat it as optional evidence; the directory name is primary.
- `~/Library/Group Containers/<name>`. **Verified naming is mixed:** `<TeamID>.<group>`
  (e.g. `HUAQ24HBR6.dev.orbstack`, `LTZ2PFU5D6.com.bitwarden.desktop`), `group.<reverse-dns>`
  with no team id (e.g. `group.net.whatsapp.WhatsApp.shared`, `group.is.workflow.my.app`),
  and occasional oddities (`--AppIdentifierPrefix-localsend.shared_group`). Match by team id
  when the first component looks like one (10 uppercase alphanumerics), else by reverse-DNS
  vendor prefix after stripping `group.`.
- `~/Library/Application Support/<Name or bundleid>`
- `~/Library/Caches/<bundleid>`
- `~/Library/Preferences/<bundleid>.plist`, `~/Library/Preferences/ByHost/<bundleid>.*.plist`
- `~/Library/Saved Application State/<bundleid>.savedState`
- `~/Library/HTTPStorages/<bundleid>`, `~/Library/WebKit/<bundleid>`, `~/Library/Cookies/<bundleid>.binarycookies`
- `~/Library/Logs/<Name or bundleid>`
- `~/Library/Application Scripts/<bundleid>`
- `~/Library/LaunchAgents/<bundleid>*.plist`, `/Library/LaunchAgents`, `/Library/LaunchDaemons`
- `/Library/Application Support/<Name>`, `/Library/PrivilegedHelperTools/<bundleid-ish>`
- `/private/var/db/receipts/<pkgid>.{bom,plist}` — pkg receipts; `pkgutil --pkgs` lists them,
  `pkgutil --pkg-info <id>` gives install location and volume; `pkgutil --files <id>` lists
  the installed file paths. Receipts survive app deletion, so a receipt whose files are all
  gone is an orphan signal, and one whose files exist but whose app bundle is gone points at
  leftover support files.

### Matching and confidence
Scored per candidate owner directory:

| Evidence | Strength |
|---|---|
| Container metadata plist names an installed bundle id | strong |
| Exact bundle id match to an installed app | strong |
| Team id match to an installed app (Group Containers) | strong |
| Cask name maps to the directory name | strong |
| Vendor prefix matches an installed app's vendor and the suffix is a known product alias | likely |
| Display name match in Application Support / Logs | likely |
| Curated alias table hit (e.g. `Code` → VS Code, `Cursor`, `JetBrains`, `Google/Chrome`) | likely |
| pkg receipt with no surviving app bundle | orphan-likely |
| Bundle id present only in the LaunchServices registry, no bundle on disk | corroborating only, never sufficient alone (the registry retains entries from disk images, Trash, Downloads, backups) |
| No match | unknown |

**Negative evidence (keep signals), applied before any orphan verdict:**

| Evidence | Effect |
|---|---|
| Directory mtime, or newest file mtime, within the last 30 days | something still writes here → not orphaned |
| A LaunchAgent/LaunchDaemon plist whose `Program`/`ProgramArguments[0]` path exists | active daemon → not orphaned |
| Name matches a Homebrew formula, a CLI tool with a known config dir, or a Node/Python package | **non-app software** class: CLI tools, formulae, and daemons legitimately own Application Support and Caches directories with no bundle anywhere (verified on the planning machine: `stremio-server` 2.8 GB, `com.wondershare.Installer` 494 MB). Attributed to "CLI / non-bundle software", never flagged orphan |
| Any `.app` anywhere on the volume with a matching bundle id or vendor prefix | installed |

Rules:
- Anything under `com.apple.*`, `group.com.apple.*`, or Apple team ids is **excluded** from
  orphan detection and attributed to macOS.
- Directories owned by dev/container detectors are claimed by them first (see conflicts, 03).
- The alias table keys on **vendor display names** as well as bundle ids and product names
  (verified: `~/Library/Caches/Smart Code ltd` style directories coexist with bundle-id ones).
- `codesign` team-id extraction runs only when an unattributed Group Container exists; that
  is its sole consumer. Results are cached per bundle id + version.
- The result for each owner is: installed (with the app), orphan-likely (with evidence), or
  unknown owner. Never a bare verdict.
- The curated alias table is the one part that is never fully generic. Start with the top
  ~50 developer-relevant apps and grow it from real scans.

### Per-app footprint
`footprint(app) = bundle size + Σ linked data`, shown in the Apps view with the split.
Note in the UI that Apple's "Applications" number counts bundles only, so ours is larger by
design.

## Developer detectors

Principle: ask the tool for its paths; fall back to defaults. Every probe has a timeout
(default 5 s, `brew` 15 s) and runs during the walk.

| Detector | Probe | Paths / facts | Reclaim |
|---|---|---|---|
| **homebrew** | Four separate probes `brew --prefix`, `brew --cellar`, `brew --caskroom`, `brew --cache` (verified: combining flags in one invocation prints usage and ignores the rest; each alone is instant), `brew list --formula --versions` (0.2 s), `brew cleanup -n` (0.7 s; prints per-formula lines, parse "Would remove" sizes). Cask listing via Caskroom dir, see Apps | `/opt/homebrew` or `/usr/local`: Cellar per formula and version, Caskroom, `~/Library/Caches/Homebrew`, `~/Library/Logs/Homebrew`. Flag multiple installed versions of the same formula | old versions: tool-managed; cache: regenerable |
| **node** | `npm config get cache`, `pnpm store path` (verified → `~/Library/pnpm/store/v10`; **also** `~/Library/Caches/pnpm` is a separate metadata cache, verified 1.4 GB, and superseded store generations may coexist: flag "superseded store version"), `yarn cache dir` for Yarn 1.x (→ `~/Library/Caches/Yarn/v6`) but `yarn config get cacheFolder` for Berry (version-gate on `yarn --version`), nvm dir from `$NVM_DIR`, `fnm`/`volta` dirs. **Verified pitfalls:** `bun pm cache` fails outside a project directory, so use the default `~/.bun/install/cache`; `nvm` is a shell function not a binary, so read `$NVM_DIR/alias/default` and resolve the `node` symlink instead of calling `nvm current` | `~/.npm/_cacache`, `~/.pnpm-store` or `~/Library/pnpm`, `~/Library/Caches/Yarn` or `.yarn/berry/cache`, `~/.bun/install/cache`, `~/.nvm/versions/node/*` with sizes and `nvm current` marker, `~/.volta`, `~/.local/share/fnm` | caches: regenerable; old node versions: tool-managed |
| **python** | `pyenv root`, `pyenv version-name`, `uv cache dir`, `uv python dir`, `pip cache dir`, `poetry config cache-dir`, conda base | `~/.pyenv/versions/*` with current marker, `~/.cache/uv`, `~/.local/share/uv/python`, `~/Library/Caches/pip`, `~/Library/Caches/pypoetry`, `~/miniconda3`/`~/anaconda3` pkgs | caches: regenerable; interpreters: tool-managed |
| **rust** | `rustup toolchain list`, `$CARGO_HOME` | `~/.cargo/registry/{cache,src,index}`, `~/.cargo/git`, `~/.rustup/toolchains/*` with default marker, `~/.rustup/downloads` | registry: regenerable; toolchains: tool-managed |
| **go** | `go env GOCACHE GOMODCACHE GOPATH GOROOT` | `~/Library/Caches/go-build`, `~/go/pkg/mod`, `~/go/bin`, Homebrew go versions | build cache: regenerable; modcache: regenerable |
| **ruby** | `gem env gemdir`, rbenv/rvm dirs | `~/.gem`, `/Library/Ruby/Gems`, `~/.rbenv/versions`, `~/.rvm` | tool-managed |
| **jvm** | none needed | `~/.gradle/{caches,wrapper,daemon}` (owned here only; removed from cli-tools), `~/.m2/repository`, `~/.sdkman/candidates`, `~/Library/Java/JavaVirtualMachines`, `~/Library/Android/sdk` (system-images, emulator AVDs in `~/.android/avd`) | gradle/m2 caches: regenerable; SDKs: tool-managed |
| **xcode** | `xcode-select -p` (on the planning machine this is `/Library/Developer/CommandLineTools`, i.e. **no Xcode installed**; the detector must degrade to path rules and `simctl` cannot be validated here), `xcrun simctl list devices -j` (mark `unavailable`), `xcrun simctl runtime list -j` | `~/Library/Developer/Xcode/{DerivedData,Archives,iOS DeviceSupport,watchOS DeviceSupport,tvOS DeviceSupport,UserData/Previews}`, `~/Library/Developer/CoreSimulator/{Devices,Caches}`, `~/Library/Developer/XCTestDevices`, `~/Library/Caches/com.apple.dt.Xcode`, `/Library/Developer/CommandLineTools`, simulator runtime images in `/Library/Developer/CoreSimulator/Images` (the bytes; `/Library/Developer/CoreSimulator/Volumes` is where they are *mounted* and is skipped by the mount guard). Probes `xcrun simctl runtime list -j` and `simctl list devices -j` are unverified here: no Xcode on the planning machine | DerivedData/Caches: regenerable; DeviceSupport/unavailable sims: tool-managed; Archives: user-data |
| **ide** | none | VS Code: `~/.vscode/extensions`, `~/Library/Application Support/Code/{CachedData,CachedExtensionVSIXs,User/workspaceStorage,logs}`; Cursor: `~/.cursor`, `~/Library/Application Support/Cursor/*` same shape; JetBrains: `~/Library/Caches/JetBrains`, `~/Library/Application Support/JetBrains`, `~/Library/Logs/JetBrains`; Toolbox apps under `~/Library/Application Support/JetBrains/Toolbox/apps`; Neovim `~/.local/share/nvim` (Mason servers, verified 1.7 GB), `~/.cache/nvim`; agents: Claude Code `~/.claude`, Codex `~/.codex` and `~/.cache/codex-runtimes` (verified 1.6 GB), Antigravity `~/.antigravity` (verified 2.5 GB), Gemini `~/.gemini`; Zed, Nova similar | CachedData/logs/workspaceStorage: regenerable; extensions: tool-managed |
| **aimodels** | `ollama list` if present | `~/.ollama/models`, `~/.cache/huggingface/hub`, `~/.cache/torch`, `~/.lmstudio/models`, `~/Library/Application Support/LM Studio` | tool-managed |
| **cli-tools** | none | `~/.cache/*` generic (attribute by subdir name), `~/.local/share/*`, `~/.terraform.d/plugin-cache`, `~/.pulumi/plugins`, `~/.kube/cache`, `~/Library/Caches/ms-playwright`, `~/Library/Caches/Cypress`, `~/Library/Caches/puppeteer`, `~/.deno`, `~/.wasmtime` | mostly regenerable |
| **electron-updaters** | none | `~/Library/Caches/*-updater`, `~/Library/Caches/*.ShipIt` (Squirrel.Mac). Verified ~2 GB on the planning machine: Lens 1.07 GB, Bitwarden 348 MB, Notion 297 MB + 242 MB. **As shipped, this is not a separate detector**: the two patterns are catalog rules in bucket 3 (App data, category "Updater cache", regenerable), and ownership resolution for a name the `*-updater`/`.ShipIt` glob doesn't cleanly parse (`com.google.GoogleUpdater` has no hyphen; `com.google.antigravity.ShipIt` names a product with no installed bundle here) falls to the `apps` detector's higher-precedence alias-table lookup, which runs on the same nodes | regenerable; attributed to the app by name/bundle id when possible |
| **ecosystems** (docs' "other ecosystems") | none | Flutter/Dart `~/.pub-cache`, `~/flutter/bin/cache`; .NET `~/.nuget/packages`, `~/.dotnet`; Haskell `~/.ghcup`, `~/.stack`, `~/.cabal`; Bazel `/private/var/tmp/_bazel_<user>`; `~/.ccache`; `~/.conan2`; SwiftPM `~/Library/Caches/org.swift.swiftpm`; `~/.cocoapods/repos`; Elixir `~/.mix`, `~/.hex`; PHP `~/.composer/cache`; Zig `~/.cache/zig`. **The detector, its package directory, its cache section key, and its `--disable-detector` name are all `ecosystems`, not `other-ecosystems`** — every other detector's name matches its package directory, and the name is what a user types on the command line | caches: regenerable; SDKs: tool-managed |
| **nix** | `nix store info` if present | `/nix/store`, `/nix/var` | tool-managed (`nix-collect-garbage -d` later). Moved here from Containers: Nix is a package manager |

## Projects: build artifacts under code roots

- Roots: `code_roots` from config, default `~/code`. Also scan `~/Developer`, `~/Projects`,
  `~/src`, `~/dev`, `~/work` if they exist.
- A **project** is the nearest ancestor containing `.git`, or a manifest (`package.json`,
  `go.mod`, `Cargo.toml`, `pyproject.toml`, `Package.swift`, `pom.xml`, `build.gradle*`).
- Artifact directories (never descended into beyond sizing): `node_modules`, `.pnpm-store`,
  `target`, `dist`, `build`, `out`, `.next`, `.nuxt`, `.svelte-kit`, `.turbo`, `.parcel-cache`,
  `.cache`, `.venv`, `venv`, `env`, `__pycache__`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`,
  `.tox`, `.gradle`, `.idea`, `DerivedData`, `.build` (SwiftPM), `Pods`, `.terraform`,
  `.serverless`, `.dart_tool`, `coverage`, `.nyc_output`, `.eggs`, `*.egg-info`, `.zig-cache`,
  `zig-out`, `.direnv`, `.devenv`, `vendor` (Go/PHP — user-data unless `vendor/modules.txt`).
- Source control: `.git/objects` sized separately as "repo history" (user-data). Detect
  `.git` worktrees and submodules to avoid double attribution.
- Per project: total artifact bytes, artifact breakdown, **last activity** = latest of
  `git log -1 --format=%ct` (fast, only if `.git` present) and the project root mtime. The
  Developer view sorts stale projects with large artifacts to the top.
- Reclaim: artifacts are `regenerable`; source and `.git` are `user-data`.

## Containers and VMs

Show two numbers per runtime: **host allocated** (the sparse disk image's real blocks) and
**guest reported** (what the daemon believes it uses). The gap is space freed inside the VM
that the host has not reclaimed.

| Detector | Probe | Paths | Notes |
|---|---|---|---|
| **orbstack** | `orb list` (verified: prints `[]` when no Linux machines), `docker context ls` (verified: context named `orbstack` with socket `~/.orbstack/run/docker.sock`), `docker system df --format json` (verified: one JSON object per line with **human-formatted strings** like `"Size":"9.853GB"`, `"Reclaimable":"5.379GB (54%)"`; parse units), `docker system df -v` for per-image detail | Disk image at `~/Library/Group Containers/HUAQ24HBR6.dev.orbstack/data/data.img.raw` (verified sparse: 245 GB apparent / 18.8 GB allocated) plus `swap.img`; `~/.orbstack` is config/run/log only; `~/Library/Caches/dev.orbstack*` | Measured 2026-09-23: the daemon reports 19.83 GB across images/containers/volumes/build cache vs 18.76 GB allocated on host — the image is sparse and its contents compress, so the guest total is the larger of the two; both numbers are shown, never reconciled into one |
| **docker** (Desktop) | `docker context ls`, `docker system df --format json` per context; `-v` combined with `--format` is historically unsupported in the Docker CLI, so run `docker system df -v` unformatted and parse the table when per-image detail is wanted. **As shipped, the detector is gated on `/Applications/Docker.app` or `~/Library/Containers/com.docker.docker` existing on disk — never on a context named `desktop-linux`/`default`.** Docker Desktop itself is **not installed on this machine** (OrbStack owns `/var/run/docker.sock` here, and its own context is named `orbstack`, not `default`), so this detector reports `missing` cleanly and is written from documentation and validated later | `~/Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw` (sparse), `~/.docker` (config, buildx, scout cache) | If both OrbStack and Desktop exist, attribute by docker context; images/containers/volumes/build-cache split from `system df` |
| **colima / lima** | `colima list --json`, `limactl list --json` | `~/.colima`, `~/.lima` | disk images sparse |
| **podman** | `podman machine list --format json` | `~/.local/share/containers/podman/machine` | |
| **vms** | none | UTM: `~/Library/Containers/com.utmapp.UTM/Data/Documents/*.utm`; Parallels: `~/Parallels/*.pvm`; VMware: `~/Virtual Machines.localized/*.vmwarevm`; VirtualBox: `~/VirtualBox VMs` | bundles as leaves; sizes allocated |
| **kubernetes** | none | `~/.kube/cache`, `~/.minikube`, `~/.kind`, `~/.rd` (Rancher Desktop) | |

Reclaim: dangling images, stopped containers, build cache → `tool-managed`; volumes → `user-data`
(named volumes can hold databases); disk image slack → `tool-managed` with the runtime's
compaction/shrink advice.

## Backups and snapshots
- iOS/iPadOS backups: `~/Library/Application Support/MobileSync/Backup/<udid>`; read
  `Info.plist` inside for device name and last backup date (needs Full Disk Access).
- Time Machine local snapshots: `tmutil listlocalsnapshots /`; sizes not directly
  available; report count and dates. `tmutil thinlocalsnapshots` is the phase-two action.
- Xcode archives (`~/Library/Developer/Xcode/Archives`) are treated as backups of builds
  (user-data).

## Personal files
Straight rules, `user-data`, never flagged for deletion:
- `~/Documents`, `~/Desktop`, `~/Downloads` (Downloads additionally lists `.dmg`, `.pkg`,
  `.zip`, `.iso` older than 30 days as a "quick wins" hint, still user-data; age from the
  `com.apple.quarantine` xattr download timestamp when present, else mtime), `~/Pictures`
  (Photos library bundle as one row), `~/Movies`, `~/Music`, `~/Public`.
- `~/Library/Mail`, `~/Library/Messages` (attachments), `~/Library/Mobile Documents`
  (iCloud Drive; dataless-aware), `~/Library/CloudStorage/*` (Dropbox, Google Drive,
  OneDrive via File Provider; dataless-aware), `~/Library/Containers/com.apple.*` (Apple app
  data such as Books, Podcasts, TV downloads: attributed to the Apple app by name).

## System caches and logs
Rules, `system` or `regenerable`:
- `~/Library/Caches/*` not claimed by a detector → attributed by bundle id to installed app
  if possible, else "Caches (unknown owner)". Regenerable.
- `/Library/Caches`, `/private/var/folders/*/*/C` (root and other users need sudo),
  `/private/var/log`, `/private/var/db/diagnostics`, `/private/var/db/DiagnosticPipeline`,
  `~/Library/Logs`, `~/Library/Logs/DiagnosticReports`, `/Library/Logs`.
- `/private/var/db/dyld`, `/System/Volumes/Data/.Spotlight-V100`, `.fseventsd`,
  `.DocumentRevisions-V100`, `/Library/Updates`, `/private/var/db/softwareupdate`.
- Rosetta and macOS installer leftovers: `/Applications/Install macOS *.app` flagged
  (tool-managed: "delete the installer app after upgrading").

## Rule catalog: initial coverage target
- ~150 declarative rules across the buckets above, each with id, explanation, and reclaim tag.
- Acceptance: on the planning machine, bucket 11 "Other" is under 5 % of used space after
  the first tuning pass.
