-- 0470_ar_aging_net_of_holdback.sql (wave 3 RT-2)
--
-- An invoice that withholds holdback carries the withheld part in holdback_cents and still carries the
-- whole amount in balance_cents (the payment doors read balance less holdback as what can be paid). The
-- books split the document: GL 1100 holds the net the customer owes now and GL 1150 holds the holdback.
-- The aging therefore reads the NET due, balance less holdback, so its total ties to 1100; the holdback
-- is reported separately through holdback_cents, as the holdback readers already do.
--
-- Only the invoice arm changes. The view keeps its name and its nine columns in the same order, and a
-- document with no holdback reads exactly as before. A credit memo against a withholding invoice carries
-- a NEGATIVE holdback_cents, so its net is its credit less the holdback part it takes back.

CREATE OR REPLACE VIEW v_ar_aging AS
WITH dealer AS (
  SELECT z.zone, (now() AT TIME ZONE z.zone)::date AS today
    FROM (SELECT settings_business_zone() AS zone) z
)
SELECT
  src.customer_id,
  src.source_type,
  src.source_ref_id,
  src.branch_id,
  src.amount_cents,
  src.age_date,
  GREATEST(dealer.today - src.age_date, 0) AS age_days,
  CASE
    WHEN dealer.today - src.age_date <= 0  THEN 'CURRENT'
    WHEN dealer.today - src.age_date <= 30 THEN '1_30'
    WHEN dealer.today - src.age_date <= 60 THEN '31_60'
    WHEN dealer.today - src.age_date <= 90 THEN '61_90'
    ELSE '90_PLUS'
  END AS bucket,
  c.name AS customer_name
FROM (
  -- open AR carried on invoices, net of the holdback the customer does not owe yet
  SELECT i.customer_id, 'invoice'::text AS source_type, i.id AS source_ref_id, i.branch_id,
         i.balance_cents - i.holdback_cents AS amount_cents, i.due_date AS age_date
  FROM invoices i
  WHERE i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.balance_cents - i.holdback_cents <> 0
  UNION ALL
  -- non-invoice AR movements (kept out of the invoices table): NSF fee, adjustment, credit memo, write-off
  SELECT ct.customer_id, 'customer_txn'::text AS source_type, ct.id AS source_ref_id, ct.branch_id,
         ct.amount_cents, (ct.created_at AT TIME ZONE dz.zone)::date AS age_date
  FROM customer_transactions ct
  CROSS JOIN dealer dz
  WHERE ct.txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND ct.source_type <> 'invoice'
) src
CROSS JOIN dealer
LEFT JOIN customers c ON c.id = src.customer_id;
