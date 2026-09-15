#!/usr/bin/env bash
# App Shape skeleton check for the hh monorepo.
# Stand-in for `fb check` until the platform CLI exists (see docs/BLOCKERS.md).
# Validates the layout from context/FUTURESHADE-CONTEXT.md section 4.2, the
# manifest header, the ci job contract, and the no-dashes house rule.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

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

if git ls-files -z | xargs -0 -r grep -IlP '[\x{2013}\x{2014}]' | grep .; then
  fail "em or en dash found in the files listed above"
fi

echo "check-shape: OK"
