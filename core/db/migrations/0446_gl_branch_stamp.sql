-- 0446: FW1-08b part B (FROM-PRINCIPAL 162, 176): every gl_journal_lines row carries a branch.
--
-- The wave's census (core/cmd/core/fw1_branch_census_red.go and the test that runs it) already
-- stamps every writer that posts a journal, so a box this migration runs on is expected to carry
-- no unstamped line. A box that does carry one is a box with history this migration must not
-- guess at: the pre check below COUNTS the unstamped lines first and, when there are any,
-- refuses in words naming each branchless journal (its id, its source and its date), so an old
-- close is never validated blind.
--
-- Nothing is backfilled here. A pre 629 year end close is not backfilled to the head office, and
-- there is no reverse and rerun tool: the lines are stamped at their own branch by whoever owns
-- that journal, and this migration is run again. The operator's runbook is
-- docs/runbooks/gl-branch-stamp-0446.md.
--
-- The CHECK is added NOT VALID and then VALIDATEd, so the constraint lands without the long
-- exclusive lock a validated ADD CONSTRAINT would take, and the VALIDATE is the moment the rule
-- is proved against the rows that are already there.
--
-- The date in the refusal is the journal's own posted_at read in UTC, so the words are the same
-- on every box whatever the server's timezone setting is.

DO $$
DECLARE
    unstamped bigint;
    journals  bigint;
    detail    text;
BEGIN
    -- The count comes FIRST: it is the answer the refusal leads with.
    SELECT count(*) INTO unstamped FROM gl_journal_lines WHERE branch_id IS NULL;
    IF unstamped = 0 THEN
        RETURN;
    END IF;

    SELECT count(DISTINCT journal_id) INTO journals
      FROM gl_journal_lines
     WHERE branch_id IS NULL;

    SELECT string_agg(
             format('  journal %s (source %s, dated %s) carries %s unstamped line(s)',
                    j.id::text,
                    coalesce(j.source, 'UNKNOWN'),
                    coalesce(to_char(j.posted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), 'no date'),
                    c.cnt::text),
             E'\n' ORDER BY j.id::text)
      INTO detail
      FROM (SELECT journal_id, count(*) AS cnt
              FROM gl_journal_lines
             WHERE branch_id IS NULL
             GROUP BY journal_id) c
      JOIN gl_journals j ON j.id = c.journal_id;

    RAISE EXCEPTION '%',
      format('gl: %s journal line(s) across %s journal(s) carry no branch:%s%s%s',
             unstamped::text,
             journals::text,
             E'\n',
             detail,
             E'\nstamp each of those lines at its own branch and run this migration again. A pre 629 year end close is not backfilled to the head office and there is no reverse and rerun tool. See docs/runbooks/gl-branch-stamp-0446.md.');
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'gl_journal_lines_branch_id_not_null'
           AND conrelid = 'gl_journal_lines'::regclass
    ) THEN
        ALTER TABLE gl_journal_lines
            ADD CONSTRAINT gl_journal_lines_branch_id_not_null
            CHECK (branch_id IS NOT NULL) NOT VALID;
    END IF;
END
$$;

ALTER TABLE gl_journal_lines
    VALIDATE CONSTRAINT gl_journal_lines_branch_id_not_null;
