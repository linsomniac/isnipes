#!/usr/bin/env bash
# PHASE8.md §14 — CI frozen path-diff gate. Fails if any frozen path changed
# between the base ref and HEAD. This is the mechanical backstop the sha
# manifest alone cannot provide: the manifest can be re-seeded, but a diff
# over the guard script + manifest THEMSELVES cannot be silently bypassed.
#
# Usage: scripts/check-frozen-paths.sh [base-ref]   (default origin/main)
#
# CI skips this step when the PR carries the 'frozen-change-approved' label
# (see .github/workflows/ci.yml), so an intentional, reviewed wire/sim change
# is still possible.
set -euo pipefail
cd "$(dirname "$0")/.."

BASE="${1:-origin/main}"

PATTERN='^(internal/sim/|internal/proto/|web/src/(proto|sim|prediction|interp|netClient)\.ts$|scripts/check-frozen\.sh$|scripts/frozen\.sha256$)'

changed="$(git diff --name-only "$BASE"...HEAD | grep -E "$PATTERN" || true)"
if [[ -n "$changed" ]]; then
  echo "FROZEN PATH GATE FAILED: frozen files changed vs $BASE:" >&2
  echo "$changed" | sed 's/^/  /' >&2
  echo "" >&2
  echo "These lock the wire schema / deterministic sim. If this change is" >&2
  echo "intentional and reviewed, add the 'frozen-change-approved' label." >&2
  exit 1
fi
echo "frozen path gate OK (no frozen path changed vs $BASE)"
