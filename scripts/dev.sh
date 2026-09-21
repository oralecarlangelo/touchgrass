#!/usr/bin/env bash
# make dev: run the API (dev proxy mode) and Vite side by side.
# The API serves /api/* and proxies everything else to Vite for HMR.
set -euo pipefail

command -v npm >/dev/null 2>&1 || { echo "dev.sh: npm is required" >&2; exit 1; }
[ -d web/node_modules ] || npm install --prefix web

APP_ENV=dev go run ./cmd/touchgrass serve &
API_PID=$!
(cd web && npm run dev) &
WEB_PID=$!

trap 'kill $API_PID $WEB_PID 2>/dev/null; wait 2>/dev/null' INT TERM
wait
