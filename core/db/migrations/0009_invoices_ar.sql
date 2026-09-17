-- 0009: invoices + AR subledger. Bill 60 holdback column from birth (M6/W6 builds the cockpit).
-- Spec: DATA_MODEL.md §9 (G-LOCK-004) · Finalize = one tx: status flip + GL journal + subledger row.
-- BT-A-021: BisTrack's 11.7M-row balance history stays archived; openings arrive as ADJUSTMENT rows.

CREATE SEQUENCE invoice_no_seq START 1;

CREATE TABLE invoices (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  invoice_no     CITEXT NOT NULL UNIQUE,
  customer_id    UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  order_id       UUID REFERENCES orders(id) ON DELETE SET NULL,
  branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  status         TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','FINALIZED','PARTIALLY_PAID','PAID','VOID')),
  issue_date     DATE NOT NULL DEFAULT CURRENT_DATE,
  due_date       DATE NOT NULL,
  subtotal_cents BIGINT NOT NULL DEFAULT 0,
  tax_cents      BIGINT NOT NULL DEFAULT 0,
  holdback_cents BIGINT NOT NULL DEFAULT 0,
  total_cents    BIGINT NOT NULL DEFAULT 0,
  balance_cents  BIGINT NOT NULL DEFAULT 0,
  finalized_at   TIMESTAMPTZ,
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  metadata       JSONB NOT NULL DEFAULT '{}',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_invoices_customer ON invoices(customer_id);
CREATE INDEX idx_invoices_branch_status ON invoices(branch_id, status);
CREATE INDEX idx_invoices_due ON invoices(due_date) WHERE status IN ('FINALIZED','PARTIALLY_PAID');

CREATE TABLE invoice_lines (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  invoice_id       UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
  line_no          INTEGER NOT NULL,
  order_line_id    UUID REFERENCES order_lines(id) ON DELETE SET NULL,
  product_id       UUID REFERENCES products(id) ON DELETE RESTRICT,
  description      TEXT NOT NULL DEFAULT '',
  qty_entered      DECIMAL(19,4) NOT NULL,
  uom_entered      CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  qty_base         DECIMAL(19,4) NOT NULL,
  unit_price_cents BIGINT NOT NULL DEFAULT 0,
  price_uom        CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  line_total_cents BIGINT NOT NULL DEFAULT 0,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (invoice_id, line_no)
);

CREATE TABLE customer_transactions (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id   UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  txn_type      TEXT NOT NULL CHECK (txn_type IN ('INVOICE','PAYMENT','CREDIT_MEMO','ADJUSTMENT','NSF','WRITE_OFF')),
  amount_cents  BIGINT NOT NULL,
  balance_after BIGINT NOT NULL,
  source_type   TEXT NOT NULL,
  source_ref_id UUID NOT NULL,
  branch_id     UUID REFERENCES locations(id) ON DELETE RESTRICT,
  memo          TEXT NOT NULL DEFAULT '',
  occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_cust_txn_customer_time ON customer_transactions(customer_id, occurred_at DESC);
CREATE INDEX idx_cust_txn_source ON customer_transactions(source_type, source_ref_id);
