-- 0428: slice 7b of the assistant's database boundary (TanyAI plan r4 slice 7, delivery, CRM and messages metadata).
--
-- 1. The zone reader (0427) needed CREATE and USAGE on schema ai only to create ai.dealer_day(). A definer function
--    runs as its owner and resolves the names in its body through its own search path (pg_catalog, public,
--    pg_temp), none of them in ai, so the owner needs no privilege on ai at call time (the caller needs USAGE, and
--    the reader has it). Both are taken back here, so the role owns one function in a schema it cannot use or
--    create in.
-- 2. The delivery views, the customer views, the message channel view, their row security and their masks.
--
-- Role names carry the database suffix exactly as in 0418.

DO $s7b$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_zone      text;
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
  v_zone   := 'assist_zone_reader_'  || suffix;
  v_reader := 'assist_reader_'       || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_view) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;
  IF to_regprocedure('ai.dealer_day()') IS NULL THEN
    RAISE EXCEPTION 'migration 0427 has not created ai.dealer_day()';
  END IF;

  -- 1. The zone reader keeps nothing on schema ai.
  EXECUTE format('REVOKE CREATE, USAGE ON SCHEMA ai FROM %I', v_zone);
  -- 2. The delivery family: ai.delivery_runs and ai.delivery_stops.
  --
  -- Rows follow the dispatch screens (board, run, stop, manifest): the branch fence of the run or the stop, the
  -- assistant's tier floor on top. delivery_runs and delivery_stops are served tables (sync): row security is
  -- enabled on them and no column and no json column is added. No latitude, longitude, instructions, gate code,
  -- signature reference, signer name, freight or load-building column is granted or exposed.
  FOREACH t IN ARRAY ARRAY['delivery_runs', 'delivery_stops'] LOOP
    SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = t;
    IF owner_name IS DISTINCT FROM current_user THEN
      RAISE EXCEPTION 'table % is owned by %, not by the migrating role %: refusing to enable row security on it',
        t, coalesce(owner_name, '(missing)'), current_user;
    END IF;
  END LOOP;
  EXECUTE format('GRANT SELECT (id, run_no, branch_id, scheduled_date, truck_id, driver_id, status, dispatched_at, completed_at) ON public.delivery_runs TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, branch_id, order_id, run_id, stop_no, scheduled_date, status, time_window, contact_name, contact_phone, address_line, city, postal_code, failure_reason, delivered_at) ON public.delivery_stops TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, code) ON public.dispatch_trucks TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, name) ON public.dispatch_drivers TO %I', v_view);

  ALTER TABLE public.delivery_runs ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.delivery_stops ENABLE ROW LEVEL SECURITY;
  EXECUTE format($p$
    CREATE POLICY assist_staff_delivery_runs ON public.delivery_runs TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_delivery_stops ON public.delivery_stops TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);

  EXECUTE format('SET LOCAL ROLE %I', v_view);
  CREATE VIEW ai.delivery_runs AS
    SELECT r.id,
           r.run_no,
           r.scheduled_date,
           r.branch_id,
           COALESCE(l.name, '') AS branch_label,
           COALESCE(t.code::text, '') AS truck_code,
           COALESCE(d.name, '') AS driver_label,
           r.status,
           (SELECT count(*) FROM public.delivery_stops s WHERE s.run_id = r.id)::int AS stop_count,
           r.dispatched_at,
           r.completed_at
      FROM public.delivery_runs r
      LEFT JOIN public.locations l ON l.id = r.branch_id
      LEFT JOIN public.dispatch_trucks t ON t.id = r.truck_id
      LEFT JOIN public.dispatch_drivers d ON d.id = r.driver_id;
  CREATE VIEW ai.delivery_stops AS
    SELECT s.id,
           s.run_id,
           COALESCE(r.run_no, '') AS run_no,
           s.stop_no,
           s.scheduled_date,
           s.branch_id,
           COALESCE(l.name, '') AS branch_label,
           s.order_id,
           COALESCE(o.order_no::text, '') AS order_no,
           COALESCE(c.name, '') AS customer_name,
           s.status,
           s.time_window,
           s.contact_name,
           s.contact_phone,
           s.address_line,
           s.city,
           s.postal_code,
           s.failure_reason,
           s.delivered_at
      FROM public.delivery_stops s
      LEFT JOIN public.delivery_runs r ON r.id = s.run_id
      LEFT JOIN public.locations l ON l.id = s.branch_id
      LEFT JOIN public.orders o ON o.id = s.order_id
      LEFT JOIN public.customers c ON c.id = o.customer_id;
  EXECUTE format('GRANT SELECT ON ai.delivery_runs, ai.delivery_stops TO %I', v_reader);
  RESET ROLE;
  -- 3. The CRM family: ai.customers and ai.customer_contacts.
  --
  -- ai.customers: the customer screens (GET /customers/{id} and the picker search) show an account to a staff
  -- caller who reaches its home branch, and every account homed to no branch (company wide by construction). No
  -- row security is put on customers: the order, quote, invoice, payment and delivery views already join it for the
  -- customer's name, and a policy there would blank those names for an account homed to another branch, which
  -- their own screens show. So the view carries the reach in its WHERE (audience, tier floor and home branch), and
  -- is a security barrier so no outer qual runs below it. The three flags are the words yes and no, which the query
  -- grammar can filter and group by. Credit limit and balance are not exposed: the receivables
  -- register gates the account position by a per branch policy while the picker shows it to any staff, and that
  -- disagreement is the lead's to settle.
  -- ai.customer_contacts: the one screen that shows a contact to staff is the receivables register's invoice
  -- detail, which shows the account's first active contact (the primary one first) to a caller who reaches one of
  -- the account's invoices. The policy admits a contact for an account with an invoice in the caller's reach; the
  -- view keeps that one contact per account. No billing address: no staff screen shows it.
  SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = 'customer_contacts';
  IF owner_name IS DISTINCT FROM current_user THEN
    RAISE EXCEPTION 'table customer_contacts is owned by %, not by the migrating role %: refusing to enable row security on it',
      coalesce(owner_name, '(missing)'), current_user;
  END IF;
  EXECUTE format('GRANT SELECT (id, name, account_no, customer_type, home_branch_id, payment_terms_days, on_hold, tax_exempt, is_active) ON public.customers TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, customer_id, name, email, phone, is_primary, is_active, created_at) ON public.customer_contacts TO %I', v_view);
  ALTER TABLE public.customer_contacts ENABLE ROW LEVEL SECURITY;
  EXECUTE format($p$
    CREATE POLICY assist_staff_customer_contacts ON public.customer_contacts TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND EXISTS (
        SELECT 1 FROM public.invoices i
        WHERE i.customer_id = customer_contacts.customer_id
          AND ((SELECT ai.claims()).global OR i.branch_id = ANY ((SELECT ai.claims()).branch_ids))
      )
    )$p$, v_view);

  EXECUTE format('SET LOCAL ROLE %I', v_view);
  CREATE VIEW ai.customers WITH (security_barrier = true) AS
    SELECT c.id,
           c.account_no::text AS account_no,
           c.name,
           c.customer_type,
           c.home_branch_id AS branch_id,
           COALESCE(l.name, '') AS branch_label,
           c.payment_terms_days,
           CASE WHEN c.on_hold THEN 'yes' ELSE 'no' END AS on_hold,
           CASE WHEN c.tax_exempt THEN 'yes' ELSE 'no' END AS tax_exempt,
           CASE WHEN c.is_active THEN 'yes' ELSE 'no' END AS is_active
      FROM public.customers c
      LEFT JOIN public.locations l ON l.id = c.home_branch_id
     WHERE (SELECT ai.claims()).audience = 'staff'
       AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
       AND (c.home_branch_id IS NULL
            OR (SELECT ai.claims()).global
            OR c.home_branch_id = ANY ((SELECT ai.claims()).branch_ids));
  CREATE VIEW ai.customer_contacts AS
    SELECT x.customer_id,
           COALESCE(c.account_no::text, '') AS account_no,
           COALESCE(c.name, '') AS customer_name,
           x.name AS contact_name,
           COALESCE(x.email::text, '') AS contact_email,
           x.phone AS contact_phone
      FROM (SELECT DISTINCT ON (cc.customer_id) cc.customer_id, cc.name, cc.email, cc.phone
              FROM public.customer_contacts cc
             WHERE cc.is_active
             ORDER BY cc.customer_id, cc.is_primary DESC, cc.created_at ASC) x
      LEFT JOIN public.customers c ON c.id = x.customer_id;
  EXECUTE format('GRANT SELECT ON ai.customers, ai.customer_contacts TO %I', v_reader);
  RESET ROLE;
  -- 4. House Messages metadata: ai.message_channels.
  --
  -- Only the channels the asker is a member of right now: the claim's channel_ids are the messaging module's own
  -- membership read (materialised membership, archived rooms left out, the money room fence applied), resolved by
  -- the handler at request time, and the policies admit a row of any of these tables only if its channel is in that
  -- list. NEVER a message body (no column of msg_messages that holds text is granted), never a member list (the
  -- view counts members and names none), never a member's name. The unread count and the time of the latest unread
  -- message are the asker's own, computed here from the asker's own member row by the rule the messaging module's
  -- unread projection (msg_unread_effective) applies, which the view cannot read itself: it compares a citext role
  -- code, a function no assist role may run, and a function in a view is checked against the caller. The rule: a
  -- channel is ALL, MENTIONS or MUTED for the asker (the asker's preference, else the channel's default; an
  -- ESCALATION channel is always ALL); ALL counts the messages from others newer than the asker's read cursor,
  -- MUTED counts none. MENTIONS counts the messages that mention the asker or the asker's role, which needs the
  -- role tables; this view does not read them, so for a MENTIONS channel the count is NULL (not known), never a
  -- number that could be wrong, and notify_mode says why.
  FOREACH t IN ARRAY ARRAY['msg_channels', 'msg_members', 'msg_messages', 'msg_notification_prefs'] LOOP
    SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = t;
    IF owner_name IS DISTINCT FROM current_user THEN
      RAISE EXCEPTION 'table % is owned by %, not by the migrating role %: refusing to enable row security on it',
        t, coalesce(owner_name, '(missing)'), current_user;
    END IF;
  END LOOP;
  EXECUTE format('GRANT SELECT (id, kind, branch_id, name, is_archived, tier, default_mode) ON public.msg_channels TO %I', v_view);
  EXECUTE format('GRANT SELECT (channel_id, user_id, last_read_at) ON public.msg_members TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, channel_id, author_id, created_at, deleted_at) ON public.msg_messages TO %I', v_view);
  EXECUTE format('GRANT SELECT (channel_id, user_id, mode) ON public.msg_notification_prefs TO %I', v_view);
  ALTER TABLE public.msg_channels ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.msg_members ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.msg_messages ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.msg_notification_prefs ENABLE ROW LEVEL SECURITY;
  EXECUTE format($p$
    CREATE POLICY assist_staff_msg_channels ON public.msg_channels TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND NOT is_archived
      AND id = ANY ((SELECT ai.claims()).channel_ids)
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_msg_members ON public.msg_members TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND channel_id = ANY ((SELECT ai.claims()).channel_ids)
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_msg_messages ON public.msg_messages TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND channel_id = ANY ((SELECT ai.claims()).channel_ids)
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_msg_notification_prefs ON public.msg_notification_prefs TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND channel_id = ANY ((SELECT ai.claims()).channel_ids)
    )$p$, v_view);

  EXECUTE format('SET LOCAL ROLE %I', v_view);
  CREATE VIEW ai.message_channels AS
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
      LEFT JOIN LATERAL (
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
  EXECUTE format('GRANT SELECT ON ai.message_channels TO %I', v_reader);
  RESET ROLE;
END
$s7b$;
