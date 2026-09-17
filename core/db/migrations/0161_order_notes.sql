-- 0161: DIB-493 — CRM-style human notes on an order.
-- Owner: internal/order (the order module is the table's only writer). Additive, self-contained.
-- Follows 0160. (The DIB-492 activity READ needs no table — the audit trail is its store.)
--
-- WHY A TABLE AND NOT MORE AUDIT ROWS. The audit_log is the append-only trail of what the SYSTEM
-- did; a note is something a PERSON said — "customer phoned, wants the flagstone held until the
-- 14th" — recorded to prove the work happened. Notes are editable (with attribution), free-text,
-- and carry a communication channel; the audit trail is none of those things and must never become
-- them. So notes are ordinary business rows here, and the activity stream (order/activity.go)
-- interleaves the two sources at read time.
--
-- COLUMNS.
--   author_id / edited_by  reference users with ON DELETE SET NULL: a note must outlive the staff
--                          account that wrote it (the proof-of-work outlives the employee), and the
--                          display name is resolved at read time, never denormalized.
--   channel                a fixed CHECK vocabulary (how the conversation happened), mirroring the
--                          status-code style every other CHECK set in this schema uses.
--   body                   bounded free text; btrim()'d non-empty so a blank note cannot exist.
--   idempotency_key        the one-time client key. UNIQUE per order, so a retried POST converges
--                          on the already-written note instead of duplicating it (the reorder /
--                          pos_sale precedent, as a constraint rather than a metadata lookup).
--   edited_by / edited_at  set ONLY on edit — a never-edited note carries NULLs and renders clean.
--
-- IDEMPOTENT. CREATE TABLE/INDEX IF NOT EXISTS throughout, so a replay is a no-op.

CREATE TABLE IF NOT EXISTS order_notes (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id        UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  author_id       UUID REFERENCES users(id) ON DELETE SET NULL,
  channel         TEXT NOT NULL CHECK (channel IN ('PHONE','EMAIL','IN_PERSON','OTHER')),
  body            TEXT NOT NULL CHECK (length(btrim(body)) > 0 AND length(body) <= 4000),
  idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
  edited_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  edited_at       TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The replay guard: one key writes one note per order, ever.
CREATE UNIQUE INDEX IF NOT EXISTS uq_order_notes_idem ON order_notes(order_id, idempotency_key);

-- The activity read: one order's notes, newest first.
CREATE INDEX IF NOT EXISTS idx_order_notes_order_time ON order_notes(order_id, created_at DESC);
