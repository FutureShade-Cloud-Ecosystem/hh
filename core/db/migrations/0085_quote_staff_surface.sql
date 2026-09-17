-- 0085: the staff quote surface (DIB-56, bundle W10-A).
--
-- Why. `quotes`/`quote_lines` have existed since 0008, but ONLY as the portal read+convert side: there
-- is no quote-create path anywhere in Go (every `INSERT INTO quotes` in the tree is a seed or a test
-- fixture), no DRAFT→SENT edge, and every service method is hard customer-scoped (`customer_id = $1` in
-- every statement, including the FOR UPDATE lock). Staff cannot build, send or edit a quote at all.
-- This migration adds the two columns/indexes that surface needs; the behaviour lands in Go.
--
-- Deliberately NOT changed:
--
--   * The status CHECK (0008:13) — DRAFT|SENT|ACCEPTED|CONVERTED|EXPIRED|LOST already covers the
--     ratified matrix. R2's "REJECTED" is this schema's LOST (D3); no new value is needed.
--   * `valid_until` — expiry is enforced in Go at guard time (D2), computed from this column. No
--     EXPIRED status is ever written and no historical row is backfilled, so a quote's stored status
--     keeps meaning exactly what it meant before this migration.
--   * `tax_cents` — M5 owns tax. The edit path recomputes subtotal/total from Σ line_total_cents and
--     reads tax_cents without ever writing it, mirroring the order edit path (0078/DIB-175).

-- Scrubname0033 last edited the quote. The 0008 table records created_by but has no updated_by, so the staff
-- surface had no column for the actor of an edit. The full who/what/when trail lives in audit_log
-- (QUOTE_CHANGED rows, seq-ordered); this is the cheap "last touched by" for the list view, not the
-- audit trail itself.
ALTER TABLE quotes ADD COLUMN updated_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- Parity with order_lines (0008:95 idx_order_lines_product). quote_lines has carried a product_id FK
-- since 0008 with no supporting index; the builder's per-product reads and the DIB-59 comparison both
-- scan by product.
CREATE INDEX idx_quote_lines_product ON quote_lines (product_id);
