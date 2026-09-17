#!/usr/bin/env bash
# Local simulation of the ci schema freeze step, per HH-3.1's lane brief.
# Each case builds a throwaway git repository and runs the same logic the
# CI step runs. SCHEMA_FREEZE_PR_BASE stands in for the pull request base
# sha the workflow takes from the event; unset means no pull request
# context, so the ledger base falls back to the freeze commit.
set -uo pipefail

freeze_check() {
  set -euo pipefail
  marker=core/db/SCHEMA-FROZEN
  if [ ! -f "$marker" ]; then
    echo "Schema freeze marker $marker is absent: the freeze is not in effect, passing."
    exit 0
  fi
  freeze_commit="$(tr -d '[:space:]' < "$marker")"
  if [ -z "$freeze_commit" ]; then
    echo "Schema freeze marker $marker is present but empty; it must hold the freeze commit id." >&2
    exit 1
  fi
  if ! git cat-file -e "$freeze_commit^{commit}" 2>/dev/null; then
    echo "Schema freeze marker names commit $freeze_commit, which is not in this checkout. The job fetches with fetch-depth: 0, so the marker content is wrong." >&2
    exit 1
  fi
  changed="$(git diff --name-only "$freeze_commit"..HEAD -- core/db/migrations/)"
  if [ -z "$changed" ]; then
    echo "No changes under core/db/migrations/ since freeze commit $freeze_commit: passing."
    exit 0
  fi
  ledger_base="$freeze_commit"
  if [ -n "${SCHEMA_FREEZE_PR_BASE:-}" ] && git cat-file -e "${SCHEMA_FREEZE_PR_BASE}^{commit}" 2>/dev/null; then
    ledger_base="$(git merge-base "${SCHEMA_FREEZE_PR_BASE}" HEAD)"
  fi
  ledger_changed="$(git diff --name-only "$ledger_base"..HEAD -- core/db/PORTED.md)"
  if [ -n "$ledger_changed" ]; then
    entry="$(git diff "$ledger_base"..HEAD -- core/db/PORTED.md | grep '^+' | grep -v '^+++' | sed 's/^+//' || true)"
    echo "Changes under core/db/migrations/ since freeze commit $freeze_commit are permitted by the core/db/PORTED.md ledger entry carried in the same change:"
    echo "$entry"
    exit 0
  fi
  echo "Schema freeze violated: files changed under core/db/migrations/ after freeze commit $freeze_commit:" >&2
  echo "$changed" >&2
  echo "Only an HH-3.7 port may change core/db/migrations/ after the freeze, and only in a pull request also carrying a core/db/PORTED.md ledger entry." >&2
  exit 1
}

scratch=""
fresh_repo() {
  scratch="$(mktemp -d)"
  cd "$scratch"
  git init -q
  git config user.email freeze-test@example.invalid
  git config user.name freeze-test
  mkdir -p core/db/migrations
  echo "select 1;" > core/db/migrations/0213_init.sql
  git add .
  git commit -qm init
}

failures=0
run_case() {
  local name="$1" want_code="$2"
  shift 2
  local out code p
  out="$(freeze_check 2>&1)"; code=$?
  if [ "$code" -ne "$want_code" ]; then
    echo "FAIL: $name (exit $code, wanted $want_code)"; echo "$out"; failures=1; return
  fi
  for p in "$@"; do
    if ! grep -qF -- "$p" <<<"$out"; then
      echo "FAIL: $name (output missing: $p)"; echo "$out"; failures=1; return
    fi
  done
  echo "PASS: $name"
  rm -rf "$scratch"
}

# Case 1: marker absent, pass with a clear message.
fresh_repo
run_case "marker absent" 0 "is absent: the freeze is not in effect, passing."

# Case 2: marker present, no migration changes since the freeze commit.
fresh_repo
base="$(git rev-parse HEAD)"
printf '%s\n' "$base" > core/db/SCHEMA-FROZEN
git add . && git commit -qm marker
echo "notes" > core/db/notes.txt
git add . && git commit -qm notes
run_case "frozen, no migration changes" 0 "No changes under core/db/migrations/ since freeze commit $base: passing."

# Case 3: migration change without PORTED.md, fail naming the file and the marker commit.
fresh_repo
base="$(git rev-parse HEAD)"
printf '%s\n' "$base" > core/db/SCHEMA-FROZEN
git add . && git commit -qm marker
echo "select 2;" > core/db/migrations/0214_bad.sql
git add . && git commit -qm bad
run_case "migration change, no ledger" 1 "core/db/migrations/0214_bad.sql" "after freeze commit $base"

# Case 4: migration change with a PORTED.md entry in the same change, pass naming the entry.
fresh_repo
base="$(git rev-parse HEAD)"
printf '%s\n' "$base" > core/db/SCHEMA-FROZEN
git add . && git commit -qm marker
echo "select 2;" > core/db/migrations/0214_ported.sql
printf '## ported 0214 from ERP master\n' > core/db/PORTED.md
git add . && git commit -qm port
run_case "migration change with ledger in the same change" 0 "permitted by the core/db/PORTED.md ledger entry" "ported 0214 from ERP master"

# Case 5: a PORTED.md edit made BEFORE the freeze must not open the hatch
# for a later migration change. The pull request branches from main after
# both the note and the freeze, so the note is outside the pull request's
# own window even though it is inside the freeze..HEAD range.
fresh_repo
printf '## ledger note from before the freeze\n' > core/db/PORTED.md
git add . && git commit -qm ledger-note
base="$(git rev-parse HEAD)"
printf '%s\n' "$base" > core/db/SCHEMA-FROZEN
git add . && git commit -qm marker
pr_base="$(git rev-parse HEAD)"
echo "select 3;" > core/db/migrations/0215_sneaky.sql
git add . && git commit -qm sneaky
SCHEMA_FREEZE_PR_BASE="$pr_base" \
  run_case "old ledger note does not open the hatch" 1 "Schema freeze violated" "0215_sneaky.sql"

# Case 6: same shape as case 5, but the pull request itself carries the
# ledger entry, so the hatch opens for it and only it.
fresh_repo
base="$(git rev-parse HEAD)"
printf '%s\n' "$base" > core/db/SCHEMA-FROZEN
git add . && git commit -qm marker
pr_base="$(git rev-parse HEAD)"
echo "select 4;" > core/db/migrations/0216_port.sql
printf '## ported 0216 from ERP master\n' > core/db/PORTED.md
git add . && git commit -qm port
SCHEMA_FREEZE_PR_BASE="$pr_base" \
  run_case "ledger entry in the pull request opens the hatch" 0 "permitted by the core/db/PORTED.md ledger entry" "ported 0216 from ERP master"

if [ "$failures" -ne 0 ]; then
  echo "freeze check self-test FAILED" >&2
  exit 1
fi
echo "freeze check self-test: all six cases pass"
