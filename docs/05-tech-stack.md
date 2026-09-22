# 05 — Tech Stack

## Language and toolchain
- Go 1.25 (present on the machine), darwin/arm64 primary. Build for amd64 too; test on arm64.
- cgo allowed for the tiny `setiopolicy_np` call (dataless-file safety) if
  `golang.org/x/sys/unix` does not expose it; otherwise pure Go.

## Libraries

| Purpose | Library | Notes |
|---|---|---|
| CLI | `github.com/spf13/cobra` + `github.com/charmbracelet/fang` | Fang wraps Cobra with styled help, version, and error output |
| TUI framework | `github.com/charmbracelet/bubbletea` | Elm-style; scanning progress via `tea.Cmd` streaming messages |
| Styling | `github.com/charmbracelet/lipgloss` | Colors, borders, adaptive light/dark, bars via block glyphs |
| Components | `github.com/charmbracelet/bubbles` | `table`, `viewport`, `progress`, `spinner`, `textinput`, `help`, `key` |
| Logging | `github.com/charmbracelet/log` | Debug log to file when `--debug` |
| Syscalls | `golang.org/x/sys/unix` | `Statfs`, `Lstat`/`Stat_t`, `Getattrlistbulk`, `Setattrlist` if needed |
| plist | `howett.net/plist` | Info.plist (XML and binary), pkg receipts, `lsregister` output parsing |
| Sizes | `github.com/dustin/go-humanize` or in-house | Must support decimal and binary; likely write a 30-line formatter to match Finder exactly |
| Config | `github.com/BurntSushi/toml` | Simple TOML config |
| Cache encoding | `encoding/gob` first; evaluate `github.com/vmihailenco/msgpack` if size/speed is poor | Benchmark on the real tree |
| Testing | stdlib `testing`, `github.com/rogpeppe/go-internal/testscript` for CLI, `github.com/charmbracelet/x/exp/teatest` for TUI | Fixture trees built in `t.TempDir()` |

Avoid: a treemap renderer (unnecessary), sqlite (overkill for phase one), heavy DI frameworks.

## Project layout

```
storix/
  cmd/storix/main.go            entrypoint, cobra root
  internal/
    cli/                        cobra commands (scan, explain, apps, dev, cache, doctor)
    scan/                       orchestrator: Collect -> Walk -> Finish -> Build -> Persist,
                                shared by the CLI and the TUI so `r` rescan does not
                                duplicate CLI logic (added in the phase 1a implementation plan)
    walk/                       walker, node tree, hard-link dedupe, skip list, IO policy
    volume/                     statfs, diskutil, tmutil facts
    classify/                   rule model, catalog, engine, conflict resolution, provenance
    detect/                     Detector interface + one package per detector
      apps/  homebrew/  node/  python/  rust/  golang/  ruby/  jvm/  xcode/  ide/
      aimodels/  projects/  orbstack/  docker/  colima/  vms/  nix/  backups/
    probe/                      shell-out helper with timeouts, PATH resolution, caching
    ledger/                     bucket aggregation, reconciliation, reclaim totals
    cache/                      scan persistence, retention, ownership fix under sudo
    report/                     text and JSON renderers
    tui/                        bubbletea app: models per view, styles, keymap
    units/                      byte formatting (decimal/binary)
    mac/                        macOS helpers: bundle Info.plist, team id via codesign, lsregister, TCC probe, terminal identity
    testutil/                   fixture builder (t.TempDir() trees: files, sparse files,
                                hard links, symlinks, deep/wide trees, unreadable dirs;
                                added in the phase 1a implementation plan)
  docs/                         these documents
  scripts/                      bench.sh (du vs storix harness) and other maintenance scripts
  testdata/                     fixture trees and captured probe outputs
```

## Conventions
- Every detector ships with captured probe output in `testdata/` so classification tests run
  without the tool installed.
- Rules have IDs (`homebrew.cache`, `xcode.deriveddata`) used in provenance and tests.
- No file is ever opened for reading during a scan; enforce with a lint-style test that
  greps `walk/` and `detect/` for `os.Open`/`os.ReadFile` outside allow-listed plist reads.
- Context propagation everywhere; Ctrl-C cancels walk and probes cleanly and still writes
  a partial cache marked `incomplete`.
- Decimal units default; formatting tested against known Finder values.

## Build and release
- `go build ./cmd/storix`; `make` targets for `build`, `test`, `lint` (`golangci-lint`), `bench`.
- Later: `goreleaser` with a Homebrew tap. Notarization is not needed for a CLI distributed via brew.
