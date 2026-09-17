-- 0144: DIB-96 / A-22 — commission tracking (rules, computed periods, drill-through lines).
-- Owner module: internal/reporting. Wave 6, Swarm B.
--
-- WHAT THE OFFICE DOES TODAY. Commission is a spreadsheet one person maintains, rebuilt by hand each
-- month from a sales report, and reconciled by argument. The two questions it cannot answer are the two
-- that get asked: "which sales are in this number?" and "what rate did you use?".
--
-- ── THREE TABLES, THREE DIFFERENT JOBS ───────────────────────────────────────────────────────────────
--   commission_rules       the POLICY. Rep + basis + rate, optionally narrowed to a category or branch.
--   commission_runs        the COMPUTED PERIOD. One row per rep per period: an artefact, not a query.
--   commission_run_lines   the DRILL-THROUGH. Every invoice line that made up the number, with the rate
--                          and the rule that chose it, frozen as at computation time.
--
-- ── NOTHING HERE POSTS TO THE GENERAL LEDGER THIS WAVE. SAID ONCE, PLAINLY ───────────────────────────
-- A commission run is an ACCRUAL VIEW: it says "on today's numbers, this is what is owed". It writes no
-- gl_journals row, it is not a liability in the trial balance, and the accrual screen says so on its
-- face. gl_journal_id below exists and is NEVER written this wave — it is the column a future posting
-- path fills, and having it now means that path is an INSERT-and-UPDATE rather than a migration under
-- time pressure at a month end. A number that quietly became a GL liability without an accountant
-- ratifying the account, the timing and the reversal policy would be a worse outcome than a spreadsheet.
--
-- ── WHY THE RATE IS COPIED ONTO EVERY LINE ───────────────────────────────────────────────────────────
-- rate_bps and rule_id are stored on commission_run_lines even though the rule row still exists. Rules
-- change — a rate is renegotiated, a category carve-out is added — and a run computed in March must
-- keep saying what it said in March. The same reasoning ar_statements uses for its aging figures and
-- payment_nsf uses for its fee. It costs a smallint per line and it buys "why is this different from
-- last month" being answerable.
--
-- ── RULE SELECTION IS DECIDED BY A DETERMINISTIC ORDER, NOT BY LUCK ──────────────────────────────────
-- A rep may have several rules in force. The most SPECIFIC one wins, and specificity is defined here so
-- it is reviewable in one place rather than living only in a Go ORDER BY:
--
--     (category set, branch set) > (category set) > (branch set) > (neither)
--     ties broken by the later effective_from, then by the later created_at
--
-- The partial unique index below stops the only tie that has no honest resolution: two rules for the
-- same rep, same specificity, same effective_from.
--
-- Conventions: UUID PKs · money BIGINT *_cents · rates in BASIS POINTS (integers, never floats) ·
-- TEXT + CHECK · TIMESTAMPTZ · explicit ON DELETE.
-- Rollback: additive (three tables). Nothing outside internal/reporting reads them.

-- ── 1. commission_rules — the policy ─────────────────────────────────────────────────────────────────
CREATE TABLE commission_rules (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  rep_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- REVENUE → the rate applies to the invoiced line total.
  -- MARGIN  → the rate applies to (line total − weighted-average cost × quantity).
  -- These are NOT interchangeable and the difference is most of the money: on a 25%-margin line, a 5%
  -- revenue rule pays four times a 5% margin rule. The named test
  -- TestCommission_MarginBasisIsNotRevenueBasis exists to die if the two are ever swapped in the Go.
  basis       TEXT NOT NULL CHECK (basis IN ('REVENUE','MARGIN')),
  -- Basis points. Integer arithmetic end to end: cents × bps / 10000, half-up, per line. A float rate
  -- would make two runs of the same period disagree in the last cent.
  rate_bps    INTEGER NOT NULL CHECK (rate_bps >= 0 AND rate_bps <= 10000),
  -- NULL = every category. Set = this rule applies only to lines whose product sits in this category.
  category_id UUID REFERENCES product_categories(id) ON DELETE CASCADE,
  -- NULL = every branch.
  branch_id   UUID REFERENCES locations(id) ON DELETE CASCADE,
  effective_from DATE NOT NULL,
  -- NULL = open-ended. A closed rule stays for history; it is never deleted on renegotiation.
  effective_to   DATE,
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  note        TEXT NOT NULL DEFAULT '',
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (effective_to IS NULL OR effective_to >= effective_from)
);
CREATE INDEX idx_commission_rules_rep ON commission_rules(rep_user_id, effective_from DESC);
-- The unresolvable tie, refused at the substrate: same rep, same scope, same start date.
CREATE UNIQUE INDEX uq_commission_rules_scope ON commission_rules(
  rep_user_id,
  COALESCE(category_id, '00000000-0000-0000-0000-000000000000'::uuid),
  COALESCE(branch_id,   '00000000-0000-0000-0000-000000000000'::uuid),
  effective_from
) WHERE is_active;

-- ── 2. commission_runs — the computed period, one row per rep ────────────────────────────────────────
CREATE TABLE commission_runs (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  rep_user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  period_start  DATE NOT NULL,
  period_end    DATE NOT NULL,
  branch_id     UUID REFERENCES locations(id) ON DELETE SET NULL,
  -- The rollups, frozen. revenue/cost/margin are the attributed sales; commission_cents is the payable.
  sale_count    INTEGER NOT NULL DEFAULT 0,
  revenue_cents BIGINT  NOT NULL DEFAULT 0,
  cost_cents    BIGINT  NOT NULL DEFAULT 0,
  margin_cents  BIGINT  NOT NULL DEFAULT 0,
  commission_cents BIGINT NOT NULL DEFAULT 0,
  -- ACCRUED is the only state this wave admits, and it means exactly "computed, not posted". The CHECK
  -- is deliberately a one-value list: widening it later is the migration that ALSO writes the posting
  -- path, so the two cannot drift apart.
  status        TEXT NOT NULL DEFAULT 'ACCRUED' CHECK (status IN ('ACCRUED')),
  -- Reserved. NEVER written this wave; see the header. Present so the posting path is an UPDATE.
  gl_journal_id UUID,
  computed_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  computed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (period_end >= period_start)
);
CREATE INDEX idx_commission_runs_rep_period ON commission_runs(rep_user_id, period_start DESC);
CREATE INDEX idx_commission_runs_period ON commission_runs(period_start, period_end);

-- ── 3. commission_run_lines — the drill-through ──────────────────────────────────────────────────────
CREATE TABLE commission_run_lines (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id        UUID NOT NULL REFERENCES commission_runs(id) ON DELETE CASCADE,
  -- Deliberately NOT a foreign key to invoice_lines. A run is history; a line that is later corrected
  -- must not silently vanish out of a computed period, and a CASCADE from an invoice edit would do
  -- exactly that. The id is carried so the UI can link through, and the DISPLAY fields below are copied
  -- so the drill-through still reads correctly if the source line is gone.
  invoice_id    UUID,
  invoice_line_id UUID,
  invoice_no    TEXT NOT NULL DEFAULT '',
  invoice_date  DATE,
  customer_name TEXT NOT NULL DEFAULT '',
  product_sku   TEXT NOT NULL DEFAULT '',
  category_code TEXT NOT NULL DEFAULT '',
  qty_base      NUMERIC(18,4) NOT NULL DEFAULT 0,
  revenue_cents BIGINT NOT NULL DEFAULT 0,
  cost_cents    BIGINT NOT NULL DEFAULT 0,
  margin_cents  BIGINT NOT NULL DEFAULT 0,
  -- The amount the rate was applied to: revenue_cents or margin_cents, per the rule's basis.
  basis         TEXT NOT NULL CHECK (basis IN ('REVENUE','MARGIN')),
  basis_cents   BIGINT NOT NULL DEFAULT 0,
  rate_bps      INTEGER NOT NULL DEFAULT 0,
  commission_cents BIGINT NOT NULL DEFAULT 0,
  -- Which rule chose the rate. ON DELETE SET NULL, not CASCADE: deleting a rule must not delete the
  -- evidence of what it once paid.
  rule_id       UUID REFERENCES commission_rules(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_commission_run_lines_run ON commission_run_lines(run_id);
