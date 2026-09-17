-- 0020: H-2 — bind an override request to an optional subject (a future quote/order).
-- The policy gate (request → approve → proceed) consults APPROVED overrides; a
-- subject_ref lets an approval be scoped to one transaction rather than blanket-
-- unblocking every below-margin quote for that requester+branch. Nullable: existing
-- and branch-level overrides leave it NULL. Indexed for the EXISTS lookup on the
-- hot path. Full per-line enforcement lands with the W3/W4 quote/order flow.

ALTER TABLE permission_approval_requests
  ADD COLUMN subject_ref TEXT;

CREATE INDEX idx_approval_requests_override_lookup
  ON permission_approval_requests (requested_by, branch_id, policy_type, status);
