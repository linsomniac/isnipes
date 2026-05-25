#!/usr/bin/env bash
# PHASE7.md DoD #2 — frozen-file guard. Phase 7 must not edit the files
# that lock the wire schema / deterministic sim:
#   - internal/sim/**            (determinism fingerprints)
#   - internal/proto/*.go        (wire schema: checksum, frame, messages, …;
#                                 _test.go excluded)
#   - web/src/{proto,sim,prediction,interp,netClient}.ts (client mirrors)
#
# The committed manifest scripts/frozen.sha256 records their sha256. This
# script recomputes and fails on any drift. Regenerate intentionally with:
#   scripts/check-frozen.sh --write
set -euo pipefail
cd "$(dirname "$0")/.."

MANIFEST="scripts/frozen.sha256"

frozen_files() {
  {
    # All wire-schema source in internal/proto (not just checksum.go) so the
    # "no wire change" invariant is mechanically backed (PHASE8 §1, DoD #2).
    # _test.go files are excluded: tests are not the wire contract.
    find internal/proto -name '*.go' ! -name '*_test.go'
    echo web/src/proto.ts
    echo web/src/sim.ts
    echo web/src/prediction.ts
    echo web/src/interp.ts
    echo web/src/netClient.ts
    find internal/sim -name '*.go'
  } | sort -u
}

compute() {
  # shellcheck disable=SC2046
  sha256sum $(frozen_files) | sort
}

if [[ "${1:-}" == "--write" ]]; then
  compute > "$MANIFEST"
  echo "wrote $MANIFEST ($(wc -l < "$MANIFEST") files)"
  exit 0
fi

if [[ ! -f "$MANIFEST" ]]; then
  echo "FROZEN GUARD: missing $MANIFEST (run with --write to seed)" >&2
  exit 1
fi

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
compute > "$TMP"

if ! diff -u "$MANIFEST" "$TMP"; then
  echo "" >&2
  echo "FROZEN GUARD FAILED: a frozen file changed. Phase 7 must not edit the" >&2
  echo "wire-schema / deterministic-sim files above. If this change is" >&2
  echo "intentional and reviewed, re-seed with: scripts/check-frozen.sh --write" >&2
  exit 1
fi
echo "frozen guard OK ($(wc -l < "$MANIFEST") files)"
