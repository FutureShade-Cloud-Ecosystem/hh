-- 0417_assist_metering.sql: TanyAI slice 2, the spend and price tables, the Pro caps and the sweep schedule.
--
-- assist_spend: the append only spend table. One row per reservation, settlement or void of one
-- provider call, keyed by (exchange_id, call_no). Monthly spend, the dealer cap and the billing
-- export all read it; core/internal/assist/spend_repository.go is the only code that touches it.
-- APPEND ONLY by the 0154 trigger pattern: UPDATE, DELETE and TRUNCATE are refused. A correction is
-- a row. What this does not stop is the owner disabling the trigger (same statement as 0154).

CREATE TABLE IF NOT EXISTS assist_spend (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind             text NOT NULL CHECK (kind IN ('reservation', 'settlement', 'void')),
    exchange_id      uuid NOT NULL,
    call_no          integer NOT NULL CHECK (call_no >= 1),
    audience         text NOT NULL CHECK (audience IN ('staff', 'pro', 'embed', 'messages')),
    staff_user_id    uuid REFERENCES users (id) ON DELETE RESTRICT,
    -- The portal user table is slice 10's; no foreign key until it is used.
    portal_user_id   uuid,
    role_code        citext NOT NULL DEFAULT '',
    alias            text NOT NULL,
    model            text NOT NULL,
    tokens_in        integer NOT NULL DEFAULT 0 CHECK (tokens_in >= 0),
    tokens_out       integer NOT NULL DEFAULT 0 CHECK (tokens_out >= 0),
    tokens_cached    integer NOT NULL DEFAULT 0 CHECK (tokens_cached >= 0),
    cost_micro_cents bigint NOT NULL DEFAULT 0 CHECK (cost_micro_cents >= 0),
    price_id         uuid,
    -- '' for a settlement from the provider call itself, 'sweep' for one the sweep wrote at the
    -- reservation's estimate because the call may have reached the provider.
    settled_by       text NOT NULL DEFAULT '' CHECK (settled_by IN ('', 'sweep')),
    -- A settlement or void carries its reservation's at, so a call never straddles a month.
    at               timestamptz NOT NULL DEFAULT now(),
    -- The Go SpendRow.Validate rule: exactly one user id, or neither for embed.
    CONSTRAINT assist_spend_user_audience CHECK (
        (audience IN ('staff', 'messages') AND staff_user_id IS NOT NULL AND portal_user_id IS NULL) OR
        (audience = 'pro'                  AND portal_user_id IS NOT NULL AND staff_user_id IS NULL) OR
        (audience = 'embed'                AND portal_user_id IS NULL AND staff_user_id IS NULL)
    ),
    CONSTRAINT assist_spend_settled_by_kind CHECK (settled_by = '' OR kind = 'settlement'),
    -- A call is reserved once, settled at most once and voided at most once.
    CONSTRAINT assist_spend_call_kind UNIQUE (exchange_id, call_no, kind)
);

CREATE INDEX IF NOT EXISTS assist_spend_at_idx ON assist_spend (at DESC);
CREATE INDEX IF NOT EXISTS assist_spend_staff_at_idx ON assist_spend (staff_user_id, at DESC) WHERE staff_user_id IS NOT NULL;
-- The sweep: open reservations by age.
CREATE INDEX IF NOT EXISTS assist_spend_open_idx ON assist_spend (at) WHERE kind = 'reservation';

CREATE OR REPLACE FUNCTION assist_spend_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'assist_spend is append-only: % is refused (record a compensating row instead)', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS assist_spend_no_update ON assist_spend;
CREATE TRIGGER assist_spend_no_update
    BEFORE UPDATE OR DELETE ON assist_spend
    FOR EACH ROW EXECUTE FUNCTION assist_spend_append_only();

DROP TRIGGER IF EXISTS assist_spend_no_truncate ON assist_spend;
CREATE TRIGGER assist_spend_no_truncate
    BEFORE TRUNCATE ON assist_spend
    FOR EACH STATEMENT EXECUTE FUNCTION assist_spend_append_only();

-- assist_model_prices: append only. Integer micro cents per million tokens. The row in force is the
-- highest effective_seq for the alias and model. NO markup column: markup lives in the billing
-- export. Deliberately NOT seeded: a model with no price row is not called, so an operator inserts
-- the row for the model they enable.
CREATE TABLE IF NOT EXISTS assist_model_prices (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    alias                    text NOT NULL,
    model                    text NOT NULL,
    price_in_micro_per_m     bigint NOT NULL CHECK (price_in_micro_per_m >= 0),
    price_out_micro_per_m    bigint NOT NULL CHECK (price_out_micro_per_m >= 0),
    price_cached_micro_per_m bigint NOT NULL CHECK (price_cached_micro_per_m >= 0),
    effective_seq            bigint NOT NULL CHECK (effective_seq >= 1),
    created_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT assist_model_prices_seq UNIQUE (alias, model, effective_seq)
);

CREATE OR REPLACE FUNCTION assist_model_prices_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'assist_model_prices is append-only: % is refused (add a row with a higher effective_seq)', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS assist_model_prices_no_update ON assist_model_prices;
CREATE TRIGGER assist_model_prices_no_update
    BEFORE UPDATE OR DELETE ON assist_model_prices
    FOR EACH ROW EXECUTE FUNCTION assist_model_prices_append_only();

DROP TRIGGER IF EXISTS assist_model_prices_no_truncate ON assist_model_prices;
CREATE TRIGGER assist_model_prices_no_truncate
    BEFORE TRUNCATE ON assist_model_prices
    FOR EACH STATEMENT EXECUTE FUNCTION assist_model_prices_append_only();

-- Sub item 5, schema only, unused until slice 10: per portal user and per account monthly caps for
-- the Pro audience, in micro cents. Nothing in Go reads or writes this table in slice 2.
CREATE TABLE IF NOT EXISTS assist_pro_caps (
    scope                  text NOT NULL CHECK (scope IN ('portal_user', 'account')),
    subject_id             uuid NOT NULL,
    monthly_cap_micro_cents bigint NOT NULL CHECK (monthly_cap_micro_cents >= 0),
    updated_by             uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at             timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, subject_id)
);

-- The sweep's schedule row (0096 job_schedules). Enabled: the function is registered in the worker
-- (cmd/core/worker.go) and a registered job with no row never fires. Every five minutes; the sweep
-- settles an open reservation at its estimate only past assist.ReservationGrace (fifteen minutes).
INSERT INTO job_schedules (name, kind, interval_secs, enabled)
VALUES ('assist.spend_reservation_sweep', 'INTERVAL', 300, TRUE)
ON CONFLICT (name) DO NOTHING;
