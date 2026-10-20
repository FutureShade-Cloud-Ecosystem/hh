-- 0413_revoked_sessions.sql
-- Revocable core session tokens (HH-678). Stores the jti and expiry of
-- explicitly signed out or revoked core session JWTs so revocations
-- survive a core restart and are shared across instances. Rows past
-- exp are swept opportunistically on write.
-- Owner module: internal/identity.

CREATE TABLE IF NOT EXISTS revoked_sessions (
    jti        TEXT PRIMARY KEY,
    exp        TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_revoked_sessions_exp ON revoked_sessions (exp);
