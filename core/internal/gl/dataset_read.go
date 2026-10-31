package gl

// dataset_read.go, the report writer's GL data set (W3-RW1): the POSTED journal lines, one leg per
// row, read through this module's own books for the reporting module's curated data sets.
//
// -- ONE BASIS, THE TRIAL BALANCE'S ------------------------------------------------------------------
// The rows are the view's basis, line for line: POSTED journals only, filed into their fiscal period
// (the FW1-01 rule, so a late posted journal lands where the books say it landed), the branch taken
// from the line with the journal header as fallback, the account addressed by code and name. Because
// the basis is the same one v_gl_account_balances aggregates, a dataset read grouped by account ties
// to TrialBalance for the same period to the cent, and the reporting module's named integration test
// pins exactly that.
//
// -- THE FENCE IS THE CALLER'S OWN REACH --------------------------------------------------------------
// The read answers the stamped caller's reach: a GLOBAL caller reads every branch, a branch scoped
// caller reads its granted branches (a scoped caller with no grants reads nothing, fail closed). The
// consolidated trial balance stays GLOBAL-only under the FW1-10 rule; this read is not a consolidated
// statement, it is a list cut by branch, the partial consolidation shape the dealer wide statements
// already serve. No caller argument names a branch here: the reach comes from the caller the guard
// stamped, so a consumer of this read cannot forget to pass it.

import (
	"context"
	"fmt"
	"sort"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// DatasetGLLine is one leg of one POSTED journal, projected for the curated data set. FiscalYear and
// FiscalMonth name the period the figure is filed into.
type DatasetGLLine struct {
	AccountCode string
	AccountName string
	FiscalYear  int
	FiscalMonth int
	BranchID    string
	EntryNo     int
	JournalMemo string
	DebitCents  int64
	CreditCents int64
}

// DatasetJournalLines reads the set, one fiscal year at a time, one month or the whole year, inside
// the stamped caller's reach.
func (s *Service) DatasetJournalLines(ctx context.Context, year, month int) ([]DatasetGLLine, error) {
	if year <= 0 {
		return nil, clientErr(400, "gl: the journal lines read needs a whole fiscal year, got %d", year)
	}
	if month < 0 || month > 12 {
		return nil, clientErr(400, "gl: the journal lines read needs a month between 1 and 12, or none for the whole year, got %d", month)
	}
	c, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	var reach any // nil binds SQL NULL, the every-branch arm
	if c.Scope != identity.ScopeGlobal {
		ids := make([]string, 0, len(c.Branches))
		for id, granted := range c.Branches {
			if granted {
				ids = append(ids, id)
			}
		}
		// Sorted so the SQL parameter, and therefore the query plan, does not depend on Go's randomised
		// map iteration order. Empty binds as the empty array: = ANY('{}') matches nothing, so a scoped
		// caller with no grants reads nothing rather than everything.
		sort.Strings(ids)
		reach = ids
	}
	if s.repo == nil {
		return nil, errNoDB
	}
	return s.repo.DatasetJournalLines(ctx, year, month, reach)
}

// DatasetJournalLines is the store read: POSTED legs of one fiscal year (one month when month > 0),
// branch cut by the reach argument (nil = every branch, a possibly-empty slice = exactly those).
func (r *Repository) DatasetJournalLines(ctx context.Context, year, month int, reach any) ([]DatasetGLLine, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.code::text, a.name, fp.year, fp.month,
		       COALESCE(l.branch_id, j.branch_id)::text, j.entry_no, j.memo,
		       COALESCE(l.debit_cents, 0), COALESCE(l.credit_cents, 0)
		  FROM gl_journals j
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED'
		   AND fp.year = $1
		   AND ($2 <= 0 OR fp.month = $2)
		   AND ($3::uuid[] IS NULL OR COALESCE(l.branch_id, j.branch_id) = ANY($3::uuid[]))
		 ORDER BY fp.year, fp.month, j.entry_no, l.line_no`, year, month, reach)
	if err != nil {
		return nil, fmt.Errorf("gl: dataset journal lines: %w", err)
	}
	defer rows.Close()
	out := make([]DatasetGLLine, 0)
	for rows.Next() {
		var l DatasetGLLine
		if err := rows.Scan(&l.AccountCode, &l.AccountName, &l.FiscalYear, &l.FiscalMonth,
			&l.BranchID, &l.EntryNo, &l.JournalMemo, &l.DebitCents, &l.CreditCents); err != nil {
			return nil, fmt.Errorf("gl: scan dataset journal line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
