# storix — Design Docs

storix is a macOS-native CLI that explains where your disk space went. It produces a
ledger where every byte on the data volume lands in exactly one bucket, links application
data back to the apps that own it, flags data left behind by removed apps, and understands
developer tooling (package managers, toolchains, project build artifacts, containers, VMs).

Phase one is strictly read-only: it breaks storage down and links things together. It does
not delete anything.

## Documents

| File | What it covers |
|---|---|
| [01-vision.md](01-vision.md) | Goals, non-goals, target user, guiding principles |
| [02-storage-model.md](02-storage-model.md) | The ledger buckets and the macOS filesystem semantics we must get right |
| [03-architecture.md](03-architecture.md) | Walker, classification engine, scan cache, detectors, TUI, CLI surface |
| [04-detectors.md](04-detectors.md) | App linking and orphan detection, developer detectors, containers/VMs, rule catalog |
| [05-tech-stack.md](05-tech-stack.md) | Libraries, project layout, conventions, testing |
| [06-roadmap.md](06-roadmap.md) | Phases, milestones, acceptance criteria |
| [07-decisions.md](07-decisions.md) | Decisions taken, assumptions, open questions |
| [08-validation-round-1.md](08-validation-round-1.md) | What was verified on the machine, what was wrong, what changed |
| [09-bench-1a.md](09-bench-1a.md) | Phase 1a benchmark numbers: `du` vs. the parallel walker, memory, cache, reconciliation, and the manual acceptance checklists |

## Status

- 2026-09-22: initial plan written.
- 2026-09-22: validation round 1 complete (independent critic review + empirical checks on
  the planning machine). Verdict GO-WITH-FIXES; all blocking fixes applied to these docs.
  Evidence in [08-validation-round-1.md](08-validation-round-1.md). Three new open
  questions (Q8–Q10) in 07 for review round 2, none blocking phase 1a.
- 2026-09-22: phase 1a implementation plan written (`asamgx/phase-1a-walker`); Q8/Q9
  resolved as predicted, Q11 added (07-decisions.md), D29–D33 recorded.
- 2026-09-22: phase 1a milestones M0–M5 shipped (scaffold, `mac`/`volume` facts and
  `doctor`, the parallel walker, ledger reconciliation and `--report`/`--json`, the flat
  scan cache) — see [06-roadmap.md](06-roadmap.md) for evidence per milestone. The
  `getattrlistbulk` experiment is deferred (D31): the walker already beats the performance
  target by roughly 2× without it. The TUI (M6) is in progress on the same branch. Bench
  numbers and the acceptance checklist are in
  [09-bench-1a.md](09-bench-1a.md).

## Machine snapshot used while planning

Collected read-only on 2026-09-22 from the development machine this tool is built for.

| Item | Value |
|---|---|
| Disk | 228 GB total, ~178 GB used on data volume, ~38 GB free |
| Files on data volume | ~3.5 million |
| Local Time Machine snapshots | none at time of planning |
| Package managers | brew, npm, pnpm, yarn, bun, cargo, rustup, go, pyenv, pip, uv, gem |
| Containers | OrbStack installed; `~/.orbstack` and `~/.docker` present |
| Dev dirs present | `~/.nvm`, `~/.pyenv`, `~/.cargo`, `~/.rustup`, `~/go`, `~/.bun`, `~/.npm`, `~/.pnpm-store`, `~/Library/pnpm`, `~/Library/Developer`, `~/.vscode`, `~/.cursor`, `~/.claude` |
| Go | 1.25.5 darwin/arm64 |
