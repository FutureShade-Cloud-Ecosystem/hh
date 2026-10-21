-- 0421: the locations take the org when the box has exactly one.
--
-- Every stock, branch and reach read holds to the caller's org through locations.org_id (UX-88), and the
-- branches the migrations plant (0003) carry none. The identity populate stamps them now, but a box that
-- was populated before that, or provisioned by hand, has an org and branches with no org on them, and
-- every one of those reads answers empty there. This stamps them once, and only where it can be certain
-- which org they belong to: a box with exactly one org row.
--
--   one org row    every location whose org_id IS NULL takes it (rows that already name an org are kept)
--   zero org rows  nothing is touched (a fresh box: the populate that provisions the org stamps them)
--   several orgs   nothing is touched (which org owns a branch is not a thing a migration can know)
--
-- Idempotent: it only writes where org_id IS NULL, so a rerun writes nothing. It deletes nothing.
DO $$
DECLARE
    org_count integer;
    only_org  uuid;
    stamped   integer;
BEGIN
    SELECT count(*) INTO org_count FROM org;
    IF org_count = 1 THEN
        SELECT id INTO only_org FROM org;
        UPDATE locations SET org_id = only_org, updated_at = now() WHERE org_id IS NULL;
        GET DIAGNOSTICS stamped = ROW_COUNT;
        RAISE NOTICE '0421: % location(s) with no org took the box''s only org', stamped;
    ELSIF org_count = 0 THEN
        RAISE NOTICE '0421: the box has no org row, so no location was changed (the populate that provisions the org stamps them)';
    ELSE
        RAISE NOTICE '0421: the box has % org rows, so no location was changed (which org owns a branch is not decidable here)', org_count;
    END IF;
END
$$;
