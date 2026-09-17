-- 0146: DIB-422 — the invoice DOCUMENT shape that lets a second producer share the invoices table,
--        and the credit-memo document the reversing-entry discipline requires.
--
-- ── WHY THIS MIGRATION EXISTS ──────────────────────────────────────────────────────────────────
-- Until now exactly one production writer inserted into `invoices`: the POS ring. Everything else
-- that needed to bill — a delivered non-POS order, a multi-ship group (0126), a warranty
-- accommodation credit — either had no path at all (W5-A named the park in shipgroup.go's header)
-- or fell back to a journal with no customer-facing document (W5-B's FALLBACK_GL).
--
-- Two producers now write one table. That is only safe if the SHAPE they write is the same shape,
-- and if the rows say which producer made them. This migration adds the three columns that make
-- both true, and NOTHING about the POS ring's INSERT changes: every column added here has a DEFAULT
-- that reproduces exactly what a POS row means today.
--
--   doc_type          'INVOICE' (default) | 'CREDIT_MEMO'. A POS row is an INVOICE by default, so
--                     the ring's INSERT is untouched and every pre-existing row is correct.
--   source_kind       WHO produced it: '' (legacy/unknown), 'POS', 'ORDER', 'MULTISHIP',
--                     'WARRANTY_CREDIT'. Scrubname0011 text with a CHECK rather than an FK — a producer is a
--                     code fact, not a configurable one, and the CHECK is where a new producer is
--                     forced to declare itself.
--   idempotency_key   the retry guard. UNIQUE where present, so "invoice this group again" and
--                     "re-run the warranty settlement" cannot mint a second document. NULL for the
--                     POS ring, whose idempotency lives on pos_sale.
--   closes_order      does this document END the order's billing? TRUE (default) reproduces today's
--                     behaviour exactly. FALSE marks a PROGRESS invoice under 0126's
--                     ON_DEMAND_SUBSET ruling — see the note on billing.OrderIDForInvoice below.
--   reverses_invoice_id  the document this memo reverses, when there is one. A warranty
--                     accommodation credit reverses nothing (no original invoice), so it is NULL.
--
-- ── THE REVERSING-ENTRY DISCIPLINE, ENFORCED IN THE SCHEMA ────────────────────────────────────
-- A credit memo is a NEGATIVE INVOICE. It is never a deleted invoice and never a mutated one: the
-- original document stays exactly as the customer received it, and a second document of the opposite
-- sign lands beside it. That is what makes AR reconstructible — "what did this account owe on
-- 2026-06-30" has one answer forever, rather than an answer that changes when somebody edits history.
--
-- The sign is enforced HERE rather than in Go, for the reason 0126 states about its coverage trigger:
-- a Go-side check binds Go callers, and the whole point of an invariant is that it binds the 2am psql
-- session too.
--
--   an INVOICE     has subtotal/tax/total/balance >= 0
--   a CREDIT_MEMO  has subtotal/tax/total/balance <= 0
--
-- ── WHY invoice_lines' NON-NEGATIVE CHECK IS SPLIT, NOT DROPPED ───────────────────────────────
-- 0021 added ck_invoice_lines_price_nonneg CHECK (unit_price_cents >= 0 AND line_total_cents >= 0).
-- A credit memo's lines mirror the original with the money negated, so line_total_cents must be
-- allowed below zero — but ONLY on a memo. The unit PRICE stays non-negative on every document (a
-- price is a price; the direction of the money is carried by the quantity and the line total), so
-- that half of the guard survives verbatim as its own constraint.
--
-- The line-sign-follows-the-header rule cannot be a CHECK (it spans two tables), so it is a
-- DEFERRABLE INITIALLY DEFERRED constraint trigger — the 0126 precedent, and deferred for the same
-- reason: a writer inserts the header and then the lines, and an immediate trigger would refuse the
-- header before its lines exist.

BEGIN;

-- ── 1. the document columns ────────────────────────────────────────────────────────────────────

ALTER TABLE invoices
    ADD COLUMN IF NOT EXISTS doc_type            TEXT NOT NULL DEFAULT 'INVOICE',
    ADD COLUMN IF NOT EXISTS source_kind         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS idempotency_key     TEXT,
    ADD COLUMN IF NOT EXISTS closes_order        BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS reverses_invoice_id UUID REFERENCES invoices(id) ON DELETE SET NULL;

ALTER TABLE invoices
    DROP CONSTRAINT IF EXISTS ck_invoices_doc_type;
ALTER TABLE invoices
    ADD CONSTRAINT ck_invoices_doc_type CHECK (doc_type IN ('INVOICE','CREDIT_MEMO'));

ALTER TABLE invoices
    DROP CONSTRAINT IF EXISTS ck_invoices_source_kind;
ALTER TABLE invoices
    ADD CONSTRAINT ck_invoices_source_kind
        CHECK (source_kind IN ('','POS','ORDER','MULTISHIP','WARRANTY_CREDIT','IMPORT'));

-- The sign discipline. Stated once, binding on every writer.
ALTER TABLE invoices
    DROP CONSTRAINT IF EXISTS ck_invoices_doc_sign;
ALTER TABLE invoices
    ADD CONSTRAINT ck_invoices_doc_sign CHECK (
        (doc_type = 'INVOICE'
            AND subtotal_cents >= 0 AND tax_cents >= 0
            AND total_cents    >= 0 AND balance_cents >= 0)
        OR
        (doc_type = 'CREDIT_MEMO'
            AND subtotal_cents <= 0 AND tax_cents <= 0
            AND total_cents    <= 0 AND balance_cents <= 0)
    );

-- A memo never reverses itself, and only a memo reverses anything.
ALTER TABLE invoices
    DROP CONSTRAINT IF EXISTS ck_invoices_reverses_is_memo;
ALTER TABLE invoices
    ADD CONSTRAINT ck_invoices_reverses_is_memo CHECK (
        reverses_invoice_id IS NULL
        OR (doc_type = 'CREDIT_MEMO' AND reverses_invoice_id <> id)
    );

-- The retry guard. Partial UNIQUE so the POS ring (NULL key) is unaffected.
CREATE UNIQUE INDEX IF NOT EXISTS uq_invoices_idempotency_key
    ON invoices(idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_invoices_doc_type_customer
    ON invoices(customer_id, doc_type);

COMMENT ON COLUMN invoices.closes_order IS
    'FALSE marks a 0126 ON_DEMAND_SUBSET progress invoice: the order is billed in part and its '
    'lifecycle must NOT advance to INVOICED yet. billing.OrderIDForInvoice filters on this, so the '
    'order module''s ar.invoice.finalized driver is untouched and the ruling lives in one place.';

-- ── 2. invoice_lines: unit price stays non-negative; the TOTAL follows the header ──────────────

ALTER TABLE invoice_lines
    DROP CONSTRAINT IF EXISTS ck_invoice_lines_price_nonneg;
ALTER TABLE invoice_lines
    ADD CONSTRAINT ck_invoice_lines_unit_price_nonneg CHECK (unit_price_cents >= 0);

CREATE OR REPLACE FUNCTION assert_invoice_line_sign()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    v_doc_type TEXT;
BEGIN
    SELECT doc_type INTO v_doc_type FROM invoices WHERE id = NEW.invoice_id;
    IF v_doc_type IS NULL THEN
        -- The header was removed inside this transaction (CASCADE); nothing to grade.
        RETURN NEW;
    END IF;
    IF v_doc_type = 'CREDIT_MEMO' AND NEW.line_total_cents > 0 THEN
        RAISE EXCEPTION
            'invoice_lines: a CREDIT_MEMO line must not carry a positive line total (invoice %, line %)',
            NEW.invoice_id, NEW.line_no
            USING ERRCODE = 'check_violation';
    END IF;
    IF v_doc_type = 'INVOICE' AND NEW.line_total_cents < 0 THEN
        RAISE EXCEPTION
            'invoice_lines: an INVOICE line must not carry a negative line total (invoice %, line %)',
            NEW.invoice_id, NEW.line_no
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_invoice_line_sign ON invoice_lines;
CREATE CONSTRAINT TRIGGER trg_invoice_line_sign
    AFTER INSERT OR UPDATE ON invoice_lines
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_invoice_line_sign();

-- ── 3. backfill the producer marker for rows that already exist ────────────────────────────────
--
-- Every invoice in the tree today came from the POS ring or from a fixture. Marking the ring's rows
-- 'POS' is what makes the two-producer split legible in a query; anything that cannot be attributed
-- keeps '' rather than being guessed at.
UPDATE invoices i
   SET source_kind = 'POS'
  FROM pos_sale ps
 WHERE ps.invoice_id = i.id AND i.source_kind = '';

COMMIT;
