//go:build finworld

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
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

	var results []FinWorldTieResult

	// 1. AR aging equals GL 1100
	{
		creditSvc := billing.NewCreditService(billing.NewRepository(pool))
		rows, err := creditSvc.Aging(sctx, asOf, "")
		if err != nil {
			return nil, fmt.Errorf("AR aging read: %w", err)
		}
		var agingTotal int64
		for _, r := range rows {
			agingTotal += r.TotalCents
		}
		gl1100, _, err := glRepo.AccountBalanceAsOf(ctx, "1100", asOf, "")
		if err != nil {
			return nil, fmt.Errorf("GL 1100 read: %w", err)
		}
		passed := agingTotal == gl1100
		results = append(results, FinWorldTieResult{
			Name:   "AR_aging_ties_to_GL_1100",
			Passed: passed,
			Detail: fmt.Sprintf("through day %d (%s): AR aging=%s (%d cents), GL 1100=%s (%d cents), diff=%d cents",
				throughDay, day.Date, finworldDollars(agingTotal), agingTotal, finworldDollars(gl1100), gl1100, agingTotal-gl1100),
		})
	}

	// 2. Customer statements equal aging and subledger
	{
		billRepo := billing.NewRepository(pool)
		creditSvc := billing.NewCreditService(billRepo)
		agingRows, err := creditSvc.Aging(sctx, asOf, "")
		if err != nil {
			return nil, fmt.Errorf("AR aging for statement tie: %w", err)
		}
		agingByCust := make(map[string]int64, len(agingRows))
		for _, r := range agingRows {
			agingByCust[r.CustomerID] = r.TotalCents
		}
		stmtSvc := billing.NewStatementService(billRepo)
		_, stmts, err := stmtSvc.GenerateRun(sctx, "MASS", "", asOf, finworldStaffUser)
		if err != nil {
			return nil, fmt.Errorf("statement run: %w", err)
		}
		stmtByCust := make(map[string]int64, len(stmts))
		for _, it := range stmts {
			stmtByCust[it.CustomerID] = it.ClosingCents
		}
		cRows, err := pool.Query(ctx, `SELECT id::text FROM customers ORDER BY account_no`)
		if err != nil {
			return nil, fmt.Errorf("customers list: %w", err)
		}
		var custIDs []string
		for cRows.Next() {
			var cid string
			if err := cRows.Scan(&cid); err != nil {
				cRows.Close()
				return nil, err
			}
			custIDs = append(custIDs, cid)
		}
		cRows.Close()

		mismatches := 0
		var firstMismatch string
		var totalSubledger int64
		for _, cid := range custIDs {
			var subledger int64
			if err := pool.QueryRow(ctx, `
				SELECT COALESCE(SUM(amount_cents), 0)
				  FROM customer_transactions
				 WHERE customer_id = $1::uuid
				   AND occurred_at <= $2`, cid, asOf).Scan(&subledger); err != nil {
				return nil, fmt.Errorf("customer subledger %s: %w", cid, err)
			}
			totalSubledger += subledger
			var docsAndReceipts int64
			if err := pool.QueryRow(ctx, `
				SELECT COALESCE((
				    SELECT SUM(balance_cents) FROM invoices
				     WHERE customer_id = $1::uuid AND status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
				), 0) + COALESCE((
				    SELECT SUM(
				        pn.fee_cents - COALESCE((
				            SELECT SUM((p2.metadata->'nsf_fee_allocations'->>pn.id::text)::bigint)
				              FROM payments p2
				             WHERE p2.customer_id = pn.customer_id
				               AND p2.reversed_at IS NULL
				               AND p2.reverses_payment_id IS NULL
				               AND p2.status <> 'BOUNCED'
				               AND (p2.metadata->'nsf_fee_allocations'->>pn.id::text) IS NOT NULL
				        ), 0)
				    )
				      FROM payment_nsf pn
				     WHERE pn.customer_id = $1::uuid
				), 0)`, cid).Scan(&docsAndReceipts); err != nil {
				return nil, fmt.Errorf("customer docs and receipts %s: %w", cid, err)
			}
			lines, err := billRepo.StatementLines(ctx, cid, asOf, billing.AllBranches)
			if err != nil {
				return nil, fmt.Errorf("statement lines %s: %w", cid, err)
			}
			var walked int64
			if len(lines) > 0 {
				walked = lines[len(lines)-1].BalanceRun
			}
			ag := agingByCust[cid]
			st := stmtByCust[cid]
			if ag != st || st != walked || walked != subledger || subledger != docsAndReceipts {
				mismatches++
				if firstMismatch == "" {
					firstMismatch = fmt.Sprintf("customer %s: aging=%d stmt=%d walked=%d subledger=%d docs=%d", cid, ag, st, walked, subledger, docsAndReceipts)
				}
			}
		}
		passed := mismatches == 0
		detail := fmt.Sprintf("checked %d customers at %s: total subledger=%s (%d cents), mismatches=%d",
			len(custIDs), day.Date, finworldDollars(totalSubledger), totalSubledger, mismatches)
		if firstMismatch != "" {
			detail += " (" + firstMismatch + ")"
		}
		results = append(results, FinWorldTieResult{
			Name:   "customer_statement_equals_aging_and_subledger",
			Passed: passed,
			Detail: detail,
		})
	}

	// 3. Stock value equals GL 1200 (per branch and consolidated)
	{
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
		passed := trtStock == trtGL && kgnStock == kgnGL && consStock == consGL && (trtStock+kgnStock == consStock)
		results = append(results, FinWorldTieResult{
			Name:   "stock_value_ties_to_GL_1200",
			Passed: passed,
			Detail: fmt.Sprintf("TRT stock=%s GL1200=%s; KGN stock=%s GL1200=%s; Consolidated stock=%s GL1200=%s",
				finworldDollars(trtStock), finworldDollars(trtGL),
				finworldDollars(kgnStock), finworldDollars(kgnGL),
				finworldDollars(consStock), finworldDollars(consGL)),
		})
	}

	// 4. HST collected and ITCs tie to GL 2200 and tax ReconcileReport
	{
		var gl2200 int64
		gl2200, _, err = glRepo.AccountBalanceAsOf(ctx, "2200", asOf, "")
		if err != nil {
			return nil, fmt.Errorf("GL 2200 read: %w", err)
		}
		// GL 2200 is a LIABILITY (CREDIT normal), while AccountBalanceAsOf returns debit - credit.
		gl2200Credit := -gl2200

		var salesTaxCents, posReturnTaxCents, billITCCents int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(tax_cents), 0)
			  FROM invoices
			 WHERE status NOT IN ('DRAFT', 'VOID')
			   AND created_at <= $1`, asOf).Scan(&salesTaxCents); err != nil {
			return nil, fmt.Errorf("invoices HST read: %w", err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(tax_cents), 0)
			  FROM pos_returns
			 WHERE status = 'COMMITTED'
			   AND created_at <= $1`, asOf).Scan(&posReturnTaxCents); err != nil {
			return nil, fmt.Errorf("pos_returns HST read: %w", err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(tax_cents), 0)
			  FROM purchasing_bills
			 WHERE status = 'POSTED'
			   AND created_at <= $1`, asOf).Scan(&billITCCents); err != nil {
			return nil, fmt.Errorf("purchasing_bills ITC read: %w", err)
		}
		netDocHST := salesTaxCents - posReturnTaxCents - billITCCents

		taxSvc := tax.NewService(tax.NewRepository(pool)).
			WithSalesHSTs(billing.NewRepository(pool)).
			WithReturnHSTs(pos.NewRepository(pool)).
			WithBillITCs(purchasing.NewRepository(pool)).
			WithGLBalances(glBalanceAdapter{svc: glSvc})

		activeMonths := make(map[YearMonth]bool)
		for i := 0; i <= throughDay; i++ {
			activeMonths[days[i].YearMonth] = true
		}
		reconcileFailures := 0
		var firstRecErr string
		for ym := range activeMonths {
			rep, rerr := taxSvc.ReconcileReport(ctx, ym.Year, ym.Month, "")
			if rerr != nil {
				return nil, fmt.Errorf("HST ReconcileReport %s: %w", ym.String(), rerr)
			}
			if rep.VarianceCents != 0 || !rep.TiedOut {
				reconcileFailures++
				if firstRecErr == "" {
					firstRecErr = fmt.Sprintf("%s variance=%d", ym.String(), rep.VarianceCents)
				}
			}
		}

		passed := gl2200Credit == netDocHST && reconcileFailures == 0
		detail := fmt.Sprintf("Sales/CM HST=%s, POS Return HST=%s, Bill ITC=%s, Net Doc HST=%s, GL 2200 (credit)=%s, diff=%d cents, monthly ReconcileReport failures=%d",
			finworldDollars(salesTaxCents), finworldDollars(posReturnTaxCents), finworldDollars(billITCCents),
			finworldDollars(netDocHST), finworldDollars(gl2200Credit), gl2200Credit-netDocHST, reconcileFailures)
		if firstRecErr != "" {
			detail += " (" + firstRecErr + ")"
		}
		results = append(results, FinWorldTieResult{
			Name:   "HST_reconcile_ties",
			Passed: passed,
			Detail: detail,
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

	// 5c. Bank month signs off at zero with outstanding items (for all closed months through throughDay)
	{
		closedMonths := 0
		badRecs := 0
		var recSummary string
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
			var status string
			var stmtBal, glBal, dit, outChq, variance int64
			err := pool.QueryRow(ctx, `
				SELECT status, statement_balance_cents, gl_balance_cents,
				       deposits_in_transit_cents, outstanding_cheques_cents, variance_cents
				  FROM bank_reconciliations
				 WHERE period_year = $1 AND period_month = $2`,
				mp.YearMonth.Year, mp.YearMonth.Month).Scan(&status, &stmtBal, &glBal, &dit, &outChq, &variance)
			if err != nil {
				badRecs++
				if recSummary == "" {
					recSummary = fmt.Sprintf("%s missing bank_reconciliation: %v", mp.YearMonth.String(), err)
				}
				continue
			}
			adjusted := stmtBal + dit - outChq
			if status != "COMPLETED" || variance != 0 || adjusted != glBal || dit <= 0 || outChq <= 0 {
				badRecs++
				if recSummary == "" {
					recSummary = fmt.Sprintf("%s status=%s stmt=%d dit=%d outChq=%d gl=%d var=%d",
						mp.YearMonth.String(), status, stmtBal, dit, outChq, glBal, variance)
				}
			} else if m == 0 || m == 10 {
				if recSummary != "" {
					recSummary += "; "
				}
				recSummary += fmt.Sprintf("%s COMPLETED stmt=%s + DIT=%s - OutChq=%s = GL=%s (var=$0.00)",
					mp.YearMonth.String(), finworldDollars(stmtBal), finworldDollars(dit), finworldDollars(outChq), finworldDollars(glBal))
			}
		}
		if recSummary == "" {
			recSummary = "no closed months yet"
		}
		results = append(results, FinWorldTieResult{
			Name:   "bank_month_signs_off_at_zero_with_outstanding_items",
			Passed: badRecs == 0,
			Detail: fmt.Sprintf("closedMonths=%d, badRecs=%d (%s)", closedMonths, badRecs, recSummary),
		})
	}

	// 6. Every journal line carries a branch and branch trial balances sum to the consolidated trial balance
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

		activeMonths := make(map[YearMonth]bool)
		for i := 0; i <= throughDay; i++ {
			activeMonths[days[i].YearMonth] = true
		}
		tbMismatches := 0
		var lastTBDetail string
		for ym := range activeMonths {
			cons, err := glSvc.TrialBalance(sctx, ym.Year, ym.Month, "")
			if err != nil {
				return nil, fmt.Errorf("consolidated TB %s: %w", ym.String(), err)
			}
			trtTB, err := glSvc.TrialBalance(sctx, ym.Year, ym.Month, finworldBranchTRT)
			if err != nil {
				return nil, fmt.Errorf("TRT TB %s: %w", ym.String(), err)
			}
			kgnTB, err := glSvc.TrialBalance(sctx, ym.Year, ym.Month, finworldBranchKGN)
			if err != nil {
				return nil, fmt.Errorf("KGN TB %s: %w", ym.String(), err)
			}
			if cons.BalanceCents != 0 || trtTB.TotalDebit+kgnTB.TotalDebit != cons.TotalDebit || trtTB.TotalCredit+kgnTB.TotalCredit != cons.TotalCredit {
				tbMismatches++
			}
			lastTBDetail = fmt.Sprintf("%s: TRT DR=%s + KGN DR=%s = Cons DR=%s (net=%s)",
				ym.String(), finworldDollars(trtTB.TotalDebit), finworldDollars(kgnTB.TotalDebit),
				finworldDollars(cons.TotalDebit), finworldDollars(cons.BalanceCents))
		}
		passed := nullHeaderBranches == 0 && nullLineBranches == 0 && tbMismatches == 0
		results = append(results, FinWorldTieResult{
			Name:   "every_journal_line_carries_a_branch_and_branch_trial_balances_sum",
			Passed: passed,
			Detail: fmt.Sprintf("nullHeaderBranches=%d, nullLineBranches=%d, tbMismatches=%d (%s)",
				nullHeaderBranches, nullLineBranches, tbMismatches, lastTBDetail),
		})
	}

	// 7. Outbox clean (no PENDING or DEAD events)
	{
		var pendingOrDead int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM events_outbox WHERE status IN ('PENDING', 'DEAD')`).Scan(&pendingOrDead); err != nil {
			return nil, err
		}
		results = append(results, FinWorldTieResult{
			Name:   "no_journal_waiting_or_dead_lettered",
			Passed: pendingOrDead == 0,
			Detail: fmt.Sprintf("events_outbox PENDING/DEAD=%d", pendingOrDead),
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

	// 9. Year-end close (after 2025-12, i.e., throughDay >= last day of Month 2)
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
			var rev2025, exp2025 int64
			if err := pool.QueryRow(ctx, `
				SELECT COALESCE(SUM(CASE WHEN account_type = 'REVENUE' THEN -net_movement_cents ELSE 0 END), 0),
				       COALESCE(SUM(CASE WHEN account_type = 'EXPENSE' THEN net_movement_cents ELSE 0 END), 0)
				  FROM v_income_statement
				 WHERE fiscal_year = 2025`).Scan(&rev2025, &exp2025); err != nil {
				return nil, err
			}
			passed := closingCount >= 1 && nullBranchLines == 0 && open2025 == 0 && count2026 == 12 && rev2025 > 0 && exp2025 > 0
			results = append(results, FinWorldTieResult{
				Name:   "year_end_close_opens_next_year_and_keeps_income_statement",
				Passed: passed,
				Detail: fmt.Sprintf("2025 CLOSING journals=%d (nullBranchLines=%d), open2025=%d, 2026 periods=%d, 2025 IS revenue=%s expenses=%s",
					closingCount, nullBranchLines, open2025, count2026, finworldDollars(rev2025), finworldDollars(exp2025)),
			})
		}
	}

	return results, nil
}
