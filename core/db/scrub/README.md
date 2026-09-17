# core/db/scrub

Workstation tooling for D32's scrub of the carried ERP baseline. Nothing
here carries a literal value; the replacement map built at run time does,
and it is local only.

## Run order

1. `carry.py`: copy the ERP migration set from the pinned reference
   checkout into `core/db/migrations/`, byte-verbatim. Verifies the
   checkout sits on the pinned commit, the file count is 196, and the
   highest number is 0212 (defaults; override with flags). Prints one
   `filename blobid` line per file; those lines feed `core/db/PORTED.md`.
2. Enumerate candidates from the carried files into the config files
   `build_map.py` reads (names, first names, addresses, attributions,
   owned domains, reserved domains, business names, supplier names,
   fixture identifiers, the T7 token). One literal per line, `#` starts
   a comment. This enumeration is the lane's human work under
   HH-3.1-scrub-rules.md; the scripts apply the rules mechanically and
   never enumerate on their own.
3. `build_map.py`: classify candidates, apply every exclusion the rules
   give, generate patterned placeholders, collision-check them against
   both repositories' text, and write the map (mode 0600). stdout is
   counts by rule class only, never a value. `--t7 exclude` drops the
   T7 token and changes nothing else; the default is `include`.
4. `prove.py`: apply the map to the carried files, then run the proofs:
   zero remaining literal matches (count only), placeholder occurrence
   and collision counts, and byte-identity of filenames, version
   numbers, and all code outside comments and strings against the ERP
   source. Exit status is zero only when every proof passes.
5. Delegated proofs, run by the lane rather than by these scripts:
   apply the scrubbed set from zero with the binary's `migrate` role
   against a scratch database (`DATABASE_URL`), and run the ERP's own
   gate suite against the result. `prove.py` prints the delegation
   note so the checklist is complete in one place.

## The map's discard rule

The map is built on the workstation, never committed, never reported,
and deleted once the proofs pass. Keep it outside the repository or in
a path `ci` never packages; mode 0600 either way. Every exclusion is
recorded in `core/db/README.md` by reason class rather than by value,
and every changed file is recorded in `core/db/PORTED.md` with its ERP
blob hash from step 1.

## Reports carry counts only

stdout of every script reports counts by rule class (mapped, excluded
by reason, dropped as no-occurrence, collisions resolved, proof
failures). No script prints a literal it scrubs.

## Tests

`python3 -m unittest test_scrub -v` in this directory. Fixtures are
synthetic by construction (reserved domains, patterned names); no real
value appears in them.
