-- 0448: FW1-12c: the credit application key's org becomes a real reference to org (id).
-- Owner module: internal/billing (the apply door's own key table, 0447).
--
-- 0447 carried org_id as TEXT with no foreign key, so the empty string could stand for "no org" and
-- org-less callers shared one key scope. Production never writes one: the staff gate resolves an
-- org uuid or refuses, and the door's handler is the apply's only caller. This migration gives the
-- column the scoped siblings' own shape (0214's tables, 0388's org_signin_method, both
--     org_id UUID NOT NULL REFERENCES org (id)):
--
--   * the type becomes UUID, so a caller whose org is not a uuid fails at the column;
--   * the foreign key references org (id), so a uuid that is no org row fails loudly too;
--   * the primary key (org_id, idempotency_key) and the NOT NULL ride along unchanged.
--
-- The door refuses an empty org in words before any write in the same change, so no new empty org
-- row can be written after this lands.
--
-- EXISTING ROWS: none can carry an empty org once the door refuses it. A row whose org_id is the
-- empty string has no org it could be given by guessing, so this migration FAILS LOUDLY on it and
-- changes nothing: the RAISE below aborts the migration (and the whole migrate run) naming the
-- count. The same is true of a non empty value that is not a uuid (the cast refuses it) and of a
-- uuid that names no org row (the foreign key refuses it); none of the three is rewritten, renamed
-- or dropped. On the boxes this route has reached, the table carries only rows the 0447 door wrote
-- from a resolved org, so a clean box converts with no rows touched.
DO $$ DECLARE empty_org bigint;
BEGIN
    SELECT count(*) INTO empty_org FROM credit_application_keys WHERE org_id = '';
    IF empty_org > 0 THEN
        RAISE EXCEPTION 'migration 0448: credit_application_keys holds % row(s) whose org_id is the empty string; an org less caller wrote them and no org can be given them by guessing, so the migration refuses to run: restore the box or delete those rows and have their applications made again', empty_org;
    END IF;
END
$$;

ALTER TABLE credit_application_keys
    ALTER COLUMN org_id SET DATA TYPE UUID USING org_id::uuid,
    ADD CONSTRAINT credit_application_keys_org_id_fkey FOREIGN KEY (org_id) REFERENCES org (id);
