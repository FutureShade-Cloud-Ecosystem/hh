-- 0010: payments + allocations + till sessions. Till first (payments FK it).
-- Spec: DATA_MODEL.md §10 (G-LOCK-004) · A-03 (W4) builds drawer ops; A-05 (W5) NSF flow;
-- RUN gateway (W16) fills gateway_ref.

CREATE SEQUENCE payment_no_seq START 1;

CREATE TABLE till_sessions (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id        UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  station          TEXT NOT NULL DEFAULT 'MAIN',
  status           TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','CLOSED')),
  opened_by        UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  opened_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  float_cents      BIGINT NOT NULL DEFAULT 0,
  closed_by        UUID REFERENCES users(id) ON DELETE RESTRICT,
  closed_at        TIMESTAMPTZ,
  counted_cents    BIGINT,
  over_short_cents BIGINT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_till_sessions_branch ON till_sessions(branch_id, status);

CREATE TABLE payments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_no      CITEXT NOT NULL UNIQUE,
  customer_id     UUID REFERENCES customers(id) ON DELETE RESTRICT,
  method          TEXT NOT NULL CHECK (method IN ('CASH','CARD','CHEQUE','EFT','OTHER')),
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  status          TEXT NOT NULL DEFAULT 'RECEIVED' CHECK (status IN ('RECEIVED','DEPOSITED','BOUNCED','REFUNDED','VOID')),
  received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  branch_id       UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  till_session_id UUID REFERENCES till_sessions(id) ON DELETE SET NULL,
  gateway_ref     TEXT NOT NULL DEFAULT '',
  received_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  metadata        JSONB NOT NULL DEFAULT '{}',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payments_customer ON payments(customer_id);
CREATE INDEX idx_payments_branch_time ON payments(branch_id, received_at DESC);
CREATE INDEX idx_payments_till ON payments(till_session_id);

CREATE TABLE payment_allocations (
  payment_id   UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
  invoice_id   UUID NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,
  amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (payment_id, invoice_id)
);

CREATE INDEX idx_payment_alloc_invoice ON payment_allocations(invoice_id);
