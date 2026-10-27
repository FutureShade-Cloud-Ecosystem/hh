-- 0440: a journal can be reversed at most once (FW1-09a).
--
-- ReverseJournal locks the original journal row FOR UPDATE and checks that no row in gl_journals already carries
-- reverses_journal_id = orig.id. This unique partial index enforces the same invariant at the table level so no caller,
-- migration, or direct writer can insert a second reversal of the same journal.
--
-- Before creating the index, check whether any existing rows share a reverses_journal_id and raise an exception naming
-- the offending journals rather than silently modifying ledger history.

DO $$
DECLARE
    dup_ids text;
BEGIN
    SELECT string_agg(reverses_journal_id::text, ', ' ORDER BY reverses_journal_id::text)
      INTO dup_ids
      FROM (
          SELECT reverses_journal_id
            FROM gl_journals
           WHERE reverses_journal_id IS NOT NULL
           GROUP BY reverses_journal_id
          HAVING count(*) > 1
      ) dups;

    IF dup_ids IS NOT NULL THEN
        RAISE EXCEPTION '0440: gl_journals has multiple reversal rows for reverses_journal_id: %; ledger history is not modified automatically', dup_ids;
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS ux_gl_journals_reverses_once
    ON gl_journals (reverses_journal_id)
    WHERE reverses_journal_id IS NOT NULL;
