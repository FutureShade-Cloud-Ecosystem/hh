-- 0174 — DIB-506 simplification pass: resume-the-open-object on a configurable surface.
--
-- WHY. A browser refresh mid-quote landed back on compose; the in-flight draft was reachable only
-- through the legacy history disclosure. The critique's fix: persist the open quote per user via
-- the EXISTING surface_user_workspace kit and restore it on load. The column is surface-neutral
-- (the kit's discipline — no quote vocabulary in the substrate): it holds "the object this person
-- last had open on this surface", an opaque reference the surface's own frontend interprets.
--
-- '' = nothing open (the default for every existing row — a no-op backfill). The value is display
-- state, not authority: restoring it still goes through every server read gate the object has.
ALTER TABLE surface_user_workspace
    ADD COLUMN open_ref text NOT NULL DEFAULT ''
        CHECK (char_length(open_ref) <= 64);

COMMENT ON COLUMN surface_user_workspace.open_ref IS
  'DIB-506: the object (e.g. quote id) this person last had open on this surface — opaque to the kit, interpreted by the surface frontend; '''' = nothing open. Display state only, never authority.';
