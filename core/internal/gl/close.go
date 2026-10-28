package gl

// close.go, the trial balance, the AR subledger tie-out (with first-seen discrepancy age), the fiscal-period
// close / Controller-only reopen, the year-end close (net income → 3900 Retained Earnings), the GL policy read,
// and the GP-parity export (PARITY DRAFT). GLBalanceCents lives in posting.go (bankrec depends on it early).

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5"
)

// actGLPeriodReopen is the sensitive-access action stamped on a Controller reopen (the recorded compensating
// control, reopening a sealed period is a privileged, reason-bearing act).
const actGLPeriodReopen = "GL_PERIOD_REOPEN"

// actGLFiscalYearOpen is the sensitive-access action stamped on a Controller's early open of the next
// fiscal year (the roll forward otherwise happens on its own ahead of need; an early open is deliberate).
const actGLFiscalYearOpen = "GL_FISCAL_YEAR_OPEN"

// actGLHeadOfficeBranchSet is the sensitive-access action stamped when the Financial Controller
// changes the head office branch setting in gl_policy (ruling 82.3).
const actGLHeadOfficeBranchSet = "GL_HEAD_OFFICE_BRANCH_SET"

// actGLYearEndClose is the sensitive-access action stamped on the Controller's year end close (the
// close seals a whole year, so its trail is a recorded row, not only the override rows).
const actGLYearEndClose = "GL_YEAR_END_CLOSE"

// controllerRole is the only role permitted to reopen a closed period (the frozen route is RequireAdmin -
// Tier-1 GLOBAL, and the service narrows it to the Financial Controller specifically). It is also the
// only role permitted to open a fiscal year early and to seal a year (FW1-09c, FROM-PRINCIPAL 222).
const controllerRole = "FINANCIAL_CONTROLLER"

// - policy -

func (s *Service) Policy(ctx context.Context, branchID string) (Policy, error) {
	return s.store.Policy(ctx, branchID)
}

// HeadOfficeBranchID returns the configured head office branch from gl_policy (defaulting to Trenton, TRT).
func (s *Service) HeadOfficeBranchID(ctx context.Context) (string, error) {
	return s.store.HeadOfficeBranchID(ctx)
}

// SetHeadOfficeBranch updates the head office branch setting in gl_policy (ruling 82.3):
// narrowed to the Financial Controller, mandatory reason, and audited.
func (s *Service) SetHeadOfficeBranch(ctx context.Context, branchID, reason string) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if caller.RoleCode != controllerRole {
		return errNotController
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errReasonRequired
	}
	branchID = strings.TrimSpace(branchID)
	if branchID == "" {
		return badReq("gl: branch_id is required")
	}
	return s.store.SetHeadOfficeBranch(ctx, branchID, func(ctx context.Context, tx pgx.Tx) error {
		sa := audit.SensitiveAccess{
			Action:     actGLHeadOfficeBranchSet,
			EntityType: "gl.policy",
			EntityID:   branchID,
			ActorID:    caller.UserID,
			BranchID:   branchID,
			Reason:     reason,
		}
		if s.auditTx != nil && tx != nil {
			return s.auditTx.RecordTx(ctx, tx, sa)
		}
		if s.audit != nil {
			return s.audit.Record(ctx, sa)
		}
		return nil
	})
}

// - trial balance -

// TrialBalanceResult is the trial balance rows plus their grand totals (which tie to zero when every posted
// journal balances, the double-entry invariant surfaces here).
type TrialBalanceResult struct {
	FiscalYear   int               `json:"fiscal_year"`
	Month        int               `json:"month,omitempty"`
	BranchID     string            `json:"branch_id,omitempty"`
	Rows         []TrialBalanceRow `json:"rows"`
	TotalDebit   int64             `json:"total_debit_cents"`
	TotalCredit  int64             `json:"total_credit_cents"`
	BalanceCents int64             `json:"balance_cents"` // Σ net debit; 0 == in balance
}

// TrialBalance is fenced to the caller's branch reach (FW1-10), the same rule ListJournals carries
// (DIB-392): a named branch must be reachable, and the consolidated figure (no branch named) is the
// whole company's books, a GLOBAL caller's read. A branch-scoped caller is refused in plain words
// rather than silently served a figure over its own branches only: the branch-list filter the
// partial consolidation needs is a new mechanism, and the dealer wide statements (finstmt) already
// serve that rule.
func (s *Service) TrialBalance(ctx context.Context, year, month int, branchID string) (TrialBalanceResult, error) {
	if err := s.authorizeBranchRead(ctx, branchID); err != nil {
		return TrialBalanceResult{}, err
	}
	rows, err := s.store.TrialBalance(ctx, year, month, branchID)
	if err != nil {
		return TrialBalanceResult{}, err
	}
	res := TrialBalanceResult{FiscalYear: year, Month: month, BranchID: branchID, Rows: rows}
	for _, r := range rows {
		res.TotalDebit += r.DebitCents
		res.TotalCredit += r.CreditCents
		res.BalanceCents += r.BalanceCents
	}
	return res, nil
}

// - AR subledger tie-out -

// TieOut compares the AR subledger balance (billing.ARBalanceCents) against GL account 1100 as-of a date and
// persists the first-seen discrepancy age (gl_tieout_state), so the report carries out_of_tie_since + age_days
// across checks. branchID "" is consolidated. Fenced to the caller's branch reach (FW1-10) with the
// same rule as TrialBalance above.
func (s *Service) TieOut(ctx context.Context, asOf time.Time, branchID string) ([]TieoutState, error) {
	if err := s.authorizeBranchRead(ctx, branchID); err != nil {
		return nil, err
	}
	if asOf.IsZero() {
		asOf = s.clock()
	}
	var out []TieoutState
	if s.ar != nil {
		sub, err := s.ar.ARBalanceCents(ctx, asOf, branchID)
		if err != nil {
			return nil, err
		}
		// GL 1100 is debit-normal (asset). Both sides must share ONE point-in-time basis: the subledger
		// ARBalanceCents is inception-to-date, so the GL side is the inception-to-date net-debit through asOf
		// across ALL fiscal years, NOT just the current year's movement (that dropped every prior-year carry-
		// forward, so any AR open across a year boundary read as a false discrepancy).
		netDebit, normal, err := s.store.AccountBalanceAsOf(ctx, "1100", asOf, branchID)
		if err != nil {
			return nil, err
		}
		glBal := netDebit
		if normal == NormalCredit { // 1100 is debit-normal; kept general for correctness
			glBal = -netDebit
		}
		st, err := s.store.UpsertTieout(ctx, "AR", "1100", branchID, sub, glBal, s.clock())
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// - periods -

func (s *Service) ListPeriods(ctx context.Context) ([]Period, error) {
	return s.store.ListPeriods(ctx)
}

// NextOpenableFiscalYear answers the next fiscal year the Financial Controller can open early
// (house year + 2 when not yet open), or 0 when the cap year is already open (S2 round, P2-1).
func (s *Service) NextOpenableFiscalYear(ctx context.Context) (int, error) {
	return s.store.NextOpenableFiscalYear(ctx, s.clock())
}

// YearOpenResult answers which fiscal year an open made sure of.
type YearOpenResult struct {
	FiscalYear int `json:"fiscal_year"`
}

// EnsureNextFiscalYear idempotently makes sure the year after the current house year exists. The worker
// calls it at start on every box, so the next year exists ahead of need without anyone acting.
func (s *Service) EnsureNextFiscalYear(ctx context.Context) (YearOpenResult, error) {
	year, err := s.store.EnsureNextFiscalYear(ctx, s.clock())
	if err != nil {
		return YearOpenResult{}, err
	}
	return YearOpenResult{FiscalYear: year}, nil
}

// OpenNextFiscalYear is the Controller's early open when no year is named explicitly by the caller.
func (s *Service) OpenNextFiscalYear(ctx context.Context, reason string) (YearOpenResult, error) {
	return s.OpenFiscalYear(ctx, 0, reason)
}

// OpenFiscalYear is the Controller's early open: narrowed to the Financial Controller, reason required,
// capped at one year beyond the roll forward's target (house year + 2), refusing in words when the year
// is already open or past the cap, and audit stamped inside the year's transaction (S2 round, P2-1).
func (s *Service) OpenFiscalYear(ctx context.Context, requestedYear int, reason string) (YearOpenResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return YearOpenResult{}, err
	}
	if caller.RoleCode != controllerRole {
		return YearOpenResult{}, errNotControllerOpenYear
	}
	if strings.TrimSpace(reason) == "" {
		return YearOpenResult{}, errReasonRequired
	}
	// Round 2 (S2 P2-4): the audit row is recorded INSIDE the year's transaction, so it commits or
	// rolls back with the year it opens; a failed audit leaves no year behind. Unwired, nothing is
	// recorded (the guards above still hold). Round 4 (S2's round 3 P1): audit_log.entity_id is a
	// uuid column (0013), so the row is keyed to the opened year's January period, read on this
	// same transaction, and the year itself rides Extra; the old "fiscal_year:<n>" entity failed
	// every real insert with SQLSTATE 22P02 and rolled the whole open back.
	year, err := s.store.OpenNextFiscalYear(ctx, s.clock(), requestedYear, func(ctx context.Context, tx pgx.Tx, year int) error {
		if s.auditTx == nil {
			return nil
		}
		periodID, perr := s.store.FiscalPeriodIDTx(ctx, tx, year, 1)
		if perr != nil {
			return perr
		}
		return s.auditTx.RecordTx(ctx, tx, audit.SensitiveAccess{
			Action:     actGLFiscalYearOpen,
			EntityType: "gl.period",
			EntityID:   periodID,
			ActorID:    caller.UserID,
			Reason:     reason,
			Extra:      map[string]any{"fiscal_year": year},
		})
	})
	if err != nil {
		return YearOpenResult{}, err
	}
	return YearOpenResult{FiscalYear: year}, nil
}

// ClosePeriod seals a fiscal period (OPEN → CLOSED) and logs the override. A reason is optional on close (it
// is mandatory on reopen). Guarded by RequireManager at the route. Before it seals, the close runs the
// FW1-09b pre flight: AR in tie with 1100, stock in tie with 1200, nothing waiting or dead lettered for
// the month, no open till drawer. A failing check refuses the close and names each failing check with its
// figures in dollars (a month still trading does not seal under the trading).
func (s *Service) ClosePeriod(ctx context.Context, id, reason string) (Period, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Period{}, err
	}
	p, err := s.store.GetPeriod(ctx, id)
	if err != nil {
		return Period{}, err
	}
	// Round 5 (S3's P2): the fence runs BEFORE the pre flight, so a refused caller never reads the
	// company's figures in the refusal. A period is company wide, its close a dealer wide act.
	if err := s.authorizeBranchWrite(ctx, ""); err != nil {
		return Period{}, err
	}
	// The pre flight reads its instants before the close's own write, unlocked; a posting landing
	// between the two follows 612's late posted rule, which is acceptable and stated (round 4).
	if fails := s.runClosePreFlight(ctx, p.Year, p.Month); len(fails) > 0 {
		caveat := ""
		if s.stockValue != nil {
			caveat = " (the stock tie reads today's stock value)"
		}
		return Period{}, clientErr(409, "gl: period close pre flight failed%s: %s", caveat, strings.Join(fails, "; "))
	}
	if reason == "" {
		reason = "period close"
	}
	return s.store.ClosePeriod(ctx, id, caller.UserID, reason)
}

// ReopenPeriod reopens a sealed period. It is Financial-Controller-only (narrowed from the RequireAdmin route),
// requires a mandatory reason, logs the gl_period_overrides row, AND writes a sensitive-access audit record -
// the recorded compensating control for an unsealing (2FA is not available in identity; this pairing is it).
func (s *Service) ReopenPeriod(ctx context.Context, id, reason string) (Period, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Period{}, err
	}
	if caller.RoleCode != controllerRole {
		return Period{}, errNotController
	}
	// Round 3: reopen is a dealer wide act on top of the Controller rule.
	if err := s.authorizeBranchWrite(ctx, ""); err != nil {
		return Period{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return Period{}, errReasonRequired
	}
	p, err := s.store.ReopenPeriod(ctx, id, caller.UserID, reason)
	if err != nil {
		return Period{}, err
	}
	if s.audit != nil {
		if aerr := s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     actGLPeriodReopen,
			EntityType: "gl.period",
			EntityID:   id,
			ActorID:    caller.UserID,
			Reason:     reason,
		}); aerr != nil {
			return Period{}, fmt.Errorf("gl: audit reopen: %w", aerr)
		}
	}
	return p, nil
}

// - year-end close (net income → 3900 Retained Earnings) -

// YearCloseResult summarizes a year-end close.
type YearCloseResult struct {
	FiscalYear        int      `json:"fiscal_year"`
	ClosingJournalID  string   `json:"closing_journal_id,omitempty"`
	ClosingJournalIDs []string `json:"closing_journal_ids,omitempty"`
	NetIncomeCents    int64    `json:"net_income_cents"`
	PeriodsClosed     bool     `json:"periods_closed"`
}

// CloseYear posts the year-end closing entry, it flattens every REVENUE/EXPENSE account for the fiscal year
// and sweeps the net income (or loss) into 3900 Retained Earnings, then seals all of the year's OPEN periods.
// After the close the income statement nets to zero and the balance sheet balances with retained earnings
// carried into equity (AC13).
//
// FW1-09c (FROM-PRINCIPAL 222): the year end close is the Financial Controller's, the way the period
// reopen and the early open are. The route is RequireAdmin (a Tier-1 GLOBAL, the GM among them), so
// the service narrows it here, before any write, in words.
func (s *Service) CloseYear(ctx context.Context, year int, reason string) (YearCloseResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return YearCloseResult{}, err
	}
	if caller.RoleCode != controllerRole {
		return YearCloseResult{}, errNotControllerCloseYear
	}
	if reason == "" {
		reason = fmt.Sprintf("year-end close FY%d", year)
	}
	// FW1-08b and FW1-09b together: each branch's results close under its own stamp, the
	// company level P&L rows sweep under the head office branch (ruling 82.3), and the closing
	// entry is dated in the closed year (calendar December 31; the fiscal year start is stored
	// policy no period math follows, the FW1-01 report's finding). A book with no P&L at all
	// closes nothing.
	// Round 3: the year end close is a dealer wide act.
	if err := s.authorizeBranchWrite(ctx, ""); err != nil {
		return YearCloseResult{}, err
	}
	// Round 4 (S3's P1), round 5 form: the once only check, every branch's closing journal and the
	// periods' sealing run under ONE transaction holding a transaction scoped advisory lock on the
	// year, so two presses at once queue and the second answers the first run's result; a failure
	// mid close leaves nothing behind, never a half closed year.
	tx, err := s.store.BeginTx(ctx)
	if err != nil {
		return YearCloseResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('gl:year-end:' || $1::int))`, year); err != nil {
		return YearCloseResult{}, fmt.Errorf("gl: lock the year end: %w", err)
	}
	ids, net, err := s.store.ClosingJournalsForYearTx(ctx, tx, year)
	if err != nil {
		return YearCloseResult{}, err
	}
	if len(ids) > 0 {
		return YearCloseResult{FiscalYear: year, ClosingJournalIDs: ids, ClosingJournalID: firstID(ids), NetIncomeCents: net, PeriodsClosed: true}, nil
	}
	// FW1-09c round 2 (S2's P2, FROM-PRINCIPAL 320 and 321): the year's periods, counted, on THIS
	// transaction under the year end's lock. A year with NO periods at all has nothing to close
	// and refuses in words, never success (321); and the count comes before the CLOSED test, so
	// "every period of the year is already CLOSED" is never vacuously true on zero periods. The
	// count also reads the seal, which is the marker a year with NO P&L leaves: such a close
	// posts no closing journal, so the once only check above cannot see the first run (320).
	total, closed, err := s.store.YearPeriodsTx(ctx, tx, year)
	if err != nil {
		return YearCloseResult{}, err
	}
	if total == 0 {
		return YearCloseResult{}, clientErr(409, "gl: fiscal year %d has no periods to close", year)
	}
	// FW1-09b, the lead's Ask 2: the close is refused until the year's last day has passed in the
	// dealer's zone (a mid year press sealed the months still ahead and stopped all posting).
	ended, err := s.store.HouseYearEnded(ctx, year, s.now())
	if err != nil {
		return YearCloseResult{}, err
	}
	if !ended {
		return YearCloseResult{}, clientErr(409, "gl: fiscal year %d has not ended; the year end close is refused until the year's last day has passed in the dealer's zone", year)
	}
	// Round 4 (S3's P1): the year end runs the pre flight for every month it will seal, and refuses in
	// words naming the month. The pre flight reads its instants before the close's own write, unlocked; a
	// posting landing between the two follows 612's late posted rule, which is acceptable and stated.
	periods, err := s.store.ListPeriods(ctx)
	if err != nil {
		return YearCloseResult{}, err
	}
	for _, p := range periods {
		if p.Year != year || p.Status != "OPEN" {
			continue
		}
		if fails := s.runClosePreFlight(ctx, p.Year, p.Month); len(fails) > 0 {
			caveat := ""
			if s.stockValue != nil {
				caveat = " (the stock tie reads today's stock value)"
			}
			return YearCloseResult{}, clientErr(409, "gl: year end close pre flight failed for %04d-%02d%s: %s", p.Year, p.Month, caveat, strings.Join(fails, "; "))
		}
	}
	// FW1-08b's per branch input: the year's P&L per account PER BRANCH, the company level rows
	// ("" = no branch stamp) swept under the head office branch the policy names (ruling 82.3).
	plsByBranch, err := s.store.PLBalancesByBranch(ctx, year)
	if err != nil {
		return YearCloseResult{}, err
	}
	if len(plsByBranch) == 0 {
		pls, perr := s.store.PLBalances(ctx, year)
		if perr != nil {
			return YearCloseResult{}, perr
		}
		for _, r := range pls {
			plsByBranch = append(plsByBranch, PLBranchRow{
				AccountCode:   r.AccountCode,
				AccountName:   r.AccountName,
				AccountType:   r.AccountType,
				NormalBalance: r.NormalBalance,
				BranchID:      "",
				NetDebitCents: r.BalanceCents,
			})
		}
	}

	var headOffice string
	byBranch := map[string]map[string]int64{}
	for _, r := range plsByBranch {
		if r.NetDebitCents == 0 {
			continue
		}
		branchID := r.BranchID
		if branchID == "" {
			if headOffice == "" {
				ho, herr := s.store.HeadOfficeBranchID(ctx)
				if herr != nil {
					return YearCloseResult{}, herr
				}
				headOffice = ho
			}
			branchID = headOffice
		}
		if byBranch[branchID] == nil {
			byBranch[branchID] = map[string]int64{}
		}
		byBranch[branchID][r.AccountCode] += r.NetDebitCents
	}

	// FW1-09c round 2 (S2's P2, FROM-PRINCIPAL 320): the year end close is once only, and that
	// must hold for a year with NO P&L too. A book with no P&L at all closes nothing (the house's
	// own rule), so it posts no closing journal, and the once only check above cannot see the
	// first run: a second press passes every check (the year has ended, no OPEN period is left
	// to pre flight, no P&L to sweep) and records a SECOND GL_YEAR_END_CLOSE row, and two
	// Controllers pressing at once each record one. The year's own seal is the marker such a
	// close leaves: when every period of the year is already CLOSED and the year holds no P&L at
	// all, there is nothing left for the press to write, so it answers the first run's result and
	// records nothing. A year that DOES hold P&L is not answered here: a year whose months were
	// sealed one by one still needs its closing entry, and the closing journal's own once only
	// check above answers that year the second time round.
	if len(plsByBranch) == 0 && closed == total {
		return YearCloseResult{FiscalYear: year, PeriodsClosed: true}, nil
	}

	branchIDs := make([]string, 0, len(byBranch))
	for b := range byBranch {
		branchIDs = append(branchIDs, b)
	}
	sort.Slice(branchIDs, func(i, j int) bool {
		if branchIDs[i] == headOffice && branchIDs[j] != headOffice {
			return true
		}
		if branchIDs[j] == headOffice && branchIDs[i] != headOffice {
			return false
		}
		return branchIDs[i] < branchIDs[j]
	})

	res := YearCloseResult{FiscalYear: year}
	closingAt := time.Date(year, 12, 31, 12, 0, 0, 0, time.UTC)
	var totalND int64 // Σ net_debit across every branch's P&L = −(the year's net income)
	for _, branchID := range branchIDs {
		acctMap := byBranch[branchID]
		codes := make([]string, 0, len(acctMap))
		for c := range acctMap {
			codes = append(codes, c)
		}
		sort.Strings(codes)

		var lines []draftLine
		var branchND int64 // Σ net_debit across the branch's P&L = −(its net income)
		for _, code := range codes {
			nd := acctMap[code]
			if nd == 0 {
				continue
			}
			if nd > 0 {
				lines = append(lines, cr(code, nd, "close to retained earnings"))
			} else {
				lines = append(lines, dr(code, -nd, "close to retained earnings"))
			}
			branchND += nd
			totalND += nd
		}
		if len(lines) == 0 {
			continue
		}
		// The 3900 offset makes the closing entry balance: reversing lines net −branchND, so 3900 nets +branchND.
		if branchND > 0 {
			lines = append(lines, dr(RetainedEarningsCode, branchND, "net loss to retained earnings"))
		} else if branchND < 0 {
			lines = append(lines, cr(RetainedEarningsCode, -branchND, "net income to retained earnings"))
		}
		draft := journalDraft{
			Source:     SourceClosing,
			Memo:       fmt.Sprintf("Year-end close FY%d", year),
			Status:     StatusPosted,
			BranchID:   branchID,
			CreatedBy:  caller.UserID,
			OccurredAt: closingAt,
			Lines:      lines,
		}
		// Round 4 (S3's P1): the closing journal posts into the closed year's own December whatever its
		// status, through the dedicated insert that resolves the period explicitly, inside the year end's
		// one transaction. The late posted rule never touches it, and the branch stamp on the draft
		// stamps every line (FW1-08b: no closing line without a branch).
		id, err := s.store.InsertClosingJournalTx(ctx, tx, draft, year)
		if err != nil {
			return YearCloseResult{}, err
		}
		res.ClosingJournalIDs = append(res.ClosingJournalIDs, id)
	}
	res.NetIncomeCents = -totalND
	if len(res.ClosingJournalIDs) == 1 {
		res.ClosingJournalID = res.ClosingJournalIDs[0]
	}
	if err := s.store.ClosePeriodsForYearTx(ctx, tx, year, caller.UserID, reason); err != nil {
		return YearCloseResult{}, err
	}
	// FW1-09c (FROM-PRINCIPAL 222): the close's own audit row rides THIS transaction, so a
	// refused audit leaves no close behind, the open year's rule (S2's P2-4). The row is
	// keyed to the closed year's January period, read on this same transaction (audit_log.
	// entity_id is a uuid column, 0013), so the id it names is the one this close just
	// sealed; the year and the reason ride Extra. Unwired, nothing is recorded (the role
	// rule and the close itself still hold).
	if s.auditTx != nil {
		periodID, perr := s.store.FiscalPeriodIDTx(ctx, tx, year, 1)
		if perr != nil {
			return YearCloseResult{}, perr
		}
		if aerr := s.auditTx.RecordTx(ctx, tx, audit.SensitiveAccess{
			Action:     actGLYearEndClose,
			EntityType: "gl.period",
			EntityID:   periodID,
			ActorID:    caller.UserID,
			Reason:     reason,
			Extra:      map[string]any{"fiscal_year": year, "reason": reason},
		}); aerr != nil {
			return YearCloseResult{}, fmt.Errorf("gl: audit the year end close: %w", aerr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return YearCloseResult{}, fmt.Errorf("gl: commit the year end: %w", err)
	}
	res.PeriodsClosed = true
	return res, nil
}

// - GP-parity export -

// ExportResult is a GP-parity export: the rendered content (a PARITY DRAFT banner + CSV) plus the batch id.
type ExportResult struct {
	BatchID  string `json:"batch_id"`
	Format   string `json:"format"`
	RowCount int    `json:"row_count"`
	IsDraft  bool   `json:"is_draft"`
	Content  string `json:"content"`
}

const parityBanner = "PARITY DRAFT, GP-format parity export; extract artifacts absent, pending cutover confirmation (Q-006). Not a filing."

func (s *Service) Export(ctx context.Context, year, month int, branchID string) (ExportResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return ExportResult{}, err
	}
	if year <= 0 {
		return ExportResult{}, badReq("gl: export requires a fiscal_year")
	}
	rows, err := s.store.ExportRows(ctx, year, month, branchID)
	if err != nil {
		return ExportResult{}, err
	}
	content := renderGPParity(year, month, rows)
	batchID, err := s.store.InsertExportBatch(ctx, year, month, "GP_PARITY", caller.UserID, len(rows))
	if err != nil {
		return ExportResult{}, err
	}
	return ExportResult{BatchID: batchID, Format: "GP_PARITY", RowCount: len(rows), IsDraft: true, Content: content}, nil
}

// renderGPParity produces the deterministic GP-parity text: a PARITY DRAFT banner line, then a CSV with a
// header and one row per posted line. Money is rendered as signed dollars (GP convention) from integer cents.
func renderGPParity(year, month int, rows []ExportRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", parityBanner)
	fmt.Fprintf(&b, "# fiscal_year=%d month=%d rows=%d\n", year, month, len(rows))
	b.WriteString("entry_no,posted_at,account_code,account_name,debit,credit,memo\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%d,%s,%s,%s,%s,%s,%s\n",
			r.EntryNo, r.PostedAt, r.AccountCode, csvEscape(r.AccountName),
			dollars(r.DebitCents), dollars(r.CreditCents), csvEscape(r.Memo))
	}
	return b.String()
}

func dollars(cents int64) string {
	if cents == 0 {
		return ""
	}
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// firstID answers a slice's first id (the year end's singular result field
// stays populated only when exactly one closing journal exists).
func firstID(ids []string) string {
	if len(ids) == 1 {
		return ids[0]
	}
	return ""
}
