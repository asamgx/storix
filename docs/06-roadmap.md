# 06 — Roadmap

Each phase ends with a review. Phase one is split into milestones so the tool is useful
early and the classification can be tuned against real scans.

## Phase 1a — Correct, fast, honest walker with a TUI

Deliverable: a macOS-correct ncdu with reconciliation.

- [ ] Repo scaffold: module, `cmd/storix`, Cobra + Fang, Makefile, lint, CI-less local `make test`.
- [ ] `volume` package: `getfsstat` mount table, per-volume used + container total/free, before/after capture for drift, purgeable via `getattrlist`/Foundation (or explicit unknown), `tmutil`/`diskutil apfs listSnapshots` on the data volume.
- [ ] `walk` package: parallel lstat walker rooted at the data volume; allocated sizes; deterministic hard-link dedupe; no symlink follow; skip list plus runtime mount-table guard (`getfsstat`); error capture; small-file aggregation with exemption prefixes; progress channel; dataless IO policy (cgo or raw syscall).
- [ ] Deadlock-free scheduler (semaphore over recursive goroutines or mutex stack + WaitGroup); a test that walks a deep synthetic tree with parallelism 1 and 64.
- [ ] Benchmark `getattrlistbulk` (raw syscall) against ReadDir+Lstat on `~/Library`; adopt if it wins by > 30 %.
- [ ] `ledger` reconciliation: scanned total vs statfs used, purgeable, unreadable list, unaccounted delta.
- [ ] `cache` package: benchmark gob vs flat encoding on the real tree first; persist and load with schema + storix version; `latest`; retention; freshness rule; sudo ownership fix.
- [ ] TUI: progress view → Browse view (table, bars, breadcrumb, sort, filter, bundles-as-leaves toggle, open in Finder, copy path) → Unaccounted view.
- [ ] `--report` and `--json` for the raw tree and reconciliation.
- [ ] `storix doctor`: FDA probe, terminal identity hint, sudo advice.
- [ ] Bench against `du -sk` on the same root; document numbers. Baseline on the planning machine: 106 s warm, single-threaded.

Acceptance:
- Ledger reconciles with `statfs`: walked bytes + purgeable + named metadata/overhead lines +
  residual = used, where the residual is displayed, never hidden, and the tolerance is the
  observed before/after drift.
- Performance is stated as a **ratio against `du -sk` on the same root**, not absolute
  seconds: warm scan at most 0.35× `du` (≈ 37 s here), cold at most 1.0× `du`. TUI stays
  responsive during the scan.
- Nested mounts (`~/OrbStack`, `/System/Volumes/Data/home`) are skipped and listed; the
  OrbStack image is counted exactly once.
- No file is opened during a scan (enforced by test). No iCloud downloads triggered
  (verified manually with a dataless file).
- Sizes agree with Finder's Get Info "on disk" for a sampled set of directories.

## Phase 1b — Classification, linking, developer awareness

Deliverable: the ledger and the smart views.

- [ ] `classify` engine: rule model, embedded catalog (~150 rules), specificity ordering, inheritance, conflict resolution, provenance.
- [ ] `probe` helper with timeouts, concurrency with the walk, captured fixtures.
- [ ] Detectors in priority order: `homebrew`, `node`, `python`, `go`, `rust`, `xcode`, `ide`, `projects`, `orbstack`, `docker`, then `apps` (inventory → footprint → orphans), then `jvm`, `ruby`, `aimodels`, `colima`, `vms`, `nix`, `backups`, `cli-tools`.
- [ ] Ledger view with 12 buckets, reclaimable sub-bars.
- [ ] Apps view (footprints, orphan candidates with evidence).
- [ ] Developer view (tools, versions with current markers, projects with last activity).
- [ ] Containers view (host allocated vs guest reported).
- [ ] "Why" panel showing provenance for any row.
- [ ] `storix explain PATH`, `storix apps`, `storix dev`.
- [ ] Curated alias table seeded with ~50 apps; a `testdata` corpus of real Library listings (paths only, no contents) for regression.

Acceptance:
- "Other" bucket under 5 % of used space on the planning machine after tuning.
- Every installed app in `/Applications` appears in the Apps view with a footprint.
- At least the known leftover data on the planning machine is surfaced as orphan-likely
  with correct evidence (validated manually during review), and the known non-app software
  directories (`stremio-server`, `com.wondershare.Installer`) are **not** flagged.
- Each detector has fixture-based tests and degrades cleanly when its tool is absent.
- **Not validatable on the planning machine:** Xcode/simulator and Docker Desktop detectors
  (neither is installed). They ship from documentation with fixtures and are marked
  unverified until run on a machine that has them.

## Phase 2 — Reclaim (act through native tools)

Deliverable: guided, dry-run-first cleanup.

- Every action shows a dry run with bytes before doing anything; confirmation per action.
- Actions are native where possible: `brew cleanup`, `brew autoremove`, `docker system prune`
  / `docker builder prune`, `orb` shrink advice, `xcrun simctl delete unavailable`,
  `tmutil thinlocalsnapshots`, `pnpm store prune`, `npm cache clean`, `go clean -cache -modcache`,
  `cargo cache` (if installed), `nix-collect-garbage`, `uv cache clean`.
- File-level removals go to **Trash** (`osascript`/Finder or the `trash` approach via
  `NSFileManager.trashItemAtURL` through a small helper), never `rm -rf`, and only for
  `regenerable` or `orphan-likely` items the user explicitly selects.
- Orphan cleanup shows the full evidence chain and requires typed confirmation.
- Undo log: what was trashed, from where, when.

## Phase 3 — Over time

- Scan diffs: "what grew since <date>", per bucket and per owner, from the cache.
- Optional scheduled scan via `launchd` with a summary notification.
- Watchlist: alert when a specific path or bucket crosses a threshold.
- Export: Markdown report; JSON schema versioning.

## Phase 4 — Polish and distribution

- Homebrew tap via goreleaser.
- Config overrides for rules; user-defined aliases.
- Intel Mac verification (`/usr/local` Homebrew, Rosetta caches).
- External volume scanning as an explicit opt-in.
