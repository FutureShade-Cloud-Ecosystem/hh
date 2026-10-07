-- 0365: a short pick is recorded with a reason, and the shortfall becomes a backorder on the
-- order line. Owners: internal/tasks (the two pick line columns) and internal/order (the rest).
--
-- THE GAP. At the yard a skid is condensed or a line is picked short (3 of 5). Until now the
-- measurement (task_pick_lines.qty_picked, 0187) carried no reason and had no effect on the
-- order: the order stayed invoiced for what was ordered, and the office learned only when the
-- customer complained. This migration holds the three things the fix needs and nothing else.
--
-- WHAT THE ORDER MODULE HAD. order_lines.fulfilled_base (0008) is the quantity that has gone out,
-- and the whole-order invoice already bills it pro-rated (invoice_source.go). Nothing modelled a
-- quantity that will NOT go out now but is still owed to the customer later. A backorder is
-- exactly that, so it is added here as two columns on the line, in the line's own unit and in the
-- base unit, rather than as a new status or a second line (a second line would split one thing the
-- customer bought into two things the customer must reconcile).
--
-- THE INVARIANT. Ordered = to fulfil + backorder. qty_entered/qty_base drop to the quantity to
-- fulfil now (what the invoice will bill, what the truck carries); backorder_* holds the rest.
-- The ordered quantity is therefore always qty + backorder, and the unit price never moves:
-- the customer agreed a price, a short pick does not reprice what was delivered.
--
-- order_line_shortfalls freezes the ORDERED values at the first short on a line (ordered quantity
-- and ordered line total), so a later correction (the yard recounts and finds the rest) restores
-- the line exactly, and a repeat of the same short can never drift a cent. It also keeps the
-- reason and who recorded it. order_shortfall_basis freezes the ORDER's ordered subtotal and tax
-- for the same reason: the header tax is pro-rated from the originals every time, never from the
-- previous pro-ration, so two corrections cannot walk the tax away from the price.
--
-- ADDITIVE AND SAFE ON A DATABASE THAT IS NEVER WIPED: two ADD COLUMNs with defaults on
-- existing tables and two new tables; no backfill. A line with no shortfall row has backorder 0,
-- which is what it always meant.

ALTER TABLE task_pick_lines
  ADD COLUMN IF NOT EXISTS short_reason text NOT NULL DEFAULT ''
    CHECK (short_reason IN ('', 'CONDENSED_SKIDS', 'DAMAGED', 'NOT_IN_YARD', 'COUNT_CORRECTION', 'OTHER')),
  ADD COLUMN IF NOT EXISTS short_note text NOT NULL DEFAULT ''
    CHECK (char_length(short_note) <= 400);

ALTER TABLE order_lines
  ADD COLUMN IF NOT EXISTS backorder_entered DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (backorder_entered >= 0),
  ADD COLUMN IF NOT EXISTS backorder_base    DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (backorder_base >= 0);

CREATE TABLE IF NOT EXISTS order_line_shortfalls (
  order_line_id       UUID PRIMARY KEY REFERENCES order_lines(id) ON DELETE CASCADE,
  order_id            UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  line_no             INTEGER NOT NULL,
  ordered_entered     DECIMAL(19,4) NOT NULL CHECK (ordered_entered > 0),
  ordered_base        DECIMAL(19,4) NOT NULL CHECK (ordered_base > 0),
  ordered_total_cents BIGINT NOT NULL CHECK (ordered_total_cents >= 0),
  reason              TEXT NOT NULL DEFAULT '',
  note                TEXT NOT NULL DEFAULT '',
  recorded_by         UUID,
  recorded_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_order_line_shortfalls_order ON order_line_shortfalls (order_id, line_no);

CREATE TABLE IF NOT EXISTS order_shortfall_basis (
  order_id               UUID PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
  ordered_subtotal_cents BIGINT NOT NULL CHECK (ordered_subtotal_cents >= 0),
  ordered_tax_cents      BIGINT NOT NULL CHECK (ordered_tax_cents >= 0),
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE order_line_shortfalls IS
  '0365: the ordered values of an order line frozen at its first short pick, with the reason and who recorded it. Ordered = order_lines.qty + order_lines.backorder.';
COMMENT ON TABLE order_shortfall_basis IS
  '0365: the ordered subtotal and tax of an order frozen at its first short pick, so the header tax is always pro-rated from the originals.';
