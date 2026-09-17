-- 0016: A-20 permission override / approval queue (W1).
-- When a policy blocks a clerk (below-margin quote, over-credit order), they file
-- an override request here; a manager/leadership decides it. Spec: A-20 handoff
-- (adapted) + decision D-016 (Q-029 authority best-assumption).

CREATE TABLE permission_approval_requests (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  requested_by     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  branch_id        UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  policy_type      TEXT NOT NULL CHECK (policy_type IN ('MARGIN_BELOW_MINIMUM','CREDIT_LIMIT_EXCEEDED','COD_CONSTRAINT')),
  detail           TEXT NOT NULL DEFAULT '',
  data             JSONB NOT NULL DEFAULT '{}',
  status           TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','APPROVED','REJECTED')),
  requested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  decided_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  decided_at       TIMESTAMPTZ,
  rejection_reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_approval_requests_branch_status ON permission_approval_requests(branch_id, status);
CREATE INDEX idx_approval_requests_requester ON permission_approval_requests(requested_by);
