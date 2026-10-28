package main

// fw1_ties.go holds the month walk's tie rules in one place, as untagged code
// in the default build, so that the three callers that have to agree run the
// same rules and the same numbers:
//
//   - the month walk (core/cmd/core/fw1_month_walk_integration_test.go), which
//     keeps its twelve assertions and calls these;
//   - the world generator (core finworld, the -tags finworld build), which
//     makes no dump unless every tie passes;
//   - core finance-check (the finance box's read only check), which runs them
//     against the live box.
//
// A tie is one rule and one number: the difference, in cents, between two
// figures that have to be equal. There is no tolerance and no rounding here.
// Money in this tree is integer cents, so a difference is a difference. A tie
// passes at zero and fails at anything else.
//
// THE SEAMS. A tie reads the modules through the owning modules' own
// services where one exists (the GL's trial balance and balance reads,
// billing's aging and statement lines, the tax engine's reconcile report) and
// reads the rest through cmd/core's own SQL, exactly as the month walk read
// them before the move. cmd/core is the composition root, not a module: the
// module census that fences cross module SQL does not reach it, and the
// walk's own reads are the precedent.
//
// THE QUERIER. The ties' own SQL rides a tieQuerier. The walk hands it the
// pool, so the walk reads exactly as it read before the move. The box's check
// hands it a READ ONLY transaction, so the check's own SQL cannot write even
// by accident: Postgres refuses a write inside a read only transaction.
//
// THE MOVE IS A MOVE. The walk's own copy of each rule is gone. Where the
// walk's assertion also carried a story (the statement run it generates, the
// reconciliation it opens and signs off, the figures it planted), the story
// stays in the walk and only the comparison moved here.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
)

// tieQuerier is the read surface the ties' own SQL rides: a pool for the
// walk, a read only transaction for the box's check.
type tieQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TieResult is one named tie's outcome: the tie's name, whether it passed,
// the variance in cents (zero when it passed) and the words that say why. A
// tie that does not apply to the month it was asked about says so, and is not
// a failure: the bank reconciliation tie has nothing to say about a month
// that carries no reconciliation.
type TieResult struct {
	Name          string
	Pass          bool
	Applicable    bool
	VarianceCents int64
	Detail        string
}

// TieMonth names the month a tie reads, and the facts only the caller knows
// about that month.
type TieMonth struct {
	// Year and Month are the fiscal year and month the ties read.
	Year  int
	Month int
	// MonthEnd is the instant the month's end is read at, in the house zone.
	MonthEnd time.Time
	// BankAccountID is the account the month's reconciliation belongs to.
	BankAccountID string
	// ExcludeLodgementIDs are till lodgements the caller has already counted
	// in its own story, so the bank tie does not count them twice.
	ExcludeLodgementIDs []string
	// ExcludeInvoiceIDs are invoices the caller's own documents did not post
	// to GL 2200 (the walk's seeded invoice).
	ExcludeInvoiceIDs []string
	// IncomeBefore is the closed year's income statement reading the caller
	// took before the close, when the caller took one. Nil means the caller
	// has no before reading, and the year end tie then compares the income
	// statement against the net the close swept to retained earnings.
	IncomeBefore map[string]int64
}

// MonthStartDate is the first day of the month, in the zone the month's end
// is read in.
func (m TieMonth) MonthStartDate() string {
	return time.Date(m.Year, time.Month(m.Month), 1, 0, 0, 0, 0, m.MonthEnd.Location()).Format("2006-01-02")
}

// MonthEndDate is the last day of the month, in the zone the month's end is
// read in.
func (m TieMonth) MonthEndDate() string {
	return m.MonthEnd.Format("2006-01-02")
}

// TieSeams are the production seams the ties read through.
type TieSeams struct {
	// Querier is the read surface the ties' own SQL rides.
	Querier tieQuerier
	// GL is the ledger service, wired as the real wiring wires it.
	GL *gl.Service
	// GLStore is the ledger's own repository over the same pool, for the
	// balance reads the service does not carry.
	GLStore *gl.Repository
	// Billing is the AR module's own repository, for the aging and the
	// statement lines.
	Billing *billing.Repository
	// Tax is the tax engine's reconcile service, wired over the owning
	// modules' repositories exactly as the tops wiring wires it.
	Tax *tax.Service
	// Branches are the branch ids the branch trial balances are read for.
	Branches []string
	// Clock is the instant the ties read "now" at. The stock tie reads both
	// sides at the same instant, so it needs one clock for both. Nil is the
	// wall clock.
	Clock func() time.Time
	// Zone is the dealer's own business day zone, the zone the house month
	// and every month's end are read in. Nil leaves the instants alone.
	Zone *time.Location
	// AP is purchasing's own AP aging (W2-9a): posted vendor bills less the
	// payments PayBill recorded, plus the landed cost payables, as of an
	// instant. It is the AP subledger the 2100 tie reads.
	AP tieAPAging
}

// tieAPAging is the one purchasing read the AP tie needs.
type tieAPAging interface {
	APAging(ctx context.Context, asOf time.Time, reach purchasing.BranchReach) (purchasing.APAgingPage, error)
}

// now is the seam's clock: the instant the ties read now at, in the dealer's
// own business day zone when the seam names one.
func (s TieSeams) now() time.Time {
	var t time.Time
	if s.Clock != nil {
		t = s.Clock()
	} else {
		t = time.Now()
	}
	if s.Zone != nil {
		return t.In(s.Zone)
	}
	return t
}

// monthEnd is the last instant of a month in the dealer's own business day
// zone: the instant the month's end is read at.
func (s TieSeams) monthEnd(year, month int) time.Time {
	loc := s.Zone
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0).Add(-time.Second)
}

// The tie names. One name per rule, the same word in the walk, the world
// generator and the box's check, so a failing line names the rule and not
// the caller.
const (
	TieNameARAging      = "ar_aging_to_gl_1100"
	TieNameStock        = "stock_to_gl_1200"
	TieNameHST          = "hst_2200_movement_to_documents"
	TieNameBank         = "bank_reconciliation_at_zero"
	TieNameStatements   = "statements_to_aging"
	TieNameBranchTotals = "branch_trial_balances_to_consolidated"
	TieNameOutbox       = "outbox_drained"
	TieNameAPAging      = "ap_aging_to_gl_2100"
	TieNameIncomeClosed = "closed_year_income_statement_unchanged"
	TieNameNoBranchless = "every_journal_line_carries_a_branch"
)

// TiesForMonth runs every month tie for one month and answers one named
// result per tie, in a fixed order. The bank tie answers that it does not
// apply when the month carries no reconciliation, which is the open month's
// own case: a running month cannot sign off.
func TiesForMonth(ctx context.Context, seams TieSeams, m TieMonth) ([]TieResult, error) {
	ties := []func(context.Context, TieSeams, TieMonth) (TieResult, error){
		TieARAgingToGL1100,
		TieStockToGL1200,
		TieHSTMovementToDocuments,
		TieBankReconciliationAtZero,
		TieStatementsToAging,
		TieBranchTrialBalancesToConsolidated,
		TieOutboxDrained,
		TieAPAgingToGL2100,
	}
	out := make([]TieResult, 0, len(ties))
	for _, tie := range ties {
		res, err := tie(ctx, seams, m)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// TieARAgingToGL1100: the AR aging total equals GL 1100, inception to date,
// to the cent. The aging is read through billing's own aging service, the
// ledger side through the GL's own balance read.
func TieARAgingToGL1100(ctx context.Context, seams TieSeams, m TieMonth) (TieResult, error) {
	rows, err := billing.NewCreditService(seams.Billing).Aging(fw1StaffCtx(ctx), m.MonthEnd, "")
	if err != nil {
		return TieResult{}, fmt.Errorf("the aging read: %w", err)
	}
	var aging int64
	for _, r := range rows {
		aging += r.TotalCents
	}
	gl1100, _, err := seams.GLStore.AccountBalanceAsOf(ctx, "1100", m.MonthEnd, "")
	if err != nil {
		return TieResult{}, fmt.Errorf("the GL 1100 read: %w", err)
	}
	variance := aging - gl1100
	detail := fmt.Sprintf("AR aging total %s ties to GL 1100 %s (inception to date)", fw1Dollars(aging), fw1Dollars(gl1100))
	if variance != 0 {
		detail = fmt.Sprintf("AR aging total %s does not tie to GL 1100 %s (inception to date): the aging is %s %s",
			fw1Dollars(aging), fw1Dollars(gl1100), shorter(aging, gl1100), byDollars(variance))
	}
	return TieResult{Name: TieNameARAging, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// TieAPAgingToGL2100: the AP aging total equals GL 2100's credit balance as of
// the month's end, to the cent (W2-9a, FIN-AP-1 = A). The aging is the AP
// subledger: posted vendor bills less the payments PayBill recorded, plus the
// landed cost payables, read through purchasing's own aging. A 2100 entry that
// no bill, payment or landed cost made (a manual accrual, an adjustment) is in
// GL and not in the aging, by design: it is a difference the Controller
// reconciles, so a failing line names every such journal.
func TieAPAgingToGL2100(ctx context.Context, seams TieSeams, m TieMonth) (TieResult, error) {
	if seams.AP == nil {
		// A caller that wires no AP aging (the month walk's older legs) gets
		// a tie that says so; the box's check always wires it and refuses
		// otherwise (financeCheckSeams).
		return TieResult{Name: TieNameAPAging, Pass: true, Applicable: false, Detail: "no AP aging is wired for this read"}, nil
	}
	page, err := seams.AP.APAging(fw1StaffCtx(ctx), m.MonthEnd, purchasing.BranchReach{Global: true})
	if err != nil {
		return TieResult{}, fmt.Errorf("the AP aging read: %w", err)
	}
	gl2100, _, err := seams.GLStore.AccountBalanceAsOf(ctx, "2100", m.MonthEnd, "")
	if err != nil {
		return TieResult{}, fmt.Errorf("the GL 2100 read: %w", err)
	}
	glCredit := -gl2100
	variance := page.TotalCents - glCredit
	detail := fmt.Sprintf("AP aging total %s ties to GL 2100 %s as of the month's end", fw1Dollars(page.TotalCents), fw1Dollars(glCredit))
	if variance != 0 {
		others, err := tieAPOtherJournals(ctx, seams, m)
		if err != nil {
			return TieResult{}, err
		}
		detail = fmt.Sprintf("AP aging total %s does not tie to GL 2100 %s as of the month's end: the aging is %s %s; 2100 entries no bill, payment or landed cost made: %s",
			fw1Dollars(page.TotalCents), fw1Dollars(glCredit), shorter(page.TotalCents, glCredit), byDollars(variance), others)
	}
	return TieResult{Name: TieNameAPAging, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// tieAPOtherJournals names the posted journals on 2100, up to the month's end,
// that no vendor bill, PayBill payment or landed cost document posted: the
// entries the Controller reconciles against the aging. It names at most ten.
func tieAPOtherJournals(ctx context.Context, seams TieSeams, m TieMonth) (string, error) {
	rows, err := seams.Querier.Query(ctx, `
		SELECT j.entry_no::text, j.source, COALESCE(j.memo, ''),
		       SUM(COALESCE(l.credit_cents, 0) - COALESCE(l.debit_cents, 0))::bigint
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE a.code = '2100' AND j.status = 'POSTED' AND j.posted_at <= $1
		   AND j.idempotency_key NOT LIKE 'purchasing.bill:%'
		   AND j.idempotency_key NOT LIKE 'purchasing.bill.payment:%'
		   AND j.idempotency_key NOT LIKE 'landedcost.%'
		 GROUP BY j.id, j.entry_no, j.source, j.memo, j.posted_at
		 ORDER BY j.posted_at, j.id
		 LIMIT 11`, m.MonthEnd)
	if err != nil {
		return "", fmt.Errorf("the 2100 journals read: %w", err)
	}
	defer rows.Close()
	var named []string
	for rows.Next() {
		var entry, source, memo string
		var credit int64
		if err := rows.Scan(&entry, &source, &memo, &credit); err != nil {
			return "", fmt.Errorf("the 2100 journals scan: %w", err)
		}
		named = append(named, fmt.Sprintf("journal %s (%s, %q, %s)", entry, source, memo, fw1Dollars(credit)))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(named) == 0 {
		return "none", nil
	}
	if len(named) > 10 {
		named = append(named[:10], "and more")
	}
	return strings.Join(named, "; "), nil
}

// TieStockToGL1200: the stock on the shelves equals GL 1200.
//
// Both sides are read at the same instant, which is the rule FW1-09e settled:
// the stock ledger answers one figure, today's, so the ledger side is read as
// of the same moment. A tie that read one side at the month's end and the
// other now refuses every correct month whose next month had moved the stock.
//
// The stock figure is the walk's own read, moved here unchanged: the value
// of the stock that is on hand, over the branch costs the pricing module
// keeps. W2-5 gives the inventory module an as of month end read, and this
// tie is where that read lands.
func TieStockToGL1200(ctx context.Context, seams TieSeams, m TieMonth) (TieResult, error) {
	var stock int64
	if err := seams.Querier.QueryRow(ctx, `
		SELECT COALESCE(ROUND(SUM(sb.on_hand_base * c.avg_unit_cost_cents))::bigint, 0)
		  FROM stock_balances sb
		  JOIN product_branch_costs c ON c.product_id = sb.product_id AND c.branch_id = sb.branch_id
		 WHERE sb.on_hand_base != 0`).Scan(&stock); err != nil {
		return TieResult{}, fmt.Errorf("stock value: %w", err)
	}
	gl1200, _, err := seams.GLStore.AccountBalanceAsOf(ctx, "1200", seams.now(), "")
	if err != nil {
		return TieResult{}, fmt.Errorf("the GL 1200 read: %w", err)
	}
	variance := stock - gl1200
	detail := fmt.Sprintf("stock value %s ties to GL 1200 %s (both read now, until the stock ledger answers an as of month end read)",
		fw1Dollars(stock), fw1Dollars(gl1200))
	if variance != 0 {
		detail = fmt.Sprintf("stock value %s does not tie to GL 1200 %s (both read now): the ledger is %s the shelves by %s",
			fw1Dollars(stock), fw1Dollars(gl1200), shorter(stock, gl1200), byDollars(variance))
	}
	return TieResult{Name: TieNameStock, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// TieHSTMovementToDocuments: GL 2200's movement in the month equals the
// month's own documents' HST, sales and credit memos less the vendor bills'
// input tax credits.
//
// Both sides are the month's: the ledger side is the month's own movement
// (the fiscal period's posted journals), the documents side is the month's
// own documents by their own date. An opening balance document is not one of
// them: its tax never reaches GL 2200, because its receivable is an opening
// posting against 3000 (the seed's opening_balance marker, FROM-PRINCIPAL
// 177 and 180).
//
// The tax engine's own reconcile report rides beside it: the movement tie is
// a posting completeness check (it proves every document's tax reached GL
// 2200), and the report compares the ledger's payable against the collected
// tax on the engine's own decisions. Both have to be at zero, and the
// variance reported is the first of the two that is not.
func TieHSTMovementToDocuments(ctx context.Context, seams TieSeams, m TieMonth) (TieResult, error) {
	var gl2200Move, docSalesHST, billITC int64
	if err := seams.Querier.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.credit_cents, 0) - COALESCE(l.debit_cents, 0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE a.code = '2200' AND j.status = 'POSTED' AND fp.year = $1 AND fp.month = $2`,
		m.Year, m.Month).Scan(&gl2200Move); err != nil {
		return TieResult{}, fmt.Errorf("the GL 2200 month movement read: %w", err)
	}
	if err := seams.Querier.QueryRow(ctx, `
		SELECT COALESCE(SUM(tax_cents), 0)
		  FROM invoices
		 WHERE status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
		   AND issue_date >= $1::date AND issue_date <= $2::date
		   AND (metadata->>'opening_balance') IS DISTINCT FROM 'true'
		   AND NOT (id::text = ANY($3))`,
		m.MonthStartDate(), m.MonthEndDate(), excludedIDs(m.ExcludeInvoiceIDs)).Scan(&docSalesHST); err != nil {
		return TieResult{}, fmt.Errorf("the month documents HST read: %w", err)
	}
	if err := seams.Querier.QueryRow(ctx, `
		SELECT COALESCE(SUM(tax_cents), 0)
		  FROM purchasing_bills
		 WHERE status = 'POSTED'
		   AND bill_date >= $1::date AND bill_date <= $2::date`,
		m.MonthStartDate(), m.MonthEndDate()).Scan(&billITC); err != nil {
		return TieResult{}, fmt.Errorf("the vendor bill ITC read: %w", err)
	}
	var posReturnHST int64
	monthStart := time.Date(m.Year, time.Month(m.Month), 1, 0, 0, 0, 0, m.MonthEnd.Location())
	nextMonthStart := monthStart.AddDate(0, 1, 0)
	if err := seams.Querier.QueryRow(ctx, `
		SELECT COALESCE(SUM(tax_cents), 0)
		  FROM pos_returns
		 WHERE status = 'COMMITTED'
		   AND created_at >= $1
		   AND created_at < $2`,
		monthStart, nextMonthStart).Scan(&posReturnHST); err != nil {
		return TieResult{}, fmt.Errorf("the POS return HST read: %w", err)
	}
	netDocHST := docSalesHST - posReturnHST - billITC
	variance := gl2200Move - netDocHST
	detail := fmt.Sprintf("GL 2200 month movement %s ties to the month's documents HST %s (sales and credit memos HST %s less vendor bill ITC %s)",
		fw1Dollars(gl2200Move), fw1Dollars(netDocHST), fw1Dollars(docSalesHST), fw1Dollars(billITC))
	if variance != 0 {
		detail = fmt.Sprintf("GL 2200 month movement %s does not tie to the month's documents HST %s (sales and credit memos HST %s less vendor bill ITC %s): GL 2200 is %s by %s",
			fw1Dollars(gl2200Move), fw1Dollars(netDocHST), fw1Dollars(docSalesHST), fw1Dollars(billITC),
			shorter(gl2200Move, netDocHST), byDollars(variance))
	}
	// The engine's own reconcile report, wired over the owning modules'
	// repositories exactly as the tops wiring wires it. An unwired seam
	// refuses the reconcile rather than answering a silent zero.
	rep, err := seams.Tax.ReconcileReport(ctx, m.Year, m.Month, "")
	if err != nil {
		return TieResult{}, fmt.Errorf("the HST reconcile: %w", err)
	}
	if rep.VarianceCents != 0 || !rep.TiedOut {
		if variance == 0 {
			variance = rep.VarianceCents
		}
		detail = fmt.Sprintf("the HST reconcile does not tie for %04d-%02d: GL 2200 payable %s against %s collected on tax decisions, a variance of %s (%s)",
			m.Year, m.Month, fw1Dollars(rep.GLPayableCents), fw1Dollars(rep.CollectedCents), fw1Dollars(rep.VarianceCents),
			shorter(rep.GLPayableCents, rep.CollectedCents))
	}
	return TieResult{Name: TieNameHST, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// TieBankReconciliationAtZero: the month's bank reconciliation ties at zero.
//
// The rule is the textbook one: the statement balance plus the deposits in
// transit less the outstanding cheques equals the ledger's own bank balance
// at the month's end. The reconciliation's own stored variance has to be at
// zero as well, so a reconciliation the module computed at zero cannot hide
// a ledger that moved under it.
//
// A month that carries no reconciliation does not apply: a running month
// cannot sign off, and the open month is running.
func TieBankReconciliationAtZero(ctx context.Context, seams TieSeams, m TieMonth) (TieResult, error) {
	if m.BankAccountID == "" {
		return TieResult{}, fmt.Errorf("the bank tie: no bank account named for %04d-%02d", m.Year, m.Month)
	}
	var statementBalance, recVariance int64
	var status string
	err := seams.Querier.QueryRow(ctx, `
		SELECT statement_balance_cents, variance_cents, status
		  FROM bank_reconciliations
		 WHERE bank_account_id = $1::uuid AND year = $2 AND month = $3`,
		m.BankAccountID, m.Year, m.Month).Scan(&statementBalance, &recVariance, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TieResult{
				Name: TieNameBank, Pass: true, Applicable: false, VarianceCents: 0,
				Detail: fmt.Sprintf("the month %04d-%02d carries no bank reconciliation yet (a running month cannot sign off)", m.Year, m.Month),
			}, nil
		}
		return TieResult{}, fmt.Errorf("the bank reconciliation read: %w", err)
	}
	depositInTransit, err := TieDepositsInTransit(ctx, seams, m)
	if err != nil {
		return TieResult{}, err
	}
	outstandingCheques, err := TieOutstandingCheques(ctx, seams, m)
	if err != nil {
		return TieResult{}, err
	}
	gl1000, _, err := seams.GLStore.AccountBalanceAsOf(ctx, "1000", m.MonthEnd, "")
	if err != nil {
		return TieResult{}, fmt.Errorf("the GL 1000 read: %w", err)
	}
	adjustedBank := statementBalance + depositInTransit - outstandingCheques
	variance := adjustedBank - gl1000
	if recVariance != 0 && variance == 0 {
		variance = recVariance
	}
	detail := fmt.Sprintf("the bank month %04d-%02d ties at zero: statement %s plus %s deposit in transit less %s outstanding cheques = %s against GL 1000 at the month's end %s (the reconciliation is %s)",
		m.Year, m.Month, fw1Dollars(statementBalance), fw1Dollars(depositInTransit),
		fw1Dollars(outstandingCheques), fw1Dollars(adjustedBank), fw1Dollars(gl1000), status)
	if variance != 0 {
		detail = fmt.Sprintf("the bank month %04d-%02d does not tie at zero: statement %s plus %s deposit in transit less %s outstanding cheques = %s against GL 1000 at the month's end %s (the ledger side is %s by %s; the reconciliation reports variance %s)",
			m.Year, m.Month, fw1Dollars(statementBalance), fw1Dollars(depositInTransit),
			fw1Dollars(outstandingCheques), fw1Dollars(adjustedBank), fw1Dollars(gl1000),
			shorter(gl1000, adjustedBank), byDollars(gl1000-adjustedBank), fw1Dollars(recVariance))
	}
	return TieResult{Name: TieNameBank, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// TieDepositsInTransit reads the lodgements that are in the ledger and not
// yet on a statement: the deposits in transit at the month's end.
func TieDepositsInTransit(ctx context.Context, seams TieSeams, m TieMonth) (int64, error) {
	var total int64
	err := seams.Querier.QueryRow(ctx, `
		SELECT COALESCE(SUM(tm.amount_cents), 0)
		  FROM till_movements tm
		 WHERE tm.movement_type = 'LODGEMENT'
		   AND ($2::timestamptz IS NULL OR tm.created_at <= $2)
		   AND NOT (tm.id::text = ANY($1))
		   AND NOT EXISTS (
		       SELECT 1 FROM bank_matches bm
		         LEFT JOIN bank_statement_lines bsl ON bsl.id = bm.statement_line_id
		        WHERE bm.target_type = 'LODGEMENT'
		          AND bm.target_ref_id = tm.id
		          AND bm.unmatched_at IS NULL
		          AND ($3::text = '' OR bsl.id IS NULL OR bsl.txn_date <= $3::date)
		   )`, excludedIDs(m.ExcludeLodgementIDs), m.MonthEnd, m.MonthEndDate()).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("the deposits in transit read: %w", err)
	}
	return total, nil
}

// TieOutstandingCheques reads the credits the ledger holds on the bank
// account that no bank match has explained: the outstanding cheques.
func TieOutstandingCheques(ctx context.Context, seams TieSeams, months ...TieMonth) (int64, error) {
	var total int64
	if len(months) > 0 && !months[0].MonthEnd.IsZero() {
		m := months[0]
		err := seams.Querier.QueryRow(ctx, `
			SELECT COALESCE(SUM(l.credit_cents), 0)
			  FROM gl_journal_lines l
			  JOIN gl_journals j ON j.id = l.journal_id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE a.code = '1000'
			   AND j.status = 'POSTED'
			   AND j.posted_at <= $1
			   AND ($2::text = '' OR (j.posted_at AT TIME ZONE 'America/Toronto')::date > COALESCE((SELECT cutover_date FROM bank_accounts WHERE id = $2::uuid), '0001-01-01'::date))
			   AND COALESCE(l.credit_cents, 0) > 0
			   AND COALESCE(j.idempotency_key, '') NOT LIKE 'payment.bounced:%'
			   AND NOT EXISTS (
			       SELECT 1 FROM bank_matches bm
			         LEFT JOIN bank_statement_lines bsl ON bsl.id = bm.statement_line_id
			        WHERE (bm.journal_id = j.id OR (bm.target_type = 'JOURNAL' AND bm.target_ref_id = j.id))
			          AND bm.unmatched_at IS NULL
			          AND (bsl.id IS NULL OR bsl.txn_date <= $3::date)
			   )`, m.MonthEnd, m.BankAccountID, m.MonthEndDate()).Scan(&total)
		if err != nil {
			return 0, fmt.Errorf("the outstanding cheques read: %w", err)
		}
		return total, nil
	}
	err := seams.Querier.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.credit_cents), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE a.code = '1000'
		   AND j.status = 'POSTED'
		   AND COALESCE(l.credit_cents, 0) > 0
		   AND COALESCE(j.idempotency_key, '') NOT LIKE 'payment.bounced:%'
		   AND NOT EXISTS (
		       SELECT 1 FROM bank_matches bm
		        WHERE (bm.journal_id = j.id OR (bm.target_type = 'JOURNAL' AND bm.target_ref_id = j.id))
		          AND bm.unmatched_at IS NULL
		   )`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("the outstanding cheques read: %w", err)
	}
	return total, nil
}

// TieStatementsToAging: every customer's statement closing balance, printed
// line walk, aging and customer_transactions subledger equal the customer's
// own documents and receipts, read independently of all four.
//
// The statement closing balances are read from the month's own statements in
// the database, so the tie reads what the run actually left behind rather
// than what the run said in memory.
func TieStatementsToAging(ctx context.Context, seams TieSeams, m TieMonth) (TieResult, error) {
	rows, err := billing.NewCreditService(seams.Billing).Aging(fw1StaffCtx(ctx), m.MonthEnd, "")
	if err != nil {
		return TieResult{}, fmt.Errorf("the aging read: %w", err)
	}
	closings, err := tieStatementClosings(ctx, seams, m)
	if err != nil {
		return TieResult{}, err
	}
	if len(rows) == 0 && len(closings) == 0 {
		return TieResult{
			Name: TieNameStatements, Pass: true, Applicable: false, VarianceCents: 0,
			Detail: fmt.Sprintf("the month %04d-%02d carries no AR to tie", m.Year, m.Month),
		}, nil
	}
	agingByCust := map[string]int64{}
	custSet := map[string]bool{}
	for _, r := range rows {
		agingByCust[r.CustomerID] = r.TotalCents
		custSet[r.CustomerID] = true
	}
	for cid := range closings {
		custSet[cid] = true
	}
	unionRows, err := seams.Querier.Query(ctx, `
		SELECT DISTINCT customer_id::text FROM customer_transactions
		UNION
		SELECT DISTINCT customer_id::text FROM invoices WHERE status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents != 0`)
	if err != nil {
		return TieResult{}, fmt.Errorf("the customer union read: %w", err)
	}
	for unionRows.Next() {
		var cid string
		if err := unionRows.Scan(&cid); err != nil {
			unionRows.Close()
			return TieResult{}, err
		}
		custSet[cid] = true
	}
	unionRows.Close()
	if err := unionRows.Err(); err != nil {
		return TieResult{}, fmt.Errorf("the customer union read: %w", err)
	}

	type custEntry struct {
		id     string
		acctNo string
	}
	var custs []custEntry
	for cid := range custSet {
		var acctNo string
		if err := seams.Querier.QueryRow(ctx, `SELECT account_no FROM customers WHERE id = $1`, cid).Scan(&acctNo); err != nil {
			return TieResult{}, fmt.Errorf("customer account_no for %s: %w", cid, err)
		}
		custs = append(custs, custEntry{id: cid, acctNo: acctNo})
	}
	sort.Slice(custs, func(i, j int) bool { return custs[i].acctNo < custs[j].acctNo })

	var variance int64
	var failures []string
	for _, c := range custs {
		var subledger int64
		if err := seams.Querier.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_cents), 0)
			  FROM customer_transactions
			 WHERE customer_id = $1 AND occurred_at <= $2`, c.id, m.MonthEnd).Scan(&subledger); err != nil {
			return TieResult{}, fmt.Errorf("the customer_transactions subledger read for %s: %w", c.acctNo, err)
		}
		// Independent net receivable from the customer's documents (invoices and credit memos)
		// and receipts (including bounced cheque NSF fee records in payment_nsf net of live
		// payments.metadata->'nsf_fee_allocations' settlements per FW1-06a).
		isCurrent := !seams.now().After(m.MonthEnd)
		var docsAndReceipts int64
		if err := seams.Querier.QueryRow(ctx, `
			SELECT COALESCE((
			    SELECT SUM(CASE WHEN $3::bool THEN i.balance_cents
			                    ELSE i.total_cents
			                         + COALESCE((SELECT SUM(t.amount_cents) FROM customer_transactions t
			                                      WHERE t.source_type = 'invoice' AND t.source_ref_id = i.id
			                                        AND t.txn_type <> i.doc_type AND t.created_at <= $2), 0)
			                         - COALESCE((SELECT SUM(CASE WHEN p.reverses_payment_id IS NULL THEN a.amount_cents ELSE -a.amount_cents END)
			                                      FROM payment_allocations a JOIN payments p ON p.id = a.payment_id
			                                     WHERE a.invoice_id = i.id AND a.created_at <= $2), 0)
			                         - COALESCE((SELECT SUM(ra.amount_cents) FROM payment_reapplications ra
			                                      WHERE ra.invoice_id = i.id AND ra.created_at <= $2), 0)
			               END)
			      FROM invoices i
			     WHERE i.customer_id = $1
			       AND i.status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
			       AND ($3::bool OR COALESCE(i.finalized_at, i.created_at) <= $2)
			), 0) + COALESCE((
			    SELECT SUM(
			        pn.fee_cents - COALESCE((
			            SELECT SUM((p2.metadata->'nsf_fee_allocations'->>pn.id::text)::bigint)
			              FROM payments p2
			             WHERE p2.customer_id = pn.customer_id
			               AND p2.reversed_at IS NULL
			               AND p2.reverses_payment_id IS NULL
			               AND p2.status <> 'BOUNCED'
			               AND ($3::bool OR p2.created_at <= $2)
			               AND (p2.metadata->'nsf_fee_allocations'->>pn.id::text) IS NOT NULL
			        ), 0)
			    )
			      FROM payment_nsf pn
			     WHERE pn.customer_id = $1
			       AND ($3::bool OR pn.created_at <= $2)
			), 0)`, c.id, m.MonthEnd, isCurrent).Scan(&docsAndReceipts); err != nil {
			return TieResult{}, fmt.Errorf("the independent documents and receipts read for %s: %w", c.acctNo, err)
		}
		lines, err := seams.Billing.StatementLines(ctx, c.id, m.MonthEnd, billing.AllBranches)
		if err != nil {
			return TieResult{}, fmt.Errorf("the statement lines for %s: %w", c.acctNo, err)
		}
		var walked int64
		if len(lines) > 0 {
			walked = lines[len(lines)-1].BalanceRun
		}
		stmt := closings[c.id]
		aging := agingByCust[c.id]
		farther := abs64(stmt - docsAndReceipts)
		if d := abs64(walked - docsAndReceipts); d > farther {
			farther = d
		}
		if d := abs64(aging - docsAndReceipts); d > farther {
			farther = d
		}
		if d := abs64(subledger - docsAndReceipts); d > farther {
			farther = d
		}
		if farther != 0 {
			variance += farther
			failures = append(failures, fmt.Sprintf(
				"%s: statement closing %s, printed line walk %s, aging %s, customer_transactions subledger %s do not tie to independent documents and receipts %s (aging vs subledger %s; statement closing vs printed walk %s; subledger vs documents %s)",
				c.acctNo, fw1Dollars(stmt), fw1Dollars(walked), fw1Dollars(aging), fw1Dollars(subledger), fw1Dollars(docsAndReceipts),
				byDollars(aging-subledger), byDollars(stmt-walked), byDollars(subledger-docsAndReceipts)))
		}
	}
	detail := fmt.Sprintf("every customer's statement closing, printed line walk, aging and subledger tie to their own documents and receipts across %d account(s)", len(custs))
	if len(failures) > 0 {
		detail = strings.Join(failures, "; ")
	}
	return TieResult{Name: TieNameStatements, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// tieStatementClosings reads the month's statement closing balances by
// customer id: the statements of the latest run whose as of date is the
// month's end. The date is the wall clock day the writer stamped, read in
// the same zone the caller handed the run, so the read finds the run the
// month made and never a neighbour's.
func tieStatementClosings(ctx context.Context, seams TieSeams, m TieMonth) (map[string]int64, error) {
	rows, err := seams.Querier.Query(ctx, `
		SELECT DISTINCT ON (s.customer_id) s.customer_id::text, s.closing_balance_cents
		  FROM ar_statements s
		  JOIN ar_statement_runs r ON r.id = s.run_id
		 WHERE r.as_of_date = $1::date
		 ORDER BY s.customer_id, r.created_at DESC`, m.MonthEnd.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("the month statements read: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var cid string
		var closing int64
		if err := rows.Scan(&cid, &closing); err != nil {
			return nil, err
		}
		out[cid] = closing
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("the month statements read: %w", err)
	}
	return out, nil
}

// TieBranchTrialBalancesToConsolidated: the branch trial balances sum to the
// consolidated one.
//
// The consolidated read and the branch reads are the GL's own trial balance,
// over the wired service, so the figure is the one the Controller's
// statements read.
func TieBranchTrialBalancesToConsolidated(ctx context.Context, seams TieSeams, m TieMonth) (TieResult, error) {
	cons, err := seams.GL.TrialBalance(fw1StaffCtx(ctx), m.Year, m.Month, "")
	if err != nil {
		return TieResult{}, fmt.Errorf("the consolidated trial balance: %w", err)
	}
	var sumDebit, sumCredit int64
	for _, b := range seams.Branches {
		tb, err := seams.GL.TrialBalance(fw1StaffCtx(ctx), m.Year, m.Month, b)
		if err != nil {
			return TieResult{}, fmt.Errorf("the %s trial balance: %w", b, err)
		}
		sumDebit += tb.TotalDebit
		sumCredit += tb.TotalCredit
	}
	variance := sumDebit - cons.TotalDebit
	if v := sumCredit - cons.TotalCredit; v != 0 && variance == 0 {
		variance = v
	}
	detail := fmt.Sprintf("the branch trial balances sum to %s debit and %s credit against the consolidated %s debit and %s credit",
		fw1Dollars(sumDebit), fw1Dollars(sumCredit), fw1Dollars(cons.TotalDebit), fw1Dollars(cons.TotalCredit))
	if variance != 0 {
		detail = fmt.Sprintf("the branch trial balances sum to %s debit and %s credit against the consolidated %s debit and %s credit (the branches are %s the whole by %s)",
			fw1Dollars(sumDebit), fw1Dollars(sumCredit), fw1Dollars(cons.TotalDebit), fw1Dollars(cons.TotalCredit),
			shorter(sumDebit, cons.TotalDebit), byDollars(sumDebit-cons.TotalDebit))
	}
	return TieResult{Name: TieNameBranchTotals, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// TieOutboxDrained: no posting event is waiting and none is dead lettered.
// The outbox is the whole book's, so this tie reads no month: it answers for
// the box.
func TieOutboxDrained(ctx context.Context, seams TieSeams, _ TieMonth) (TieResult, error) {
	var pending, dead int
	if err := seams.Querier.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE status = 'PENDING'`).Scan(&pending); err != nil {
		return TieResult{}, fmt.Errorf("the waiting read: %w", err)
	}
	if err := seams.Querier.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE status = 'DEAD'`).Scan(&dead); err != nil {
		return TieResult{}, fmt.Errorf("the dead letter read: %w", err)
	}
	detail := "no journal event is waiting and none is dead lettered"
	if pending != 0 || dead != 0 {
		var topics []string
		rows, err := seams.Querier.Query(ctx, `SELECT DISTINCT topic FROM events_outbox WHERE status IN ('PENDING','DEAD')`)
		if err == nil {
			for rows.Next() {
				var topic string
				if err := rows.Scan(&topic); err == nil {
					topics = append(topics, topic)
				}
			}
			rows.Close()
		}
		detail = fmt.Sprintf("%d journal events are waiting and %d are dead lettered after the consumer's pass (topics: %s)",
			pending, dead, strings.Join(topics, ", "))
	}
	variance := int64(pending + dead)
	return TieResult{Name: TieNameOutbox, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// TieClosedYearIncomeStatementUnchanged: the closed year's income statement
// is the one the close left behind.
//
// The caller that ran the close has the reading it took before the close and
// hands it over (IncomeBefore). The caller that reads a year closed long ago
// has no such reading, and the tie then compares the income statement
// against the net the close swept to retained earnings: the close swept the
// year's profit and loss and left the statement holding it, which is what
// the before reading held.
func TieClosedYearIncomeStatementUnchanged(ctx context.Context, seams TieSeams, m TieMonth) (TieResult, error) {
	after, net, err := tieIncomeStatement(ctx, seams, m.Year)
	if err != nil {
		return TieResult{}, err
	}
	if len(after) == 0 {
		return TieResult{}, fmt.Errorf("the closed year %d carries no income statement: a year end close of a year with no profit and loss posts no closing journal and seals no period", m.Year)
	}
	var variance int64
	var failures []string
	if m.IncomeBefore != nil {
		for code, amt := range m.IncomeBefore {
			if after[code] != amt {
				variance += abs64(after[code] - amt)
				failures = append(failures, fmt.Sprintf("the year end close changed the closed year's income statement: account %s held %s before the close and %s after (%s)",
					code, fw1Dollars(amt), fw1Dollars(after[code]), byDollars(after[code]-amt)))
			}
		}
	} else {
		swept, err := tieSweptNet(ctx, seams, m.Year)
		if err != nil {
			return TieResult{}, err
		}
		variance = abs64(net - swept)
		if variance != 0 {
			failures = append(failures, fmt.Sprintf("the closed year %d's income statement holds a net of %s against the %s the close swept to retained earnings (%s)",
				m.Year, fw1Dollars(net), fw1Dollars(swept), byDollars(net-swept)))
		}
	}
	detail := fmt.Sprintf("the closed year %d's income statement is the one the close left behind", m.Year)
	if len(failures) > 0 {
		detail = strings.Join(failures, "; ")
	}
	return TieResult{Name: TieNameIncomeClosed, Pass: variance == 0, Applicable: true, VarianceCents: variance, Detail: detail}, nil
}

// tieIncomeStatement reads the closed year's income statement, the same view
// the walk reads: the statement views exclude the closing journals, so the
// statement survives the close.
func tieIncomeStatement(ctx context.Context, seams TieSeams, year int) (map[string]int64, int64, error) {
	out := map[string]int64{}
	var net int64
	rows, err := seams.Querier.Query(ctx, `
		SELECT account_code, account_type, SUM(amount_cents)::bigint
		  FROM v_income_statement
		 WHERE fiscal_year = $1
		 GROUP BY account_code, account_type`, year)
	if err != nil {
		return nil, 0, fmt.Errorf("the income statement read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code, acctType string
		var amt int64
		if err := rows.Scan(&code, &acctType, &amt); err != nil {
			return nil, 0, err
		}
		out[code] = amt
		if acctType == "REVENUE" {
			net += amt
		} else {
			net -= amt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("the income statement read: %w", err)
	}
	return out, net, nil
}

// tieSweptNet reads the net the year's close swept to retained earnings: the
// closing journal's own retained earnings leg.
func tieSweptNet(ctx context.Context, seams TieSeams, year int) (int64, error) {
	var retained int64
	err := seams.Querier.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.status = 'POSTED' AND j.source = 'CLOSING' AND a.code = '3900' AND fp.year = $1`, year).Scan(&retained)
	if err != nil {
		return 0, fmt.Errorf("the closing journal read: %w", err)
	}
	return -retained, nil
}

// -- the small helpers the ties and the walk share ---------------------------------------------

// fw1Dollars writes an integer cent figure as dollars.
func fw1Dollars(c int64) string { return fmt.Sprintf("$%.2f", float64(c)/100) }

// fw1StaffCtx is the global staff context the GL's close and tie reads stamp their actor from.
func fw1StaffCtx(ctx context.Context) context.Context {
	return staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID: "d7000000-0000-4000-9000-000000000006", Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})
}

// shorter says which of two figures is the smaller one, in words.
func shorter(a, b int64) string {
	if a < b {
		return "short"
	}
	return "over"
}

// byDollars writes a variance as a signed dollar amount.
func byDollars(c int64) string {
	if c < 0 {
		return "-" + fw1Dollars(-c)
	}
	return fw1Dollars(c)
}

// abs64 is the absolute value of an integer cent figure.
func abs64(c int64) int64 {
	if c < 0 {
		return -c
	}
	return c
}

// excludedIDs is the caller's exclusion list as an array the SQL can read. A
// nil slice rides as NULL, and NOT (x = ANY(NULL)) is not true, so the rows
// would vanish from the sum instead of being counted.
func excludedIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
