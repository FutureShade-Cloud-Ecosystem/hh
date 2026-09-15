# Recorded HH Pro fixtures, vendored (D22)

The sixteen JSON files in this directory are byte for byte copies of the
recorded live captures in HH Pro, vendored so that
`core/api/tools/fixtures.mjs` runs in `ci` with no sibling checkout (D22 in
`docs/analysis/DECISIONS.md`).

- Source path: `hh_pro_dibbits/src/core/supplier/__tests__/fixtures-live/`
- Source commit: `829d387baec93556ffa0e9242f4091e9be9544ec`
- Copied: the sixteen `*.json` files, unchanged. The source directory's own
  `README.md` is not copied. The captures already carry the one edit that
  source records: every `token` value is `live-capture-token-redacted`, so
  no credential is present.

## The rule

A fixture that does not validate against the schema the contract assigns to
its route is a finding, never a schema to loosen. `fixtures.mjs` fails until
the finding is resolved in the schema (when the schema is what is wrong) or
recorded with its reason in `KNOWN_FINDINGS`. Never edit a fixture to make it
pass: these files are what the server said.

## Running against the live sibling

`HH_PRO_ROOT=../hh_pro_dibbits node core/api/tools/fixtures.mjs` reads the
captures from a checkout instead of this directory. Re-vendor only by copying
the files byte for byte from a named source commit and updating the commit
above.
