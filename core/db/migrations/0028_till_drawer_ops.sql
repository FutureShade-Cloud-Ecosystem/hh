-- 0028: A-03 till / cash-drawer drawer-session ops (W4, gate criterion 3 cash half).
-- Owner module: internal/till. EVOLVES till's own 0010 tables (till_sessions/payments/
-- payment_allocations) + adds per-tender / denomination / movement children + a policy table.
-- Conventions: UUID PKs · money BIGINT *_cents · TIMESTAMPTZ · explicit ON DELETE · flag-don't-delete.
-- Over/short is DERIVED (never a stored per-tender column).

-- 1a. Three-role accountability + start/closing float + override note on the SESSION header.
--     opened_by already exists (0010:12). Add counted_by/completed_by (distinct = segregation of
--     custody from reconciliation) + the COUNTED intermediate state for a two-step blind close.
ALTER TABLE till_sessions
  ADD COLUMN counted_by          UUID REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN counted_at          TIMESTAMPTZ,
  ADD COLUMN completed_by        UUID REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN completed_at        TIMESTAMPTZ,
  ADD COLUMN closing_float_cents BIGINT,                          -- carried to next open
  ADD COLUMN blind_close         BOOLEAN NOT NULL DEFAULT TRUE,   -- best-practice: count without seeing expected
  ADD COLUMN override_notes      TEXT NOT NULL DEFAULT '';        -- manager sign-off reason
-- closed_by/closed_at/counted_cents/over_short_cents already exist (0010:15-18). over_short_cents is
-- RETAINED as a denormalized CLOSE-TIME SNAPSHOT of the total CASH over/short (always re-derivable).

-- Widen the status lifecycle OPEN -> COUNTED -> CLOSED (0010:11 was OPEN/CLOSED). Drop+re-add the CHECK.
ALTER TABLE till_sessions DROP CONSTRAINT till_sessions_status_check;
ALTER TABLE till_sessions ADD CONSTRAINT till_sessions_status_check
  CHECK (status IN ('OPEN','COUNTED','CLOSED'));
-- At most ONE non-closed session per drawer (branch+station): a drawer cannot be double-opened.
CREATE UNIQUE INDEX uq_till_open_per_drawer
  ON till_sessions(branch_id, station) WHERE status <> 'CLOSED';

-- 1b. Per-tender expected/actual/lodged child. EXPECTED is the close-time snapshot of Σ(payments of this
--     method); ACTUAL is the human blind count; LODGED is banked. Per-tender variance is DERIVED, never stored.
CREATE TABLE till_tender_totals (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  till_session_id UUID NOT NULL REFERENCES till_sessions(id) ON DELETE CASCADE,
  method          TEXT NOT NULL CHECK (method IN ('CASH','CHEQUE','CARD','EFT','OTHER')), -- mirrors payments.method
  expected_cents  BIGINT NOT NULL DEFAULT 0,   -- close snapshot of Σ(payments) for this method (system side)
  actual_cents    BIGINT,                       -- blind-counted (cash/cheque) or processor-confirmed (card)
  lodged_cents    BIGINT NOT NULL DEFAULT 0,    -- banked / dropped for this method
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (till_session_id, method)
);
CREATE INDEX idx_till_tender_totals_session ON till_tender_totals(till_session_id);

-- 1c. Optional denomination detail. phase = OPEN or CLOSE.
CREATE TABLE till_denomination_counts (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  till_session_id    UUID NOT NULL REFERENCES till_sessions(id) ON DELETE CASCADE,
  phase              TEXT NOT NULL CHECK (phase IN ('OPEN','CLOSE')),
  denomination_cents BIGINT NOT NULL CHECK (denomination_cents > 0), -- e.g. 500 = a $5
  quantity           INTEGER NOT NULL CHECK (quantity >= 0),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_till_denoms_session ON till_denomination_counts(till_session_id, phase);

-- 1d. Movements: paid-in / paid-out / drop / lodgement.
CREATE TABLE till_movements (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  till_session_id UUID NOT NULL REFERENCES till_sessions(id) ON DELETE RESTRICT,
  movement_type   TEXT NOT NULL CHECK (movement_type IN ('PAID_IN','PAID_OUT','DROP','LODGEMENT')),
  method          TEXT NOT NULL DEFAULT 'CASH' CHECK (method IN ('CASH','CHEQUE','CARD','EFT','OTHER')),
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  reason          TEXT NOT NULL DEFAULT '',
  reference       TEXT NOT NULL DEFAULT '',     -- bag/deposit slip no.
  created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_till_movements_session ON till_movements(till_session_id);

-- 1e. Offline-first + signature on the PAYMENTS row (A-03's own 0010 table).
--     idempotency_key = client-generated UUID; the partial-unique index makes the write replay-safe.
--     offline_captured marks a tender taken with no connectivity. signature_ref = opaque pointer to A-19's
--     encrypted signature evidence.
ALTER TABLE payments
  ADD COLUMN idempotency_key  TEXT NOT NULL DEFAULT '',
  ADD COLUMN offline_captured BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN signature_ref    TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX uq_payments_idempotency_key
  ON payments(idempotency_key) WHERE idempotency_key <> '';

-- 1f. Till policy (the variance threshold + close grain) — DATA, not a code const. Keyed by branch_id.
--     Seeded CONSERVATIVE + PROVISIONAL pending the float/reconciliation SOP (D-A03-1/2).
CREATE TABLE till_policy (
  branch_id                UUID PRIMARY KEY REFERENCES locations(id) ON DELETE CASCADE,
  variance_threshold_cents BIGINT NOT NULL DEFAULT 0,        -- PROVISIONAL: 0 = any non-zero variance -> manager
  close_grain              TEXT NOT NULL DEFAULT 'SESSION'   -- PROVISIONAL: drawer SESSION (no shifts)
                             CHECK (close_grain IN ('SESSION','DAY')),
  require_blind_close      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Seed an org-default conservative policy for the two known BRANCH locations (0003). ALL VALUES PROVISIONAL.
-- NOTE: the 0003 locations table column is loc_type (not location_type).
INSERT INTO till_policy (branch_id, variance_threshold_cents, close_grain)
SELECT id, 0, 'SESSION' FROM locations WHERE loc_type = 'BRANCH'
ON CONFLICT (branch_id) DO NOTHING;
