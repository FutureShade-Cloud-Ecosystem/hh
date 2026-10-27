-- 0436: the org stamp rule as one SQL function, for a box provisioned after migrate (SF-2p2).
--
-- 0432 stamps every invoice's org and adds invoices_org_id_not_null only on a box that already had an org row when it
-- ran. A box the identity populate provisions later (every demo box) kept its NULL org invoices and got no check. This is
-- 0432's body, parameterised and otherwise unchanged, so the populate runs the SAME rule through each owning module's
-- seam (billing's StampInvoiceOrgs calls it for invoices; UX-95c adds orders and tasks) instead of a second copy:
--   no org row on the box   nothing is changed and 0 is answered;
--   otherwise               a row with no branch, or at a location with no org, is NOT guessed at: it RAISES with the
--                           counts of each (the words of 0432); every other NULL org row takes its branch's
--                           locations.org_id; then CHECK (org_id IS NOT NULL) is added NOT VALID under the given name
--                           only if missing, and VALIDATEd. The answer is the rows stamped.
-- Idempotent: it writes only where org_id IS NULL, adds the check only if missing, deletes nothing. The table and the
-- column names are quoted identifiers (%I) of regclass and caller supplied names; the caller is a module's own seam.
-- 0432 is not edited.
CREATE FUNCTION org_stamp_and_check(p_table regclass, p_branch_col text, p_check text) RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
    org_count integer;
    no_branch bigint;
    no_org    bigint;
    stamped   bigint;
    what      text := regexp_replace(p_table::text, '^.*\.', '');
BEGIN
    SELECT count(*) INTO org_count FROM org;
    IF org_count = 0 THEN
        RAISE NOTICE 'org_stamp_and_check: the box has no org row, so no % row was changed and no constraint was added', what;
        RETURN 0;
    END IF;

    EXECUTE format('SELECT count(*) FROM %s WHERE org_id IS NULL AND %I IS NULL', p_table, p_branch_col) INTO no_branch;
    EXECUTE format('SELECT count(*) FROM %s t JOIN locations l ON l.id = t.%I WHERE t.org_id IS NULL AND l.org_id IS NULL',
                   p_table, p_branch_col) INTO no_org;
    IF no_branch > 0 OR no_org > 0 THEN
        RAISE EXCEPTION 'org stamp of %: % row(s) have no branch and % row(s) sit at a location with no org; stamp the locations'' org_id (migration 0421 on a one org box, the identity populate otherwise) and run this again, a row is not given an org by guessing', what, no_branch, no_org;
    END IF;

    EXECUTE format('UPDATE %s t SET org_id = l.org_id FROM locations l WHERE l.id = t.%I AND t.org_id IS NULL', p_table, p_branch_col);
    GET DIAGNOSTICS stamped = ROW_COUNT;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = p_check AND conrelid = p_table) THEN
        EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I CHECK (org_id IS NOT NULL) NOT VALID', p_table, p_check);
    END IF;
    EXECUTE format('ALTER TABLE %s VALIDATE CONSTRAINT %I', p_table, p_check);
    RETURN stamped;
END
$$;
