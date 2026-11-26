#!/usr/bin/env bash
# Enforces a minimum statement coverage for EVERY Go source file (not just the
# project total). Usage: scripts/coverage-check.sh [profile] [threshold]
set -euo pipefail

profile="${1:-coverage.out}"
threshold="${2:-90}"

if [[ ! -f "$profile" ]]; then
  echo "coverage profile '$profile' not found; run 'make cover' first" >&2
  exit 2
fi

awk -v threshold="$threshold" '
  NR == 1 { next } # "mode: ..." header
  {
    # Line format: path/file.go:startLine.col,endLine.col numStatements hitCount
    block = $1; stmts = $2; hits = $3
    file = substr(block, 1, index(block, ":") - 1)
    if (!(block in seen)) { seen[block] = 1; total[file] += stmts; all += stmts }
    if (hits > 0 && !(block in hit)) { hit[block] = 1; covered[file] += stmts; allCovered += stmts }
  }
  END {
    failed = 0
    for (f in total) {
      pct = total[f] ? covered[f] * 100 / total[f] : 100
      status = pct + 1e-9 >= threshold ? "ok" : "FAIL"
      if (status == "FAIL") failed++
      printf "%-4s %6.1f%%  %s\n", status, pct, f | "sort -k3"
    }
    close("sort -k3")
    printf "\nTotal: %.1f%% (%d files below %s%%)\n", allCovered * 100 / all, failed, threshold
    exit failed > 0
  }
' "$profile"
