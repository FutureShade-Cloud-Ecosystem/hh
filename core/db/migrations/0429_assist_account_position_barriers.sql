-- 0429: slice 7c of the assistant's database boundary (TanyAI plan r4, slice 7 follow up).
--
-- 1. Every view that joins customers for a name is a security barrier. customers carries no row security (a policy
--    there would blank the names the other views join), so a name is visible to a view only through the view's own
--    reach; as plain views, an outer WHERE could be evaluated at the customers scan ahead of that reach, and the
--    safety of the older views would rest on no assist role ever being able to create a function. A barrier
--    removes the dependence (ai.customers is one already, from 0428).
-- 2. ai.message_channels requires the asker's own member row as well as the signed channel list, so a stale or
--    mistaken claim lists no channel the asker is not in.
-- 3. Credit limit and open balance on ai.customers, masked per row by a third claim field, the home branches whose
--    customers' account position the asker may see under the receivables register's own per branch policy
--    (ar_invoice_policy.account_position_visibility: MANAGER_UP, ALL_STAFF, HIDDEN). The all zero uuid is the
--    dealer default, which customers homed to no branch follow. The open balance is the register's: the sum of an
--    account's FINALIZED and PARTIALLY_PAID balances across EVERY branch, which the invoices row policy (the asker's
--    branches) cannot give; so it comes from ai.account_positions, a view owned by a role of its own
--    (assist_position_owner_<suffix>: NOLOGIN, no power, three columns of invoices, one policy that shows it every
--    branch's invoices to a staff tier 1 or 2 claim), readable by the view owner alone.
--
-- Role names carry the database suffix exactly as in 0418.

DO $s7c$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_reader    text;
  v_claims    text;
  v_pos       text;
  app         text := current_user;
  is_super    boolean;
  v           text;
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
  v_claims := 'assist_claims_owner_' || suffix;
  v_pos    := 'assist_position_owner_' || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_view) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;
  IF to_regclass('ai.message_channels') IS NULL THEN
    RAISE EXCEPTION 'migration 0428 has not created ai.message_channels';
  END IF;

  -- The claim grows the account position branches (ai.claims() reads fields by name, so the function needs no
  -- change; an old claim reads NULL, which the masks treat as no account position anywhere).
  EXECUTE format('SET LOCAL ROLE %I', v_claims);
  ALTER TYPE ai.claim_set ADD ATTRIBUTE account_position_branch_ids uuid[];
  RESET ROLE;

  -- The position owner, created once like the others.
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_pos) THEN
    EXECUTE format('CREATE ROLE %I NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION', v_pos);
  END IF;
  SELECT rolsuper INTO is_super FROM pg_roles WHERE rolname = current_user;
  IF NOT is_super THEN
    EXECUTE format('GRANT %I TO %I WITH SET TRUE, INHERIT FALSE', v_pos, app);
  END IF;
  SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = 'invoices';
  IF owner_name IS DISTINCT FROM current_user THEN
    RAISE EXCEPTION 'table invoices is owned by %, not by the migrating role %: refusing to grant on it', coalesce(owner_name, '(missing)'), current_user;
  END IF;
  EXECUTE format('GRANT SELECT (customer_id, status, balance_cents) ON public.invoices TO %I', v_pos);
  EXECUTE format($p$
    CREATE POLICY assist_position_invoices ON public.invoices TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
    )$p$, v_pos);
  EXECUTE format('GRANT SELECT (credit_limit_cents) ON public.customers TO %I', v_view);
  EXECUTE format('GRANT USAGE, CREATE ON SCHEMA ai TO %I', v_pos);
  EXECUTE format('SET LOCAL ROLE %I', v_pos);
  CREATE VIEW ai.account_positions AS
    SELECT i.customer_id,
           SUM(i.balance_cents)::bigint AS open_balance_cents
      FROM public.invoices i
     WHERE i.status IN ('FINALIZED', 'PARTIALLY_PAID')
     GROUP BY i.customer_id;
  EXECUTE format('GRANT SELECT ON ai.account_positions TO %I', v_view);
  RESET ROLE;
  EXECUTE format('REVOKE CREATE, USAGE ON SCHEMA ai FROM %I', v_pos);

  EXECUTE format('SET LOCAL ROLE %I', v_view);

  -- 1. The barriers.
  FOREACH v IN ARRAY ARRAY['orders', 'quotes', 'invoices', 'payments', 'delivery_stops', 'customer_contacts', 'ar_aging'] LOOP
    EXECUTE format('ALTER VIEW ai.%I SET (security_barrier = true)', v);
  END LOOP;

  -- 2. The channel view, with the asker's own member row required (an inner lateral join in place of a left one).
  CREATE OR REPLACE VIEW ai.message_channels AS
    SELECT ch.id,
           ch.kind,
           ch.name,
           ch.branch_id,
           COALESCE(l.name, '') AS branch_label,
           (SELECT count(*) FROM public.msg_members m WHERE m.channel_id = ch.id)::int AS member_count,
           COALESCE(u.mode, '') AS notify_mode,
           CASE u.mode
             WHEN 'MUTED' THEN 0::bigint
             WHEN 'MENTIONS' THEN NULL::bigint
             ELSE COALESCE(u.raw_count, 0)::bigint
           END AS unread_count,
           u.latest_at AS latest_unread_at
      FROM public.msg_channels ch
      LEFT JOIN public.locations l ON l.id = ch.branch_id
      JOIN LATERAL (
        SELECT CASE WHEN ch.tier = 'ESCALATION' THEN 'ALL' ELSE COALESCE(p.mode, ch.default_mode, 'ALL') END AS mode,
               count(msg.id) AS raw_count,
               max(msg.created_at) AS latest_at
          FROM public.msg_members mem
          LEFT JOIN public.msg_notification_prefs p ON p.channel_id = mem.channel_id AND p.user_id = mem.user_id
          LEFT JOIN public.msg_messages msg
            ON msg.channel_id = mem.channel_id
           AND msg.created_at > mem.last_read_at
           AND msg.deleted_at IS NULL
           AND (msg.author_id IS NULL OR msg.author_id <> mem.user_id)
         WHERE mem.channel_id = ch.id AND mem.user_id::text = (SELECT ai.claims()).asker_id
         GROUP BY p.mode
      ) u ON TRUE;

  -- 3. ai.customers with the account position. New columns go last (CREATE OR REPLACE VIEW appends); the barrier
  -- and the reach in the WHERE stay as they were.
  CREATE OR REPLACE VIEW ai.customers WITH (security_barrier = true) AS
    SELECT c.id,
           c.account_no::text AS account_no,
           c.name,
           c.customer_type,
           c.home_branch_id AS branch_id,
           COALESCE(l.name, '') AS branch_label,
           c.payment_terms_days,
           CASE WHEN c.on_hold THEN 'yes' ELSE 'no' END AS on_hold,
           CASE WHEN c.tax_exempt THEN 'yes' ELSE 'no' END AS tax_exempt,
           CASE WHEN c.is_active THEN 'yes' ELSE 'no' END AS is_active,
           CASE WHEN COALESCE(c.home_branch_id, '00000000-0000-0000-0000-000000000000'::uuid)
                     = ANY ((SELECT ai.claims()).account_position_branch_ids)
                THEN c.credit_limit_cents END AS credit_limit_cents,
           CASE WHEN COALESCE(c.home_branch_id, '00000000-0000-0000-0000-000000000000'::uuid)
                     = ANY ((SELECT ai.claims()).account_position_branch_ids)
                THEN COALESCE(p.open_balance_cents, 0)::bigint END AS open_balance_cents
      FROM public.customers c
      LEFT JOIN public.locations l ON l.id = c.home_branch_id
      LEFT JOIN ai.account_positions p ON p.customer_id = c.id
     WHERE (SELECT ai.claims()).audience = 'staff'
       AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
       AND (c.home_branch_id IS NULL
            OR (SELECT ai.claims()).global
            OR c.home_branch_id = ANY ((SELECT ai.claims()).branch_ids));
  RESET ROLE;
END
$s7c$;
