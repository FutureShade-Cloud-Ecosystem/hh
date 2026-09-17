-- 0195 — the second-truck approval gate: a FIFTH policy on the ONE approvals queue, and the parked
-- raise that queue decides.
--
-- THE CLIENT'S CASE, verbatim: "If there is a situation where they're adding a second truck that's
-- unforeseen and there's some cost that you're trying to pass on to the customer they weren't
-- originally quoted on, we'll have some sort of approval gate for that."
--
-- 0194 gave that sentence its two numbers: `orders.quoted_freight_cents` (the frozen baseline) and
-- `orders.freight_cents` (the opening actual, which the conversion now actually writes). This
-- migration gives it its DECISION and its PARKED STATE, in that order, and the order matters.
--
-- ── THIS IS NOT A SECOND APPROVAL SYSTEM, AND HERE IS THE PROOF ──────────────────────────────────
--
-- There is exactly one approvals queue in this system: permission_approval_requests (0016, widened
-- by 0075). Every property that makes an approval an approval lives on it and stays there —
--
--   WHO ASKED         requested_by, stamped from the authenticated caller and never from a body
--   WHICH BRANCH      branch_id, which the reachability guard grades
--   WHAT KIND         policy_type, the CHECK widened in §1 below
--   THE STATE         status PENDING|APPROVED|REJECTED, moved ONLY by SaveDecisionTx's CAS
--   WHO DECIDED       decided_by / decided_at / rejection_reason
--   FOUR EYES         ApprovalRequest.Decide's requester≠approver rule, which is absolute
--
-- The table in §2 holds NONE of those things as its own truth. It holds the PROPOSED STATE — the
-- freight nobody has agreed to yet — and it carries `state` and `approval_id` as a MIRROR of a
-- decision made elsewhere, written by the very transaction that stamps the decision. Its `state`
-- column can only move inside identity's decide transaction; a row can no more approve itself than
-- a DRAFT journal can post itself.
--
-- Which is the point: THIS IS THE JE PATTERN. 0075 §3 parks a DRAFT gl_journals row, files a
-- JE_OVER_THRESHOLD request whose subject_ref is that journal, and posts the journal inside the
-- decide transaction. A freight raise has exactly the same shape — a durable artefact that can
-- legitimately sit in a non-effective state — so it gets exactly the same treatment.
--
-- ── WHY NOT THE MARGIN PATTERN (refuse-and-re-attempt) ───────────────────────────────────────────
--
-- MARGIN_BELOW_MINIMUM stores nothing: the clerk is refused, an override is approved, and the clerk
-- RE-SENDS the quote, which now passes. That works because re-attempting a quote costs one click on
-- a form that is still open in front of the person who filled it in.
--
-- Re-attempting a freight split does not. The dispatcher who proposed "four trucks, $260 / $260 /
-- $260 / $220" would have to be found again, hours later, and asked to retype four numbers they no
-- longer have in front of them — and every digit they mistype is money on a customer's invoice. The
-- whole reason to park the arithmetic is so that APPROVING it is the act that applies it, with the
-- figures the approver actually read. A gate whose approval creates a to-do item for the requester
-- has not closed the loop; it has moved it.
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- §1 is the 0075:41-43 widening verbatim in shape: DROP the CHECK, ADD it back with one more value.
-- Every value that was legal before is still legal, so a live row cannot be invalidated by it, and
-- the constraint is re-created inside the same transaction the runner wraps this file in — there is
-- no window in which permission_approval_requests is unconstrained on a live database.
-- §2 is a CREATE TABLE and three CREATE INDEX. Nothing is dropped, nothing is renamed, no existing
-- row is read or written. Staging is never wiped: this applies clean from zero and on top of a
-- database already at 0191 (and at 0194, which it does not touch), and re-running is a no-op
-- because cmd/migrate records the version and never replays a file.
-- Reverting costs `DROP TABLE delivery_freight_raises` plus re-narrowing the CHECK, which is safe
-- only once no row holds the fifth value — the usual order for a widened enum.

-- ── §1. the fifth policy_type ───────────────────────────────────────────────────────────────────
--
-- FREIGHT_ABOVE_QUOTE, and the name is doing work. It is not "FREIGHT_SPLIT" (a split within the
-- quoted freight is an ordinary dispatch act and must sail straight through) and not "EXTRA_TRUCK"
-- (a fourth truck at no extra charge is nobody's business but the yard's). The thing that needs a
-- decision is precisely the INVARIANT BREACH: the customer-facing freight would rise ABOVE what the
-- customer was quoted. That is what the constant says, so a reader of the queue cannot mistake this
-- for a logistics approval. It goes on a customer's charge.
--
-- identity.CanApprove's switch DEFAULTS TO FALSE, so this value is undecidable by everyone until its
-- arm is written — a widened CHECK on its own opens nothing. The arm added alongside this migration
-- routes it to railApproverRoles (GM / Scrubname0010 Scrubname0007 / platform admin), the same authority
-- MARGIN and JE already answer to, and deliberately NOT to HR: a person who cannot see a customer's
-- money has no business raising what that customer is charged.
--
-- NOTE WHAT DOES NOT CHANGE HERE, AND WHY IT MATTERS: no column is added to this table for the
-- money. 0075 rides a JE's debit total in the existing `data` JSONB (data->>'amount_cents') because
-- a journal total is finance information for a finance approver. A freight raise is a figure on a
-- CUSTOMER's invoice, and GET /api/v1/staff/approvals is open to every authenticated staff member —
-- so putting the quoted/proposed pair on the approval row would publish a customer's delivery charge
-- to the whole roster through a queue that has no money gate of its own. The figures live in §2,
-- behind dispatch's own visibility policy, and reach a screen only through the one method that has
-- already asked it. The approval row carries a sentence with no numbers in it.
ALTER TABLE permission_approval_requests DROP CONSTRAINT permission_approval_requests_policy_type_check;
ALTER TABLE permission_approval_requests ADD CONSTRAINT permission_approval_requests_policy_type_check
  CHECK (policy_type IN ('MARGIN_BELOW_MINIMUM','CREDIT_LIMIT_EXCEEDED','COD_CONSTRAINT','JE_OVER_THRESHOLD','FREIGHT_ABOVE_QUOTE'));

-- ── §2. the parked raise ────────────────────────────────────────────────────────────────────────
--
-- One row = one freight raise somebody has PROPOSED and nobody has agreed to. It holds the proposal
-- and only the proposal: the freight asked for, the baseline it exceeds, where the charge stands
-- today, how many trucks the ask is for, the per-load shares if the dispatcher named them, who asked,
-- when, and — as a mirror of a decision owned elsewhere — its state and its approval row.
--
-- THREE MONEY COLUMNS, NOT TWO, and the third is the one people leave out. `quoted` is what the
-- customer was shown and `proposed` is what they would be charged, but a re-split of an ALREADY
-- APPROVED raise proposes a figure above the quote all over again — and gating it a second time
-- would make an approver decide the same money twice, which is how approvers learn to click through.
-- `current_freight_cents` records where the charge actually stood when the ask was made, so the gate
-- can fire on a NEW rise rather than on the standing one, and so the approver's card can say which
-- of the two it is.
--
-- order_no AND customer_name ARE COPIED HERE ON PURPOSE. They are display, and display normally
-- belongs at the join — dispatch already reaches customers.name that way for the load board. But an
-- approval is a RECORD OF WHAT SOMEBODY WAS ASKED TO AGREE TO, and a card that re-derives its
-- subject can end up showing a name that was edited after the fact. The approver decided about "Scrubname0002
-- of speaker-0006, #SO-TRT-1041"; that is what the row says forever.
--
-- amounts_cents IS NULLABLE AND NULL IS NOT AN EMPTY LIST. NULL means "even split" — the dispatcher
-- asked for N trucks and let the server divide, so the arithmetic must be done at APPLY time against
-- the loads that actually exist then. A non-null array is the dispatcher's own per-load shares,
-- validated to sum to proposed_freight_cents before it was ever stored. Storing an even split as a
-- materialised array would freeze a division that may need to be re-done, and would make the two
-- cases indistinguishable a month later.
CREATE TABLE delivery_freight_raises (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id               UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  branch_id              UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  -- What the approver read, frozen (see above).
  order_no               TEXT NOT NULL DEFAULT '',
  customer_name          TEXT NOT NULL DEFAULT '',
  -- The three figures. All three are >= 0 because freight is a charge, never a credit; a refund is
  -- a credit note, which is billing's act and not dispatch's.
  quoted_freight_cents   BIGINT NOT NULL CHECK (quoted_freight_cents >= 0),
  current_freight_cents  BIGINT NOT NULL CHECK (current_freight_cents >= 0),
  proposed_freight_cents BIGINT NOT NULL CHECK (proposed_freight_cents >= 0),
  loads                  INT NOT NULL CHECK (loads BETWEEN 1 AND 10),
  amounts_cents          BIGINT[],
  -- Provenance. RESTRICT rather than 0016's CASCADE on its own requested_by, and the difference is
  -- deliberate: an override request is a workflow artefact, whereas this row is the evidence for a
  -- figure that reached a customer's invoice. Deleting the person must not delete the reason.
  requested_by           UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  requested_by_role      TEXT NOT NULL DEFAULT '',
  requested_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- The MIRROR of a decision owned by permission_approval_requests. Never moved by dispatch on its
  -- own authority; only ever by the transaction that stamps the request.
  state                  TEXT NOT NULL DEFAULT 'PARKED' CHECK (state IN ('PARKED','APPLIED','REFUSED')),
  approval_id            UUID REFERENCES permission_approval_requests(id) ON DELETE SET NULL,
  decided_by             UUID REFERENCES users(id) ON DELETE SET NULL,
  decided_at             TIMESTAMPTZ,
  -- A row in this table IS a raise, by definition. The gate that files it has already established
  -- the invariant breach; encoding it here means a future writer that has not can never get a row in
  -- — including one that lands via a fixture, a backfill, or a hand-typed INSERT at 2am.
  CONSTRAINT delivery_freight_raises_above_quote CHECK (proposed_freight_cents > quoted_freight_cents),
  -- A decided row names its decider, and an undecided row does not pretend to have one.
  CONSTRAINT delivery_freight_raises_decided_pair CHECK (
    (state = 'PARKED'  AND decided_at IS NULL) OR
    (state <> 'PARKED' AND decided_at IS NOT NULL)
  )
);

COMMENT ON TABLE delivery_freight_raises IS
  'One PROPOSED rise of a customer''s delivery charge above the freight they were quoted, parked until the approvals queue decides it. The decision itself lives in permission_approval_requests; this table holds only the state that decision applies (the gl_journals DRAFT pattern).';

COMMENT ON COLUMN delivery_freight_raises.amounts_cents IS
  'The dispatcher''s own per-load shares, or NULL meaning "divide it evenly at apply time". NULL is not an empty list: an even split must be re-computed against the loads that exist when it is applied.';

COMMENT ON COLUMN delivery_freight_raises.current_freight_cents IS
  'Scrubname0032 the customer-facing freight stood when the raise was asked for. The gate fires on a rise above BOTH this and the quoted baseline, so re-splitting an already-approved raise does not ask an approver to decide the same money twice.';

-- ONE PARKED RAISE PER ORDER. Partial, so the history of applied and refused raises is unbounded
-- while the live queue can never hold two competing asks against one invoice — which is the state
-- that would let an approver agree to $260 and a second approver agree to $340 for the same trucks.
-- The service refuses the second ask in words; this index is what makes that refusal true under a
-- race rather than merely likely.
CREATE UNIQUE INDEX uq_freight_raise_one_parked_per_order
  ON delivery_freight_raises (order_id) WHERE state = 'PARKED';

-- The queue read: what is waiting, per branch.
CREATE INDEX idx_freight_raise_branch_state ON delivery_freight_raises (branch_id, state);

-- The card read: the approvals surface holds an approval id and needs the figures behind it.
CREATE INDEX idx_freight_raise_approval ON delivery_freight_raises (approval_id);
