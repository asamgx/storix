# 07 — Decisions, Assumptions, Open Questions

## Decisions taken (2026-09-22)

| # | Decision | Rationale |
|---|---|---|
| D1 | Go + Charm stack (Bubble Tea, Lip Gloss, Bubbles, Fang over Cobra) | Fast, single binary, best-in-class TUI ergonomics; user preference |
| D2 | Scan root is `/System/Volumes/Data`; system volume reported via statfs, never walked; mount boundaries come from the `getfsstat` mount table, not `st_dev` | Firmlinks make a walk from `/` double count or skip; the sealed volume is a fixed cost. Verified: system and data volumes share one `st_dev`, and two mounts (`~/OrbStack` NFS, `/System/Volumes/Data/home` autofs) nest inside the scan root |
| D3 | Allocated size (`st_blocks*512`) everywhere; apparent size kept as secondary field | Sparse disk images and clones make apparent size wrong by tens of GB |
| D4 | Never open files; set dataless-materialization IO policy off | Speed; avoid iCloud / File Provider downloads |
| D5 | Decimal units default, `--binary` flag | Match Finder and System Settings so users trust the numbers |
| D6 | Unprivileged by default; `sudo storix scan --system` opt-in; unreadable paths reported explicitly | Honesty over silent undercount; most value is in user-owned data anyway |
| D7 | Two layers: walker (dumb, fast) and classifier (rules + detectors) | Testability; rules tunable without touching traversal |
| D8 | Declarative rules first, imperative detectors only where needed | Most attribution is path-shaped; keeps the catalog reviewable |
| D9 | Provenance on every claim; confidence levels for orphans | Heuristics must be explainable; no bare verdicts |
| D10 | Reclaimability tagged in phase one, acted on in phase two | Makes the phase-one report meaningful; safe foundation for cleanup |
| D11 | Persist every scan in `~/Library/Application Support/storix` | Instant drill-down; enables diffs later. Chosen over a dotfile next to the binary because it is the macOS convention, survives binary moves, and keeps `~` clean |
| D12 | Probes run concurrently with the walk, each with a timeout | `brew` alone takes seconds; never stall the scan |
| D13 | Bundles are leaves in the UI, fully sized | Matches how macOS presents them; avoids noise |
| D14 | No treemap; ncdu-style drill-down with bars | Proven UX, renders well in Lip Gloss, far less work |
| D15 | Internal disk only by default; external volumes opt-in | Avoid spinning up disks and slow network mounts |
| D16 | `com.apple.*` excluded from orphan detection | System components use reverse-DNS names too |
| D17 | Phase two acts via native tools and Trash, never `rm -rf` | Safety; tools know their own invariants |
| D18 | Small-file aggregation threshold 64 KB with exemption prefixes; filename rules run before aggregation | 1 MB would delete the evidence orphan detection depends on (plists, cookies, receipts) |
| D19 | Hard-link bytes go to the lexicographically smallest path | Deterministic across parallel runs; required for diffs and stable tests |
| D20 | Performance acceptance is a ratio against `du -sk`, not absolute seconds | Measured `du` warm is 106 s on the planning machine; the walk is latency-bound |
| D21 | Purgeable comes from `getattrlist`/Foundation or is shown as unknown; never from `diskutil info` | Verified `diskutil info` has no purgeable field on macOS 26 |
| D22 | Footprint is a cross-cutting view over buckets 2–4, never summed into the ledger | Keeps buckets exclusive while still answering "how big is VS Code really" |
| D23 | Nix lives in Developer, not Containers | It is a package manager |
| D24 | "No `.app` anywhere on the volume" is the precondition for orphan-likely; recent mtime and live LaunchAgents are keep signals; non-bundle software is its own class | Prevents false orphans from unconventional install locations and CLI tools |
| D25 | Bare `storix` loads a cache under 1 h old from the same version and root set, else rescans; `r` rescans | Q1 resolved |
| D26 | `codesign` runs only when an unattributed Group Container exists; cached per bundle id + version | Q4 resolved |
| D27 | JSON schema versioned from release one; version stored in cache files too | Q5 resolved |
| D28 | Config/cache dir is `storix` under Application Support | Q7 resolved |
| D29 | Per-volume `used` comes from `getattrlist`'s `ATTR_VOL_SPACEUSED`, not `statfs.Bfree` | `statfs` free/used is container-wide on an APFS volume group: on the planning machine `getattrlist` space-used for the data volume is 181.18 GB while `statfs`-derived used is 210.76 GB (the whole container, all volumes). `getattrlist` restates `statfs`'s size/free/avail fields exactly (verified: 0 or 4 KB difference, alignment-only) but gives the per-volume used figure statfs cannot (M1 finding) |
| D30 | Mount points are matched under both the firmlink spelling (e.g. `/Users/andrewsam/OrbStack`) and the data-volume spelling (`/System/Volumes/Data/Users/andrewsam/OrbStack`); `/home` is a symlinked special case pointing at the autofs mount `/System/Volumes/Data/home` | A nested mount discovered by `getfsstat` reports one spelling or the other depending on how it was mounted; the walker's scan-path guard must recognize a mount point regardless of which spelling reached it, or a firmlink-vs-data-volume mismatch silently descends into a mount it meant to skip (M1/M2 finding) |
| D31 | The `getattrlistbulk` raw-syscall reader is deferred past phase 1a, not shipped as an experiment behind a flag | At the measured ≈0.17–0.18× ratio against `du -sk` (see `docs/09-bench-1a.md`), the ReadDir+Lstat walker already beats the 0.35× acceptance target by roughly 2×; a raw-syscall reader with a private-ish record layout and manual buffer parsing is not worth its ABI risk for a bench line that is not the bottleneck. Revisit only if a future workload (many more inodes, slower storage) closes that 2× margin |
| D32 | Charm stack is `charm.land/lipgloss/v2` and `github.com/charmbracelet/bubbletea/v2` / `bubbles/v2` import paths, not the pre-rename `github.com/charmbracelet/lipgloss` | The v2 releases moved Lip Gloss's module path to `charm.land/lipgloss/v2` (see `go.mod`); Bubble Tea v2 and Bubbles v2 stayed under `github.com/charmbracelet/...` with a `/v2` suffix. 05-tech-stack.md's library table predates this rename |
| D33 | `golang.org/x/sys` is pinned to v0.47.0, not the newest available | v0.47.0 is the latest release compatible with Go 1.25.5's toolchain constraints at the time these packages were built; it still has every symbol phase 1a needs (`Getfsstat`, `Statfs`, `Lstat`, `Fstatat`, `Setattrlist`, `Attrlist`, the `ATTR_VOL_*`/`SF_DATALESS`/`UF_COMPRESSED` constants) and still lacks `Getattrlist` (get) and `Getattrlistbulk`, so those stay raw syscalls regardless of the pin |

| D34 | Code roots (`~/code` plus any of `~/Developer`, `~/Projects`, `~/src`, `~/dev`, `~/work` that exist) land in Developer: project source is "Project source" (user-data), build-artifact directories are "Build artifacts" (regenerable), `.git` is "Repo history" (user-data). A project is the nearest ancestor with `.git` **or** a manifest file, and the "largest projects" gate is stated as **the six largest by artifact bytes**, not "nearest `.git`" | On this machine 4 of the 6 largest `~/code` entries (`gib/armchair`, `gib/apres` have git; `toto`, `mochi` do not) have no top-level `.git`, so a `.git`-only gate would silently drop or misattribute them; the manifest-only case needs its own detector test (M11 critique fix) |
| D35 | `simctl` is gated behind `xcodebuild -checkFirstLaunchStatus` (exit 0) under `DEVELOPER_DIR`; it is never run otherwise, and `storix doctor` prints the gate verdict | Running `simctl` ungated performed an unwanted CoreSimulator component install during planning. Verified on this machine: gate passed (first-launch components already installed from that planning-time install), `simctl` queried and returned zero devices/runtimes in 340 ms, installing nothing further |
| D36 | A `.app` bundle nested inside another owner's own data directory, inside `Caches`, or inside `.Trash` never satisfies the "installed" precondition for orphan detection; only a `.app` found by the walk anywhere else on the volume does | Stale update copies (`Raycast.app` under Application Support, Sparkle `Updater.app` copies under Caches) would otherwise mask real orphans by making every uninstalled app look installed. Caveat verified on this machine: `~/.Trash` returns EACCES to an unprivileged scan process, so the walker cannot see into it at all here — `DynamicLakePro`, which the phase 1b plan expected to demonstrate the in-Trash case, instead surfaces as `orphan-likely` on the directory-name evidence alone. The rule is implemented and unit-tested; the in-Trash path itself is unverified end to end on this machine (see docs/10) |
| D37 | A pkg receipt whose install location and `--files` are both gone, with no live LaunchAgent/LaunchDaemon and no write in 30 days, resolves to orphan-likely even when a receipt still exists | Verified: `com.paloaltonetworks.globalprotect.pkg`'s receipt names `Applications/GlobalProtect.app` as its install location, which does not exist; `pkgutil --files` for it are all gone; no `paloaltonetworks` LaunchDaemon exists; last write 2025-12-29. The phase 1b plan's original corpus line expected GlobalProtect installed — verified wrong, corrected before M8 per the plan critique |
| D38 | Wondershare's leftover directories (`com.wondershare.Installer`, `com.wondershare.mac-drfoneframe`) are orphan-likely, not exempted as non-app software. `docs/06-roadmap.md`'s Phase 1b acceptance bullet is amended: it no longer lists `com.wondershare.Installer` among directories that must not be flagged. `stremio-server` remains the non-app exemplar | Verified: no `Wondershare.app` exists anywhere on the volume by any route the backstop checks. Installer/uninstaller residue with no surviving application is exactly the case orphan detection exists to surface, unlike `stremio-server`, which is a running helper process's own data directory for an application (Stremio) that *is* installed |
| D39 | Probes run as the invoking user, never as root, even under `sudo storix scan --system`: `mac.InvokingUser()` supplies the uid/gid, and `probe.Exec` sets `exec.Cmd.SysProcAttr.Credential` from it whenever the process's effective uid is 0 | Homebrew refuses to run as root outright, and every other probe's `HOME`/config-file resolution needs the invoking user's home, not root's. Implemented in `internal/probe/exec.go`; unverified end to end on this machine because no scan in phase 1b's validation ran under `sudo` |
| D40 | A claim carries exactly one `Reclaim` tag; a detector cannot yet split one claim's bytes into a reclaimable and a non-reclaimable share. Adding a per-claim `ReclaimableBytes` override (so a detector's own more precise figure, e.g. Homebrew's `cleanup -n` total or a container daemon's per-line reclaimable share, overrides the coarse bucket-level guess) is deferred past phase 1b | Verified on this machine: the OrbStack Group Container's one claim reports its full 18.76 GB as `tool-managed` (all-or-nothing), while the daemon's own `system df` breakdown says only part of its 19.83 GB guest total is reclaimable per line (54% of images, 99% of stopped containers, 19% of volumes). The ledger's bucket-level "reclaimable" total is therefore a claim-granularity approximation, not the daemon's own finer number, until a future milestone adds the override |
| D41 | Rule specificity for the resolver is `(Depth, Shape, Literals, Priority, Source.ID)` in that order: `Depth` is segment count, `Shape` packs each segment's pattern class (literal > in-segment glob > `{name}` capture > bare `*`) from the deepest segment back toward the root, `Literals` is how much literal text is pinned down, `Priority` is the hand-set tie-break, and `Source.ID` makes the result deterministic when everything else ties. `walk.Node` gained a preorder `ID int32`, assigned by both `walk.finalize` and `cache.reader`, so every claim and every accessor is keyed by that ID with no reverse pointer map needed | `~/Library/Caches/*.ShipIt` and `~/Library/Caches/{bundleid}` are the same depth over the same directory and would otherwise tie and resolve alphabetically by rule ID — verified on this machine, 15 directories match one of the two updater forms. A `map[*walk.Node]int32` over ~490k nodes would cost 25–30 MB against a stated < 10 MB classification memory budget, which the node-ID field avoids |
| D42 | The two new cache sections (`"detectors"`, `"classification"`) plus the apps inventory's own (`"apps"`) are added to `cache.Meta.Sections` **without** a schema bump; `cache.SchemaVersion` and `report.SchemaVersion` both stay `1`, with the new JSON fields (`ledger.buckets`, top-level `classification`/`apps`, per-node `bucket`/`owner`/`reclaim`/`source`) purely additive | `scan.section()` already tolerates a missing section, so an old cache loads and re-classifies from the code catalog plus its stored facts rather than erroring; a JSON or cache consumer written against phase 1a's schema keeps working unmodified against phase 1b's documents |

## Assumptions
- A1: Single primary user account; other users' homes are reported as unreadable/system.
- A2: Code roots default to `~/code`; other common roots probed if present.
- A3: Apple silicon primary; Intel supported via `/usr/local` Homebrew rules but validated later.
- A4: OrbStack is the primary container runtime on the planning machine; Docker Desktop
  rules are written from documentation and validated when available.
- A5: The scan tree for ~3.5 M files fits comfortably in memory with small-file aggregation
  (target < 500 MB RSS during scan, < 100 MB cache file).

## Open questions — resolved in validation round 1 (2026-09-22)
- Q1 → D25. Q2 → D18 (64 KB + exemptions). Q3 → conservative table, plus unmatched large
  directories are logged to a tuning file so the table grows from real scans. Q4 → D26.
  Q5 → D27. Q6 → no watch mode in phase one. Q7 → D28.

## Open questions for review round 2
- Q8: Purgeable source. `getattrlist` with `ATTR_VOL_SPACEUSED`/`ATTR_VOL_SPACEAVAIL` versus
  Foundation's `NSURLVolumeAvailableCapacityForImportantUsageKey` via cgo. Proposal: try
  `getattrlist` first (pure Go); use Foundation only if it does not expose purgeable.
  **Resolved in phase 1a implementation:** `getattrlist`'s volume attributes restate `statfs`
  (space free/avail agree to within 4 KB alignment) and expose no purgeable attribute at all;
  Foundation's `NSURLVolumeAvailableCapacityForImportantUsageKey` is the only source, matching
  the original spike prediction (D21 unchanged).
- Q9: cgo policy. The dataless IO policy and possibly purgeable each need one cgo call, and
  `getattrlistbulk` needs a raw syscall. Proposal: one small cgo file behind a build tag; the
  pure-Go build reports "dataless protection unavailable" so cross-compilation stays possible.
  **Resolved:** implemented exactly as proposed (`internal/mac/iopolicy_cgo.go` +
  `purgeable_cgo.go`/`purgeable_darwin.m`, with `_nocgo.go` twins); `make build-nocgo` compiles
  and `storix doctor` reports cgo availability and the purgeable source.
- Q10: `storix explain` accepting a bundle id in addition to a path. Proposal: yes, cheap.
  Not yet implemented; `storix explain` itself is phase 1b scope (03-architecture.md's CLI
  surface lists it, but it does not appear in phase 1a's roadmap milestones).
- Q11: Purgeable may overlap `scanned`. Evictable-but-locally-present iCloud files (and, when
  present, snapshot-held blocks) can be counted both in the walked tree and in the purgeable
  figure, so the ledger identity `used = scanned + purgeable + residual` is not "no
  double-counting", just "every byte assigned to a labeled line". **Observed on the planning
  machine (`docs/09-bench-1a.md`):** the report itself now carries the label — the purgeable
  line reads "freed when space runs short; may overlap scanned bytes" — rather than silently
  reordering the identity or trying to subtract the overlap out (which nothing on macOS
  exposes cleanly).

## Risks
- R1: TCC. Most protected paths fail silently without FDA; Desktop/Documents/Downloads
  normally trigger a one-time dialog on first access. Some data-vault paths are denied even
  with FDA (verified). Handled via `doctor`, the Unaccounted view, and README notes.
- R7: cgo. Two calls (`setiopolicy_np`, possibly purgeable) are not in `x/sys/unix`. See Q9.
- R8: `brew list --cask` aborts when any untrusted third-party tap is present (verified).
  Caskroom directory listing is the primary source; brew JSON is enrichment only.
- R2: `lsregister -dump` output format is undocumented and can change between macOS releases.
  Treat as optional evidence; never a sole source.
- R3: Homebrew probes are slow and can hit the network for cask JSON. Use `--json=v2` with
  local formulae only, and cache results per brew version.
- R4: `docker system df` requires a running daemon; if OrbStack is stopped, report host
  image size only with a note.
- R5: APFS clones can make the scanned sum exceed statfs used. Explain it in the UI rather
  than clamping silently.
- R6: Memory on very large trees (tens of millions of files) — aggregation threshold and
  optional depth-limited retention mitigate; not a phase-one target.
