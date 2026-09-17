-- 0051: MSG-01 — unread-summary view + the messaging event-topic contract note.
-- Owner: internal/messaging. Feature: MSG-01 / DIB-152 (G-LOCK-076).
--
-- WHY a dedicated msg.event.* topic family (FR-3 wiring, no new broker):
--   The notify.Dispatcher enforces ONE handler per topic and PANICS on a duplicate registration
--   (internal/notify/dispatcher.go). The financial topics the PRD lists (payment.received,
--   order.fulfilled, …) are ALREADY owned by the GL posting consumer (gl.PostedTopics). A second
--   handler on those exact topics would crash the server at boot. So messaging owns its OWN topic
--   family — 'msg.event.<topic>' — emitted to the SAME events_outbox by the same emitOutbox pattern
--   and drained by the SAME dispatcher. This reuses the transactional outbox + dispatcher (the
--   explicit instruction: LISTEN/NOTIFY or the existing dispatcher only, no new broker) while
--   staying collision-free. A producer that already emits e.g. payment.received ALSO emits
--   msg.event.payment.received (a one-line addition at the emit site) OR a thin relay re-emits it;
--   the P1 demo seed (0052) exercises the mapping end-to-end. The channel-routing + dedup logic
--   lives in internal/messaging (consumer.go), keeping the dispatcher domain-free.
--
-- The six P1 topics (PRD §7 / §9 Q2): msg.event.order.released, msg.event.order.blocked,
-- msg.event.stock.received, msg.event.delivery.scheduled, msg.event.payment.received,
-- msg.event.credit.hold  (nsf/credit-hold collapsed to credit.hold). See internal/messaging/consumer.go.

-- ── Unread summary view ─────────────────────────────────────────────────────────
-- Single source of truth for the FR-7 unread math, so the count is computed identically everywhere
-- (endpoint, tests) and a future digest can reuse it. Per (member, channel): the number of messages
-- newer than the member's last_read_at that the member did NOT author and that are not deleted.
-- A member's OWN messages never count as unread; a system EVENT message (author NULL) always can.
-- mentioned is a P1 placeholder (FALSE) — @mention parsing lands with FR-2 mentions; the column
-- exists now so the API/consumer shape (unread + mention flags) is stable across the phase.
CREATE VIEW msg_unread_summary AS
SELECT
  mem.user_id                                        AS user_id,
  mem.channel_id                                     AS channel_id,
  COUNT(msg.id)                                      AS unread_count,
  MAX(msg.created_at)                                AS latest_at,
  FALSE                                              AS mentioned
FROM msg_members mem
LEFT JOIN msg_messages msg
  ON msg.channel_id = mem.channel_id
 AND msg.created_at > mem.last_read_at
 AND msg.deleted_at IS NULL
 AND (msg.author_id IS NULL OR msg.author_id <> mem.user_id)
GROUP BY mem.user_id, mem.channel_id;
