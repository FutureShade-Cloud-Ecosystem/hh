package finstmt

// repository.go, finstmt's read layer. It touches ONLY the 0034 views (v_income_statement,
// v_balance_sheet, v_trial_balance, v_gl_detail, v_gl_account_balances, v_ar_aging) + fiscal_periods,
// and the 0034 finstmt_schedules registry table. NO other module's tables, NO posting, NO writes to any
// GL table, a subtly-wrong view is fixed by the integrator's 0036, never here.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var errNoDB = errors.New("finstmt: database not configured")

// -- statements (POSTED-only, via the 0034 views) -----------------------------

// IncomeStatement returns natural-orientation P&L rows for a fiscal year/month/branch, summed across the
// per-account/month/branch view grain. month==0 = whole year; branchID=="" = consolidated (all branches).
func (r *Repository) IncomeStatement(ctx context.Context, year, month int, branchID string) ([]StatementRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT account_code, account_name, account_type, COALESCE(SUM(amount_cents), 0)
		   FROM v_income_statement
		  WHERE fiscal_year = $1
		    AND ($2 = 0 OR month = $2)
		    AND ($3 = '' OR branch_id = $3::uuid)
		  GROUP BY account_code, account_name, account_type
		  ORDER BY account_code`,
		year, month, branchID)
	if err != nil {
		return nil, fmt.Errorf("finstmt: income statement: %w", err)
	}
	return scanStatementRows(rows)
}

// BalanceSheet returns natural-orientation ASSET/LIABILITY/EQUITY rows for a fiscal year, summed across
// branches when consolidated. branchID=="" = consolidated.
func (r *Repository) BalanceSheet(ctx context.Context, year int, branchID string) ([]StatementRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT account_code, account_name, account_type, COALESCE(SUM(amount_cents), 0)
		   FROM v_balance_sheet
		  WHERE fiscal_year = $1
		    AND ($2 = '' OR branch_id = $2::uuid)
		  GROUP BY account_code, account_name, account_type
		  ORDER BY account_code`,
		year, branchID)
	if err != nil {
		return nil, fmt.Errorf("finstmt: balance sheet: %w", err)
	}
	return scanStatementRows(rows)
}

// TrialBalance returns the signed per-account balance (Σdebit − Σcredit) projected into StatementRow.
func (r *Repository) TrialBalance(ctx context.Context, year int) ([]StatementRow, error) {
	full, err := r.TrialBalanceFull(ctx, year)
	if err != nil {
		return nil, err
	}
	out := make([]StatementRow, 0, len(full))
	for _, t := range full {
		out = append(out, StatementRow{
			AccountCode: t.AccountCode, AccountName: t.AccountName,
			AccountType: t.AccountType, AmountCents: t.BalanceCents,
		})
	}
	return out, nil
}

// TrialBalanceFull returns the full debit/credit/balance columns for the report + export.
func (r *Repository) TrialBalanceFull(ctx context.Context, year int) ([]TrialBalanceRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT account_code, account_name, account_type, normal_balance,
		        total_debit_cents, total_credit_cents, balance_cents
		   FROM v_trial_balance
		  WHERE fiscal_year = $1
		  ORDER BY account_code`, year)
	if err != nil {
		return nil, fmt.Errorf("finstmt: trial balance: %w", err)
	}
	defer rows.Close()
	var out []TrialBalanceRow
	for rows.Next() {
		var t TrialBalanceRow
		if err := rows.Scan(&t.AccountCode, &t.AccountName, &t.AccountType, &t.NormalBalance,
			&t.TotalDebitCents, &t.TotalCreditCents, &t.BalanceCents); err != nil {
			return nil, fmt.Errorf("finstmt: scan trial balance: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GLDetail returns POSTED journal lines from v_gl_detail, filtered for the drill-down.
func (r *Repository) GLDetail(ctx context.Context, f GLDetailFilter) ([]GLDetailRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT line_id::text, journal_id::text, entry_no, source, period_year, period_month,
		        account_code, account_name, account_type,
		        COALESCE(branch_id::text, ''), COALESCE(debit_cents, 0), COALESCE(credit_cents, 0),
		        COALESCE(memo, ''), posted_at
		   FROM v_gl_detail
		  WHERE ($1 = 0  OR period_year  = $1)
		    AND ($2 = 0  OR period_month = $2)
		    AND ($3 = '' OR account_code = $3)
		    AND ($4 = '' OR branch_id    = $4::uuid)
		  ORDER BY posted_at NULLS LAST, entry_no, line_id`,
		f.Year, f.Month, f.AccountCode, f.BranchID)
	if err != nil {
		return nil, fmt.Errorf("finstmt: gl detail: %w", err)
	}
	defer rows.Close()
	var out []GLDetailRow
	for rows.Next() {
		var d GLDetailRow
		var postedAt *time.Time
		if err := rows.Scan(&d.LineID, &d.JournalID, &d.EntryNo, &d.Source, &d.PeriodYear, &d.PeriodMonth,
			&d.AccountCode, &d.AccountName, &d.AccountType, &d.BranchID,
			&d.DebitCents, &d.CreditCents, &d.Memo, &postedAt); err != nil {
			return nil, fmt.Errorf("finstmt: scan gl detail: %w", err)
		}
		if postedAt != nil {
			d.PostedAt = postedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ARAging returns the AR aging rows (invoice + non-invoice), whose grand total ties to ARBalanceCents.
func (r *Repository) ARAging(ctx context.Context, branchID string) ([]AgingRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT customer_id::text, COALESCE(customer_name, ''), source_type, source_ref_id::text,
		        COALESCE(branch_id::text, ''), amount_cents, age_days, bucket
		   FROM v_ar_aging
		  WHERE ($1 = '' OR branch_id = $1::uuid)
		  ORDER BY customer_id, age_days DESC`, branchID)
	if err != nil {
		return nil, fmt.Errorf("finstmt: ar aging: %w", err)
	}
	defer rows.Close()
	var out []AgingRow
	for rows.Next() {
		var a AgingRow
		if err := rows.Scan(&a.CustomerID, &a.CustomerName, &a.SourceType, &a.SourceRefID,
			&a.BranchID, &a.AmountCents, &a.AgeDays, &a.Bucket); err != nil {
			return nil, fmt.Errorf("finstmt: scan aging: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanStatementRows(rows pgx.Rows) ([]StatementRow, error) {
	defer rows.Close()
	var out []StatementRow
	for rows.Next() {
		var s StatementRow
		if err := rows.Scan(&s.AccountCode, &s.AccountName, &s.AccountType, &s.AmountCents); err != nil {
			return nil, fmt.Errorf("finstmt: scan statement row: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// -- finstmt_schedules registry CRUD ------------------------------------------

const scheduleCols = `id::text, report, recipients, cadence, COALESCE(branch_id::text, ''), is_active`

func scanSchedule(row pgx.Row) (Schedule, error) {
	var s Schedule
	err := row.Scan(&s.ID, &s.Report, &s.Recipients, &s.Cadence, &s.BranchID, &s.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, errNotFound
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("finstmt: scan schedule: %w", err)
	}
	return s, nil
}

func (r *Repository) ListSchedules(ctx context.Context) ([]Schedule, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+scheduleCols+` FROM finstmt_schedules ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("finstmt: list schedules: %w", err)
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) GetSchedule(ctx context.Context, id string) (Schedule, error) {
	if r.pool == nil {
		return Schedule{}, errNoDB
	}
	return scanSchedule(r.pool.QueryRow(ctx, `SELECT `+scheduleCols+` FROM finstmt_schedules WHERE id = $1`, id))
}

func (r *Repository) CreateSchedule(ctx context.Context, in ScheduleInput, createdBy string) (Schedule, error) {
	if r.pool == nil {
		return Schedule{}, errNoDB
	}
	return scanSchedule(r.pool.QueryRow(ctx,
		`INSERT INTO finstmt_schedules (report, recipients, cadence, branch_id, is_active, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+scheduleCols,
		in.Report, in.Recipients, in.Cadence, nullUUID(in.BranchID), in.IsActive, nullUUID(createdBy)))
}

func (r *Repository) UpdateSchedule(ctx context.Context, id string, in ScheduleInput) (Schedule, error) {
	if r.pool == nil {
		return Schedule{}, errNoDB
	}
	return scanSchedule(r.pool.QueryRow(ctx,
		`UPDATE finstmt_schedules
		    SET report = $2, recipients = $3, cadence = $4, branch_id = $5, is_active = $6, updated_at = now()
		  WHERE id = $1
		  RETURNING `+scheduleCols,
		id, in.Report, in.Recipients, in.Cadence, nullUUID(in.BranchID), in.IsActive))
}

func (r *Repository) DeleteSchedule(ctx context.Context, id string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM finstmt_schedules WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("finstmt: delete schedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
