# 03 — Architecture

## Overview

```
                 ┌──────────────┐
  statfs/diskutil│  volumes     │──────────────────────────────┐
  tmutil         └──────────────┘                              │
                                                               ▼
  ┌──────────────┐   size tree   ┌──────────────┐   ledger   ┌──────────────┐
  │   walker     │──────────────▶│  classifier  │───────────▶│   report     │
  │ (parallel    │               │  (rules +    │            │  TUI / text  │
  │  lstat)      │               │   detectors) │            │  / JSON      │
  └──────────────┘               └──────────────┘            └──────────────┘
         │                              ▲
         ▼                              │ external facts
  ┌──────────────┐               ┌──────────────┐
  │  scan cache  │               │  probes      │  brew, pnpm, go env, uv,
  │  (on disk)   │               │  (shell out, │  docker system df, orb,
  └──────────────┘               │   timeouts)  │  simctl, pkgutil, lsregister
                                 └──────────────┘
```

Two clean layers:

1. **Walker** — produces a size tree of the data volume. Knows nothing about apps or tools.
   Fast, parallel, correct about macOS filesystem semantics (see 02).
2. **Classifier** — consumes the tree plus facts gathered by **probes** (shell commands) and
   assigns every node to a bucket, an owner, a reclaimability tag, and a provenance record.
   Mostly declarative rules; a few imperative **detectors** for apps, dev tools, containers.

Probes run **concurrently with the walk**, each with a timeout, so a slow `brew` or a
missing `docker` never stalls the scan.

## Walker

- Root: `/System/Volumes/Data` (see 02 for why). Additional roots configurable (external
  volumes opt-in).
- Traversal: `os.ReadDir` + `Lstat` per entry via `syscall.Stat_t` to get `Blocks`, `Ino`,
  `Dev`, `Nlink`, `Mode`, `Flags`, `Mtimespec`. Optimization for later: `getattrlistbulk`
  returns names + attributes for a whole directory in one syscall and is materially faster
  than readdir + per-entry lstat on APFS. **Verified:** `golang.org/x/sys/unix` v0.48 has no
  `Getattrlistbulk` wrapper, so this would be a raw `syscall.Syscall6` or a small cgo shim.
  Phase 1a uses ReadDir + Lstat; the bulk path is a measured optimization, not a dependency.
- Concurrency: bounded parallelism with **no bounded channel between workers and the queue**.
  Workers that push child directories onto a bounded channel can all block on send with no
  receiver left, which deadlocks. Use either a semaphore over recursive goroutines
  (`errgroup.SetLimit`) or a mutex-guarded stack plus a `WaitGroup`. Start with
  `min(2*NumCPU, 16)` in flight; measure. The walk is latency-bound, not CPU-bound
  (verified: `du` on `~/Library` ran at ~42k entries/s with 6 s of system time out of 29 s),
  so parallelism is where the win is.
- Never descend into another mount point (mount table from `getfsstat`, see 02; `st_dev`
  is not sufficient on APFS volume groups). Never follow symlinks. Never `open()`.
- Hard-link dedupe: a concurrent map of `(dev, ino)` for entries with `nlink > 1`. Attribution
  must be **deterministic**, not "first sighting", because a parallel walk changes the winner
  per run and would break diffs and tests. Rule: bytes go to the lexicographically smallest
  path among the links; other links record zero bytes but keep the path and a pointer to the
  owner. Resolved after the walk from the collected link set. **Verified:** the heavy
  hard-link user on the planning machine is pnpm (store ↔ `node_modules`, 9.4 GB store),
  not nvm. Test dedupe against a pnpm project specifically.
- Skip list (hard-coded, never walked): `/System/Volumes/{Preboot,VM,Update,Hardware,xarts,iSCPreboot,Recovery}`,
  `/Volumes`, `/dev`, `/Network`, `/.vol`, `/private/var/vm` (sized via statfs of VM
  volume), `.fseventsd`, `.Spotlight-V100` (sized as one node if readable), `.DocumentRevisions-V100`.
  Plus **every mount point from `getfsstat`** other than the scan root, discovered at
  runtime (verified nested mounts: `/System/Volumes/Data/home` autofs, `~/OrbStack` NFS).
- Errors: every `EPERM`/`EACCES`/`ENOENT`-during-walk is recorded on the node as
  `unreadable` with errno. Parent totals mark themselves partial.
- Progress: emits counters (dirs, files, bytes, errors, current path) on a channel for the
  TUI spinner/progress view.
- Output: an in-memory tree of `Node{Name, Bytes, Apparent, Files, Dirs, Mtime, Flags, Children}`
  plus a flat index for path lookup. Files below a configurable threshold (default **64 KB**)
  are aggregated into their parent to bound memory; the aggregate keeps count and bytes.
  The comparison is against `max(allocated, apparent)`, not allocated alone, so an in-cloud
  file (0 allocated blocks but a large logical size) or a compressed file (small allocated,
  large apparent) stays visible as its own node rather than disappearing into the aggregate.
  Directories are always kept. **Aggregation must not destroy classifier evidence:** small
  per-app files (preference plists, ByHost plists, `.binarycookies`, LaunchAgent plists, pkg
  receipts) are exactly what the app detector keys on. Two safeguards: (1) an exemption list
  of path prefixes where every file is retained (`~/Library/Preferences`, `~/Library/Cookies`,
  `~/Library/LaunchAgents`, `/Library/LaunchAgents`, `/Library/LaunchDaemons`,
  `~/Library/Saved Application State`, `/private/var/db/receipts`); (2) classification rules
  that match file names run **during the walk**, before aggregation, and record their claim on
  the parent aggregate. Note this is stricter than D19's hard-link rule: an aggregated
  hard-link alias keeps only its count in the parent's small-file bucket, not its path — only
  a retained node (above the threshold, or exempt) keeps a path for its alias flag.
- Baseline measured 2026-09-22 on the planning machine (~3.5 M files, Apple silicon):
  single-threaded `du -sk /System/Volumes/Data` takes **106 s warm** and reports 191 GB
  against 177.6 GB used per `statfs` (the excess is APFS clone inflation; see bucket 12).
  Target for the parallel walker: under 90 s cold, under 30 s warm, TUI responsive
  throughout. Earlier estimates of 10–20 s were optimistic and are withdrawn.

## Volumes and system facts

Collected once per scan, independent of the walk:

- `statfs` for every mounted volume → total/used/free per volume; identifies the data
  volume's device for `st_dev` checks.
- `diskutil info /System/Volumes/Data` (parsed) or `diskutil apfs list -plist` → purgeable
  space, container free space.
- `tmutil listlocalsnapshots /System/Volumes/Data` (the data volume, not `/`) and
  `diskutil apfs listSnapshots /System/Volumes/Data` (verified to accept a mount point) →
  snapshot names and dates; sizes are not directly exposed, reported as count with a note.
- `statfs` captured **before and after** the walk; both shown; drift defines tolerance.
- Terminal identity (`TERM_PROGRAM`, parent process) and a Full Disk Access probe (attempt
  `ReadDir` on `~/Library/Mail` or another TCC-protected path) to produce the permission hint.
  Note: inside tmux `TERM_PROGRAM=tmux`; walk up the process tree to the real terminal app.
- Dataless IO policy: `golang.org/x/sys/unix` exposes `SYS_IOPOLICYSYS` (322) but no
  `Setiopolicy` wrapper, so `setiopolicy_np(IOPOL_TYPE_VFS_MATERIALIZE_DATALESS_FILES,
  IOPOL_SCOPE_PROCESS, IOPOL_MATERIALIZE_DATALESS_FILES_OFF)` is a one-line cgo call or a raw
  syscall. It is defense in depth, not the primary control: for dataless *files* the walker
  never opens anything, so the policy is the only thing standing between a stray read and an
  iCloud download; for dataless *directories* `ReadDir` itself is the trigger, so the walker
  rule is stronger than the policy — **never list a directory flagged `SF_DATALESS`** (checked
  before `ReadDir`, not after), which holds even in a `CGO_ENABLED=0` build where the policy
  call is unavailable.

## Classifier

### Rule model
A rule is a declarative record:

```
Rule {
  Match:        path pattern (glob over the display path; `~` expands to home)
  Bucket:       one of the 12 ledger buckets
  Category:     sub-category (e.g. "Package cache", "Toolchain", "Simulators")
  Owner:        static owner label or a capture from the pattern (e.g. `~/Library/Caches/{bundleid}`)
  Reclaim:      regenerable | tool-managed | orphaned | user-data | system | unknown
  Explain:      one-line human explanation shown in the UI
  Priority:     specificity; deeper/more specific patterns win
}
```

- **First match wins, most specific first.** `~/Library/Caches/Homebrew` must go to
  Homebrew, not generic Caches.
- Rules live in an embedded catalog (Go structs or an embedded YAML/TOML file). Users can
  add overrides in a config file later.
- Classification is applied top-down: when a directory matches, its whole subtree inherits
  unless a more specific child rule matches. This keeps it O(nodes).

### Detectors
Imperative classifiers for things patterns cannot express. Each detector implements:

```
Detector interface {
  Name() string
  Probe(ctx) (Facts, error)         // shell out / read plists; runs concurrently with the walk
  Classify(tree, facts) []Claim     // attaches bucket/owner/reclaim/provenance to nodes
}
```

Detectors (details in 04):
- `apps` — installed app inventory, per-app footprint, orphan detection.
- `homebrew`, `node` (npm/pnpm/yarn/bun/nvm/volta/fnm), `python` (pyenv/uv/pip/poetry/conda),
  `rust` (cargo/rustup), `go`, `ruby`, `jvm` (gradle/maven/sdkman), `xcode`, `ide`
  (VS Code/Cursor/JetBrains/Claude Code), `aimodels` (Ollama/HF/LM Studio).
- `projects` — build artifacts under code roots.
- `orbstack`, `docker`, `colima`, `vms` (UTM/Parallels/VMware), `nix`.
- `backups` — iOS backups, snapshots.

### Provenance
Every claim records `{detector or rule id, evidence[]}`. Evidence strings are shown verbatim
in the TUI's "why" panel, e.g. `bundle id com.tinyspeck.slackmacgap matches /Applications/Slack.app`
or `pnpm store path reported by 'pnpm store path'`.

### Conflicts
Detectors can claim overlapping paths (e.g. `~/Library/Caches/com.docker.docker` by both
`apps` and `docker`). Resolution order: container/VM and dev detectors beat `apps`, which
beats generic rules. Conflicts are logged for rule tuning.

### Footprint is a view, not a bucket
Buckets are exclusive; the per-app **footprint** is not. VS Code's Application Support
directory (verified ~680 MB) is claimed by the `ide` detector into bucket 4, yet it belongs
in VS Code's footprint. Footprint is therefore a cross-cutting view that sums components
across buckets 2, 3, and 4 and labels each component with the bucket it landed in. It is
never added into the ledger.

## Scan cache

- Location: `~/Library/Application Support/storix/scans/<timestamp>.bin` plus `latest`
  symlink. Also `~/Library/Application Support/storix/config.toml`.
- Format: **benchmark before committing.** `~/Library` alone has ~1.2 M entries; reflection
  driven gob is unlikely to load a several-hundred-thousand-node tree in under a second.
  Plan for a flat, versioned encoding (node array + string table + parent indices) and
  measure gob against it in phase 1a. Targets to validate, not assume: < 100 MB, < 1 s load.
  The cache file carries the schema version and the storix version that wrote it.
- Purpose now: instant TUI drill-down and `--from-cache` re-render without rescanning.
  Purpose later: diffs between scans ("what grew since 2026-09-15").
- Freshness: bare `storix` loads the latest cache if it is under one hour old, was written by
  the same storix version, and used the same root set; otherwise it rescans. The header shows
  the cache age; `r` rescans.
- Retention: keep last N (default 10), configurable.
- sudo scans chown the file to the invoking user.

## Presentation

### TUI (Bubble Tea)
Views, switched with tabs/keys:

1. **Ledger** — the 12 buckets as rows with proportional bars, bytes, percent, and
   reclaimable sub-bar. Enter drills into a bucket.
2. **Browse** — ncdu-style directory table for any subtree: name, bar, size, files, owner
   chip, reclaim chip. Breadcrumb header. Keys: `enter`/`backspace` navigate, `o` open in
   Finder (`open -R`), `y` copy path, `b` toggle bundles-as-leaves, `s` sort, `/` filter,
   `?` help, `w` "why" side panel with provenance.
3. **Apps** — table of installed apps with footprint (bundle + linked data), sortable;
   second section for orphaned owners with confidence and evidence.
4. **Developer** — grouped by tool: cache/toolchain rows, versions with sizes and
   "current" marker; projects section listing build artifact dirs by project with last
   commit date.
5. **Containers** — per runtime: host image allocated size vs daemon-reported usage, split
   images/containers/volumes/build cache; machines list for OrbStack.
6. **Unaccounted** — unreadable directories with errno and the permission hints.

Scanning shows a progress view (spinner, counters, current path, elapsed) and transitions
into the Ledger when done.

**Divergence from the original design (phase 1b):** probes do not surface as pending chips that
update in place. `scan.Run` joins every detector's `Probe` (`run.Wait`, with a grace period for
stragglers) before classification runs once, so there is no "still running" state for the TUI to
render by the time a view exists to render it — a probe is either done by classification time or
counted as `Timeout`/`Degraded`. Per-detector timing and state are still shown, just as a finished
table (`storix doctor`, the why panel, `--debug`) rather than as live-updating chips.

### Non-interactive
- `--report` prints the ledger and each section statically with Lip Gloss styling (colors
  auto-disabled when not a TTY or `NO_COLOR`).
- `--json` emits the ledger, facts, errors, and the classified tree **limited by
  `--depth` (default 3) and `--min-size` (default 10 MB)**; `--full` removes the limits. The
  unlimited tree is hundreds of MB and is not a sensible default. Schema is versioned
  (`"schema": 1`).
- `--md` (nice-to-have) emits a Markdown report.

## CLI surface (phase one)

```
storix                      scan (or load fresh cache) and open the TUI
storix scan [--report|--json] [--system] [--roots PATH...] [--no-cache] [--binary]
storix explain PATH         what is this path, who owns it, is it reclaimable, and why
storix apps [--orphans]     installed apps with footprints; orphan candidates
storix dev                  developer breakdown (text)
storix cache {list|clear}   manage stored scans
storix doctor               permission check (FDA), sudo advice, tool availability
```

`sudo storix scan --system` includes root-only system directories and merges into the same
ledger. Everything else is identical.

## Configuration

`~/Library/Application Support/storix/config.toml`:

```toml
units = "decimal"            # or "binary"
code_roots = ["~/code"]      # scanned for project build artifacts
extra_roots = []             # additional volumes to walk (opt-in)
keep_scans = 10
small_file_threshold = "64KB" # files below this are aggregated into their parent
[detectors]
disabled = []                # e.g. ["vms"]
```

## Error handling posture

- The scan never aborts on a per-path error. Errors are data.
- A probe failure (tool missing, timeout, non-zero exit) marks that detector as
  `degraded` with the reason; its rules fall back to static default paths.
- Panics in a detector are recovered and reported; the scan still completes.
- Exit code 0 on a completed scan even with unreadable directories; non-zero only for
  configuration errors or an aborted walk.
