-- 0160: DIB-479 / OR-2 — the two SPLIT-BUYER portal roles.
--
-- WHY. CP-07 shipped three portal roles (0019). `buyer` holds BOTH "may pay invoices" and "may
-- commit orders", and a contractor outfit does not work that way: the project manager builds the
-- order, the bookkeeper pays the bill. The HH Pro portal models exactly that split (`pm` and `ap`),
-- and until the ERP can express it, the portal must either render a role picker the server cannot
-- enforce — the permission-gate-silently-off bug class — or lie about who its people are.
--
-- WHAT. Two new values in the role CHECK set on both role-bearing tables:
--
--   buyer_no_pay  — buyer MINUS {pay_invoices, manage_payment_methods}   (buys, cannot pay)
--   ap_finance    — buyer MINUS {submit_rfq, create_orders, edit_delivery_instructions}
--                                                                        (pays, cannot buy)
--
-- The capability sets themselves live in Go (internal/portal/rbac.go, CapabilitiesFor) — this
-- migration only widens the value domain the columns will accept. That split is deliberate and it
-- is the one from 0019: the DB owns WHICH ROLE STRINGS EXIST, the code owns WHAT A ROLE MAY DO, and
-- no capability is ever stored, so re-shaping a persona is a code change with no data migration.
--
-- NO DATA CHANGES. Not one existing row moves. Widening a CHECK set can only ever admit rows it
-- previously refused; every account_admin/buyer/field_crew row stays exactly as valid as it was.
--
-- IDEMPOTENT. Each constraint is dropped IF EXISTS and re-added, so re-running the file is a no-op
-- and a partially-applied run (the DROP landed, the ADD did not) heals on the next pass. The
-- constraint names are the ones Postgres generated for the inline CHECKs in 0019
-- (<table>_role_check); they are dropped by that name and re-added under it, so the schema keeps
-- one obvious name for this rule rather than accumulating a second one beside it.

ALTER TABLE portal_users   DROP CONSTRAINT IF EXISTS portal_users_role_check;
ALTER TABLE portal_users   ADD  CONSTRAINT portal_users_role_check
  CHECK (role IN ('account_admin','buyer','buyer_no_pay','ap_finance','field_crew'));

ALTER TABLE portal_invites DROP CONSTRAINT IF EXISTS portal_invites_role_check;
ALTER TABLE portal_invites ADD  CONSTRAINT portal_invites_role_check
  CHECK (role IN ('account_admin','buyer','buyer_no_pay','ap_finance','field_crew'));

COMMENT ON COLUMN portal_users.role IS
  'CP-07 portal role. account_admin | buyer | buyer_no_pay | ap_finance | field_crew. '
  'Capabilities are derived in Go (portal.CapabilitiesFor), never stored: a role is an identity, '
  'a capability set is a policy, and only one of the two belongs in a row.';
