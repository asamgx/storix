#!/usr/bin/env bash
set -euo pipefail

# scripts/accept-1b.sh — run a full scan and check the phase 1b acceptance
# numbers: the twelve buckets, how much of each is reclaimable, how large the
# unclassified "Other" bucket is, and whether the walked buckets still sum to
# the scanned bytes exactly.
#
# Read-only: it builds storix and runs one scan with --no-cache, so it touches
# nothing but its own output and the build products the Makefile already
# writes.
#
# Usage:
#   scripts/accept-1b.sh [--strict] [--other-max PERCENT] [--root PATH]
#                        [--binary PATH] [--no-build]
#
# Flags:
#   --strict             exit non-zero when a check fails. Without it the
#                        script prints the same verdicts and always exits 0,
#                        which is what you want while tuning rules.
#   --other-max PERCENT  how much of used space may be unclassified before
#                        the Other check fails (default 5, the phase 1b
#                        target; M8 ships with the catalog alone, so pass
#                        --other-max 15 until the detectors land).
#   --root PATH          scan root (default: the whole data volume).
#   --binary PATH        storix binary to run (default: ./storix).
#   --no-build           use the binary as it is instead of running `make build`.

strict=0
other_max=5
root=""
binary="./storix"
build=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    --strict) strict=1; shift ;;
    --other-max) other_max="$2"; shift 2 ;;
    --root) root="$2"; shift 2 ;;
    --binary) binary="$2"; shift 2 ;;
    --no-build) build=0; shift ;;
    -h|--help) sed -n '3,28p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "accept-1b: unknown flag $1" >&2; exit 2 ;;
  esac
done

cd "$(dirname "$0")/.."

if [[ $build -eq 1 ]]; then
  echo "building…"
  make build >/dev/null
fi

if [[ ! -x "$binary" ]]; then
  echo "accept-1b: no binary at $binary (run make build, or pass --binary)" >&2
  exit 2
fi

json="$(mktemp -t storix-accept-1b)"
txt="$(mktemp -t storix-accept-1b-txt)"
trap 'rm -f "$json" "$txt"' EXIT

args=(scan --no-cache)
[[ -n "$root" ]] && args+=("$root")

# Two scans: the JSON document carries the numbers the checks read, and the
# text report carries the unmatched listing, and the two output formats are
# mutually exclusive on one invocation.
echo "scanning for the numbers…"
"$binary" "${args[@]}" --json >"$json"
echo "scanning for the unmatched listing…"
"$binary" "${args[@]}" --report --debug >"$txt"

# Everything below reads the JSON document, which carries the same numbers the
# text report prints; parsing one document rather than scraping a table keeps
# the checks honest when the report's layout changes.
set +e
python3 - "$json" "$other_max" "$strict" <<'PY'
import json, sys

doc = json.load(open(sys.argv[1]))
other_max = float(sys.argv[2])
strict = sys.argv[3] == "1"

led = doc["ledger"]
buckets = led.get("buckets") or []
used = led["volume"]["used_after"]
scanned = led["scanned"]["bytes"]

def gb(n):
    return f"{n/1e9:8.2f} GB"

def pct(n, whole):
    return f"{100*n/whole:5.1f}%" if whole else "    -%"

print()
print(f"{'#':>2}  {'bucket':<24} {'bytes':>11} {'of used':>8} {'reclaimable':>13}")
for i, b in enumerate(buckets, 1):
    rec = b.get("reclaimable", 0)
    print(f"{i:>2}  {b['label']:<24} {gb(b['bytes'])} {pct(b['bytes'], used):>8} "
          f"{gb(rec) if rec else '':>13}")

walked = {"macos", "purgeable", "unaccounted"}
total_walked = sum(b["bytes"] for b in buckets if b["id"] not in walked)
other = next((b["bytes"] for b in buckets if b["id"] == "other"), 0)
appdata = next((b["bytes"] for b in buckets if b["id"] == "app-data"), 0)
developer = next((b["bytes"] for b in buckets if b["id"] == "developer"), 0)
reclaimable = sum(b.get("reclaimable", 0) for b in buckets)

print()
print(f"  used          {gb(used)}")
print(f"  scanned       {gb(scanned)}")
print(f"  walked sum    {gb(total_walked)}")
print(f"  reclaimable   {gb(reclaimable)} ({pct(reclaimable, used).strip()} of used)")
print(f"  app data      {gb(appdata)}")
print(f"  developer     {gb(developer)}")
print(f"  other         {gb(other)} ({pct(other, used).strip()} of used)")
print()

failures = []
if len(buckets) != 12:
    failures.append(f"the ledger has {len(buckets)} buckets, not 12")
if total_walked != scanned:
    failures.append(f"the walked buckets sum to {total_walked}, scanned is {scanned} "
                    f"(off by {total_walked - scanned} bytes)")
else:
    print("  ok   the walked buckets sum to the scanned bytes exactly")

share = 100 * other / used if used else 0
if share >= other_max:
    failures.append(f"Other is {share:.1f}% of used space, over the {other_max:g}% limit")
else:
    print(f"  ok   Other is {share:.1f}% of used space, under the {other_max:g}% limit")

for f in failures:
    print(f"  FAIL {f}")

sys.exit(1 if failures and strict else 0)
PY
verdict=$?
set -e

echo
echo "largest directories no rule reached:"
sed -n '/^UNMATCHED/,/^$/p' "$txt" | tail -n +2

echo
sed -n '/^DEBUG/,$p' "$txt" | sed -n '1,4p'

exit $verdict
