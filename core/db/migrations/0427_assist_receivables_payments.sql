-- 0427: slice 7a of the assistant's database boundary (TanyAI plan r3 slice 7, receivables and payments).
--
-- The invoice, aging, payment and allocation views, their row security and their masks. The payment class is real
-- here: a payment's amount, method and status, its unapplied figure and every allocation cell are NULL unless the
-- signed claim's payment_trail flag is set, decided in Go by the role gate's own admission to the two trail routes
-- (ruling 87). The claim type already carries payment_trail (0418), so this file changes no type.
--
-- Rows follow the receivables screens: an invoice is visible where the register returns it (branch reach, the
-- assistant's tier floor on top); a payment where the customer workspace's payment list or the payment history
-- returns it (the workspace leaves out VOID, NSF and BOUNCED payments, so an asker without the trail does not get
-- those rows); an allocation only where a trail route returns it (the payment history by the payment's branch, the
-- invoice's payments by the invoice's branch), so only for an asker who holds the trail.
--
-- The aging view restates the aging screen's projection (credit_repository.go agingSelect) over the invoices the
-- row policy lets the asker reach, because v_ar_aging runs as its owner and would bypass row security. The aging
-- day is the dealer's day: the view reads the business_zone setting through a policy that lets the view owner see
-- that one row of system_settings and nothing else.
--
-- Role names carry the database suffix exactly as in 0418.

DO $s7a$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_reader    text;
  t           text;
  owner_name  text;
BEGIN
  norm := translate(db, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz');
  norm := regexp_replace(norm, '[^a-z0-9]', '_', 'g');
  IF norm = db AND length(norm) <= 30 THEN
    suffix := norm;
  ELSE
    suffix := left(norm, 21) || '_' || left(md5(db), 8);
  END IF;
  v_view   := 'assist_view_owner_'   || suffix;
  v_reader := 'assist_reader_'       || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_view) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;

  FOREACH t IN ARRAY ARRAY['invoices', 'payments', 'payment_allocations', 'system_settings'] LOOP
    SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = t;
    IF owner_name IS DISTINCT FROM current_user THEN
      RAISE EXCEPTION 'table % is owned by %, not by the migrating role %: refusing to enable row security on it',
        t, coalesce(owner_name, '(missing)'), current_user;
    END IF;
  END LOOP;

  -- Exact column grants. Gateway references, payment tokens, signature references, card types and the metadata
  -- of every table are not granted: they are secret and in no view.
  EXECUTE format('GRANT SELECT (id, invoice_no, doc_type, customer_id, order_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents) ON public.invoices TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, payment_no, customer_id, method, amount_cents, status, received_at, branch_id) ON public.payments TO %I', v_view);
  EXECUTE format('GRANT SELECT (payment_id, invoice_id, amount_cents, created_at) ON public.payment_allocations TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, name, account_no) ON public.customers TO %I', v_view);
  EXECUTE format('GRANT SELECT (key, value) ON public.system_settings TO %I', v_view);

  ALTER TABLE public.invoices ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.payments ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.payment_allocations ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.system_settings ENABLE ROW LEVEL SECURITY;

  EXECUTE format($p$
    CREATE POLICY assist_staff_invoices ON public.invoices TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);
  -- A payment is a row of the workspace's payment list (every status but VOID, NSF and BOUNCED) for any asker, and
  -- of the payment history (every status) for an asker who holds the trail.
  EXECUTE format($p$
    CREATE POLICY assist_staff_payments ON public.payments TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
      AND ((SELECT ai.claims()).payment_trail IS TRUE OR status NOT IN ('VOID', 'NSF', 'BOUNCED'))
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_payment_allocations ON public.payment_allocations TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND (SELECT ai.claims()).payment_trail IS TRUE
      AND (
        EXISTS (SELECT 1 FROM public.payments p
                 WHERE p.id = payment_allocations.payment_id
                   AND ((SELECT ai.claims()).global OR p.branch_id = ANY ((SELECT ai.claims()).branch_ids)))
        OR EXISTS (SELECT 1 FROM public.invoices i
                    WHERE i.id = payment_allocations.invoice_id
                      AND ((SELECT ai.claims()).global OR i.branch_id = ANY ((SELECT ai.claims()).branch_ids)))
      )
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_zone_only ON public.system_settings TO %I
    USING (key::text = 'business_zone')$p$, v_view);

  EXECUTE format('SET LOCAL ROLE %I', v_view);

  CREATE VIEW ai.invoices AS
    SELECT i.id,
           i.invoice_no::text AS invoice_no,
           i.doc_type,
           i.status,
           i.issue_date,
           i.due_date,
           i.branch_id,
           COALESCE(l.name, '') AS branch_label,
           i.customer_id,
           COALESCE(c.name, '') AS customer_name,
           COALESCE(c.account_no::text, '') AS account_no,
           i.order_id,
           COALESCE(o.order_no::text, '') AS order_no,
           i.subtotal_cents,
           i.tax_cents,
           i.holdback_cents,
           i.total_cents,
           i.balance_cents
      FROM public.invoices i
      LEFT JOIN public.customers c ON c.id = i.customer_id
      LEFT JOIN public.locations l ON l.id = i.branch_id
      LEFT JOIN public.orders o ON o.id = i.order_id;

  -- One row per account with an open balance in the asker's reach, the aging screen's projection: open invoices
  -- (FINALIZED and PARTIALLY_PAID) bucketed by DUE DATE against the dealer's day, the same five bands, a zero
  -- balance dropped. An account that lives partly elsewhere ages for the reach alone.
  CREATE VIEW ai.ar_aging AS
    SELECT c.id AS customer_id,
           COALESCE(c.name, '') AS customer_name,
           COALESCE(c.account_no::text, '') AS account_no,
           d.today AS as_of,
           COALESCE(SUM(i.balance_cents) FILTER (WHERE d.today <= i.due_date), 0)::bigint AS current_cents,
           COALESCE(SUM(i.balance_cents) FILTER (WHERE d.today > i.due_date AND d.today <= i.due_date + 30), 0)::bigint AS days_1_30_cents,
           COALESCE(SUM(i.balance_cents) FILTER (WHERE d.today > i.due_date + 30 AND d.today <= i.due_date + 60), 0)::bigint AS days_31_60_cents,
           COALESCE(SUM(i.balance_cents) FILTER (WHERE d.today > i.due_date + 60 AND d.today <= i.due_date + 90), 0)::bigint AS days_61_90_cents,
           COALESCE(SUM(i.balance_cents) FILTER (WHERE d.today > i.due_date + 90), 0)::bigint AS days_90_plus_cents,
           COALESCE(SUM(i.balance_cents), 0)::bigint AS open_cents
      FROM public.customers c
      JOIN public.invoices i ON i.customer_id = c.id AND i.status IN ('FINALIZED', 'PARTIALLY_PAID')
      CROSS JOIN LATERAL (
        SELECT (now() AT TIME ZONE COALESCE(
                 (SELECT z.zone
                    FROM (SELECT btrim(s.value #>> '{}') AS zone
                            FROM public.system_settings s
                           WHERE s.key::text = 'business_zone' AND jsonb_typeof(s.value) = 'string') z
                   WHERE z.zone <> '' AND z.zone <> 'Local'
                     AND (EXISTS (SELECT 1 FROM pg_timezone_names n WHERE n.name = z.zone)
                          OR EXISTS (SELECT 1 FROM pg_timezone_abbrevs a WHERE a.abbrev = z.zone))),
                 'America/Toronto'))::date AS today
      ) d
     GROUP BY c.id, c.name, c.account_no, d.today
    HAVING COALESCE(SUM(i.balance_cents), 0) <> 0;

  -- The payment class: amount, method, status and the unapplied figure are NULL unless the claim holds the trail.
  CREATE VIEW ai.payments AS
    SELECT p.id,
           p.payment_no::text AS payment_no,
           p.received_at,
           p.branch_id,
           COALESCE(l.name, '') AS branch_label,
           p.customer_id,
           COALESCE(c.name, '') AS customer_name,
           CASE WHEN (SELECT ai.claims()).payment_trail IS TRUE THEN p.method END AS method,
           CASE WHEN (SELECT ai.claims()).payment_trail IS TRUE THEN p.status END AS status,
           CASE WHEN (SELECT ai.claims()).payment_trail IS TRUE THEN p.amount_cents END AS amount_cents,
           CASE WHEN (SELECT ai.claims()).payment_trail IS TRUE
                THEN GREATEST(p.amount_cents - COALESCE(a.applied, 0), 0)::bigint END AS unapplied_cents
      FROM public.payments p
      LEFT JOIN public.customers c ON c.id = p.customer_id
      LEFT JOIN public.locations l ON l.id = p.branch_id
      LEFT JOIN LATERAL (
        SELECT SUM(pa.amount_cents) AS applied FROM public.payment_allocations pa WHERE pa.payment_id = p.id
      ) a ON TRUE;

  -- Allocations: rows only for an asker who holds the trail (the policy), cells masked again by the claim. The
  -- payment and invoice numbers come through the row security of their own tables, so a number on the other side
  -- of a cross branch allocation is empty when that side is outside the asker's reach.
  CREATE VIEW ai.payment_allocations AS
    SELECT pa.payment_id,
           COALESCE(p.payment_no::text, '') AS payment_no,
           pa.invoice_id,
           COALESCE(i.invoice_no::text, '') AS invoice_no,
           CASE WHEN (SELECT ai.claims()).payment_trail IS TRUE THEN pa.amount_cents END AS amount_cents,
           CASE WHEN (SELECT ai.claims()).payment_trail IS TRUE THEN pa.created_at END AS allocated_at
      FROM public.payment_allocations pa
      LEFT JOIN public.payments p ON p.id = pa.payment_id
      LEFT JOIN public.invoices i ON i.id = pa.invoice_id;

  EXECUTE format('GRANT SELECT ON ai.invoices, ai.ar_aging, ai.payments, ai.payment_allocations TO %I', v_reader);
  RESET ROLE;
END
$s7a$;
