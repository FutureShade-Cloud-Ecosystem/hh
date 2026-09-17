-- 0019: CP-07 — contractor portal multi-user auth.
-- Per-person logins for a contractor company (customers 1:N), three roles, and a
-- single-use, time-boxed invite/accept flow. Decisions: D-025 (auth backend in the
-- greenfield ERP serving the existing React portal; contractor JWT), D-026 (invite
-- email deferred — accept-link returned in the admin response + CRM signal to outbox).
-- Conventions: UUID PKs · CITEXT email · TEXT + CHECK (no ENUMs) · explicit ON DELETE.

CREATE TABLE portal_users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id   UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  email         CITEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL CHECK (role IN ('account_admin','buyer','field_crew')),
  status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','pending')),
  last_login_at TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_portal_users_customer ON portal_users (customer_id);

CREATE TABLE portal_invites (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id  UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  email        CITEXT NOT NULL,
  role         TEXT NOT NULL CHECK (role IN ('account_admin','buyer','field_crew')),
  token_hash   TEXT NOT NULL UNIQUE,           -- sha256(raw token); raw is shown once to the admin
  expires_at   TIMESTAMPTZ NOT NULL,
  accepted_at  TIMESTAMPTZ,                     -- non-null = consumed (single-use)
  created_by   UUID REFERENCES portal_users(id) ON DELETE SET NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_portal_invites_customer ON portal_invites (customer_id);
