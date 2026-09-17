-- 0119: gl_journals.entry_no is minted from a sequence row, not from MAX()+1 (DIB-418).
--
-- THE BUG. gl.Repository.InsertJournal allocated the next journal number with
--
--     SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year = $1
--
-- and then INSERTed that value under UNIQUE (fiscal_year, entry_no). That is a read-modify-write with no
-- lock between the read and the write. Under READ COMMITTED two concurrent posting transactions both read
-- the same MAX — an uncommitted neighbour's header is invisible to them — so both compute the same next
-- number and both try to insert it. One commits; the other dies on the unique constraint.
--
-- What that costs is not an abstraction. Every automated posting path funnels through InsertJournal:
-- the events_outbox consumer (invoice.finalized, payment.received, order.fulfilled, po.received, …), the
-- landed-cost capitalization, bankrec's clearing journals, month-end close. The dispatcher runs those
-- handlers concurrently, so the collision window is the program's ordinary operating mode, and the loser
-- surfaces as `gl: insert journal header: ... duplicate key ...` — an opaque 500 on a money path. Worse,
-- the loser of an idempotent post is retried by the outbox, so the failure is invisible until an operator
-- reads dead-lettered events; and the failure is LOAD-DEPENDENT, which is the class of bug that shows up
-- first on the busiest day of the month.
--
-- THE FIX (the 0087/0090 pattern, ruled). One counter row per fiscal year, and ONE mint:
-- next_entry_no(fiscal_year). The row is locked before it is read, so a second minter WAITS instead of
-- reading a stale value. gl swaps its MAX()+1 query for `SELECT next_entry_no($1)` (tagged
-- // SWARM-W4-C DIB-418) and no other call site exists — the drift cannot recur without going out of its
-- way to.
--
-- WHY A TABLE ROW, NOT A POSTGRES SEQUENCE. Two reasons, both about the numbers an accountant will read:
--   1. A sequence is NON-TRANSACTIONAL. nextval() survives a rollback, so a posting transaction that fails
--      its balance check burns a number and leaves a permanent hole in the journal register. A row bumped
--      inside the caller's transaction rolls back with it — no hole. Journal numbering is a statutory
--      register (ASPE / CRA audit trail); "the numbers are contiguous" is a property auditors ask about.
--   2. A sequence per fiscal year needs runtime DDL (CREATE SEQUENCE) when the year rolls over, which needs
--      elevated rights and cannot be rolled back alongside the row that motivated it. A row needs none.
--
-- WHAT IS TRADED. The row lock serializes journal-header inserts WITHIN one fiscal year for the remainder
-- of the minting transaction. That window is deliberately small: InsertJournal resolves the fiscal period
-- and every line's account BEFORE it mints, so the lock covers only the header insert, the line inserts and
-- the commit. Two different fiscal years never contend (separate rows). This is the same trade 0087 made
-- for order numbers and 0090 for dispatch numbers, and it is the trade that buys gaplessness.

CREATE TABLE gl_entry_no_sequences (
  fiscal_year INTEGER PRIMARY KEY,
  last_value  BIGINT NOT NULL DEFAULT 0 CHECK (last_value >= 0),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed one row per fiscal year that already holds journals, AT the current maximum, so the first mint after
-- this migration returns MAX+1 — exactly what the old expression would have returned. No existing number is
-- re-issued and no number is skipped at the changeover.
INSERT INTO gl_entry_no_sequences (fiscal_year, last_value)
SELECT fiscal_year, MAX(entry_no) FROM gl_journals GROUP BY fiscal_year
ON CONFLICT (fiscal_year) DO NOTHING;

-- next_entry_no mints the next journal number for a fiscal year.
--
-- Fast path: the year's row exists → SELECT … FOR UPDATE takes the row lock, so a concurrent minter blocks
-- HERE (before reading) rather than reading the same value and colliding at INSERT time. The subsequent
-- UPDATE cannot lose to anyone: the lock is already ours.
--
-- First mint of a year: no row yet, so there is nothing to lock and two sessions could both fall through.
-- The INSERT … ON CONFLICT DO UPDATE closes that: the loser's INSERT conflicts, takes the winner's row lock,
-- and bumps the winner's value. The seed expression starts the counter ABOVE any legacy MAX(entry_no) for
-- the year, so a year whose rows predate this table (or were written by a direct SQL fixture) can never have
-- a number re-issued.
--
-- A NULL year RAISES rather than minting an unattributable number: a journal that cannot be year-stamped
-- must not be written at all.
CREATE FUNCTION next_entry_no(p_year INTEGER) RETURNS INTEGER AS $$
DECLARE
  v_n BIGINT;
BEGIN
  IF p_year IS NULL THEN
    RAISE EXCEPTION 'next_entry_no: fiscal_year is required';
  END IF;

  SELECT last_value INTO v_n
    FROM gl_entry_no_sequences
   WHERE fiscal_year = p_year
     FOR UPDATE;

  IF FOUND THEN
    UPDATE gl_entry_no_sequences
       SET last_value = v_n + 1, updated_at = now()
     WHERE fiscal_year = p_year
    RETURNING last_value INTO v_n;
    RETURN v_n::int;
  END IF;

  INSERT INTO gl_entry_no_sequences (fiscal_year, last_value)
       VALUES (p_year,
               GREATEST(COALESCE((SELECT MAX(entry_no) FROM gl_journals WHERE fiscal_year = p_year), 0), 0) + 1)
  ON CONFLICT (fiscal_year)
  DO UPDATE SET last_value = gl_entry_no_sequences.last_value + 1, updated_at = now()
    RETURNING last_value INTO v_n;

  RETURN v_n::int;
END;
$$ LANGUAGE plpgsql;

-- Prove the seed: no fiscal year may be left with a counter BELOW a number already on a journal, which is
-- the only way this migration could hand out a duplicate.
DO $$
DECLARE
  v_bad BIGINT;
BEGIN
  SELECT count(*) INTO v_bad
    FROM (SELECT fiscal_year, MAX(entry_no) AS mx FROM gl_journals GROUP BY fiscal_year) j
    LEFT JOIN gl_entry_no_sequences s ON s.fiscal_year = j.fiscal_year
   WHERE s.last_value IS NULL OR s.last_value < j.mx;
  IF v_bad > 0 THEN
    RAISE EXCEPTION '0119: % fiscal year(s) seeded below their existing MAX(entry_no)', v_bad;
  END IF;
  RAISE NOTICE '0119: gl entry_no counters seeded for % fiscal year(s)',
    (SELECT count(*) FROM gl_entry_no_sequences);
END;
$$;
