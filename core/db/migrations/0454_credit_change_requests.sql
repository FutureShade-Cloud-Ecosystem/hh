-- 0454: credit change requests and approvals.
--
-- A manager may lower a customer's credit limit or place a credit hold at once. An increase above
-- the first dealer threshold (default 0 cents) up to the second dealer threshold (default 2500000
-- cents, $25,000.00) requires one approver (a branch manager or the Financial Controller). An
-- increase above the second dealer threshold requires a second approver (the Financial Controller
-- or the General Manager). The requester never approves their own request, the same person never
-- approves twice, and every change is recorded in credit_change_requests and audit_log.

ALTER TABLE ar_credit_policy
  ADD COLUMN increase_first_threshold_cents BIGINT NOT NULL DEFAULT 0 CHECK (increase_first_threshold_cents >= 0),
  ADD COLUMN increase_second_threshold_cents BIGINT NOT NULL DEFAULT 2500000 CHECK (increase_second_threshold_cents >= 0),
  ADD CONSTRAINT ck_ar_credit_policy_increase_thresholds CHECK (increase_second_threshold_cents >= increase_first_threshold_cents);

CREATE TABLE credit_change_requests (
  id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id             UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  branch_id               UUID REFERENCES locations(id) ON DELETE SET NULL,
  change_kind             TEXT NOT NULL CHECK (change_kind IN ('LIMIT_CHANGE', 'HOLD')),
  previous_limit_cents    BIGINT NOT NULL DEFAULT 0 CHECK (previous_limit_cents >= 0),
  new_limit_cents         BIGINT CHECK (new_limit_cents IS NULL OR new_limit_cents >= 0),
  delta_cents             BIGINT NOT NULL DEFAULT 0,
  hold_kind               TEXT CHECK (hold_kind IS NULL OR hold_kind IN ('NSF', 'AR_AGING', 'CREDIT_LIMIT', 'COD', 'MANUAL')),
  hold_id                 UUID REFERENCES credit_holds(id) ON DELETE SET NULL,
  reason                  TEXT NOT NULL CHECK (btrim(reason) <> ''),
  status                  TEXT NOT NULL CHECK (status IN ('PENDING', 'APPROVED', 'APPLIED', 'REJECTED')),
  required_approvals      INT NOT NULL DEFAULT 0 CHECK (required_approvals BETWEEN 0 AND 2),
  first_threshold_cents   BIGINT NOT NULL DEFAULT 0 CHECK (first_threshold_cents >= 0),
  second_threshold_cents  BIGINT NOT NULL DEFAULT 2500000 CHECK (second_threshold_cents >= first_threshold_cents),
  requested_by            UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  requested_by_role       TEXT NOT NULL DEFAULT '',
  requested_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  first_approved_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  first_approved_by_role  TEXT NOT NULL DEFAULT '',
  first_approved_at       TIMESTAMPTZ,
  first_approval_note     TEXT NOT NULL DEFAULT '',
  second_approved_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  second_approved_by_role TEXT NOT NULL DEFAULT '',
  second_approved_at      TIMESTAMPTZ,
  second_approval_note    TEXT NOT NULL DEFAULT '',
  rejected_by             UUID REFERENCES users(id) ON DELETE SET NULL,
  rejected_by_role        TEXT NOT NULL DEFAULT '',
  rejected_at             TIMESTAMPTZ,
  rejection_reason        TEXT NOT NULL DEFAULT '',
  applied_at              TIMESTAMPTZ,
  created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ck_credit_change_requests_target CHECK (
    (change_kind = 'LIMIT_CHANGE' AND new_limit_cents IS NOT NULL AND hold_kind IS NULL)
    OR (change_kind = 'HOLD' AND new_limit_cents IS NULL AND hold_kind IS NOT NULL)
  ),
  CONSTRAINT ck_credit_change_requests_self_approve_first CHECK (
    first_approved_by IS NULL OR first_approved_by <> requested_by
  ),
  CONSTRAINT ck_credit_change_requests_self_approve_second CHECK (
    second_approved_by IS NULL OR (second_approved_by <> requested_by AND second_approved_by <> first_approved_by)
  ),
  CONSTRAINT ck_credit_change_requests_applied_stamped CHECK (
    status <> 'APPLIED' OR applied_at IS NOT NULL
  ),
  CONSTRAINT ck_credit_change_requests_rejected_stamped CHECK (
    status <> 'REJECTED' OR rejected_at IS NOT NULL
  )
);

CREATE INDEX idx_credit_change_requests_customer ON credit_change_requests (customer_id, created_at DESC);
CREATE INDEX idx_credit_change_requests_branch_status ON credit_change_requests (branch_id, status, created_at DESC);
