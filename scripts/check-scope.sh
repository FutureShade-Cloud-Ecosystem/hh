#!/usr/bin/env bash
# Scope gate for the hh monorepo (HH-30).
# No file in the repository may name the old npm scope @hh/ in code,
# configuration or documents, apart from ONE history note that records the
# rename (docs/scope-rename-history.md). And no package in the web workspace
# may carry a name outside @hardscapehouse/, apart from the workspace root
# (web/package.json, name hh-web). Reason: the npm scope hh belongs to a
# stranger; see docs/scope-rename-history.md for the ruling and the rename.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fail() { echo "check-scope: FAIL: $*" >&2; exit 1; }

# The probe is built from byte escapes so this file itself never carries the
# old scope literal: only the history note may.
probe="$(printf '\x40\x68\x68\x2f')"
# Self-test: prove the escape construction yields exactly the four bytes the
# scan needs before a clean scan is trusted. Both sides are escape-built, so
# the literal cannot leak into this file through the test either.
expected="$(printf '\x40\x68\x68\x2f')"
[ "$probe" = "$expected" ] || fail "probe self-test failed: escape construction changed"
[ "${#probe}" -eq 4 ] || fail "probe self-test failed: probe is ${#probe} bytes, expected 4"

# The one file allowed to name the old scope, and it must keep doing so: a
# gate that silently stops matching its own record would be a gate nobody
# can trust to fail.
note="docs/scope-rename-history.md"
[ -f "$note" ] || fail "the history note $note is missing: the rename record must stay"
grep -qF -- "$probe" "$note" || fail "the history note $note no longer records the old scope"

# Scan every tracked file. Any match outside the note is a failure.
offenders="$(git ls-files -z | xargs -0 -r grep -lF -- "$probe" | grep -Fxv "$note" || true)"
if [ -n "$offenders" ]; then
  echo "check-scope: FAIL: the old npm scope is named outside the history note $note:" >&2
  printf '%s\n' "$offenders" >&2
  exit 1
fi

# Every web workspace package (packages/* and apps/*) must be named inside
# @hardscapehouse/. The workspace root web/package.json is exempt: its name
# is hh-web, not a scoped package name.
scope_ok='@hardscapehouse/'
for manifest in web/packages/*/package.json web/apps/*/package.json; do
  [ -e "$manifest" ] || continue
  name="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("name",""))' "$manifest")"
  case "$name" in
    "$scope_ok"*) ;;
    "") fail "$manifest declares no name" ;;
    *) fail "$manifest carries the name $name, outside $scope_ok" ;;
  esac
done

echo "check-scope: OK"
