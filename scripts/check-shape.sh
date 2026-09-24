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

# The HH-3.7 ports are excepted by name, on the same ground as the baseline.
# Seven of the eight are byte-verbatim ERP carries under the MID ruling
# (Plane #25, D39) from the same pre-rule source, and scrubbing their comment
# text would break the byte-identity the ruling and the port ledger demand
# (0219's one permitted adaptation is its bookkeeping lookup pattern, which is
# not prose). The eighth, 0220, is a core-authored rewrite whose only dashes
# sit inside its two WHERE literals, copied byte-identical from core's own
# carried baseline at 0032_gl_module.sql and 0035_nsf_credit_hold.sql, which
# the carried_baseline range already excepts; rewriting them would break the
# ruling's condition 2. The same byte-identity ground excepts the eight files
# from the date house rule: their carried comment prose contains calendar
# references from the pre-rule source, and scrubbing them would break the
# byte-identity the ruling and the ledger demand. This script scans dashes
# only, so the date ground is recorded here rather than configured. The
# exception is a closed list of exactly the eight ported files, so every later
# migration and every other file stays under both rules.
ported_carry='^core/db/migrations/(0216_a_saved_sort_is_curation_too|0217_purchasing_delivery_method|0218_a_widget_title_that_was_never_chosen_stops_speaking_for_the_catalog|0219_purchasing_delivery_method_can_be_unrecorded|0220_a_finance_note_a_client_reads_names_nobody|0221_the_setup_entries_behind_no_customer_document_come_back_out|0222_an_invoice_with_no_journal_behind_it_asks_for_one|0223_pos_counter_layout)\.sql$'

if git ls-files -z | xargs -0 -r grep -IlP '[\x{2013}\x{2014}]' \
  | grep -Ev "$carried_baseline" | grep -Ev "$ported_carry" | grep .; then
  fail "em or en dash found in the files listed above"
fi

echo "check-shape: OK"
