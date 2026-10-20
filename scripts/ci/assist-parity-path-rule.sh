#!/usr/bin/env bash
# The assist parity path rule (TanyAI plan r3 section 1, review r1 P2-4).
#
#   assist-parity-path-rule.sh --match            read changed file paths on stdin, print those that are
#                                                 visibility files (core/internal/assist/parity/visibility_files.txt)
#   assist-parity-path-rule.sh <base-sha> <dir>   in the integration job: report whether the change touches
#                                                 a visibility file, then run the parity suite verbosely in
#                                                 <dir> (the core module) and FAIL unless every parity test
#                                                 passed and none was skipped.
#
# The suite runs on every integration job either way; what the rule adds is that a PR touching a visibility
# function cannot get through with the suite red or skipped, and that a skipped suite is a failure and not
# a polite pass (the legs skip when DATABASE_URL is not set).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
list="$here/../../core/internal/assist/parity/visibility_files.txt"

match() {
  local path pat
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    while IFS= read -r pat; do
      case "$pat" in ''|'#'*) continue ;; esac
      case "$pat" in
        */) case "$path" in "$pat"*) echo "$path"; break ;; esac ;;
        *) if [ "$path" = "$pat" ]; then echo "$path"; break; fi ;;
      esac
    done < "$list"
  done
}

if [ "${1:-}" = "--match" ]; then
  match
  exit 0
fi

base="${1:-}"
dir="${2:?core module directory}"
touched=""
if [ -n "$base" ] && git cat-file -e "$base^{commit}" 2>/dev/null; then
  touched="$(git diff --name-only "$base"...HEAD | match || true)"
else
  echo "assist parity path rule: base $base is not available, treating the change as touching a visibility file"
  touched="(unknown)"
fi
if [ -n "$touched" ]; then
  echo "assist parity path rule: this change touches visibility files:"
  echo "$touched" | sed 's/^/  /'
else
  echo "assist parity path rule: no visibility file touched; the suite still must not be skipped"
fi

out="$(mktemp)"
trap 'rm -f "$out"' EXIT
( cd "$dir" && go test -tags integration ./internal/assist/parity -count=1 -v -timeout 10m ) 2>&1 | tee "$out"
if grep -q -e '--- SKIP' "$out"; then
  echo "assist parity path rule: a parity test was skipped" >&2
  exit 1
fi
if grep -q -e '--- FAIL' "$out" || ! grep -q -e '^ok' "$out"; then
  echo "assist parity path rule: the parity suite is red" >&2
  exit 1
fi
for name in TestParity_EveryOrderAndLineIsInTheViewIffTheOrderModulesRecordReadReturnsIt TestParity_ATierThreeCallerIsRefusedByTheTierFloor; do
  if ! grep -q -e "--- PASS: $name " "$out"; then
    echo "assist parity path rule: $name did not pass" >&2
    exit 1
  fi
done
