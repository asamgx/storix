# storix

storix is a macOS-native CLI that explains where your disk space actually went. It walks
the data volume and produces a reconciled ledger where every byte lands in one bucket — the
buckets sum to what the volume itself reports as used, and whatever the scan could not see is
named explicitly rather than silently missing. It is read-only: storix never deletes, moves,
or modifies anything, and it never opens a file to size it (sizes come from `lstat`/
`getattrlist`, not from reading file contents).

This is phase one: a correct, fast, honest walker with a terminal UI. It does not yet
classify data by app or developer tool, link data back to the app that owns it, or find data
left behind by removed apps — that is phase two. See [`docs/`](docs/) for the full design and
roadmap, in particular [`docs/01-vision.md`](docs/01-vision.md) for the problem storix solves
and [`docs/06-roadmap.md`](docs/06-roadmap.md) for what is implemented so far.

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
./storix scan --report      # walk the data volume, print the ledger (the default)
./storix scan --json        # the same scan as versioned JSON (schema 1)
./storix doctor             # what this build and this terminal can see
./storix cache list          # scans saved under ~/Library/Application Support/storix/scans
```

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

Run it with `sudo storix scan --system` to include root-only directories in the same ledger;
everything else behaves the same.

### `storix doctor`

Prints the environment a scan would run in: whether this build can turn off dataless
materialization and read purgeable space, which terminal you're running in, whether Full Disk
Access is granted, the volume and container space numbers, which mounts nest inside the scan
root, and local Time Machine snapshots. Run this first if a scan's numbers look off.

### `storix cache`

Every scan is stored under `~/Library/Application Support/storix/scans`. `storix cache list`
shows saved scans newest-first, `storix cache prune` keeps only the newest few, `storix cache
clear` deletes them all, and `storix cache path` prints the storage directory.

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
benchmark numbers in [`docs/09-bench-1a.md`](docs/09-bench-1a.md).
