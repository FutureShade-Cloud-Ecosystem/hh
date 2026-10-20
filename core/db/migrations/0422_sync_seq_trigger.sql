-- 0422_sync_seq_trigger.sql: the database draws a sync seq whenever a writer did not.
--
-- Every device feed pulls "seq IS NOT NULL AND seq > cursor". Nothing in the schema filled seq (0213 added
-- it nullable with no default and no trigger), so freshness depended on every write site in the code
-- remembering to draw one by hand, and several do not. This migration closes that class for good:
--
--   1. sync_seq_draw() draws from the table's stream counter (scope_sequences, the same upsert every
--      adapter runs) BEFORE an INSERT whose seq is NULL, and BEFORE an UPDATE that changed the row
--      but left seq where it was. It defers to every explicit draw: a writer that sets seq itself
--      (the statement already carries its own draw) changes seq, so the trigger does nothing.
--   2. One trigger per served table, one stream per table.
--   3. A floor per stream, so no new draw lands at or below a seq a device already holds.
--   4. A one time explicit re-draw of every row, so a device that synced past a row changed earlier
--      without a draw catches up (its feed learns the row, and the deletes). Set based, per stream.
--
-- Left out on purpose: stock_balances (SF-7 owns it with its lock order place), every msg_* table
-- (their seq is an IDENTITY column, a different counter), tasks (excluded for good: two streams by
-- task type, pick_tasks for PICK rows and weights for NEED_WEIGHT rows, and its writers draw them
-- explicitly), dashboard_user_widgets (stream still to be ruled).
-- will_call_pickups is in, on its single stream will_call (the pickups UX-72 change put every writer on
-- it, the counter lifted past the frozen pickups value). Its floor is the greatest of the will_call counter,
-- the legacy pickups counter and the greatest seq the table holds (a device cursor can sit at a number the
-- old counter handed out although no row holds it now), and its rows are re-drawn
-- explicitly below, which is what lets a staff pickup that sat under a contractor device's cursor reach
-- that device.
-- docs/adr/0025-sync-seq-trigger.md records the reasoning.
--
-- An upsert (INSERT ... ON CONFLICT DO UPDATE) draws twice: the BEFORE INSERT trigger for the
-- proposed row, then the BEFORE UPDATE trigger on the conflict arm. A harmless gap; gaps are allowed.
-- A json, xml or geometric column on a trigger table would make every UPDATE fail on the row
-- comparison; the catalogue test refuses one.
--
-- Forward only. Idempotent: CREATE OR REPLACE FUNCTION, DROP TRIGGER IF EXISTS.

CREATE OR REPLACE FUNCTION sync_seq_draw() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    drawn bigint;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.seq IS NOT NULL THEN
            RETURN NEW;
        END IF;
    ELSE
        -- An explicit draw moved seq; a true no op update changed nothing. Either way, nothing to do.
        IF NEW.seq IS DISTINCT FROM OLD.seq OR NOT (NEW IS DISTINCT FROM OLD) THEN
            RETURN NEW;
        END IF;
    END IF;
    INSERT INTO scope_sequences (scope) VALUES (TG_ARGV[0])
    ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
    RETURNING next_value INTO drawn;
    NEW.seq := drawn;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION sync_seq_draw() IS
    'Draws the table''s sync seq (stream = first trigger argument) when the writer did not. ADR 0025.';

DO $$
DECLARE
    m record;
    n bigint;
    base bigint;
    top bigint;
    t text;
BEGIN
    -- table, stream: exactly one stream per table.
    CREATE TEMP TABLE sync_seq_map (tbl text PRIMARY KEY, stream text NOT NULL) ON COMMIT DROP;
    INSERT INTO sync_seq_map (tbl, stream) VALUES
        ('payments', 'payments'),
        ('invoices', 'invoices'),
        ('quotes', 'quotes'),
        ('delivery_runs', 'runs'),
        ('delivery_stops', 'stops'),
        ('delivery_pod_photos', 'pod'),
        ('products', 'products'),
        ('customers', 'customers'),
        ('vendors', 'vendors'),
        ('portal_payment_methods', 'payment_methods'),
        ('portal_users', 'team'),
        ('warranty_claims', 'warranty_claims'),
        ('pos_parked_tickets', 'parked_tickets'),
        ('customer_quote', 'customer_quote'),
        ('procurement_projects', 'projects'),
        ('dispatch_trucks', 'trucks'),
        ('dispatch_drivers', 'drivers'),
        ('attachment', 'attachments'),
        ('plan', 'orders'),
        ('plan_line', 'scope_items'),
        ('permission_approval_requests', 'approvals'),
        ('pricing_list_prices', 'price_matrix'),
        ('purchasing_pos', 'purchase_orders'),
        ('task_shortages', 'shortages'),
        ('count_sheets', 'cycle_counts'),
        ('will_call_pickups', 'will_call');

    -- 2. the triggers.
    FOR m IN SELECT tbl, stream FROM sync_seq_map ORDER BY tbl LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS trg_sync_seq_draw ON %I', m.tbl);
        EXECUTE format(
            'CREATE TRIGGER trg_sync_seq_draw BEFORE INSERT OR UPDATE ON %I '
            'FOR EACH ROW EXECUTE FUNCTION sync_seq_draw(%L)', m.tbl, m.stream);
    END LOOP;

    -- 3. the floors: next_value holds the last value handed out, so the floor is the greatest seq
    -- any table on the stream holds (no + 1).
    FOR m IN SELECT DISTINCT stream FROM sync_seq_map ORDER BY stream LOOP
        top := 0;
        FOR t IN SELECT tbl FROM sync_seq_map WHERE stream = m.stream LOOP
            EXECUTE format('SELECT COALESCE(max(seq), 0) FROM %I', t) INTO n;
            top := greatest(top, n);
        END LOOP;
        -- will_call_pickups moved from the old pickups counter to will_call (UX-72). A contractor device may hold
        -- a cursor the old counter handed out to a row that a staff write has since re-drawn lower, so no row
        -- holds that number but the device's cursor does: the floor includes the legacy counter.
        IF m.stream = 'will_call' THEN
            SELECT COALESCE(max(next_value), 0) INTO n FROM scope_sequences WHERE scope = 'pickups';
            top := greatest(top, n);
        END IF;
        INSERT INTO scope_sequences (scope, next_value) VALUES (m.stream, top)
        ON CONFLICT (scope) DO UPDATE SET next_value = greatest(scope_sequences.next_value, EXCLUDED.next_value);
    END LOOP;

    -- 4. the one time re-draw, an explicit set based draw (a touch would be skipped by the trigger's
    -- own condition): advance the stream once by the table's row count, then number the rows past
    -- the old counter in their present seq order. Live and tombstoned rows alike, so devices learn
    -- the deletes too. The new seq is above every old seq, so the trigger sees an explicit draw.
    FOR m IN SELECT tbl, stream FROM sync_seq_map ORDER BY stream, tbl LOOP
        EXECUTE format('SELECT count(*) FROM %I', m.tbl) INTO n;
        CONTINUE WHEN n = 0;
        UPDATE scope_sequences SET next_value = next_value + n WHERE scope = m.stream
        RETURNING next_value - n INTO base;
        EXECUTE format(
            'UPDATE %1$I x SET seq = %2$s + r.rn '
            'FROM (SELECT ctid AS c, row_number() OVER (ORDER BY seq NULLS FIRST, ctid) AS rn FROM %1$I) r '
            'WHERE x.ctid = r.c', m.tbl, base);
    END LOOP;
END;
$$;
