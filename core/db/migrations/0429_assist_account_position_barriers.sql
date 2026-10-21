-- 0429: slice 7c of the assistant's database boundary (TanyAI plan r4, slice 7 follow up).
--
-- 1. Every view that joins customers for a name is a security barrier. customers carries no row security (a policy
--    there would blank the names the other views join), so a name is visible to a view only through the view's own
--    reach; as plain views, an outer WHERE could be evaluated at the customers scan ahead of that reach, and the
--    safety of the older views would rest on no assist role ever being able to create a function. A barrier
--    removes the dependence (ai.customers is one already, from 0428).
-- 2. ai.message_channels requires the asker's own member row as well as the signed channel list, so a stale or
--    mistaken claim lists no channel the asker is not in.
-- 3. Credit limit and balance on ai.customers (added in a later commit of this slice).
--
-- Role names carry the database suffix exactly as in 0418.

DO $s7c$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_reader    text;
  v           text;
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
  IF to_regclass('ai.message_channels') IS NULL THEN
    RAISE EXCEPTION 'migration 0428 has not created ai.message_channels';
  END IF;

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
  RESET ROLE;
END
$s7c$;
