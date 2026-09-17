-- 0126: DIB-78 — multi-shipment / single-invoice fulfilment groups.
--
-- THE GAP. dispatch already keys delivery stops to ORDERS (0012 manifest_lines, 0107 delivery
-- stops), so a 40-line landscape order that goes out on three trucks over two weeks is three stops
-- pointing at one order with nothing that says WHICH LINES went on WHICH TRUCK. The yard knows,
-- because they loaded it; the system does not, so the second truck's picker gets the whole order
-- again, and the customer gets one invoice at the end whose lines cannot be tied to any delivery
-- they actually witnessed.
--
-- A SHIPMENT GROUP is that missing noun: a named subset of an order's line quantities that is
-- picked, staged and dispatched as a unit. Groups are ADDITIVE to the existing dispatch model, not
-- a replacement — a stop still points at an order, and a group carries the (optional) stop it went
-- out on. Nothing in dispatch changes, and an order with no groups behaves exactly as it does today.
--
-- ── THE INVARIANT, AND WHY IT IS A DEFERRED CONSTRAINT TRIGGER ─────────────────────────────────
--
--     for every order that HAS groups:  for every order line,
--         SUM(shipment_group_lines.qty_base over that line) = order_lines.qty_base
--
-- i.e. the groups PARTITION the order. No quantity is in two groups (double-shipped) and no
-- quantity is in none (orphaned — the line nobody ever loads, which is exactly the failure this
-- feature exists to prevent).
--
-- It cannot be a CHECK: the sum spans two tables. It cannot be an ordinary row trigger either,
-- because building a plan is inherently a sequence of partial states — the first INSERT of a
-- two-group split is transiently short by the second group's quantity, and an immediate trigger
-- would refuse it and force the writer to disable its own guard. So it is a CONSTRAINT TRIGGER,
-- DEFERRABLE INITIALLY DEFERRED, which fires ONCE at COMMIT with the transaction's final state.
-- Any writer — this module, a future one, or a hand-typed psql session at 2am — is held to it.
--
-- It fires on order_lines too, because the other way to orphan a line is to ADD one to an order
-- that already has a shipment plan and never place it in a group. (That is precisely what a
-- SCOPE:F10 change-order apply does, which is why the two features shipped together.)
--
-- ORDERS WITH NO GROUPS ARE UNAFFECTED. The trigger short-circuits when the order has no
-- shipment_groups rows, so multi-ship is opt-in and every order written before this migration —
-- and every POS ring, quote conversion and portal reorder after it — is untouched.
--
-- ── THE INVOICING RULING (reversible) ──────────────────────────────────────────────────────────
-- DEFAULT: ONE invoice, raised when EVERY group is COMPLETE. That is the ask in DIB-78 and it is
-- what a contractor's AP department wants — a job is one bill.
--
-- OPT-IN: ON_DEMAND_SUBSET raises an invoice for the DELIVERED subset on request. It exists because
-- the same contractor on 30-day terms with a 6-week phased job will not accept 6 weeks of unbilled
-- work, and progress billing is how that trade is actually done.
--
-- The policy is a column on the order, and it is REVERSIBLE while no invoice has yet been raised
-- against the order (enforced in the service, which is where the invoice facts live). Once money
-- has been billed the choice is history and flipping it would change what the already-issued
-- invoice means. Storing it per-order rather than as a global setting is deliberate: it is a
-- commercial term of one job, negotiated with one customer, not a company posture.

BEGIN;

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS multiship_invoice_policy text NOT NULL DEFAULT 'ON_ALL_COMPLETE';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'ck_orders_multiship_invoice_policy'
    ) THEN
        ALTER TABLE orders ADD CONSTRAINT ck_orders_multiship_invoice_policy
            CHECK (multiship_invoice_policy IN ('ON_ALL_COMPLETE','ON_DEMAND_SUBSET'));
    END IF;
END $$;

-- ── the groups ─────────────────────────────────────────────────────────────────────────────────
--
-- STATUS LADDER, deliberately mirroring the ORDER ladder rather than inventing a parallel
-- vocabulary: PLANNED → PICKING → READY → DISPATCHED → COMPLETE, plus CANCELLED. A yard lead who
-- knows what READY means on an order knows what it means on a group.
CREATE TABLE IF NOT EXISTS shipment_groups (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    group_no        integer NOT NULL,
    label           text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'PLANNED',
    requested_date  date,
    -- The dispatch stop this group went out on, when it has one. NOT an FK: dispatch is another
    -- module's fence and a group must survive a stop being rescheduled away or purged. The stop
    -- still points at the ORDER, exactly as it does today — this is the reverse pointer that says
    -- which LINES rode on it.
    delivery_stop_id uuid,
    dispatched_at   timestamptz,
    completed_at    timestamptz,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT shipment_groups_status_check CHECK (status IN (
        'PLANNED','PICKING','READY','DISPATCHED','COMPLETE','CANCELLED')),
    CONSTRAINT ck_shipment_groups_complete_stamped
        CHECK (status <> 'COMPLETE' OR completed_at IS NOT NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_shipment_groups_order_no
    ON shipment_groups(order_id, group_no);
CREATE INDEX IF NOT EXISTS idx_shipment_groups_order ON shipment_groups(order_id, group_no);
CREATE INDEX IF NOT EXISTS idx_shipment_groups_stop
    ON shipment_groups(delivery_stop_id) WHERE delivery_stop_id IS NOT NULL;

-- ── the group lines ────────────────────────────────────────────────────────────────────────────
--
-- Keyed on order_line_id (the stable identity) rather than (order_id, line_no): the change-order
-- apply path renumbers nothing, but a line_no is a presentation ordinal and building a partition on
-- an ordinal is how partitions rot.
CREATE TABLE IF NOT EXISTS shipment_group_lines (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    shipment_group_id uuid NOT NULL REFERENCES shipment_groups(id) ON DELETE CASCADE,
    order_line_id     uuid NOT NULL REFERENCES order_lines(id) ON DELETE CASCADE,
    qty_base          numeric(19,4) NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT ck_shipment_group_lines_qty_positive CHECK (qty_base > 0)
);

-- One row per (group, line): a line's share of a group is ONE number, not a running tally.
CREATE UNIQUE INDEX IF NOT EXISTS uq_shipment_group_lines_group_line
    ON shipment_group_lines(shipment_group_id, order_line_id);
CREATE INDEX IF NOT EXISTS idx_shipment_group_lines_line ON shipment_group_lines(order_line_id);

-- ── the coverage invariant ─────────────────────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION assert_shipment_groups_cover_order(p_order_id uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_has_groups boolean;
    v_bad        record;
BEGIN
    -- Multi-ship is opt-in: an order with no LIVE groups is not partitioned and is not graded.
    -- CANCELLED groups are excluded from the partition — a cancelled truck's quantity returns to
    -- the pool of what still has to be placed, and holding it against coverage would deadlock the
    -- plan (you could never cancel a group without simultaneously re-covering its lines).
    SELECT EXISTS (
        SELECT 1 FROM shipment_groups g
         WHERE g.order_id = p_order_id AND g.status <> 'CANCELLED'
    ) INTO v_has_groups;
    IF NOT v_has_groups THEN
        RETURN;
    END IF;

    SELECT ol.line_no,
           ol.qty_base                     AS want,
           COALESCE(SUM(sgl.qty_base), 0)  AS got
      INTO v_bad
      FROM order_lines ol
      LEFT JOIN shipment_group_lines sgl ON sgl.order_line_id = ol.id
      LEFT JOIN shipment_groups g
             ON g.id = sgl.shipment_group_id AND g.status <> 'CANCELLED'
     WHERE ol.order_id = p_order_id
       AND (g.id IS NOT NULL OR sgl.id IS NULL)
     GROUP BY ol.id, ol.line_no, ol.qty_base
    HAVING COALESCE(SUM(sgl.qty_base), 0) <> ol.qty_base
     ORDER BY ol.line_no
     LIMIT 1;

    IF FOUND THEN
        RAISE EXCEPTION
            'shipment groups do not partition order %: line % has qty_base % but its groups sum to % '
            '(every order line quantity must sit in exactly one live shipment group — no line orphaned, '
            'no quantity shipped twice)',
            p_order_id, v_bad.line_no, v_bad.want, v_bad.got
            USING ERRCODE = 'check_violation';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION trg_shipment_groups_cover()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_order_id uuid;
BEGIN
    IF TG_TABLE_NAME = 'order_lines' THEN
        v_order_id := COALESCE(NEW.order_id, OLD.order_id);
    ELSIF TG_TABLE_NAME = 'shipment_groups' THEN
        v_order_id := COALESCE(NEW.order_id, OLD.order_id);
    ELSE -- shipment_group_lines
        SELECT g.order_id INTO v_order_id
          FROM shipment_groups g
         WHERE g.id = COALESCE(NEW.shipment_group_id, OLD.shipment_group_id);
    END IF;

    -- A cascade-deleted order takes its groups with it; there is nothing left to partition.
    IF v_order_id IS NULL OR NOT EXISTS (SELECT 1 FROM orders WHERE id = v_order_id) THEN
        RETURN NULL;
    END IF;

    PERFORM assert_shipment_groups_cover_order(v_order_id);
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS ck_shipment_group_lines_cover ON shipment_group_lines;
CREATE CONSTRAINT TRIGGER ck_shipment_group_lines_cover
    AFTER INSERT OR UPDATE OR DELETE ON shipment_group_lines
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION trg_shipment_groups_cover();

DROP TRIGGER IF EXISTS ck_shipment_groups_cover ON shipment_groups;
CREATE CONSTRAINT TRIGGER ck_shipment_groups_cover
    AFTER INSERT OR UPDATE OR DELETE ON shipment_groups
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION trg_shipment_groups_cover();

DROP TRIGGER IF EXISTS ck_order_lines_shipment_cover ON order_lines;
CREATE CONSTRAINT TRIGGER ck_order_lines_shipment_cover
    AFTER INSERT OR UPDATE OR DELETE ON order_lines
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION trg_shipment_groups_cover();

COMMIT;
