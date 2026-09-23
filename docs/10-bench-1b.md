# 10 — Phase 1b Benchmark and Acceptance (2026-09-23)

Numbers below are from one `scripts/accept-1b.sh --no-build` run (`storix scan --json --no-cache`
under the hood) on the same development machine as docs/08–09, immediately followed by
`storix scan --report --no-cache`, `storix apps`, and `storix dev` against the resulting cache —
close enough in time that the bucket totals agree to the byte between runs. All three commands
are read-only; nothing on the machine was installed, started, or modified to produce these
numbers, and no xcode/xcrun/brew-install/docker-prune command was run outside what the detectors
themselves issue.

## Machine

| Item | Value |
|---|---|
| CPU | Apple M4, 10 cores |
| OS | macOS 26.6 (Darwin 25.6.0) |
| Go | 1.25.5 darwin/arm64 |
| `storix` build | `v0.1.0-11-ga6aade8` |
| Data volume used (`statfs`, at scan time) | 188.14 GB before → 188.14 GB after (drift ≈4.9 MB) |
| Files / directories walked | 3,264,471 files, 353,338 directories, 511,966 retained nodes |
| Hard-link groups deduped | 8,948 groups, 643.7 MB not double counted |
| Unreadable directories | 392 (see [Unreadable / unaccounted](#the-twelve-buckets) below) |

## The twelve buckets

`storix scan --json --no-cache` → `scripts/accept-1b.sh`:

| # | Bucket | Bytes | % of used | Reclaimable |
|---|---|---:|---:|---:|
| 1 | macOS | 32.43 GB | 17.2% | — |
| 2 | Applications | 29.93 GB | 15.9% | 0.00 GB |
| 3 | App data | 48.33 GB | 25.7% | 13.33 GB |
| 4 | Developer | 69.68 GB | 37.0% | 63.68 GB |
| 5 | Containers & VMs | 18.76 GB | 10.0% | 18.76 GB |
| 6 | Personal files | 3.79 GB | 2.0% | — |
| 7 | Backups | 0.00 GB | 0.0% | — |
| 8 | System caches & logs | 13.15 GB | 7.0% | 4.59 GB |
| 9 | Trash | 0.00 GB | 0.0% | — |
| 10 | Purgeable | 2.29 GB | 1.2% | 2.29 GB |
| 11 | Other | 0.00 GB | 0.0% | — |
| 12 | Unreadable / unaccounted | 2.21 GB | 1.2% | — |

- `used = 188.14 GB`, `scanned = 183.64 GB`; **the walked buckets (2–9, 11) sum to the scanned
  bytes exactly** — the identity M8's gate asks for holds to the byte on this machine.
- **Other is 0.0% of used space**, not just under the 5% Phase 1b target but under the 15%
  M8-catalog-alone and 8% M10 interim targets too: the catalog (~288 rules; see
  [Engine](#engine)) plus the detector set leaves nothing of substance unclassified. The
  acceptance script's "top 10 unmatched paths" lists five directories, all reporting 0 bytes
  (`/Volumes`, `/mnt`, `/pkg`, `/private/tftpboot`, `/sw` — empty mount-point stubs that exist on
  every macOS install and were never populated on this one).
- Total reclaimable across all buckets: **102.65 GB, 54.6% of used**. App data is
  48.33 GB (13.33 GB of it reclaimable: 12.59 GB regenerable + 0.73 GB orphaned), Developer is
  69.68 GB (63.68 GB of it reclaimable: 36.63 GB regenerable + 27.04 GB tool-managed), well past
  the ≥20 GB / ≥25 GB / ≥40 GB gates M8/M10/M11 set for those two buckets in sequence.
- Applications' reclaimable 0.00 GB is not a rounding artefact of nothing being reclaimable — it
  is 53 KB of orphaned bytes (the near-empty Caskroom directories of the cask-only apps, see
  docs/11) rounded down; the bucket is 29.93 GB of user-data bundles by design (D13, apps are
  never suggested for deletion in phase one).

## Engine

`internal/classify/bench_test.go` (`BenchmarkEngineRun`, `STORIX_BENCH_ROOT` over the whole data
volume), measured the same day on this machine: the 288 catalog rules compile in **0.35 ms** and
one classification pass (`Engine.Run`) over **495,000 retained nodes** takes **29 ms**, allocating
10.9 MB in 71,000 allocations — about 390 B of heap per node. Budget was 300 ms; the pass costs a
tenth of it and well under 0.2% of the walk it follows.

That 29 ms is the engine alone. The end-to-end `classify` timing the JSON report carries
(`detect.Classify` for every detector plus `Engine.Run`) was **98 ms** in the scan behind this
document's bucket table, over 511,966 nodes — still two orders of magnitude inside budget.

## Probe timing

Probes run concurrently with the walk (D12); the acceptance gate is that the slowest one still
finishes before the walk does. Measured:

| Stage | Duration |
|---|---:|
| Walk | 21.17 s |
| Facts | 31 ms |
| Classify (engine + all detectors) | 124 ms |
| Ledger | 3 ms |
| Total | 21.40 s |

The slowest individual probe was `apps` at 12.79 s, almost all of it one `lsregister -dump` call
timing out at its 10 s limit (see [Detectors](#detectors)); `homebrew`'s four-call sequence plus
`brew cleanup -n` was the next slowest at 12.45 s. Both finish comfortably inside the 21.17 s
walk, so `Timing.Probe < Timing.Walk` holds as M9's gate requires.

## Detectors

| Detector | State | Duration | Reason |
|---|---|---:|---|
| aimodels | degraded | 132 ms | `ollama list`: server not responding — could not find ollama app; weights on disk still counted |
| apps | degraded | 12,789 ms | `lsregister`: timed out after 10.022 s |
| backups | degraded | 0.5 ms | `MobileSync/Backup` could not be listed (EPERM); needs Full Disk Access |
| cli-tools | ok | 0.0 ms | — |
| colima | missing * | 3.5 ms | neither `colima` nor `limactl` is on the path |
| docker | missing * | 0.0 ms | no `/Applications/Docker.app` and no `com.docker.docker` container |
| ecosystems | missing | 0.1 ms | none of Flutter, .NET, Haskell, Bazel, ccache, Conan, CocoaPods, Elixir, PHP, Zig has a directory here |
| go | ok | 192 ms | — |
| homebrew | ok | 12,452 ms | — |
| ide | ok | 1.0 ms | — |
| jvm | ok | 0.6 ms | — |
| kubernetes | ok | 0.0 ms | — |
| nix | missing | 2.7 ms | no `/nix` store and no `nix` on the path |
| node | ok | 2,115 ms | — |
| orbstack | ok | 2,424 ms | — |
| podman | missing * | 3.1 ms | `podman` is not on the path |
| projects | ok | 2,423 ms | — |
| python | ok | 1,888 ms | — |
| ruby | ok | 276 ms | — |
| rust | ok | 137 ms | — |
| vms | missing | 0.1 ms | no UTM, Parallels, VMware, VirtualBox directory exists |
| **xcode** | **ok \*** | 2,175 ms | gate passed: first-launch components installed, simctl queried |

`*` marks `Unverified() == true` — written from documentation rather than confirmed against a
running installation of the tool. No detector reported `panic` or `timeout`; every `missing` and
`degraded` state falls back cleanly to its static catalog rules, which is what keeps Other at
0.0% even with six tools absent from this machine.

## Conflicts

`Classification.Conflicts`, 1,843 total, by kind:

| Kind | Count |
|---|---:|
| rule over rule | 944 |
| detector over rule | 451 |
| apps over rule | 433 |
| detector over apps | 10 |
| detector over detector | 5 |

**Zero conflicts have a rule beating a detector or the application inventory** — the precedence
rule docs/03 states (Detector > Apps > Rule) is never violated on this machine, which is the
property `scripts/accept-1b.sh --strict` actually checks (rule-over-rule and rule-over-detector
are structurally different: a catalog with both a specific rule and a catch-all colliding on the
same node is expected and is not a violation of anything).

## The six largest projects

`internal/detect/projects`, sorted by build-artifact bytes (the gate as restated per the M11
critique fix — a "nearest `.git`" rule alone misses four of these six, which have no top-level
`.git`):

| Artifact bytes | Project | Notes |
|---:|---|---|
| 5.45 GB | `~/code/gib/armchair` | git |
| 1.80 GB | `~/code/gib/apres` | git |
| 1.60 GB | `~/code/lumenic` | git |
| 1.21 GB | `~/code/fofo` | git |
| 0.67 GB | `~/code/toto` | manifest only, no top-level `.git` |
| 0.47 GB | `~/code/mochi` | manifest only, no top-level `.git`; also the user's own build (docs/11) |

## Containers: OrbStack host vs. daemon

| | Bytes |
|---|---:|
| Host allocated (`data.img.raw`, 245.11 GB apparent) | 18.76 GB |
| Host allocated (`swap.img`, 1.07 GB apparent) | 4 KB |
| Guest reported — Images (37, 11 active), 54% reclaimable | 9.85 GB |
| Guest reported — Containers (12, 2 active), 99% reclaimable | 0.14 GB |
| Guest reported — Local Volumes (23, 3 active), 19% reclaimable | 4.76 GB |
| Guest reported — Build Cache (77 entries) | 5.09 GB |
| **Guest reported total** | **19.83 GB** |

The daemon accounts for 19.83 GB inside an image the host has given 18.76 GB — the image is
sparse and its contents compress, so the guest's figure is the larger one; both numbers are
shown rather than reconciled, which is the whole point of the containers view (docs/04).

## Acceptance script

`scripts/accept-1b.sh --no-build` (equivalent to `--strict` here, since every check passed):

```
ok   the walked buckets sum to the scanned bytes exactly
ok   Other is 0.0% of used space, under the 5% limit
ok   no detector panicked
```

No `--other-max` override was needed — the default 5% target is met by the M8 catalog alone
plus the full M9–M11 detector set, not just after tuning.

## Not verified on this machine

Several things ship from documentation with fixtures rather than from a machine that has the
tool, per the phase 1b decision to ship full `docs/04` scope even where this machine cannot
exercise every path (see `docs/07-decisions.md`):

- **Docker Desktop.** Not installed here (OrbStack owns `/var/run/docker.sock`); the detector is
  gated on `/Applications/Docker.app` or `~/Library/Containers/com.docker.docker` existing (D9 in
  the M9 fix), reports `missing` cleanly, and is exercised only against documentation-derived
  fixtures.
- **Colima and Podman.** Neither is installed; both report `missing` cleanly against a real
  `LookPath` failure, but their classify/degrade paths are fixture-only.
- **Xcode simulator parsing beyond zero devices.** The gate itself (`xcodebuild
  -checkFirstLaunchStatus`) and `xcrun simctl list devices -j` / `runtime list -j` all ran for
  real and returned real, if empty, JSON (zero devices, zero runtimes) — this machine has no
  simulator installed. The three historical device-availability spellings (`isAvailable`, an
  `availability` string containing "unavailable", `availabilityError`) are exercised only against
  documentation-derived fixtures, which is why `xcode` is the one detector marked `Unverified()`
  in the table above.
- **The sudo probe lane.** `probe.Exec` drops privilege to the invoking user via
  `SysProcAttr.Credential` (`mac.InvokingUser`) when running as root, so Homebrew (which refuses
  to run as root) still works under `sudo storix scan --system` — this code path exists and is
  unit-tested, but no scan in this document ran as root, so the behavior is unverified end to end
  on this machine.
- **The in-trash verdict.** `~/.Trash` returns EACCES to this (unprivileged) scan process, so the
  walker cannot see into it and no node under it is ever classified `in-trash` here, whatever the
  Finder shows. `DynamicLakePro`, which the phase 1b plan expected to surface as an in-Trash
  bundle, instead surfaces as `orphan-likely` (evidence: no application bundle anywhere the walk
  *can* see) — the correct answer given what this process can read, not a defect in the
  detector. Confirmed by direct check: `ls -la ~/.Trash` from this same shell also returns
  `Permission denied`.

## Review round (2026-09-23)

An independent code review of the phase 1b diff, with four deep-dive sub-reviews
(ledger/cache/JSON, classify engine, apps verdicts, concurrency/TUI), found and we fixed:

- **Ledger table denominator.** The twelve rows were measured against the data volume's
  used space while bucket 1 is the sibling volumes, so percentages summed to 115 %. The
  denominator is now the container's used space, container overhead joined bucket 12, and
  the identity line is computed rather than asserted.
- **Deep scan roots.** `--roots ~/Documents/X` classified as 100 % Other; the engine now
  inherits the claim the root's unwalked ancestors would give it.
- **Apps verdict clock.** Verdict ages and the 30-day keep window used wall-clock time at
  load, so a cached scan changed its answers over time; they now use the scan's own clock.
- **Vendor folders.** `~/Library/Application Support/Google` was attributed whole to Chrome,
  Android Studio's data included; vendor folders now key `vendor:<prefix>` and footprints
  subtract descendant components across owners.
- **Degraded probes.** A timed-out or unreadable probe could manufacture an orphan verdict;
  such verdicts are capped at Unknown with the reason, permission errors are never "gone",
  and the LaunchServices dump (11.6 s here, previously capped at 10 s) is budgeted so it
  completes: 58 applications listed, was 55.
- **Concurrency.** The walker now joins its progress reporter (a send-on-closed-channel
  window), the TUI quits on a second keypress during a scan, leaf-retainer hooks are
  panic-contained, and detector Wait honours each probe's own budget instead of a 2 s grace.
- **Safety.** The team-id cache is written via CreateTemp and rename, never through a
  symlink; the per-scan team resolver no longer lives on the shared detector singleton.

After the fixes: `go test -race ./...` 36 packages green, lint clean, `scripts/accept-1b.sh
--strict` exits 0 with Other at 0.0 %, no detector panicked, and no conflict where a rule beats
a detector or the apps inventory.
