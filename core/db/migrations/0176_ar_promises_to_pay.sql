-- 0176 — DIB-510: A PROMISE TO PAY. "He says the cheque goes out Friday."
--
-- PURPOSE. The single most common outcome of a collections call is neither a payment nor a refusal —
-- it is a date. Until now that date lived in somebody's notebook, which means the next person to ring
-- the same account either did not know about it (and chased a customer who had already committed) or
-- knew about it only because they happened to be the same person. Both are how a supplier loses an
-- account it was entitled to keep.
--
-- A promise is a COMMITMENT WE RECORDED, not a change to the money. Nothing in this table touches a
-- balance, a due date, an aging bucket or a credit hold. The invoice stays exactly as overdue as it
-- was; what changes is that the register can say "promised CA$4,300 by 2026-09-04, taken by Scrubname0008" and
-- the next caller can decide whether that promise is still worth waiting on. Keeping it out of the
-- money is the whole discipline: a promise that quietly un-aged an invoice would let a customer
-- postpone a credit hold with a phone call.
--
-- ONE OPEN PROMISE PER INVOICE, enforced by a partial unique index rather than by a read-then-write.
-- Two live promises for one document is not a richer record, it is two people who did not know about
-- each other — precisely the state this table exists to end. Recording a new one requires settling the
-- old one, which is a deliberate act with a reason attached.
--
-- STATUS, and why 'BROKEN' is a status and not a deletion:
--   OPEN      made, not yet due.
--   KEPT      settled by money arriving. Set when a payment covers the promised amount.
--   BROKEN    the promised date passed with the money still outstanding. This is the row a
--             collections person most needs to see, and deleting it — or letting it silently expire
--             back to "no promise" — would erase the one fact that justifies a firmer tone.
--   CANCELLED withdrawn by us (invoice credited, dispute opened, account settled another way).
-- Transitions are recorded by the service; the CHECK only bounds the vocabulary.
--
-- Additive: one table, two indexes. Dropping it loses the commitments and nothing else.

CREATE TABLE IF NOT EXISTS ar_promises_to_pay (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  -- Real foreign keys here, unlike 0175's send log: a promise is a live piece of work about a live
  -- document, not a historical record of something we did to a customer. If the invoice goes away the
  -- promise is meaningless, so it goes too.
  invoice_id     UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
  customer_id    UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,

  -- The two facts a promise IS. A partial promise ("half now, half at month end") is a real thing, so
  -- the amount is recorded rather than assumed to be the balance.
  promised_cents BIGINT NOT NULL CHECK (promised_cents > 0),
  promised_date  DATE NOT NULL,

  status         TEXT NOT NULL DEFAULT 'OPEN'
                   CHECK (status IN ('OPEN','KEPT','BROKEN','CANCELLED')),

  -- WHO on the customer's side made the commitment, in their own words ("Scrubname0023, on site"). Scrubname0011 text,
  -- because the person who answers the phone is very often not a contact row.
  promised_by    TEXT NOT NULL DEFAULT '' CHECK (char_length(promised_by) <= 120),
  note           TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),

  -- WHO on our side took it. The attribution is the point: a promise nobody is named against is a
  -- rumour.
  taken_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  taken_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

  settled_at     TIMESTAMPTZ,
  settled_why    TEXT NOT NULL DEFAULT '',

  -- The one-time client key: a double-submitted promise converges rather than duplicating.
  idempotency_key TEXT NOT NULL DEFAULT '',

  CONSTRAINT ar_promises_settled_has_time
    CHECK (status = 'OPEN' OR settled_at IS NOT NULL)
);

-- ONE OPEN PROMISE PER INVOICE. The database owns this, not a query.
CREATE UNIQUE INDEX IF NOT EXISTS ux_ar_promises_one_open
  ON ar_promises_to_pay (invoice_id) WHERE status = 'OPEN';

CREATE UNIQUE INDEX IF NOT EXISTS ux_ar_promises_key
  ON ar_promises_to_pay (idempotency_key) WHERE idempotency_key <> '';

-- The register read: "which of this account's promises are live, and which were broken".
CREATE INDEX IF NOT EXISTS idx_ar_promises_customer
  ON ar_promises_to_pay (customer_id, promised_date);

COMMENT ON TABLE ar_promises_to_pay IS
  'DIB-510: a recorded commitment to pay an invoice by a date. Never touches balances, due dates, aging or holds — it records what a customer said, so the next caller knows.';
