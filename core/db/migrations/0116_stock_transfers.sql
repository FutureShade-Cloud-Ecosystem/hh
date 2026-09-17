-- 0116: DIB-71 — inter-branch stock transfers (Trenton <-> Kingston).
--
-- THE SHAPE. A transfer is a PAIR of stock moves separated in time: stock leaves the source branch
-- when the truck is loaded, and arrives at the destination when it is unloaded. Between those two
-- events the units belong to neither branch's on-hand and are not lost — they are IN TRANSIT.
--
-- WHERE IN-TRANSIT LIVES, AND WHY NOT IN stock_balances. The obvious modelling is a TRANSIT
-- location row whose stock_balances line holds the units. It was rejected: locations.branch_id is
-- non-null and FK'd to a BRANCH, so a transit location necessarily belongs to one branch or the
-- other, and every existing on-hand read in the tree (inventory.AvailabilityFacts,
-- InStockProductIDs, OnHandByBranch, OnHandQtyBranch, invcount's SnapshotOnHand, the POS glance in
-- pos/repository.go which is OUTSIDE this fence) sums stock_balances by branch_id with no location
-- filter. Every one of them would silently start counting a truck on the 401 as sellable stock at
-- whichever end owned the transit row. Fixing that means editing readers in another swarm's fence.
--
-- So in-transit is DERIVED, from this table: qty_shipped_base - qty_received_base on open lines.
-- The ledger stays exactly as balanced as it was:
--
--   ship    → stock_moves(from_location = source branch, to_location = NULL)  and -qty on the
--             source stock_balances row.
--   receive → stock_moves(from_location = NULL, to_location = dest branch)    and +qty on the
--             destination stock_balances row.
--
-- Each leg reconciles against v_stock_balance_drift on its own (that view splits every move into a
-- +qty leg at to_location and a -qty leg at from_location and compares per (product, location) —
-- see 0022), so the whole-DB drift gate stays at zero throughout the in-transit window, which a
-- transit-location model could not have promised. "Balanced double-entry" is asserted where it is
-- actually meaningful: over the PAIR. For every RECEIVED transfer, units out of source == units
-- into destination, and system-wide on-hand is conserved. That is the leased invariant test.
--
-- THE GL SIDE follows the existing move-posting convention (inventory posts through the injected
-- GLPoster with roles resolved via gl.ResolveRule, never a literal account code):
--   ship    → DR in_transit / CR inventory   at the SOURCE branch
--   receive → DR inventory   / CR in_transit at the DESTINATION branch
-- Both journals balance on their own, so a transfer left in transit over a period close leaves the
-- value parked in a real account rather than vanishing.

CREATE TABLE stock_transfers (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  transfer_no    CITEXT NOT NULL UNIQUE,
  from_branch_id UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  to_branch_id   UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  status         TEXT NOT NULL DEFAULT 'DRAFT'
                   CHECK (status IN ('DRAFT','IN_TRANSIT','RECEIVED','CANCELLED')),
  notes          TEXT NOT NULL DEFAULT '',
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  shipped_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  received_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  shipped_at     TIMESTAMPTZ,
  received_at    TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A transfer to yourself is a no-op that would still write two moves and two journals.
  CHECK (from_branch_id <> to_branch_id),
  -- The timestamps are the audit trail for the pair; a status cannot claim an event that has no time.
  CHECK ((status IN ('IN_TRANSIT','RECEIVED')) = (shipped_at IS NOT NULL)),
  CHECK ((status = 'RECEIVED') = (received_at IS NOT NULL))
);

CREATE INDEX idx_stock_transfers_from   ON stock_transfers(from_branch_id, status);
CREATE INDEX idx_stock_transfers_to     ON stock_transfers(to_branch_id, status);
CREATE INDEX idx_stock_transfers_open   ON stock_transfers(status) WHERE status = 'IN_TRANSIT';

CREATE TABLE stock_transfer_lines (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  transfer_id       UUID NOT NULL REFERENCES stock_transfers(id) ON DELETE CASCADE,
  line_no           INTEGER NOT NULL CHECK (line_no > 0),
  product_id        UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  uom               CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  qty_base          DECIMAL(19,4) NOT NULL CHECK (qty_base > 0),
  qty_shipped_base  DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (qty_shipped_base  >= 0),
  qty_received_base DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (qty_received_base >= 0),
  -- The source branch's weighted-average at SHIP time. The destination receives at this cost, so
  -- moving stock between yards does not invent or destroy value.
  unit_cost_cents   BIGINT NOT NULL DEFAULT 0,
  ship_move_id      UUID REFERENCES stock_moves(id) ON DELETE RESTRICT,
  recv_move_id      UUID REFERENCES stock_moves(id) ON DELETE RESTRICT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (transfer_id, line_no),
  -- You cannot receive what was never shipped. This is the schema half of "receive completes the
  -- pair"; the service half is the receive guard.
  CHECK (qty_received_base <= qty_shipped_base),
  CHECK (qty_shipped_base  <= qty_base)
);

CREATE INDEX idx_stock_transfer_lines_transfer ON stock_transfer_lines(transfer_id, line_no);
-- The in-transit bucket read: open legs per (product, destination branch).
CREATE INDEX idx_stock_transfer_lines_intransit ON stock_transfer_lines(product_id)
  WHERE qty_shipped_base > qty_received_base;

-- ── numbering ───────────────────────────────────────────────────────────────────────────────────
-- Branch-prefixed, mirroring next_order_no (0087, DIB-388): one mint, per-branch counter row, so
-- the format cannot drift across writers. Numbered off the SOURCE branch — the yard that gives the
-- stock up owns the document.

CREATE TABLE transfer_no_sequences (
  branch_id  UUID PRIMARY KEY REFERENCES locations(id) ON DELETE RESTRICT,
  last_value BIGINT NOT NULL DEFAULT 0 CHECK (last_value >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION next_transfer_no(p_branch_id UUID) RETURNS TEXT AS $$
DECLARE
  v_code TEXT;
  v_root UUID;
  v_n    BIGINT;
BEGIN
  SELECT CASE WHEN l.loc_type = 'BRANCH' THEN l.id ELSE l.branch_id END
    INTO v_root FROM locations l WHERE l.id = p_branch_id;
  IF v_root IS NULL THEN
    RAISE EXCEPTION 'next_transfer_no: unknown branch %', p_branch_id;
  END IF;

  SELECT l.code::text INTO v_code FROM locations l WHERE l.id = v_root;
  IF v_code IS NULL OR v_code = '' THEN
    RAISE EXCEPTION 'next_transfer_no: branch % has no code', v_root;
  END IF;

  INSERT INTO transfer_no_sequences (branch_id, last_value)
       VALUES (v_root, 1)
  ON CONFLICT (branch_id)
  DO UPDATE SET last_value = transfer_no_sequences.last_value + 1, updated_at = now()
    RETURNING last_value INTO v_n;

  RETURN '#TR-' || v_code || '-' || v_n::text;
END;
$$ LANGUAGE plpgsql;

INSERT INTO transfer_no_sequences (branch_id)
SELECT id FROM locations WHERE loc_type = 'BRANCH'
ON CONFLICT (branch_id) DO NOTHING;

COMMENT ON FUNCTION next_transfer_no(UUID) IS
  'DIB-71: mints #TR-<branch code>-<n> off the SOURCE branch, mirroring next_order_no (0087).';

-- ── GL: the in-transit account + the two posting rules ──────────────────────────────────────────
--
-- 1250 sits between 1200 Inventory and 1300 Prepaid Expenses, which is where an auditor expects to
-- find stock that has left one yard and not yet arrived at the other. Without it, a transfer open
-- over a period close would either double-count (value at both ends) or vanish (value at neither) —
-- both of which reconcile the subledger to 1200 incorrectly and are exactly the kind of drift the
-- monthly inventory→GL reconciliation exists to surface.
--
-- Both legs are resolved through gl.ResolveRule at post time, never as a literal in Go, matching the
-- convention every other producer follows (po.received, inventory.adjusted, warranty.settled).
INSERT INTO gl_accounts (code, name, account_type, normal_balance) VALUES
  ('1250', 'Stock In Transit (Inter-Branch)', 'ASSET', 'DEBIT')
ON CONFLICT (code) DO NOTHING;

INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('inventory.transfer_shipped',  'inventory',  '1200'),
  ('inventory.transfer_shipped',  'in_transit', '1250'),
  ('inventory.transfer_received', 'inventory',  '1200'),
  ('inventory.transfer_received', 'in_transit', '1250')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
