#!/usr/bin/env bash
# Fails if any core package is below the coverage threshold.
# Usage: scripts/check_coverage.sh <go-test-output-file> [min-percent]
set -euo pipefail
file="${1:?usage: check_coverage.sh <go-test-output> [min]}"
min="${2:-70}"
core=(hub polls reactions chat events infra stats)

status=0
for pkg in "${core[@]}"; do
  line=$(grep -E "internal/${pkg}[[:space:]]" "$file" | grep -E "coverage: [0-9.]+%" | tail -1 || true)
  if [[ -z "$line" ]]; then
    echo "MISSING coverage for internal/${pkg}"
    status=1
    continue
  fi
  pct=$(sed -E 's/.*coverage: ([0-9.]+)%.*/\1/' <<<"$line")
  if awk -v p="$pct" -v m="$min" 'BEGIN { exit !(p+0 >= m+0) }'; then
    printf 'ok    internal/%-10s %6s%%  (min %s%%)\n' "$pkg" "$pct" "$min"
  else
    printf 'LOW   internal/%-10s %6s%%  (min %s%%)\n' "$pkg" "$pct" "$min"
    status=1
  fi
done
exit $status
