-- 0037: W6 INV-CORE — the minimal inventory spine sales/counts/landed-cost build on. Owner: internal/inventory.
-- Conventions: money BIGINT *_cents · quantities DECIMAL(19,4) · TEXT+CHECK (no ENUMs) · TIMESTAMPTZ · CAD only.
-- stock_moves + stock_balances already exist (0005): this migration ADDS the append-only enforcement, the
-- weighted-average-cost + on-hand READ MODEL the COGS basis reads from, and RELAXES the negative-stock CHECK
-- so a sell-before-receipt surfaces as negative on-hand (by-design detection, Q-009 — opening balances are
-- zero until an A-15 count). No lot/serial; branch-grain; a nullable bin column for a future put-away.
--
-- DX-01 (stock-on-sale) is PARTIAL at W6: only a COLLECT_NOW POS sale posts a SHIP move + COGS; a
-- COLLECT_LATER/DELIVERED line records ZERO moves and is quantified on the pending-fulfilment surface.

-- 1. Append-only discipline. stock_moves is an immutable ledger (0005 doctrine: "corrections = compensating
--    moves"). A row may be INSERTed and, for test/GC hygiene, DELETEd, but NEVER mutated in place — an UPDATE
--    is a loud error so a posted cost/qty can never be silently rewritten under a settled COGS journal.
CREATE OR REPLACE FUNCTION stock_moves_no_update() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'stock_moves is append-only — a posted move cannot be updated (post a compensating move)';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_stock_moves_no_update ON stock_moves;
CREATE TRIGGER trg_stock_moves_no_update
  BEFORE UPDATE ON stock_moves
  FOR EACH ROW EXECUTE FUNCTION stock_moves_no_update();

-- 2. Optional bin (put-away hint; NULL = branch default location). Additive, nullable — no backfill.
ALTER TABLE stock_moves   ADD COLUMN IF NOT EXISTS bin_code TEXT;
ALTER TABLE stock_balances ADD COLUMN IF NOT EXISTS bin_code TEXT;

-- 3. Relax the negative-stock CHECK. stock_balances (0005) carried CHECK (allocated_base >= 0) AND
--    CHECK (on_hand_base >= allocated_base). The second blocks negative on-hand, but a counter sale before
--    the receipt is booked MUST be allowed to drive on-hand negative (the detection signal). Drop every CHECK
--    on the table (unnamed → discovered), then re-add ONLY the allocated-non-negative invariant, named so the
--    gate-suite can assert it. on_hand_base is now free to go negative.
DO $$
DECLARE c record;
BEGIN
  FOR c IN
    SELECT conname FROM pg_constraint
    WHERE conrelid = 'stock_balances'::regclass AND contype = 'c'
  LOOP
    EXECUTE format('ALTER TABLE stock_balances DROP CONSTRAINT %I', c.conname);
  END LOOP;
END $$;

ALTER TABLE stock_balances
  ADD CONSTRAINT stock_balances_allocated_nonneg CHECK (allocated_base >= 0);

-- 4. On-hand read model: available = on-hand − allocated (may be negative on-hand; available never lies).
CREATE OR REPLACE VIEW v_stock_on_hand AS
SELECT sb.product_id,
       sb.location_id,
       sb.branch_id,
       sb.on_hand_base,
       sb.allocated_base,
       (sb.on_hand_base - sb.allocated_base) AS available_base,
       sb.bin_code,
       sb.updated_at
FROM stock_balances sb;

-- 5. Weighted-average unit cost read model, derived from the inbound legs of the move ledger (RECEIPT /
--    RETURN / positive ADJUST — anything with a to_location and a booked value). avg = Σvalue / Σqty, rounded
--    to the cent. INV-CORE's real service maintains product_branch_costs (0005) transactionally; this view is
--    the ledger-derived cross-check + the scaffold cost basis (returns nothing → found=false semantics, so the
--    COGS leg stays dormant, byte-identical to gl.NoopCostBasis until INV-CORE fills the bodies).
CREATE OR REPLACE VIEW v_product_weighted_avg_cost AS
SELECT product_id,
       branch_id,
       CASE WHEN SUM(qty_base) > 0
            THEN ROUND(SUM(COALESCE(value_cents, unit_cost_cents * qty_base, 0)) / SUM(qty_base))::BIGINT
            ELSE 0 END AS avg_unit_cost_cents,
       SUM(qty_base) AS received_qty_base
FROM stock_moves
WHERE to_location_id IS NOT NULL
GROUP BY product_id, branch_id;
