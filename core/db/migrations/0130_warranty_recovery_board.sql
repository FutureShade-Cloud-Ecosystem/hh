-- 0130: DIB-221 — WTY-RECOVERY, the outstanding-money recovery worklist. Owner: internal/warranty (W5-B).
--
-- THE CLIENT REALITY THIS TABLE IS FOR. Roughly $40k sits in live claims where the vendor has AGREED to a
-- credit and the credit has not landed. Agreeing is not receiving. P1 recorded realized_cents at settlement and
-- called it done, which means the board could not tell "Techo said yes last March" from "Techo paid". 0129
-- turned the agreed amount into an ACCRUAL; this migration adds the other half — what actually came back, and
-- the chase log that got it back.
--
--   outstanding = accrued − received
--
-- accrued is Σ warranty_settlement_postings.amount_cents WHERE accrues_recovery (0129).
-- received is Σ warranty_recovery_actions.amount_cents WHERE action = 'RECEIVED' (here).
-- Nothing is denormalized onto the claim: a cached balance is a second source of truth for money, and this one
-- would drift the first time a receipt was voided.

-- ─────────────────────────────────────────────────────────────────────────────
-- The recovery action log. Every contact with the vendor about an outstanding claim, in order.
--
--   CONTACT_MADE — "called Scrubname0028 at Techo, left a message". No money.
--   PROMISED     — the vendor committed to an amount by a date. Money, but not ours yet: promised_cents feeds
--                  the board's "promised" column so a rep can see which chases have a commitment behind them.
--   RECEIVED     — the credit landed. This is the ONLY action that reduces outstanding.
--   DISPUTED     — the vendor is contesting. No money; surfaces the claim as at-risk.
--   NOTE         — free text.
--
-- WHY THE AMOUNT LIVES ON THE ACTION AND NOT ON THE CLAIM. Recoveries arrive in pieces (a partial credit note,
-- then the rest next month). One row per arrival with its own date is what makes "received" reconstructible and
-- what makes a mistaken receipt reversible by a compensating row rather than by editing history.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_recovery_actions (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  claim_id      UUID NOT NULL REFERENCES warranty_claims(id) ON DELETE CASCADE,
  -- Denormalized vendor fence, same reason 0091 denormalized branch_id: the per-vendor roll-up must not have to
  -- join back to the claim, and a claim whose vendor is later corrected must not silently re-attribute a
  -- recovery that was chased with the ORIGINAL vendor.
  vendor_id     UUID REFERENCES vendors(id) ON DELETE SET NULL,
  action        TEXT NOT NULL
                  CHECK (action IN ('CONTACT_MADE','PROMISED','RECEIVED','DISPUTED','NOTE')),
  amount_cents  BIGINT NOT NULL DEFAULT 0 CHECK (amount_cents >= 0),
  promised_date DATE,                            -- PROMISED: when the vendor said it would land
  reference     TEXT NOT NULL DEFAULT '',        -- the vendor's credit-note number / cheque no
  note          TEXT NOT NULL DEFAULT '',
  actor_id      UUID REFERENCES users(id) ON DELETE SET NULL,
  occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- The two money actions must carry money; the three non-money actions must not pretend to. Without this a
  -- CONTACT_MADE row with an amount would silently do nothing to the balance and look like a lost receipt.
  CONSTRAINT warranty_recovery_actions_amount_shape CHECK (
    (action IN ('PROMISED','RECEIVED') AND amount_cents > 0)
    OR (action NOT IN ('PROMISED','RECEIVED') AND amount_cents = 0)
  )
);
CREATE INDEX idx_warranty_recovery_actions_claim ON warranty_recovery_actions(claim_id, occurred_at);
CREATE INDEX idx_warranty_recovery_actions_vendor ON warranty_recovery_actions(vendor_id, action);
-- The board's hot read: received rows, per claim.
CREATE INDEX idx_warranty_recovery_actions_received ON warranty_recovery_actions(claim_id)
  WHERE action = 'RECEIVED';

-- ─────────────────────────────────────────────────────────────────────────────
-- The reconciliation view. One row per accruing claim: accrued, received, outstanding, and the aging bucket
-- measured from the day the accrual was posted (not from when the claim opened — a claim opened in January and
-- settled in June has been OWED for however long since June, and aging the receivable from January would
-- overstate every bucket).
--
-- A VIEW, not a table: the arithmetic has exactly one definition and no refresh to forget. The board reads it,
-- the tests assert against it, and there is no path by which a stored balance and the rows disagree.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE VIEW warranty_recovery_balances AS
SELECT
  c.id                                   AS claim_id,
  c.claim_no                             AS claim_no,
  c.branch_id                            AS branch_id,
  c.vendor_id                            AS vendor_id,
  p.amount_cents                         AS accrued_cents,
  COALESCE(r.received_cents, 0)          AS received_cents,
  p.amount_cents - COALESCE(r.received_cents, 0) AS outstanding_cents,
  COALESCE(r.promised_cents, 0)          AS promised_cents,
  p.posted_at                            AS accrued_at,
  r.last_action_at                       AS last_action_at,
  r.action_count                         AS action_count
FROM warranty_settlement_postings p
JOIN warranty_claims c ON c.id = p.claim_id
LEFT JOIN LATERAL (
  SELECT
    COALESCE(SUM(a.amount_cents) FILTER (WHERE a.action = 'RECEIVED'), 0) AS received_cents,
    COALESCE(SUM(a.amount_cents) FILTER (WHERE a.action = 'PROMISED'), 0) AS promised_cents,
    MAX(a.occurred_at)                                                    AS last_action_at,
    COUNT(*)                                                              AS action_count
  FROM warranty_recovery_actions a
  WHERE a.claim_id = p.claim_id
) r ON TRUE
WHERE p.accrues_recovery;
