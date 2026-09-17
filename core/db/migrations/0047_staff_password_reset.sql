-- 0047: staff (ERP) admin-generated password reset (DIB-186).
-- Mirrors 0030 portal_password_resets, but the ERP flow is ADMIN-GENERATED and
-- link-based: an admin mints a reset link for a user and hands it over out-of-band.
-- There is NO email/outbox — the raw token is returned to the admin directly (the
-- INVITE pattern), so this table stores only the sha256 hash. Single-use + time-boxed.
CREATE TABLE users_password_resets (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,           -- sha256(raw token); the raw is handed to the admin, never stored
  expires_at  TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,                     -- non-null = used (single-use)
  created_by  UUID REFERENCES users(id),       -- the admin who generated the link
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_users_password_resets_user ON users_password_resets (user_id);
