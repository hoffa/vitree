#!/bin/sh
set -eu

go test -v -coverprofile=cover.out .
pct=$(go tool cover -func=cover.out | awk '/^total:/ {print $3}' | tr -d %)
echo "coverage: ${pct}%"
awk -v p="$pct" 'BEGIN{exit (p+0<95)}' || {
  echo "coverage ${pct}% < 95%"
  exit 1
}
