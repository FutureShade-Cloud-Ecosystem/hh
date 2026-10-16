-- 0401: walk change 7, slice S7 — per-person notification preferences
--       (event-class toggles + quiet hours) and the desktop-push gate.
-- Owner: internal/messaging. Follows 0157 (per-user rules, decisions not defaults) and 0069
-- (notification prefs additive, non-breaking).
--
-- Plain-language intent. A user with no row in EITHER table gets the row's column defaults; an
-- absent category is the per-category default from messaging.EventCategoryDefaults; an absent
-- quiet_hours row is enabled=false, 18:00 to 06:00, America/Los_Angeles, empty bypass list
-- (the shipped, quiet-by-default UI state). Both tables store DECISIONS, not defaults (the 0157
-- invariant that made "delete every row" return the system to DIB-449 behaviour applies here too).
--
-- The desktop-push gate lives in code (ShouldDeliverPush, internal/messaging/quiet_hours.go). SQL
-- carries the user's two choices; the predicate is pure (no DB), so the whole rule matrix is
-- unit-tested DB-free, exactly like shouldNotify.
--
-- ── Forced acknowledgement pierces everything ─────────────────────────────────
-- The DIB-269 / DIB-449 defence-in-depth ruling still stands here word for word: a forced-ack
-- post is un-skippable and a quiet-hours window does not silence it. The brief pins this with
-- "forced-acknowledgement notices (`requiresAck(topic)`) always reach the recipient in-app and
-- push regardless of preferences". The migration enforces nothing; the predicate enforces it.
-- A future bug that confuses the two is caught by TestShouldDeliverPush_ForcedAckBreaksThrough.

-- ═══════════════════════════════════════════════════════════════════════════════
-- Per-person event class notification preferences ("Events that reach me")
-- ═══════════════════════════════════════════════════════════════════════════════
--
-- The closed 6-item category set is the brief's board order; the CHECK is the schema's copy of
-- messaging.EventCategories so a value the code does not know can never be stored (the same
-- text-plus-CHECK shape as 0157's msg_payment_notify_rules.rule).
--
--   1. orders_released_and_blocked      (default in_app: true,  push: true)
--   2. stock_received                   (default in_app: true,  push: false)
--   3. credit_holds_and_credit_owed     (default in_app: true,  push: true)
--   4. deliveries_scheduled             (default in_app: true,  push: false)
--   5. payments                         (default in_app: false, push: false)
--   6. approvals_waiting_on_me          (default in_app: true,  push: true)
--
-- in_app is the "show in the hub" gate (the badge + toast pipeline). push is the "raise a desktop
-- notification" gate (the SSE/desktop-push path that calls ShouldDeliverPush).
--
-- The hot read is "what did THIS user decide for THIS category?"; the index is on (user_id, category)
-- which IS the primary key (the index is implicit), so no extra index is needed.
CREATE TABLE msg_person_event_prefs (
  user_id        UUID    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  event_category TEXT    NOT NULL CHECK (event_category IN (
                                  'orders_released_and_blocked',
                                  'stock_received',
                                  'credit_holds_and_credit_owed',
                                  'deliveries_scheduled',
                                  'payments',
                                  'approvals_waiting_on_me')),
  in_app         BOOLEAN NOT NULL,
  push           BOOLEAN NOT NULL,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, event_category)
);

-- The "all categories for one user" read (the GET endpoint). Partial / not partial is a no-op
-- difference at six rows per user, so a plain b-tree is fine; the table is small and the read is
-- in-process.
CREATE INDEX idx_msg_person_event_prefs_user ON msg_person_event_prefs(user_id);

-- DELIBERATELY NOT SEEDED. Each user decides for themselves; absent → the per-category default from
-- messaging.EventCategoryDefaults (the same honesty the DIB-449 prefs surface applies to an
-- inherited channel default: "no row" and "off" look identical to the reader because they mean
-- the same thing to the system).

-- ═══════════════════════════════════════════════════════════════════════════════
-- Per-person quiet hours ("QUIET HOURS", ruling 8)
-- ═══════════════════════════════════════════════════════════════════════════════
--
-- One row per user (the primary key). start_local / end_local are 24-hour HH:MM strings in the
-- user's own timezone; overnight windows (start > end, e.g. 18:00 to 06:00) are supported and
-- normalized at the predicate boundary (QuietHours.ActiveAt). The CHECKs reject anything that is
-- not a well-formed HH:MM, so the predicate never has to defend against malformed input.
--
-- bypass_user_ids is the DM carve-out from the board ("DMs from these people still ring as drawn
-- on the board"): a list of users whose DMs pierce the quiet window. Empty list is the shipped
-- default, the gated DM sender list (the principal's Tracker rows for "DM pierce") lives in user-
-- config, not here.
--
-- timezone defaults to America/Los_Angeles (the principal's home zone); time.LoadLocation returns
-- a fixed offset for that name, so the predicate can build a *time.Location reliably. A bad
-- timezone in a PUT body is the handler's fault (400), caught at the service boundary before any
-- row is written.
CREATE TABLE msg_quiet_hours (
  user_id          UUID      NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  enabled          BOOLEAN   NOT NULL DEFAULT FALSE,
  start_local      TEXT      NOT NULL DEFAULT '18:00'
                    CHECK (start_local ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  end_local        TEXT      NOT NULL DEFAULT '06:00'
                    CHECK (end_local ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  timezone         TEXT      NOT NULL DEFAULT 'America/Los_Angeles',
  bypass_user_ids  UUID[]    NOT NULL DEFAULT '{}',
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- DELIBERATELY NOT SEEDED. The shipped state is "quiet hours off" (the column default) so a fresh
-- database is NOT in a quiet window, and the same property holds after a row is deleted (one row
-- per user; ON DELETE CASCADE).

-- GATED (do NOT build here):
--   * per-DOW quiet schedules ("quiet Sat–Sun"). The board draws one window a day; widening to a
--     per-DOW schedule is a real request and a column-add, deliberately left for the user-led follow
--     up rather than built.
--   * a per-channel quiet override ("never quiet #alerts-all"). The TierEscalation / TierFirehose
--     tier already does this work in code (ShouldDeliverPush clause (a)), a quiet-channel column
--     would be a second expression of "this channel pierces me through".
--   * a server-side audit of quiet-hours changes. The audit spine (internal/audit) takes the row
--     write through a normal INSERT; the column-by-column change audit is a separate ticket.