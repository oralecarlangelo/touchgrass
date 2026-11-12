#!/usr/bin/env bash
# Run `go test` on the packages a change affects (scripts/affected-packages.sh).
#
#   scripts/test-affected.sh [base-ref] [extra go test flags...]
set -euo pipefail

base="${1:-origin/develop}"
[ "$#" -gt 0 ] && shift
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
packages="$("$here/affected-packages.sh" "$base")"

if [ -z "$packages" ]; then
  echo "no Go package is affected by this change; nothing to test"
  exit 0
fi

if [ "$packages" = "ALL" ]; then
  echo "a file every package depends on changed; running the full suite"
  exec go test "$@" ./cmd/... ./internal/... ./migrations/... ./web ./docs-site
fi

count="$(printf '%s\n' "$packages" | wc -l | tr -d ' ')"
echo "testing $count affected package(s)"
# shellcheck disable=SC2086 # one import path per line, split on purpose
exec go test "$@" $packages
