-- 0473: the monthly finance charge run (wave 3, plan row FC-1).
-- Owner: internal/billing. Additive, non-breaking, self-contained.
--
-- A dealer charges for late payment: the balance more than 30 days past due at a house month's last
-- day carries a finance charge of one and a half percent, with a minimum, once per customer for the
-- month. The charge is a real invoice raised through the invoice writer (DR 1100 / CR finance
-- charge income), so its money is AR money, it ages, it can be paid, and the GL derives its journal
-- from the document exactly as every invoice's journal is derived.
--
-- This migration carries the run's three facts:
--
--   1. THE RUN TABLE, and the unique key that makes a customer and a month one charge. Two runs
--      pressed at the same moment, a double pressed button, a retried worker: the second claim on
--      the same (branch, customer, month) finds the first's row and charges nothing. The key is a
--      database fact, not a Go side check two processes can both answer no to.
--
--   2. THE WAIVER, the Controller's per customer flag. A waived customer is skipped by the run. The
--      row IS the record: who set it and when, and, once the Controller clears it, who cleared it
--      and when. The row is never deleted, so the flag's whole history stays readable.
--
--   3. THE CHART'S ACCOUNT and the posting rule the charge's journal resolves through. The chart
--      carried no finance charge income account (the 4200 fee block stops at NSF and restocking
--      fees), so this migration adds 4220 Finance Charge Income beside them and the default
--      invoice.finalized/finance_charge rule pointing at it; a branch override rides the same
--      partial index pair every other rule does.
--
-- The source kind FINANCE_CHARGE joins the invoices producer markers, so a charge document is
-- recognizable at the row: the run's own candidates read excludes it (a charge is never itself
-- charged), and the register, the statements and the aging read it as the invoice it is.

-- 1. The run table: one row per charge the run raised.
CREATE TABLE IF NOT EXISTS finance_charge_runs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- A run is one branch's books; the customer is the account charged.
    branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
    customer_id    UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,

    -- The house month the run charges, YYYY-MM, the word the request names and the document's words
    -- carry.
    month          TEXT NOT NULL,

    -- The month's last house day, the day the past due balance was measured as of.
    as_of_date     DATE NOT NULL,

    -- The balance more than 30 days past due the charge was computed on, and the charge itself,
    -- both integer cents.
    past_due_cents BIGINT NOT NULL CHECK (past_due_cents > 0),
    charge_cents   BIGINT NOT NULL CHECK (charge_cents > 0),

    -- The charge document the run raised through the invoice writer. Nullable for the moment
    -- between the claim and the document: the row is claimed first (the claim decides the race),
    -- the document follows in the same transaction.
    invoice_id     UUID REFERENCES invoices(id) ON DELETE RESTRICT,

    -- Who pressed the run, when.
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT finance_charge_runs_month_shape CHECK (month ~ '^[0-9]{4}-[0-9]{2}$'),

    -- THE KEY: one charge per customer per house month per branch. The run takes an advisory lock
    -- first so two concurrent runs serialise cleanly, and this constraint is the belt under it:
    -- whatever path claims a second time is refused by the database.
    CONSTRAINT finance_charge_runs_once UNIQUE (branch_id, customer_id, month)
);

CREATE INDEX IF NOT EXISTS idx_finance_charge_runs_branch_month ON finance_charge_runs(branch_id, month);

-- 2. The Controller's waiver: one row per customer ever waived, the record of both acts on it.
CREATE TABLE IF NOT EXISTS finance_charge_waivers (
    -- The customer the flag is per. The row is the flag's whole history, so it is keyed by the
    -- customer and never replaced, only stamped.
    customer_id UUID PRIMARY KEY REFERENCES customers(id) ON DELETE RESTRICT,

    -- The Controller's own words for why, required (an undocumented waiver is a favour, and a
    -- favour with no reason on it is what an auditor asks about).
    reason      TEXT NOT NULL,

    -- The setting: who and when.
    waived_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    waived_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The clearing: who and when. NULL is an active waiver; a stamp releases the customer back to
    -- the run. Setting the flag again after a clear refreshes the record and clears the clearing.
    cleared_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    cleared_at  TIMESTAMPTZ,

    CONSTRAINT finance_charge_waivers_reason_nonempty CHECK (btrim(reason) <> '')
);

-- 3a. The source kind the writer stamps on the charge document.
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS ck_invoices_source_kind;
ALTER TABLE invoices
    ADD CONSTRAINT ck_invoices_source_kind
        CHECK (source_kind IN ('','POS','ORDER','MULTISHIP','WARRANTY_CREDIT','IMPORT','SUPPLIER_DEPOSIT','FINANCE_CHARGE'));

-- 3b. The income account, beside the other fee incomes (4200 NSF Fee Income, 4210 Restocking Fee
--     Income), and the posting rule the charge document's journal resolves its income leg through.
INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES ('4220', 'Finance Charge Income', 'REVENUE', 'CREDIT')
ON CONFLICT (code) DO NOTHING;

INSERT INTO posting_rules (event_type, line_role, account_code)
VALUES ('invoice.finalized', 'finance_charge', '4220')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;

-- Prove the rule resolves to an ACTIVE account here, at migrate time, rather than letting the first
-- charge document's journal discover it.
DO $$
DECLARE
    v_bad TEXT;
BEGIN
    SELECT string_agg(event_type || '/' || line_role || ' -> ' || account_code, ', ')
      INTO v_bad
      FROM posting_rules p
      LEFT JOIN gl_accounts a ON a.code = p.account_code
     WHERE p.event_type = 'invoice.finalized' AND p.line_role = 'finance_charge'
       AND (a.id IS NULL OR NOT a.is_active);
    IF v_bad IS NOT NULL THEN
        RAISE EXCEPTION '0473: the finance charge posting rule does not resolve to an active account: %', v_bad;
    END IF;
END $$;
