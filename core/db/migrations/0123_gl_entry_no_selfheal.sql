-- 0123: next_entry_no floors itself at the register it is numbering (DIB-418, follow-up).
--
-- WHAT 0119 GOT WRONG. It assumed the counter row is the ONLY thing that ever decides an entry_no. It is not.
-- gl_journals.entry_no is a plain column, and three shipped fixtures plus one migration write it directly:
--
--   migrations/0056_bistrack_tieout_demo.sql            the June/July tie-out demo journals
--   internal/bankrec/integration_test.go                (SELECT COALESCE(MAX(entry_no),0)+1 …) inline
--   internal/finstmt/integration_test.go                hand-picked entry numbers
--   internal/platform/db/invariants_integration_test.go a uniqueEntryNo() helper
--
-- Every one of those pushes MAX(entry_no) ABOVE the counter row, and the next real mint then hands back a
-- number that already exists — a unique violation on gl_journals_fiscal_year_entry_no_key, which is the exact
-- failure DIB-418 set out to remove. It showed up the moment the tagged suite ran in parallel: fiscal year
-- 2026 had last_value = 116 against MAX(entry_no) = 126, and every subsequent post in that year died.
--
-- A data load, a restore, or a support fix will do the same thing in production. So the counter cannot merely
-- be trusted; it has to be reconciled with the register it numbers.
--
-- THE FIX. Under the row lock it already takes, next_entry_no floors the counter at the year's actual
-- MAX(entry_no) before incrementing. Concurrency is unaffected — and this is the part worth being precise
-- about, because it looks like the read-modify-write the original bug WAS:
--
--   * The original defect read MAX(entry_no) with NO lock, so two sessions read the same value and both
--     inserted it.
--   * Here the row lock is taken FIRST. Only one session is inside the function for a given fiscal year at a
--     time, so the MAX it reads cannot move underneath it, and the value it writes back cannot be lost.
--
-- The read is an index-only scan on gl_journals_fiscal_year_entry_no_key (a UNIQUE btree on
-- (fiscal_year, entry_no)), so the cost is a single index descent per journal — nothing next to the header
-- and line inserts that follow it.
--
-- The other properties 0119 argued for are untouched: the bump still lives in the caller's transaction, so a
-- rolled-back post returns its number and the register stays gapless.

CREATE OR REPLACE FUNCTION next_entry_no(p_year INTEGER) RETURNS INTEGER AS $$
DECLARE
  v_n   BIGINT;
  v_max BIGINT;
BEGIN
  IF p_year IS NULL THEN
    RAISE EXCEPTION 'next_entry_no: fiscal_year is required';
  END IF;

  SELECT last_value INTO v_n
    FROM gl_entry_no_sequences
   WHERE fiscal_year = p_year
     FOR UPDATE;

  IF FOUND THEN
    -- Read the register UNDER the lock we already hold. Any number written by something other than this
    -- function (a fixture, a migration, a data load) is absorbed here rather than colliding on the next post.
    SELECT COALESCE(MAX(entry_no), 0) INTO v_max FROM gl_journals WHERE fiscal_year = p_year;
    IF v_max > v_n THEN
      v_n := v_max;
    END IF;
    UPDATE gl_entry_no_sequences
       SET last_value = v_n + 1, updated_at = now()
     WHERE fiscal_year = p_year
    RETURNING last_value INTO v_n;
    RETURN v_n::int;
  END IF;

  -- First mint of a year: no row to lock, so INSERT … ON CONFLICT DO UPDATE settles the race between two
  -- sessions that both fall through here (the loser takes the winner's row lock and bumps its value).
  INSERT INTO gl_entry_no_sequences (fiscal_year, last_value)
       VALUES (p_year,
               GREATEST(COALESCE((SELECT MAX(entry_no) FROM gl_journals WHERE fiscal_year = p_year), 0), 0) + 1)
  ON CONFLICT (fiscal_year)
  DO UPDATE SET last_value = GREATEST(
                  gl_entry_no_sequences.last_value,
                  COALESCE((SELECT MAX(entry_no) FROM gl_journals j WHERE j.fiscal_year = p_year), 0)
                ) + 1,
                updated_at = now()
    RETURNING last_value INTO v_n;

  RETURN v_n::int;
END;
$$ LANGUAGE plpgsql;

-- Re-reconcile every counter that has already drifted, so an environment migrated at 0119 and written to
-- since does not need one failed post to heal itself.
UPDATE gl_entry_no_sequences s
   SET last_value = j.mx, updated_at = now()
  FROM (SELECT fiscal_year, MAX(entry_no) AS mx FROM gl_journals GROUP BY fiscal_year) j
 WHERE j.fiscal_year = s.fiscal_year
   AND s.last_value < j.mx;

DO $$
DECLARE
  v_bad BIGINT;
BEGIN
  SELECT count(*) INTO v_bad
    FROM gl_entry_no_sequences s
    JOIN (SELECT fiscal_year, MAX(entry_no) AS mx FROM gl_journals GROUP BY fiscal_year) j
      ON j.fiscal_year = s.fiscal_year
   WHERE s.last_value < j.mx;
  IF v_bad > 0 THEN
    RAISE EXCEPTION '0123: % counter row(s) still behind their register', v_bad;
  END IF;
  RAISE NOTICE '0123: next_entry_no now self-heals against direct entry_no writers';
END;
$$;
