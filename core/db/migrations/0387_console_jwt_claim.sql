-- 0387_console_jwt_claim.sql
-- Owner module: internal/identity (HH-4c item 6b, the sign in exchange).
-- Additive only: one new table, nothing else touched, starts empty.
-- One row per FB Console JWT the exchange has accepted: the key is the
-- SHA-256 of the JWT's decoded signature bytes (the canonical form, so two
-- spellings of one signature are one key) and the row lives until the JWT's
-- own exp, after which Console refuses the JWT anyway. The primary key is
-- the replay gate: an atomic insert that conflicts is a JWT already spent,
-- across replicas and restarts. No JWT, no signature and no user id is
-- stored, only the hash and two instants.
CREATE TABLE console_jwt_claim (
    sig_hash   BYTEA PRIMARY KEY CHECK (octet_length(sig_hash) = 32),
    exp        TIMESTAMPTZ NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX console_jwt_claim_exp_idx ON console_jwt_claim (exp);
