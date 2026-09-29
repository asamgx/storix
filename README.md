# storix

storix is a macOS-native CLI that explains where your disk space actually went. It walks
the data volume and produces a reconciled ledger where every byte lands in one of twelve
buckets — Applications, App data, Developer, Containers & VMs, Personal files, and so on — and
the buckets sum to what the volume itself reports as used; whatever the scan could not see is
named explicitly rather than silently missing. It links application data back to the app that
owns it and flags data left behind by applications that are no longer installed. It is
read-only throughout: storix never deletes, moves, or modifies anything, and it never opens a
file to size it (sizes come from `lstat`/`getattrlist`, not from reading file contents). Acting
on any of this is phase two. See [`docs/`](docs/) for the full design and roadmap, in particular
[`docs/01-vision.md`](docs/01-vision.md) for the problem storix solves and
[`docs/06-roadmap.md`](docs/06-roadmap.md) for what is implemented so far.

## Install

Build from source (no released binary yet):

```sh
git clone https://github.com/asamgx/storix
cd storix
make build      # -> ./storix
```

Requires Go 1.25+ on macOS (Apple silicon or Intel). `make build-nocgo` builds a
`CGO_ENABLED=0` variant for testing cross-compilation; it still runs correctly, with the
dataless-file protection and the Foundation purgeable-space reading reported as unavailable
(`storix doctor` shows which build you have).

## Usage

```sh
./storix                # scan (or load a fresh cache) and open the interactive browser
./storix scan --report  # walk the data volume, print the ledger (the default)
./storix scan --json    # the same scan as versioned JSON (schema 1)
./storix apps           # installed applications, their footprint, and what's left behind
./storix dev            # toolchains, package caches, and code-root projects
./storix explain PATH   # why this directory is counted the way it is, and who owns it
./storix doctor         # what this build, this terminal, and this machine's tools can see
./storix cache list     # scans saved under ~/Library/Application Support/storix/scans
```

With no subcommand, storix opens an interactive terminal browser over the stored scan (see
[The interactive browser](#the-interactive-browser)) when one is under an hour old, or a fresh
walk otherwise; redirected to a file or a pipe it prints the text report instead.

### `storix scan`

`storix scan` walks `/System/Volumes/Data` (the actual data volume; see
[`docs/02-storage-model.md`](docs/02-storage-model.md) for why the visible `/` is not it) and
prints a **ledger**: the bytes it could account for, the space macOS reports as purgeable, and
the residual between those and what the volume calls used. Useful flags:

| Flag | Meaning |
|---|---|
| `--report` / `--json` | text ledger (default) or a versioned JSON document |
| `--roots PATH...` | scan a different root (default: the data volume) |
| `--parallelism N` | worker count (default `min(2·NumCPU, 16)`) |
| `--threshold SIZE` | fold files smaller than this into their parent (default `64KB`) |
| `--full` | in `--json`, drop the depth/size limits and emit every node |
| `--binary` | KiB/MiB/GiB instead of Finder's KB/MB/GB |
| `--debug` | print stage timings and memory statistics |
| `--system` | also attempt root-only system directories (needs `sudo`) |
| `--code-roots PATH...` | directories to look for source projects under (default: `~/code` plus `~/Developer`, `~/Projects`, `~/src`, `~/dev`, `~/work` if they exist) |
| `--disable-detector NAME` | switch off one tool detector by name (repeatable); the catalog's static rules still bucket its paths, just without the detector's own evidence |

Run it with `sudo storix scan --system` to include root-only directories in the same ledger;
probes still run as the invoking user, not as root, because Homebrew and several other tools
refuse to run as root at all (see [`docs/07-decisions.md`](docs/07-decisions.md) D39).

### `storix apps`

`./storix apps` lists every installed application with its **footprint** — bundle size plus
every directory linked to it across App data, Developer, and Containers & VMs, since a single
bucket can never say what one application costs. It also lists Homebrew casks whose application
has been removed, owners whose application appears to be gone (**orphan-likely**, with evidence
and a confidence level), and directories nothing could be attributed to. `--orphans` prints just
the last three sections; `--all` lists every row instead of a summary; `--json` emits the same
document as JSON. See [`docs/11-apps-validation.md`](docs/11-apps-validation.md) for a worked
example against this machine.

### `storix dev`

`./storix dev` prints the Developer bucket on its own: every tool detector's rows (package
caches, toolchains, installed versions with a "current" marker), and the source projects under
your code roots with their build-artifact bytes and last activity. `--projects` prints just the
projects table.

### `storix explain PATH|BUNDLEID`

`./storix explain` answers the question the ledger raises about one thing: given a path, why is
this directory in that bucket, who owns it, and is it reclaimable — printing the rule, detector,
or application inventory that claimed it, the evidence behind that claim, and any claim that
lost to it. Given a bundle id, an owner key (`app:…`, `cask:…`, `cli:…`, `project:…`), or a
product name, it prints that owner's whole footprint instead, wherever the ledger counted it.
Unlike the other commands, `explain` uses the latest stored scan whatever its age rather than
insisting on a recent one, since an explanation is about a stored answer.

### `storix doctor`

Prints the environment a scan would run in: whether this build can turn off dataless
materialization and read purgeable space, which terminal you're running in, whether Full Disk
Access is granted, the volume and container space numbers, which mounts nest inside the scan
root, and local Time Machine snapshots. Run this first if a scan's numbers look off.

It also lists every registered detector, which of its external tools (`brew`, `docker`, `git`,
and so on) it found on the augmented `PATH` a real scan searches, and — because it is the one
place in the program allowed to invoke it outside a scan — the Xcode simulator gate's verdict:
`storix` never runs `xcrun simctl` unless `xcodebuild -checkFirstLaunchStatus` has already exited
0 under `DEVELOPER_DIR`, because running `simctl` ungated can trigger an unwanted CoreSimulator
component install.

### `storix cache`

Every scan is stored under `~/Library/Application Support/storix/scans`. `storix cache list`
shows saved scans newest-first, `storix cache prune` keeps only the newest few, `storix cache
clear` deletes them all, and `storix cache path` prints the storage directory.

## The interactive browser

Running `storix` with no subcommand opens a terminal UI with six views, switched with the
number keys or `tab`:

| Key | View | What it shows |
|---|---|---|
| `1` | Ledger | the twelve buckets as bars with bytes, percent, and a reclaimable sub-bar; `enter` drills into a bucket |
| `2` | Browse | an ncdu-style directory table for any subtree, with owner and reclaim chips per row |
| `3` | Apps | installed applications by footprint, then cask-only/orphan-likely/unknown owners |
| `4` | Developer | tool detector rows (caches, toolchains, versions with a "current" marker) and code-root projects |
| `5` | Containers | each runtime's host-allocated size next to what its daemon reports using |
| `6` (or `u`) | Unaccounted | unreadable directories with their errno and a permission hint |

`w` toggles a "why" side panel in every view: for a Browse row it shows the rule, detector, or
application inventory that claimed the node, the evidence behind that claim, and any claim that
lost to it; for an Apps row it shows the owner's verdict and evidence. `o` reveals the selected
row in Finder, `y` copies its path, `s`/`n`/`m` change sort order, and `/` filters. `?` opens the
full help.

## Permissions

storix runs unprivileged by default. Without **Full Disk Access**, several protected
directories (Mail, Messages, Safari's data vault, and similar) come back permission-denied;
storix reports each one by path and reason rather than undercounting silently, and `storix
doctor` tells you whether Full Disk Access is granted and how to grant it. The **first scan**
of `~/Desktop`, `~/Documents`, and `~/Downloads` can trigger macOS's one-time TCC permission
dialog for your terminal app — approve it so those directories are counted.

## Why the ledger says "does not reconcile"

On real machines the walked total, the purgeable space, and the volume's reported "used"
figure do not add up exactly, and storix says so rather than hiding it. The gap is mostly
filesystem metadata that `lstat` cannot see (APFS directory inodes report zero blocks) plus
APFS clones that the walk counts once per path but the filesystem stores once per block — both
are named in the residual line, not silently absorbed into the total.

## More

Full design, architecture, and validation notes live in [`docs/`](docs/), including measured
benchmark numbers in [`docs/09-bench-1a.md`](docs/09-bench-1a.md) and
[`docs/10-bench-1b.md`](docs/10-bench-1b.md), and the application inventory's acceptance run in
[`docs/11-apps-validation.md`](docs/11-apps-validation.md).
