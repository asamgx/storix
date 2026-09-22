# 09 — Phase 1a Benchmark (2026-09-22)

Numbers below are from the planning/development machine, the same one all of docs/01–08
describe. Everything measured here is warm (page cache populated by the earlier `doctor`/
`scan` runs in this session); a `--cold` run (`sudo purge` before each measurement) is
supported by `scripts/bench.sh` but was not run for this document — see
[Not measured](#not-measured).

**Measurement conditions.** This machine was shared with other agents actively building and
running tests against the same repository while these numbers were taken (`asamgx/phase-1a-walker`
had concurrent work in `internal/scan`, `internal/tui`, and elsewhere during this session). That
inflated `du`'s wall time by roughly 10× partway through measurement (a single-threaded,
non-overlapping syscall loop absorbs disk/scheduler contention almost linearly), while
`storix scan`'s own wall time only rose by roughly 1.5× under the same contention (its worker
pool keeps other I/O in flight while one is blocked). Both a clean, low-contention measurement
taken early in the session and a later, contended sweep are recorded below; the clean
measurement is the one judged against the 0.35× target, and the contended numbers are kept as
supporting evidence, not as the headline ratio.

## Machine

| Item | Value |
|---|---|
| CPU | Apple silicon, 10 cores |
| RAM | 16 GB |
| OS | macOS 26 (Darwin 25.6.0) |
| Go | 1.25.5 darwin/arm64 |
| Data volume used (`statfs`, at scan time) | ≈181.18 GB before, ≈181.17 GB after (drift ≈8 MB) |
| Files on data volume (this walk) | 3,208,370 in 349,508 directories |

## `du -sk` baseline (warm)

**Clean measurement** (1 run, taken early in the session before other agents' builds/tests
ramped up), via `scripts/bench.sh --runs 1`:

| Run | Wall seconds |
|---|---|
| 1 (clean) | 107.76s |

This matches the original design-time baseline recorded in `docs/03-architecture.md` /
`docs/08-validation-round-1.md` (106s warm, full data volume, single-threaded) to within 2%, so
it is treated as the representative figure.

**Contended measurements** (2 runs, taken later in the session with other agents' `go build`/
`go test` active against the same repo):

| Run | Wall seconds |
|---|---|
| 2 (contended) | 1140.29s |
| 3 (contended) | 1039.04s |
| mean | 1089.66s |

An unprivileged `du -sk` over the whole data volume always exits non-zero (permission-denied
subdirectories — Mail, TCC-protected caches, another user's home — are expected, not a
measurement error); `scripts/bench.sh` ignores that exit status and reads the wall time from
`/usr/bin/time -p` regardless.

## Parallelism sweep

**Clean reference point** (1 run, same session as the clean `du` run, default parallelism —
`min(2·NumCPU, 16)` = 16 on this 10-core machine): `storix scan --report` completed in
**18.53s**, scanning 3,208,370 files across 349,508 directories. Ratio against the clean `du`
baseline: **18.53 / 107.76 = 0.172×**.

**Contended sweep** (parallelism 8/16/32, 2 runs each, same contended window as the second and
third `du` runs above; `storix scan` has no `--no-cache` flag yet — phase 1a's CLI always walks
fresh, only a future cache-aware path would need one — so the sweep runs the plain scan):

| parallelism | run 1 | run 2 | mean seconds | ratio to contended `du` (1089.66s) |
|---|---|---|---|---|
| 8 | 29.67s | 27.44s | 28.56s | 0.026× |
| 16 | 28.04s | 28.90s | 28.47s | 0.026× |
| 32 | 29.00s | 29.87s | 29.44s | 0.027× |

The contended ratios are misleadingly small: `du`'s wall time absorbed nearly all of the
shared-machine contention (10× slower than its clean run) while storix's own wall time barely
moved (28–29s vs. 18.53s clean, about 1.5×), so dividing by the inflated `du` figure produces a
ratio that is not comparable to the 0.35× target — it is included only to show that storix's
scan time is close to flat across parallelism 8–32 even under load, not as the headline number.
64 was not measured in the contended sweep (dropped to keep the total measurement time
reasonable while the machine was shared).

**Result against the target:** using the clean, uncontended measurement (the one comparable to
the design-time baseline), warm ratio is **0.172×**, easily inside the 0.35× acceptance target
in `docs/06-roadmap.md` (D20) — roughly **2× better than required**. `du -sk`'s single-threaded,
non-overlapping syscall loop is the entire reason the ratio is this far under 1.0: storix's
16-worker walker overlaps directory reads instead of blocking on each one in turn.

## Memory (`--debug`)

From `storix scan --report --debug` on the full data volume (default parallelism, 16 workers
on this machine):

| Metric | Value |
|---|---|
| Heap allocated after the walk (`runtime.MemStats.HeapAlloc`) | 170.8 MB |
| Total allocated over the run (`TotalAlloc`) | 3.28 GB |
| Memory obtained from the OS (`Sys`) | 342.8 MB |
| GC cycles | 138 |
| Nodes retained | 490,806 (of 3,208,370 files across 349,508 directories) |

Both figures are comfortably inside the phase 1a targets in the implementation plan (heap
after Finalize ≤ 300 MB; peak RSS < 500 MB — `Sys` is not RSS but is the closest figure this
build's `--debug` output exposes, and it alone is well under the RSS ceiling).

## Cache (M5)

Measured by the M5 milestone's benchmarks (`internal/cache/bench_test.go`, `BenchmarkReal*`
against a real tree via `STORIX_BENCH_ROOT`):

| Tree | Nodes | Encoded size | Decode time |
|---|---|---|---|
| Full data volume | 490,043 | 42.0 MB | 33.6 ms |
| `~/Library` | 106,028 | 10.4 MB | (not separately re-measured; see full-volume figure) |

The `encoding/gob` baseline (`BenchmarkGobDecode`/`BenchmarkRealGobDecode`), kept only for
comparison and never shipped, decodes about 2.5× slower than the flat format, with
proportionally more allocations — the flat format's struct-of-arrays layout with one arena
allocation is the reason, against gob's per-node reflection-driven allocation.

## Reconciliation (re-measured this session)

From a live `storix scan --report` run against `/System/Volumes/Data`:

```
scanned     174.49 GB   (allocated bytes, hard links counted once)
purgeable     1.57 GB   (may overlap scanned bytes — Q11)
residual      5.11 GB   (filesystem metadata, 392 unreadable directories, and unaccounted)
──────────────────────
used        181.18 GB   (reported by the volume before the walk; 181.17 GB after)
```

Identity `scanned + purgeable + residual = used` holds to the byte. The `Reconciles` verdict
is **false**, as expected (`docs/06-roadmap.md`, `docs/02-storage-model.md`): the residual
(≈5.11 GB) is far larger than the ≈8 MB drift tolerance, because APFS directory inodes report
zero blocks (no `lstat`-visible filesystem-metadata figure) and 392 directories were
permission-denied without Full Disk Access or `sudo`. The report's verdict line states this
explicitly rather than hiding it.

Container `disk3`: total 245.11 GB, used 210.75 GB (data 181.17 GB + macOS volumes 28.14 GB
across `/`, Preboot, Update, VM), free 34.36 GB, overhead 1.44 GB (container structure outside
any volume). Two mounts nested inside the scan root were skipped and listed as expected:
`/Users/andrewsam/OrbStack` (nfs) and `/home` → `/System/Volumes/Data/home` (autofs).

## Decision: `getattrlistbulk` experiment deferred (D31)

The implementation plan (`docs/07-decisions.md` D31) scheduled a raw-syscall
`getattrlistbulk` `DirReader` as an M3 experiment, adopted only if it beat `ReadDir`+`Lstat` by
more than 30%. It was not built for phase 1a: at the measured clean ratio of roughly
**0.17–0.18× `du`**, the ReadDir+Lstat walker already beats the 0.35× acceptance
target by about 2×, so a second directory-reading path — parsing variable-length
`attrreference_t` records by hand, behind a private-ish ABI relative to the public
`getattrlist` — was judged not worth the risk for a bench line that is not the bottleneck.
Revisit if a future workload (many more inodes, slower storage, tighter latency budget) closes
that margin.

## Manual dataless check (checklist, not run)

No API evicts an iCloud file on demand, so this cannot be scripted or synthesized; it needs a
real dataless file already present from Optimized Storage, and running it would risk
triggering the very materialization the check is supposed to rule out if done carelessly. Not
run for this document. Steps to run manually when such a file is available:

1. Find a dataless entry: `ls -lO ~/Library/Mobile\ Documents/**` and look for the `dataless`
   flag in the listing (or scan for `st_flags` with `SF_DATALESS` set, `0x40000000`).
2. Record its current allocation: `stat -f '%b' <path>` (blocks) and `stat -f '%z' <path>`
   (apparent size) before touching it with storix.
3. Confirm the policy storix will set: `storix doctor` → "dataless materialization" section
   should read `current policy: off` after a scan has run at least once, or run `storix scan`
   first.
4. Run `storix scan --json --full --roots <the enclosing directory>` and locate the file in
   the output: expect `dataless: true`, allocated bytes `0`, apparent bytes matching step 2.
5. Re-check `stat -f '%b'` on the same path immediately after: it must be unchanged (still the
   pre-scan block count) — if it grew, the scan (or something concurrent) materialized the
   file, which is the failure mode this check exists to catch.
6. Repeat steps 4–5 once against a `CGO_ENABLED=0` build (`make build-nocgo`) to confirm the
   walker-level rule — never `ReadDir` a directory flagged `SF_DATALESS`, never open a file —
   is sufficient on its own, independent of the cgo dataless-policy call.

## Finder Get Info sampling (skeleton, not filled in)

Finder's Get Info "on disk" figure has no CLI or API equivalent, so this table has to be filled
in by hand: open each directory in Finder, `⌘I`, read "on disk", and compare against the
allocated bytes storix reports for the same path (`storix scan --json --full --roots <dir>` or
the `--report` top-N list). A match within a few MB (Finder rounds; bundle sizes can shift
between runs if something inside is actively written) is a pass.

| Directory | Finder "on disk" | storix allocated | Match? |
|---|---|---|---|
| `~` (home) | | | |
| `/Applications` | | | |
| `~/Library` | | | |
| `/opt/homebrew` | | | |
| `/System/Library` | | | |
| `/Applications/Xcode.app` | | | |
| `~/Library/Developer` | | | |
| `/private/var` | | | |

## Not measured

- Cold-run numbers (`scripts/bench.sh --cold`, which needs `sudo purge` and several minutes
  per data point) — not run for this document; warm-only per the task scope.
- The manual dataless check and the Finder Get Info sampling above (checklists only; both need
  a human at the machine, not a scripted run).
- `~/Library`-only decode timing was not re-run separately this session; the figure above is
  from the M5 milestone's own benchmark record.
