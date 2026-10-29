//go:build finworld

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FinWorldTieResult records one tie verification outcome.
type FinWorldTieResult struct {
	Name   string
	Passed bool
	Detail string
}

func finworldDollars(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

// runFinWorldTies runs the month walk's tie functions directly against the
// finworld database through day `throughDay`.
func runFinWorldTies(ctx context.Context, pool *pgxpool.Pool, plan *FinWorldPlanT, throughDay int) ([]FinWorldTieResult, error) {
	days := finworldFlattenDays(plan)
	if throughDay < 0 || throughDay >= len(days) {
		return nil, fmt.Errorf("finworld ties: throughDay %d out of range 0..%d", throughDay, len(days)-1)
	}
	day := days[throughDay].Day
	loc, err := time.LoadLocation(plan.HouseZone)
	if err != nil {
		return nil, err
	}
	dTime, err := time.ParseInLocation("2006-01-02", day.Date, loc)
	if err != nil {
		return nil, err
	}
	asOf := time.Date(dTime.Year(), dTime.Month(), dTime.Day(), 23, 59, 59, 999999999, loc)

	sctx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: finworldStaffUser,
		Scope:  identity.ScopeGlobal,
		Status: identity.StatusActive,
	})
	glRepo := gl.NewRepository(pool)
	glSvc := gl.NewService(glRepo)

	seams, err := financeCheckSeams(ctx, pool, pool, nil)
	if err != nil {
		return nil, fmt.Errorf("finworld wire tie seams: %w", err)
	}
	seams.Clock = func() time.Time { return asOf }

	var bankAccountID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&bankAccountID); err != nil {
		return nil, fmt.Errorf("operating bank account 1000: %w", err)
	}

	var activeTieMonths []TieMonth
	for mIdx, mp := range plan.Months {
		if len(mp.TradingDays) == 0 || mp.TradingDays[0].TradingDayIndex > throughDay {
			continue
		}
		tm := TieMonth{
			Year:          mp.YearMonth.Year,
			Month:         mp.YearMonth.Month,
			MonthEnd:      seams.monthEnd(mp.YearMonth.Year, mp.YearMonth.Month),
			BankAccountID: bankAccountID,
		}
		lastDayIdx := mp.TradingDays[len(mp.TradingDays)-1].TradingDayIndex
		if throughDay < lastDayIdx {
			tm.MonthEnd = asOf
		}
		_ = mIdx
		activeTieMonths = append(activeTieMonths, tm)
	}
	latestTieMonth := activeTieMonths[len(activeTieMonths)-1]

	var results []FinWorldTieResult

	// 1. AR aging equals GL 1100 (shared TieARAgingToGL1100 in fw1_ties.go)
	{
		allPass := true
		var lastDetail string
		for _, tm := range activeTieMonths {
			res, err := TieARAgingToGL1100(ctx, seams, tm)
			if err != nil {
				return nil, fmt.Errorf("TieARAgingToGL1100 %04d-%02d: %w", tm.Year, tm.Month, err)
			}
			if !res.Pass {
				allPass = false
				lastDetail = fmt.Sprintf("%04d-%02d: %s", tm.Year, tm.Month, res.Detail)
				break
			}
			lastDetail = fmt.Sprintf("%04d-%02d: %s", tm.Year, tm.Month, res.Detail)
		}
		results = append(results, FinWorldTieResult{
			Name:   "AR_aging_ties_to_GL_1100",
			Passed: allPass,
			Detail: lastDetail,
		})
	}

	// 2. Customer statements equal aging and subledger (shared TieStatementsToAging in fw1_ties.go)
	{
		// Ensure a statement run exists at the current month's MonthEnd if we are mid-month.
		var hasRun bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ar_statement_runs WHERE as_of_date = $1::date)`, latestTieMonth.MonthEnd.Format("2006-01-02")).Scan(&hasRun); err != nil {
			return nil, fmt.Errorf("check statement run: %w", err)
		}
		if !hasRun {
			stmtSvc := billing.NewStatementService(billing.NewRepository(pool))
			if _, _, err := stmtSvc.GenerateRun(sctx, "MASS", "", latestTieMonth.MonthEnd, finworldStaffUser); err != nil {
				return nil, fmt.Errorf("statement run for tie: %w", err)
			}
		}
		allPass := true
		var lastDetail string
		for _, tm := range activeTieMonths {
			res, err := TieStatementsToAging(ctx, seams, tm)
			if err != nil {
				return nil, fmt.Errorf("TieStatementsToAging %04d-%02d: %w", tm.Year, tm.Month, err)
			}
			if !res.Pass {
				allPass = false
				lastDetail = fmt.Sprintf("%04d-%02d: %s", tm.Year, tm.Month, res.Detail)
				break
			}
			lastDetail = fmt.Sprintf("%04d-%02d: %s", tm.Year, tm.Month, res.Detail)
		}
		results = append(results, FinWorldTieResult{
			Name:   "customer_statement_equals_aging_and_subledger",
			Passed: allPass,
			Detail: lastDetail,
		})
	}

	// 3. Stock value equals GL 1200 (shared TieStockToGL1200 in fw1_ties.go + per-branch check)
	{
		allPass := true
		var failDetail string
		for _, tm := range activeTieMonths {
			res, err := TieStockToGL1200(ctx, seams, tm)
			if err != nil {
				return nil, fmt.Errorf("TieStockToGL1200 %04d-%02d: %w", tm.Year, tm.Month, err)
			}
			if !res.Pass {
				allPass = false
				failDetail = fmt.Sprintf("%04d-%02d: %s", tm.Year, tm.Month, res.Detail)
				break
			}
		}
		stockByBranch := func(branchID string) (int64, error) {
			var v int64
			err := pool.QueryRow(ctx, `
				SELECT COALESCE(ROUND(SUM(sb.on_hand_base * c.avg_unit_cost_cents))::bigint, 0)
				  FROM stock_balances sb
				  JOIN product_branch_costs c ON c.product_id = sb.product_id AND c.branch_id = sb.branch_id
				 WHERE sb.on_hand_base != 0
				   AND ($1 = '' OR sb.branch_id::text = $1)`, branchID).Scan(&v)
			return v, err
		}
		trtStock, err := stockByBranch(finworldBranchTRT)
		if err != nil {
			return nil, fmt.Errorf("TRT stock read: %w", err)
		}
		kgnStock, err := stockByBranch(finworldBranchKGN)
		if err != nil {
			return nil, fmt.Errorf("KGN stock read: %w", err)
		}
		consStock, err := stockByBranch("")
		if err != nil {
			return nil, fmt.Errorf("consolidated stock read: %w", err)
		}
		trtGL, _, err := glRepo.AccountBalanceAsOf(ctx, "1200", asOf, finworldBranchTRT)
		if err != nil {
			return nil, err
		}
		kgnGL, _, err := glRepo.AccountBalanceAsOf(ctx, "1200", asOf, finworldBranchKGN)
		if err != nil {
			return nil, err
		}
		consGL, _, err := glRepo.AccountBalanceAsOf(ctx, "1200", asOf, "")
		if err != nil {
			return nil, err
		}
		passed := allPass && trtStock == trtGL && kgnStock == kgnGL && consStock == consGL && (trtStock+kgnStock == consStock)
		detail := fmt.Sprintf("TRT stock=%s GL1200=%s; KGN stock=%s GL1200=%s; Consolidated stock=%s GL1200=%s",
			finworldDollars(trtStock), finworldDollars(trtGL),
			finworldDollars(kgnStock), finworldDollars(kgnGL),
			finworldDollars(consStock), finworldDollars(consGL))
		if failDetail != "" {
			detail += " (" + failDetail + ")"
		}
		results = append(results, FinWorldTieResult{
			Name:   "stock_value_ties_to_GL_1200",
			Passed: passed,
			Detail: detail,
		})
	}

	// 4. HST collected and ITCs tie to GL 2200 and tax ReconcileReport (shared TieHSTMovementToDocuments in fw1_ties.go)
	{
		allPass := true
		var lastDetail string
		for _, tm := range activeTieMonths {
			res, err := TieHSTMovementToDocuments(ctx, seams, tm)
			if err != nil {
				return nil, fmt.Errorf("TieHSTMovementToDocuments %04d-%02d: %w", tm.Year, tm.Month, err)
			}
			if !res.Pass {
				allPass = false
				lastDetail = fmt.Sprintf("%04d-%02d: %s", tm.Year, tm.Month, res.Detail)
				break
			}
			lastDetail = fmt.Sprintf("%04d-%02d: %s", tm.Year, tm.Month, res.Detail)
		}
		results = append(results, FinWorldTieResult{
			Name:   "HST_reconcile_ties",
			Passed: allPass,
			Detail: lastDetail,
		})
	}

	// 4b. AP (2100) ties to the AP aging at every month end (W2-9a's rule, the same tie the box's
	// finance-check runs): purchasing's own aging, posted vendor bills less PayBill's payments plus the
	// landed cost payables, against GL 2100. GRNI (2150) clears to zero at every month end.
	{
		apSeams := TieSeams{Querier: pool, GLStore: glRepo, AP: financeCheckAPAging(pool)}
		allPass := true
		var lastDetail string
		for _, tm := range activeTieMonths {
			res, err := TieAPAgingToGL2100(ctx, apSeams, tm)
			if err != nil {
				return nil, fmt.Errorf("AP tie %04d-%02d: %w", tm.Year, tm.Month, err)
			}
			gl2150, _, err := glRepo.AccountBalanceAsOf(ctx, "2150", tm.MonthEnd, "")
			if err != nil {
				return nil, fmt.Errorf("GL 2150 read %04d-%02d: %w", tm.Year, tm.Month, err)
			}
			if !res.Pass || gl2150 != 0 {
				allPass = false
				lastDetail = fmt.Sprintf("%04d-%02d FAIL: %s; GL 2150=%s", tm.Year, tm.Month, res.Detail, finworldDollars(gl2150))
				break
			}
			lastDetail = fmt.Sprintf("%04d-%02d: %s (GL 2150 GRNI=%s)", tm.Year, tm.Month, res.Detail, finworldDollars(gl2150))
		}
		results = append(results, FinWorldTieResult{
			Name:   "AP_2100_ties_to_vendor_subledger",
			Passed: allPass,
			Detail: lastDetail,
		})
	}

	// 5a. Till lodgements post DR 1000 / CR 1020 at lodgement
	{
		rows, err := pool.Query(ctx, `
			SELECT tm.id::text, COALESCE(tm.reference, ''), tm.amount_cents
			  FROM till_movements tm
			 WHERE tm.movement_type = 'LODGEMENT'
			 ORDER BY tm.created_at, tm.id`)
		if err != nil {
			return nil, fmt.Errorf("till lodgements read: %w", err)
		}
		type lodgRow struct {
			id     string
			ref    string
			amount int64
		}
		var lodgements []lodgRow
		for rows.Next() {
			var r lodgRow
			if err := rows.Scan(&r.id, &r.ref, &r.amount); err != nil {
				rows.Close()
				return nil, err
			}
			lodgements = append(lodgements, r)
		}
		rows.Close()

		badCount := 0
		var firstBad string
		for _, l := range lodgements {
			var jid string
			var dr1000, cr1020 int64
			err := pool.QueryRow(ctx, `
				SELECT j.id::text,
				       COALESCE(SUM(CASE WHEN a.code = '1000' THEN COALESCE(jl.debit_cents, 0) - COALESCE(jl.credit_cents, 0) ELSE 0 END), 0),
				       COALESCE(SUM(CASE WHEN a.code = '1020' THEN COALESCE(jl.credit_cents, 0) - COALESCE(jl.debit_cents, 0) ELSE 0 END), 0)
				  FROM gl_journals j
				  JOIN gl_journal_lines jl ON jl.journal_id = j.id
				  JOIN gl_accounts a ON a.id = jl.account_id
				 WHERE j.status = 'POSTED'
				   AND (j.source_ref_id = $1::uuid OR j.idempotency_key = 'till.movement.posted:' || $1)
				 GROUP BY j.id`, l.id).Scan(&jid, &dr1000, &cr1020)
			if err != nil || dr1000 != l.amount || cr1020 != l.amount {
				badCount++
				if firstBad == "" {
					firstBad = fmt.Sprintf("lodgement %s (%s): amount=%d dr1000=%d cr1020=%d err=%v", l.id, l.ref, l.amount, dr1000, cr1020, err)
				}
			}
		}
		passed := badCount == 0
		detail := fmt.Sprintf("checked %d lodgements, bad=%d", len(lodgements), badCount)
		if firstBad != "" {
			detail += " (" + firstBad + ")"
		}
		results = append(results, FinWorldTieResult{
			Name:   "till_lodgement_posts_bank_1000_at_lodgement",
			Passed: passed,
			Detail: detail,
		})
	}

	// 5b. Bank lodgement match posts no second journal
	{
		var duplicateJournals int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*)
			  FROM bank_matches bm
			  JOIN gl_journals j ON j.id = bm.journal_id
			 WHERE bm.target_type = 'LODGEMENT'
			   AND bm.unmatched_at IS NULL
			   AND j.source_ref_id IS DISTINCT FROM bm.target_ref_id`).Scan(&duplicateJournals); err != nil {
			return nil, fmt.Errorf("bank lodgement match check: %w", err)
		}
		results = append(results, FinWorldTieResult{
			Name:   "bank_lodgement_match_posts_no_second_journal",
			Passed: duplicateJournals == 0,
			Detail: fmt.Sprintf("lodgement matches with distinct journal source_ref_id=%d", duplicateJournals),
		})
	}

	// 5b2. Undeposited funds 1020 ties to unlodged RECEIVED payments
	{
		gl1020, _, err := glRepo.AccountBalanceAsOf(ctx, "1020", asOf, "")
		if err != nil {
			return nil, fmt.Errorf("GL 1020 read: %w", err)
		}
		var unlodgedCents int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_cents), 0)
			  FROM payments
			 WHERE status = 'RECEIVED'
			   AND reversed_at IS NULL
			   AND reverses_payment_id IS NULL
			   AND received_at <= $1`, asOf).Scan(&unlodgedCents); err != nil {
			return nil, fmt.Errorf("unlodged payments read: %w", err)
		}
		results = append(results, FinWorldTieResult{
			Name:   "undeposited_1020_ties_to_unlodged_receipts",
			Passed: gl1020 == unlodgedCents,
			Detail: fmt.Sprintf("GL 1020=%s (%d cents), unlodged RECEIVED payments=%s (%d cents), diff=%d cents",
				finworldDollars(gl1020), gl1020, finworldDollars(unlodgedCents), unlodgedCents, gl1020-unlodgedCents),
		})
	}

	// 5c. Bank month signs off at zero with outstanding items (shared TieBankReconciliationAtZero in fw1_ties.go)
	{
		closedMonths := 0
		badRecs := 0
		var recParts []string
		for m := 0; m < len(plan.Months); m++ {
			mp := plan.Months[m]
			if !mp.IsClosed || len(mp.TradingDays) == 0 {
				continue
			}
			lastDayIdx := mp.TradingDays[len(mp.TradingDays)-1].TradingDayIndex
			if throughDay < lastDayIdx {
				continue
			}
			closedMonths++
			tm := TieMonth{
				Year:          mp.YearMonth.Year,
				Month:         mp.YearMonth.Month,
				MonthEnd:      seams.monthEnd(mp.YearMonth.Year, mp.YearMonth.Month),
				BankAccountID: bankAccountID,
			}
			res, err := TieBankReconciliationAtZero(ctx, seams, tm)
			if err != nil {
				return nil, fmt.Errorf("TieBankReconciliationAtZero %s: %w", mp.YearMonth.String(), err)
			}
			dit, err := TieDepositsInTransit(ctx, seams, tm)
			if err != nil {
				return nil, fmt.Errorf("TieDepositsInTransit %s: %w", mp.YearMonth.String(), err)
			}
			outChq, err := TieOutstandingCheques(ctx, seams, tm)
			if err != nil {
				return nil, fmt.Errorf("TieOutstandingCheques %s: %w", mp.YearMonth.String(), err)
			}
			if !res.Pass || !res.Applicable || dit <= 0 || outChq <= 0 {
				badRecs++
				recParts = append(recParts, fmt.Sprintf("%s FAIL (%s, dit=%s, outChq=%s)", mp.YearMonth.String(), res.Detail, finworldDollars(dit), finworldDollars(outChq)))
			} else {
				recParts = append(recParts, fmt.Sprintf("%s PASS (%s)", mp.YearMonth.String(), res.Detail))
			}
		}
		recSummary := strings.Join(recParts, "; ")
		if recSummary == "" {
			recSummary = "no closed months yet"
		}
		results = append(results, FinWorldTieResult{
			Name:   "bank_month_signs_off_at_zero_with_outstanding_items",
			Passed: badRecs == 0,
			Detail: fmt.Sprintf("closedMonths=%d, badRecs=%d (%s)", closedMonths, badRecs, recSummary),
		})
	}

	// 6. Every journal line carries a branch and branch trial balances sum to the consolidated trial balance (shared TieBranchTrialBalancesToConsolidated in fw1_ties.go)
	{
		var nullHeaderBranches, nullLineBranches int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM gl_journals WHERE status = 'POSTED' AND branch_id IS NULL`).Scan(&nullHeaderBranches); err != nil {
			return nil, err
		}
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*)
			  FROM gl_journal_lines jl
			  JOIN gl_journals j ON j.id = jl.journal_id
			 WHERE j.status = 'POSTED'
			   AND jl.branch_id IS NULL`).Scan(&nullLineBranches); err != nil {
			return nil, err
		}

		tbMismatches := 0
		var lastTBDetail string
		for _, tm := range activeTieMonths {
			res, err := TieBranchTrialBalancesToConsolidated(ctx, seams, tm)
			if err != nil {
				return nil, fmt.Errorf("TieBranchTrialBalancesToConsolidated %04d-%02d: %w", tm.Year, tm.Month, err)
			}
			if !res.Pass {
				tbMismatches++
			}
			lastTBDetail = fmt.Sprintf("%04d-%02d: %s", tm.Year, tm.Month, res.Detail)
		}
		passed := nullHeaderBranches == 0 && nullLineBranches == 0 && tbMismatches == 0
		results = append(results, FinWorldTieResult{
			Name:   "every_journal_line_carries_a_branch_and_branch_trial_balances_sum",
			Passed: passed,
			Detail: fmt.Sprintf("nullHeaderBranches=%d, nullLineBranches=%d, tbMismatches=%d (%s)",
				nullHeaderBranches, nullLineBranches, tbMismatches, lastTBDetail),
		})
	}

	// 7. Outbox clean (shared TieOutboxDrained in fw1_ties.go)
	{
		res, err := TieOutboxDrained(ctx, seams, latestTieMonth)
		if err != nil {
			return nil, fmt.Errorf("TieOutboxDrained: %w", err)
		}
		results = append(results, FinWorldTieResult{
			Name:   "no_journal_waiting_or_dead_lettered",
			Passed: res.Pass,
			Detail: res.Detail,
		})
	}

	// 8a. Walk month and next year periods open
	{
		if _, err := glSvc.EnsureNextFiscalYear(ctx); err != nil {
			return nil, fmt.Errorf("EnsureNextFiscalYear: %w", err)
		}
		var open2026, open2027 int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM fiscal_periods WHERE year = 2026`).Scan(&open2026); err != nil {
			return nil, err
		}
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM fiscal_periods WHERE year = 2027 AND status = 'OPEN'`).Scan(&open2027); err != nil {
			return nil, err
		}
		results = append(results, FinWorldTieResult{
			Name:   "walk_month_and_next_year_periods_open",
			Passed: open2026 == 12 && open2027 == 12,
			Detail: fmt.Sprintf("fiscal_periods 2026=%d, 2027 OPEN=%d", open2026, open2027),
		})
	}

	// 8b. Month close passes pre-flight and seals the month
	{
		closedChecked := 0
		notClosed := 0
		for m := 0; m < len(plan.Months); m++ {
			mp := plan.Months[m]
			if !mp.IsClosed || len(mp.TradingDays) == 0 {
				continue
			}
			lastDayIdx := mp.TradingDays[len(mp.TradingDays)-1].TradingDayIndex
			if throughDay < lastDayIdx {
				continue
			}
			closedChecked++
			var status string
			err := pool.QueryRow(ctx, `
				SELECT status FROM fiscal_periods WHERE year = $1 AND month = $2`,
				mp.YearMonth.Year, mp.YearMonth.Month).Scan(&status)
			if err != nil || status != "CLOSED" {
				notClosed++
			}
		}
		results = append(results, FinWorldTieResult{
			Name:   "month_close_passes_its_pre_flight_and_seals_the_month",
			Passed: notClosed == 0,
			Detail: fmt.Sprintf("closedMonthsChecked=%d, notClosed=%d", closedChecked, notClosed),
		})
	}

	// 9. Year-end close (shared TieClosedYearIncomeStatementUnchanged in fw1_ties.go)
	if len(plan.Months) > 2 && len(plan.Months[2].TradingDays) > 0 {
		m2LastDay := plan.Months[2].TradingDays[len(plan.Months[2].TradingDays)-1].TradingDayIndex
		if throughDay >= m2LastDay {
			var closingCount, nullBranchLines, open2025, count2026 int
			if err := pool.QueryRow(ctx, `
				SELECT COUNT(*) FROM gl_journals
				 WHERE fiscal_year = 2025 AND source = 'CLOSING' AND status = 'POSTED'`).Scan(&closingCount); err != nil {
				return nil, err
			}
			if err := pool.QueryRow(ctx, `
				SELECT COUNT(*)
				  FROM gl_journal_lines jl
				  JOIN gl_journals j ON j.id = jl.journal_id
				 WHERE j.fiscal_year = 2025 AND j.source = 'CLOSING' AND j.status = 'POSTED'
				   AND jl.branch_id IS NULL`).Scan(&nullBranchLines); err != nil {
				return nil, err
			}
			if err := pool.QueryRow(ctx, `
				SELECT COUNT(*) FROM fiscal_periods WHERE year = 2025 AND status <> 'CLOSED'`).Scan(&open2025); err != nil {
				return nil, err
			}
			if err := pool.QueryRow(ctx, `
				SELECT COUNT(*) FROM fiscal_periods WHERE year = 2026`).Scan(&count2026); err != nil {
				return nil, err
			}
			dec2025 := TieMonth{
				Year:          2025,
				Month:         12,
				MonthEnd:      seams.monthEnd(2025, 12),
				BankAccountID: bankAccountID,
			}
			isTieRes, err := TieClosedYearIncomeStatementUnchanged(ctx, seams, dec2025)
			if err != nil {
				return nil, fmt.Errorf("TieClosedYearIncomeStatementUnchanged 2025: %w", err)
			}
			passed := closingCount >= 1 && nullBranchLines == 0 && open2025 == 0 && count2026 == 12 && isTieRes.Pass
			results = append(results, FinWorldTieResult{
				Name:   "year_end_close_opens_next_year_and_keeps_income_statement",
				Passed: passed,
				Detail: fmt.Sprintf("2025 CLOSING journals=%d (nullBranchLines=%d), open2025=%d, 2026 periods=%d, %s",
					closingCount, nullBranchLines, open2025, count2026, isTieRes.Detail),
			})
		}
	}

	return results, nil
}
