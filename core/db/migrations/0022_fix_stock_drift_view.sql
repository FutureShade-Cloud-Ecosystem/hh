-- 0022: correct v_stock_balance_drift for TRANSFER moves (L8 review nit).
-- The 0021 view grouped by COALESCE(to_location_id, from_location_id) and netted
-- to/from within one group, so a TRANSFER (both locations set) collapsed onto a
-- single location — the destination netted to zero and the source was never debited.
-- The seed is RECEIPT-only, so the original test passed vacuously; this breaks the
-- moment the W3 inventory/transfer service writes its first TRANSFER. Fix: split each
-- move into a +qty leg at the destination and a -qty leg at the source (UNION ALL),
-- then aggregate per (product, location).

CREATE OR REPLACE VIEW v_stock_balance_drift AS
WITH ledger AS (
  SELECT product_id, location_id, SUM(delta) AS net_base
  FROM (
    SELECT product_id, to_location_id   AS location_id,  qty_base AS delta
      FROM stock_moves WHERE to_location_id   IS NOT NULL
    UNION ALL
    SELECT product_id, from_location_id AS location_id, -qty_base AS delta
      FROM stock_moves WHERE from_location_id IS NOT NULL
  ) legs
  GROUP BY product_id, location_id
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

-- NOTE (W3): once a GL service writes journals, add gl_journal_lines immutability
-- for POSTED journals (or VOID + reversing entries). Deferred here because the
-- single-transaction "insert POSTED journal + its lines" pattern (supported by the
-- DEFERRABLE balance trigger in 0021) has no application writer yet.
