-- 0005: immutable double-entry stock ledger + balance projection + layer-capable valuation.
-- Spec: DATA_MODEL.md §5 (G-LOCK-004) · Traces: R1, R2, BT-A-002 (OPEN Q-001: FIFO-vs-avg is a
-- service policy switch; schema identical under both). Append-only discipline: app role gets
-- INSERT/SELECT only on stock_moves once roles exist (W1); corrections = compensating moves.

CREATE TABLE stock_moves (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id       UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  from_location_id UUID REFERENCES locations(id) ON DELETE RESTRICT,
  to_location_id   UUID REFERENCES locations(id) ON DELETE RESTRICT,
  qty_base         DECIMAL(19,4) NOT NULL CHECK (qty_base > 0),
  qty_entered      DECIMAL(19,4) NOT NULL,
  uom_entered      CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  move_type        TEXT NOT NULL CHECK (move_type IN
                     ('RECEIPT','TRANSFER','PICK','SHIP','ADJUST','COUNT','BREAK_PALLET','RETURN')),
  unit_cost_cents  BIGINT,
  value_cents      BIGINT,
  weight_kg_total  DECIMAL(19,4),
  source_type      TEXT,
  source_ref_id    UUID,
  reason           TEXT NOT NULL DEFAULT '',
  actor_id         UUID REFERENCES users(id) ON DELETE SET NULL,
  branch_id        UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  metadata         JSONB NOT NULL DEFAULT '{}',
  occurred_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (from_location_id IS NOT NULL OR to_location_id IS NOT NULL)
);

CREATE INDEX idx_stock_moves_product_time ON stock_moves(product_id, occurred_at DESC);
CREATE INDEX idx_stock_moves_branch_time  ON stock_moves(branch_id, occurred_at DESC);
CREATE INDEX idx_stock_moves_from         ON stock_moves(from_location_id);
CREATE INDEX idx_stock_moves_to           ON stock_moves(to_location_id);
CREATE INDEX idx_stock_moves_source       ON stock_moves(source_type, source_ref_id);

CREATE TABLE stock_balances (
  product_id     UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  location_id    UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  on_hand_base   DECIMAL(19,4) NOT NULL DEFAULT 0,
  allocated_base DECIMAL(19,4) NOT NULL DEFAULT 0,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (product_id, location_id),
  CHECK (allocated_base >= 0),
  CHECK (on_hand_base >= allocated_base)
);

CREATE INDEX idx_stock_balances_branch ON stock_balances(branch_id);

CREATE TABLE cost_layers (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id      UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  branch_id       UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  received_at     TIMESTAMPTZ NOT NULL,
  qty_received    DECIMAL(19,4) NOT NULL CHECK (qty_received > 0),
  qty_remaining   DECIMAL(19,4) NOT NULL CHECK (qty_remaining >= 0),
  unit_cost_cents BIGINT NOT NULL,
  source_move_id  UUID NOT NULL REFERENCES stock_moves(id) ON DELETE RESTRICT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_cost_layers_open ON cost_layers(product_id, branch_id, received_at)
  WHERE qty_remaining > 0;

CREATE TABLE product_branch_costs (
  product_id          UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  branch_id           UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  avg_unit_cost_cents BIGINT NOT NULL DEFAULT 0,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (product_id, branch_id)
);
