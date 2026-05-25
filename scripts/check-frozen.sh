#!/usr/bin/env bash
# PHASE7/PHASE8 DoD #2 — frozen-file guard. Later phases must not edit the
# files that lock the wire schema / deterministic sim:
#   - internal/sim/**            (determinism fingerprints + replay/maze
#                                 fixtures — ALL regular files, not just *.go)
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

WEB_MIRRORS=(
  web/src/proto.ts
  web/src/sim.ts
  web/src/prediction.ts
  web/src/interp.ts
  web/src/netClient.ts
)

# Fail loudly if a required path is missing rather than silently emitting a
# short manifest (a `find` error otherwise does not propagate through the
# old `sha256sum $(...)` command substitution).
require_paths() {
  local p
  for p in internal/proto internal/sim "${WEB_MIRRORS[@]}"; do
    [[ -e "$p" ]] || { echo "FROZEN GUARD: required path missing: $p" >&2; exit 1; }
  done
}

# NUL-delimited so filenames with spaces/globs are safe; fail-fast on any
# enumeration error.
frozen_files() {
  {
    # All wire-schema source in internal/proto (not just checksum.go) so the
    # "no wire change" invariant is mechanically backed (PHASE8 §1, DoD #2).
    # _test.go files are excluded: tests are not the wire contract.
    find internal/proto -type f -name '*.go' ! -name '*_test.go' -print0
    printf '%s\0' "${WEB_MIRRORS[@]}"
    # ALL regular files under internal/sim, incl. testdata/ replay + maze
    # fixtures that the determinism tests read (codex: *.go alone left them
    # unfrozen).
    find internal/sim -type f -print0
  } | sort -z -u
}

compute() {
  frozen_files | xargs -0 sha256sum | sort
}

require_paths

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
