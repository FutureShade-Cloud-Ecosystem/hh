-- 0376_ar_aging_house_day.sql
--
-- The AR aging view's day is the dealer's, not the database session's. 0034 built v_ar_aging and
-- 0064 re-stated it with every age computed from CURRENT_DATE, which names the session TimeZone's
-- day (UTC on every deployment this house runs): between the UTC day change and the house zone's
-- own, four or five hours each Ontario evening, every open item aged one day more than the
-- dealer's wall clock says, enough to cross a 30/60/90 bucket edge an evening early.
--
-- THE ONE CHOICE, NAMED: the house day is derived in SQL from the session clock converted to the
-- house zone,
--
--     (now() AT TIME ZONE 'America/Toronto')::date
--
-- and that one expression is used by every SQL-side aging computation: this view and billing's
-- aging sync projection (internal/billing/syncscope.go). A view cannot receive a bound parameter,
-- so deriving in SQL is the only shape the view and the projection can share, and it is the same
-- zone and the same day the Go side decides and binds through platform/houseday (the invoice
-- writers' issue and due days), so the writer and every reader of the row name one day.
--
-- The customer_transactions arm's age_date moves to the same zone for the same reason: created_at
-- is an instant, and casting it at the session zone filed an evening adjustment under tomorrow.
--
-- The view keeps all nine columns of 0064 in the same order; only the day every age is counted
-- from changes. invoices.issue_date's column default stays as it is: the Go writers bind the
-- house day themselves, and the default cannot be dropped while fenced packages' test inserts
-- still rely on it (testaudit's invoice writer census holds the writers to the binding).

CREATE OR REPLACE VIEW v_ar_aging AS
SELECT
  src.customer_id,
  src.source_type,
  src.source_ref_id,
  src.branch_id,
  src.amount_cents,
  src.age_date,
  GREATEST((now() AT TIME ZONE 'America/Toronto')::date - src.age_date, 0) AS age_days,
  CASE
    WHEN (now() AT TIME ZONE 'America/Toronto')::date - src.age_date <= 0  THEN 'CURRENT'
    WHEN (now() AT TIME ZONE 'America/Toronto')::date - src.age_date <= 30 THEN '1_30'
    WHEN (now() AT TIME ZONE 'America/Toronto')::date - src.age_date <= 60 THEN '31_60'
    WHEN (now() AT TIME ZONE 'America/Toronto')::date - src.age_date <= 90 THEN '61_90'
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
         ct.amount_cents, (ct.created_at AT TIME ZONE 'America/Toronto')::date AS age_date
  FROM customer_transactions ct
  WHERE ct.txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND ct.source_type <> 'invoice'
) src
LEFT JOIN customers c ON c.id = src.customer_id;
