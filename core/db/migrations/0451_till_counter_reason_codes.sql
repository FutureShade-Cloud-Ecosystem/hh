-- 0451: the counter reason codes.
--
-- A till paid in or paid out posted to 9999 Suspense against the drawer with free text reason words,
-- and the suspense balance filled with money no one had classified. A branch now keeps a list of the
-- codes the drawer names, and each code points at the GL account the movement posts to, so a movement
-- that names a code never lands in suspense (W2-6b).
--
-- The list is per branch: the same code string can sit on two branches pointing at different accounts
-- (postage at one yard is not the other yard's postage), so the key is (branch_id, code).
--
-- The movement carries the list row it names, not a copy of the label or the account: the row is the
-- one fact, and a manager may later edit the label or point the code at another account. The posting
-- reads the account from the row at post time through the gl module's seam.
--
-- Spec: W2-6b (fin2 wave 2) · Traces: FROM-PRINCIPAL 132 (a movement with no code still posts to 9999
-- Suspense, so a movement recorded before this migration keeps its suspense posting).

CREATE TABLE till_counter_reason_codes (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id       UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
  code            TEXT NOT NULL,
  label           TEXT NOT NULL,
  movement_kind   TEXT NOT NULL CHECK (movement_kind IN ('PAID_IN', 'PAID_OUT', 'BOTH')),
  gl_account_code TEXT NOT NULL,
  active          BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (branch_id, code)
);

CREATE INDEX idx_till_counter_reason_codes_branch ON till_counter_reason_codes (branch_id);

-- The drawer names a code and the movement points at the list row that code names. A movement with no
-- code keeps NULL, which is every movement recorded before this migration and every movement on a
-- branch whose list carries no active code for that kind; those still post to 9999 Suspense.
ALTER TABLE till_movements
  ADD COLUMN reason_code_id UUID REFERENCES till_counter_reason_codes(id) ON DELETE RESTRICT;

-- The starter list every branch begins with: the four spends and the one correction a till draws on
-- most, each pointing at an account that is already in the chart (5100 Freight-In and 6000 Payroll and
-- 6200 Utilities are expenses, 2300 Customer Deposits is the liability a customer overpayment sits in).
--
-- The starter rows ship INACTIVE. A branch that has not turned a code on keeps today's behaviour (a
-- paid in or paid out with no code still posts to 9999 Suspense), and the branch manager or the
-- Financial Controller turns a code on through the till module's own list API once the drawer and the
-- books are ready to name it. Shipping them live would change every branch's till on the day the
-- migration lands, and the drawer's screen cannot name a code yet (RULES-S4 addendum 9).
INSERT INTO till_counter_reason_codes (branch_id, code, label, movement_kind, gl_account_code, active)
SELECT l.id, v.code, v.label, v.kind, v.account, FALSE
  FROM locations l
 CROSS JOIN (VALUES
   ('POSTAGE',         'Postage and courier',                    'PAID_OUT', '5100'),
   ('CLEANING',        'Cleaning and sundry supplies',           'PAID_OUT', '6200'),
   ('STAFF_MEALS',     'Staff meals',                            'PAID_OUT', '6000'),
   ('CASH_REFUND',     'Customer cash refund correction',        'BOTH',    '2300')
 ) AS v(code, label, kind, account)
 WHERE l.loc_type = 'BRANCH'
ON CONFLICT (branch_id, code) DO NOTHING;
