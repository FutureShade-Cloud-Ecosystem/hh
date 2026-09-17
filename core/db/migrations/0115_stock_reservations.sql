-- 0115: DIB-60 — three-tier stock allocation policy + the reservation substrate.
--
-- THE GAP. Until now "available" and "on hand" were the same number everywhere availability was
-- read (inventory.AvailabilityFacts, InStockProductIDs, OnHandByBranch, the POS glance). A branch
-- could therefore promise the same pallet to a confirmed order, a live quote and a transfer at the
-- same moment, and only discover the double-sell when a picker walked out to an empty bay. The
-- 0005 stock_balances.allocated_base column was declared for exactly this and never written by any
-- producer — it is still 0 in every row, in every environment.
--
-- WHY A NEW COLUMN AND NOT allocated_base. allocated_base carried a CHECK (on_hand_base >=
-- allocated_base) in 0005 which a later migration had to relax, and nothing in the tree writes it.
-- Re-purposing a column with a dead meaning means every reader has to know which era it is looking
-- at. qty_reserved is written by exactly one producer (the reservation projection below) and its
-- invariant is stated once, here: qty_reserved = SUM of ACTIVE reservations that HOLD stock, for
-- that (product, location). allocated_base is left untouched and dormant; nothing reads it.
--
-- THE THREE TIERS. A reservation is typed by WHAT asked for it, and graded by policy into whether
-- it HOLDS stock:
--
--   ORDER    — a confirmed sales order. Default HARD: it takes the stock out of available.
--   QUOTE    — a live quote. Default SOFT: it is visible and it expires, but it does NOT reduce
--              available. A soft hold that blocked a cash sale would be worse than no hold at all —
--              the quote may never convert, and the counter must not be told "none left" because a
--              contractor is thinking about it.
--   TRANSFER — stock committed to an outbound inter-branch transfer (DIB-71). Always HARD: the
--              units are spoken for by another branch and a second promise on them is a real
--              oversell.
--
-- available = on_hand - qty_reserved, and qty_reserved counts ONLY hold_mode='HARD' rows. So the
-- SOFT/HARD grade, not the type, is what arithmetic sees — which is what makes the per-branch
-- policy below meaningful rather than decorative: a branch that sets quote_mode='HARD' genuinely
-- starts reserving against quotes, with no code change and no new column.
--
-- EXPIRY. A soft hold with no expiry is a leak: a quote abandoned in a parking lot would hold a
-- number on a screen forever. Every reservation carries expires_at (NULL = does not expire, which
-- is the default for ORDER — a confirmed order holds until it ships or is cancelled). The sweep
-- that flips ACTIVE→EXPIRED is a service call (inventory.ExpireDueReservations); registering it on
-- the jobs runner is a PARK for the union, so this migration ships the data shape and the manual
-- seam and does not assume a scheduler.

CREATE TABLE stock_reservations (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id    UUID NOT NULL REFERENCES products(id)  ON DELETE RESTRICT,
  branch_id     UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  location_id   UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  res_type      TEXT NOT NULL CHECK (res_type  IN ('ORDER','QUOTE','TRANSFER')),
  hold_mode     TEXT NOT NULL CHECK (hold_mode IN ('HARD','SOFT')),
  qty_base      DECIMAL(19,4) NOT NULL CHECK (qty_base > 0),
  status        TEXT NOT NULL DEFAULT 'ACTIVE'
                  CHECK (status IN ('ACTIVE','RELEASED','CONSUMED','EXPIRED')),
  source_type   TEXT NOT NULL DEFAULT '',
  source_ref_id UUID,
  reason        TEXT NOT NULL DEFAULT '',
  expires_at    TIMESTAMPTZ,
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  closed_at     TIMESTAMPTZ,
  -- A closed reservation must say when it closed, and an open one must not pretend it did. This is
  -- the column the release/consume/expire paths are audited against.
  CHECK ((status = 'ACTIVE') = (closed_at IS NULL))
);

-- The hot read is "the live holds on this (product, location)" — the projection maintainer and the
-- 4-bucket view both drive off it. PARTIAL on ACTIVE: closed rows are history and are never summed.
CREATE INDEX idx_stock_reservations_live ON stock_reservations(product_id, location_id)
  WHERE status = 'ACTIVE';
CREATE INDEX idx_stock_reservations_branch_live ON stock_reservations(branch_id, product_id)
  WHERE status = 'ACTIVE';
-- The expiry sweep scans due rows only.
CREATE INDEX idx_stock_reservations_due ON stock_reservations(expires_at)
  WHERE status = 'ACTIVE' AND expires_at IS NOT NULL;
-- Source lookup: "release everything this order/quote/transfer holds".
CREATE INDEX idx_stock_reservations_source ON stock_reservations(source_type, source_ref_id)
  WHERE status = 'ACTIVE';

-- The projection. Maintained by inventory's reservation writer in the same transaction as the
-- stock_reservations row, so a reader never sees the two disagree.
ALTER TABLE stock_balances
  ADD COLUMN qty_reserved DECIMAL(19,4) NOT NULL DEFAULT 0;

-- Reserved is a quantity, and a negative quantity reserved is not a state the domain has. The
-- projection maintainer clamps at zero on release; this CHECK is what turns a clamp bug from a
-- silently wrong "available" into a failed transaction.
ALTER TABLE stock_balances
  ADD CONSTRAINT stock_balances_qty_reserved_nonneg CHECK (qty_reserved >= 0);

-- DELIBERATELY ABSENT: a CHECK (on_hand_base >= qty_reserved). A hard reservation may legitimately
-- exceed on-hand — an order confirmed against inbound stock, or stock shipped out from under a
-- reservation by a correction. Available then reads NEGATIVE, which is the honest answer and is
-- exactly what the shortfall worklist (DIB-72) is looking for. Forbidding it in the schema would
-- force the writer to silently under-reserve, which is the oversell this whole migration exists to
-- prevent.

COMMENT ON COLUMN stock_balances.qty_reserved IS
  'DIB-60: SUM(qty_base) of ACTIVE hold_mode=HARD stock_reservations for this (product, location). '
  'available = on_hand_base - qty_reserved (may be negative). allocated_base is the dormant 0005 '
  'column and is not part of this arithmetic.';

-- ── per-branch allocation policy ────────────────────────────────────────────────────────────────
--
-- Resolved per branch with a SYSTEM DEFAULT fallback, so a branch with no row behaves correctly on
-- day one rather than behaving as if allocation were off. The stated default policy is:
--
--     ORDER    → HARD, no expiry            (hard-reserve on order confirm)
--     QUOTE    → SOFT, expires in 72 hours  (soft-reserve on quote)
--     TRANSFER → HARD, no expiry            (not configurable — see below)
--
-- TRANSFER IS NOT IN THIS TABLE ON PURPOSE. A transfer that does not hold its stock is an overdraft
-- waiting to happen at the source branch, and the no-overdraft guard in DIB-71 grades against
-- available. Making it configurable would let a branch switch off its own oversell guard from a
-- settings screen. It is HARD, always, in code.

CREATE TABLE branch_allocation_policy (
  branch_id       UUID PRIMARY KEY REFERENCES locations(id) ON DELETE CASCADE,
  order_mode      TEXT NOT NULL DEFAULT 'HARD' CHECK (order_mode IN ('HARD','SOFT','NONE')),
  quote_mode      TEXT NOT NULL DEFAULT 'SOFT' CHECK (quote_mode IN ('HARD','SOFT','NONE')),
  -- 0 = no expiry. Only meaningful when the matching mode is not NONE.
  order_ttl_hours INTEGER NOT NULL DEFAULT 0  CHECK (order_ttl_hours >= 0),
  quote_ttl_hours INTEGER NOT NULL DEFAULT 72 CHECK (quote_ttl_hours >= 0),
  updated_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE branch_allocation_policy IS
  'DIB-60: per-branch allocation policy. A branch with NO row resolves to the system default '
  '(ORDER=HARD/no-expiry, QUOTE=SOFT/72h). TRANSFER is always HARD and is not configurable here.';

-- Seed the two live branches at the system default so the policy surface has something to show and
-- so an operator editing Kingston cannot accidentally be editing "the global setting".
INSERT INTO branch_allocation_policy (branch_id)
SELECT id FROM locations WHERE loc_type = 'BRANCH'
ON CONFLICT (branch_id) DO NOTHING;

-- ── available_base now means what it says ───────────────────────────────────────────────────────
--
-- v_stock_on_hand (0037) already published an available_base column, computed as on_hand_base -
-- allocated_base. Since nothing has ever written allocated_base, that column has been equal to
-- on_hand_base in every row in every environment — "available" was a synonym for "on hand" wearing
-- a different name, which is the single most dangerous kind of wrong: readers trusted it.
--
-- Repointing it at qty_reserved is numerically a no-op TODAY (allocated_base is 0 everywhere, so
-- both expressions equal on_hand_base until the first reservation is written) and semantically the
-- whole of DIB-60. Every existing consumer of available_base — inventory.OnHandFor, and the surfaces
-- that render it — starts telling the truth the moment a reservation exists, without being edited.
--
-- CREATE OR REPLACE VIEW can change an expression but cannot reorder or retype existing columns, so
-- qty_reserved is APPENDED after updated_at. allocated_base is kept in the projection (dropping it
-- would break the replace) and is now purely historical.
CREATE OR REPLACE VIEW v_stock_on_hand AS
SELECT sb.product_id,
       sb.location_id,
       sb.branch_id,
       sb.on_hand_base,
       sb.allocated_base,
       sb.on_hand_base - sb.qty_reserved AS available_base,
       sb.bin_code,
       sb.updated_at,
       sb.qty_reserved
  FROM stock_balances sb;
