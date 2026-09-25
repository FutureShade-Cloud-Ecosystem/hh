-- 0213 — DIB-612: a saved sort is presentation curation, so it lives in the 0172 substrate.
--
-- WHAT THIS CHANGES, AND WHAT IT DELIBERATELY DOES NOT.
--
-- 0172 constrained item_kind to ('panel','column','chip','action') in BOTH item tables. DIB-612 asks
-- for "the user's saved sort still wins", and the only way that sentence means anything is if a
-- person's sort is STORED. This widens the enum by one value in both tables and does nothing else:
-- no column is added, no row is written, no row is read, no row is moved.
--
-- IT MUST NOT TOUCH ANYBODY'S EXISTING CURATION, and that is a property this file is written to have
-- rather than one it hopes for. Widening a CHECK is a DROP of the old constraint and an ADD of a
-- wider one; the ADD re-validates every existing row against a constraint that is a strict SUPERSET
-- of the one they already satisfy, so every row passes and no row is rewritten. There is no UPDATE
-- and no DELETE in this file. The swarm gate plants surface_user_items rows, fingerprints them, runs
-- this migration and asserts the fingerprint is unchanged; that is the check, and it passes because
-- there is nothing here that could change one.
--
-- WHY A NEW KIND RATHER THAN A TYPED COLUMN. The substrate's whole argument (0172's header, the
-- 0140/0149 ruling) is that presentation curation is content-free strings + positions, whole-or-
-- absent per (user, surface, kind), reset by DELETE. "Which way this person's list is sorted" is
-- exactly that sentence — and a typed `sort_key`/`sort_desc` pair on surface_user_workspace would
-- have been a second mechanism with the same semantics, a second reset path, and a second chance to
-- get reset-is-a-DELETE subtly wrong. Deleting the person's sort rows IS "sort the way the register
-- shipped", with no merge to unwind, because that is what every other kind already means.
--
-- THE SERVER STILL VALIDATES SHAPE ONLY (OQ-3). item_id stays a frontend-registry id: this migration
-- knows that sorts exist, and knows nothing whatever about which columns are sortable. The register's
-- own whitelist (backend/internal/billing/register_sort.go) is a separate, independent guard on the
-- READ path, so a curation row naming a column that has since been retired resolves to the shipped
-- order rather than to anything at all happening.
--
-- IDS ARE ADD-ONLY (0140's rule). Nothing here renames an id, and nothing here seeds one: a sort is
-- a person's own preference, and a dealer default sort would be a product decision nobody has made.
-- surface_role_items takes the wider enum anyway, because the two tables are one substrate and an
-- enum that differed between them is the drift this pair of constraints exists to prevent.

ALTER TABLE surface_role_items
    DROP CONSTRAINT surface_role_items_item_kind_check;
ALTER TABLE surface_role_items
    ADD CONSTRAINT surface_role_items_item_kind_check
    CHECK (item_kind IN ('panel', 'column', 'chip', 'action', 'sort'));

ALTER TABLE surface_user_items
    DROP CONSTRAINT surface_user_items_item_kind_check;
ALTER TABLE surface_user_items
    ADD CONSTRAINT surface_user_items_item_kind_check
    CHECK (item_kind IN ('panel', 'column', 'chip', 'action', 'sort'));

COMMENT ON TABLE surface_user_items IS
  'DIB-507: one person''s presentation override for one surface. Whole-or-absent per (user,surface,kind); reset to dealer default = DELETE. DIB-612 added the `sort` kind: at most one visible row, whose registry id names the column and the direction together.';
