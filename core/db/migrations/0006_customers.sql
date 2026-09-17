-- 0006: customer master + contacts. Global parties; home branch is a preference, not a fence.
-- Spec: DATA_MODEL.md §6 (G-LOCK-004) · Hooks: on_hold (03/W8 COD), tax_exempt (M5/W3).

CREATE TABLE customers (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_no         CITEXT NOT NULL UNIQUE,
  name               TEXT NOT NULL,
  customer_type      TEXT NOT NULL DEFAULT 'CONTRACTOR' CHECK (customer_type IN ('CONTRACTOR','RETAIL','BUILDER','GOVERNMENT','INTERNAL')),
  home_branch_id     UUID REFERENCES locations(id) ON DELETE SET NULL,
  payment_terms_days INTEGER NOT NULL DEFAULT 30,
  credit_limit_cents BIGINT NOT NULL DEFAULT 0,
  on_hold            BOOLEAN NOT NULL DEFAULT FALSE,
  tax_exempt         BOOLEAN NOT NULL DEFAULT FALSE,
  billing_address    JSONB NOT NULL DEFAULT '{}',
  is_active          BOOLEAN NOT NULL DEFAULT TRUE,
  metadata           JSONB NOT NULL DEFAULT '{}',
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_customers_name   ON customers(name);
CREATE INDEX idx_customers_active ON customers(is_active) WHERE is_active;

CREATE TABLE customer_contacts (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  email       CITEXT,
  phone       TEXT NOT NULL DEFAULT '',
  role        TEXT NOT NULL DEFAULT '',
  is_primary  BOOLEAN NOT NULL DEFAULT FALSE,
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_customer_contacts_customer ON customer_contacts(customer_id);
