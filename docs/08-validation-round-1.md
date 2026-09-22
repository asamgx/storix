# 08 — Validation Round 1 (2026-09-22)

Two independent lanes: an Opus critic agent reviewed all docs and ran its own read-only
checks; the author ran a separate set of empirical probes on the planning machine
(macOS 26, Darwin 25.6.0, Apple silicon, Go 1.25.5). Nothing on the machine was modified.

**Verdict: GO-WITH-FIXES.** Six blocking factual errors were found and all are corrected in
docs 02–07. The architecture (walker/classifier split, allocated sizes, provenance,
honest unaccounted bucket, data-volume scan root) held up.

## Blocking findings and fixes

| # | Finding | Evidence | Fix |
|---|---|---|---|
| B1 | Purgeable bucket had no data source | `diskutil info /System/Volumes/Data` prints Volume Used, Container Total, Container Free; no purgeable field | Source is `getattrlist` volume attributes or Foundation via cgo; else shown as unknown (D21, Q8) |
| B2 | Claimed directory inodes carry block usage | `lstat` on `~`, `~/Library`, `/Applications` → `st_blocks = 0` | Removed; filesystem metadata is a named line in bucket 12 |
| B3 | Mount guard relied on `st_dev` | `/` and `/System/Volumes/Data` both `dev=16777230`; VM `16777231`, Preboot `16777232`. Nested mounts inside scan root: `/System/Volumes/Data/home` (autofs), `~/OrbStack` (NFS `OrbStack:/OrbStack`, `du` = 20.8 GB, duplicates the host image) | Mount table from `getfsstat`; skip every mount point other than the root (D2 updated) |
| B4 | OrbStack image path wrong | `~/.orbstack` holds bin/config/log/run/shell/ssh only. Image at `~/Library/Group Containers/HUAQ24HBR6.dev.orbstack/data/data.img.raw`: 245.1 GB apparent, 18.8 GB allocated | Paths corrected in 02 and 04 |
| B5 | 1 MB small-file aggregation deleted classifier evidence (plists, cookies, receipts) | Contradiction between 03 and 04 | 64 KB threshold + exemption prefixes + filename rules before aggregation (D18) |
| B6 | `brew --prefix --cellar --caskroom --cache` is not valid | Prints usage for `--prefix`, ignores the rest | Four separate probes |

## Should-fix findings applied

- Performance targets restated as a ratio against `du -sk` (measured: 106 s warm for the
  full data volume; `du -sk ~/Library` 28.7 s for 1.19 M entries ≈ 42k entries/s; walk is
  latency-bound). D20.
- Hard-link attribution made deterministic (smallest path). pnpm, not nvm, is the heavy
  hard-link user (9.4 GB store). D19.
- Per-volume `statfs` convention stated: only per-volume used is meaningful; container
  overhead (~6 GB here) is a named line.
- Reconciliation drift measured (~0.5 GB in a minute, idle machine); `statfs` captured before
  and after the walk, tolerance derived from drift.
- Data-vault paths denied even with FDA (`~/Library/Caches/com.apple.ap.adprivacyd`, container
  metadata plists); first-run TCC dialogs for Desktop/Documents/Downloads documented.
- Orphan detection: "no `.app` anywhere on the volume" precondition; keep signals (recent
  mtime, live LaunchAgent); non-bundle software class (`stremio-server` 2.8 GB,
  `com.wondershare.Installer` 494 MB would otherwise be false positives); LaunchServices
  registry demoted to corroborating evidence. D24.
- Rule catalog gaps added: Electron updater caches (`*-updater`, `*.ShipIt`, ~2 GB here),
  `~/Library/Caches/pnpm` (1.4 GB, distinct from the store), agent/IDE dirs (`~/.antigravity`
  2.5 GB, `~/.cache/codex-runtimes` 1.6 GB, `~/.codex`, `~/.local/share/nvim` 1.7 GB), Flutter,
  .NET, Haskell, Bazel, ccache, conan, SwiftPM, CocoaPods repos, `/private/tmp`,
  `/private/var/tmp`. `~/.gradle` de-duplicated between jvm and cli-tools. Nix moved to
  Developer (D23).
- Simulator runtime bytes are in `CoreSimulator/Images`, not `Volumes`.
- Probe corrections: Yarn Berry uses `yarn config get cacheFolder`; `tmutil listlocalsnapshots`
  targets the data volume; `docker system df -v` cannot be combined with `--format`;
  `bun pm cache` needs a project dir; `nvm` is a shell function; `brew list --cask` aborts on
  untrusted taps (R8).
- Dataless detection via `SF_DATALESS` flag (verified `st_flags = 0x40000060`, blocks 0), not
  inferred from zero blocks; `setiopolicy_np` must be process-scoped; it is not in
  `x/sys/unix` (only `SYS_IOPOLICYSYS = 322`), so cgo or raw syscall (Q9, R7).
- `x/sys/unix` v0.48 verified: has `Lstat`, `Fstatat`, `Getdirentries`, `Statfs`, `Getfsstat`,
  `Clonefile*`, `SF_DATALESS`; lacks `Getattrlistbulk` and any iopolicy wrapper.
- Footprint declared a cross-cutting view, never summed into the ledger (D22).
- Walker scheduler must not use a bounded channel fed by its own workers (deadlock).
- Cache format to be benchmarked (gob vs flat encoding) before commitment; JSON output
  depth/size limited by default.
- Xcode and Docker Desktop are not installed on the planning machine; those detectors are
  marked unverifiable here in the roadmap.

## Probes verified working on the planning machine

| Probe | Result |
|---|---|
| `brew --prefix` / `--cellar` / `--caskroom` / `--cache` | instant; `/opt/homebrew`, `.../Cellar`, `.../Caskroom`, `~/Library/Caches/Homebrew` |
| `brew list --formula --versions` | 0.2 s |
| `brew cleanup -n` | 0.7 s |
| `pnpm store path` | `~/Library/pnpm/store/v10` |
| `uv cache dir` / `uv python dir` | `~/.cache/uv` / `~/.local/share/uv/python` |
| `go env GOCACHE GOMODCACHE` | `~/Library/Caches/go-build` / `~/go/pkg/mod` |
| `npm config get cache` | `~/.npm` |
| `yarn cache dir` (1.22) | `~/Library/Caches/Yarn/v6` |
| `pyenv root` / `version-name` | `~/.pyenv` / `system` |
| `rustup toolchain list` | `stable-aarch64-apple-darwin (active, default)` |
| `gem env gemdir` | `/Library/Ruby/Gems/2.6.0` |
| `xcode-select -p` | `/Library/Developer/CommandLineTools` (no Xcode) |
| `orb list` | `[]` (no Linux machines) |
| `docker context ls` | contexts `default`, `orbstack*` |
| `docker system df --format json` | one JSON object per line, human-formatted size strings |
| `lsregister -dump` | ~560 bundles; parse `path:` + `identifier:` |
| `codesign -dv --verbose=4 <app>` | 26 ms; `TeamIdentifier=HUAQ24HBR6` for OrbStack |
| `pkgutil --pkgs` | 109 receipts |
| FDA probe (`ls ~/Library/Mail`) | readable in this terminal (tmux) → FDA granted |

## Machine numbers captured

| Metric | Value |
|---|---|
| Data volume used (`statfs`) | 177.6–178.9 GB across readings |
| `du -sk /System/Volumes/Data` | 191.1 GB (clone inflation ≈ 13 GB) |
| Inodes on data volume | ~3.5 M |
| `du` warm, full volume | 106 s |
| `du` warm, `~/Library` | 28.7 s, 1.19 M entries |
| OrbStack `data.img.raw` | 245.1 GB apparent / 18.8 GB allocated |
| Docker (OrbStack ctx) reported | images 9.9 GB, volumes 4.8 GB, build cache 5.1 GB, containers 0.1 GB |
| Group Containers / Containers | 118 / 696 directories |

## Not yet validated
- `xcrun simctl` probes (no Xcode here).
- Docker Desktop paths (not installed here).
- Purgeable via `getattrlist` (to be tried first thing in phase 1a; Q8).
- Whether `getattrlistbulk` beats ReadDir+Lstat by enough to matter (phase 1a benchmark).
