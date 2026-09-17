-- 0125: SCOPE:F10 — post-pick change orders.
--
-- THE GAP. internal/order shipped a staff line-edit path in DIB-175 (0008 order_lines mutated in
-- place under a row lock) whose edit-state matrix DIES AT READY, and which is a straight-through
-- write: a manager types a new quantity and the order changes underneath whoever is holding the
-- pick ticket. That is right for "the customer rang while it was still on the shelf" and wrong for
-- "the truck is half loaded". Once a pick has STARTED there are two other parties with a stake in
-- the change — the yard, who are physically holding the goods, and finance, when the delta is
-- material — and neither of them has any say in a straight-through edit.
--
-- So a post-pick change is not an edit. It is a REQUEST that carries a proposed delta, collects
-- acknowledgements, and only then applies. This migration is that request.
--
-- ── WHY THE DELTA IS STORED, NOT THE RESULT ────────────────────────────────────────────────────
-- A change request could store "what the order should look like afterwards" (a target line set) or
-- "what to do to the order" (a delta). It stores the DELTA, per line, because between the request
-- and the approval the order is STILL WORKABLE — that is the whole point of the feature; a yard
-- does not stop picking for three hours while a controller finds their phone. A stored target set
-- would silently revert any other legitimate movement in that window (a shortage substitution, a
-- fulfilled quantity) because applying it means overwriting. A delta applies ON TOP of whatever the
-- order became, and where it cannot (the line was removed underneath it) it FAILS LOUDLY at apply
-- rather than resurrecting a line nobody asked for.
--
-- ── ONE OPEN REQUEST PER ORDER ─────────────────────────────────────────────────────────────────
-- uq_change_orders_one_open enforces at most one non-terminal request per order. Two concurrent
-- deltas against the same lines cannot be reasoned about by the people signing them: the yard
-- acknowledges "add 2 pallets", finance approves "remove 1 pallet", and what applies depends on
-- commit order. A partial unique index makes the second request a 409 at the door instead.
--
-- ── THE $500 THRESHOLD IS A CONFIGURED NUMBER, NOT A CONSTANT ──────────────────────────────────
-- finance_threshold_cents is STAMPED ONTO THE ROW at request time (default 50000 = $500). The
-- threshold in force when a change was raised is part of the story of why it did or did not need a
-- controller's signature, and re-reading today's configuration when auditing a change from March
-- answers the wrong question. Same reasoning as the pricing audit's resolved-price snapshot.
--
-- ── STATUS LADDER ──────────────────────────────────────────────────────────────────────────────
--   DRAFT            raised, nothing collected yet (never persisted today; reserved for a future
--                    "compose the delta over several screens" flow)
--   PENDING_YARD     awaiting the yard ACK round trip
--   PENDING_FINANCE  yard has acked; |delta| >= threshold so a controller must sign
--   APPROVED         every required signature collected; ready to apply
--   APPLIED          the delta has been written to order_lines and reservations re-run (terminal)
--   REJECTED         a signer refused (terminal)
--   CANCELLED        the requester withdrew it (terminal)
--
-- APPROVED is a distinct, persisted state rather than an implicit "acks are complete" derivation
-- so that apply is idempotent and re-entrant: the apply transaction moves APPROVED→APPLIED under a
-- row lock, so two clicks on the apply button cannot double-add two pallets.

BEGIN;

CREATE TABLE IF NOT EXISTS change_orders (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id                uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    branch_id               uuid REFERENCES locations(id) ON DELETE RESTRICT,
    change_no               text NOT NULL,
    status                  text NOT NULL DEFAULT 'PENDING_YARD',
    reason                  text NOT NULL DEFAULT '',

    -- The money delta, computed from the lines at request time and re-computed under the lock at
    -- apply. Signed: a removal is negative. abs() is what the threshold grades — a $600 removal is
    -- exactly as material to a controller as a $600 addition.
    delta_cents             bigint NOT NULL DEFAULT 0,
    finance_threshold_cents bigint NOT NULL DEFAULT 50000,
    finance_required        boolean NOT NULL DEFAULT false,

    -- The order's status at the moment the request was raised, and whether the tasks substrate
    -- reported a started pick. Stamped, not derived: "was this really post-pick?" must be
    -- answerable months later, when the order is CLOSED and its pick tasks are DONE.
    order_status_at_request text NOT NULL DEFAULT '',
    pick_started_at_request boolean NOT NULL DEFAULT false,

    requested_by            uuid REFERENCES users(id) ON DELETE SET NULL,
    requested_at            timestamptz NOT NULL DEFAULT now(),

    yard_acked_by           uuid REFERENCES users(id) ON DELETE SET NULL,
    yard_acked_at           timestamptz,
    yard_ack_note           text NOT NULL DEFAULT '',
    -- The tasks-substrate round trip this ACK travelled on, when one was minted. NOT an FK: the
    -- tasks table is another module's, and a change order must survive a task being purged.
    yard_task_id            uuid,

    finance_approved_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    finance_approved_at     timestamptz,
    finance_note            text NOT NULL DEFAULT '',

    decided_by              uuid REFERENCES users(id) ON DELETE SET NULL,
    decided_at              timestamptz,
    decision_note           text NOT NULL DEFAULT '',

    applied_at              timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT change_orders_status_check CHECK (status IN (
        'DRAFT','PENDING_YARD','PENDING_FINANCE','APPROVED','APPLIED','REJECTED','CANCELLED')),
    CONSTRAINT ck_change_orders_threshold_positive CHECK (finance_threshold_cents > 0),
    -- An APPLIED row must carry its stamp. Without this a bug that forgot the stamp would look on
    -- screen exactly like a change that was applied at an unknown time.
    CONSTRAINT ck_change_orders_applied_stamped CHECK (status <> 'APPLIED' OR applied_at IS NOT NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_change_orders_change_no ON change_orders(change_no);

-- At most ONE live request per order (see the header). Terminal rows are excluded, so an order can
-- accumulate a full history of applied/rejected changes.
CREATE UNIQUE INDEX IF NOT EXISTS uq_change_orders_one_open
    ON change_orders(order_id)
 WHERE status IN ('DRAFT','PENDING_YARD','PENDING_FINANCE','APPROVED');

CREATE INDEX IF NOT EXISTS idx_change_orders_order ON change_orders(order_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_change_orders_open_branch
    ON change_orders(branch_id, status)
 WHERE status IN ('PENDING_YARD','PENDING_FINANCE','APPROVED');

-- change_order_lines — the delta, one row per affected order line.
--
-- op ADD carries a NULL line_no (the line does not exist yet; apply mints the next line_no) and a
-- product + qty + price. op QTY and op REMOVE carry the target line_no. qty_delta_base is signed
-- for QTY, and for REMOVE it is the full negative of the line at request time, recorded so the
-- money delta is computable from the delta rows alone without re-reading the order.
CREATE TABLE IF NOT EXISTS change_order_lines (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    change_order_id   uuid NOT NULL REFERENCES change_orders(id) ON DELETE CASCADE,
    seq               integer NOT NULL,
    op                text NOT NULL,
    line_no           integer,
    product_id        uuid REFERENCES products(id) ON DELETE RESTRICT,
    description       text NOT NULL DEFAULT '',
    -- The entered quantity as typed, plus its base projection. ADD/QTY use qty_entered_new as the
    -- TARGET quantity for the line; qty_delta_base is the signed base movement it implies.
    qty_entered_new   numeric(19,4),
    uom_entered       citext REFERENCES uoms(code) ON DELETE RESTRICT,
    qty_base_before   numeric(19,4) NOT NULL DEFAULT 0,
    qty_base_after    numeric(19,4) NOT NULL DEFAULT 0,
    qty_delta_base    numeric(19,4) NOT NULL DEFAULT 0,
    unit_price_cents  bigint NOT NULL DEFAULT 0,
    price_uom         citext REFERENCES uoms(code) ON DELETE RESTRICT,
    line_delta_cents  bigint NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT change_order_lines_op_check CHECK (op IN ('ADD','QTY','REMOVE')),
    -- An ADD has no existing line to point at; QTY/REMOVE must name one. Encoding this here means a
    -- malformed delta cannot reach the apply transaction at all.
    CONSTRAINT ck_change_order_lines_op_shape CHECK (
        (op = 'ADD'    AND line_no IS NULL AND product_id IS NOT NULL) OR
        (op IN ('QTY','REMOVE') AND line_no IS NOT NULL)
    ),
    CONSTRAINT ck_change_order_lines_add_qty CHECK (op <> 'ADD' OR qty_base_after > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_change_order_lines_seq
    ON change_order_lines(change_order_id, seq);
-- One delta row per existing line per request: two contradictory instructions for line 3 in the
-- same change are unsignable for the same reason two open changes are.
CREATE UNIQUE INDEX IF NOT EXISTS uq_change_order_lines_line
    ON change_order_lines(change_order_id, line_no)
 WHERE line_no IS NOT NULL;

CREATE SEQUENCE IF NOT EXISTS change_order_no_seq START 1;

COMMIT;
