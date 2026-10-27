-- 0444: ai.ar_aging reads the same union the aging screen reads (FW1-06a round 4, the review's R3-P1-1).
--
-- The assistant's receivables view summed open invoices only, so for an account whose only open AR is an NSF
-- fee it said nothing was owed while the aging screen showed the fee. The view now unions the non-invoice AR
-- rows of customer_transactions the same way billing's agingSelect (credit_repository.go), the projection the
-- aging screen and the auto-hold rule read, does: the counted transaction types, grouped by source so a paid
-- fee nets to zero on its original date, each group aged from its earliest dealer's day. The column set and
-- the reader's grant stay exactly what 0427 gave them.
--
-- The branch reach: the invoice arm answers through the invoices row policy (0427), so the view ages only
-- what the asker could read on the screen. customer_transactions has no row policy, so the non-invoice arm
-- carries the same reach inside the view through the asker's own claims, the way the payments view masks its
-- cells by them. An account that lives partly elsewhere ages for the reach alone, as on the screen.
--
-- The zone: the screen ages a non-invoice group from its earliest dealer's day (0394's one zone rule). The
-- zone name reaches the view the way the day does (0427): ai.dealer_zone(), a SECURITY DEFINER function under
-- the zone reader, the one role that can read the business zone setting and run the settings module's zone
-- rule. It gives the reader the zone name and nothing else. A function called in a view is checked against
-- the caller, as ai.claims() and ai.dealer_day() are, so the reader holds EXECUTE and the catalogue test's
-- list names it beside ai.dealer_day(). The CREATE and USAGE on schema ai that the creation needs are granted
-- here and revoked before the file ends, so the zone reader's end state is still 0428's.

DO $fw1_06a_r4$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_reader    text;
  v_zone      text;
BEGIN
  norm := translate(db, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz');
  norm := regexp_replace(norm, '[^a-z0-9]', '_', 'g');
  IF norm = db AND length(norm) <= 30 THEN
    suffix := norm;
  ELSE
    suffix := left(norm, 21) || '_' || left(md5(db), 8);
  END IF;
  v_view   := 'assist_view_owner_'  || suffix;
  v_reader := 'assist_reader_'      || suffix;
  v_zone   := 'assist_zone_reader_' || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_view) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;
  IF to_regprocedure('ai.dealer_day()') IS NULL THEN
    RAISE EXCEPTION 'migration 0427 has not created ai.dealer_day()';
  END IF;

  -- The columns of customer_transactions the union reads, granted to the view's owner exactly as 0427 granted
  -- the columns of the tables its views read.
  EXECUTE format('GRANT SELECT (id, customer_id, branch_id, txn_type, source_type, source_ref_id, amount_cents, occurred_at) ON public.customer_transactions TO %I', v_view);

  -- The dealer's zone for the non-invoice arm's age dates, created under the zone reader exactly as
  -- ai.dealer_day() was; the body names pg_catalog and public only, so the owner needs no USAGE on ai to run it.
  EXECUTE format('GRANT CREATE, USAGE ON SCHEMA ai TO %I', v_zone);
  EXECUTE format('SET LOCAL ROLE %I', v_zone);
  CREATE FUNCTION ai.dealer_zone() RETURNS pg_catalog.text
    LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
    AS $f$ SELECT public.settings_business_zone() $f$;
  REVOKE ALL ON FUNCTION ai.dealer_zone() FROM PUBLIC;
  EXECUTE format('GRANT EXECUTE ON FUNCTION ai.dealer_zone() TO %I', v_reader);
  RESET ROLE;
  EXECUTE format('REVOKE CREATE, USAGE ON SCHEMA ai FROM %I', v_zone);

  -- The view, replaced under its owner so its ownership stays. CREATE OR REPLACE keeps the column set and the
  -- reader's grant; the grant is carried again so the file says what the reader holds, as 0427 did.
  EXECUTE format('SET LOCAL ROLE %I', v_view);

  CREATE OR REPLACE VIEW ai.ar_aging AS
    WITH open_ar AS (
      SELECT i.customer_id, i.balance_cents, i.due_date
        FROM public.invoices i
       WHERE i.status IN ('FINALIZED', 'PARTIALLY_PAID') AND i.balance_cents <> 0
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
$fw1_06a_r4$;
