-- 0470_ar_aging_net_of_holdback.sql (wave 3 RT-2)
--
-- An invoice that withholds holdback carries the withheld part in holdback_cents and still carries the
-- whole amount in balance_cents (the payment doors read balance less holdback as what can be paid). The
-- books split the document: GL 1100 holds the net the customer owes now and GL 1150 holds the holdback.
-- The aging therefore reads the NET due, balance less holdback, so its total ties to 1100; the holdback
-- is reported separately through holdback_cents, as the holdback readers already do.
--
-- Only the invoice arm changes (here and in ai.ar_aging below). The view keeps its name and its nine columns in the same order, and a
-- document with no holdback reads exactly as before. A credit memo against a withholding invoice lowers
-- that invoice's own holdback_cents by the part it takes back, and carries that part as a NEGATIVE
-- holdback_cents of its own as a record; only a positive holdback is ever netted, so the memo's own line
-- is its credit and nothing more.

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
         i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0)) AS amount_cents, i.due_date AS age_date
  FROM invoices i
  WHERE i.status IN ('FINALIZED','PARTIALLY_PAID')
    AND i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0)) <> 0
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

-- The assistant's receivables view reads the same net (0444's union, replaced under its owner so its
-- ownership and the reader's grant stay): it must follow the aging screen to the cent.
DO $rt2_net$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_reader    text;
BEGIN
  norm := translate(db, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz');
  norm := regexp_replace(norm, '[^a-z0-9]', '_', 'g');
  IF norm = db AND length(norm) <= 30 THEN
    suffix := norm;
  ELSE
    suffix := left(norm, 21) || '_' || left(md5(db), 8);
  END IF;
  v_view   := 'assist_view_owner_' || suffix;
  v_reader := 'assist_reader_'     || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_view) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;
  IF to_regprocedure('ai.dealer_zone()') IS NULL THEN
    RAISE EXCEPTION 'migration 0444 has not created ai.dealer_zone()';
  END IF;

  -- The view's owner already reads invoices.holdback_cents (0427 granted it with the other columns).
  EXECUTE format('SET LOCAL ROLE %I', v_view);

  CREATE OR REPLACE VIEW ai.ar_aging AS
    WITH open_ar AS (
      SELECT i.customer_id,
             i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0)) AS balance_cents,
             i.due_date
        FROM public.invoices i
       WHERE i.status IN ('FINALIZED', 'PARTIALLY_PAID')
         AND i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0)) <> 0
      UNION ALL
      SELECT ct.customer_id,
             SUM(ct.amount_cents) AS balance_cents,
             MIN((ct.occurred_at AT TIME ZONE ai.dealer_zone())::date) AS due_date
        FROM public.customer_transactions ct
       WHERE ct.txn_type IN ('NSF', 'ADJUSTMENT', 'CREDIT_MEMO', 'WRITE_OFF')
         AND ct.source_type <> 'invoice'
         AND ((SELECT ai.claims()).global OR ct.branch_id = ANY ((SELECT ai.claims()).branch_ids))
       GROUP BY ct.customer_id, ct.branch_id, ct.source_type, COALESCE(ct.source_ref_id, ct.id)
      HAVING SUM(ct.amount_cents) <> 0
    )
    SELECT c.id AS customer_id,
           COALESCE(c.name, '') AS customer_name,
           COALESCE(c.account_no::text, '') AS account_no,
           d.today AS as_of,
           COALESCE(SUM(o.balance_cents) FILTER (WHERE d.today <= o.due_date), 0)::bigint AS current_cents,
           COALESCE(SUM(o.balance_cents) FILTER (WHERE d.today >  o.due_date AND d.today <= o.due_date + 30), 0)::bigint AS days_1_30_cents,
           COALESCE(SUM(o.balance_cents) FILTER (WHERE d.today >  o.due_date + 30 AND d.today <= o.due_date + 60), 0)::bigint AS days_31_60_cents,
           COALESCE(SUM(o.balance_cents) FILTER (WHERE d.today >  o.due_date + 60 AND d.today <= o.due_date + 90), 0)::bigint AS days_61_90_cents,
           COALESCE(SUM(o.balance_cents) FILTER (WHERE d.today >  o.due_date + 90), 0)::bigint AS days_90_plus_cents,
           COALESCE(SUM(o.balance_cents), 0)::bigint AS open_cents
      FROM public.customers c
      JOIN open_ar o ON o.customer_id = c.id
      CROSS JOIN LATERAL (SELECT ai.dealer_day() AS today) d
     GROUP BY c.id, c.name, c.account_no, d.today
    HAVING COALESCE(SUM(o.balance_cents), 0) <> 0;

  EXECUTE format('GRANT SELECT ON ai.ar_aging TO %I', v_reader);
  RESET ROLE;
END
$rt2_net$;

-- The retention ledger is the one dated source of an invoice's holdback (RT-2 fix round 2).
--
-- invoice_holdback_as_of(invoice, instant) is what the invoice still withheld at that instant: its
-- RETENTION rows (positive at the withholding, negative for a credit memo's part, a write off's part,
-- and positive again for a write off's reversal) less its RELEASE rows (RT-3), every row counted from
-- its own created_at. It is owned by lien, the ledger's module, and it is the one thing the dated
-- readers call (the aging, the AR balance behind the 1100 tie, the finance charge, the statement), so
-- no reader rebuilds a past holdback by hand and RT-3's release reaches every one of them by writing
-- its row. An invoice with no ledger row withheld nothing and answers zero.
--
-- NOTE FOR RT-3: the release must write one RELEASE row per invoice (never one job level row with no
-- invoice_id, which this function cannot see) and lower invoices.holdback_cents in the same call on the
-- same transaction, through a writer or repository that carries the seam (billing.ErrNoHoldbackSeam is
-- the refusal a bare one answers). The today branch of every reader reads the column and the past branch
-- reads this function, so a release that moves only one of them splits the two.
--
-- invoice_holdback_withheld(invoice) is the holdback the invoice was first written with: its first
-- positive RETENTION row, whatever later lowered it or restored it (a write off's reversal is a later
-- positive row and is not a second withholding). NULL when the ledger never held the invoice.
CREATE OR REPLACE FUNCTION invoice_holdback_as_of(p_invoice_id uuid, p_as_of timestamptz) RETURNS bigint
LANGUAGE sql STABLE AS $$
  SELECT GREATEST(COALESCE(SUM(CASE l.entry_type WHEN 'RETENTION' THEN l.amount_cents ELSE -l.amount_cents END), 0), 0)::bigint
    FROM holdback_ledger l
   WHERE l.invoice_id = p_invoice_id AND l.created_at <= p_as_of
$$;

CREATE OR REPLACE FUNCTION invoice_holdback_withheld(p_invoice_id uuid) RETURNS bigint
LANGUAGE sql STABLE AS $$
  SELECT l.amount_cents::bigint
    FROM holdback_ledger l
   WHERE l.invoice_id = p_invoice_id AND l.entry_type = 'RETENTION' AND l.amount_cents > 0
   ORDER BY l.created_at, l.id
   LIMIT 1
$$;

-- The reader's index: one invoice's rows by date.
CREATE INDEX IF NOT EXISTS idx_holdback_ledger_invoice ON holdback_ledger(invoice_id, created_at);
