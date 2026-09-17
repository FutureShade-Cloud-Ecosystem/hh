-- 0121: AP vendor bills, the three-way match, and instalments.
--
-- THE THREE-WAY MATCH is the control that stops a supplier being paid for goods nobody received at a price
-- nobody agreed to. Three numbers per PO line:
--
--   ORDERED  purchasing_po_lines.qty_ordered_base  × unit_cost_cents   (what we agreed to buy)
--   RECEIVED purchasing_po_lines.qty_received_base                     (what landed on the yard)
--   BILLED   Σ purchasing_bill_lines.qty_billed_base × unit_cost_cents (what the vendor is asking for)
--
-- and three outcomes:
--
--   MATCHED       billed agrees with received, and the billed price agrees with the ordered price, both
--                 within tolerance. Nothing for a human to look at.
--   SHORT_BILLED  the vendor billed FEWER units than we received. Not a loss — the opposite — but it leaves
--                 GRNI (2150) accrued with no payable behind it, so it must be visible or the month-end
--                 reconciliation is a mystery. It is its own state precisely because it is NOT an
--                 overcharge and must not be triaged as one.
--   VARIANCE      anything that costs money: billed MORE units than received, or a unit price above the
--                 agreed one, beyond tolerance. A VARIANCE bill cannot post without an explicit, recorded
--                 acceptance (purchasing.Service.PostBill's accept_variance) — the gate is the point.
--
-- TOLERANCE. Two dimensions, because they fail differently. A quantity tolerance is a percentage (bulk
-- aggregate is weighed, not counted; 20.03 tonnes against 20 ordered is not a discrepancy). A price
-- tolerance is a percentage OR a flat cents floor, whichever is MORE forgiving — 2% of a $3 line is six
-- cents, and no clerk should be pulled off the counter for six cents. Both default conservatively and are
-- per-branch overridable, because the branches buy different things.

CREATE TABLE purchasing_match_tolerance (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- NULL branch_id = the org default. Partial unique indexes below (the posting_rules pattern) because a
  -- plain UNIQUE would allow many NULL rows and the default must be singular.
  branch_id                 UUID REFERENCES locations(id) ON DELETE CASCADE,
  qty_tolerance_pct         NUMERIC(6,3) NOT NULL DEFAULT 0.000 CHECK (qty_tolerance_pct >= 0),
  price_tolerance_pct       NUMERIC(6,3) NOT NULL DEFAULT 2.000 CHECK (price_tolerance_pct >= 0),
  price_tolerance_abs_cents BIGINT       NOT NULL DEFAULT 500   CHECK (price_tolerance_abs_cents >= 0),
  updated_by                UUID REFERENCES users(id) ON DELETE SET NULL,
  updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX ux_purchasing_tolerance_default ON purchasing_match_tolerance((branch_id IS NULL)) WHERE branch_id IS NULL;
CREATE UNIQUE INDEX ux_purchasing_tolerance_branch  ON purchasing_match_tolerance(branch_id) WHERE branch_id IS NOT NULL;

-- The org default. qty 0% is deliberate: a receiving clerk types the quantity they counted, so a billed
-- quantity that differs from it is a real disagreement with the vendor, not measurement noise. Branches that
-- buy bulk by weight raise it.
INSERT INTO purchasing_match_tolerance (branch_id, qty_tolerance_pct, price_tolerance_pct, price_tolerance_abs_cents)
VALUES (NULL, 0.000, 2.000, 500);

-- ── vendor bills ─────────────────────────────────────────────────────────────────────────────────────────

CREATE TABLE purchasing_bills (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- The VENDOR's invoice number as printed on their paper. Unique per vendor, which is the duplicate-payment
  -- control: the same invoice cannot be entered twice under two clerks.
  bill_no        CITEXT NOT NULL,
  vendor_id      UUID NOT NULL REFERENCES vendors(id) ON DELETE RESTRICT,
  branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  po_id          UUID REFERENCES purchasing_pos(id) ON DELETE RESTRICT,
  status         TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','POSTED','VOID')),
  match_state    TEXT NOT NULL DEFAULT 'UNMATCHED'
                   CHECK (match_state IN ('UNMATCHED','MATCHED','VARIANCE','SHORT_BILLED')),
  bill_date      DATE NOT NULL DEFAULT CURRENT_DATE,
  due_date       DATE,
  -- CAD, and no currency column — same reason as purchasing_pos (0120).
  subtotal_cents BIGINT NOT NULL DEFAULT 0,
  tax_cents      BIGINT NOT NULL DEFAULT 0 CHECK (tax_cents >= 0),
  total_cents    BIGINT NOT NULL DEFAULT 0,
  -- The acceptance a VARIANCE bill needs before it may post: who accepted it, when, and why. A NULL here on
  -- a POSTED bill in VARIANCE would mean the gate was bypassed, so the service never writes one.
  variance_accepted_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  variance_accepted_at   TIMESTAMPTZ,
  variance_accepted_note TEXT NOT NULL DEFAULT '',
  gl_journal_id  UUID REFERENCES gl_journals(id) ON DELETE RESTRICT,
  posted_at      TIMESTAMPTZ,
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (vendor_id, bill_no)
);

CREATE INDEX idx_purchasing_bills_po ON purchasing_bills(po_id);
CREATE INDEX idx_purchasing_bills_branch_status ON purchasing_bills(branch_id, status);

CREATE TABLE purchasing_bill_lines (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bill_id              UUID NOT NULL REFERENCES purchasing_bills(id) ON DELETE CASCADE,
  line_no              INTEGER NOT NULL,
  -- A bill line WITHOUT a po_line_id is a charge with no PO behind it (a fuel surcharge, a pallet deposit).
  -- It has nothing to match against, so it is never MATCHED — it is UNMATCHED and it lands wholly on the
  -- variance account when the bill posts. Making that legal in the schema and visible in the state is
  -- better than forcing a clerk to invent a PO line for it.
  po_line_id           UUID REFERENCES purchasing_po_lines(id) ON DELETE RESTRICT,
  description          TEXT NOT NULL DEFAULT '',
  qty_billed_base      DECIMAL(19,4) NOT NULL CHECK (qty_billed_base > 0),
  unit_cost_cents      BIGINT NOT NULL DEFAULT 0 CHECK (unit_cost_cents >= 0),
  line_total_cents     BIGINT NOT NULL DEFAULT 0,
  match_state          TEXT NOT NULL DEFAULT 'UNMATCHED'
                         CHECK (match_state IN ('UNMATCHED','MATCHED','VARIANCE','SHORT_BILLED')),
  -- Signed, both of them, and the sign is the whole diagnosis: qty_variance > 0 = billed for more than
  -- landed; price_variance > 0 = charged more than agreed.
  qty_variance_base    DECIMAL(19,4) NOT NULL DEFAULT 0,
  price_variance_cents BIGINT NOT NULL DEFAULT 0,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (bill_id, line_no)
);

CREATE INDEX idx_purchasing_bill_lines_bill ON purchasing_bill_lines(bill_id);
CREATE INDEX idx_purchasing_bill_lines_po_line ON purchasing_bill_lines(po_line_id);

-- ── instalments ──────────────────────────────────────────────────────────────────────────────────────────
--
-- A large hardscape buy is routinely settled in stages (deposit on order, balance on delivery; or three
-- equal payments over a season). The bill is ONE payable posted once; the instalments are the schedule
-- against it. Σ(amount_cents) must equal the bill total — enforced in the service where a helpful error can
-- name the shortfall, and asserted in the tests, rather than by a trigger that can only say "constraint
-- violated".
CREATE TABLE purchasing_bill_instalments (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bill_id      UUID NOT NULL REFERENCES purchasing_bills(id) ON DELETE CASCADE,
  seq          INTEGER NOT NULL CHECK (seq > 0),
  due_date     DATE NOT NULL,
  amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
  status       TEXT NOT NULL DEFAULT 'SCHEDULED' CHECK (status IN ('SCHEDULED','PAID','CANCELLED')),
  paid_at      TIMESTAMPTZ,
  note         TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (bill_id, seq)
);

CREATE INDEX idx_purchasing_instalments_bill ON purchasing_bill_instalments(bill_id);
CREATE INDEX idx_purchasing_instalments_due ON purchasing_bill_instalments(due_date) WHERE status = 'SCHEDULED';
