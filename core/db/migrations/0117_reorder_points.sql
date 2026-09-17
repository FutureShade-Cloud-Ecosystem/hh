-- 0117: DIB-72 — reorder points per (product, branch) + the replenishment worklist substrate.
--
-- A reorder point is branch-grained, not product-grained, and that is the whole point of the table.
-- Trenton and Kingston have different yards, different traffic and different lead times; a single
-- global "min qty" on products would either flood the small yard or starve the big one. The PK is
-- (product_id, branch_id) and there is no product-level fallback row — a product with no row at a
-- branch is simply NOT replenishment-managed there, which is an honest state and is different from
-- "managed with a point of zero".
--
-- WHAT CROSSES THE LINE. Shortfall grades against AVAILABLE (on_hand - hard reservations, DIB-60),
-- not on-hand, and it nets off stock already inbound on an open transfer (DIB-71) and already on an
-- open purchase order. Grading on on-hand alone produces two classic bad worklists: it hides a
-- shortfall behind stock that is already promised to someone else, and it re-orders stock that is
-- sitting on a truck or on a PO. The projected position the service computes is:
--
--     projected = available + in_transit_in + on_order
--     shortfall when projected < reorder_point_base
--     suggested_qty = max(reorder_qty_base, reorder_point_base - projected), rounded up to
--                     order_multiple_base when one is set
--
-- order_multiple_base exists because hardscape does not sell in ones: a product bought by the skid
-- has a full_skid_quantity (A-18, 0018) and suggesting 7 of a 60-per-skid paver is a suggestion the
-- buyer has to redo by hand. The service seeds this column from full_skid_quantity where it is
-- known and leaves it NULL otherwise (NULL = no rounding, suggest the raw number).
--
-- preferred_vendor_id is the CREATE-PO handoff's first argument. Purchasing is Swarm C's fence this
-- wave, so the handoff is a CONSUMER-DECLARED SEAM with a stub default in the service — this column
-- is the data the seam needs and it is populated here regardless of who ends up implementing the
-- other side.

CREATE TABLE product_reorder_points (
  product_id          UUID NOT NULL REFERENCES products(id)  ON DELETE CASCADE,
  branch_id           UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
  reorder_point_base  DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (reorder_point_base >= 0),
  -- The standard replenishment quantity ("order a skid"). 0 = no standard qty; the service then
  -- suggests exactly the gap to the reorder point.
  reorder_qty_base    DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (reorder_qty_base >= 0),
  order_multiple_base DECIMAL(19,4) CHECK (order_multiple_base IS NULL OR order_multiple_base > 0),
  preferred_vendor_id UUID REFERENCES vendors(id) ON DELETE SET NULL,
  lead_time_days      INTEGER NOT NULL DEFAULT 0 CHECK (lead_time_days >= 0),
  is_active           BOOLEAN NOT NULL DEFAULT TRUE,
  notes               TEXT NOT NULL DEFAULT '',
  updated_by          UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (product_id, branch_id)
);

-- The worklist read is "every managed line at this branch", so branch leads the index.
CREATE INDEX idx_reorder_points_branch ON product_reorder_points(branch_id)
  WHERE is_active;

COMMENT ON TABLE product_reorder_points IS
  'DIB-72: per (product, branch) replenishment policy. Shortfall grades AVAILABLE + in-transit + '
  'on-order against reorder_point_base. No row at a branch = not replenishment-managed there.';

-- ── NO DATA SEEDING HERE, ON PURPOSE ────────────────────────────────────────────────────────────
--
-- The obvious move is an INSERT ... SELECT that opens a managed line for every stocked product at
-- half its current on-hand. It was written, it worked when tested against an already-seeded
-- database, and it is wrong: `go run ./cmd/migrate` runs BEFORE `go run ./cmd/seed`, so on a clean
-- environment this migration sees an empty stock_balances and creates 2 rows instead of 61. The
-- worklist would then be silently, invisibly under-populated in exactly the environments nobody
-- checks — a fresh CI database, a new laptop, the union rebuild — and fully populated only on the
-- one machine where the migration happened to run second. Order-dependent seeding is a bug that
-- disguises itself as data.
--
-- So the bootstrap moved to where it can see real data and be re-run: inventory's
-- BootstrapReorderPoints (POST /api/v1/inventory/reorder-points/bootstrap, manager-guarded). It
-- opens a managed line for every stocked product at a branch with the point at HALF of current
-- on-hand and skips products that already have a line. Half is deliberate: NOTHING is short
-- immediately after bootstrap, so the worklist opens empty and the first row a reviewer sees appear
-- is one their own action caused — a transfer shipped out, an order hard-reserved. A worklist that
-- is red on arrival teaches people to ignore it.
