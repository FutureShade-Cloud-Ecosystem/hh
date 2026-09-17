-- 0013: audit log + transactional event outbox.
-- Spec: DATA_MODEL.md §13, MODULE_FRAMEWORK.md §4 (G-LOCK-004) · Traces: R10.
-- audit_log.actor_id has NO FK by design: entries must survive user deletion + system actors.
-- Outbox rows are written INSIDE the business tx; the dispatcher publishes + marks them.

CREATE TABLE audit_log (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  action      TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id   UUID,
  actor_id    UUID,
  branch_id   UUID,
  changes     JSONB NOT NULL DEFAULT '{}',
  request_id  TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_actor_time ON audit_log(actor_id, created_at DESC);
CREATE INDEX idx_audit_action ON audit_log(action);

CREATE TABLE events_outbox (
  event_id     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  topic        TEXT NOT NULL,
  payload      JSONB NOT NULL,
  occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  status       TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PUBLISHED','DEAD')),
  attempts     INTEGER NOT NULL DEFAULT 0,
  published_at TIMESTAMPTZ,
  last_error   TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_outbox_pending ON events_outbox(occurred_at) WHERE status = 'PENDING';
CREATE INDEX idx_outbox_topic ON events_outbox(topic);
