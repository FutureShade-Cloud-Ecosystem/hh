-- 0175 — DIB-510: WHAT WE SENT A CUSTOMER ABOUT AN INVOICE, AND WHETHER THEY OPENED IT.
--
-- PURPOSE. The receivables register has to answer two questions the system could not answer at all:
--   1. "Has this customer actually seen this invoice?"  — the register's warning line
--      ("Customer has not opened invoice") and the detail's Activity timeline
--      ("Invoice emailed" / "Viewed") both read this table and nothing else.
--   2. "Have we already chased them, and how hard?"     — the ad-hoc reminder with its TONE.
--
-- ONE TABLE FOR BOTH, because they are one fact: a thing we sent about one invoice, at one moment,
-- through one channel, which the recipient either opened or did not. Splitting "the invoice email"
-- from "the reminder email" would give the timeline two sources to interleave and the open-tracking
-- two places to look, and the first time they disagreed the screen would be telling somebody that a
-- customer never opened an invoice they demonstrably replied to.
--
-- WHAT THIS IS NOT. It is NOT the automated collections cadence (0110 `ar_notice_log`), which is a
-- per-bucket sweep with its own once-per-bucket invariant and its own suppression rules. That ledger
-- stays exactly as it is and this table does not read or write it. This one records the DELIBERATE
-- act of a person at a desk deciding to send something now — a different fact with a different
-- author, and conflating the two would let a staff reminder consume a bucket the sweep still owes.
--
-- COLUMNS.
--   kind             INVOICE   the document itself, emailed to the customer.
--                    REMINDER  a chase about an unpaid document (carries `tone`).
--                    RECEIPT   confirmation that a payment was applied.
--                    STATEMENT the account statement produced from this register's bulk action; the
--                              statement ARTEFACT still lives in 0105 ar_statements — this row only
--                              records that we sent it and lets the timeline show it.
--   tone             the reminder's register: FRIENDLY | FIRM | FINAL. '' for every other kind.
--                    Stored rather than derived because "which tone did we use last time" is the
--                    whole reason a human picks the next one, and re-deriving it from the body text
--                    later is guesswork about a decision somebody already made.
--   channel          EMAIL | SMS | PRINT. Recorded, not assumed: a printed-and-posted statement is a
--                    send, and a row that pretended it was an email would make `opened_at IS NULL`
--                    read as "ignored" when the truth is "unknowable by this route".
--   to_address       a snapshot of where it went. Snapshot, not a join: the contact row can change
--                    and this must stay a true record of what we did.
--   idempotency_key  the one-time client key. UNIQUE where present — a double-clicked Send Reminder
--                    converges on the row it already wrote instead of texting somebody twice.
--   opened_at        FIRST open. open_count counts every one. Both NULL/0 until an open is seen, and
--                    "not opened" is the honest default rather than an assumption of delivery.
--   sent_by          the staff member who decided. ON DELETE SET NULL: the record of the act must
--                    outlive the employment.
--
-- OPEN TRACKING IS HONEST ABOUT ITS LIMITS. An open is recorded when the customer's browser fetches
-- the invoice document through the tracked link we sent. That means it under-reports (a customer who
-- reads the PDF attachment in a mail client we never hear from) and it never over-reports. The
-- register therefore says "not opened" and never "ignored", and the difference is deliberate.
--
-- Additive and idempotent: one table, three indexes. Dropping it loses the send history and nothing
-- else — no money, no document, no cadence.

CREATE TABLE IF NOT EXISTS ar_invoice_deliveries (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  -- RECORDED IDENTIFIERS, NOT CASCADING FOREIGN KEYS (the 0110 posture, verbatim reasoning): this is
  -- a record of something we did to a customer and it must stay true if the invoice is later voided.
  invoice_id      UUID NOT NULL,
  customer_id     UUID NOT NULL,
  invoice_no      TEXT NOT NULL DEFAULT '',

  kind            TEXT NOT NULL CHECK (kind IN ('INVOICE','REMINDER','RECEIPT','STATEMENT')),
  tone            TEXT NOT NULL DEFAULT '' CHECK (tone IN ('','FRIENDLY','FIRM','FINAL')),
  channel         TEXT NOT NULL DEFAULT 'EMAIL' CHECK (channel IN ('EMAIL','SMS','PRINT')),
  to_address      TEXT NOT NULL DEFAULT '',

  -- Snapshot of what was owed when we sent. What the balance later becomes does not change what we
  -- told them, and a reminder quoting a figure nobody can reconstruct is a reminder nobody can defend.
  balance_cents   BIGINT NOT NULL DEFAULT 0,

  idempotency_key TEXT NOT NULL DEFAULT '',
  sent_by         UUID REFERENCES users(id) ON DELETE SET NULL,
  sent_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

  opened_at       TIMESTAMPTZ,
  open_count      INTEGER NOT NULL DEFAULT 0 CHECK (open_count >= 0),

  -- A tone only means something on a chase.
  CONSTRAINT ar_invoice_deliveries_tone_is_reminder
    CHECK (tone = '' OR kind = 'REMINDER'),
  -- open_count and opened_at agree or the row is lying about one of them.
  CONSTRAINT ar_invoice_deliveries_open_agrees
    CHECK ((opened_at IS NULL AND open_count = 0) OR (opened_at IS NOT NULL AND open_count > 0))
);

-- THE replay guard. Partial-unique so rows written without a key (a backfill, a system send) are
-- unconstrained, exactly as 0028 does for payments.
CREATE UNIQUE INDEX IF NOT EXISTS ux_ar_invoice_deliveries_key
  ON ar_invoice_deliveries (idempotency_key) WHERE idempotency_key <> '';

-- The register's per-invoice read: "was this emailed, was it opened, when were we last in touch".
CREATE INDEX IF NOT EXISTS idx_ar_invoice_deliveries_invoice
  ON ar_invoice_deliveries (invoice_id, sent_at DESC);

-- The cooldown read: "has this account heard from us recently" — per CUSTOMER, because a contractor
-- with eleven overdue invoices should get one conversation, not eleven (0110's rule, same reason).
CREATE INDEX IF NOT EXISTS idx_ar_invoice_deliveries_customer
  ON ar_invoice_deliveries (customer_id, sent_at DESC);

COMMENT ON TABLE ar_invoice_deliveries IS
  'DIB-510: one row per deliberate send about one invoice (document, reminder+tone, receipt, statement) plus its open tracking. NOT the 0110 automated collections cadence.';
