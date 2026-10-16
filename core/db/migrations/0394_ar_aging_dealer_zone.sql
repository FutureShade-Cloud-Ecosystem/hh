-- 0394_ar_aging_dealer_zone.sql
--
-- The AR aging view's day is the DEALER'S zone, not Ontario's. 0376 derived every age from
-- (now() AT TIME ZONE 'America/Toronto')::date, which is right for an Ontario dealer and wrong for a
-- Pacific one: at 22:00 in Los Angeles on an invoice's due date billing (which binds the dealer's
-- business_zone) says current while finstmt's AR aging and the two reporting aging reports, which read
-- this view, said one day past due, because Toronto is already on the next day.
--
-- THE CHOICE, NAMED: a SQL function reads the dealer's zone and the view takes its day from it. The
-- other shape (the view becomes a function of the zone, the Go readers pass it) was refused: it would
-- change the reading SQL of finstmt, the reporting catalog and the saved report templates (0143), for
-- the same answer. The view keeps its name and its nine columns in the same order.
--
-- settings_business_zone() belongs to the settings module, which owns system_settings and its
-- business_zone key (internal/settings/dealer_config.go). It answers what the Go reader answers: the
-- stored zone when it is a JSON string naming a zone the database knows, otherwise the Ontario default
-- (the key absent, blank, not a string, "Local" or unknown). A dealer with no row is the Ontario dealer
-- exactly as before. The function validates by using the name, so a name the view could not use never
-- reaches it, and it needs no scan of pg_timezone_names.
--
-- STABLE: the view evaluates it once per statement (one row CTE joined to the union), so every row of
-- one query is aged against the same day, and there is no per row lookup. No cache: a zone change is
-- live at the next read, like billing's own aging reads.
--
-- The customer_transactions arm's age_date moves to the dealer's zone for the same reason as in 0376:
-- created_at is an instant and an evening adjustment is filed under the dealer's day.

CREATE OR REPLACE FUNCTION settings_business_zone() RETURNS text
LANGUAGE plpgsql STABLE AS $$
DECLARE
  z text;
BEGIN
  SELECT btrim(s.value #>> '{}') INTO z
    FROM system_settings s
   WHERE s.key = 'business_zone' AND jsonb_typeof(s.value) = 'string';
  IF z IS NULL OR z = '' OR z = 'Local' THEN
    RETURN 'America/Toronto';
  END IF;
  BEGIN
    PERFORM now() AT TIME ZONE z;
  EXCEPTION WHEN invalid_parameter_value THEN
    RETURN 'America/Toronto';
  END;
  RETURN z;
END
$$;

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
  -- open AR carried on invoices
  SELECT i.customer_id, 'invoice'::text AS source_type, i.id AS source_ref_id, i.branch_id,
         i.balance_cents AS amount_cents, i.due_date AS age_date
  FROM invoices i
  WHERE i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.balance_cents <> 0
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
