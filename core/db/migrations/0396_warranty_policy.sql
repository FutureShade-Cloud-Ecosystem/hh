-- 0396_warranty_policy.sql
--
-- The warranty claim threshold (walk change 6, the Dealer Admin Warranty setting): the dealer wide
-- knob that decides whether a defect's cost at the line total becomes a warranty claim or is logged
-- as shrinkage. The boards' default is $50.00, held as 5000 integer cents, lowerable as the process
-- improves; the Manager Home Warranty board reads it, the counter till reads it for the return
-- sentence, Dealer Admin sets it.
--
-- THE SHAPE IS THE POS_POLICY PRECEDENT (0043, internal/pos/policy.go): branch_id NULL is the
-- org-wide default row, seeded here, and a non-NULL branch_id is that branch's override, UNIQUE so a
-- branch holds at most one. The read resolves the branch row first and falls back to the org row, so
-- a dealer configures once and overrides per branch only when it means it.
--
-- MONEY IS INTEGER CENTS (the house convention): BIGINT, never a float. The CHECK keeps the knob at
-- zero or above: a negative threshold would classify every defect a claim. The validation answers in
-- the service too (a 422 in words), so the CHECK is the backstop, never the refusal a client sees.
--
-- updated_by and updated_at carry the LAST change on the row itself. The history lives on the audit
-- spine (audit_log, written by the warranty service's Recorder on every change with the old and new
-- value), not in a second table beside it: the change is already recorded, by the module that made
-- it, in the one place this program keeps who did what (the pos policy history's ruling, kept).

CREATE TABLE warranty_policy (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id             UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE,  -- NULL = org-wide default row
  claim_threshold_cents BIGINT NOT NULL DEFAULT 5000 CHECK (claim_threshold_cents >= 0),
  updated_by            UUID REFERENCES users(id) ON DELETE SET NULL,
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO warranty_policy (branch_id, claim_threshold_cents)
VALUES (NULL, 5000);
