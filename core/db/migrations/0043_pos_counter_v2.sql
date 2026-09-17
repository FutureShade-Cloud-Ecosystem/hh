-- 0043: POS Counter V2 — the client-confirmed transaction types the W4 counter didn't carry
-- (Q-042 list, answered 2026-07-07): COD with an off toggle, deposits on order, the PRICING ORDER
-- printable document, returns (cash + account) with restock + optional logistics pickup, parked
-- orders, and the Q-044 payment-matching attribution. Owner module: internal/pos (returns/parked/
-- pricing/policy) — allocations attribution is a column on the 0010 payments table set.
-- Conventions: UUID PKs · money BIGINT *_cents · TEXT + CHECK (no ENUMs) · TIMESTAMPTZ · explicit ON DELETE.

-- ── 1. POS policy — DATA, not code consts (till_policy/gl_policy precedent). branch NULL = org default.
CREATE TABLE pos_policy (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id   UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE,  -- NULL = org-wide default row
  cod_enabled BOOLEAN NOT NULL DEFAULT TRUE,  -- the client's "able to turn this off so people can't do it"
  notes       TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO pos_policy (branch_id, cod_enabled, notes)
VALUES (NULL, TRUE, 'org default — COD on; flip per branch or org-wide from the POS admin');

-- ── 2. Parked orders — a saved ticket that has NOT become an order (no stock effect, no AR).
--     "make an order to save product for later, or to wait for payment". Stock is NOT reserved in V2
--     (reservation is a scoped follow-up); prices re-derive from the engine on resume so a parked
--     ticket never rings at a stale price.
CREATE SEQUENCE parked_ticket_no_seq START 1;
CREATE TABLE pos_parked_tickets (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  ticket_no   CITEXT NOT NULL UNIQUE,          -- '#PARK-n'
  branch_id   UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  customer_id UUID REFERENCES customers(id) ON DELETE SET NULL,   -- NULL = walk-in
  sale_type   TEXT NOT NULL DEFAULT 'COLLECT_NOW',
  notes       TEXT NOT NULL DEFAULT '',
  lines       JSONB NOT NULL,                  -- [{product_id, qty_entered, uom_entered}] — re-priced on resume
  status      TEXT NOT NULL DEFAULT 'PARKED' CHECK (status IN ('PARKED','RESUMED','DISCARDED')),
  parked_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  resumed_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  resumed_at  TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_parked_branch_status ON pos_parked_tickets(branch_id, status);

-- ── 3. Pricing orders — the client's "official document we can make and print for customers to show
--     them quick pricing and price comparisons" — NOT a quote (no acceptance lifecycle), NOT an order
--     (no stock/AR). Lines snapshot the engine price AND the list price so the printed doc carries the
--     comparison; validity window on the face.
CREATE SEQUENCE pricing_order_no_seq START 1;
CREATE TABLE pricing_orders (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  doc_no         CITEXT NOT NULL UNIQUE,        -- '#PRICE-n'
  branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  customer_id    UUID REFERENCES customers(id) ON DELETE SET NULL,   -- NULL = walk-in / prospect
  customer_name  TEXT NOT NULL DEFAULT '',      -- printable name even for walk-ins
  notes          TEXT NOT NULL DEFAULT '',
  valid_until    DATE,                          -- printed on the face; prices are indicative until rung
  subtotal_cents BIGINT NOT NULL DEFAULT 0,
  tax_cents      BIGINT NOT NULL DEFAULT 0,
  total_cents    BIGINT NOT NULL DEFAULT 0,
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE pricing_order_lines (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pricing_order_id UUID NOT NULL REFERENCES pricing_orders(id) ON DELETE CASCADE,
  line_no          INTEGER NOT NULL,
  product_id       UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  sku              TEXT NOT NULL DEFAULT '',
  name             TEXT NOT NULL DEFAULT '',
  qty_entered      DECIMAL(19,4) NOT NULL,
  uom_entered      TEXT NOT NULL,
  unit_price_cents BIGINT NOT NULL,             -- the engine price offered
  list_price_cents BIGINT NOT NULL DEFAULT 0,   -- the list comparison the client asked to show
  line_total_cents BIGINT NOT NULL,
  UNIQUE (pricing_order_id, line_no)
);

-- ── 4. Returns — cash + account, against ONE original POS sale. Restock is a real RETURN stock move
--     (at the current weighted average, so the average is unchanged); the money side reverses revenue
--     + HST against the refund rail (drawer cash / card rail / account credit). Optional logistics
--     pickup fields carry the client's "return pick up scheduled by logistics" until the dispatch
--     module exists (the queue below is its hand-off surface).
CREATE SEQUENCE return_no_seq START 1;
CREATE TABLE pos_returns (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  return_no             CITEXT NOT NULL UNIQUE,   -- '#RET-n'
  order_id              UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  invoice_id            UUID NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,
  customer_id           UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  branch_id             UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  refund_method         TEXT NOT NULL CHECK (refund_method IN ('CASH','CARD','ACCOUNT_CREDIT')),
  status                TEXT NOT NULL DEFAULT 'COMMITTED' CHECK (status IN ('COMMITTED')),
  subtotal_cents        BIGINT NOT NULL,
  tax_cents             BIGINT NOT NULL,
  total_cents           BIGINT NOT NULL,
  reason                TEXT NOT NULL DEFAULT '',
  restock               BOOLEAN NOT NULL DEFAULT TRUE,
  refund_payment_id     UUID REFERENCES payments(id) ON DELETE SET NULL,  -- the REFUNDED row (cash/card)
  till_session_id       UUID REFERENCES till_sessions(id) ON DELETE SET NULL, -- drawer the cash left
  pickup_required       BOOLEAN NOT NULL DEFAULT FALSE,
  pickup_site           TEXT NOT NULL DEFAULT '',
  pickup_requested_date DATE,
  pickup_status         TEXT NOT NULL DEFAULT 'NONE'
                          CHECK (pickup_status IN ('NONE','REQUESTED','SCHEDULED','DONE')),
  idempotency_key       TEXT NOT NULL DEFAULT '',
  created_by            UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ux_pos_returns_idem ON pos_returns(idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX idx_pos_returns_order ON pos_returns(order_id);
CREATE INDEX idx_pos_returns_pickup ON pos_returns(branch_id, pickup_status)
  WHERE pickup_status IN ('REQUESTED','SCHEDULED');
CREATE TABLE pos_return_lines (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  return_id        UUID NOT NULL REFERENCES pos_returns(id) ON DELETE CASCADE,
  order_line_id    UUID NOT NULL REFERENCES order_lines(id) ON DELETE RESTRICT,
  product_id       UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  qty_base         DECIMAL(19,4) NOT NULL CHECK (qty_base > 0),
  unit_price_cents BIGINT NOT NULL,
  line_total_cents BIGINT NOT NULL,
  tax_cents        BIGINT NOT NULL DEFAULT 0,
  reason_code      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_pos_return_lines_return ON pos_return_lines(return_id);
-- Over-return guard: enforced in the service inside the tx (sum of prior returned qty per order_line
-- FOR UPDATE), not by constraint — partial returns across multiple visits are legitimate.

-- ── 5. Q-044 payment-matching attribution — WHO applied a payment to WHICH invoice is now stamped on
--     the allocation itself (created_at already carries when).
ALTER TABLE payment_allocations
  ADD COLUMN allocated_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- ── 5b. COD joins the pos_sale sale_type vocabulary (additive fix: the 0029 CHECK predates the COD
--     sale type this migration's features carry — without it a COD ring cannot persist its facet row).
--     COD = the client's "cash on DEMAND" pay-as-you-go posture, not cash-on-delivery.
ALTER TABLE pos_sale DROP CONSTRAINT pos_sale_sale_type_check;
ALTER TABLE pos_sale ADD CONSTRAINT pos_sale_sale_type_check
  CHECK (sale_type IN ('COLLECT_NOW','COLLECT_LATER','DELIVERED','COD'));

-- ── 6. Posting rules for the two new outbox topics (literal-topic naming per the 0032 two-name map).
--     pos.return.committed — the money reversal: DR revenue + DR HST / CR the refund rail.
--     order.returned — the stock side: DR inventory / CR COGS at the current weighted average.
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('pos.return.committed', 'revenue',        '4000'),
  ('pos.return.committed', 'hst',            '2200'),
  ('pos.return.committed', 'undeposited',    '1020'),
  ('pos.return.committed', 'card_clearing',  '1010'),
  ('pos.return.committed', 'ar',             '1100'),
  ('order.returned',       'inventory',      '1200'),
  ('order.returned',       'cogs',           '5000')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
