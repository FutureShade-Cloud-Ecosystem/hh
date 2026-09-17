-- 0106: JE approver assignment + payment reversal provenance + the surcharge/EFT flip (DIB-360/361,
-- DIB-170, DIB-102). Owner modules: internal/gl (approvals), internal/payments + internal/payment
-- (reversals, tender config). Wave 3, Swarm B.
--
-- Three small, related things land together because each is columns-and-a-CHECK on a table that
-- already exists, and none of them justifies a migration number of its own.
--
-- ═════════════════════════════════════════════════════════════════════════════════════════════════
-- 1. DIB-360/361 — WHO approves, not just WHETHER approval is needed
-- ═════════════════════════════════════════════════════════════════════════════════════════════════
-- The threshold half already shipped: gl_policy.je_approval_threshold_cents (0032, $5,000, per-branch
-- via gl_policy.branch_id) and gl.thresholdFor()/derivedStatus() park an over-threshold manual JE as
-- DRAFT/PENDING_APPROVAL. 0075 blessed that as-is. What has never existed is the OTHER half: who is
-- allowed to sign it off. Today "a manager who is not the maker" is the whole rule, which means a
-- $400,000 reclass can be approved by whichever manager happens to be logged in.
--
-- gl_approver_assignments answers "for THIS branch and THIS policy type, these named people may
-- decide". It is a config table, not a permission system: identity still decides whether someone is
-- a manager at all, and four-eyes (approver ≠ maker) is still enforced in the store's guarded UPDATE.
-- This narrows an already-authorized set; it can never widen one. That ordering matters — a config
-- row must not be able to grant authority, only to withhold it.
--
-- An EMPTY assignment set for a branch means "no named approvers configured", which resolves to the
-- shipped behaviour (any eligible manager). Fail-OPEN is the deliberate choice here and it is the
-- opposite of the choice made for payment credentials, because the failure modes are not symmetric:
-- an unconfigured branch that cannot post ANY journal is an outage, while an unconfigured branch that
-- posts with the old rule is exactly where it was yesterday.

CREATE TABLE gl_approver_assignments (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- NULL branch_id = org-wide assignment; a branch row NARROWS it for that branch.
  branch_id    UUID REFERENCES locations(id) ON DELETE CASCADE,
  -- Which approval this person may decide. Matches permission_approval_requests.policy_type (0075).
  policy_type  TEXT NOT NULL DEFAULT 'JE_OVER_THRESHOLD'
                 CHECK (policy_type IN ('MARGIN_BELOW_MINIMUM','CREDIT_LIMIT_EXCEEDED','COD_CONSTRAINT','JE_OVER_THRESHOLD')),
  -- The role this assignment models (documentation for the operator reading the config screen; the
  -- authority still comes from identity, never from this string).
  role         TEXT NOT NULL DEFAULT '',
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  enabled      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One assignment per (branch, policy, user). A duplicate is a data-entry slip, not a second grant.
CREATE UNIQUE INDEX ux_gl_approver_assignment
  ON gl_approver_assignments (COALESCE(branch_id, '00000000-0000-0000-0000-000000000000'::uuid), policy_type, user_id);
-- The decide-path read: "who may decide this policy type at this branch".
CREATE INDEX idx_gl_approver_lookup ON gl_approver_assignments (policy_type, branch_id) WHERE enabled;

-- ═════════════════════════════════════════════════════════════════════════════════════════════════
-- 2. DIB-170 — payment UNDO/CANCEL: reversing entries, never deletion
-- ═════════════════════════════════════════════════════════════════════════════════════════════════
-- PRE-RATIFIED RULING, implemented literally: a payment is NEVER deleted and NEVER edited away. An
-- undo posts a REVERSING payment — a second, negative-effect row that points at the original — and
-- the original stays exactly as it was. "Void" as an operator word survives as sugar: a reversal
-- posted on the same day as the original is LABELLED a void in the UI. Same rows underneath.
--
-- Why this is not negotiable, stated once so nobody has to re-derive it: the AR subledger
-- (customer_transactions) and the GL are append-only projections of the payment stream. Deleting a
-- payment leaves the projections describing money that no longer has a source, and the only way to
-- discover that is a tie-out that fails months later with no way back to the act that caused it. A
-- reversing entry keeps both sides self-describing at every instant.
--
-- The columns are provenance only. The reversal itself is an ordinary payments row (method/branch/
-- amount as the original) written by the service in one transaction with its allocations, its
-- customer_transactions rows and its outbox event.
ALTER TABLE payments ADD COLUMN reverses_payment_id UUID REFERENCES payments(id) ON DELETE RESTRICT;
ALTER TABLE payments ADD COLUMN reversed_by_payment_id UUID REFERENCES payments(id) ON DELETE RESTRICT;
ALTER TABLE payments ADD COLUMN reversal_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN reversed_at TIMESTAMPTZ;
-- A reversal must carry a reason. Reason-required is a policy the DB can hold, so it is held here and
-- not only in a handler that a second call site could forget.
ALTER TABLE payments ADD CONSTRAINT payments_reversal_needs_reason
  CHECK (reverses_payment_id IS NULL OR reversal_reason <> '');
-- A payment may be reversed at most once. Without this, two operators clicking undo at the same
-- moment both post a reversal and the customer's balance goes the wrong way by the payment amount.
CREATE UNIQUE INDEX ux_payments_one_reversal ON payments (reverses_payment_id) WHERE reverses_payment_id IS NOT NULL;
-- The reversal-aware AR read: an original that HAS been reversed.
CREATE INDEX idx_payments_reversed ON payments (reversed_at) WHERE reversed_at IS NOT NULL;

-- ═════════════════════════════════════════════════════════════════════════════════════════════════
-- 3. DIB-102 — surcharge rules + EFT as a tender method
-- ═════════════════════════════════════════════════════════════════════════════════════════════════
-- Both halves were built RESERVED-OFF in 0027: payment_config.surcharge_enabled (FALSE),
-- payment_config.mdr_bps, payments.surcharge_cents (0) and payments.card_type. payments.method's
-- CHECK has admitted 'EFT' since 0010. So this is a FLIP, not a build — and the only thing genuinely
-- missing is the per-tender configuration the flip needs to be safe.
--
-- surcharge_card_types: Ontario prohibits surcharging debit and prepaid. The calculator already
-- hard-codes CREDIT-only, and that stays the floor; this column can only NARROW it further (a dealer
-- who chooses not to surcharge at all while the org flag is on). It cannot widen it to debit.
-- eft_enabled: whether the counter offers EFT as a tender at all. Default FALSE — a tender nobody has
-- been trained on should not appear in the ring the day this merges.
ALTER TABLE payment_config ADD COLUMN surcharge_card_types TEXT NOT NULL DEFAULT 'CREDIT'
  CHECK (surcharge_card_types IN ('CREDIT','NONE'));
ALTER TABLE payment_config ADD COLUMN eft_enabled BOOLEAN NOT NULL DEFAULT FALSE;
-- The surcharge line must be explainable to a customer at the counter: the rate ACTUALLY applied is
-- stamped on the payment, so a receipt dispute is answered from the row and not recomputed from a
-- config that may have changed since. min(mdr_bps, 240) at capture time.
ALTER TABLE payments ADD COLUMN surcharge_bps INT NOT NULL DEFAULT 0
  CHECK (surcharge_bps BETWEEN 0 AND 240);
