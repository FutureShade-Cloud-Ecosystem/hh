-- 0218: RETIRE the FY2026-06 BisTrack demonstration seed planted by 0056_bistrack_tieout_demo.sql, by
-- posting its four journals back out as reversing entries.
--
-- WHY. 0056 posted four ADJUSTMENT journals straight into the books — including 630,000 cents net into
-- 1100 Accounts Receivable — with no invoice, payment or receipt behind them. Its own header states the
-- condition it was built for:
--
--     "Before this migration, staging has ERP GL postings only in JULY (June ERP books empty)."
--
-- That condition no longer holds. demoseed now emits ar.invoice.finalized / payment.received for every
-- invoice and payment it plants, so the posting consumer writes real journals for them; June 2026 carries
-- nine real invoice journals of its own. The seed was scaffolding for empty books and the books are no
-- longer empty.
--
-- WHAT IT COSTS IF IT STAYS. Two things an accountant checks first disagree by exactly 630,000 cents:
--   * 1100 Accounts Receivable (Balance Sheet, v_balance_sheet) vs the AR subledger (AR Aging), and
--   * GET /api/v1/gl/tie-out, which reconciles those two and reports in_tie:false at that delta.
-- Neither disagreement is a defect in the ledger. Both are this seed, visible on the statements a client
-- reads, with nothing on the page that explains them.
--
-- WHAT IT DOES NOT COST. The seed's other purpose — making GET /api/v1/gl/tieout/bistrack tie at delta 0 —
-- is ALREADY unreachable once invoices post journals of their own: measured on a from-zero rebuild, three
-- of the fixture's seven accounts are out (1100 by 1,488,154 · 2200 by 171,204 · 4000 by 1,316,950), because
-- the fixture's expected balances were calibrated against books with no ERP invoice activity before July.
-- Recalibrating the fixture to whatever demoseed currently produces would reconcile our own books against
-- themselves; the fixture's own README names that tautology as the thing it was built to avoid. The
-- reconciliation MACHINERY stays proven by bistrack_tieout_integration_test.go, which posts its own isolated
-- scenario through the real posting path and is untouched by this migration.
--
-- REVERSING, NOT DELETING. Nothing is deleted, updated, truncated or reseeded — staging carries live tester
-- data and is never wiped. The original four journals stay exactly as posted; four new journals post their
-- legs the other way, which is how a posted entry is withdrawn from a ledger. The pair nets to zero on every
-- affected account, so every remaining balance on the books traces to a real invoice or payment.
--
-- Closing net-debit REMOVED by this migration (Σ = 0, balanced, mirrors 0056 exactly the other way):
--   1020 -497,500 · 1100 -630,000 · 2100 +250,000 · 2200 +130,000 · 4000 +1,000,000 · 6100 -250,000 ·
--   6350 -2,500
--
-- IDEMPOTENT / GUARDED (mirrors 0056): fixed journal UUIDs + ON CONFLICT (id) DO NOTHING, a stable
-- idempotency_key per journal (0032 ux_gl_journals_idem), entry_no computed as MAX(entry_no)+n within FY2026
-- so it never collides with organic postings (UNIQUE(fiscal_year, entry_no)). Re-runs no-op. If 0056 never
-- ran on this database there is nothing to reverse and this migration no-ops too.
--
-- posted_at is the period end rather than 0056's mid-month, so the reversal sorts AFTER the entry it
-- withdraws in any date-ordered drill-down, and still satisfies posted_at <= 2026-06-30 23:59:59 so the
-- inception-to-date closing balance nets to zero.
--
-- MONEY: integer cents (BIGINT). Each journal is internally balanced, so the DEFERRABLE constraint trigger
-- trg_gl_journal_balanced (0021) is satisfied at COMMIT.

DO $$
DECLARE
  v_period_id UUID;
  v_base      INT;
  v_posted    TIMESTAMPTZ := '2026-06-30 12:00:00+00';
  -- the four 0056 journals this migration withdraws
  o_j1 UUID := 'd0560000-0000-4000-9000-000000000001';  -- invoice finalized
  o_j2 UUID := 'd0560000-0000-4000-9000-000000000002';  -- payment received
  o_j3 UUID := 'd0560000-0000-4000-9000-000000000003';  -- till over/short
  o_j4 UUID := 'd0560000-0000-4000-9000-000000000004';  -- rent accrual
  -- their reversals
  r_j1 UUID := 'd0580000-0000-4000-9000-000000000001';
  r_j2 UUID := 'd0580000-0000-4000-9000-000000000002';
  r_j3 UUID := 'd0580000-0000-4000-9000-000000000003';
  r_j4 UUID := 'd0580000-0000-4000-9000-000000000004';
  a_1020 UUID; a_1100 UUID; a_2100 UUID; a_2200 UUID; a_4000 UUID; a_6100 UUID; a_6350 UUID;
BEGIN
  -- Already reversed → no-op.
  IF EXISTS (SELECT 1 FROM gl_journals WHERE id = r_j1) THEN
    RETURN;
  END IF;

  -- 0056 never ran here (or was skipped by one of its own guards) → nothing to reverse.
  IF NOT EXISTS (SELECT 1 FROM gl_journals WHERE id = o_j1) THEN
    RETURN;
  END IF;

  SELECT period_id INTO v_period_id FROM gl_journals WHERE id = o_j1;
  IF v_period_id IS NULL THEN
    RETURN;
  END IF;

  SELECT id INTO a_1020 FROM gl_accounts WHERE code = '1020';
  SELECT id INTO a_1100 FROM gl_accounts WHERE code = '1100';
  SELECT id INTO a_2100 FROM gl_accounts WHERE code = '2100';
  SELECT id INTO a_2200 FROM gl_accounts WHERE code = '2200';
  SELECT id INTO a_4000 FROM gl_accounts WHERE code = '4000';
  SELECT id INTO a_6100 FROM gl_accounts WHERE code = '6100';
  SELECT id INTO a_6350 FROM gl_accounts WHERE code = '6350';
  IF a_1020 IS NULL OR a_1100 IS NULL OR a_2100 IS NULL OR a_2200 IS NULL
     OR a_4000 IS NULL OR a_6100 IS NULL OR a_6350 IS NULL THEN
    -- A required account is missing (chart not fully migrated) — skip rather than post a partial reversal.
    RETURN;
  END IF;

  SELECT COALESCE(MAX(entry_no), 0) INTO v_base FROM gl_journals WHERE fiscal_year = 2026;

  -- Memos are read by a client on the GL drill-down, so they say what happened in plain words.
  INSERT INTO gl_journals (id, fiscal_year, entry_no, period_id, source, status, posted_at, branch_id, memo, idempotency_key)
  VALUES
    (r_j1, 2026, v_base + 1, v_period_id, 'ADJUSTMENT', 'POSTED', v_posted, NULL, 'Reversal of June setup entry — sales invoice (no customer invoice behind it)', 'm2c1-retire-inv-2026-06'),
    (r_j2, 2026, v_base + 2, v_period_id, 'ADJUSTMENT', 'POSTED', v_posted, NULL, 'Reversal of June setup entry — customer payment (no receipt behind it)',      'm2c1-retire-pay-2026-06'),
    (r_j3, 2026, v_base + 3, v_period_id, 'ADJUSTMENT', 'POSTED', v_posted, NULL, 'Reversal of June setup entry — till over/short',                             'm2c1-retire-till-2026-06'),
    (r_j4, 2026, v_base + 4, v_period_id, 'ADJUSTMENT', 'POSTED', v_posted, NULL, 'Reversal of June setup entry — rent accrual',                                'm2c1-retire-rent-2026-06')
  ON CONFLICT (id) DO NOTHING;

  -- Every leg is 0056's leg with debit and credit exchanged.
  INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, memo) VALUES
    -- 1. was: DR 1100 1,130,000 / CR 4000 1,000,000 / CR 2200 130,000
    (r_j1, 1, a_1100, NULL,    1130000, 'accounts receivable reversed'),
    (r_j1, 2, a_4000, 1000000, NULL,    'revenue reversed'),
    (r_j1, 3, a_2200, 130000,  NULL,    'HST reversed'),
    -- 2. was: DR 1020 500,000 / CR 1100 500,000
    (r_j2, 1, a_1020, NULL,    500000,  'undeposited receipt reversed'),
    (r_j2, 2, a_1100, 500000,  NULL,    'accounts receivable restored'),
    -- 3. was: DR 6350 2,500 / CR 1020 2,500
    (r_j3, 1, a_6350, NULL,    2500,    'cash short reversed'),
    (r_j3, 2, a_1020, 2500,    NULL,    'undeposited restored'),
    -- 4. was: DR 6100 250,000 / CR 2100 250,000
    (r_j4, 1, a_6100, NULL,    250000,  'rent reversed'),
    (r_j4, 2, a_2100, 250000,  NULL,    'accounts payable reversed');
END $$;
