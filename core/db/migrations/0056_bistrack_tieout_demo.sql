-- 0056: M2-C1 BisTrack tie-out DEMO SEED — post the deterministic FY2026-06 counterpart GL journals the
-- static BisTrack snapshot (internal/gl/testdata/bistrack_gl_snapshot_2026-06.json) expects, so that
--   GET /api/v1/gl/tieout/bistrack?year=2026&month=6
-- ties to delta=0 (ties_within_tolerance:true) on STAGING.
--
-- WHY A MIGRATION (not cmd/seed): the staging deploy runs ONLY migrations (bin/migrate), never cmd/seed —
-- dibbits_seed.sql does not exist on staging (same reason 0043/0044/0046/0049 bootstrap demo data here).
-- Before this migration, staging has ERP GL postings only in JULY (June ERP books empty), so the tie-out
-- returned all 7 accounts BISTRACK_ONLY / erp_cents:0 (delta≠0, ties_within_tolerance:false). This posts the
-- June counterparts through direct gl_journals/gl_journal_lines inserts that satisfy the balance trigger
-- (trg_gl_journal_balanced: each POSTED journal must have Σdebit == Σcredit at COMMIT).
--
-- SOURCE OF TRUTH: the fixture is a per-account CLOSING TRIAL BALANCE for 2026-06. The tie-out reconciles the
-- ERP inception-to-date closing balance (TrialBalanceAsOf, posted_at <= 2026-06-30) against it. We post EXACTLY
-- the ERP counterpart legs that net each erp_account_code to the fixture amount (mirrors the hand-authored legs
-- in bistrack_tieout_integration_test.go::postBisTrackScenario). Closing net-debit by account after this seed:
--   1020 +497,500 · 1100 +630,000 · 2100 -250,000 · 2200 -130,000 · 4000 -1,000,000 · 6100 +250,000 ·
--   6350 +2,500   (Σ = 0, balanced) — matching the fixture rows exactly.
--
-- The four business events (each an internally-balanced journal):
--   1. Invoice finalized:   DR 1100 AR 1,130,000 / CR 4000 Revenue 1,000,000 / CR 2200 HST 130,000
--   2. Payment received:    DR 1020 Undeposited 500,000 / CR 1100 AR 500,000
--   3. Till session short:  DR 6350 Cash Over/Short 2,500 / CR 1020 Undeposited 2,500
--   4. Rent accrual:        DR 6100 Rent 250,000 / CR 2100 AP 250,000
--
-- IDEMPOTENT / GUARDED (mirrors 0046/0049): fixed journal UUIDs + ON CONFLICT (id) DO NOTHING and a stable
-- idempotency_key per journal (0032 ux_gl_journals_idem). entry_no is computed as MAX(entry_no)+n within the
-- target fiscal year so it never collides with organic FY2026 postings (UNIQUE(fiscal_year, entry_no)). Re-runs
-- no-op. All prerequisites (gl_accounts 0007, fiscal_periods 2026-06 0007) are created by earlier MIGRATIONS,
-- so this is fresh-migrate-safe and does NOT depend on cmd/seed. branch_id is NULL (org-consolidated), which is
-- exactly the grain the staging endpoint reconciles (branch_id="" → consolidated).
--
-- MONEY: integer cents (BIGINT). posted_at is mid-June so posted_at <= period end (2026-06-30 23:59:59).

DO $$
DECLARE
  v_period_id UUID;
  v_base      INT;
  v_posted    TIMESTAMPTZ := '2026-06-15 12:00:00+00';
  v_j1        UUID := 'd0560000-0000-4000-9000-000000000001';  -- invoice finalized
  v_j2        UUID := 'd0560000-0000-4000-9000-000000000002';  -- payment received
  v_j3        UUID := 'd0560000-0000-4000-9000-000000000003';  -- till short
  v_j4        UUID := 'd0560000-0000-4000-9000-000000000004';  -- rent accrual
  -- account ids (resolved from the 0007 chart; every code exists post-migration)
  a_1020 UUID; a_1100 UUID; a_2100 UUID; a_2200 UUID; a_4000 UUID; a_6100 UUID; a_6350 UUID;
BEGIN
  -- Idempotency: if the first demo journal already exists, this migration already ran — no-op.
  IF EXISTS (SELECT 1 FROM gl_journals WHERE id = v_j1) THEN
    RETURN;
  END IF;

  SELECT id INTO v_period_id FROM fiscal_periods WHERE year = 2026 AND month = 6;
  IF v_period_id IS NULL THEN
    -- The 2026 periods are seeded by 0007; if absent, there is nothing to reconcile — skip safely.
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
    -- A required GL account is missing (chart not fully migrated) — skip rather than post a wrong journal.
    RETURN;
  END IF;

  -- entry_no is unique per fiscal_year; take the next four above the current max so we never collide with
  -- any organic FY2026 postings that may already exist.
  SELECT COALESCE(MAX(entry_no), 0) INTO v_base FROM gl_journals WHERE fiscal_year = 2026;

  INSERT INTO gl_journals (id, fiscal_year, entry_no, period_id, source, status, posted_at, branch_id, memo, idempotency_key)
  VALUES
    (v_j1, 2026, v_base + 1, v_period_id, 'ADJUSTMENT', 'POSTED', v_posted, NULL, 'M2-C1 demo: invoice finalized', 'm2c1-demo-inv-2026-06'),
    (v_j2, 2026, v_base + 2, v_period_id, 'ADJUSTMENT', 'POSTED', v_posted, NULL, 'M2-C1 demo: payment received', 'm2c1-demo-pay-2026-06'),
    (v_j3, 2026, v_base + 3, v_period_id, 'ADJUSTMENT', 'POSTED', v_posted, NULL, 'M2-C1 demo: till over/short', 'm2c1-demo-till-2026-06'),
    (v_j4, 2026, v_base + 4, v_period_id, 'ADJUSTMENT', 'POSTED', v_posted, NULL, 'M2-C1 demo: rent accrual',    'm2c1-demo-rent-2026-06')
  ON CONFLICT (id) DO NOTHING;

  INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, memo) VALUES
    -- 1. Invoice finalized: DR 1100 1,130,000 / CR 4000 1,000,000 / CR 2200 130,000
    (v_j1, 1, a_1100, 1130000, NULL,    'AR'),
    (v_j1, 2, a_4000, NULL,    1000000, 'revenue'),
    (v_j1, 3, a_2200, NULL,    130000,  'HST'),
    -- 2. Payment received (cash/cheque → 1020 undeposited): DR 1020 500,000 / CR 1100 500,000
    (v_j2, 1, a_1020, 500000,  NULL,    'undeposited receipt'),
    (v_j2, 2, a_1100, NULL,    500000,  'AR settled'),
    -- 3. Till session short: DR 6350 2,500 / CR 1020 2,500
    (v_j3, 1, a_6350, 2500,    NULL,    'cash short'),
    (v_j3, 2, a_1020, NULL,    2500,    'undeposited reduced'),
    -- 4. Rent accrual: DR 6100 250,000 / CR 2100 250,000
    (v_j4, 1, a_6100, 250000,  NULL,    'rent'),
    (v_j4, 2, a_2100, NULL,    250000,  'AP');
END $$;
