-- 0138: DIB-217 — MSG-EVENTS high-priority automations, forced-ack bubbles, and the
--       state-conflict ruling (LAST-WRITE-WINS with the superseded event auto-resolved).
-- Owner: internal/messaging. Wave 5 / Swarm D. Additive, non-breaking, follows 0071.
--
-- ── THE RULING (state conflicts), stated here so it is reviewable in one place ───
-- A forced-ack EVENT asserts a FACT about a record ("Order #SO-1042 is blocked — credit hold").
-- Between the post and the ack, the record can change again (the hold is cleared, the order is
-- released). The unacked bubble is now a STALE FACT PRESENTED AS CURRENT. The ruling:
--
--     LAST-WRITE-WINS. The newest event about a record is the truth. When a newer event arrives for
--     the same state_key, every OLDER un-acked forced-ack message for that key is AUTO-RESOLVED
--     (superseded_by = the new message, superseded_at = now) and stops blocking. A user NEVER acks a
--     stale fact presented as current.
--
-- Corollaries that fall out of the ruling and are enforced in code (consumer.go / repository.go):
--   * Supersession resolves the OLD bubble; it does not ack it. msg_acks stays the record of who
--     actually SAW something — a superseded message has no ack, and that is the honest answer.
--   * An ALREADY-ACKED message is not superseded (there is nothing left to resolve, and rewriting it
--     would corrupt the ack history).
--   * A message never supersedes itself (redelivery of the SAME event is the existing event_key
--     idempotency no-op, not a supersede).
--   * Ordering is by created_at, not arrival: an out-of-order redelivery cannot resurrect an older
--     fact over a newer one (the newer row is already superseding-or-current; the older insert
--     supersedes nothing newer than itself).

-- ── The state key: WHAT a forced-ack event is a fact ABOUT ─────────────────────
-- state_key identifies the RECORD the event asserts a fact about — 'order:<uuid>',
-- 'customer:<uuid>' — deliberately NOT the topic, so "order blocked" and "order released" for the
-- SAME order collide (that collision is the whole point). NULL for every message that is not a
-- state assertion (a TEXT post, an approval card, a plain informational event).
ALTER TABLE msg_messages ADD COLUMN state_key TEXT;

-- ── The supersede pointer ───────────────────────────────────────────────────────
-- superseded_by names the NEWER message that replaced this fact; superseded_at stamps when. Both NULL
-- for a live message. ON DELETE SET NULL: deleting the newer message must not cascade-delete history.
ALTER TABLE msg_messages ADD COLUMN superseded_by UUID REFERENCES msg_messages(id) ON DELETE SET NULL;
ALTER TABLE msg_messages ADD COLUMN superseded_at TIMESTAMPTZ;

-- A message is either live or superseded — the pointer and the stamp move together.
ALTER TABLE msg_messages ADD CONSTRAINT msg_messages_supersede_pair
  CHECK ((superseded_by IS NULL) = (superseded_at IS NULL));

-- ── Priority (DIB-217's high-priority tier) ────────────────────────────────────
-- NORMAL — an ordinary informational event.
-- HIGH   — the priority tier: it forces an ack (requires_ack), renders as a persistent bubble, and is
--          mirrored into #escalations (0137). requires_ack stays the STORAGE FLAG the kiosk and the
--          bubble both poll (0071's shared schema — DIB-275 is built ONCE and consumed twice); priority
--          is the AUTOMATION's classification of the event that set it.
ALTER TABLE msg_messages ADD COLUMN priority TEXT NOT NULL DEFAULT 'NORMAL'
  CHECK (priority IN ('NORMAL','HIGH'));

-- The supersede scan: "older LIVE forced-ack messages for this state key". Partial + narrow, because
-- it runs on every high-priority event insert.
CREATE INDEX idx_msg_messages_open_state ON msg_messages(state_key, created_at DESC)
  WHERE state_key IS NOT NULL AND requires_ack AND superseded_at IS NULL;

-- The bubble/kiosk poll already has idx_msg_messages_requires_ack (0071); it now also filters on
-- superseded_at IS NULL, which the partial index above serves for the keyed case.

-- GATED (do NOT build here): a per-user "snooze" on a forced-ack bubble, and operator-configurable
-- priority rules (which topics are HIGH). The priority set is a code-level allowlist
-- (consumer.go: ackRequiredTopics, extended by DIB-217) precisely so widening it stays a deliberate,
-- reviewed change rather than a config toggle someone flips at 2am.
