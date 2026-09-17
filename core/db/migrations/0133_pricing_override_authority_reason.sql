-- 0133_pricing_override_authority_reason.sql — DIB-420, Wave 5 Swarm C.
--
-- WHAT CHANGED IN THE PROGRAM, AND WHY THIS COLUMN HAS TO FOLLOW IT.
--
-- Until now `has_approved_override` on POST /api/v1/pricing/resolve was taken from the request body
-- and handed straight to the A-20 margin gate, whose first line is `if hasApprovedOverride { return
-- nil }`. Any ACTIVE staff member behind readGuard could clear the margin floor by typing one boolean.
-- DIB-420 makes the flag a REQUEST: the server resolves the authority for itself, from the caller the
-- auth guard already authenticated, and refuses when there is none.
--
-- 0114 declared APPROVED_OVERRIDE as a bypass reason and deliberately never wrote it, with the note
-- "an approved override is not a bypass, it is a pass". That was honest about a gate that took the
-- client's word: nothing had been decided, so there was nothing to record. Now something IS decided,
-- by the server, on a named authority — and a below-floor price that goes out on somebody's authority
-- is precisely what a month-end reader has to be able to count.
--
-- TWO REASONS, NOT ONE, because there are two authorities and they are not equally accountable:
--
--   APPROVED_OVERRIDE       an APPROVED margin override on the DIB-360 approvals substrate. Somebody
--                           filed a request, somebody with authority approved it, and both are dated
--                           rows in approval_requests.
--   MANAGER_SCOPE_OVERRIDE  the caller's own manager scope over the branch being priced. No approval
--                           row exists, because the manager IS the approver. This is a legitimate
--                           door, and it must be a COUNTABLE one — "how many below-floor prices went
--                           out last month with nobody but the seller signing off?" is the question,
--                           and a single merged reason cannot answer it.
--
-- The existing scope CHECK (a reason only on an action='BYPASS' row) is untouched and still holds:
-- the resolver writes the honoured override as a BYPASS row, because pricing_audit_log_action_check
-- admits exactly CREATE/UPDATE/DELETE/BYPASS and inventing a fifth verb for this would fork every
-- report that already reads the fourth.

BEGIN;

ALTER TABLE pricing_audit_log DROP CONSTRAINT IF EXISTS pricing_audit_bypass_reason_known;

ALTER TABLE pricing_audit_log
  ADD CONSTRAINT pricing_audit_bypass_reason_known
  CHECK (bypass_reason IS NULL OR bypass_reason IN
         ('PRODUCT_POLICY_DISABLED','CATEGORY_POLICY_DISABLED',
          'APPROVED_OVERRIDE','MANAGER_SCOPE_OVERRIDE'));

COMMENT ON COLUMN pricing_audit_log.bypass_reason IS
  'Why the margin floor did not stop this price: the enforcement policy was off '
  '(PRODUCT_POLICY_DISABLED / CATEGORY_POLICY_DISABLED), or a server-resolved authority cleared it '
  '(APPROVED_OVERRIDE = an approvals-substrate record; MANAGER_SCOPE_OVERRIDE = the caller''s own '
  'manager scope over the branch). DIB-420: the client flag alone is never a reason.';

COMMIT;
