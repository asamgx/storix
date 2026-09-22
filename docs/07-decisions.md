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
- Q9: cgo policy. The dataless IO policy and possibly purgeable each need one cgo call, and
  `getattrlistbulk` needs a raw syscall. Proposal: one small cgo file behind a build tag; the
  pure-Go build reports "dataless protection unavailable" so cross-compilation stays possible.
- Q10: `storix explain` accepting a bundle id in addition to a path. Proposal: yes, cheap.

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
