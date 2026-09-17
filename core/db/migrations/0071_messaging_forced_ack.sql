-- 0071: MSG-01 Feature 1 — Screen-Takeover forced acknowledgement (requires_ack + msg_acks).
-- Owner: internal/messaging. Feature: scopeBrief #1 (buildableSpikeNow). Follows 0050/0069 conventions.
--
-- Plain-language intent: some system EVENT posts are too important to scroll past (a credit hold, a
-- blocked order). Such a message is flagged requires_ack; a shared kiosk/tablet (the Lit
-- <hos-kiosk-takeover> overlay) shows it full-screen and BLOCKS until a staffer taps "I got it",
-- which writes one ack row here. The kiosk polls the un-acked set (UnackedRequiresAck).
--
-- COORDINATION NOTE (DIB-217): requires_ack + msg_acks is the SHARED forced-ack schema. DIB-217's
-- forced-ack work structurally OVERLAPS this — the P1 lane must CONSUME this schema (this column +
-- this table), NOT fork its own flag/table. The consumer allowlist
-- (internal/messaging/consumer.go: ackRequiredTopics) is the P1 producer that sets requires_ack today
-- (credit-hold / order-blocked only — deliberately NOT blanket-set). See service.go AckMessage /
-- UnackedRequiresAck for the read/write seam.
--
-- Conventions (0050/0069): identity is the SoT — msg_acks references users(id) and msg_messages(id),
-- both ON DELETE CASCADE. TIMESTAMPTZ; additive, non-breaking (requires_ack defaults FALSE, so every
-- pre-existing message stays a normal, non-blocking post).

-- ── Forced-ack flag on a message ────────────────────────────────────────────────
ALTER TABLE msg_messages
  ADD COLUMN requires_ack BOOLEAN NOT NULL DEFAULT FALSE;

-- The kiosk poll (UnackedRequiresAck) scans requires_ack messages the caller has not yet acked; a
-- PARTIAL index keeps that scan cheap even as the history grows (the flag is rare), newest-first to
-- match the poll's ORDER BY created_at DESC.
CREATE INDEX idx_msg_messages_requires_ack ON msg_messages(created_at DESC) WHERE requires_ack;

-- ── Acknowledgement rows ────────────────────────────────────────────────────────
-- One ack per (message, user): WHO acknowledged a forced-ack message, WHEN, and HOW. method is the
-- input channel — TAP is the v1 canned path; QUICK_REPLY / VOICE are reserved (schema only) for the
-- gated device/AI work below so the CHECK never has to change when those land.
CREATE TABLE msg_acks (
  message_id UUID NOT NULL REFERENCES msg_messages(id) ON DELETE CASCADE,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  acked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  method     TEXT NOT NULL CHECK (method IN ('TAP','QUICK_REPLY','VOICE')),
  PRIMARY KEY (message_id, user_id)
);
-- "All acks by user U" lookups (mirrors idx_msg_members_user); the (message,user) PK already covers
-- the per-message NOT-EXISTS anti-join the poll runs.
CREATE INDEX idx_msg_acks_user ON msg_acks(user_id);

-- GATED (do NOT build here):
--  * device_registrations + real forklift/tablet device binding — hardware is unknown, so there is no
--    device schema yet; the kiosk mounts today via a ?kiosk=1 shell flag, not a bound device.
--  * AI-authored quick-replies (the QUICK_REPLY / VOICE methods) belong to TanyAI DIB-218 — v1 ships
--    only the canned TAP "I got it". The CHECK already admits the future methods so no re-migration.
