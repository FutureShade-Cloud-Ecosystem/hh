-- 0008: quotes + orders (separate tables; conversion is a link, not a mutation).
-- Spec: DATA_MODEL.md §8 (G-LOCK-004) · Traces: BT-A-025 (BisTrack merges them; we split for
-- zero-trust ownership), M2 entered/base qty trio, W16 COD-gate + W12 multi-ship hooks noted.

CREATE SEQUENCE quote_no_seq START 1;
CREATE SEQUENCE order_no_seq START 1;

CREATE TABLE quotes (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_no       CITEXT NOT NULL UNIQUE,
  customer_id    UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  status         TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','SENT','ACCEPTED','CONVERTED','EXPIRED','LOST')),
  valid_until    DATE,
  subtotal_cents BIGINT NOT NULL DEFAULT 0,
  tax_cents      BIGINT NOT NULL DEFAULT 0,
  total_cents    BIGINT NOT NULL DEFAULT 0,
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  metadata       JSONB NOT NULL DEFAULT '{}',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_quotes_customer ON quotes(customer_id);
CREATE INDEX idx_quotes_branch_status ON quotes(branch_id, status);

CREATE TABLE quote_lines (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_id         UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
  line_no          INTEGER NOT NULL,
  product_id       UUID REFERENCES products(id) ON DELETE RESTRICT,
  description      TEXT NOT NULL DEFAULT '',
  qty_entered      DECIMAL(19,4) NOT NULL,
  uom_entered      CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  qty_base         DECIMAL(19,4) NOT NULL,
  unit_price_cents BIGINT NOT NULL DEFAULT 0,
  price_uom        CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  line_total_cents BIGINT NOT NULL DEFAULT 0,
  metadata         JSONB NOT NULL DEFAULT '{}',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (quote_id, line_no)
);

CREATE TABLE orders (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_no               CITEXT NOT NULL UNIQUE,
  customer_id            UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  branch_id              UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  quote_id               UUID REFERENCES quotes(id) ON DELETE SET NULL,
  status                 TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN
                           ('DRAFT','CONFIRMED','PICKING','READY','PARTIALLY_FULFILLED','FULFILLED','INVOICED','CLOSED','CANCELLED')),
  fulfillment            TEXT NOT NULL DEFAULT 'PICKUP' CHECK (fulfillment IN ('PICKUP','DELIVERY','WILL_CALL')),
  requested_date         DATE,
  subtotal_cents         BIGINT NOT NULL DEFAULT 0,
  tax_cents              BIGINT NOT NULL DEFAULT 0,
  total_cents            BIGINT NOT NULL DEFAULT 0,
  deposit_required_cents BIGINT NOT NULL DEFAULT 0,
  created_by             UUID REFERENCES users(id) ON DELETE SET NULL,
  metadata               JSONB NOT NULL DEFAULT '{}',
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orders_customer ON orders(customer_id);
CREATE INDEX idx_orders_branch_status ON orders(branch_id, status);
CREATE INDEX idx_orders_quote ON orders(quote_id);

CREATE TABLE order_lines (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id         UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  line_no          INTEGER NOT NULL,
  product_id       UUID REFERENCES products(id) ON DELETE RESTRICT,
  description      TEXT NOT NULL DEFAULT '',
  qty_entered      DECIMAL(19,4) NOT NULL,
  uom_entered      CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  qty_base         DECIMAL(19,4) NOT NULL,
  allocated_base   DECIMAL(19,4) NOT NULL DEFAULT 0,
  fulfilled_base   DECIMAL(19,4) NOT NULL DEFAULT 0,
  unit_price_cents BIGINT NOT NULL DEFAULT 0,
  price_uom        CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  line_total_cents BIGINT NOT NULL DEFAULT 0,
  is_special_order BOOLEAN NOT NULL DEFAULT FALSE,
  metadata         JSONB NOT NULL DEFAULT '{}',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (order_id, line_no),
  CHECK (fulfilled_base <= qty_base),
  CHECK (allocated_base >= 0)
);

CREATE INDEX idx_order_lines_product ON order_lines(product_id);
