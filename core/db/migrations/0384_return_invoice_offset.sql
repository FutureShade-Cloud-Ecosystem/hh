-- 0384_return_invoice_offset.sql
--
-- Item 10, the principal's ruling on an order that is not fully paid (FROM-PRINCIPAL 47): a refund goes
-- back in cash or card only up to what was actually paid on the order's invoice; the rest is account
-- credit that offsets the open invoice. The server decides the split.
--
-- pos_returns.offset_cents is the part of a return that reduced the order's open invoice instead of
-- going out on a rail: the split's account part on a CASH or CARD return, and the invoice offset the
-- account rail made on an ACCOUNT_CREDIT return. What the invoice has really taken in money is its
-- total, less its balance, less these offsets, less what earlier returns already paid out in cash or
-- card. Returns made before this migration carry zero here (their offsets cannot be reconstructed).
--
-- Owner module: internal/pos.

ALTER TABLE pos_returns
  ADD COLUMN offset_cents BIGINT NOT NULL DEFAULT 0 CHECK (offset_cents >= 0),
  ADD CONSTRAINT pos_returns_offset_within_total CHECK (applied_cents + offset_cents <= total_cents);
