# 01 — Vision

## Problem

macOS reports storage in coarse categories (Applications, Documents, System Data, ...).
"System Data" is a black box that on a developer machine is dominated by things Apple's UI
never names: package manager caches, language toolchains, container disk images, Xcode
derived data, simulators, and data left behind by applications that were removed long ago.

Existing tools each cover a slice:

- `du`, `ncdu`, `dust`, `dua`: fast generic tree sizing, no macOS semantics, no attribution.
- DaisyDisk, GrandPerspective, OmniDiskSweeper: visual, still generic, GUI only.
- AppCleaner, Pearcleaner: app-to-data linking, but only for the app you point them at.
- `npkill`, `kondo`, `cargo-sweep`: project build artifacts for one ecosystem each.
- `brew cleanup`, `docker system df`, `xcrun simctl`: per-tool, no global view.

Nothing gives a single, reconciled, developer-aware answer to "where did 178 GB go?"

## Goal

A CLI that, in one run, answers:

1. **What is taking my storage**, as a ledger where every byte lands in exactly one bucket
   and the buckets sum to what the volume reports as used.
2. **What is inside "System Data"**, named and attributed.
3. **Which data belongs to which app**, including a per-app footprint (bundle + containers +
   caches + support files + preferences + logs).
4. **Which app data is orphaned**: the app is gone but its data remains.
5. **What developer tooling is holding**: per package manager, per toolchain version,
   per project build directory, per container/VM image.
6. **What is reclaimable and how**, tagged now, acted on in a later phase.

## Non-goals (phase one)

- Deleting, moving, or modifying any user file. Phase one is read-only.
- Scanning external volumes, network mounts, or Time Machine destinations by default.
- Being a general-purpose `du` replacement for Linux. macOS first; portability is not a
  design constraint.
- A GUI. Terminal UI only.
- Real-time monitoring. Scans are on demand (scheduled diffs are a later phase).

## Target user

A single developer on Apple silicon macOS with many package managers, a container runtime
(OrbStack and/or Docker Desktop), IDEs, and a long history of installing and removing apps.

## Guiding principles

1. **Reconcile or admit it.** Totals must match `statfs`. Anything the scan cannot see is
   reported explicitly as unreadable/unaccounted, with the reason and the fix.
2. **Never open a file.** Sizes come from `lstat`/`getattrlist`. Opening files is slow and
   can trigger iCloud / File Provider downloads.
3. **Allocated size, not apparent size.** Sparse disk images and APFS clones make `st_size`
   lie. Use block counts.
4. **Match Finder's numbers.** Decimal units by default so the tool agrees with
   System Settings → Storage.
5. **Attribution with provenance.** Every classified path records which rule or detector
   claimed it and why. The UI can always answer "why is this here?"
6. **Confidence, not certainty.** Orphan detection is heuristic. Show strong / likely /
   unknown and the evidence, never a bare verdict.
7. **Ask the tool, don't guess the path.** When Homebrew, pnpm, Go, uv, Docker, or Xcode can
   report their own paths and usage, use that and fall back to known defaults.
8. **Degrade gracefully.** A missing tool, a slow command, or a permission error must never
   fail the scan. It becomes a note in the report.
9. **Persist every scan.** The cache makes drill-down instant and enables "what grew since
   last time" later.
10. **Reclaim through native cleaners (later).** When phase two arrives, prefer
    `brew cleanup`, `docker system prune`, `simctl delete unavailable`, and moving to Trash
    over deleting files directly.
