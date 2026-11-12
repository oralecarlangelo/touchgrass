#!/usr/bin/env bash
# Print the Go packages a change affects, one import path per line: every package
# with a changed file, plus every package that depends on one of them.
#
#   scripts/affected-packages.sh [base-ref]      # default: origin/develop
#
# The change is `git diff <merge-base of base and HEAD>..working tree`, so it covers
# committed, staged and unstaged edits alike. Output:
#   - nothing        : no Go package is affected (docs-only or web-only change)
#   - "./..." style list of import paths: run `go test` on exactly these
#   - the single word ALL: something every package depends on changed (go.mod,
#     go.sum, the toolchain, the Makefile, CI, or this script); run the full suite
#
# A package is "changed" when any file in its directory changed, not only .go files:
# packages embed SQL, fixtures, golden files and built assets. Reverse dependencies
# come from `go list -deps`, so a change in internal/store also selects
# internal/service, internal/http and cmd/touchgrass.
set -euo pipefail

base="${1:-origin/develop}"
module="$(go list -m)"
root="$(git rev-parse --show-toplevel)"
cd "$root"

merge_base="$(git merge-base "$base" HEAD 2>/dev/null || true)"
[ -n "$merge_base" ] || { echo "ALL"; exit 0; }

changed="$( { git diff --name-only "$merge_base"; git ls-files --others --exclude-standard; } | sort -u)"
[ -n "$changed" ] || exit 0

# Anything here can change the result of every package.
if printf '%s\n' "$changed" | grep -Eq '^(go\.mod|go\.sum|Makefile|\.golangci\.yml|\.github/workflows/|scripts/affected-packages\.sh$)'; then
  echo "ALL"
  exit 0
fi

# Directories that hold a Go package (or files one embeds), as import paths.
# -e: a clean checkout has no built web/docs bundles yet, and go list refuses a
# package whose //go:embed pattern matches nothing. The selection only needs import
# paths and dependency edges, which -e still reports.
all_packages="$(go list -e ./cmd/... ./internal/... ./migrations/... ./web ./docs-site 2>/dev/null)"

changed_packages=""
while IFS= read -r file; do
  dir="$(dirname "$file")"
  # Walk up until a directory that is a Go package: a fixture under
  # internal/http/testdata belongs to internal/http, web/src to ./web.
  while [ "$dir" != "." ] && [ "$dir" != "/" ]; do
    candidate="$module/$dir"
    if printf '%s\n' "$all_packages" | grep -Fxq "$candidate"; then
      changed_packages="$changed_packages$candidate"$'\n'
      break
    fi
    dir="$(dirname "$dir")"
  done
done <<< "$changed"

changed_packages="$(printf '%s' "$changed_packages" | sort -u)"
[ -n "$changed_packages" ] || exit 0

# Every package whose dependency closure (itself included, test imports included)
# meets a changed package.
# shellcheck disable=SC2086 # one import path per line, split on purpose
go list -e -test -deps -f '{{.ImportPath}} {{range .Deps}}{{.}} {{end}}' $all_packages 2>/dev/null \
  | awk -v changed="$changed_packages" -v module="$module" '
      BEGIN { n = split(changed, list, "\n"); for (i = 1; i <= n; i++) if (list[i] != "") want[list[i]] = 1 }
      {
        pkg = $1
        sub(/ \[.*$/, "", pkg); sub(/\.test$/, "", pkg); sub(/_test$/, "", pkg)
        if (index(pkg, module) != 1) next
        hit = (pkg in want)
        for (i = 2; i <= NF && !hit; i++) { dep = $i; if (dep in want) hit = 1 }
        if (hit) affected[pkg] = 1
      }
      END { for (pkg in affected) print pkg }' \
  | grep -Fx -f <(printf '%s\n' "$all_packages") | sort -u
