-- 0154_assist_token_ledger.sql — DIB-431 (governance 1): every AI exchange, written down once.
--
-- WHY A LEDGER AND NOT A COUNTER. A `tokens_used_this_month` integer on the user row would answer
-- the budget question in one read and would be the wrong shape for every other question the client
-- asked at the same meeting: which people are actually using this, what did the assistant cost on
-- the day of the tester session, did the number jump because of one person or twenty. A counter can
-- only be believed; a ledger can be checked. Spend is a SUM over rows (internal/assist/ledger.go),
-- which is slower and true.
--
-- APPEND-ONLY, ENFORCED IN THE DATABASE. The trigger below refuses UPDATE, DELETE and TRUNCATE on
-- this table. Not because the application intends any of the three — it has no code that does — but
-- because "the numbers we bill against cannot be quietly edited" is a property of the DATA, and a
-- property held only by the absence of a function is a property that lasts until someone writes
-- that function. This is the same reasoning the stock move ledger is built on.
--
--   What this genuinely stops: a well-meant "fix" in a later release, a hand-run UPDATE in psql
--                              during an incident, an ORM cascade nobody predicted.
--   What it does not stop:     a superuser who disables the trigger first. There is no defence
--                              against the owner of the database inside the database, and claiming
--                              otherwise would be worse than saying so here.
--
-- CORRECTIONS ARE ROWS, NOT EDITS. If an exchange is ever recorded wrongly, the fix is a
-- compensating row, exactly as it would be in a general ledger. That is why there is no
-- `corrected_at` column to tempt anyone.
--
-- THE STUB WRITES ROWS TOO, WITH ZEROS. The pilot's default provider is deterministic and costs
-- nothing, so tokens_in/tokens_out are 0 — and the row still lands. Counting EXCHANGES is the
-- measurement the pilot actually needs ("did anyone use this after the demo?"), and it must not
-- start reading zero the day a real provider is switched on. Hence the CHECK is >= 0, not > 0.

CREATE TABLE IF NOT EXISTS assist_token_ledger (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Scrubname0033 asked. ON DELETE RESTRICT: a person's spend history is not deleted by removing their
    -- login, and an admin who wants the row gone has to say so out loud.
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- The role AT THE TIME OF THE EXCHANGE, denormalised on purpose. Budgets are per role, so a
    -- promotion in September must not retroactively re-bucket August's spend.
    role_code  citext NOT NULL,
    provider   text NOT NULL,
    model      text NOT NULL,
    tokens_in  integer NOT NULL DEFAULT 0 CHECK (tokens_in >= 0),
    tokens_out integer NOT NULL DEFAULT 0 CHECK (tokens_out >= 0),
    -- 'chat' today. Named rather than assumed so a future non-chat consumer (a summariser, a draft
    -- writer) lands in the same ledger instead of a second one nobody sums.
    kind       text NOT NULL DEFAULT 'chat',
    at         timestamptz NOT NULL DEFAULT now()
);

-- The one query this table exists to serve: "this user's rows for this month". Month bucketing is
-- done in SQL against `at` rather than stored as a period column — a stored period is a second
-- truth that drifts the first time someone backdates a row.
CREATE INDEX IF NOT EXISTS assist_token_ledger_user_at_idx
    ON assist_token_ledger (user_id, at DESC);
-- The admin surface's query: everyone's spend for the current month, one pass.
CREATE INDEX IF NOT EXISTS assist_token_ledger_at_idx
    ON assist_token_ledger (at DESC);

CREATE OR REPLACE FUNCTION assist_token_ledger_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'assist_token_ledger is append-only: % is refused (record a compensating row instead)', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS assist_token_ledger_no_update ON assist_token_ledger;
CREATE TRIGGER assist_token_ledger_no_update
    BEFORE UPDATE OR DELETE ON assist_token_ledger
    FOR EACH ROW EXECUTE FUNCTION assist_token_ledger_append_only();

-- TRUNCATE bypasses row triggers entirely, which is precisely why it needs its own statement-level
-- one: without this line the table is append-only against every edit except the one that empties it.
DROP TRIGGER IF EXISTS assist_token_ledger_no_truncate ON assist_token_ledger;
CREATE TRIGGER assist_token_ledger_no_truncate
    BEFORE TRUNCATE ON assist_token_ledger
    FOR EACH STATEMENT EXECUTE FUNCTION assist_token_ledger_append_only();
