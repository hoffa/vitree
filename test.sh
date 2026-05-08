#!/usr/bin/env bash
set -euo pipefail

MIN_COVERAGE="${MIN_COVERAGE:-70}"

go test -coverprofile=cover.out .
pct=$(go tool cover -func=cover.out | awk '/^total:/ {print $3}' | tr -d %)
echo "coverage: ${pct}%"
awk -v p="$pct" -v min="$MIN_COVERAGE" 'BEGIN{exit (p+0<min)}' || {
  echo "coverage ${pct}% < ${MIN_COVERAGE}%"
  exit 1
}
