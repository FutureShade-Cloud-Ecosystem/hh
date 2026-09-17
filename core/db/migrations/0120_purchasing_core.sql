-- 0120: purchasing core — branch-scoped PO numbering, the PO aggregate, and the receiving document.
--
-- WHAT THIS IS. Wave 4 gives Dibbits a real purchase order: a document a buyer drafts, orders from a vendor,
-- and receives against line by line. Until now the only purchasing artefacts in the program were the 0011
-- shells (vendors / purchase_orders / po_lines / po_receipts), which inventory's receive path reads and
-- writes but which nothing ever CREATED — there was no buyer-facing lifecycle at all.
--
-- ── WHY TWO SETS OF TABLES, AND WHAT EACH ONE IS ─────────────────────────────────────────────────────────
--
-- purchasing_pos / purchasing_po_lines are the PURCHASE ORDER: what we agreed to buy, at what price, from
-- whom. It carries the DRAFT → ORDERED → PARTIAL → RECEIVED lifecycle, the ordered quantity that the
-- three-way match measures against, and the running received/billed rollups.
--
-- The 0011 purchase_orders / po_lines rows this module writes are NOT a second copy of that. They are the
-- RECEIVING DOCUMENT (goods-received note): one per receipt event, whose lines carry exactly the quantities
-- landing on the yard RIGHT NOW. inventory.ReceivePOTx consumes such a document — it walks the lines,
-- writes the RECEIPT stock moves, maintains the weighted-average cost, and emits po.received so the GRNI
-- journal (DR 1200 / CR 2150) posts. That is the whole reason for the shape:
--
--   * STOCK ARITHMETIC STAYS INVENTORY'S. Purchasing never writes a stock_move, never touches a balance,
--     never computes a weighted average. It writes a document and hands inventory the id. Unwire that seam
--     and receiving refuses rather than quietly recording a receipt that moved nothing
--     (purchasing.ErrStockSeamUnwired).
--   * PARTIAL RECEIVING NEEDS NO NEW INVENTORY CODE. inventory's exported write path receives everything
--     outstanding on the document it is given. A document whose lines ARE the partial quantities is
--     therefore a partial receipt, expressed in the vocabulary inventory already speaks.
--   * LANDED COST TIES IN FOR FREE. internal/landedcost apportions over the RECEIVED lines of a PO id. Hand
--     it the receiving document's id and it apportions over exactly the lines of that one receipt — which is
--     what a freight invoice for one truck actually covers.
--
-- The receiving document's number is the PO's number with a receipt suffix (#PO-TRT-4/R2), so nobody reading
-- purchase_orders can mistake it for a second purchase order, and the receipt it belongs to is legible from
-- the number alone. purchasing_receipts.stock_doc_id is the explicit link back.
--
-- ── STATUS VOCABULARY ────────────────────────────────────────────────────────────────────────────────────
-- purchasing_pos.status is the ruled lifecycle: DRAFT, ORDERED, PARTIAL, RECEIVED (+ CLOSED, CANCELLED).
-- The 0011 purchase_orders CHECK spells its equivalents SUBMITTED / PARTIALLY_RECEIVED; that column is not
-- touched here and keeps its own vocabulary, because inventory writes it (POPartiallyReceived / POReceived)
-- and renaming it would be an edit to a shipped module. A receiving document only ever passes through
-- DRAFT → RECEIVED anyway: it is created holding exactly what is being received, so it is never partial.

-- ── branch-scoped PO numbering (the 0087 pattern) ────────────────────────────────────────────────────────
--
-- Same rule, same reasons as order numbering: one mint, per-branch counters in a TABLE rather than a
-- sequence. A table needs no DDL when a branch opens, two branches never contend on one counter, and the
-- row lock inside the caller's transaction means a rolled-back draft returns its number instead of leaving
-- a hole in the PO register. 0011 created a global `po_no_seq`; it is left in place (nothing has ever called
-- it) and the seed below starts every branch above its last value so a branch-coded number can never read
-- as older than a legacy one.

CREATE TABLE po_no_sequences (
  branch_id  UUID PRIMARY KEY REFERENCES locations(id) ON DELETE RESTRICT,
  last_value BIGINT NOT NULL DEFAULT 0 CHECK (last_value >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO po_no_sequences (branch_id, last_value)
SELECT l.id, GREATEST((SELECT last_value FROM po_no_seq), 0)
  FROM locations l
 WHERE l.loc_type = 'BRANCH'
ON CONFLICT (branch_id) DO NOTHING;

-- next_po_no mints '#PO-<BRANCH CODE>-<n>'. The argument may be a BRANCH location or any location beneath
-- one — either resolves to the owning branch's code, so the number always names a real branch. An unknown
-- branch RAISES: a PO that cannot be branch-stamped must not be written at all.
CREATE FUNCTION next_po_no(p_branch_id UUID) RETURNS TEXT AS $$
DECLARE
  v_code TEXT;
  v_root UUID;
  v_n    BIGINT;
BEGIN
  SELECT CASE WHEN l.loc_type = 'BRANCH' THEN l.id ELSE l.branch_id END
    INTO v_root
    FROM locations l
   WHERE l.id = p_branch_id;
  IF v_root IS NULL THEN
    RAISE EXCEPTION 'next_po_no: unknown branch %', p_branch_id;
  END IF;

  SELECT l.code::text INTO v_code FROM locations l WHERE l.id = v_root;
  IF v_code IS NULL OR v_code = '' THEN
    RAISE EXCEPTION 'next_po_no: branch % has no code', v_root;
  END IF;

  INSERT INTO po_no_sequences (branch_id, last_value)
       VALUES (v_root, 1)
  ON CONFLICT (branch_id)
  DO UPDATE SET last_value = po_no_sequences.last_value + 1, updated_at = now()
    RETURNING last_value INTO v_n;

  RETURN '#PO-' || upper(v_code) || '-' || v_n::text;
END;
$$ LANGUAGE plpgsql;

-- ── the purchase order ───────────────────────────────────────────────────────────────────────────────────

CREATE TABLE purchasing_pos (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  po_no       CITEXT NOT NULL UNIQUE,
  vendor_id   UUID NOT NULL REFERENCES vendors(id) ON DELETE RESTRICT,
  branch_id   UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  status      TEXT NOT NULL DEFAULT 'DRAFT'
                CHECK (status IN ('DRAFT','ORDERED','PARTIAL','RECEIVED','CLOSED','CANCELLED')),
  -- NO currency COLUMN, DELIBERATELY. internal/platform/db's TestInvariant_GLStaysCADOnUSDPO allowlists the
  -- DX-19 foreign-currency exception to exactly three tables — payment_config, purchase_orders, landed_costs
  -- — and everything else in the program is CAD. A Wave 4 purchase order is therefore CAD, and the receiving
  -- document it writes is created CAD / fx 1.0. Widening that allowlist to purchasing_pos + purchasing_bills
  -- is a one-line edit to a test in another package's fence; it is PARKED for the union and named in
  -- REPORT-W4-C.md. Until it lands, a USD purchase order is not enterable here — which is a smaller lie than
  -- a currency column nothing converts.
  expected_at DATE,
  ordered_at  TIMESTAMPTZ,
  closed_at   TIMESTAMPTZ,
  notes       TEXT NOT NULL DEFAULT '',
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_purchasing_pos_branch_status ON purchasing_pos(branch_id, status);
CREATE INDEX idx_purchasing_pos_vendor ON purchasing_pos(vendor_id);

CREATE TABLE purchasing_po_lines (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  po_id             UUID NOT NULL REFERENCES purchasing_pos(id) ON DELETE CASCADE,
  line_no           INTEGER NOT NULL,
  product_id        UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  description       TEXT NOT NULL DEFAULT '',
  qty_ordered_base  DECIMAL(19,4) NOT NULL CHECK (qty_ordered_base > 0),
  uom               CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  unit_cost_cents   BIGINT NOT NULL DEFAULT 0 CHECK (unit_cost_cents >= 0),
  -- Rollups maintained by the receiving + billing paths. received is CHECKed against ordered here as well as
  -- guarded in the service, because an over-receipt that slipped past the service would silently overstate
  -- inventory; the constraint makes that a loud failure instead.
  qty_received_base DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (qty_received_base >= 0),
  qty_billed_base   DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (qty_billed_base >= 0),
  billed_cents      BIGINT NOT NULL DEFAULT 0,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (po_id, line_no),
  CHECK (qty_received_base <= qty_ordered_base)
);

CREATE INDEX idx_purchasing_po_lines_po ON purchasing_po_lines(po_id);
CREATE INDEX idx_purchasing_po_lines_product ON purchasing_po_lines(product_id);

-- ── receipts ─────────────────────────────────────────────────────────────────────────────────────────────
--
-- One row per receiving EVENT. stock_doc_id is the 0011 receiving document inventory converted into stock
-- moves; it is NOT NULL by the time the transaction commits (the row and the stock write happen together or
-- not at all), but the column is nullable so the receipt row can be written before the document id is known
-- without a chicken-and-egg deferral.
CREATE TABLE purchasing_receipts (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  po_id          UUID NOT NULL REFERENCES purchasing_pos(id) ON DELETE RESTRICT,
  seq            INTEGER NOT NULL CHECK (seq > 0),
  grn_no         CITEXT NOT NULL UNIQUE,
  stock_doc_id   UUID REFERENCES purchase_orders(id) ON DELETE RESTRICT,
  landed_cost_id UUID REFERENCES landed_costs(id) ON DELETE SET NULL,
  value_cad_cents BIGINT NOT NULL DEFAULT 0,
  received_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  notes          TEXT NOT NULL DEFAULT '',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (po_id, seq)
);

CREATE INDEX idx_purchasing_receipts_po ON purchasing_receipts(po_id);

CREATE TABLE purchasing_receipt_lines (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  receipt_id       UUID NOT NULL REFERENCES purchasing_receipts(id) ON DELETE CASCADE,
  po_line_id       UUID NOT NULL REFERENCES purchasing_po_lines(id) ON DELETE RESTRICT,
  qty_base         DECIMAL(19,4) NOT NULL CHECK (qty_base > 0),
  unit_cost_cents  BIGINT NOT NULL DEFAULT 0,
  value_cents      BIGINT NOT NULL DEFAULT 0,
  stock_po_line_id UUID REFERENCES po_lines(id) ON DELETE SET NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_purchasing_receipt_lines_receipt ON purchasing_receipt_lines(receipt_id);
CREATE INDEX idx_purchasing_receipt_lines_po_line ON purchasing_receipt_lines(po_line_id);
