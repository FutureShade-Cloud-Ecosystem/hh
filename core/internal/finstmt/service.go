package finstmt

// service.go, statement composition on top of the repository reads: revenue/expense subtotals + net
// income, the balance-sheet invariant (assets == liabilities + equity + net income) including the
// 2500/2510/2520 loan/lease liabilities, trial-balance tie, comparatives (incl. empty history),
// drill-down tie, AR aging roll-up, and finstmt_schedules validation. The pure helpers (mergeComparative,
// summarizeAging, validateScheduleInput, sumDetailNatural) are unit-tested with in-memory fakes; the DB
// path is exercised by the build-tagged integration tests.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// -- error taxonomy (mapped to HTTP by the handler) ---------------------------

var errNotFound = errors.New("finstmt: not found")

// validationError is a client-input error the handler maps to 400.
type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }

func badf(format string, a ...any) error { return validationError{msg: fmt.Sprintf(format, a...)} }

// -- DIB-391 branch fence -----------------------------------------------------
//
// Every statement route mounts behind requireManager, which admits a BRANCH-scoped Tier-2 manager
// (identity/rbac.go:92), and the handlers took branch_id straight off the query string, while the
// repository reads an EMPTY branch_id as ALL branches. A Trenton manager could therefore read Kingston's
// P&L by naming it, and the CONSOLIDATED books by naming nothing. Both are org-level finance facts.
//
// The rule is landedcost's (service.go:97-103), applied to all five surfaces and their CSV/XLSX exports
// (the exports run through these same service calls, so they are fenced by construction):
//
//   - a GLOBAL caller is unchanged, consolidated ("" = all branches) is a GLOBAL-scope capability;
//   - a non-GLOBAL caller who names NO branch is 403, not silently consolidated: they must name a branch
//     they can reach rather than enumerate the org;
//   - a non-GLOBAL caller who names a branch must pass CanAccessBranch.

var (
	errUnauthorized = authzError{status: http.StatusUnauthorized, msg: "finstmt: authentication required"}
	// errForbidden is the horizontal-access boundary: a manager who may not read THESE books.
	errForbidden = authzError{status: http.StatusForbidden, msg: "finstmt: caller cannot access this branch"}
)

// authzError carries its own HTTP status so the handler maps authn/authz without a second taxonomy.
type authzError struct {
	status int
	msg    string
}

func (e authzError) Error() string { return e.msg }

// authorizeBranch applies the fence above. It is the ONLY place the rule is written; every statement
// read calls it, so a new surface that forgets it is visible as a missing call, not as a subtle leak.
func (s *Service) authorizeBranch(ctx context.Context, branchID string) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if branchID == "" {
		if caller.Scope != identity.ScopeGlobal {
			return errForbidden // a scoped caller must name an accessible branch, not read the consolidated books
		}
		return nil
	}
	if !caller.CanAccessBranch(branchID) {
		return errForbidden
	}
	return nil
}

// authorizeConsolidated fences a read that has NO branch dimension at all (the trial balance is
// org-wide by construction): only a GLOBAL caller may run it.
func (s *Service) authorizeConsolidated(ctx context.Context) error {
	return s.authorizeBranch(ctx, "")
}

// -- composed statement reports -----------------------------------------------

// IncomeStatementReportFor builds the P&L with revenue/expense subtotals + net income.
func (s *Service) IncomeStatementReportFor(ctx context.Context, year, month int, branchID string) (IncomeStatementReport, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return IncomeStatementReport{}, err
	}
	rows, err := s.repo.IncomeStatement(ctx, year, month, branchID)
	if err != nil {
		return IncomeStatementReport{}, err
	}
	return composeIncome(year, month, branchID, rows), nil
}

// composeIncome splits P&L rows into revenue/expense sections + net income (pure; unit-tested).
func composeIncome(year, month int, branchID string, rows []StatementRow) IncomeStatementReport {
	rep := IncomeStatementReport{Year: year, Month: month, BranchID: branchID}
	for _, r := range rows {
		switch r.AccountType {
		case "REVENUE":
			rep.Revenue = append(rep.Revenue, r)
			rep.RevenueCents += r.AmountCents
		case "EXPENSE":
			rep.Expenses = append(rep.Expenses, r)
			rep.ExpenseCents += r.AmountCents
		}
	}
	rep.NetIncomeCents = rep.RevenueCents - rep.ExpenseCents
	return rep
}

// NetIncomeCents is the full-fiscal-year net income used by the balance-sheet invariant.
func (s *Service) NetIncomeCents(ctx context.Context, year int, branchID string) (int64, error) {
	rep, err := s.IncomeStatementReportFor(ctx, year, 0, branchID)
	if err != nil {
		return 0, err
	}
	return rep.NetIncomeCents, nil
}

// BalanceSheetReportFor groups the three sections and asserts the balance invariant, including the
// 2500 RBC LOC / 2510 Equipment Loans / 2520 Lease Obligations liabilities. The identity holds because
// every POSTED journal balances: total debits == total credits ⇒ assets == liabilities + equity + net income.
func (s *Service) BalanceSheetReportFor(ctx context.Context, year int, branchID string) (BalanceSheetReport, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return BalanceSheetReport{}, err
	}
	rows, err := s.repo.BalanceSheet(ctx, year, branchID)
	if err != nil {
		return BalanceSheetReport{}, err
	}
	ni, err := s.NetIncomeCents(ctx, year, branchID)
	if err != nil {
		return BalanceSheetReport{}, err
	}
	return composeBalance(year, branchID, rows, ni), nil
}

// composeBalance groups ASSET/LIABILITY/EQUITY rows (incl. 2500/2510/2520 loans/leases) and asserts the
// balance invariant assets == liabilities + equity + net income (pure; unit-tested).
func composeBalance(year int, branchID string, rows []StatementRow, netIncomeCents int64) BalanceSheetReport {
	// Non-nil empty sections so the JSON serializes as [] not null (the FE maps over each, T1.3/B3).
	rep := BalanceSheetReport{
		Year: year, BranchID: branchID, NetIncomeCents: netIncomeCents,
		Assets:      []StatementRow{},
		Liabilities: []StatementRow{},
		Equity:      []StatementRow{},
	}
	for _, r := range rows {
		switch r.AccountType {
		case "ASSET":
			rep.Assets = append(rep.Assets, r)
			rep.TotalAssetsCents += r.AmountCents
		case "LIABILITY":
			rep.Liabilities = append(rep.Liabilities, r)
			rep.TotalLiabilitiesCents += r.AmountCents
		case "EQUITY":
			rep.Equity = append(rep.Equity, r)
			rep.TotalEquityCents += r.AmountCents
		}
	}
	rep.Balanced = rep.TotalAssetsCents == rep.TotalLiabilitiesCents+rep.TotalEquityCents+rep.NetIncomeCents
	return rep
}

// TrialBalanceReportFor returns the full TB with the double-entry tie (Σdebit == Σcredit).
func (s *Service) TrialBalanceReportFor(ctx context.Context, year int) (TrialBalanceReport, error) {
	// No branch dimension: the trial balance IS the consolidated ledger, so it is GLOBAL-only.
	if err := s.authorizeConsolidated(ctx); err != nil {
		return TrialBalanceReport{}, err
	}
	rows, err := s.repo.TrialBalanceFull(ctx, year)
	if err != nil {
		return TrialBalanceReport{}, err
	}
	rep := TrialBalanceReport{Year: year, Rows: rows}
	for _, r := range rows {
		rep.TotalDebitCents += r.TotalDebitCents
		rep.TotalCreditCents += r.TotalCreditCents
	}
	rep.InBalance = rep.TotalDebitCents == rep.TotalCreditCents
	return rep, nil
}

// GLDetail returns the drill-down rows for a filter.
func (s *Service) GLDetail(ctx context.Context, f GLDetailFilter) ([]GLDetailRow, error) {
	if err := s.authorizeBranch(ctx, f.BranchID); err != nil {
		return nil, err
	}
	return s.repo.GLDetail(ctx, f)
}

// -- comparatives -------------------------------------------------------------

// IncomeStatementComparison returns current vs prior-fiscal-year P&L rows. When prior history is empty,
// every current row is still returned with PriorCents == 0 (the empty-comparative case, AC4).
func (s *Service) IncomeStatementComparison(ctx context.Context, year, month int, branchID string) ([]ComparativeRow, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return nil, err
	}
	cur, err := s.repo.IncomeStatement(ctx, year, month, branchID)
	if err != nil {
		return nil, err
	}
	prior, err := s.repo.IncomeStatement(ctx, year-1, month, branchID)
	if err != nil {
		return nil, err
	}
	return mergeComparative(cur, prior), nil
}

// BalanceSheetComparison returns current vs prior-fiscal-year balance-sheet rows (empty prior ⇒ 0s).
func (s *Service) BalanceSheetComparison(ctx context.Context, year int, branchID string) ([]ComparativeRow, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return nil, err
	}
	cur, err := s.repo.BalanceSheet(ctx, year, branchID)
	if err != nil {
		return nil, err
	}
	prior, err := s.repo.BalanceSheet(ctx, year-1, branchID)
	if err != nil {
		return nil, err
	}
	return mergeComparative(cur, prior), nil
}

// mergeComparative joins two statement slices by account code. Accounts present in either period appear
// once; a period that lacks the account contributes 0 (so an empty prior year yields prior=0, never a
// dropped or duplicated row). Deterministic order: by account code.
func mergeComparative(current, prior []StatementRow) []ComparativeRow {
	idx := map[string]*ComparativeRow{}
	order := []string{}
	get := func(r StatementRow) *ComparativeRow {
		c, ok := idx[r.AccountCode]
		if !ok {
			c = &ComparativeRow{AccountCode: r.AccountCode, AccountName: r.AccountName, AccountType: r.AccountType}
			idx[r.AccountCode] = c
			order = append(order, r.AccountCode)
		}
		return c
	}
	for _, r := range current {
		get(r).CurrentCents += r.AmountCents
	}
	for _, r := range prior {
		get(r).PriorCents += r.AmountCents
	}
	sort.Strings(order)
	out := make([]ComparativeRow, 0, len(order))
	for _, code := range order {
		c := idx[code]
		c.DeltaCents = c.CurrentCents - c.PriorCents
		out = append(out, *c)
	}
	return out
}

// -- drill-down tie -----------------------------------------------------------

// sumDetailNatural sums GL detail rows into a single natural-orientation amount for an account type:
// REVENUE/LIABILITY/EQUITY are credit-normal (Σcredit − Σdebit); ASSET/EXPENSE are debit-normal. A
// statement figure ties EXACTLY to this over the detail rows behind its account.
func sumDetailNatural(rows []GLDetailRow, accountType string) int64 {
	var debit, credit int64
	for _, r := range rows {
		debit += r.DebitCents
		credit += r.CreditCents
	}
	switch accountType {
	case "REVENUE", "LIABILITY", "EQUITY":
		return credit - debit
	default: // ASSET, EXPENSE
		return debit - credit
	}
}

// DrillDown returns the GL detail behind a statement line and the tied natural-orientation amount.
func (s *Service) DrillDown(ctx context.Context, year, month int, accountCode, accountType, branchID string) ([]GLDetailRow, int64, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return nil, 0, err
	}
	rows, err := s.repo.GLDetail(ctx, GLDetailFilter{Year: year, Month: month, AccountCode: accountCode, BranchID: branchID})
	if err != nil {
		return nil, 0, err
	}
	return rows, sumDetailNatural(rows, accountType), nil
}

// -- AR aging roll-up ---------------------------------------------------------

// AgingSummaryFor returns the bucketed AR aging whose grand total ties to billing's ARBalanceCents.
func (s *Service) AgingSummaryFor(ctx context.Context, branchID string) (AgingSummary, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return AgingSummary{}, err
	}
	rows, err := s.repo.ARAging(ctx, branchID)
	if err != nil {
		return AgingSummary{}, err
	}
	return summarizeAging(branchID, rows), nil
}

// summarizeAging buckets the rows in the canonical band order (including the non-invoice AR rows, which
// carry the same customer_id/branch_id shape) and totals them. GrandTotalCents == Σ all row amounts,
// which ties to ARBalanceCents by construction (same two UNION sources).
func summarizeAging(branchID string, rows []AgingRow) AgingSummary {
	byBucket := map[string]int64{}
	var grand int64
	for _, r := range rows {
		byBucket[r.Bucket] += r.AmountCents
		grand += r.AmountCents
	}
	buckets := make([]AgingBucket, 0, len(agingBucketOrder))
	for _, b := range agingBucketOrder {
		buckets = append(buckets, AgingBucket{Bucket: b, Cents: byBucket[b]})
	}
	return AgingSummary{BranchID: branchID, Buckets: buckets, Rows: rows, GrandTotalCents: grand}
}

// -- finstmt_schedules CRUD (registry-only at W5) -----------------------------

func (s *Service) ListSchedules(ctx context.Context) ([]Schedule, error) {
	return s.repo.ListSchedules(ctx)
}

func (s *Service) GetSchedule(ctx context.Context, id string) (Schedule, error) {
	return s.repo.GetSchedule(ctx, id)
}

func (s *Service) CreateSchedule(ctx context.Context, in ScheduleInput, createdBy string) (Schedule, error) {
	if err := validateScheduleInput(in); err != nil {
		return Schedule{}, err
	}
	return s.repo.CreateSchedule(ctx, normalizeScheduleInput(in), createdBy)
}

func (s *Service) UpdateSchedule(ctx context.Context, id string, in ScheduleInput) (Schedule, error) {
	// Reject a malformed (non-UUID) id before the repo call, otherwise it reaches `WHERE id = $1` on
	// the uuid PK and raises SQLSTATE 22P02 → opaque HTTP 500. A clean 400 mirrors pricing's path-uuid guard.
	if _, err := uuid.Parse(id); err != nil {
		return Schedule{}, badf("id must be a uuid")
	}
	if err := validateScheduleInput(in); err != nil {
		return Schedule{}, err
	}
	return s.repo.UpdateSchedule(ctx, id, normalizeScheduleInput(in))
}

func (s *Service) DeleteSchedule(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return badf("id must be a uuid")
	}
	return s.repo.DeleteSchedule(ctx, id)
}

func normalizeScheduleInput(in ScheduleInput) ScheduleInput {
	in.Report = strings.ToUpper(strings.TrimSpace(in.Report))
	in.Cadence = strings.ToUpper(strings.TrimSpace(in.Cadence))
	in.Recipients = normalizeRecipients(in.Recipients)
	in.BranchID = strings.TrimSpace(in.BranchID)
	return in
}

// validateScheduleInput enforces the 0034 CHECK sets in-app (so a bad payload is a 400, not a Postgres
// 23514) plus recipient e-mail validity, a scheduled report with an unparseable recipient is rejected.
func validateScheduleInput(in ScheduleInput) error {
	report := strings.ToUpper(strings.TrimSpace(in.Report))
	if !validReports[report] {
		return badf("report must be one of INCOME_STATEMENT, BALANCE_SHEET, TRIAL_BALANCE, GL_DETAIL, AR_AGING")
	}
	cadence := strings.ToUpper(strings.TrimSpace(in.Cadence))
	if !validCadences[cadence] {
		return badf("cadence must be one of DAILY, WEEKLY, MONTHLY")
	}
	recips := splitRecipients(in.Recipients)
	if len(recips) == 0 {
		return badf("at least one recipient e-mail is required")
	}
	for _, addr := range recips {
		if _, err := mail.ParseAddress(addr); err != nil {
			return badf("invalid recipient e-mail: %q", addr)
		}
	}
	return nil
}

func splitRecipients(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeRecipients(raw string) string { return strings.Join(splitRecipients(raw), ",") }

// -- audit stamping (best-effort export access trail) -------------------------

// recordExport stamps a report-export access on the A-21 trail when an auditor is wired. Best-effort:
// an audit failure never blocks delivering the (read-only) statement. Round 4 (S2's round 3 P1):
// audit_log.entity_id is a uuid column (0013), so the row carries NO entity (the empty id is what
// lands NULL in the column) and the report name and format ride the allowlisted Extra keys; the old
// report-name entity failed every real insert with SQLSTATE 22P02, so no export was ever audited. A
// failed audit still never blocks the export, but it is logged at Error with the report named, not
// dropped at Warn.
func (s *Service) recordExport(ctx context.Context, report, format, actorID, branchID string) {
	if s.audit == nil {
		return
	}
	err := s.audit.Record(ctx, audit.SensitiveAccess{
		Action:     "FINSTMT_EXPORT",
		EntityType: "finstmt.report",
		EntityID:   "",
		ActorID:    actorID,
		BranchID:   branchID,
		Purpose:    "STATEMENT_EXPORT",
		Extra:      map[string]any{"report": report, "format": format},
	})
	if err != nil {
		s.log.Error("finstmt: export audit record failed, the export is not on the trail", "report", report, "err", err)
	}
}
