-- 0362: card_charge_attempts.resolution_note_full, the manager's whole note (A2 fix round 2, F3).
-- Owner module: internal/payment. Purely additive: one column and one backfill. Dropping the column
-- reopens the defect and nothing else.
--
-- The round 2 review's F3: Resolve stored the note with the trim and the sanitiser only, the
-- StampLateCapture fold composed it into last_error under the house diagnostic cap, and a note
-- longer than what the cap leaves room for (about 200 runes) was silently cut on the happy path
-- too, with nothing upstream bounding it. The ruling's design: last_error stays a bounded
-- diagnostic (the cap is the house rule and is not widened), and the whole sanitised note lives in
-- its own column.
--
--   resolution_note_full   the whole sanitised note, written by Resolve beside the bounded
--                          resolution_note. The column is deliberately NOT cleared by
--                          StampLateCapture: the resolution trio (resolution, resolution_note,
--                          resolved_by) is RESOLVED's alone by 0361's cca_resolution_only_on_resolved
--                          and must go with the move, but the full note is the manager's record,
--                          and carrying it across the reversal is the point of this column (a
--                          VOID_UNKNOWN or VOID_DECLINED row keeps the whole first decision
--                          readable next to the appended reversal diagnostics). No converse CHECK
--                          forces it empty off RESOLVED for exactly that reason; Resolve remains
--                          its only writer, and a later manager's Resolve overwrites it.
--
-- The fold reads resolution_note_full (falling back to resolution_note for a row whose note
-- predates this column), so the composed diagnostic starts from the whole note and the cap takes
-- the head of it, never a pre cut tail.
--
-- Idempotent: every statement carries its own guard, so a second application changes nothing.
ALTER TABLE card_charge_attempts
  ADD COLUMN IF NOT EXISTS resolution_note_full TEXT NOT NULL DEFAULT '';

-- Rows resolved before this column carry their note whole in resolution_note (the sanitiser at the
-- write landed with the same fix round; an unsanitised legacy note is redacted by nothing here, the
-- same as it is today, and the fold's exposure to it is unchanged by this migration).
UPDATE card_charge_attempts
   SET resolution_note_full = resolution_note
 WHERE resolution_note_full = '' AND resolution_note <> '';
