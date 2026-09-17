-- 0157: DIB-462 — targeted payment notifications (per-user opt-in rules) +
--       DIB-463 — the payments-today home tile's role gating.
-- Owner: internal/messaging (rules) + internal/dashboard (tile row). Additive, non-breaking; follows 0152.
--
-- ── What the client asked for, and what it is NOT ───────────────────────────────
-- Phase-0 sync, 2026-08-11. Three cases where somebody genuinely wants to be told about ONE payment:
--
--   (a) an ONLINE / remote payment landing on an account or a statement — "I don't have to call them
--       again"; nobody at a counter saw it happen, so nobody knows unless the system says so;
--   (b) a payment taken at a DIFFERENT BRANCH than the order's — the money is on your job and it
--       arrived at somebody else's till;
--   (c) a payment against an ORDER THE USER OWNS (they are the assigned rep).
--
-- This does NOT reopen DIB-449. A routine payment still rolls up: no per-event channel message, one
-- day line, individual payments as thread replies. A rule is ADDITIVE — it sends a targeted notice to
-- the ONE person who asked for it, on top of a rollup that is unchanged. If every rule in this table
-- were deleted the system would behave exactly as DIB-449 shipped it, which is the property that makes
-- this safe to add to a surface whose whole fix was "stop shouting".
--
-- ── Why rows are OPT-IN and absent by default ──────────────────────────────────
-- 0152 rejected seeding a msg_notification_prefs row per user for two reasons that apply here word for
-- word: a seeded roster is a snapshot of today's staff (it would not cover next month's hire), and it
-- FABRICATES A CHOICE NOBODY MADE. So this table stores DECISIONS, not defaults:
--
--     no row  → the rule is OFF. That is the shipped, quiet behaviour and it needs no storage.
--     row     → this user made a choice. enabled=TRUE follows it; enabled=FALSE records that they
--               turned it back off (kept, not deleted, so "I already decided about this" survives).
--
-- The consequence is stated as an invariant because a test pins it
-- (TestPaymentRules_NoRuleRowsMeansNobodyIsNotified): AN EMPTY TABLE NOTIFIES NOBODY.
CREATE TABLE msg_payment_notify_rules (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- The rule this row answers for. Text + CHECK rather than an enum type, matching msg_event_classes:
  -- the rule set is a CODE constant (messaging/payment_rules.go PaymentRules) and the CHECK is the
  -- schema's copy of it, so a value the code does not know can never be stored.
  --
  --   ONLINE        (a) the payment reached us with nobody at a counter — portal / statement / remote.
  --   CROSS_BRANCH  (b) the branch that took the payment is not the branch that owns the order.
  --   OWN_ORDER     (c) the payment is against an order this user is the assigned rep on.
  rule       TEXT NOT NULL CHECK (rule IN ('ONLINE','CROSS_BRANCH','OWN_ORDER')),
  enabled    BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, rule)
);

-- The hot read is the fan-out: "who turned THIS rule on?", once per routine payment. Partial on
-- enabled so the index holds only the opted-in minority — which, quiet-by-default being the point, is
-- the whole design intent expressed as an index.
CREATE INDEX idx_msg_payment_notify_rules_enabled
  ON msg_payment_notify_rules(rule) WHERE enabled;

-- DELIBERATELY NOT SEEDED. Every rule ships OFF for every user, including the people who asked for the
-- feature. An opt-in that arrives pre-opted-in is not an opt-in.

-- ═══════════════════════════════════════════════════════════════════════════════
-- DIB-463 — the payments-today tile, role-gated through the SHIPPED 0140 mechanism
-- ═══════════════════════════════════════════════════════════════════════════════
--
-- The tile itself is a catalog entry in internal/dashboard/model.go ('payments.today'). What lives
-- HERE is the only thing that decides WHO SEES IT, and it is the same table every other tile's role
-- default lives in — dashboard_profile_tiles (0140). No new visibility mechanism, no role check in
-- tile code, no second place to look when someone asks why they cannot see it.
--
-- 'manager' and 'admin' are the profiles the owner/finance roles map to in 0140's role→profile table:
-- BRANCH_MANAGER, SALES_MANAGER, PAYABLES_RECEIVABLES and FINANCIAL_CONTROLLER resolve to 'manager';
-- GENERAL_MANAGER and SUPER_ADMIN resolve to 'admin'. Counter, yard and driver do not get it, and the
-- catalog entry ships DefaultVisible=false so no curated layout picks it up by accident either.
--
-- Positions: appended after each profile's existing rows (manager ends at 7, admin at 9) so nobody's
-- home screen is re-ordered by this migration. An admin can move or hide it in the layout editor like
-- any other tile — this row is a default, not a lock.
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible) VALUES
  ('manager', 'payments.today', 8,  true),
  ('admin',   'payments.today', 10, true)
ON CONFLICT (profile, tile_id) DO NOTHING;

-- GATED (do NOT build here):
--   * a fourth rule for account-level payments with no order (the customer-level rep). The ruling in
--     0136 is that rep assignment is CUSTOMER-level with an ORDER override, so this is buildable with
--     no schema change — but "own order" is what was asked for, and widening it to "own customer"
--     would notify a rep about money on a job somebody else wrote.
--   * branch-scoping the ONLINE / CROSS_BRANCH fan-out. Today an opted-in user hears about every
--     matching payment, which is defensible because the switch is theirs and it is off until they
--     flip it. Narrowing it to "payments touching my branches" is a real request and a one-predicate
--     change to UsersWithPaymentRule; it has not been ruled on.
--   * a per-rule delivery channel (e-mail / SMS). The notice rides the SHIPPED per-user DM channel
--     (EnsureRepDMChannel, 0136) so it lands in the same inbox, obeys the same DIB-268 badge prefs,
--     and needs no new transport.
