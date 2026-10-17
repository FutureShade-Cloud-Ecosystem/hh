-- 0400: S6 change 7 membership rules and channel visibility, hand-added members.
-- Owner: internal/messaging.
--
-- WHAT THIS ADDS. Walk change 7 ruling 7 (Notifications opt in, Settings
-- membership rules, New channel admins only):
--   - msg_channels.visibility, a PUBLIC/PRIVATE flag defaulting to PUBLIC. A
--     PRIVATE channel is hidden from the public directory and only accepts
--     members by hand (the manager wall: room owner or house messages admin),
--     matching the brief's hand-added reason.
--   - msg_members.added_reason and msg_members.added_by. A hand-added
--     EXPLICIT row carries the text reason the manager gave and the user who
--     added them, so the Settings > Membership rules "Added by hand" list can
--     name both. Existing rows backfill added_by NULL (the history's managers
--     are unknown) and added_reason '' (no prior text).
--
-- FORWARD ONLY. The added_reason column is a TEXT default '' so every existing
-- row backfills empty in place. added_by references users(id), and the failure
-- mode is the column's full safety definition: an existing row that names a
-- user id the column's reference cannot satisfy fails the ALTER. There is no
-- such row today (the column is new), so the column goes in NULLable.

ALTER TABLE msg_channels
  ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'PUBLIC'
  CHECK (visibility IN ('PUBLIC', 'PRIVATE'));

ALTER TABLE msg_members
  ADD COLUMN IF NOT EXISTS added_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE msg_members
  ADD COLUMN IF NOT EXISTS added_by UUID REFERENCES users(id);