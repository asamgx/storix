# 06 — Roadmap

Each phase ends with a review. Phase one is split into milestones so the tool is useful
early and the classification can be tuned against real scans.

## Phase 1a — Correct, fast, honest walker with a TUI

Deliverable: a macOS-correct ncdu with reconciliation.

- [x] Repo scaffold: module, `cmd/storix`, Cobra + Fang, Makefile, lint, CI-less local `make test`. — M0 shipped (`265cc73`): `storix version`, `make test`/`build-nocgo` green.
- [x] `volume` package: `getfsstat` mount table, per-volume used + container total/free, before/after capture for drift, purgeable via `getattrlist`/Foundation (or explicit unknown), `tmutil`/`diskutil apfs listSnapshots` on the data volume. — M1 shipped (`87af578`, `9257eaa`): `storix doctor` prints the 11-mount table, container `disk3`, purgeable 1.57 GB (Foundation), dataless policy off.
- [x] `walk` package: parallel lstat walker rooted at the data volume; allocated sizes; deterministic hard-link dedupe; no symlink follow; skip list plus runtime mount-table guard (`getfsstat`); error capture; small-file aggregation with exemption prefixes; progress channel; dataless IO policy (cgo or raw syscall). — M2 shipped (`5b79f66`): a live scan retains 490,806 nodes over 3,208,370 files, dedupes 8,751 hard-link groups (630.1 MB not double counted), skips both nested mounts.
- [x] Deadlock-free scheduler (semaphore over recursive goroutines or mutex stack + WaitGroup); a test that walks a deep synthetic tree with parallelism 1 and 64. — part of M2 (`5b79f66`); scheduler tests run under `-race` at P=1 and P=64.
- [ ] Benchmark `getattrlistbulk` (raw syscall) against ReadDir+Lstat on `~/Library`; adopt if it wins by > 30 %. — **deferred, not implemented.** At the measured ≈0.17–0.18× `du` ratio (`docs/09-bench-1a.md`) the ReadDir+Lstat walker already beats the 0.35× target by roughly 2×, so the raw-syscall reader's ABI risk was judged not worth it for phase 1a (D31). Revisit if a slower workload closes that margin.
- [x] `ledger` reconciliation: scanned total vs statfs used, purgeable, unreadable list, unaccounted delta. — M4 shipped (`34c2d89`): identity `scanned + purgeable + residual = used` printed and holds to the byte on every run.
- [x] `cache` package: benchmark gob vs flat encoding on the real tree first; persist and load with schema + storix version; `latest`; retention; freshness rule; sudo ownership fix. — M5 shipped (`78352d8`): flat format decodes a 490k-node real scan in ~34 ms at 42 MB, 2.5× faster than the gob baseline; `storix cache list|prune|clear|path`.
- [x] TUI: progress → Browse → Unaccounted, help overlay, all §6 keys (M6; 20k-row keypress 357 µs; goldens in internal/tui/testdata).
- [x] `--report` and `--json` for the raw tree and reconciliation. — shipped with M4 (`34c2d89`): `storix scan --report` and `storix scan --json` (schema 1, depth/min-size limited unless `--full`).
- [x] `storix doctor`: FDA probe, terminal identity hint, sudo advice. — M1 shipped (`87af578`): build info, dataless policy, purgeable source, terminal identity, FDA probe + hint, sudo state, volumes/container tables, nested-mount list, snapshots, cache dir status.
- [x] Bench against `du -sk` on the same root; document numbers. Baseline on the planning machine: 106 s warm, single-threaded. — `scripts/bench.sh` and `docs/09-bench-1a.md` (this milestone, M7).

Acceptance:
- [~] Ledger reconciles with `statfs`: walked bytes + purgeable + named metadata/overhead lines +
  residual = used, where the residual is displayed, never hidden, and the tolerance is the
  observed before/after drift. **Identity met, "reconciles" verdict not, as designed:** the
  report prints `used = scanned + purgeable + residual` holding to the byte, with the residual
  (filesystem metadata, 392 unreadable directories' content, and unaccounted) always shown; the
  `Reconciles` verdict itself reads false on this machine because the ~5.11 GB residual exceeds
  the ~8 MB drift tolerance, and the report says so in the verdict text rather than hiding it.
- [x] Performance is stated as a **ratio against `du -sk` on the same root**, not absolute
  seconds: warm scan at most 0.35× `du` (≈ 37 s here), cold at most 1.0× `du`. TUI stays
  responsive during the scan. — **met, by roughly 2×**: see `docs/09-bench-1a.md` for the
  per-parallelism table (warm ratio ≈ 0.17–0.18×). The TUI responsiveness half of this bullet
  is not yet checked (TUI in progress).
- [x] Nested mounts (`~/OrbStack`, `/System/Volumes/Data/home`) are skipped and listed; the
  OrbStack image is counted exactly once. — met: `storix doctor` and `storix scan --report`
  both list `/Users/andrewsam/OrbStack` (nfs) and `/home` (autofs) under mounts skipped.
- [~] No file is opened during a scan (enforced by test). No iCloud downloads triggered
  (verified manually with a dataless file). — the no-open lint test (`walk/lint_test.go`)
  shipped with M2 and is enforced by `make test`; the manual dataless check itself was **not
  run** for this doc (no evicted iCloud file was available to test against without triggering
  a download) — see the checklist in `docs/09-bench-1a.md`.
- [ ] Sizes agree with Finder's Get Info "on disk" for a sampled set of directories. — **not
  yet sampled**; `docs/09-bench-1a.md` has the sampling table skeleton for 8 directories, to be
  filled in manually (Finder's Get Info has no CLI/API equivalent to automate).

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
