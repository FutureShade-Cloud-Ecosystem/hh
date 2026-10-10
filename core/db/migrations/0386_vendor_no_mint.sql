-- 0386_vendor_no_mint.sql
-- Owner module: internal/purchasing (the vendor create route, the roles roadmap's Buying and Stock
-- core slice: "no vendor create or edit", section 1.5).
--
-- A vendor number mint for staff created vendors. The seeded set carries word slugs (V-TECHO,
-- V-UNILOCK, migration 0057 and the dibbits seed); the mint draws zero padded numerics from a
-- sequence behind the prefix VM-, which cannot collide with any of them and which the load doors
-- refuse as a legacy key (vendorload.go), so the mint and a load never share a key, and the number is minted server side in the
-- INSERT, never accepted from the caller, the same rule the branch scoped PO number states (0120).
--
-- A plain SEQUENCE rather than 0120's per branch counter TABLE, on purpose: a vendor is org wide,
-- there is exactly one counter for the whole set, and a counter table's whole reason (a new row of
-- counters per branch with no DDL) has no work to do here.

CREATE SEQUENCE vendor_no_seq START 101;

CREATE FUNCTION next_vendor_no() RETURNS text AS $$
  SELECT 'VM-' || lpad(nextval('vendor_no_seq')::text, 4, '0')
$$ LANGUAGE sql;
