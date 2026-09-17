-- 0069: MSG-01 — per-user notification preferences + channel tiers + the #alerts-all firehose.
-- Owner: internal/messaging. Feature: MSG-01 (Feature A). Follows 0050/0052 conventions.
-- Plain-language intent: a channel now carries a notification TIER (how loud it is), and each user can
-- tune how a channel notifies THEM (desktop on/off, whether escalation overrides a mute, mute). The
-- new #alerts-all FIREHOSE channel is the company-wide critical-alert firehose: escalation posts reach
-- everyone even when they have muted the noise, when their escalation_override is on (see shouldNotify
-- in internal/messaging/service.go).
--
-- Conventions (0050): identity is the SoT — prefs reference users(id) and channels reference
-- msg_channels(id), both ON DELETE CASCADE. TIMESTAMPTZ; additive, non-breaking.

-- ── Channel tier (how loud a channel is) ────────────────────────────────────────
-- STANDARD   — a normal channel (default; matches every channel seeded before this migration).
-- ESCALATION — a channel whose posts are escalations (louder; drives escalation_override behaviour).
-- FIREHOSE   — the company-wide critical firehose (#alerts-all): escalation posts bypass a mute for any
--              user whose escalation_override is on (shouldNotify).
ALTER TABLE msg_channels
  ADD COLUMN tier TEXT NOT NULL DEFAULT 'STANDARD'
    CHECK (tier IN ('STANDARD','ESCALATION','FIREHOSE'));

-- ── Per-user notification preferences ───────────────────────────────────────────
-- One row per (channel, user) — the caller's own notification tuning for that channel. Absent row =
-- the defaults below (desktop on, escalation overrides a mute, not muted), so a user with no explicit
-- pref still notifies normally. This is DISTINCT from msg_members.muted (the legacy channel-hide flag):
-- these prefs drive the shouldNotify desktop-notification decision, not membership/visibility.
CREATE TABLE msg_notification_prefs (
  channel_id          UUID NOT NULL REFERENCES msg_channels(id) ON DELETE CASCADE,
  user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  desktop_enabled     BOOLEAN NOT NULL DEFAULT TRUE,   -- raise a desktop notification for this channel
  escalation_override BOOLEAN NOT NULL DEFAULT TRUE,   -- a FIREHOSE escalation still notifies even if muted
  muted               BOOLEAN NOT NULL DEFAULT FALSE,  -- suppress notifications for this channel
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, user_id)
);
-- "All my prefs" lookups (mirrors idx_msg_members_user).
CREATE INDEX idx_msg_notification_prefs_user ON msg_notification_prefs(user_id);

-- ── Firehose seed: #alerts-all (company-wide critical alerts) ────────────────────
-- A GLOBAL TEAM channel every active staff auto-joins (auto_scope GLOBAL, auto_role '*'), exactly like
-- #office/#finance in 0052 — but tier FIREHOSE so escalations pierce a mute (per escalation_override).
-- Fixed UUID + ON CONFLICT (id) DO NOTHING, matching the 0052 seed style; safe on any DB.
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role, tier) VALUES
  ('a5000000-0000-4000-9000-000000000005','TEAM',NULL,
     'alerts-all','#alerts-all','Company-wide critical alerts — escalations reach everyone','GLOBAL','*','FIREHOSE')
ON CONFLICT (id) DO NOTHING;

-- GATED (do NOT build routing here): house-fallback auto-routing. No assigned-rep column exists —
-- customers/orders carry only created_by — so a `house-<branch>` channel cannot be auto-routed yet.
-- TODO(decision: house-fallback routing / assigned-rep source): once an assigned-rep (or a decision to
-- fall back to created_by/branch) is settled, seed the `house-<branch>` channels and add the routing in
-- the consumer. Deliberately NOT seeded here to avoid dead, unrouted channels.
