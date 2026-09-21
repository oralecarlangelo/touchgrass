#!/usr/bin/env bash
# Gate govulncheck on actionable findings (exit 0/3 like govulncheck itself).
#
# KNOWN lists accepted-risk IDs: daemon-side Moby issues with no available
# fix ("Fixed in: N/A"), flagged only because touchgrass imports the Docker
# *client* package from the same mega-module. touchgrass neither ships nor
# operates dockerd, so these are not exploitable via our call path; any
# finding NOT in this list fails the gate.
set -uo pipefail

KNOWN="GO-2026-4887 GO-2026-4883"

OUT="$(govulncheck "$@" 2>&1)"
STATUS=$?
echo "$OUT"

if echo "$OUT" | grep -q "No vulnerabilities found"; then
  exit 0
fi

NEW="$(echo "$OUT" | grep -Eo "GO-[0-9]{4}-[0-9]+" | sort -u)"

for id in $NEW; do
  case " $KNOWN " in
    *" $id "*) ;;
    *)
      echo "gate: new actionable finding $id" >&2
      exit 3
      ;;
  esac
done

if [ "$STATUS" -eq 0 ]; then
  exit 0
fi

if [ -n "$NEW" ]; then
  echo "gate: only known accepted-risk findings ($KNOWN)" >&2
  exit 0
fi

exit "$STATUS"
