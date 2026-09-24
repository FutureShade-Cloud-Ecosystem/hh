-- 0220, ported from the ERP's 0217 under the HH-3.7 MID ruling (Plane #25, D39):
-- the two seeded finance notes are rewritten to the ERP's clean replacement
-- wording, with the predicates adapted to core's own carried text.
--
-- WHAT THIS CORRECTS. The carried seed rows at 0032:55 (gl_policy.notes) and
-- 0035:63 (nsf_policy.notes) hold the D32-scrubbed variant of the ERP's
-- retired sentence: the personal identifier was removed before the first
-- commit, but the scrub classes did not cover the workstream code, the stale
-- amount or the availability phrase, and those still sit in two strings a
-- client reads (the GL Policy card body and the NSF policy editor's Notes
-- field). The ERP retired that sentence in 0217 with clean provisional
-- wording; this port applies the same wording here.
--
-- THE PREDICATES ARE CORE'S OWN TEXT. Each WHERE clause is byte-identical to
-- the value core already carries at 0032:55 and 0035:63 respectively, so the
-- update matches exactly the seeded rows and no row a person has edited (the
-- NSF field is editable; a dealer who typed their own note has said something
-- and is not overwritten). The ERP's original sentence never enters core, in
-- this file or anywhere else (ruling condition 2): it appears zero times in
-- the carried baseline, and this file's predicates were extracted from core's
-- own migrations, not from the ERP's.
--
-- SAFETY, following the ERP's 0217 properties. Two scoped UPDATEs inside one
-- transaction; nothing added, dropped or renamed. Idempotent: a second run
-- matches nothing. updated_at is deliberately not bumped, because this is a
-- correction of something the program said, not something a person did to the
-- policy. No reference to schema_migrations: the demoseed harness applies
-- these files to a scratch database without that bookkeeping table, and
-- Postgres resolves relations at parse time.

BEGIN;

UPDATE gl_policy
   SET notes = 'These settings are provisional until the accountant confirms them.'
 WHERE notes = 'PLACEHOLDER — pending external-accountant confirmation (Scrubname0001 Holt on leave, Q-021)';

UPDATE nsf_policy
   SET notes = 'This fee is provisional until the accountant confirms it.'
 WHERE notes = 'PLACEHOLDER $45 — pending external-accountant confirmation (Scrubname0001 Holt on leave, Q-021)';

COMMENT ON COLUMN gl_policy.notes IS
  'Seeded default body text for the GL Policy card. The seeded value reads: These settings are provisional until the accountant confirms them. '
  'Core 0220 (the ERP 0217 port) rewrote the pre-go-live seed wording to that '
  'sentence because the carried scrubbed variant still held an internal '
  'workstream code and an availability phrase; rows a person edited are not '
  'matched by the byte-identity predicate and keep their text.';

COMMENT ON COLUMN nsf_policy.notes IS
  'Seeded default for the NSF policy editor Notes field. The seeded value reads: This fee is provisional until the accountant confirms it. '
  'Core 0220 (the ERP 0217 port) rewrote the pre-go-live seed wording to that '
  'sentence because the carried scrubbed variant still held an internal '
  'workstream code, a stale amount and an availability phrase; the amount is '
  'deliberately absent so the note cannot contradict the fee row it sits in.';

COMMIT;
