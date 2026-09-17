-- 0021: post-W2-audit schema invariants (M-2, L-2, L-6, L-8).
-- These are detective/preventive guards on the financial + catalogue scaffold so a
-- future W3/W4 service writer cannot silently violate the doctrine the data model
-- already promises. No data is rewritten; the existing seed satisfies every check.

-- ── M-2: GL journals must balance when POSTED ────────────────────────────────
-- The per-line CHECK (0007) only enforces one-sided positivity; nothing stopped an
-- UNBALANCED journal (Σdebits ≠ Σcredits). Enforce double-entry at POST time with a
-- DEFERRABLE constraint trigger, so a journal + its lines can be written in one tx
-- and the balance is asserted at COMMIT (matches the "entry in the same transaction
-- as its source document" doctrine).
CREATE OR REPLACE FUNCTION assert_gl_journal_balanced() RETURNS trigger AS $$
DECLARE total_debit BIGINT; total_credit BIGINT;
BEGIN
  IF NEW.status = 'POSTED' THEN
    SELECT COALESCE(SUM(debit_cents), 0), COALESCE(SUM(credit_cents), 0)
      INTO total_debit, total_credit
      FROM gl_journal_lines WHERE journal_id = NEW.id;
    IF total_debit = 0 AND total_credit = 0 THEN
      RAISE EXCEPTION 'gl journal % posted with no lines', NEW.id USING ERRCODE = 'check_violation';
    END IF;
    IF total_debit <> total_credit THEN
      RAISE EXCEPTION 'gl journal % is unbalanced: debits=% credits=%',
        NEW.id, total_debit, total_credit USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_gl_journal_balanced
  AFTER INSERT OR UPDATE ON gl_journals
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_gl_journal_balanced();

-- ── M-2: stock balance ↔ ledger reconciliation (detective) ───────────────────
-- stock_balances is an app-maintained projection with no hard tie to stock_moves.
-- This view surfaces any drift between the projection and the movement ledger so
-- monitoring/tests can assert "zero drift". (Auto-maintenance by trigger lands with
-- the W3 inventory service that becomes the sole writer; enforcing it now would
-- fight the set-based seed loader.)
CREATE OR REPLACE VIEW v_stock_balance_drift AS
WITH ledger AS (
  SELECT product_id,
         COALESCE(to_location_id, from_location_id) AS location_id,
         SUM(CASE WHEN to_location_id   IS NOT NULL THEN qty_base ELSE 0 END)
       - SUM(CASE WHEN from_location_id IS NOT NULL THEN qty_base ELSE 0 END) AS net_base
  FROM stock_moves
  GROUP BY product_id, COALESCE(to_location_id, from_location_id)
)
SELECT COALESCE(b.product_id, l.product_id)   AS product_id,
       COALESCE(b.location_id, l.location_id) AS location_id,
       COALESCE(b.on_hand_base, 0)            AS projected_on_hand,
       COALESCE(l.net_base, 0)                AS ledger_net,
       COALESCE(b.on_hand_base, 0) - COALESCE(l.net_base, 0) AS drift
FROM stock_balances b
FULL OUTER JOIN ledger l
  ON b.product_id = l.product_id AND b.location_id = l.location_id
WHERE COALESCE(b.on_hand_base, 0) <> COALESCE(l.net_base, 0);

-- ── L-2: line prices and totals may not be negative ──────────────────────────
-- DEFAULT 0 stays (a genuinely free line is legal); negatives never are. Closes the
-- "silent negative line" revenue-leakage vector before the W3 writers exist.
ALTER TABLE quote_lines   ADD CONSTRAINT ck_quote_lines_price_nonneg   CHECK (unit_price_cents >= 0 AND line_total_cents >= 0);
ALTER TABLE order_lines   ADD CONSTRAINT ck_order_lines_price_nonneg   CHECK (unit_price_cents >= 0 AND line_total_cents >= 0);
ALTER TABLE invoice_lines ADD CONSTRAINT ck_invoice_lines_price_nonneg CHECK (unit_price_cents >= 0 AND line_total_cents >= 0);

-- ── L-6: disambiguate the two is_special_order flags ─────────────────────────
-- They are legitimately different concepts; the names collided. Document the
-- distinct semantics so neither is mistaken for the other.
COMMENT ON COLUMN products.is_special_order IS
  'CATALOGUE attribute: this product is always sold as a special / non-stock order (from udfSpecialOrder).';
COMMENT ON COLUMN order_lines.is_special_order IS
  'LINE attribute: this particular order line is being placed as a special order, independent of the product''s catalogue flag.';

-- ── L-8: drop the dead texture index ─────────────────────────────────────────
-- texture is promoted + editable but is NOT a queryable facet (not in the product
-- Filter), so this index is never used. Drop it; re-add if texture becomes a facet.
DROP INDEX IF EXISTS idx_products_texture;
