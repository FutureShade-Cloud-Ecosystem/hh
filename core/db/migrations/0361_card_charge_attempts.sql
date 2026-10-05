-- 0361: card_charge_attempts, written and committed before every counter card processor call
-- (decision log row 813). Owner module: internal/payment. Purely additive: one table, its
-- indexes and one job_schedules row. Dropping them reopens the defect and nothing else.
--
-- One row per till leg, keyed by the leg's idempotency key, so one leg key reaches the processor
-- at most once, ever. The status column carries the lifecycle (CHARGING, CAPTURED, DECLINED,
-- UNKNOWN, BOUND, VOIDING, VOIDED, VOID_UNKNOWN, VOID_DECLINED, RESOLVED) and every transition is a
-- single guarded UPDATE in the payment store, so one row is the only arbiter between "the sale
-- owns this capture" and "the compensator owns it". The CHECK constraints below keep the
-- impossible rows out whatever the application does: a BOUND row has its payment, a row that
-- holds money has a processor ref, a VOIDED row says how, a RESOLVED row says who and why.
--
-- No currency column: the DX-19 invariant forbids one (0103's header records why).
-- The existing payment_attempts table (0035) is not reused: it logs declined attempts the client
-- posts, and its result CHECK admits only DECLINED and ERROR.
--
-- Idempotent: every statement carries its own guard, so a second application changes nothing.
-- job_schedules columns are 0096's real ones (name, kind, interval_secs, enabled); the schedule
-- row is enabled on purpose, because a compensating sweep that is switched off is the defect
-- itself, and with no attempts it reads one empty partial index. A schedule whose name has no
-- registered job is reported at boot and skipped (0096), so the row is inert until the sweep's
-- job registration lands.
CREATE TABLE IF NOT EXISTS card_charge_attempts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  leg_key         TEXT NOT NULL UNIQUE CHECK (leg_key <> ''),  -- one charge per leg key, ever
  sale_key        UUID NOT NULL,                               -- the ring's pos_sale idempotency key
  leg_seq         INT  NOT NULL CHECK (leg_seq >= 1),
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  branch_id       UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  till_session_id UUID NOT NULL REFERENCES till_sessions(id) ON DELETE RESTRICT,
  customer_id     UUID REFERENCES customers(id) ON DELETE SET NULL,
  card_type       TEXT NOT NULL DEFAULT '',
  processor       TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL CHECK (status IN ('CHARGING','CAPTURED','DECLINED','UNKNOWN','BOUND',
                    'VOIDING','VOIDED','VOID_UNKNOWN','VOID_DECLINED','RESOLVED')),
  processor_ref   TEXT NOT NULL DEFAULT '',
  reversal_kind   TEXT NOT NULL DEFAULT '' CHECK (reversal_kind IN ('','VOID','REFUND')),
  reversal_ref    TEXT NOT NULL DEFAULT '',
  resp_code       TEXT NOT NULL DEFAULT '',
  last_error      TEXT NOT NULL DEFAULT '',  -- diagnostics only; never a token, a credential or a PAN
  payment_id      UUID REFERENCES payments(id) ON DELETE RESTRICT,
  resolution      TEXT NOT NULL DEFAULT '' CHECK (resolution IN ('','NOT_CHARGED','REVERSED_AT_PROCESSOR')),
  resolution_note TEXT NOT NULL DEFAULT '',
  resolved_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT cca_bound_has_payment CHECK (status <> 'BOUND' OR payment_id IS NOT NULL),
  CONSTRAINT cca_money_has_ref CHECK (status NOT IN ('CAPTURED','BOUND','VOIDING','VOIDED','VOID_UNKNOWN',
    'VOID_DECLINED') OR processor_ref <> ''),
  CONSTRAINT cca_voided_says_how CHECK (status <> 'VOIDED' OR (reversal_kind <> '' AND reversal_ref <> '')),
  CONSTRAINT cca_resolved_says_how CHECK (status <> 'RESOLVED'
    OR (resolution <> '' AND resolution_note <> '' AND resolved_by IS NOT NULL))
);

-- The sweep reads the rows still in flight, oldest first.
CREATE INDEX IF NOT EXISTS idx_cca_sweep ON card_charge_attempts (status, updated_at)
  WHERE status IN ('CHARGING','CAPTURED','VOIDING');
-- The till guard looks up unresolved attempts for one session and amount.
CREATE INDEX IF NOT EXISTS idx_cca_till_guard ON card_charge_attempts (till_session_id, amount_cents)
  WHERE status IN ('CHARGING','CAPTURED','UNKNOWN','VOIDING','VOID_UNKNOWN','VOID_DECLINED');
-- A settlement report is matched by processor ref.
CREATE INDEX IF NOT EXISTS idx_cca_processor_ref ON card_charge_attempts (processor_ref) WHERE processor_ref <> '';
CREATE INDEX IF NOT EXISTS idx_cca_sale_key ON card_charge_attempts (sale_key);

INSERT INTO job_schedules (name, kind, interval_secs, enabled)
VALUES ('payment.card_attempt_sweep', 'INTERVAL', 60, TRUE)
ON CONFLICT (name) DO NOTHING;
