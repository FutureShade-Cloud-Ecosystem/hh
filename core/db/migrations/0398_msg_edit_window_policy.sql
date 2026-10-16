-- 0398: the messaging module's edit window setting (House Messages change 7,
-- slice S4, ruling 6: a person edits or deletes only their own messages,
-- inside an edit window kept as a setting). Owner: internal/messaging.
--
-- WHAT THIS DOES. One single-row policy table in the module's own msg_
-- namespace, the settings kit's typed policy posture for behavior bearing
-- knobs (quote_policy, pos_policy): the row is the dealer's edit window in
-- minutes, the window a person's own message stays editable and deletable
-- in after it is sent. The messaging module cannot read system_settings for
-- this (the tablegate allowlist is frozen for this lane and the knob is the
-- module's own behavior), so the setting lives where the module's own
-- writes live, and the seed inserts the default the boards assume.
--
-- edit_window_minutes 0 is legal and means the window is closed at once
-- (nothing is editable after sending); a larger row widens the window. The
-- read falls back to the same default when the row is absent, so a database
-- migrated before this seed row (none: forward only) and a nil pool answer
-- the same number the row seeds.
--
-- Forward only, additive only: a new table beside the msg_* family, no
-- column on an existing table is touched (msg_messages has carried
-- edited_at and deleted_at since 0050).

CREATE TABLE IF NOT EXISTS msg_policy (
  id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  edit_window_minutes INT NOT NULL DEFAULT 15 CHECK (edit_window_minutes >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO msg_policy (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
