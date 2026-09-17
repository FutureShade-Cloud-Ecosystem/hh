-- 0027: contractor-portal commerce — stored payment methods, password resets, and the
-- additive columns the BFF feature surfaces need. Forward-only, additive (no table is
-- recreated). The pay flow REUSES payments + payment_allocations + customer_transactions
-- (0010/0009) — only the vault metadata is new here.
-- Spec: contractor-portal-plan §4.2 (payments/Run, SAQ A) · §4.6 (advisory contact seed,
-- self-service). Conventions: UUID PKs · CITEXT email · TEXT + CHECK (no ENUMs) · explicit ON DELETE.

-- Vaulted payment methods (P3.2). We store ONLY an opaque Run vault token + display
-- metadata — never PAN/account/routing (PCI SAQ A). One Run "customer" per contractor
-- account; gateway names which processor minted the token so a Stub→Run cutover is data-safe.
CREATE TABLE portal_payment_methods (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id               UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  created_by_portal_user_id UUID REFERENCES portal_users(id) ON DELETE SET NULL,
  gateway                   TEXT NOT NULL DEFAULT 'stub',  -- stub | run (which processor vaulted it)
  gateway_token             TEXT NOT NULL,                 -- opaque durable vault token; never a PAN
  method_type               TEXT NOT NULL CHECK (method_type IN ('CARD','BANK')),
  brand                     TEXT NOT NULL DEFAULT '',      -- VISA / MASTERCARD / bank name
  last4                     TEXT NOT NULL DEFAULT '',
  exp_month                 INTEGER,                       -- card only; NULL for bank/ACH
  exp_year                  INTEGER,
  is_default                BOOLEAN NOT NULL DEFAULT FALSE,
  status                    TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','removed')),
  metadata                  JSONB NOT NULL DEFAULT '{}',
  created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_portal_payment_methods_customer ON portal_payment_methods (customer_id) WHERE status = 'active';
-- At most one default method per account (partial unique; ignores soft-deleted rows).
CREATE UNIQUE INDEX uq_portal_payment_methods_default
  ON portal_payment_methods (customer_id) WHERE is_default AND status = 'active';

-- Self-service password resets (M2.2). Mirrors the invite primitive: store only the
-- sha256 hash of the raw token; single-use (consumed_at) and time-boxed (expires_at);
-- enumeration-safe issue. A successful confirm must revoke existing sessions (app-side).
CREATE TABLE portal_password_resets (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  portal_user_id UUID NOT NULL REFERENCES portal_users(id) ON DELETE CASCADE,
  token_hash     TEXT NOT NULL UNIQUE,           -- sha256(raw token); raw is delivered by email/D-026
  expires_at     TIMESTAMPTZ NOT NULL,
  consumed_at    TIMESTAMPTZ,                     -- non-null = used (single-use)
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_portal_password_resets_user ON portal_password_resets (portal_user_id);

-- Delivery instructions on a (future-scheduled) delivery — the one portal-owned write on
-- orders (CP-06). Nullable/additive: existing orders carry no note.
ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS delivery_instructions TEXT;

-- One-way advisory link from a portal user to the ERP contact that seeded them (§4.6.2).
-- ON DELETE SET NULL: removing the source contact must never cascade-delete a live login.
ALTER TABLE portal_users
  ADD COLUMN IF NOT EXISTS source_contact_id UUID REFERENCES customer_contacts(id) ON DELETE SET NULL;

-- Per-user notification preferences (consumed by the notification engine, P2.6). Additive
-- with a safe empty default so existing users need no backfill.
ALTER TABLE portal_users
  ADD COLUMN IF NOT EXISTS notification_prefs JSONB NOT NULL DEFAULT '{}';
