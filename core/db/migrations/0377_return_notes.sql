-- 0377_return_notes.sql
--
-- Sub item 10.2, the credit note across orders: one note holding N single-order
-- returns of one customer at one branch, committed in ONE transaction. The note
-- is the credit note (the Q11 ruling): the pos_returns rows and their journals
-- stay the money's own record, each child keeping its own return number and its
-- own pos.return.committed journal (the GL builder and its idempotency key are
-- untouched), and no billing credit memo document sits beside them.
--
-- The note carries the customer, the one branch, the rail, the totals summed
-- over its children, and its own idempotency key (unique where not empty, the
-- ux_pos_returns_idem idiom): one key per note, the children's keys derived
-- from it by the service. pos_returns.note_id links each child to its note,
-- NULL on every single-order return.
--
-- Owner module: internal/pos.

CREATE SEQUENCE return_note_no_seq START 1;

CREATE TABLE return_notes (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  note_no        CITEXT NOT NULL UNIQUE,   -- '#NOTE-n'
  customer_id    UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  refund_method  TEXT NOT NULL CHECK (refund_method IN ('CASH','CARD','ACCOUNT_CREDIT')),
  subtotal_cents BIGINT NOT NULL,
  tax_cents      BIGINT NOT NULL,
  total_cents    BIGINT NOT NULL,
  reason         TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT NOT NULL DEFAULT '',
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ux_return_notes_idem ON return_notes(idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX idx_return_notes_customer ON return_notes(customer_id);

ALTER TABLE pos_returns ADD COLUMN note_id UUID REFERENCES return_notes(id) ON DELETE RESTRICT;
CREATE INDEX idx_pos_returns_note ON pos_returns(note_id);
