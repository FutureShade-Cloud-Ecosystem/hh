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
-- The movement carries the list row it names, and the account that row pointed at when the movement
-- was recorded: the row is the one fact, and a manager may later edit the label or point the code at
-- another account. The account the movement carries is the account it posts to, so a repoint made
-- while the movement waited never moves the money (W2-6b fix round 3). The posting reads that
-- account from the movement, and only asks the gl module's seam for the account of a movement
-- recorded before this column existed.
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

-- The movement carries the account the code pointed at WHEN THE MOVEMENT WAS RECORDED (W2-6b fix round
-- 3). The account resolves once, at record time, and travels with the movement: a manager may repoint
-- the code later, and a repoint must not move a movement that has not posted yet.
--
-- The column is nullable. Every movement recorded before this migration keeps NULL, a movement that
-- names no code keeps NULL, and both still post the way they always did.
ALTER TABLE till_movements
  ADD COLUMN gl_account_code TEXT;

-- The starter list every branch begins with: the three spends a till drawer draws on most, each
-- pointing at an expense account of its own.
--
-- The three accounts are planted here, because the spends have no home in the chart yet. Postage is
-- not cost of goods (5100 Freight-In) and staff meals are not payroll (6000 Payroll): both are on the
-- wrong P&L line, and a list that is off today is turned on some day, so the day a branch turns a
-- code on is the day that spend starts landing on that line. 6400, 6500 and 6600 are free in the
-- chart: the 6xxx block the chart holds runs 6000 Payroll, 6100 Rent, 6200 Utilities, 6300 Bank Fees
-- and 6350 Cash Over/Short.
--
-- The guard on a number a dealer already uses: the insert never overwrites. ON CONFLICT (code) DO
-- NOTHING leaves a chart that already holds 6400, 6500 or 6600 exactly as the dealer left it, under
-- whatever meaning the dealer gave that number, and the second guard below keeps the starter list off
-- it too. A starter code is planted only where the chart holds the very account this migration
-- defines, so a branch whose chart already carries one of these numbers under another meaning is not
-- pointed at it; that branch's manager adds a code against an account of their own instead.
INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES
  ('6400', 'Office Supplies and Postage', 'EXPENSE', 'DEBIT'),
  ('6500', 'Cleaning and Janitorial',    'EXPENSE', 'DEBIT'),
  ('6600', 'Staff Meals and Amenities',  'EXPENSE', 'DEBIT')
ON CONFLICT (code) DO NOTHING;

-- The starter rows ship INACTIVE. A branch that has not turned a code on keeps today's behaviour (a
-- paid in or paid out with no code still posts to 9999 Suspense), and the branch manager or the
-- Financial Controller turns a code on through the till module's own list API once the drawer and the
-- books are ready to name it. Shipping them live would change every branch's till on the day the
-- migration lands, and the drawer's screen cannot name a code yet (RULES-S4 addendum 9).
--
-- A cash refund to a customer is not on this list. It goes through the returns flow, with its own
-- document and its own tax reversal, and it is never a drawer paid out, so it is never a code the
-- drawer names. The list carries spends and spends only, and every code on it posts to an expense.
INSERT INTO till_counter_reason_codes (branch_id, code, label, movement_kind, gl_account_code, active)
SELECT l.id, v.code, v.label, v.kind, a.code, FALSE
  FROM locations l
 CROSS JOIN (VALUES
   ('POSTAGE',     'Postage and office supplies', 'PAID_OUT', '6400', 'Office Supplies and Postage'),
   ('CLEANING',    'Cleaning and janitorial',     'PAID_OUT', '6500', 'Cleaning and Janitorial'),
   ('STAFF_MEALS', 'Staff meals and amenities',   'PAID_OUT', '6600', 'Staff Meals and Amenities')
 ) AS v(code, label, kind, account, account_name)
  JOIN gl_accounts a
    ON a.code = v.account
   AND a.name = v.account_name
   AND a.account_type = 'EXPENSE'
   AND a.is_active
 WHERE l.loc_type = 'BRANCH'
ON CONFLICT (branch_id, code) DO NOTHING;
