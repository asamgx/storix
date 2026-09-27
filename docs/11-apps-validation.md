# 11 — Application Inventory Validation (2026-09-23)

The acceptance table below is `storix apps` / `storix apps --orphans --all` against a fresh,
`--no-cache` scan of this machine, cross-checked with the JSON document. Everything here is
read-only: the seven probes the `apps` detector runs touch nothing, and no bundle's contents were
opened. Five are commands — `brew --caskroom` (to find the Caskroom, with the two standard
locations as the fallback), `pkgutil`, `lsregister -dump`, `mdfind` (for application bundles
outside the standard folders) and `codesign` — and two are reads: the application folders and the
launchd plists.

Counts (`storix apps --json`, `.counts`): **55 bundles, 51 casks, 9 App Store** apps, 210 owners
resolved, 553 candidate directories considered.

**Re-run 2026-09-27** after the pre-merge fixes (`make build && ./storix scan > /dev/null &&
./storix apps --orphans --all --from-cache`, cross-checked against `apps --json --from-cache`):
55 bundles, **48 casks**, 9 App Store apps, 218 owners, 546 candidates. The cask-only,
orphan-likely and unknown-owner sections below are from this re-run. The installed table is still
the 2026-09-23 one; this pass did not re-measure it.

## Installed (top 20 by footprint)

`storix apps` (default sort: size). Footprint = bundle + linked App data + linked Developer
Caches + linked Developer data + linked Containers (VM):

| App | Footprint | Bundle | Data | Caches | Dev | VM | Source | Confidence |
|---|---:|---:|---:|---:|---:|---:|---|---|
| OrbStack | 19.50 GB | 728.6 MB | 1.4 MB | 4.5 MB | – | 18.76 GB | app | strong |
| Arc | 16.79 GB | 898.8 MB | 10.04 GB | 5.85 GB | – | – | app | strong |
| Spotify | 9.19 GB | 412.6 MB | 7.00 GB | 1.78 GB | – | – | app | strong |
| Xcode | 7.09 GB | 4.94 GB | 127 KB | 0 B | 2.15 GB | – | app | strong |
| AutoCAD | 5.89 GB | 3.49 GB | 2.38 GB | 18 MB | – | – | vendor | corroborating |
| Google Chrome | 5.24 GB | 2.23 GB | 2.70 GB | 313.1 MB | – | – | app | strong |
| Notion | 4.63 GB | 304.5 MB | 3.76 GB | 562.6 MB | – | – | app | likely |
| Stremio | 3.67 GB | 266.2 MB | 2.92 GB | 486.6 MB | – | – | app | strong |
| Lens | 3.35 GB | 1.69 GB | 517.9 MB | 1.15 GB | – | – | app | likely |
| WhatsApp | 3.28 GB | 691.2 MB | 1.62 GB | 969 MB | – | – | app | likely |
| Android Studio | 3.03 GB | 3.03 GB | 4 KB | 168 KB | 221 KB | – | app | strong |
| VS Code | 2.83 GB | 430.5 MB | 12 KB | 668 KB | 2.39 GB | – | app | corroborating |
| Zed | 1.83 GB | 423.1 MB | 4 KB | 1.5 MB | 1.41 GB | – | app | strong |
| Slack | 1.69 GB | 336.6 MB | 1.35 GB | 1.6 MB | – | – | app | strong |
| ChatGPT | 1.47 GB | 1.44 GB | 4 MB | 31 MB | – | – | app | likely |
| Brave Browser | 1.32 GB | 456.5 MB | 128.7 MB | 735.8 MB | – | – | app | strong |
| GarageBand | 1.20 GB | 1.20 GB | 0 B | 0 B | – | – | app | strong |
| Raycast | 797 MB | 139.2 MB | 546.3 MB | 111.5 MB | – | – | app | likely |
| Claude | 643.1 MB | 591.7 MB | 50.7 MB | 811 KB | – | – | app | strong |
| Bitwarden | 626.6 MB | 263.7 MB | 5.9 MB | 357 MB | – | – | app | likely |

Every one of the 55 `/Applications` bundles resolves to a footprint (D22: footprint is never
summed into the ledger). ChatGPT's bundle id is `com.openai.codex` — confirmed directly
(`apps:apps/bundle-id` source on that path) rather than assumed, and it is not confused with the
Codex CLI (`~/.codex`, `~/.cache/codex-runtimes`), which the `ide` detector attributes to
Developer under the label "Codex" because a `SourceDetector` claim always outranks an `apps`
claim on the same node (docs/03 precedence).

## Cask-only (1)

`storix apps` "cask installed, application missing":

| Cask | Expected app | Installed | Data | Evidence |
|---|---|---|---:|---|
| stremioservice | StremioService.app | 2026-02-10 | none found | the Caskroom `.app` entry is a dangling symlink (the version directory holds only `.metadata`); no data directory attributed to it separately from Stremio's own |

On 2026-09-23 this section also listed `cursor`, `devtoys` and `mattermost`. Those three casks were
uninstalled after that run. Their data is still on disk, so all three are now in orphan-likely
below (see Divergences).

## Orphan-likely (17, 3.19 GB)

`storix apps --orphans --all --from-cache`, confidence as the text report prints it (the JSON
`confidence` field in brackets where they differ):

| Owner | Size | Last write | Confidence | Evidence |
|---|---:|---|---|---|
| Cursor | 1.89 GB | 2026-07-28 | likely | `com.todesktop.230313mzl4w4u92` directories named after a bundle identifier; `~/.cursor` (1.51 GB) and `~/Library/Application Support/Cursor` (374.3 MB) are the `ide` detector's Developer claims, joined to the orphan through its keys; no bundle anywhere on the volume |
| Wondershare | 511.1 MB | 2026-04-22 | likely | `com.wondershare.Installer` and `com.wondershare.mac-drfoneframe` directories named after bundle identifiers; no bundle |
| Warp | 373.1 MB | 2025-11-25 | likely | `dev.warp.Warp-Stable` directory; no `Warp.app` on the volume |
| Antigravity | 201 MB | 2026-02-15 | likely | `com.google.antigravity*` directories named after a bundle identifier; no bundle anywhere on the volume |
| Mattermost | 128.4 MB | 2025-11-23 | likely | `Mattermost.Desktop.plist` plus Application Support and Logs named `Mattermost`; no bundle |
| Atlas | 62.6 MB | 2025-10-30 | likely | `com.openai.atlas*`; kept distinct from ChatGPT (`com.openai.codex`) per the alias table's non-distinct-suffix rule |
| GlobalProtect | 19.1 MB | 2025-12-29 | likely | installer receipt `com.paloaltonetworks.globalprotect.pkg`; no bundle |
| DynamicLake Pro | 5.4 MB | 2026-05-29 | likely | directory named after a bundle identifier; no bundle found (see [docs/10](10-bench-1b.md): the walker cannot see `~/.Trash`, so this is not the in-Trash case the original plan expected) |
| Cap | 852 KB | 2025-07-23 | likely | `so.cap.desktop`, no bundle |
| DevToys | 520 KB | 2025-11-13 | likely | `com.devtoys*` caches, prefs and WebKit data; no bundle |
| TabNine | 217 KB | 2025-07-26 | likely* | the evidence says "the only evidence is the directory name, so this is possible rather than likely" |
| boringNotch | 41 KB | 2025-07-26 | likely | `theboringteam.boringnotch` container, no bundle |
| Chromium | 12 KB | 2026-09-14 | likely* | the only copy on the volume is a staged Playwright download (`~/Library/Caches/ms-playwright/chromium-1161/chrome-mac/Chromium.app`), which does not count as installed; evidence again says "possible rather than likely" |
| Microsoft Edge | 12 KB | 2026-09-14 | likely* | directory-name evidence only |
| Vivaldi | 12 KB | 2026-09-14 | likely* | directory-name evidence only |
| Opera | 8 KB | 2026-09-14 | likely | `com.operasoftware.Opera`, no bundle |
| Mozilla | 0 B | 2026-07-22 | **possible** (JSON: `corroborating`) | `/Library/Application Support/Mozilla`, directory-name evidence only; nothing inside it belongs to an installed application |

`*`: `apps.orphanConfidence` (`internal/apps/verdict.go`) writes "possible rather than likely" only
when it returns `classify.Corroborating`, which the text report prints as "possible". Mozilla is
that case, and both columns agree. For TabNine, Chromium, Microsoft Edge and Vivaldi the same
sentence is in the evidence, but the reported confidence, in both the text and the JSON, is
`likely`. So on this build something after `orphanConfidence` raises the verdict while the
evidence line stays. The tier and the prose disagree for those four rows. This is an open
follow-up for the apps lane, not a data problem: the orphan verdict itself is right either way.

UI Launcher, orphan-likely on 2026-09-23, is now **installed**. It lives under
`/Library/Application Support/Autodesk/AdODIS/…`, which the apps inventory now finds, so it was a
false orphan and is fixed.

## Non-app / own-build (correctly not flagged)

None of the following appear in the cask-only, orphan-likely, or unknown-owner sections; each is
attributed to something that explains it, per `storix apps --json`:

| Name | Attribution |
|---|---|
| `stremio-server` | folded into the installed Stremio's own footprint, not a separate owner |
| `k9s`, `lazygit`, `zoxide`, `gk`, `GitKrakenCLI`, `turborepo`, `fastmcp`, `go`, `net.temurin.21.jdk`, `checkpoint-nodejs`, `create-next-app-nodejs`, `nextjs-nodejs`, `segment`, `com.segment.storage.*`, `firestore`, `CEF`, `.wrangler`, `Claude Code` (`claude-code@latest`) | `nonApp`, owner key `cli:<name>` — 19 entries total in this category |
| `storix` | `ownBuild`, owner key `project:storix` (this very repository under `~/code`) |
| Mochi | `ownBuild`, owner key `project:mochi` (the user's own build, project at `~/code/mochi`) |
| Autodesk / AutoCAD | `installed`, source `vendor` (depth-3 vendor folder rule; confidence `corroborating` because attribution is by folder, not a single bundle id) — see the installed table above |
| `Google` directory (`~/Library/Application Support/Google`, `~/Library/Caches/Google`, `~/Library/Google/GoogleSoftwareUpdate`, Keystone LaunchAgents) | folded into Google Chrome's own footprint via `apps/cask-zap` and `apps/alias`, not a standalone vendor row |
| `Microsoft` directory (`~/Library/Application Support/Microsoft`) | folded into VS Code's own footprint via `apps/alias` |
| Codex CLI (`~/.codex`, `~/.cache/codex-runtimes`, `~/Library/Application Support/Codex`) | claimed by the `ide` detector into Developer (`SourceDetector` outranks `SourceApps`), separate from the ChatGPT app whose real bundle id is `com.openai.codex` |

## Unknown owner

105 of 546 candidate directories in the 2026-09-27 re-run (largest: `go-build` 3.91 GB, `pnpm`
1.48 GB, `ms-playwright` 1.06 GB, `Homebrew` 758.4 MB) — every one of these is itself a developer-tool cache name that the
`node`/`go`/`homebrew`/`cli-tools` detectors already claim at a *different* node (e.g. the real
`go-build` cache under `~/Library/Caches/go-build` is Developer; the `unknown` list here is
picking up same-named directories the `apps` detector's own candidate scan visits but that a
higher-precedence detector has already claimed elsewhere in the tree, so they cost nothing in the
ledger). Only `go-build` is over 3 GB, and it is the Go build cache the `go` detector already counts. The total is well inside the tuning-log workflow the plan
describes rather than a gap in coverage.

## Divergences from the original expectation, with reasons

- **GlobalProtect is orphan-likely, not installed.** The plan's initial corpus line expected it
  installed; verified read-only during planning (`pkgutil --pkg-info` shows install location
  `Applications/GlobalProtect.app`, which does not exist; `--files` are all gone; no
  `paloaltonetworks` LaunchDaemon) and confirmed again in this scan (D37).
- **DynamicLake Pro is orphan-likely, not in-Trash.** The plan expected an in-Trash verdict for a
  Trash-dwelling bundle; this scan process cannot list `~/.Trash` (EACCES, confirmed directly),
  so the walker never sees whatever is there and the verdict falls back to the directory-name
  evidence alone. Correct given what the process can read; see docs/10 for the confirmation.
- **TabNine, Chromium, Microsoft Edge and Vivaldi say "possible" in their evidence but carry
  `likely`**, in the text and in the JSON (see the note under the orphan table). The sentence is
  only written together with `Corroborating`, so on this build the confidence is raised after
  the evidence is written. That is a real inconsistency between the prose and the tier, and an
  open follow-up. Mozilla is the one row where both say "possible".
- **Cursor, DevToys and Mattermost moved from cask-only to orphan-likely** between the 09-23 run
  and the 09-27 re-run. Their casks were uninstalled in between (51 casks became 48) and their
  data stayed. So the cask receipts that used to explain the data are gone, and the
  bundle-identifier directories are the evidence left. Cursor keeps its 1.89 GB because the
  `ide` detector's claims on `~/.cursor` and `~/Library/Application Support/Cursor` now join
  the orphan through the product key. Before, they were attributed to nobody.
- **Antigravity shrank from 2.8 GB to 201 MB** because `~/.antigravity` was removed from this
  machine between the two runs. What is left is Application Support and the
  `com.google.antigravity*` caches and preferences.
- **UI Launcher was a false orphan and is now installed.** Its bundle is under
  `/Library/Application Support/Autodesk/AdODIS/…`, outside the standard application folders.
  The 09-23 build never looked there; the inventory now finds it.
- **ChatGPT's bundle id is `com.openai.codex`**, confirmed by `codesign`/plist evidence in this
  scan, which is why it is not confused with the separate Codex CLI (attributed to Developer by
  the `ide` detector) or with OpenAI's `Atlas` product (kept `Distinct` in the alias table so a
  shared `com.openai` vendor prefix cannot merge them).
- **`com.todesktop.230313mzl4w4u92` resolves to Cursor via the cask's own zap list**, not via a
  bundle match (Cursor.app is absent) — the receipt's `zap.trash` entries are the primary
  evidence, exactly as the plan's `apps/cask-zap` source records.
- **StremioService is a dangling Caskroom symlink**, not a normal cask-installed app: the version
  directory under `/opt/homebrew/Caskroom/stremioservice` holds `.metadata` only, so it is
  reported cask-only with "none found" for data rather than a missing-app evidence trail.
