# 02 — Storage Model

## The ledger

The top-level output is a ledger. Every scanned byte is assigned to exactly one top-level
bucket. The buckets, plus the non-scanned buckets derived from `statfs`/`diskutil`, sum to
the volume's reported used space.

| # | Bucket | Contents | Source |
|---|---|---|---|
| 1 | **macOS** | Sealed system volume, Preboot, VM (swap), Update, Hardware volumes | `statfs` per volume, never walked |
| 2 | **Applications** | `.app` bundles in `/Applications`, `~/Applications`, Homebrew Caskroom, Setapp. Per-app footprint links bucket 3 items to the app | Walk + app detector |
| 3 | **App data** | Library data attributed to an app: Containers, Group Containers, Application Support, Caches, Preferences, Saved Application State, HTTPStorages, WebKit, Logs, LaunchAgents, Application Scripts, pkg receipts. Sub-split: installed / likely orphaned / unknown owner | Walk + app linking |
| 4 | **Developer** | Toolchains and package caches (brew, npm, pnpm, yarn, bun, nvm, pyenv, uv, pip, cargo, rustup, go, gem, gradle, maven, cocoapods, nix), Xcode (DerivedData, Archives, DeviceSupport, Simulators), IDE and agent data (VS Code, Cursor, JetBrains, Neovim, Claude Code, Codex, Antigravity), AI model caches (Ollama, Hugging Face, LM Studio), project build artifacts under code roots | Walk + dev detectors |
| 5 | **Containers & VMs** | OrbStack, Docker Desktop, Colima/Lima, Podman, UTM, Parallels, VMware Fusion | Walk + container detectors |
| 6 | **Personal files** | Documents, Desktop, Downloads, Pictures + Photos library, Movies, Music, Mail, Messages attachments, local iCloud Drive, other cloud storage (File Provider) | Walk |
| 7 | **Backups** | iOS device backups (`MobileSync/Backup`), local Time Machine snapshots | Walk + `tmutil` |
| 8 | **System caches & logs** | `/Library/Caches`, `/private/var/folders`, `/private/var/log`, `/private/var/db/diagnostics`, `~/Library/Logs`, Spotlight index, `/Library/Updates`, font/icon/dyld caches | Walk (some require sudo) |
| 9 | **Trash** | `~/.Trash`, per-volume `.Trashes` | Walk |
| 10 | **Purgeable** | APFS purgeable space | `getattrlist` on the volume with `ATTR_VOL_SPACEUSED` / `ATTR_VOL_SPACEAVAIL` compared against `statfs`, or Foundation's `NSURLVolumeAvailableCapacityForImportantUsageKey` via cgo. **Verified:** `diskutil info` prints no purgeable field on macOS 26, so it is not a source. If neither API yields a number, this bucket is folded into 12 and labeled unknown rather than shown as zero |
| 11 | **Other** | Scanned but matched no rule. Should be small; large "Other" is a signal to add rules | Walk |
| 12 | **Unreadable / unaccounted** | (a) directories that returned EPERM/EACCES, listed individually; (b) filesystem metadata: APFS directory inodes and B-tree structures report **zero blocks via lstat** (verified), so several GB of metadata is expected and is shown as a named line; (c) container-level overhead: per-volume used sums to less than container used (verified ~6 GB gap); (d) the residual `statfs` used − scanned − purgeable − snapshots − (b) − (c). The residual can go negative when APFS clones inflate the scanned sum (verified: `du` reports 191 GB against 178 GB used); shown as "shared/cloned blocks" in that case | Derived |

Bucket 12 is deliberate. Without Full Disk Access a terminal cannot read Mail, Messages,
Safari, Time Machine, and several app containers. Root-owned parts of `/private/var` need
sudo. The tool must say what it could not see and what would fix it, never silently
undercount.

## Reclaimability tags

Every classified item carries a reclaimability class from day one, even though phase one
does not act on it:

| Tag | Meaning | Examples |
|---|---|---|
| `regenerable` | Safe to delete; the tool rebuilds it on demand | npm/pnpm/yarn/bun caches, Go build cache, cargo registry, DerivedData, Homebrew download cache, `node_modules`, `target/`, `.venv` |
| `tool-managed` | Reclaim via the owning tool's own command | Docker images/volumes/build cache, old Homebrew versions, unavailable simulators, unused nvm/pyenv/rustup versions |
| `orphaned` | Owner app appears to be gone | Containers / Application Support for uninstalled apps |
| `user-data` | Never suggest deletion | Documents, Photos library, Mail, projects' source |
| `system` | Managed by macOS; leave alone | Sealed volume, swap, `/private/var/db` |
| `unknown` | Not enough information | Anything in bucket 11 |

This lets the phase-one report say "40 GB in Developer, 28 GB of it regenerable" and gives
phase two a safe foundation.

## macOS filesystem semantics we must get right

### Volumes, firmlinks, and nested mounts
- The boot disk is an APFS container with several volumes: the sealed **system** volume
  mounted at `/`, the **data** volume at `/System/Volumes/Data`, plus Preboot, Recovery,
  VM, Update, Hardware.
- Paths like `/Applications`, `/Users`, `/Library`, `/private/var` on `/` are **firmlinks**
  into the data volume. A naive walk from `/` either double counts or, with a
  "don't cross devices" rule, skips almost everything.
- **Decision:** walk `/System/Volumes/Data` as the single scan root. Report the system
  volume and the other small volumes as fixed numbers from `statfs`. Skip
  `/System/Volumes/*` other than Data, `/Volumes/*`, `/dev`, `/Network`, and any mount point.
- **Verified 2026-09-22 (macOS 26 / Darwin 25):** the system volume (`/`) and the data volume
  (`/System/Volumes/Data`) report the **same `st_dev`** (APFS volume group), while VM and
  Preboot report distinct ones. So `st_dev` alone cannot separate system from data content
  and cannot stop a walk from `/` crossing a firmlink. Mount-boundary detection must use the
  mount table (`getfsstat` → list of `f_mntonname`) and compare paths against mount points,
  with `st_dev` as a secondary check for external and virtual volumes. Walking only the data
  volume root sidesteps the firmlink problem entirely.
- Inode numbers on the data volume exceed 2^60; store as `uint64`.
- **Mounts nested inside the scan root (verified):** `/System/Volumes/Data/home` (autofs)
  and `~/OrbStack` (NFS export of the OrbStack VM, `OrbStack:/OrbStack`). A naive walk enters
  the NFS mount and double counts the container data (verified: `du` returns 20.8 GB there,
  the same bytes as the host disk image). The walker must load the mount table at start and
  refuse to descend into any path that is a mount point other than the scan root. Nested
  mounts are listed in the report as "mounted volumes skipped" with their type.
- **Per-volume `statfs` convention (verified):** every APFS volume in the container reports
  the same total and the same free space because free space is container-wide. Only
  per-volume *used* is meaningful. The ledger uses container total, container free, and
  per-volume used; the gap between the sum of per-volume used and container used is the
  "container overhead" line in bucket 12.
- **Drift (verified):** used space changed by ~0.5 GB between two readings a minute apart on
  an idle machine. Capture `statfs` before and after the walk, show both, and derive the
  reconciliation tolerance from the observed drift instead of a fixed number.
- Present paths to the user in their familiar form (`/Users/...`, `/Applications/...`) by
  stripping the `/System/Volumes/Data` prefix.

### Sizes
- Use `st_blocks * 512` (allocated), never `st_size` (apparent). Sparse files
  (OrbStack `data.img`, Docker `Docker.raw`, VM disks, sparsebundles) differ by tens of GB.
- APFS **clones** (copy-on-write duplicates) report full `st_blocks` on both copies, so the
  scanned sum can exceed real usage. Unavoidable without per-extent inspection; absorbed by
  bucket 12 and explained.
- **Hard links**: dedupe by `(st_dev, st_ino)` when `st_nlink > 1`. Homebrew and nvm use
  them heavily.
- **Symlinks**: never followed. Counted as their own tiny size.
- **Directory inodes**: **verified to report `st_blocks = 0` on APFS.** Directory metadata
  is invisible to lstat and lands in bucket 12 as the named "filesystem metadata" line. Do
  not attempt to sum it.

### Dataless files (iCloud Drive, File Provider clouds)
Verified on the planning machine: evicted iCloud files show `st_flags` with `SF_DATALESS`
(`0x40000000`) set and `st_blocks = 0` while `st_size > 0`. Detect via the flag, **not** by
inferring from zero blocks, because locally present transparently compressed files
(`UF_COMPRESSED`) can also show small block counts. Dataless *directories* also exist under
File Provider roots, so the materialization policy matters for `ReadDir` as well as reads.
- Files under `~/Library/Mobile Documents`, `~/Library/CloudStorage`, and any evicted iCloud
  Desktop/Documents item may be **dataless**: metadata present, content in the cloud.
- `lstat` on them is safe. **Opening them triggers a download.** The walker never opens
  files, and additionally sets the process IO policy
  `IOPOL_TYPE_VFS_MATERIALIZE_DATALESS_FILES = OFF` via `setiopolicy_np` as a safety net.
- Dataless files show `st_blocks = 0`, so allocated-size accounting is naturally correct.
  Report "N files, X GB in cloud, not on disk" for these locations.

### Bundles as leaves
- Directories that macOS treats as a single object are sized fully but shown as one row and
  not drilled into by default: `.app`, `.framework`, `.photoslibrary`, `.musiclibrary`,
  `.tvlibrary`, `.xcodeproj`, `.xcworkspace`, `.playground`, `.pvm`, `.utm`, `.vmwarevm`,
  `.sparsebundle`, `.sparseimage`, `.dmg`, `.pkg`, `.savedState`, `.download`, `.appex`,
  `.qlgenerator`, `.kext`, `.bundle`, `.plugin`, `.prefPane`, `.lproj`.
- A toggle in the TUI opens them.

### Permissions (TCC and root)
- Without **Full Disk Access** for the terminal app, reads of `~/Library/Mail`,
  `~/Library/Messages`, `~/Library/Safari`, `~/Library/Cookies`, some
  `~/Library/Containers/*`, `~/Library/Application Support/MobileSync`, and Time Machine
  data fail with EPERM.
- Root-owned trees (`/private/var/db`, `/private/var/folders/zz`, `/Library/Caches` parts,
  other users' homes, `.Spotlight-V100`) fail with EACCES unless run with sudo.
- **Data vaults (verified):** some paths are denied even with Full Disk Access, e.g.
  `~/Library/Caches/com.apple.ap.adprivacyd` and the container metadata plists inside
  `~/Library/Containers/*`. These are reported as "protected by the system" rather than as
  fixable permission problems.
- **First-run prompts:** a terminal without permission for Desktop, Documents, or Downloads
  normally triggers a one-time system dialog on first access. Document this in `doctor` and
  the README; it is expected, not a bug.
- **Decision:** run unprivileged by default. Collect every failed directory with its errno.
  Report them in bucket 12 with two hints: "grant Full Disk Access to <terminal app>" and
  "run `sudo storix scan --system` to include root-only system directories". Detect the
  terminal via `TERM_PROGRAM` / parent process bundle for the hint.
- sudo mode must write the cache with the invoking user's ownership (`SUDO_UID`/`SUDO_GID`).

### Units
- Finder and System Settings use **decimal** (1 GB = 1,000,000,000 bytes). Default to
  decimal so our numbers line up with Apple's. `--binary` flag for GiB.

### Special locations worth naming in the rules
- `/private/var/vm` — swap files (bucket 1).
- `/private/var/folders/<xx>/<hash>/{C,T}` — per-user cache and temp; identify the current
  user's via `getconf DARWIN_USER_CACHE_DIR` / `DARWIN_USER_TEMP_DIR`.
- `/private/var/db/receipts` — pkg receipts, used for orphan detection, tiny in size.
- `/Library/Updates`, `/Library/Apple/System/Library/Receipts` — macOS update leftovers.
- `/Applications/Install macOS *.app` — installers, often 12+ GB.
- `~/Library/Application Support/MobileSync/Backup` — iOS backups.
- `~/Library/Developer/Xcode/{DerivedData,Archives,iOS DeviceSupport,watchOS DeviceSupport}`
  and `~/Library/Developer/CoreSimulator/{Devices,Caches}`.
- `~/Library/Caches/com.apple.dt.Xcode`, `~/Library/Caches/CocoaPods`, `~/Library/Caches/go-build`,
  `~/Library/Caches/pip`, `~/Library/Caches/Yarn`, `~/Library/Caches/Homebrew`,
  `~/Library/Caches/ms-playwright`, `~/Library/Caches/Cypress`, `~/Library/Caches/puppeteer`.
- `/opt/homebrew` (arm64) and `/usr/local` (Intel / Rosetta Homebrew).
- `/private/tmp`, `/private/var/tmp` (Bazel output base lives at `/private/var/tmp/_bazel_<user>`).
- `~/Library/Caches/*-updater`, `~/Library/Caches/*.ShipIt` — Electron/Squirrel updater
  caches (verified ~2 GB on the planning machine across Lens, Notion, Bitwarden).
- `/nix` — Nix store if present.
- `~/Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw` — Docker Desktop disk.
- `~/Library/Group Containers/HUAQ24HBR6.dev.orbstack/data/data.img.raw` — OrbStack disk
  image (verified: 245 GB apparent, 18.8 GB allocated on the planning machine). `~/.orbstack`
  holds only config, run sockets, logs, and shell integration.
