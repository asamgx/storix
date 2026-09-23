#!/usr/bin/env bash
set -euo pipefail

# scripts/accept-1b.sh — run one scan and check the phase 1b acceptance
# numbers: the twelve buckets, how much of each is reclaimable, how large the
# unclassified "Other" bucket is, whether the walked buckets still sum to the
# scanned bytes exactly, what happened to every detector, what the
# classifier could not place, and the conflicts it logged.
#
# Read-only: it builds storix and runs one scan with --no-cache, so it
# touches nothing but its own output and the build products the Makefile
# already writes.
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
#
# --strict fails the run when: Other is at or over --other-max percent of
# used space; the twelve walked buckets do not sum to the scanned bytes; any
# detector's state is panic; or classification.conflicts.by_kind has a rule
# beating a detector or the application inventory, which the precedence rule
# in docs/03 says can never happen and so is a bug rather than tuning.
#
# Everything below is read from one `storix scan --json --no-cache`
# document: the bucket totals, the detector table, the conflict tally, the
# six largest projects and the unmatched listing all come from the
# classification section of that document; nothing here scrapes the text
# report.

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
    -h|--help) sed -n '3,38p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
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
trap 'rm -f "$json"' EXIT

args=(scan --no-cache --json)
[[ -n "$root" ]] && args+=("$root")

echo "scanning…"
"$binary" "${args[@]}" >"$json"

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
cls = doc.get("classification") or {}

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

# Detector table: name, state, duration, reason, unverified marker (*).
print()
print(f"{'detector':<14} {'state':<10} {'duration':>10}  reason")
dets = cls.get("detectors") or []
panics = [d for d in dets if d["state"] == "panic"]
for d in sorted(dets, key=lambda d: d["name"]):
    dur_ms = d.get("duration_ns", 0) / 1e6
    dur = f"{dur_ms:7.1f}ms" if dur_ms else ""
    mark = "" if d.get("verified", True) else " *"
    print(f"  {d['name']:<12} {d['state']+mark:<10} {dur:>10}  {d.get('reason', '')}")
if not dets:
    print("  (classification.detectors is not in this build's JSON)")

if panics:
    failures.append(f"{len(panics)} detector(s) are in the panic state")
else:
    print("  ok   no detector panicked")

# Conflict counts by kind.
print()
conf = cls.get("conflicts")
if conf is None:
    print("  conflicts: not in JSON yet (M12)")
else:
    print(f"  conflicts: {conf.get('total', 0)} total")
    order = ["rule-over-rule", "detector-over-rule", "apps-over-rule",
              "detector-over-apps", "detector-over-detector"]
    by_kind = {k["kind"]: k["count"] for k in conf.get("by_kind", [])}
    for kind in order:
        if by_kind.get(kind):
            print(f"    {kind:<22} {by_kind[kind]}")
    unexpected = {k: v for k, v in by_kind.items() if k not in order and v}
    for kind, n in sorted(unexpected.items()):
        print(f"    {kind:<22} {n}  (unexpected)")
    rule_wins = by_kind.get("rule-over-detector", 0) + by_kind.get("rule-over-apps", 0)
    if rule_wins:
        failures.append(f"{rule_wins} conflict(s) have a rule beating a detector or the apps inventory")

# The six largest projects, across every detector that reports any (in
# practice, only the projects detector does).
print()
print("six largest projects under the code roots:")
projects = []
for entry in cls.get("developer") or []:
    projects.extend(entry.get("projects") or [])
projects.sort(key=lambda p: p.get("artifact_bytes", 0), reverse=True)
for p in projects[:6]:
    print(f"  {gb(p.get('artifact_bytes', 0))}  {p['root']}")
if not projects:
    print("  (none found under the configured code roots)")

# The ten largest directories no rule or detector reached.
print()
print("top 10 unmatched paths (largest directories no rule reached):")
unmatched = cls.get("unmatched") or []
for u in unmatched[:10]:
    print(f"  {gb(u.get('bytes', 0))}  {u['path']}")
if not unmatched:
    print("  none")

print()
t = doc.get("timing") or {}
def ms(key):
    return f"{t.get(key, 0) / 1e6:.0f}ms"
print(f"  timing   facts {ms('facts_ns')}, walk {ms('walk_ns')}, classify {ms('classify_ns')}, "
      f"ledger {ms('ledger_ns')}, total {ms('total_ns')}")

for f in failures:
    print(f"  FAIL {f}")

sys.exit(1 if failures and strict else 0)
PY
verdict=$?
set -e

exit $verdict
