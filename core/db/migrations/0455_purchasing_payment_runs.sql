-- 0455: payment runs, the propose, review, pay document over open vendor bills.
--
-- A payment run is the AP clerk's paying morning: pick the posted bills due by a house day (one
-- branch, optionally one vendor), take some off or lower an amount while the run is still a
-- proposal, then pay. The run is a DOCUMENT with its own id and status, so the answer to "what did
-- we pay on the run" is a row set and not a memory: the bills are paid through PayBill, one
-- payment each, under the key run:<run id>:<bill id>, so the same bill can never be paid twice by
-- the same run however many times the pay press lands (a re-press replays the first payment and
-- writes nothing, the instalment table carrying no key column, the key riding the journal).
--
-- THE RUN NEVER MOVES MONEY ITSELF. Every cent moves through PayBill's own door and journal, so
-- the aging, the subledger and GL 2100 keep their single source of truth and a run that dies
-- mid-flight leaves the bills exactly as a clerk paying them by hand would.
--
-- BILL STATE MOVES UNDER THE RUN. A bill paid elsewhere between the review and the pay is paid for
-- at most its open amount, or skipped with a reason, so the line records what the run ASKED to pay
-- (amount_cents, the reviewed selection) beside what it ACTUALLY paid (paid_cents), and a skip
-- says why in words.

CREATE TABLE purchasing_payment_runs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id   UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  status      TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','PAID')),
  -- The proposal's filter: posted bills due (or dateless, falling back to their bill date) on or
  -- before this house day, in this branch, and for this vendor when named.
  due_on      DATE NOT NULL,
  vendor_id   UUID REFERENCES vendors(id) ON DELETE RESTRICT,
  -- The house day the run pays on: every payment it writes is dated this day in the dealer's zone.
  pay_date    DATE NOT NULL,
  paid_at     TIMESTAMPTZ,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_purchasing_payment_runs_branch ON purchasing_payment_runs(branch_id, status);

CREATE TABLE purchasing_payment_run_lines (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id        UUID NOT NULL REFERENCES purchasing_payment_runs(id) ON DELETE CASCADE,
  seq           INTEGER NOT NULL CHECK (seq > 0),
  bill_id       UUID NOT NULL REFERENCES purchasing_bills(id) ON DELETE RESTRICT,
  -- The reviewed selection: the bill's open amount as proposed, as the review lowered it.
  amount_cents  BIGINT NOT NULL CHECK (amount_cents > 0),
  -- SELECTED is waiting for the pay; PAID carries the cents that actually moved; SKIPPED carries
  -- the reason (paid elsewhere, voided) in words.
  status        TEXT NOT NULL DEFAULT 'SELECTED' CHECK (status IN ('SELECTED','PAID','SKIPPED')),
  paid_cents    BIGINT NOT NULL DEFAULT 0 CHECK (paid_cents >= 0),
  skip_reason   TEXT NOT NULL DEFAULT '',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (run_id, bill_id),
  UNIQUE (run_id, seq)
);

CREATE INDEX idx_purchasing_payment_run_lines_bill ON purchasing_payment_run_lines(bill_id);
