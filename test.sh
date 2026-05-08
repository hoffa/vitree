#!/bin/sh
set -eu

go test -coverprofile=cover.out .
pct=$(go tool cover -func=cover.out | awk '/^total:/ {print $3}' | tr -d %)
echo "coverage: ${pct}%"
awk -v p="$pct" 'BEGIN{exit (p+0<80)}' || {
  echo "coverage ${pct}% < 80%"
  exit 1
}
