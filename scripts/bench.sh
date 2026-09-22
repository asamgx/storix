#!/usr/bin/env bash
set -euo pipefail

# scripts/bench.sh — measure storix's parallel walker against `du -sk` on the
# same root, at a sweep of --parallelism values, and (optionally) append the
# results table into docs/09-bench-1a.md.
#
# Read-only: touches nothing but its own stdout and, with --write,
# docs/09-bench-1a.md. `--cold` runs `sudo purge` before each measured run,
# which evicts the system-wide page/UBC cache — it changes cache state, not
# any file, and is opt-in because it needs sudo and adds minutes per run.
#
# Usage:
#   scripts/bench.sh [--root PATH] [--binary PATH] [--parallelism "8 16 32 64"]
#                     [--runs N] [--cold] [--write]
#
# Flags:
#   --root PATH          scan root for both du and storix (default: the data
#                         volume, /System/Volumes/Data)
#   --binary PATH         storix binary to benchmark (default: ./storix; run
#                         `make build` first — this script never builds it)
#   --parallelism "..."   space-separated worker counts to sweep (default:
#                         "8 16 32 64")
#   --runs N              measurements per data point (default: 3)
#   --cold                run `sudo purge` before every du and storix run;
#                         default is warm (no purge), matching the 0.35×
#                         acceptance target in docs/06-roadmap.md
#   --write               append the results table to docs/09-bench-1a.md
#                         instead of only printing it to stdout
#
# Notes:
#   - `storix scan` has no `--no-cache` flag yet: phase 1a's CLI always walks
#     fresh (only the TUI's cache load path reads a stored scan), so this
#     script does not pass one. If a future `storix scan` grows a cache
#     bypass flag, add it to the invocation below rather than assuming its
#     absence stays true.
#   - Wall time is read from `/usr/bin/time -p`'s "real" line, which matches
#     the report's own "elapsed" line to within a few milliseconds.

ROOT="/System/Volumes/Data"
BINARY="./storix"
PARALLELISM="8 16 32 64"
RUNS=3
COLD=0
WRITE=0
DOCS_OUT="docs/09-bench-1a.md"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) ROOT="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --parallelism) PARALLELISM="$2"; shift 2 ;;
    --runs) RUNS="$2"; shift 2 ;;
    --cold) COLD=1; shift ;;
    --write) WRITE=1; shift ;;
    -h|--help)
      sed -n '4,39p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "bench.sh: unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ ! -x "$BINARY" ]]; then
  echo "bench.sh: $BINARY not found or not executable; run 'make build' first" >&2
  exit 1
fi

maybe_purge() {
  if [[ "$COLD" -eq 1 ]]; then
    echo "  (cold: sudo purge)" >&2
    sudo purge
  fi
}

# time_cmd runs "$@" with stdout discarded and prints the wall-clock seconds
# from /usr/bin/time -p's "real" line. The wrapped command's own exit status is
# not treated as a script failure: an unprivileged `du -sk` over the whole
# data volume always exits non-zero (permission-denied subdirectories are
# expected, not a bench.sh bug), and this script only cares about the timing.
time_cmd() {
  local t
  set +e
  t=$( { /usr/bin/time -p "$@" >/dev/null; } 2>&1 | awk '/^real/{print $2}' )
  set -e
  if [[ -z "$t" ]]; then
    echo "bench.sh: could not read a wall time from: $*" >&2
    return 1
  fi
  printf '%s' "$t"
}

mean() {
  awk '{s+=$1; n++} END{if (n>0) printf "%.2f", s/n; else print "0"}'
}

echo "== du -sk $ROOT  (warm, x$RUNS) =="
du_times=()
for i in $(seq 1 "$RUNS"); do
  maybe_purge
  t=$(time_cmd du -sk "$ROOT")
  echo "  run $i: ${t}s"
  du_times+=("$t")
done
du_mean=$(printf '%s\n' "${du_times[@]}" | mean)
echo "du mean: ${du_mean}s"
echo

echo "== $BINARY scan --report --parallelism P  (warm, x$RUNS each) =="
table_rows=()
for p in $PARALLELISM; do
  p_times=()
  for i in $(seq 1 "$RUNS"); do
    maybe_purge
    t=$(time_cmd "$BINARY" scan --report --parallelism "$p" --roots "$ROOT")
    echo "  P=$p run $i: ${t}s"
    p_times+=("$t")
  done
  p_mean=$(printf '%s\n' "${p_times[@]}" | mean)
  ratio=$(awk -v a="$p_mean" -v b="$du_mean" 'BEGIN{ if (b>0) printf "%.3f", a/b; else print "n/a" }')
  table_rows+=("| $p | ${p_mean}s | ${ratio}× |")
  echo "  P=$p mean: ${p_mean}s  ratio ${ratio}x"
done
echo

echo "| parallelism | mean seconds | ratio to du |"
echo "|---|---|---|"
printf '%s\n' "${table_rows[@]}"

if [[ "$WRITE" -eq 1 ]]; then
  {
    echo
    echo "<!-- scripts/bench.sh run $(date -u +%Y-%m-%dT%H:%M:%SZ), root=$ROOT, runs=$RUNS, cold=$COLD -->"
    echo "du -sk $ROOT mean: ${du_mean}s over $RUNS runs"
    echo
    echo "| parallelism | mean seconds | ratio to du |"
    echo "|---|---|---|"
    printf '%s\n' "${table_rows[@]}"
  } >> "$DOCS_OUT"
  echo "appended table to $DOCS_OUT"
fi
