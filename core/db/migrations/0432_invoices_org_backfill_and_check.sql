-- 0432: every invoice carries its org (SF-2p, SF-3 folded in).
--
-- The staff invoices feed fences `i.org_id = $1`, so an invoice with no org_id is invisible on every Counter Till.
-- The credit memo writer stamped none (it now takes its branch's org), and the invoices the migrations 0046, 0055
-- and 0179 plant, and the Dibbits seed, were written before any org existed. This stamps the ones that can be
-- stamped and then makes a missing org an error.
--
--   no org row on the box      nothing is changed. A fresh box has no org yet (the identity populate provisions it
--                              and stamps the locations), so no invoice can be told its org and no constraint can
--                              hold; the CI migrate-only database and a new demo box are this case.
--   one or more org rows       every invoice whose org_id IS NULL takes its branch's locations.org_id. A row with
--                              no branch, or whose location has no org, is NOT guessed at: the migration RAISES
--                              and says how many of each, so the locations are stamped (0421 does it for a box
--                              with exactly one org; the identity populate otherwise) before this runs again.
--                              Then CHECK (org_id IS NOT NULL) is added NOT VALID and VALIDATEd.
--
-- The backfill goes through 0422's trg_sync_seq_draw (BEFORE INSERT OR UPDATE on invoices), which re-serves every
-- stamped row with a new seq; no separate re-draw is written. Idempotent: it writes only where org_id IS NULL and
-- adds the constraint only if it is missing. It deletes nothing.
DO $$
DECLARE
    org_count   integer;
    no_branch   integer;
    no_org      integer;
    stamped     integer;
BEGIN
    SELECT count(*) INTO org_count FROM org;
    IF org_count = 0 THEN
        RAISE NOTICE '0432: the box has no org row, so no invoice was changed and no constraint was added (the populate that provisions the org stamps the locations)';
        RETURN;
    END IF;

    SELECT count(*) INTO no_branch FROM invoices WHERE org_id IS NULL AND branch_id IS NULL;
    SELECT count(*) INTO no_org
      FROM invoices i JOIN locations l ON l.id = i.branch_id
     WHERE i.org_id IS NULL AND l.org_id IS NULL;
    IF no_branch > 0 OR no_org > 0 THEN
        RAISE EXCEPTION '0432: % invoice(s) have no branch and % invoice(s) sit at a location with no org; stamp the locations'' org_id (migration 0421 on a one org box, the identity populate otherwise) and run this again, an invoice is not given an org by guessing', no_branch, no_org;
    END IF;

    UPDATE invoices i SET org_id = l.org_id
      FROM locations l
     WHERE l.id = i.branch_id AND i.org_id IS NULL;
    GET DIAGNOSTICS stamped = ROW_COUNT;
    RAISE NOTICE '0432: % invoice(s) with no org took their branch''s org', stamped;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'invoices_org_id_not_null' AND conrelid = 'invoices'::regclass) THEN
        ALTER TABLE invoices ADD CONSTRAINT invoices_org_id_not_null CHECK (org_id IS NOT NULL) NOT VALID;
    END IF;
    ALTER TABLE invoices VALIDATE CONSTRAINT invoices_org_id_not_null;
END
$$;
