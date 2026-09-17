-- 0155_assist_limits.sql — DIB-431 (governance 2): the monthly AI budget, as config-as-data.
--
-- SHAPE COPIED FROM 0140, DELIBERATELY. The cockpit solved this exact problem a wave ago: a default
-- per ROLE, an override per PERSON, and "reset to role" implemented as DELETING the override rather
-- than as a merge that has to be unwound. Two tables, one resolver, one rule:
--
--     a user override is WHOLE OR ABSENT.
--
-- Whole-or-absent is the property that makes the surface honest. If overrides merged field by field,
-- an admin looking at a person's row could not tell which numbers were that person's and which were
-- inherited, and "reset to role" would have to guess which halves to drop. With this rule the answer
-- is always one of two sentences — "500,000 (their own)" or "500,000 (from SALES_MANAGER)" — and
-- reset is a DELETE that cannot leave a fragment behind. Resolution lives in one function
-- (internal/assist/limits.go, Resolve) so there is exactly one place that can get it wrong.
--
-- WHAT A BUDGET MEANS. tokens_in + tokens_out summed over the CURRENT CALENDAR MONTH, compared to
-- the budget before an exchange is allowed. Over budget is a plain, friendly refusal in the chat —
-- "monthly AI limit reached — ask your admin" — and NOT an error state, because nothing has gone
-- wrong: a person hit a number an administrator chose. The structured command layer (0153) keeps
-- working at zero cost throughout, which is the point of having built it first.
--
-- NO 'UNLIMITED' VALUE, ON PURPOSE. It would be one nullable column, and it would be set on the
-- first person who complained, and the pilot's whole question — what does this cost? — would stop
-- having an answer. Leadership gets a large number instead of an exemption. If unlimited is ever
-- genuinely wanted it should arrive as a decision with a name on it, not as a NULL that was always
-- there.
--
-- 0 IS LEGAL AND MEANS "NO AI FOR THIS ROLE". It is how the assistant is turned off for a group
-- without turning it off for everyone, and it is a budget like any other, so an admin reads it in
-- the same column as every other number rather than hunting for a separate switch.

CREATE TABLE IF NOT EXISTS assist_role_limits (
    role_code            citext PRIMARY KEY REFERENCES roles (code) ON DELETE CASCADE,
    monthly_token_budget integer NOT NULL CHECK (monthly_token_budget >= 0),
    updated_by           uuid REFERENCES users (id),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS assist_user_limits (
    -- ON DELETE CASCADE, unlike the ledger: an override is a preference, not a record of what
    -- happened. Removing the person removes their preference and leaves their history intact.
    user_id              uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    monthly_token_budget integer NOT NULL CHECK (monthly_token_budget >= 0),
    updated_by           uuid REFERENCES users (id),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- ── role defaults ──────────────────────────────────────────────────────────────────────────────
-- The numbers are a starting position for a pilot, not a finding. Roughly: a chat exchange on a
-- real provider costs on the order of 1–3k tokens, so 60,000 is ~20–60 questions a month, 200,000
-- is a few a day, and 1,000,000 is "stop thinking about it" for the people who will be asked to
-- evaluate this. They are in a table precisely so the first week's ledger can correct them without
-- a release.
--
-- A role with NO ROW here resolves to the code default (internal/assist/limits.go, DefaultBudget)
-- rather than to zero. A role added in a later migration must not silently switch the assistant off
-- for its holders — and must not silently grant them the largest budget in the table either, which
-- is why the code default is the small one.
INSERT INTO assist_role_limits (role_code, monthly_token_budget) VALUES
    -- leadership: the people who will judge whether this is worth paying for.
    ('SUPER_ADMIN',          1000000),
    ('GENERAL_MANAGER',       600000),
    ('FINANCIAL_CONTROLLER',  600000),
    ('HR',                    200000),
    -- managers: a working allowance.
    ('BRANCH_MANAGER',        300000),
    ('SALES_MANAGER',         300000),
    ('PROCUREMENT_MANAGER',   200000),
    ('YARD_MANAGER',          200000),
    ('LOGISTICS_MANAGER',     200000),
    ('PAYABLES_RECEIVABLES',  300000),
    -- frontline: enough to use it daily at the counter, small enough that the pilot learns
    -- something from the ledger.
    ('INSIDE_SALES',          150000),
    ('OUTSIDE_SALES',         150000),
    ('YARD_TEAM',              60000),
    ('DRIVERS',                60000)
ON CONFLICT (role_code) DO NOTHING;
