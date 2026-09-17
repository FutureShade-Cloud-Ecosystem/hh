-- 0134_purchasing_currency.sql — DIB-421, Wave 5 Swarm C. The deferred coherent chunk from
-- REPORT-W4-C's PARK-P2, landed whole.
--
-- ── WHAT WAS PARKED, AND WHY IT WAS PARKED RATHER THAN BODGED ────────────────────────────────────
-- Wave 4 shipped purchasing CAD-only. Not because a foreign purchase order is rare — Dibbits buys
-- stone and hardscape product from the US constantly — but because making one enterable takes THREE
-- changes that only work together, and one of them lives in another package's fence:
--
--   1. the columns (here),
--   2. the DX-19 invariant allowlist in internal/platform/db/invariants_integration_test.go, which
--      asserts that NO table outside {payment_config, purchase_orders, landed_costs} carries a
--      `currency` column, and
--   3. the toCAD boundary conversion in purchasing/service.go.
--
-- Doing (1) alone turns the union's full parallel suite red. Doing (2) alone weakens a real invariant
-- for nothing. Doing (3) alone converts a column that does not exist. W4-C did none of them and wrote
-- the prescription down instead. This is that prescription, verbatim.
--
-- ── WHAT THE INVARIANT ACTUALLY PROTECTS, AND WHY WIDENING IT IS NOT WEAKENING IT ────────────────
-- DX-19's rule is "the GL and payments are CAD". It is enforced by a whole-DB assertion that no
-- unexpected table grows a currency column, precisely so that a foreign amount cannot creep into a
-- money path by accident. The exception list is a list of DOCUMENTS — things a vendor sends us that
-- are denominated in their money and get converted at OUR boundary. A purchase order and a vendor
-- bill are exactly that kind of document, and they were on the list in spirit from the beginning:
-- 0011's purchase_orders (the receiving document) has carried currency + fx_rate all along, and
-- internal/inventory has always converted it at receipt. The two purchasing tables are the same
-- documents in the module that now owns them.
--
-- The invariant's teeth are unchanged: gl_journals and gl_journal_lines still may not carry a
-- currency column, and the test still asserts that.
--
-- ── fx_rate LIVES ON THE DOCUMENT, NOT IN A RATE TABLE ──────────────────────────────────────────
-- The rate that matters is the one in force when the document was agreed, and it must not move
-- afterwards. A shared rate table re-read at receipt time would silently re-cost a PO placed in March
-- at September's rate, and the stock ledger would disagree with the purchase order for reasons nobody
-- could reconstruct. Same reasoning as landed_costs, which has stored its own rate since 0011.
--
-- NUMERIC(12,6): six decimal places is what a bank quote carries. CHECK (fx_rate > 0) because a zero
-- or negative rate makes every converted cost zero or negative, and a zero-cost receipt silently
-- destroys a weighted average.
--
-- ── DEFAULT 'CAD' / 1.0 IS A BACKFILL AND A CONTRACT ─────────────────────────────────────────────
-- Every row that exists today was created CAD by construction (Wave 4 wrote no other value), so the
-- default backfills correctly and is not a guess. It is also the contract for every caller that never
-- names a currency: they keep getting exactly the behaviour they have now, at fx 1.0, where the
-- conversion is the identity.

BEGIN;

-- ── the purchase order ──────────────────────────────────────────────────────────────────────────
ALTER TABLE purchasing_pos
  ADD COLUMN currency CITEXT NOT NULL DEFAULT 'CAD' CHECK (currency IN ('CAD','USD')),
  ADD COLUMN fx_rate  NUMERIC(12,6) NOT NULL DEFAULT 1.0 CHECK (fx_rate > 0);

-- A CAD document may not carry a rate other than 1.0. Without this a clerk can leave 1.37 in the box
-- after switching the currency back, and every line silently costs 37% more with nothing on screen to
-- explain it. The service normalizes to the same rule (normalizeCurrency); this is the floor under it.
ALTER TABLE purchasing_pos
  ADD CONSTRAINT purchasing_pos_cad_is_unity
  CHECK (currency <> 'CAD' OR fx_rate = 1.0);

-- ── the vendor bill ─────────────────────────────────────────────────────────────────────────────
-- A bill gets its OWN currency and rate rather than inheriting the PO's, because it is a separate
-- document that arrives later. The vendor may invoice a USD purchase order in CAD, and the rate on
-- the invoice is the rate the vendor used — not the one we agreed in March. Inheriting would make the
-- three-way match compare two numbers converted at different rates while claiming they are the same
-- currency, which is the most expensive kind of quiet wrong.
ALTER TABLE purchasing_bills
  ADD COLUMN currency CITEXT NOT NULL DEFAULT 'CAD' CHECK (currency IN ('CAD','USD')),
  ADD COLUMN fx_rate  NUMERIC(12,6) NOT NULL DEFAULT 1.0 CHECK (fx_rate > 0);

ALTER TABLE purchasing_bills
  ADD CONSTRAINT purchasing_bills_cad_is_unity
  CHECK (currency <> 'CAD' OR fx_rate = 1.0);

COMMENT ON COLUMN purchasing_pos.currency IS
  'DX-19 document currency. Line unit costs are stored IN THIS CURRENCY; the toCAD boundary '
  'conversion (purchasing/service.go) happens when a receipt, a receipt value or a match input is '
  'produced. The GL never sees a foreign amount.';
COMMENT ON COLUMN purchasing_pos.fx_rate IS
  'Rate in force when the PO was agreed, frozen on the document. Never re-read from a rate table: a '
  'March PO must not re-cost at September''s rate.';
COMMENT ON COLUMN purchasing_bills.currency IS
  'The currency the VENDOR invoiced in, which need not be the PO''s. Converted to CAD before the '
  'three-way match runs, so the match never compares two currencies.';
COMMENT ON COLUMN purchasing_bills.fx_rate IS
  'Rate on the vendor''s invoice, frozen on the bill.';

COMMIT;
