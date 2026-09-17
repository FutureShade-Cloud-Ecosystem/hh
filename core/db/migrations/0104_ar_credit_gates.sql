-- 0104: AR credit gates — automatic credit holds from aging / limit breach / COD (DIB-65, DIB-52).
-- Owner module: internal/billing (the AR side). Wave 3, Swarm B.
--
-- ── WHY THIS BENDS THE MODULE RULE, DELIBERATELY AND ON THE RECORD ────────────────────────────────
-- credit_holds (0035) is internal/till's table: the NSF path writes it, the AR console reads it, and
-- till.HoldAwareCreditGate is the seam the POS ring and quote→order convert BOTH already consult. The
-- correct answer to "AR aging should place a hold" is therefore NOT a second holds table. A second
-- table would duplicate the keystone invariant (ux_credit_holds_one_active: at most ONE ACTIVE hold
-- per customer), and duplicating an invariant is how you end up with two of them disagreeing at the
-- counter while a customer waits.
--
-- So internal/billing writes credit_holds directly, exactly as internal/till does, and the gate that
-- reads them is not touched at all. The POS ring and convert start honouring AR-placed holds with
-- ZERO code change on their side, because they were already asking the right question. That is the
-- whole design: this migration makes new ROWS appear in a table something already watches.
--
-- The bend is bounded and stated: billing writes credit_holds and ar_credit_policy; it does not read
-- or write payment_nsf, and it never releases a hold placed by the NSF path without recording which
-- path placed it (that is what hold_kind is for).
--
-- ── WHAT AN AUTO-HOLD IS ─────────────────────────────────────────────────────────────────────────
-- A nightly job (internal/jobs, schedule row at the bottom of this file) walks accounts on credit
-- terms and places a hold when EITHER trigger fires:
--   AR_AGING      — the account carries at least aging_threshold_cents of AR past aging_bucket_days.
--   CREDIT_LIMIT  — open AR exceeds credit_limit_cents by more than limit_tolerance_cents.
-- COD holds are different in kind and are placed at order time, not nightly:
--   COD           — a COD account (payment_terms_days = 0) with an unpaid balance is barred from
--                   DISPATCH until it pays. It is not barred from ordering or from paying.
-- Every threshold here is a POLICY ROW, not a constant in Go. Raising the aging window at go-live is
-- an UPDATE, not a deploy — the same doctrine gl_policy and nsf_policy already follow.
--
-- ── OVERRIDE IS NOT RELEASE ──────────────────────────────────────────────────────────────────────
-- Release (0035, Tier-1, mandatory reason) ends a hold: the account is good again. An OVERRIDE says
-- "this hold is correct and stays, but let this account transact until <time> anyway" — the thing a
-- credit manager actually does when a cheque is in the mail. Keeping them distinct matters, because
-- collapsing override into release destroys the record that the account was ever over its limit, and
-- that record is the entire point of the aging report. Both are audited; override additionally
-- REQUIRES a reason and an expiry, so "temporarily" cannot quietly mean "forever".
--
-- Rollback: additive (one new table, five new columns on credit_holds, one schedule row). The new
-- columns all have defaults, so 0035's writers are unaffected; dropping them disables auto-holds and
-- leaves NSF holds working exactly as before.

-- ── 1. AR credit policy — DATA, not code ─────────────────────────────────────────────────────────
-- branch_id NULL = org default; a branch row overrides it. Ships flag-OFF (auto_hold_enabled FALSE)
-- so merging this migration changes nobody's ability to transact until an operator turns it on.
CREATE TABLE ar_credit_policy (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id             UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE, -- NULL = org default
  -- AR aging trigger: at least `aging_threshold_cents` of balance older than `aging_bucket_days`.
  aging_bucket_days     INT    NOT NULL DEFAULT 60 CHECK (aging_bucket_days BETWEEN 1 AND 365),
  aging_threshold_cents BIGINT NOT NULL DEFAULT 0 CHECK (aging_threshold_cents >= 0),
  aging_hold_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
  -- Credit-limit trigger: open AR over the customer's limit by more than the tolerance. The tolerance
  -- exists so a $3 rounding drift on a $50,000 limit does not stop a truck.
  limit_hold_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
  limit_tolerance_cents BIGINT NOT NULL DEFAULT 0 CHECK (limit_tolerance_cents >= 0),
  -- COD enforcement (DIB-52): a COD account with an unpaid balance cannot DISPATCH.
  cod_enforced          BOOLEAN NOT NULL DEFAULT TRUE,
  -- The master switch for the nightly evaluator. FALSE = the job runs, evaluates, and places nothing
  -- (it still records what it WOULD have held, so an operator can see the blast radius before the flip).
  auto_hold_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
  notes                 TEXT   NOT NULL DEFAULT '',
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO ar_credit_policy (branch_id, aging_bucket_days, aging_threshold_cents, limit_tolerance_cents, auto_hold_enabled, notes)
VALUES (NULL, 60, 0, 0, FALSE,
        'Org default. auto_hold_enabled ships FALSE (DIB-65 flag-off): the nightly job evaluates and reports but places no holds until an operator flips it.');

-- ── 2. credit_holds gains provenance + the override window ───────────────────────────────────────
-- hold_kind records WHICH gate placed the row. 0035's NSF writer does not set it, so the default is
-- 'NSF' — that keeps every existing row and every existing INSERT correct without touching till.
ALTER TABLE credit_holds ADD COLUMN hold_kind TEXT NOT NULL DEFAULT 'NSF'
  CHECK (hold_kind IN ('NSF','AR_AGING','CREDIT_LIMIT','COD','MANUAL'));
-- TRUE when a machine placed it (the nightly job), FALSE when a person did. An operator needs to know
-- whether to argue with a policy or with a colleague.
ALTER TABLE credit_holds ADD COLUMN auto_placed BOOLEAN NOT NULL DEFAULT FALSE;
-- The override window. NULL override_until = no override in force.
ALTER TABLE credit_holds ADD COLUMN override_until   TIMESTAMPTZ;
ALTER TABLE credit_holds ADD COLUMN override_reason  TEXT NOT NULL DEFAULT '';
ALTER TABLE credit_holds ADD COLUMN overridden_by    UUID REFERENCES users(id) ON DELETE SET NULL;
-- An override without a reason is an unsigned cheque. The CHECK makes the reason structurally
-- mandatory rather than a validation somebody can forget in a second call site.
ALTER TABLE credit_holds ADD CONSTRAINT credit_holds_override_needs_reason
  CHECK (override_until IS NULL OR override_reason <> '');

-- The gate's hot read: "is this customer held RIGHT NOW, override considered".
CREATE INDEX idx_credit_holds_active_override ON credit_holds (customer_id, override_until) WHERE status = 'ACTIVE';

-- ── 3. The nightly evaluation schedule (0096 job_schedules) ──────────────────────────────────────
-- 08:00 UTC ≈ 04:00 Eastern: after the overnight posting settles, before anyone is at a counter. The
-- job is idempotent (it converges the hold set to what policy says it should be), which the runner
-- requires — it has no lease and will start a job again if the previous run is still going.
INSERT INTO job_schedules (name, kind, nightly_minute, enabled)
VALUES ('ar.credit_hold_sweep', 'NIGHTLY', 480, TRUE);
