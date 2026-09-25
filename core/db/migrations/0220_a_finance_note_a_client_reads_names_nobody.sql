-- 0220, ported from the ERP's 0217 under the HH-3.7 MID ruling (Plane #25, D39):
-- the two seeded finance notes are rewritten to the ERP's clean replacement
-- wording, with the predicates matching the carried rows by LIKE pattern per
-- the merge condition on the port, recorded as D45.
--
-- WHAT THIS CORRECTS. The carried seed rows at 0032:55 (gl_policy.notes) and
-- 0035:63 (nsf_policy.notes) hold the D32-scrubbed variant of the ERP's
-- retired sentence: the scrub replaced the personal identifier's first token
-- with a patterned placeholder, and what remained in the carried text is the
-- surname beside that placeholder, a dash character, the workstream code, the
-- stale amount and the availability phrase (the D45 finding, recorded against
-- both files in core/db/PORTED.md by reason class). Those sit in two strings
-- a client reads (the GL Policy card body and the NSF policy editor's Notes
-- field). The ERP retired that sentence in 0217 with clean provisional
-- wording; this port applies the same wording here, which retires the
-- remaining identifying value from live data.
--
-- THE PREDICATES ARE LIKE PATTERNS, NOT THE CARRIED TEXT. Each WHERE clause
-- anchors on the patterned placeholder token, the workstream code and the
-- surrounding program words, with wildcards where the surname and the dash
-- sit: the same single seeded row per table matches (proven by a direct LIKE
-- check and on the from-zero apply), no row a person has edited is touched
-- (the NSF field is editable; a dealer who typed their own note has said
-- something and is not overwritten), and neither the surname nor the dash
-- enters this file. The ERP's original sentence never enters core either, in
-- this file or anywhere else (ruling condition 2): it appears zero times in
-- the carried baseline. By D45 the baseline files stay byte-for-byte as
-- carried (frozen migrations are never edited, history is never rewritten);
-- the text remains in git history as a known exposure in a private
-- repository.
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
 WHERE notes LIKE 'PLACEHOLDER % pending external-accountant confirmation (Scrubname0001 % on leave, Q-021)';

UPDATE nsf_policy
   SET notes = 'This fee is provisional until the accountant confirms it.'
 WHERE notes LIKE 'PLACEHOLDER $45 % pending external-accountant confirmation (Scrubname0001 % on leave, Q-021)';

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
