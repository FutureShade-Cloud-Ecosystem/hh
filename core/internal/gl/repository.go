package gl

// repository.go, the GL pgx access layer; the real glStore. It reads/writes ONLY GL-owned tables (the 0007
// block: gl_accounts / gl_journals / gl_journal_lines / posting_rules / fiscal_periods; plus the 0032
// additions: gl_policy / gl_period_overrides / gl_recurring_templates / gl_export_batches / gl_tieout_state)
// and the 0034 balance/detail views. A nil pool is tolerated (scaffold/offline mode) → errNoDB.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isCheckViolation catches the deferred balance-trigger rejection (0021) at POST time.
func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23514" || pgErr.Code == "P0001")
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// - policy -

func (r *Repository) Policy(ctx context.Context, branchID string) (Policy, error) {
	if r.pool == nil {
		return Policy{}, errNoDB
	}
	// A branch row overrides the org-default (branch_id IS NULL). Prefer the branch row when present.
	q := `SELECT COALESCE(branch_id::text,''), je_approval_threshold_cents, fiscal_year_start_month,
	             cost_basis_strategy, exclude_special_buys, notes
	      FROM gl_policy
	      WHERE branch_id IS NULL OR ($1 <> '' AND branch_id = $1::uuid)
	      ORDER BY (branch_id IS NOT NULL) DESC
	      LIMIT 1`
	var p Policy
	err := r.pool.QueryRow(ctx, q, branchID).Scan(&p.BranchID, &p.JEApprovalThresholdCents,
		&p.FiscalYearStartMonth, &p.CostBasisStrategy, &p.ExcludeSpecialBuys, &p.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		// No policy seeded, conservative default: everything routes to approval.
		return Policy{JEApprovalThresholdCents: 0, FiscalYearStartMonth: 1, CostBasisStrategy: "WEIGHTED_AVG"}, nil
	}
	if err != nil {
		return Policy{}, fmt.Errorf("gl: policy: %w", err)
	}
	return p, nil
}

// - posting rules -

func (r *Repository) PostingAccounts(ctx context.Context, eventType, branchID string) (map[string]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT line_role, account_code::text, (branch_id IS NOT NULL) AS is_branch
		 FROM posting_rules
		 WHERE event_type = $1 AND is_active
		   AND (branch_id IS NULL OR ($2 <> '' AND branch_id = $2::uuid))
		 ORDER BY is_branch`, eventType, branchID) // NULL (default) first, branch override last
	if err != nil {
		return nil, fmt.Errorf("gl: posting accounts %q: %w", eventType, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var role, code string
		var isBranch bool
		if err := rows.Scan(&role, &code, &isBranch); err != nil {
			return nil, fmt.Errorf("gl: scan posting rule: %w", err)
		}
		out[role] = code // branch rows come last → override the default
	}
	return out, rows.Err()
}

func (r *Repository) ListActiveRules(ctx context.Context) ([]RuleRef, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT pr.event_type, pr.line_role, pr.account_code::text,
		        (a.id IS NOT NULL AND a.is_active) AS ok
		 FROM posting_rules pr
		 LEFT JOIN gl_accounts a ON a.code = pr.account_code
		 WHERE pr.is_active
		 ORDER BY pr.event_type, pr.line_role`)
	if err != nil {
		return nil, fmt.Errorf("gl: list active rules: %w", err)
	}
	defer rows.Close()
	var out []RuleRef
	for rows.Next() {
		var rr RuleRef
		if err := rows.Scan(&rr.EventType, &rr.LineRole, &rr.AccountCode, &rr.AccountOK); err != nil {
			return nil, fmt.Errorf("gl: scan rule ref: %w", err)
		}
		out = append(out, rr)
	}
	return out, rows.Err()
}

// - chart of accounts -

const accountCols = `id::text, code::text, name, account_type, normal_balance, COALESCE(parent_id::text,''), is_active`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Code, &a.Name, &a.AccountType, &a.NormalBalance, &a.ParentID, &a.IsActive)
	return a, err
}

func (r *Repository) ListAccounts(ctx context.Context) ([]Account, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+accountCols+` FROM gl_accounts ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("gl: list accounts: %w", err)
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("gl: scan account: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repository) AccountByCode(ctx context.Context, code string) (Account, bool, error) {
	if r.pool == nil {
		return Account{}, false, errNoDB
	}
	a, err := scanAccount(r.pool.QueryRow(ctx, `SELECT `+accountCols+` FROM gl_accounts WHERE code = $1`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, fmt.Errorf("gl: account by code %q: %w", code, err)
	}
	return a, true, nil
}

func (r *Repository) InsertAccount(ctx context.Context, in CreateAccountInput) (Account, error) {
	if r.pool == nil {
		return Account{}, errNoDB
	}
	a, err := scanAccount(r.pool.QueryRow(ctx,
		`INSERT INTO gl_accounts (code, name, account_type, normal_balance, parent_id)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+accountCols,
		in.Code, in.Name, in.AccountType, in.NormalBalance, nullStr(in.ParentID)))
	if isUniqueViolation(err) {
		return Account{}, clientErr(409, "gl: account code %q already exists", in.Code)
	}
	if err != nil {
		return Account{}, fmt.Errorf("gl: insert account: %w", err)
	}
	return a, nil
}

func (r *Repository) DraftLineCountForAccountCode(ctx context.Context, code string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM gl_journal_lines l
		 JOIN gl_journals j ON j.id = l.journal_id
		 JOIN gl_accounts a ON a.id = l.account_id
		 WHERE a.code = $1 AND j.status = 'DRAFT'`, code).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("gl: draft line count for %q: %w", code, err)
	}
	return n, nil
}

func (r *Repository) SetAccountActive(ctx context.Context, code string, active bool) (Account, error) {
	if r.pool == nil {
		return Account{}, errNoDB
	}
	a, err := scanAccount(r.pool.QueryRow(ctx,
		`UPDATE gl_accounts SET is_active = $2, updated_at = now() WHERE code = $1 RETURNING `+accountCols, code, active))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, errNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("gl: set account active: %w", err)
	}
	return a, nil
}

// - journals (the shared persist path) -

// InsertJournal persists a journalDraft in ONE transaction: it resolves the fiscal period from OccurredAt
// (a CLOSED period is rejected, nothing may post into a sealed period), resolves every line's account_code
// to an active account_id, allocates the next entry_no for the fiscal year, and writes the header + lines.
// For a POSTED draft the deferred balance trigger (0021) asserts Σdebit = Σcredit at COMMIT. Idempotent on
// IdempotencyKey (gl_journals partial-unique): a replay returns the existing id with created=false.
func (r *Repository) InsertJournal(ctx context.Context, d journalDraft) (string, bool, error) {
	if r.pool == nil {
		return "", false, errNoDB
	}
	if d.IdempotencyKey != "" {
		if id, found, err := r.journalIDByKey(ctx, d.IdempotencyKey); err != nil {
			return "", false, err
		} else if found {
			return id, false, nil
		}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("gl: begin insert journal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// THE HOUSE ZONE, not the instant's own. `OccurredAt` defaults to the service clock, so before
	// this the month a posting landed in was decided by the server's timezone, and on a UTC server
	// that files a 9pm Ontario sale into tomorrow, and a 9pm sale on the 31st into next month's
	// books. See businessday.go for the full argument and for the three places this product had
	// already answered the same question the same way.
	y, m := fiscalYearMonth(d.OccurredAt)
	var periodID, pstatus string
	err = tx.QueryRow(ctx, `SELECT id::text, status FROM fiscal_periods WHERE year = $1 AND month = $2`, y, m).Scan(&periodID, &pstatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, clientErr(422, "gl: no fiscal period for %04d-%02d", y, m)
	}
	if err != nil {
		return "", false, fmt.Errorf("gl: resolve period: %w", err)
	}
	if pstatus == "CLOSED" {
		return "", false, errClosedPeriod
	}

	// Resolve account codes → active account ids.
	lineAcctIDs := make([]string, len(d.Lines))
	for i, ln := range d.Lines {
		var id string
		var active bool
		err := tx.QueryRow(ctx, `SELECT id::text, is_active FROM gl_accounts WHERE code = $1`, ln.AccountCode).Scan(&id, &active)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, unprocessable("gl: unknown account code %q", ln.AccountCode)
		}
		if err != nil {
			return "", false, fmt.Errorf("gl: resolve line account: %w", err)
		}
		if !active {
			return "", false, unprocessable("gl: account %q is inactive; cannot post", ln.AccountCode)
		}
		lineAcctIDs[i] = id
	}

	var entryNo int
	// SWARM-W4-C DIB-418: mint through next_entry_no(fiscal_year) (migration 0119) instead of
	// COALESCE(MAX(entry_no),0)+1. The old expression was a read-modify-write under UNIQUE(fiscal_year,
	// entry_no) with no lock between read and write, so two concurrent posting transactions read the same
	// MAX and the loser died on the unique constraint, an opaque 500 on every automated money path
	// (outbox consumer, landed cost, bankrec, close). next_entry_no locks the year's counter row FOR UPDATE
	// before reading it, so a second minter waits instead of colliding; the bump lives in THIS transaction,
	// so a rollback returns the number rather than burning it (numbering stays gapless, unlike a sequence).
	if err := tx.QueryRow(ctx, `SELECT next_entry_no($1)`, y).Scan(&entryNo); err != nil {
		return "", false, fmt.Errorf("gl: next entry_no: %w", err)
	}

	var postedAt any
	if d.Status == StatusPosted {
		postedAt = d.OccurredAt
	}
	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO gl_journals
		   (fiscal_year, entry_no, period_id, source, source_ref_id, memo, status, posted_at, branch_id,
		    created_by, idempotency_key, approved_by, reverses_journal_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 RETURNING id::text`,
		y, entryNo, periodID, d.Source, nullStr(d.SourceRefID), d.Memo, d.Status, postedAt, nullStr(d.BranchID),
		nullStr(d.CreatedBy), d.IdempotencyKey, nullStr(d.ApprovedBy), nullStr(d.ReversesJournalID)).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) && d.IdempotencyKey != "" {
			// A concurrent insert won the idempotency race, return the winner.
			if existing, found, ferr := r.journalIDByKey(ctx, d.IdempotencyKey); ferr == nil && found {
				return existing, false, nil
			}
		}
		return "", false, fmt.Errorf("gl: insert journal header: %w", err)
	}

	for i, ln := range d.Lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, branch_id, memo)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, i+1, lineAcctIDs[i], nullCents(ln.DebitCents), nullCents(ln.CreditCents), nullStr(d.BranchID), ln.Memo); err != nil {
			return "", false, fmt.Errorf("gl: insert journal line: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		if isCheckViolation(err) {
			return "", false, errUnbalanced
		}
		return "", false, fmt.Errorf("gl: commit journal: %w", err)
	}
	return id, true, nil
}

func nullCents(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (r *Repository) journalIDByKey(ctx context.Context, key string) (string, bool, error) {
	var id string
	err := r.pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1 AND idempotency_key <> ''`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("gl: journal by key: %w", err)
	}
	return id, true, nil
}

const journalCols = `id::text, fiscal_year, entry_no, period_id::text, source, COALESCE(source_ref_id::text,''),
	memo, status, posted_at, COALESCE(branch_id::text,''), COALESCE(approved_by::text,''),
	COALESCE(reverses_journal_id::text,''), idempotency_key, COALESCE(created_by::text,''), created_at`

func scanJournal(row pgx.Row) (Journal, error) {
	var j Journal
	err := row.Scan(&j.ID, &j.FiscalYear, &j.EntryNo, &j.PeriodID, &j.Source, &j.SourceRefID, &j.Memo,
		&j.Status, &j.PostedAt, &j.BranchID, &j.ApprovedBy, &j.ReversesJournalID, &j.IdempotencyKey,
		&j.CreatedBy, &j.CreatedAt)
	return j, err
}

func (r *Repository) journalLines(ctx context.Context, journalID string) ([]JournalLine, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT l.line_no, a.code::text, a.name, COALESCE(l.debit_cents,0), COALESCE(l.credit_cents,0), l.memo
		 FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		 WHERE l.journal_id = $1 ORDER BY l.line_no`, journalID)
	if err != nil {
		return nil, fmt.Errorf("gl: journal lines: %w", err)
	}
	defer rows.Close()
	var out []JournalLine
	for rows.Next() {
		var l JournalLine
		if err := rows.Scan(&l.LineNo, &l.AccountCode, &l.AccountName, &l.DebitCents, &l.CreditCents, &l.Memo); err != nil {
			return nil, fmt.Errorf("gl: scan journal line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) GetJournal(ctx context.Context, id string) (Journal, error) {
	if r.pool == nil {
		return Journal{}, errNoDB
	}
	j, err := scanJournal(r.pool.QueryRow(ctx, `SELECT `+journalCols+` FROM gl_journals WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Journal{}, errNotFound
	}
	if err != nil {
		return Journal{}, fmt.Errorf("gl: get journal: %w", err)
	}
	lines, err := r.journalLines(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	j.Lines = lines
	return j, nil
}

func (r *Repository) ListJournals(ctx context.Context, f JournalFilter) ([]Journal, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	// DIB-204: posted_at date-range predicate. posted_at is nullable (DRAFT journals have no post
	// date); a bounded date filter therefore only matches POSTED rows, which is the intended behaviour
	// (you filter the ledger by when entries hit the books). Empty bound = unbounded on that side.
	// $1..$3 are the shared status/branch/source filters; $4/$5 are the date bounds.
	// Cursor pagination (HH-3.2 B4): the offset family is retired; $6/$7
	// are the resume key (NULL on the first page, guarded by the IS NULL
	// leg so the row comparison only evaluates with a key present), $8 the
	// limit. No count query: the cursor envelope carries no total.
	const whereClause = `
		 WHERE ($1 = '' OR status = $1) AND ($2 = '' OR branch_id = $2::uuid)
		   AND ($3 = '' OR source_ref_id = $3::uuid)
		   AND ($4 = '' OR (posted_at IS NOT NULL AND posted_at::date >= $4::date))
		   AND ($5 = '' OR (posted_at IS NOT NULL AND posted_at::date <= $5::date))
		   AND ($6::int IS NULL OR (fiscal_year, entry_no) < ($6::int, $7::int))`
	var fy, en any
	if f.AfterCursor {
		fy, en = f.CursorFiscalYear, f.CursorEntryNo
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+journalCols+` FROM gl_journals`+whereClause+`
		 ORDER BY fiscal_year DESC, entry_no DESC
		 LIMIT $8`, f.Status, f.BranchID, f.SourceRefID, f.DateFrom, f.DateTo, fy, en, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("gl: list journals: %w", err)
	}
	defer rows.Close()
	var out []Journal
	for rows.Next() {
		j, err := scanJournal(rows)
		if err != nil {
			return nil, fmt.Errorf("gl: scan journal: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Attach lines (bounded by the page limit).
	for i := range out {
		lines, err := r.journalLines(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Lines = lines
	}
	return out, nil
}

func (r *Repository) ApproveJournal(ctx context.Context, id, approverID string) (Journal, error) {
	if r.pool == nil {
		return Journal{}, errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE gl_journals SET status = 'POSTED', approved_by = $2, posted_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'DRAFT'`, id, nullStr(approverID))
	if err != nil {
		if isCheckViolation(err) {
			return Journal{}, errUnbalanced
		}
		return Journal{}, fmt.Errorf("gl: approve journal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Journal{}, clientErr(409, "gl: journal is not in DRAFT state")
	}
	return r.GetJournal(ctx, id)
}

// ApproveJournalTx posts a parked DRAFT journal WITHIN the caller's transaction (approvals-rail atomic
// decide, 0075). The maker-checker guards, DRAFT-state + four-eyes (approver ≠ creator), are folded
// into one guarded UPDATE so the ledger post shares the approval-decision's transaction: the journal
// POSTED and the request-row APPROVED stamp commit or roll back together, never one without the other.
// On 0 rows a follow-up read on the SAME tx distinguishes four-eyes (403) from not-DRAFT (409) from
// not-found (404) so the decide handler surfaces the real status.
func (r *Repository) ApproveJournalTx(ctx context.Context, tx pgx.Tx, id, approverID string) error {
	tag, err := tx.Exec(ctx,
		`UPDATE gl_journals SET status = 'POSTED', approved_by = $2, posted_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'DRAFT' AND (created_by IS NULL OR created_by <> $2)`, id, nullStr(approverID))
	if err != nil {
		if isCheckViolation(err) {
			return errUnbalanced
		}
		return fmt.Errorf("gl: approve journal (tx): %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// 0 rows, the guard rejected it. Diagnose (same tx) for a precise status.
	var status string
	var createdBy *string
	switch e := tx.QueryRow(ctx, `SELECT status, created_by::text FROM gl_journals WHERE id = $1`, id).Scan(&status, &createdBy); {
	case errors.Is(e, pgx.ErrNoRows):
		return clientErr(404, "gl: journal not found")
	case e != nil:
		return fmt.Errorf("gl: approve journal diagnose (tx): %w", e)
	}
	if createdBy != nil && *createdBy == approverID {
		return errFourEyes
	}
	return clientErr(409, "gl: journal is %s, not DRAFT, cannot approve", status)
}

func (r *Repository) VoidJournal(ctx context.Context, id string) (Journal, error) {
	if r.pool == nil {
		return Journal{}, errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE gl_journals SET status = 'VOID', updated_at = now() WHERE id = $1 AND status = 'DRAFT'`, id)
	if err != nil {
		return Journal{}, fmt.Errorf("gl: void journal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Journal{}, clientErr(409, "gl: only a DRAFT journal can be voided (POSTED journals must be reversed)")
	}
	return r.GetJournal(ctx, id)
}

// - periods -

const periodCols = `id::text, year, month, status, closed_at`

func scanPeriod(row pgx.Row) (Period, error) {
	var p Period
	err := row.Scan(&p.ID, &p.Year, &p.Month, &p.Status, &p.ClosedAt)
	return p, err
}

func (r *Repository) ListPeriods(ctx context.Context) ([]Period, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+periodCols+` FROM fiscal_periods ORDER BY year, month`)
	if err != nil {
		return nil, fmt.Errorf("gl: list periods: %w", err)
	}
	defer rows.Close()
	var out []Period
	for rows.Next() {
		p, err := scanPeriod(rows)
		if err != nil {
			return nil, fmt.Errorf("gl: scan period: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) GetPeriod(ctx context.Context, id string) (Period, error) {
	if r.pool == nil {
		return Period{}, errNoDB
	}
	p, err := scanPeriod(r.pool.QueryRow(ctx, `SELECT `+periodCols+` FROM fiscal_periods WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Period{}, errNotFound
	}
	if err != nil {
		return Period{}, fmt.Errorf("gl: get period: %w", err)
	}
	return p, nil
}

func (r *Repository) ClosePeriod(ctx context.Context, id, actorID, reason string) (Period, error) {
	if r.pool == nil {
		return Period{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Period{}, fmt.Errorf("gl: begin close: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'CLOSED', closed_at = now(), closed_by = $2, updated_at = now()
		 WHERE id = $1 AND status = 'OPEN'`, id, nullStr(actorID))
	if err != nil {
		return Period{}, fmt.Errorf("gl: close period: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Period{}, clientErr(409, "gl: period is not OPEN")
	}
	if err := insertOverride(ctx, tx, id, "CLOSE", reason, actorID); err != nil {
		return Period{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Period{}, fmt.Errorf("gl: commit close: %w", err)
	}
	return r.GetPeriod(ctx, id)
}

func (r *Repository) ReopenPeriod(ctx context.Context, id, actorID, reason string) (Period, error) {
	if r.pool == nil {
		return Period{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Period{}, fmt.Errorf("gl: begin reopen: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'OPEN', closed_at = NULL, closed_by = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'CLOSED'`, id)
	if err != nil {
		return Period{}, fmt.Errorf("gl: reopen period: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Period{}, clientErr(409, "gl: period is not CLOSED")
	}
	if err := insertOverride(ctx, tx, id, "REOPEN", reason, actorID); err != nil {
		return Period{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Period{}, fmt.Errorf("gl: commit reopen: %w", err)
	}
	return r.GetPeriod(ctx, id)
}

func (r *Repository) ClosePeriodsForYear(ctx context.Context, year int, actorID, reason string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("gl: begin close-year: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx,
		`UPDATE fiscal_periods SET status = 'CLOSED', closed_at = now(), closed_by = $2, updated_at = now()
		 WHERE year = $1 AND status = 'OPEN' RETURNING id::text`, year, nullStr(actorID))
	if err != nil {
		return fmt.Errorf("gl: close periods for year: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("gl: scan closed period: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := insertOverride(ctx, tx, id, "CLOSE", reason, actorID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func insertOverride(ctx context.Context, tx pgx.Tx, periodID, action, reason, actorID string) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO gl_period_overrides (period_id, action, reason, actor_id) VALUES ($1,$2,$3,$4)`,
		periodID, action, reason, nullStr(actorID)); err != nil {
		return fmt.Errorf("gl: insert period override: %w", err)
	}
	return nil
}

// - balances (0034 views) -

func (r *Repository) AccountBalance(ctx context.Context, code string, year, month int, branchID string) (int64, string, error) {
	if r.pool == nil {
		return 0, "", errNoDB
	}
	var normal string
	err := r.pool.QueryRow(ctx, `SELECT normal_balance FROM gl_accounts WHERE code = $1`, code).Scan(&normal)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", errNotFound
	}
	if err != nil {
		return 0, "", fmt.Errorf("gl: account normal balance: %w", err)
	}
	var netDebit int64
	err = r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(net_debit_cents),0) FROM v_gl_account_balances
		 WHERE account_code = $1 AND fiscal_year = $2
		   AND ($3 <= 0 OR month = $3)
		   AND ($4 = '' OR branch_id = $4::uuid)`, code, year, month, branchID).Scan(&netDebit)
	if err != nil {
		return 0, "", fmt.Errorf("gl: account balance: %w", err)
	}
	return netDebit, normal, nil
}

// AccountBalanceAsOf sums the POSTED net-debit of an account inception-to-date across EVERY fiscal year,
// bounded by posted_at <= asOf. Unlike AccountBalance it drops the fiscal_year/month restriction, so an AR
// balance carried across a year boundary is included on the GL side of the tie-out (the year-boundary fix).
func (r *Repository) AccountBalanceAsOf(ctx context.Context, code string, asOf time.Time, branchID string) (int64, string, error) {
	if r.pool == nil {
		return 0, "", errNoDB
	}
	var normal string
	err := r.pool.QueryRow(ctx, `SELECT normal_balance FROM gl_accounts WHERE code = $1`, code).Scan(&normal)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", errNotFound
	}
	if err != nil {
		return 0, "", fmt.Errorf("gl: account normal balance: %w", err)
	}
	var netDebit int64
	err = r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
		 FROM gl_journals j
		 JOIN gl_journal_lines l ON l.journal_id = j.id
		 JOIN gl_accounts a      ON a.id = l.account_id
		 WHERE a.code = $1 AND j.status = 'POSTED' AND j.posted_at <= $2
		   AND ($3 = '' OR COALESCE(l.branch_id, j.branch_id) = $3::uuid)`,
		code, asOf, branchID).Scan(&netDebit)
	if err != nil {
		return 0, "", fmt.Errorf("gl: account balance as-of: %w", err)
	}
	return netDebit, normal, nil
}

// TrialBalanceAsOf is the INCEPTION-TO-DATE closing trial balance: one row per account with any POSTED
// activity through asOf (posted_at <= asOf), summed across ALL fiscal years, the whole-chart analog of
// AccountBalanceAsOf. This is the correct basis for reconciling against a BisTrack GLExport CLOSING trial
// balance, which is cumulative for balance-sheet accounts (and year-to-date for P&L once prior years are
// closed to retained earnings). It deliberately does NOT use v_gl_account_balances / fiscal_period grain
// (that view is per-year-per-month movement); a single-period movement query understates every balance-sheet
// account by its opening balance and manufactures false discrepancies against a real closing export. branchID
// "" is consolidated.
func (r *Repository) TrialBalanceAsOf(ctx context.Context, asOf time.Time, branchID string) ([]TrialBalanceRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT a.code::text, a.name, a.account_type, a.normal_balance,
		        COALESCE(SUM(l.debit_cents),0), COALESCE(SUM(l.credit_cents),0),
		        COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)),0)
		 FROM gl_accounts a
		 JOIN gl_journal_lines l ON l.account_id = a.id
		 JOIN gl_journals j      ON j.id = l.journal_id
		 WHERE j.status = 'POSTED' AND j.posted_at <= $1
		   AND ($2 = '' OR COALESCE(l.branch_id, j.branch_id) = $2::uuid)
		 GROUP BY a.code, a.name, a.account_type, a.normal_balance
		 ORDER BY a.code`, asOf, branchID)
	if err != nil {
		return nil, fmt.Errorf("gl: trial balance as-of: %w", err)
	}
	defer rows.Close()
	var out []TrialBalanceRow
	for rows.Next() {
		t, err := scanTBRow(rows)
		if err != nil {
			return nil, fmt.Errorf("gl: scan tb-as-of row: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanTBRow(row pgx.Rows) (TrialBalanceRow, error) {
	var t TrialBalanceRow
	err := row.Scan(&t.AccountCode, &t.AccountName, &t.AccountType, &t.NormalBalance,
		&t.DebitCents, &t.CreditCents, &t.BalanceCents)
	return t, err
}

func (r *Repository) TrialBalance(ctx context.Context, year, month int, branchID string) ([]TrialBalanceRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT account_code::text, account_name, account_type, normal_balance,
		        COALESCE(SUM(debit_cents),0), COALESCE(SUM(credit_cents),0), COALESCE(SUM(net_debit_cents),0)
		 FROM v_gl_account_balances
		 WHERE fiscal_year = $1 AND ($2 <= 0 OR month = $2) AND ($3 = '' OR branch_id = $3::uuid)
		 GROUP BY account_code, account_name, account_type, normal_balance
		 ORDER BY account_code`, year, month, branchID)
	if err != nil {
		return nil, fmt.Errorf("gl: trial balance: %w", err)
	}
	defer rows.Close()
	var out []TrialBalanceRow
	for rows.Next() {
		t, err := scanTBRow(rows)
		if err != nil {
			return nil, fmt.Errorf("gl: scan tb row: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repository) PLBalances(ctx context.Context, year int) ([]TrialBalanceRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT account_code::text, account_name, account_type, normal_balance,
		        COALESCE(SUM(debit_cents),0), COALESCE(SUM(credit_cents),0), COALESCE(SUM(net_debit_cents),0)
		 FROM v_gl_account_balances
		 WHERE fiscal_year = $1 AND account_type IN ('REVENUE','EXPENSE')
		 GROUP BY account_code, account_name, account_type, normal_balance
		 ORDER BY account_code`, year)
	if err != nil {
		return nil, fmt.Errorf("gl: pl balances: %w", err)
	}
	defer rows.Close()
	var out []TrialBalanceRow
	for rows.Next() {
		t, err := scanTBRow(rows)
		if err != nil {
			return nil, fmt.Errorf("gl: scan pl row: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// - tie-out state (first-seen age) -

func (r *Repository) UpsertTieout(ctx context.Context, subledger, accountCode, branchID string, subledgerCents, glCents int64, at time.Time) (TieoutState, error) {
	if r.pool == nil {
		return TieoutState{}, errNoDB
	}
	delta := subledgerCents - glCents
	inTie := delta == 0

	// Look up the existing state (branch NULL vs branch-scoped are distinct rows).
	var (
		firstOut *time.Time
		exists   bool
	)
	branchPred := "branch_id IS NULL"
	args := []any{subledger, accountCode}
	if branchID != "" {
		branchPred = "branch_id = $3::uuid"
		args = append(args, branchID)
	}
	err := r.pool.QueryRow(ctx,
		`SELECT first_out_of_tie_at FROM gl_tieout_state WHERE subledger = $1 AND account_code = $2 AND `+branchPred, args...).Scan(&firstOut)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		exists = false
	case err != nil:
		return TieoutState{}, fmt.Errorf("gl: tie-out select: %w", err)
	default:
		exists = true
	}

	// Compute the new first-seen timestamp: clear on tie; set on first break; hold otherwise.
	var newFirst *time.Time
	if !inTie {
		if firstOut != nil {
			newFirst = firstOut
		} else {
			t := at
			newFirst = &t
		}
	}

	if exists {
		upd := []any{newFirst, at, delta, subledger, accountCode}
		pred := "branch_id IS NULL"
		if branchID != "" {
			pred = "branch_id = $6::uuid"
			upd = append(upd, branchID)
		}
		_, err = r.pool.Exec(ctx,
			`UPDATE gl_tieout_state
			 SET first_out_of_tie_at = $1, last_checked_at = $2, last_delta_cents = $3, updated_at = now()
			 WHERE subledger = $4 AND account_code = $5 AND `+pred, upd...)
	} else {
		_, err = r.pool.Exec(ctx,
			`INSERT INTO gl_tieout_state (subledger, account_code, branch_id, first_out_of_tie_at, last_checked_at, last_delta_cents)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			subledger, accountCode, nullStr(branchID), newFirst, at, delta)
	}
	if err != nil {
		return TieoutState{}, fmt.Errorf("gl: tie-out upsert: %w", err)
	}

	st := TieoutState{
		Subledger:      subledger,
		AccountCode:    accountCode,
		BranchID:       branchID,
		SubledgerCents: subledgerCents,
		GLCents:        glCents,
		DeltaCents:     delta,
		InTie:          inTie,
		OutOfTieSince:  newFirst,
		LastCheckedAt:  at,
	}
	if newFirst != nil {
		st.AgeDays = int(at.Sub(*newFirst).Hours() / 24)
	}
	return st, nil
}

// - recurring -

func scanRecurring(row pgx.Row) (RecurringTemplate, error) {
	var t RecurringTemplate
	var linesJSON []byte
	var nextRun *time.Time
	if err := row.Scan(&t.ID, &t.Name, &t.Memo, &linesJSON, &t.Schedule, &nextRun, &t.IsActive); err != nil {
		return RecurringTemplate{}, err
	}
	if len(linesJSON) > 0 {
		if err := json.Unmarshal(linesJSON, &t.Lines); err != nil {
			return RecurringTemplate{}, fmt.Errorf("gl: recurring lines json: %w", err)
		}
	}
	if nextRun != nil {
		t.NextRun = nextRun.Format("2006-01-02")
	}
	return t, nil
}

const recurringCols = `id::text, name, memo, lines, schedule, next_run, is_active`

func (r *Repository) ListRecurring(ctx context.Context) ([]RecurringTemplate, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+recurringCols+` FROM gl_recurring_templates ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("gl: list recurring: %w", err)
	}
	defer rows.Close()
	var out []RecurringTemplate
	for rows.Next() {
		t, err := scanRecurring(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repository) GetRecurring(ctx context.Context, id string) (RecurringTemplate, error) {
	if r.pool == nil {
		return RecurringTemplate{}, errNoDB
	}
	t, err := scanRecurring(r.pool.QueryRow(ctx, `SELECT `+recurringCols+` FROM gl_recurring_templates WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return RecurringTemplate{}, errNotFound
	}
	if err != nil {
		return RecurringTemplate{}, err
	}
	return t, nil
}

// - GP-parity export -

func (r *Repository) InsertExportBatch(ctx context.Context, year, month int, format, actorID string, rowCount int) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO gl_export_batches (fiscal_year, month, format, row_count, exported_by)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id::text`,
		year, month, format, rowCount, nullStr(actorID)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("gl: insert export batch: %w", err)
	}
	return id, nil
}

func (r *Repository) ExportRows(ctx context.Context, year, month int, branchID string) ([]ExportRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT entry_no, COALESCE(to_char(posted_at,'YYYY-MM-DD'),''), account_code::text, account_name,
		        COALESCE(debit_cents,0), COALESCE(credit_cents,0), COALESCE(memo,'')
		 FROM v_gl_detail
		 WHERE period_year = $1 AND ($2 <= 0 OR period_month = $2) AND ($3 = '' OR branch_id = $3::uuid)
		 ORDER BY entry_no, account_code`, year, month, branchID)
	if err != nil {
		return nil, fmt.Errorf("gl: export rows: %w", err)
	}
	defer rows.Close()
	var out []ExportRow
	for rows.Next() {
		var e ExportRow
		if err := rows.Scan(&e.EntryNo, &e.PostedAt, &e.AccountCode, &e.AccountName, &e.DebitCents, &e.CreditCents, &e.Memo); err != nil {
			return nil, fmt.Errorf("gl: scan export row: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// - DIB-360/361: approval configuration (0106 gl_approver_assignments + gl_policy write) -

// SetJEThreshold writes the maker-checker threshold for a branch ("" = the org default row).
//
// The branch row is UPSERTED because gl_policy carries other columns (fiscal year, cost basis) whose
// defaults must not be silently replaced by a threshold edit, ON CONFLICT touches only the one
// column the caller asked to change.
func (r *Repository) SetJEThreshold(ctx context.Context, branchID string, cents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	if branchID == "" {
		tag, err := r.pool.Exec(ctx,
			`UPDATE gl_policy SET je_approval_threshold_cents = $1, updated_at = now() WHERE branch_id IS NULL`, cents)
		if err != nil {
			return fmt.Errorf("gl: set org JE threshold: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("gl: no org gl_policy row, migration 0032 seeds it")
		}
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO gl_policy (branch_id, je_approval_threshold_cents)
		 VALUES ($1::uuid, $2)
		 ON CONFLICT (branch_id) DO UPDATE SET je_approval_threshold_cents = EXCLUDED.je_approval_threshold_cents,
		                                       updated_at = now()`, branchID, cents)
	if err != nil {
		return fmt.Errorf("gl: set branch JE threshold: %w", err)
	}
	return nil
}

// ListApprovers returns enabled + disabled assignments for a policy, including the org-wide rows that
// apply to every branch. Org-wide rows come first so a reader sees the general rule before the
// branch-specific additions.
func (r *Repository) ListApprovers(ctx context.Context, policyType, branchID string) ([]ApproverAssignment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT a.id::text, COALESCE(a.branch_id::text,''), a.policy_type, a.role, a.user_id::text,
		        COALESCE(NULLIF(u.display_name,''), u.email::text, ''), a.enabled, a.created_at
		   FROM gl_approver_assignments a
		   LEFT JOIN users u ON u.id = a.user_id
		  WHERE a.policy_type = $1
		    AND (a.branch_id IS NULL OR ($2 <> '' AND a.branch_id = $2::uuid))
		  ORDER BY a.branch_id NULLS FIRST, a.created_at`, policyType, branchID)
	if err != nil {
		return nil, fmt.Errorf("gl: list approvers: %w", err)
	}
	defer rows.Close()
	var out []ApproverAssignment
	for rows.Next() {
		var a ApproverAssignment
		if err := rows.Scan(&a.ID, &a.BranchID, &a.PolicyType, &a.Role, &a.UserID, &a.UserName, &a.Enabled, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("gl: scan approver: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertApprover creates or re-enables an assignment. A duplicate (branch, policy, user) is a
// data-entry slip, not a second grant, so it updates in place rather than inserting a twin.
func (r *Repository) UpsertApprover(ctx context.Context, a ApproverAssignment) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var branch any
	if a.BranchID != "" {
		branch = a.BranchID
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO gl_approver_assignments (branch_id, policy_type, role, user_id, enabled)
		 VALUES ($1::uuid, $2, $3, $4::uuid, TRUE)
		 ON CONFLICT (COALESCE(branch_id, '00000000-0000-0000-0000-000000000000'::uuid), policy_type, user_id)
		 DO UPDATE SET role = EXCLUDED.role, enabled = TRUE, updated_at = now()
		 RETURNING id::text`, branch, a.PolicyType, a.Role, a.UserID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("gl: upsert approver: %w", err)
	}
	return id, nil
}

// RemoveApprover deletes an assignment outright.
//
// Deleting rather than disabling is right HERE and wrong for money: this table records who may sign,
// not what anyone signed. The signatures themselves live on permission_approval_requests and
// gl_journals.approved_by, which are untouched by removing someone's future authority, so history
// stays intact while the config stays readable.
func (r *Repository) RemoveApprover(ctx context.Context, id string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM gl_approver_assignments WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("gl: remove approver: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("gl: approver assignment %s not found", id)
	}
	return nil
}
