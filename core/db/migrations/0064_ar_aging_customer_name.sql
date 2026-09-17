-- 0064: T2.2 — AR Aging showed a raw customer UUID under a "Customer" header. Add the customer NAME
-- to v_ar_aging so the report reads for a human (FE falls back to the short id when name is null).
-- Purely ADDITIVE: CREATE OR REPLACE VIEW keeps all 8 existing columns in the same order and appends
-- customer_name last; a LEFT JOIN on the customers PK is 1:1 (no row multiplication, no total drift),
-- so billing's ARBalanceCents subledger tie-out (amount_cents unchanged) is untouched.
CREATE OR REPLACE VIEW v_ar_aging AS
SELECT
  src.customer_id,
  src.source_type,
  src.source_ref_id,
  src.branch_id,
  src.amount_cents,
  src.age_date,
  GREATEST(CURRENT_DATE - src.age_date, 0) AS age_days,
  CASE
    WHEN CURRENT_DATE - src.age_date <= 0  THEN 'CURRENT'
    WHEN CURRENT_DATE - src.age_date <= 30 THEN '1_30'
    WHEN CURRENT_DATE - src.age_date <= 60 THEN '31_60'
    WHEN CURRENT_DATE - src.age_date <= 90 THEN '61_90'
    ELSE '90_PLUS'
  END AS bucket,
  c.name AS customer_name
FROM (
  -- open AR carried on invoices
  SELECT i.customer_id, 'invoice'::text AS source_type, i.id AS source_ref_id, i.branch_id,
         i.balance_cents AS amount_cents, i.due_date AS age_date
  FROM invoices i
  WHERE i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.balance_cents <> 0
  UNION ALL
  -- non-invoice AR movements (kept out of the invoices table): NSF fee, adjustment, credit memo, write-off
  SELECT ct.customer_id, 'customer_txn'::text AS source_type, ct.id AS source_ref_id, ct.branch_id,
         ct.amount_cents, ct.created_at::date AS age_date
  FROM customer_transactions ct
  WHERE ct.txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND ct.source_type <> 'invoice'
) src
LEFT JOIN customers c ON c.id = src.customer_id;
