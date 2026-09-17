-- 0177 — DIB-510: A DISPUTED INVOICE. The customer is not late; they are arguing.
--
-- PURPOSE. The register's status vocabulary needs "Disputed", and the count chip beside it has to be
-- a real count of a real state or it is decoration. Until now the system had no way to say that an
-- invoice is unpaid FOR A REASON — so a short-paid order under discussion aged into the 60-day bucket
-- beside a customer who simply had not paid, went into the same collections tone, and eventually
-- tripped the same credit hold. Chasing a customer for money they have told you they are contesting
-- is the fastest way to turn a solvable disagreement into a lost account.
--
-- A DISPUTE IS A FLAG ON THE COLLECTIONS CONVERSATION, NOT ON THE MONEY. Exactly like 0176's promise:
-- the balance, the due date and the aging buckets are untouched, because the money genuinely is
-- outstanding and the ledger must keep saying so. What a dispute changes is what a human does next —
-- and, deliberately, the register's default worklist, which offers disputes as their own chip instead
-- of leaving them mixed into "overdue".
--
-- RESOLUTION IS RECORDED, NOT ERASED. Closing a dispute writes an outcome and a timestamp; it does
-- not delete the row. "This invoice was disputed for three weeks in August and we resolved it by
-- crediting the delivery fee" is exactly the history the next argument needs.
--
--   OPEN      raised, under discussion.
--   RESOLVED  settled — outcome names how (paid in full, credited, written off, withdrawn).
--
-- ONE OPEN DISPUTE PER INVOICE, by partial unique index, for the same reason 0176 allows one open
-- promise: a second one is two people who did not know about each other.
--
-- Additive: one table, two indexes.

CREATE TABLE IF NOT EXISTS ar_invoice_disputes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  invoice_id  UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,

  status      TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','RESOLVED')),

  -- A bounded vocabulary rather than free text, because the register groups by it and because the
  -- four reasons a hardscape-supply invoice gets disputed are genuinely these four. OTHER carries the
  -- note.
  reason      TEXT NOT NULL CHECK (reason IN ('SHORT_DELIVERY','DAMAGED','PRICING','NOT_ORDERED','OTHER')),
  note        TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),

  -- The amount under argument. 0 means "the whole document" — a customer disputing one line of a
  -- twelve-line invoice is the common case and the register shows the figure they are holding back.
  amount_cents BIGINT NOT NULL DEFAULT 0 CHECK (amount_cents >= 0),

  raised_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  raised_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

  outcome     TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('','PAID','CREDITED','WRITTEN_OFF','WITHDRAWN')),
  resolved_by UUID REFERENCES users(id) ON DELETE SET NULL,
  resolved_at TIMESTAMPTZ,

  idempotency_key TEXT NOT NULL DEFAULT '',

  CONSTRAINT ar_disputes_resolved_has_outcome
    CHECK (status = 'OPEN' OR (outcome <> '' AND resolved_at IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_ar_disputes_one_open
  ON ar_invoice_disputes (invoice_id) WHERE status = 'OPEN';

CREATE UNIQUE INDEX IF NOT EXISTS ux_ar_disputes_key
  ON ar_invoice_disputes (idempotency_key) WHERE idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_ar_disputes_customer
  ON ar_invoice_disputes (customer_id, raised_at DESC);

COMMENT ON TABLE ar_invoice_disputes IS
  'DIB-510: an invoice the customer is contesting. Flags the collections conversation, never the money — balance, due date and aging are untouched.';
