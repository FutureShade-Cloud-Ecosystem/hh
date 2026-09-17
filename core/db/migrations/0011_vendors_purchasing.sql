-- 0011: vendors + purchase orders + receiving headers.
-- Spec: DATA_MODEL.md §11 (G-LOCK-004) · Receipt LINES are stock_moves rows
-- (move_type=RECEIPT, source_type='po_receipt', source_ref_id=po_line id) — one
-- source of truth for received qty + cost; 3-way match (M2+) reads moves ⋈ po_lines.

CREATE SEQUENCE po_no_seq START 1;

CREATE TABLE vendors (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vendor_no          CITEXT NOT NULL UNIQUE,
  name               TEXT NOT NULL,
  payment_terms_days INTEGER NOT NULL DEFAULT 30,
  is_edi             BOOLEAN NOT NULL DEFAULT FALSE,
  remit_address      JSONB NOT NULL DEFAULT '{}',
  is_active          BOOLEAN NOT NULL DEFAULT TRUE,
  metadata           JSONB NOT NULL DEFAULT '{}',
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE purchase_orders (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  po_no         CITEXT NOT NULL UNIQUE,
  vendor_id     UUID NOT NULL REFERENCES vendors(id) ON DELETE RESTRICT,
  branch_id     UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  status        TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','SUBMITTED','PARTIALLY_RECEIVED','RECEIVED','CLOSED','CANCELLED')),
  expected_at   DATE,
  freight_cents BIGINT NOT NULL DEFAULT 0,
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  metadata      JSONB NOT NULL DEFAULT '{}',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_po_vendor ON purchase_orders(vendor_id);
CREATE INDEX idx_po_branch_status ON purchase_orders(branch_id, status);

CREATE TABLE po_lines (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  po_id           UUID NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
  line_no         INTEGER NOT NULL,
  product_id      UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  qty_entered     DECIMAL(19,4) NOT NULL,
  uom_entered     CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  qty_base        DECIMAL(19,4) NOT NULL,
  received_base   DECIMAL(19,4) NOT NULL DEFAULT 0,
  unit_cost_cents BIGINT NOT NULL DEFAULT 0,
  cost_uom        CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (po_id, line_no),
  CHECK (received_base <= qty_base)
);

CREATE INDEX idx_po_lines_product ON po_lines(product_id);

CREATE TABLE po_receipts (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  po_id       UUID NOT NULL REFERENCES purchase_orders(id) ON DELETE RESTRICT,
  received_by UUID REFERENCES users(id) ON DELETE SET NULL,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  notes       TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_po_receipts_po ON po_receipts(po_id);
