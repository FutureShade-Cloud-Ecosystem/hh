-- 0097: the out-of-band (SMS) notification outbox. Owner: internal/messagingprovider (Wave 2, Swarm B).
--
-- Plain-language intent: some things have to reach a person who is not looking at the ERP — a
-- customer whose order is staged and waiting, an account that has gone past due. Those go out as a
-- text message. This table is the durable queue those notices sit in between "the business fact
-- committed" and "the provider accepted it".
--
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
-- WHY THIS IS NOT events_outbox (0013), WHICH ALREADY EXISTS.
-- events_outbox is the ERP's DOMAIN event log, and its dispatcher claims rows by status alone —
-- `WHERE status = 'PENDING'`, any topic. A topic added to that table without a handler registered on
-- THAT dispatcher instance is claimed, fails with "no handler registered for topic", retries, and
-- dead-letters. The shared dispatcher's handler set is fixed at boot before it starts running, so a
-- module wired after it starts cannot add one without a data race on the handler map.
--
-- So this is a SECOND, TOPIC-SCOPED queue with its own dispatcher instance — the SAME notify.Dispatcher
-- type, the same notify.Notifier port, the same retry/dead-letter policy, over a different table. That
-- keeps out-of-band delivery completely disjoint from financial event posting: an SMS provider outage
-- can never retry, block or dead-letter a GL journal, and a slow GL drain can never delay a customer
-- notice. It is the same reason payments and messaging do not share a queue.
--
-- The shape is deliberately identical to events_outbox (topic/payload/status/attempts/last_error/
-- occurred_at/published_at) so notify.PgOutboxStore's claim semantics port over verbatim and the two
-- can be merged later if the boot ordering is ever reworked.
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
--
-- NO CREDENTIALS, NO PHONE NUMBERS AT REST BEYOND DELIVERY. payload carries the business reference
-- (order id, invoice id) and the resolved recipient — it does NOT carry provider credentials, ever.
-- A published row's payload is redacted to '{}' by the dispatcher's secret-topic path if the topic is
-- registered as secret, which is how a recipient's number stops living here once it has been used.
--
-- Rollback: purely additive (one table). Dropping it is safe — the module nil-seams on an absent pool
-- and the yard stage write simply stops enqueueing a notice.

CREATE TABLE sms_outbox (
  event_id     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  topic        TEXT NOT NULL,                     -- 'sms.ar.notice' | 'sms.dispatch.notice'
  payload      JSONB NOT NULL,
  occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  status       TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PUBLISHED','DEAD')),
  attempts     INTEGER NOT NULL DEFAULT 0,
  published_at TIMESTAMPTZ,
  last_error   TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- The dispatcher's only hot read: the oldest pending notice.
CREATE INDEX idx_sms_outbox_pending ON sms_outbox(occurred_at) WHERE status = 'PENDING';
-- Operability: "what has failed to send", newest first.
CREATE INDEX idx_sms_outbox_dead ON sms_outbox(occurred_at DESC) WHERE status = 'DEAD';
