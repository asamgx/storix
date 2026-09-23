# 11 — Application Inventory Validation (2026-09-23)

The acceptance table below is `storix apps` / `storix apps --orphans --all` against a fresh,
`--no-cache` scan of this machine, cross-checked with the JSON document. Everything here is
read-only: the seven probes the `apps` detector runs (`brew --caskroom` is actually the
`homebrew` detector's; `apps` itself runs `pkgutil`, `lsregister -dump`, `codesign`, and reads
plists/receipts, never brew) touch nothing, and no bundle's contents were opened.

Counts (`storix apps --json`, `.counts`): **55 bundles, 51 casks, 9 App Store** apps, 210 owners
resolved, 553 candidate directories considered.

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

## Cask-only (4)

`storix apps` "cask installed, application missing":

| Cask | Expected app | Installed | Data | Evidence |
|---|---|---|---:|---|
| cursor | Cursor.app | 2025-07-23 | 1.89 GB | cask zap lists `~/.cursor`, `~/Library/Application Support/Cursor`, `~/Library/Caches/com.todesktop.*` — `com.todesktop.230313mzl4w4u92` resolves to Cursor through this zap list, not a bundle match |
| devtoys | DevToys.app | 2025-07-24 | 532 KB | cask zap / prefs match `com.devtoys*` |
| mattermost | Mattermost.app | 2025-08-05 | 128.4 MB | Application Support, Logs, Preferences all named `Mattermost` |
| stremioservice | StremioService.app | 2026-02-10 | none found | the Caskroom `.app` entry is a dangling symlink (the version directory holds only `.metadata`); no data directory attributed to it separately from Stremio's own |

## Orphan-likely (14, 3.77 GB)

`storix apps --orphans --all`, confidence and evidence as reported:

| Owner | Size | Last write | Confidence | Evidence |
|---|---:|---|---|---|
| Antigravity | 2.8 GB | 2026-02-15 | likely | directory named after a bundle identifier (`com.google.antigravity*`); no bundle anywhere on the volume |
| Wondershare | 511.1 MB | 2026-04-22 | **possible** (JSON: `corroborating`) | directory-name evidence only; no `Wondershare.app` anywhere |
| Warp | 373.1 MB | 2025-11-25 | likely | `dev.warp.Warp-Stable` directory; no `Warp.app` on the volume |
| Atlas | 62.6 MB | 2025-10-30 | likely | `com.openai.atlas*`; kept distinct from ChatGPT (`com.openai.codex`) per the alias table's non-distinct-suffix rule |
| GlobalProtect | 19.1 MB | 2025-12-29 | likely | pkg receipt `com.paloaltonetworks.globalprotect.pkg` whose install location and `--files` are both gone, no LaunchDaemon |
| DynamicLake Pro | 5.4 MB | 2026-05-29 | likely | directory named after a bundle identifier; no bundle found (see [docs/10](10-bench-1b.md) — the walker cannot see `~/.Trash`, so this is not the in-Trash case the original plan expected) |
| UI Launcher | 1.9 MB | 2026-05-22 | likely | `com.electron.ui-launcher` prefs, no bundle |
| Cap | 852 KB | 2025-07-23 | likely | `so.cap.desktop`, no bundle |
| TabNine | 217 KB | 2025-07-26 | likely* | evidence text: "the only evidence is the directory name, so this is possible rather than likely" — reported confidence tier is still `likely` |
| boringNotch | 41 KB | 2025-07-26 | likely | `theboringteam.boringnotch` container, no bundle |
| Chromium | 12 KB | 2026-09-14 | likely* | the only copy on the volume is a staged Playwright download (`~/Library/Caches/ms-playwright/chromium-1161/chrome-mac/Chromium.app`), which does not count as installed; evidence text again says "possible rather than likely" |
| Microsoft Edge | 12 KB | 2026-09-14 | likely* | directory-name evidence only |
| Vivaldi | 12 KB | 2026-09-14 | likely* | directory-name evidence only |
| Opera | 8 KB | 2026-09-14 | likely | `com.operasoftware.Opera`, no bundle |

`*` — for TabNine, Chromium, Microsoft Edge and Vivaldi, the verdict's evidence text explicitly
downgrades the read to "possible rather than likely", but the `Confidence` field the report
sorts and colors by is still `classify.Likely` on this build; there is no `Possible` tier between
`Likely` and `Corroborating` in `internal/classify/bucket.go`. This is a genuine wording/tier
mismatch worth a follow-up, not a data problem — the underlying evidence and the orphan verdict
itself are correct either way.

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

108 of 553 candidate directories (largest: `go-build` 2.27 GB, `pnpm` 1.48 GB, `ms-playwright`
1.06 GB, `Homebrew` 750.6 MB) — every one of these is itself a developer-tool cache name that the
`node`/`go`/`homebrew`/`cli-tools` detectors already claim at a *different* node (e.g. the real
`go-build` cache under `~/Library/Caches/go-build` is Developer; the `unknown` list here is
picking up same-named directories the `apps` detector's own candidate scan visits but that a
higher-precedence detector has already claimed elsewhere in the tree, so they cost nothing in the
ledger). None is over 3 GB and the total is well inside the tuning-log workflow the plan
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
- **Chromium, Microsoft Edge, Vivaldi are "possible" by evidence text but "likely" by the stored
  confidence tier**, as detailed above — a real, minor inconsistency between the prose and the
  enum, not a classification error.
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
