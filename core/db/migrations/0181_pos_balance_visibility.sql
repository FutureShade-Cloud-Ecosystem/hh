-- 0181 — the counter's ACCOUNT STANDING: who may see a customer's past-due balance at the till.
--
-- WHY THIS EXISTS. speaker-0007, Aug 26, describing how he actually does a price check: he starts a
-- counter sale, because that gives him the price profile, the inventory, the lead time — "and it
-- also gives me an opportunity to ask them to pay their bill." So when an account with a past-due
-- balance lands on a ticket, the Customer step says so. As a FACT, in the same quiet clothes as
-- every other fact on that card. Never a nag, never a block: this column decides visibility and
-- nothing else — the credit gate (billing/credit_gate.go) still owns whether a sale may proceed,
-- and it is unchanged by this migration.
--
-- WHY A TYPED POLICY COLUMN AND NOT A CURATION ROW (D-105). The presentation substrate carries
-- choices that are ergonomics — which tiles, which columns, which keys. This is not one of those.
-- A customer's outstanding balance is money about a third party, and who may see it is a
-- SECURITY-BEARING decision, which the ratified hybrid puts in a typed, CHECK-constrained policy
-- column read at the marshal layer. Same shape, same vocabulary and the same admin PUT as
-- margin_visibility (0170) — deliberately, so a dealer configuring the counter meets one idea about
-- visibility rather than two.
--
--   ALL_STAFF   (default) every staff caller receives the balance fields.
--   MANAGER_UP  Tier-1/Tier-2 only (identity.StaffContext.IsManager); frontline callers receive a
--               payload with the balance fields BYTE-ABSENT — never CSS-hidden, never sent-and-hidden.
--   HIDDEN      nobody does; the dealer has switched the surface off.
--
-- WHY THE DEFAULT IS ALL_STAFF AND MARGIN'S IS MANAGER_UP. They protect opposite things. Margin is
-- the DEALER's own position and leaks the business's economics to whoever is standing at the
-- screen. A past-due balance is the CUSTOMER's own fact, which the counter person is being asked to
-- raise with the person it belongs to — a default that hid it from the counter would ship the
-- feature switched off for the one role that asked for it. A dealer who disagrees flips this in one
-- admin PUT, which is the point of it being a column.

ALTER TABLE pos_policy
  ADD COLUMN balance_visibility TEXT NOT NULL DEFAULT 'ALL_STAFF'
    CHECK (balance_visibility IN ('MANAGER_UP', 'ALL_STAFF', 'HIDDEN'));

COMMENT ON COLUMN pos_policy.balance_visibility IS
  'Aug-26 counter feedback (N4): which staff tiers receive a customer''s past-due balance on the POS Customer step (marshal-gated server-side, byte-absent otherwise). ALL_STAFF (default) | MANAGER_UP | HIDDEN. Visibility only — the credit gate still owns whether a sale may proceed.';
