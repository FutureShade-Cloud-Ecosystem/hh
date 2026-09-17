#!/usr/bin/env bash
# App Shape skeleton check for the hh monorepo.
# Stand-in for `fb check` until the platform CLI exists (see docs/BLOCKERS.md).
# Validates the layout from context/FUTURESHADE-CONTEXT.md section 4.2, the
# manifest header, the ci job contract, and the no-dashes house rule.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# grep -P with Unicode code points needs a UTF-8 locale; under a non-UTF-8
# locale it errors out, and inside the if pipeline below that error would
# read as "no dashes found".
export LC_ALL=C.UTF-8

fail() { echo "check-shape: FAIL: $*" >&2; exit 1; }

required=(
  manifest.yaml
  context/FUTURESHADE-CONTEXT.md
  core/go.mod
  core/cmd/core/main.go
  core/internal
  core/db/migrations
  core/db/queries
  core/api
  core/api/openapi.yaml
  web/package.json
  web/packages/design-system
  web/packages/api-client
  web/packages/offline-store
  web/packages/auth
  web/packages/shell-chrome
  web/apps
  tauri/hh-work
  tauri/hh-field
  tauri/hh-pro
  statecharts
  agents
  docs/adr
  AGENTS.md
  CLAUDE.md
  .github/workflows/ci.yml
)
for path in "${required[@]}"; do
  [ -e "$path" ] || fail "missing required path: $path"
done

grep -q '^shape: 1$' manifest.yaml || fail "manifest.yaml must declare shape: 1"
grep -q '^app: hh$' manifest.yaml || fail "manifest.yaml must declare app: hh"

grep -q '@AGENTS.md' CLAUDE.md || fail "CLAUDE.md must import @AGENTS.md"

grep -Eq '^  ci:$' .github/workflows/ci.yml \
  || fail ".github/workflows/ci.yml must declare a job literally named ci"
grep -q 'id-token: write' .github/workflows/ci.yml \
  || fail "the ci workflow must declare permissions: id-token: write"

while IFS= read -r f; do
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$f" \
    || fail "invalid JSON: $f"
done < <(git ls-files '*.json')

# Self-test: prove the dash pattern matches a known dash string before a
# clean scan is trusted. The probe is built from byte escapes so this file
# itself stays free of dashes.
dash_probe="$(printf 'a\xe2\x80\x93b\xe2\x80\x94c')"
printf '%s\n' "$dash_probe" | grep -qP '[\x{2013}\x{2014}]' \
  || fail "dash self-test failed: pattern did not match a known dash string"

# The dash rule holds for everything this repository writes. The carried ERP
# baseline is excepted by range: D32 carries 0001 through 0212 byte-verbatim
# from a source written before the house rule existed, and editing it would
# break the verbatim-carry proof. Additive migrations from 0213 on, and every
# other file, stay under the rule.
carried_baseline='^core/db/migrations/(0[01][0-9]{2}|020[0-9]|021[0-2])_'

if git ls-files -z | xargs -0 -r grep -IlP '[\x{2013}\x{2014}]' \
  | grep -Ev "$carried_baseline" | grep .; then
  fail "em or en dash found in the files listed above"
fi

echo "check-shape: OK"
