package bankrec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

var errNoDB = errors.New("bankrec: database not configured")

// Begin opens a transaction on the bankrec pool for the service's one transaction writers (FW1-05): the
// match and its clearing journal, and the unmatch and its reversal, commit or roll back together.
func (r *Repository) Begin(ctx context.Context) (pgx.Tx, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	return r.pool.Begin(ctx)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// - bank accounts -

// ListAccounts returns the 9 seeded accounts, each with its live-unmatched line count. CREDIT_CARD accounts
// are flagged tracking-only (no Visa spend booked at W5).
func (r *Repository) ListAccounts(ctx context.Context) ([]BankAccount, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.id::text, a.name, a.account_type, a.gl_account_code::text, a.institution, a.last4, a.is_active,
		       COALESCE(l.unmatched, 0),
		       COALESCE(to_char(a.cutover_date, 'YYYY-MM-DD'), ''),
		       a.cutover_statement_balance_cents,
		       COALESCE(a.cutover_outstanding_journal_ids::text[], '{}'::text[])
		FROM bank_accounts a
		LEFT JOIN (
			SELECT bank_account_id, COUNT(*) AS unmatched
			FROM bank_statement_lines WHERE status = 'UNMATCHED' GROUP BY bank_account_id
		) l ON l.bank_account_id = a.id
		ORDER BY a.gl_account_code`)
	if err != nil {
		return nil, fmt.Errorf("bankrec: list accounts: %w", err)
	}
	defer rows.Close()
	var out []BankAccount
	for rows.Next() {
		var a BankAccount
		var cutDate string
		if err := rows.Scan(&a.ID, &a.Name, &a.AccountType, &a.GLAccountCode, &a.Institution, &a.Last4, &a.IsActive,
			&a.UnmatchedLines, &cutDate, &a.CutoverStatementBalanceCents, &a.CutoverOutstandingJournalIDs); err != nil {
			return nil, fmt.Errorf("bankrec: scan account: %w", err)
		}
		if cutDate != "" {
			a.CutoverDate = &cutDate
		}
		a.TrackingOnly = a.AccountType == AccountCreditCard
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAccount returns one account (for import validation).
func (r *Repository) GetAccount(ctx context.Context, id string) (BankAccount, error) {
	if r.pool == nil {
		return BankAccount{}, errNoDB
	}
	var a BankAccount
	var cutDate string
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, name, account_type, gl_account_code::text, institution, last4, is_active,
		        COALESCE(to_char(cutover_date, 'YYYY-MM-DD'), ''),
		        cutover_statement_balance_cents,
		        COALESCE(cutover_outstanding_journal_ids::text[], '{}'::text[])
		 FROM bank_accounts WHERE id = $1`, id,
	).Scan(&a.ID, &a.Name, &a.AccountType, &a.GLAccountCode, &a.Institution, &a.Last4, &a.IsActive,
		&cutDate, &a.CutoverStatementBalanceCents, &a.CutoverOutstandingJournalIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return BankAccount{}, errNotFound
	}
	if err != nil {
		return BankAccount{}, fmt.Errorf("bankrec: get account: %w", err)
	}
	if cutDate != "" {
		a.CutoverDate = &cutDate
	}
	a.TrackingOnly = a.AccountType == AccountCreditCard
	return a, nil
}

// RecordAccountCutover records the reconciliation starting point for a bank account under its row lock,
// refusing when the cut-over month or any later month on that account has already signed off (FW1-04 round 2).
func (r *Repository) RecordAccountCutover(ctx context.Context, in CutoverInput, actorID string) (BankAccount, error) {
	if r.pool == nil {
		return BankAccount{}, errNoDB
	}
	cutTime, err := time.Parse("2006-01-02", in.CutoverDate)
	if err != nil {
		return BankAccount{}, badRequest("bankrec: cutover_date must be YYYY-MM-DD, got %q", in.CutoverDate)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return BankAccount{}, fmt.Errorf("bankrec: begin cutover: %w", err)
	}
	defer tx.Rollback(ctx)

	var a BankAccount
	err = tx.QueryRow(ctx, `
		SELECT id::text, name, account_type, gl_account_code::text, institution, last4, is_active
		  FROM bank_accounts
		 WHERE id = $1::uuid
		   FOR UPDATE`, in.BankAccountID).
		Scan(&a.ID, &a.Name, &a.AccountType, &a.GLAccountCode, &a.Institution, &a.Last4, &a.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return BankAccount{}, errNotFound
	}
	if err != nil {
		return BankAccount{}, fmt.Errorf("bankrec: lock account for cutover: %w", err)
	}
	var completedY, completedM int
	err = tx.QueryRow(ctx, `
		SELECT year, month
		  FROM bank_reconciliations
		 WHERE bank_account_id = $1::uuid
		   AND status = 'COMPLETED'
		   AND (year > $2 OR (year = $2 AND month >= $3))
		 ORDER BY year ASC, month ASC
		 LIMIT 1`,
		in.BankAccountID, cutTime.Year(), int(cutTime.Month())).Scan(&completedY, &completedM)
	if err == nil {
		return BankAccount{}, conflict("bankrec: cannot record cut-over at %s because month %d-%02d is already completed",
			in.CutoverDate, completedY, completedM)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BankAccount{}, fmt.Errorf("bankrec: check completed months for cutover: %w", err)
	}

	ids := in.OutstandingJournalIDs
	if ids == nil {
		ids = []string{}
	}
	var cutDate string
	var cutBal int64
	if err := tx.QueryRow(ctx, `
		UPDATE bank_accounts
		   SET cutover_date = $2::date,
		       cutover_statement_balance_cents = $3::bigint,
		       cutover_outstanding_journal_ids = $4::uuid[],
		       cutover_recorded_by = $5,
		       cutover_recorded_at = now()
		 WHERE id = $1::uuid
		RETURNING to_char(cutover_date, 'YYYY-MM-DD'),
		          cutover_statement_balance_cents,
		          COALESCE(cutover_outstanding_journal_ids::text[], '{}'::text[])`,
		in.BankAccountID, in.CutoverDate, in.OpeningStatementBalanceCents, ids, nullStr(actorID)).
		Scan(&cutDate, &cutBal, &a.CutoverOutstandingJournalIDs); err != nil {
		return BankAccount{}, fmt.Errorf("bankrec: update account cutover: %w", err)
	}
	a.CutoverDate = &cutDate
	a.CutoverStatementBalanceCents = &cutBal
	a.TrackingOnly = a.AccountType == AccountCreditCard
	if err := tx.Commit(ctx); err != nil {
		return BankAccount{}, fmt.Errorf("bankrec: commit cutover: %w", err)
	}
	return a, nil
}

// - import profiles -

// ProfileByCode resolves a column-mapping profile by its code (0033 bank_import_profiles).
func (r *Repository) ProfileByCode(ctx context.Context, code string) (ImportProfile, error) {
	if r.pool == nil {
		return ImportProfile{}, errNoDB
	}
	var p ImportProfile
	var mapping []byte
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, code::text, format, mapping FROM bank_import_profiles WHERE code = $1 AND is_active`, code,
	).Scan(&p.ID, &p.Code, &p.Format, &mapping)
	if errors.Is(err, pgx.ErrNoRows) {
		return ImportProfile{}, errNotFound
	}
	if err != nil {
		return ImportProfile{}, fmt.Errorf("bankrec: profile by code: %w", err)
	}
	if err := json.Unmarshal(mapping, &p.Mapping); err != nil {
		return ImportProfile{}, fmt.Errorf("bankrec: decode profile mapping: %w", err)
	}
	return p, nil
}

// InsertImportAudit records the import envelope (IMPORTED or FAILED). Returns the import id.
func (r *Repository) InsertImportAudit(ctx context.Context, tx pgx.Tx, accountID, profileID, filename, status, errDetail string, rowCount int, importedBy string) (string, error) {
	var id string
	err := tx.QueryRow(ctx,
		`INSERT INTO bank_statement_imports (bank_account_id, profile_id, filename, status, row_count, error_detail, imported_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id::text`,
		accountID, nullStr(profileID), filename, status, rowCount, errDetail, nullStr(importedBy),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("bankrec: insert import audit: %w", err)
	}
	return id, nil
}

// ImportLines persists parsed lines idempotently and records the IMPORTED audit, all in one tx. Returns the
// import id + counts. The UNIQUE(bank_account_id, dedupe_hash, line_ordinal) index (migration 0442) makes a
// re-import a no-op (AC1) while keeping byte identical lines of ONE file apart: line_ordinal is the line's
// one based position within its file, so a re-import collides line for line (same ordinal, same hash) but
// the second of two identical rows in one fresh file takes the next ordinal and lands (FW1-05).
func (r *Repository) ImportLines(ctx context.Context, accountID, profileID, filename string, lines []RawStatementLine, importedBy string) (importID string, inserted int, err error) {
	if r.pool == nil {
		return "", 0, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("bankrec: begin import: %w", err)
	}
	defer tx.Rollback(ctx)

	// Each month the file touches is locked in the shared form of the reconciliation key, in ascending order, so an
	// import serialises against a completion of that month as CompleteReconciliation's comment says (SF-2j round 2).
	months := map[string][2]int{}
	var keys []string
	for _, l := range lines {
		k := fmt.Sprintf("%04d-%02d", l.TxnDate.Year(), int(l.TxnDate.Month()))
		if _, ok := months[k]; !ok {
			months[k] = [2]int{l.TxnDate.Year(), int(l.TxnDate.Month())}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, lkerr := lockReconMonthShared(ctx, tx, accountID, months[k][0], months[k][1]); lkerr != nil {
			return "", 0, lkerr
		}
	}

	importID, err = r.InsertImportAudit(ctx, tx, accountID, profileID, filename, ImportStatusImported, "", len(lines), importedBy)
	if err != nil {
		return "", 0, err
	}
	for i, l := range lines {
		tag, ierr := tx.Exec(ctx, `
			INSERT INTO bank_statement_lines
			  (bank_account_id, import_id, txn_date, description, reference, amount_cents, dedupe_hash, line_ordinal, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'UNMATCHED')
			ON CONFLICT (bank_account_id, dedupe_hash, line_ordinal) DO NOTHING`,
			accountID, importID, l.TxnDate, l.Description, l.Reference, l.AmountCents, l.DedupeHash, i+1)
		if ierr != nil {
			return "", 0, fmt.Errorf("bankrec: insert line: %w", ierr)
		}
		if tag.RowsAffected() == 1 {
			inserted++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", 0, fmt.Errorf("bankrec: commit import: %w", err)
	}
	return importID, inserted, nil
}

// RecordFailedImport writes a FAILED audit row (parse error) in its own tx.
func (r *Repository) RecordFailedImport(ctx context.Context, accountID, profileID, filename, detail, importedBy string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("bankrec: begin failed import: %w", err)
	}
	defer tx.Rollback(ctx)
	id, err := r.InsertImportAudit(ctx, tx, accountID, profileID, filename, ImportStatusFailed, detail, 0, importedBy)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// - statement lines -

// LinesFilter is the statement-line list query: the account, an optional
// status filter, and the cursor window (HH-3.2 B5).
type LinesFilter struct {
	AccountID string
	Status    string
	Limit     int
	// AfterCursor resumes strictly after (CursorTxnDate, CursorCreatedAt,
	// CursorID), the list's stable-ordering key.
	AfterCursor     bool
	CursorTxnDate   time.Time
	CursorCreatedAt time.Time
	CursorID        string
}

// ListLines returns the statement lines for an account, optionally filtered
// by status.
//
// Cursor pagination (HH-3.2 B5): the offset family is retired. The resume
// predicate is a row-value comparison over the list's stable-ordering key
// (txn_date DESC, created_at DESC, id DESC tie-breaker; the id completes
// the key the pin's two-column order left ambiguous), guarded by an IS
// NULL leg so it evaluates only with a key present. No count query: the
// cursor envelope carries no total.
func (r *Repository) ListLines(ctx context.Context, f LinesFilter) ([]StatementLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var cTxn, cCreated, cID any
	if f.AfterCursor {
		cTxn, cCreated, cID = f.CursorTxnDate, f.CursorCreatedAt, f.CursorID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, bank_account_id::text, COALESCE(import_id::text, ''), txn_date, description, reference,
		       amount_cents, dedupe_hash, status, created_at
		FROM bank_statement_lines
		WHERE bank_account_id = $1 AND ($2 = '' OR status = $2)
		  AND ($3::timestamptz IS NULL OR (txn_date, created_at, id) < ($3::timestamptz, $4::timestamptz, $5::uuid))
		ORDER BY txn_date DESC, created_at DESC, id DESC
		LIMIT $6`, f.AccountID, f.Status, cTxn, cCreated, cID, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("bankrec: list lines: %w", err)
	}
	defer rows.Close()
	var out []StatementLine
	for rows.Next() {
		var l StatementLine
		if err := rows.Scan(&l.ID, &l.BankAccountID, &l.ImportID, &l.TxnDate, &l.Description, &l.Reference,
			&l.AmountCents, &l.DedupeHash, &l.Status, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("bankrec: scan line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetLine returns one statement line.
func (r *Repository) GetLine(ctx context.Context, id string) (StatementLine, error) {
	if r.pool == nil {
		return StatementLine{}, errNoDB
	}
	var l StatementLine
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, bank_account_id::text, COALESCE(import_id::text, ''), txn_date, description, reference,
		       amount_cents, dedupe_hash, status
		FROM bank_statement_lines WHERE id = $1`, id).
		Scan(&l.ID, &l.BankAccountID, &l.ImportID, &l.TxnDate, &l.Description, &l.Reference,
			&l.AmountCents, &l.DedupeHash, &l.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return StatementLine{}, errNotFound
	}
	if err != nil {
		return StatementLine{}, fmt.Errorf("bankrec: get line: %w", err)
	}
	return l, nil
}

// UnmatchedCreditLines returns the UNMATCHED positive (deposit/settlement) lines for an account in the window
// , the auto-matcher's work list.
func (r *Repository) UnmatchedCreditLines(ctx context.Context, accountID string, from, to time.Time) ([]StatementLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, bank_account_id::text, COALESCE(import_id::text, ''), txn_date, description, reference,
		       amount_cents, dedupe_hash, status
		FROM bank_statement_lines
		WHERE bank_account_id = $1 AND status = 'UNMATCHED' AND amount_cents > 0
		  AND txn_date >= $2 AND txn_date < $3
		ORDER BY txn_date`, accountID, from, to)
	if err != nil {
		return nil, fmt.Errorf("bankrec: unmatched credit lines: %w", err)
	}
	defer rows.Close()
	var out []StatementLine
	for rows.Next() {
		var l StatementLine
		if err := rows.Scan(&l.ID, &l.BankAccountID, &l.ImportID, &l.TxnDate, &l.Description, &l.Reference,
			&l.AmountCents, &l.DedupeHash, &l.Status); err != nil {
			return nil, fmt.Errorf("bankrec: scan work line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// - matches -

// PersistMatch flips the line to MATCHED, CLAIMS the paired payment(s), and inserts the match row, all in ONE
// transaction (replacing the former non-transactional line-flip + separate MarkDeposited that could partially
// commit). Guards, any of which aborts the whole tx:
//   - the line must still be UNMATCHED (else 409);
//   - every claimed payment must still be RECEIVED, RowsAffected must equal len(claimPaymentIDs), else another
//     match already deposited it (409), so we never post a second clearing journal against it;
//   - the match target must have no live match, the partial-unique ux_bank_matches_live_target (0036) rejects
//     a second live match on the same payment/lodgement (belt to the payment-claim suspenders).
//
// The clearing journal is posted by the SERVICE only AFTER this returns, so a rejected duplicate posts NO
// journal. The journal id is written back via SetMatchJournal.
// PersistMatchReach claims the match, fenced to the caller's branch reach (SF-2g). Under the payment rows' lock (the
// same ascending lock the claim takes), the branch of each named payment is read through the payments module and
// checked against reach; a payment outside it, or one that does not exist, is not claimed and is reported EXACTLY as
// a missing payment is, with no number: the caller cannot tell "not yours" from "not there". The branch is a fact
// of the row read under its lock, in the claim's own transaction, so another writer cannot move the payment between
// the check and the claim; and a refusal rolls the whole transaction back, so nothing is written. A nil reach
// REFUSES (SF-2d round 5); a dealer wide caller passes AllBranches.
func (r *Repository) PersistMatchReach(ctx context.Context, m Match, claimPaymentIDs []string, reach func(branchID string) bool) (string, error) {
	if reach == nil {
		return "", errNoReach
	}
	if r.pool == nil {
		return "", errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("bankrec: begin match: %w", err)
	}
	defer tx.Rollback(ctx)
	id, _, err := r.PersistMatchReachTx(ctx, tx, m, claimPaymentIDs, reach)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("bankrec: commit match: %w", err)
	}
	return id, nil
}

// MatchPaymentFact is what bankrec read about one claimed payment under its row lock in the match's own
// transaction (FW1-05): the branch, the tender method and the gross amount, the facts a hand match checks
// (amount and method) and the facts its clearing journal needs (branch and method).
type MatchPaymentFact struct {
	PaymentID   string
	BranchID    string
	Method      string
	AmountCents int64
}

// PersistMatchReachTx is the claim body of PersistMatchReach on the CALLER's transaction (FW1-05: the match
// and its clearing journal share one transaction, so the service opens the tx, claims here, posts through
// the JournalPosterTx arm, and commits once). Same guards, same locks, same order as PersistMatchReach. It
// returns the facts of the claimed payments, read under the same lock, for the caller's checks and journal.
func (r *Repository) PersistMatchReachTx(ctx context.Context, tx pgx.Tx, m Match, claimPaymentIDs []string, reach func(branchID string) bool) (string, []MatchPaymentFact, error) {
	if reach == nil {
		return "", nil, errNoReach
	}

	// The lock order of every bankrec writer (LOCK-ORDER.md): the reconciliation row of the line's month FOR SHARE and
	// the shared advisory key FIRST, before the line UPDATE and before any payment row. A line inside a COMPLETED month
	// is refused: a line imported after the sign off would otherwise post a clearing into a signed off month. A line
	// that does not exist falls through to the UPDATE's own refusal below.
	var lineAcct string
	var lineYear, lineMonth int
	lerr0 := tx.QueryRow(ctx,
		`SELECT bank_account_id::text, EXTRACT(YEAR FROM txn_date)::int, EXTRACT(MONTH FROM txn_date)::int FROM bank_statement_lines WHERE id::text = $1`,
		m.StatementLineID).Scan(&lineAcct, &lineYear, &lineMonth)
	if lerr0 != nil && !errors.Is(lerr0, pgx.ErrNoRows) {
		return "", nil, fmt.Errorf("bankrec: read the line to match: %w", lerr0)
	}
	if lerr0 == nil {
		status, lkerr := lockReconMonthShared(ctx, tx, lineAcct, lineYear, lineMonth)
		if lkerr != nil {
			return "", nil, lkerr
		}
		if status == ReconCompleted {
			return "", nil, errMonthCompleted
		}
	}

	tag, err := tx.Exec(ctx,
		`UPDATE bank_statement_lines SET status = 'MATCHED' WHERE id = $1 AND status = 'UNMATCHED'`, m.StatementLineID)
	if err != nil {
		return "", nil, fmt.Errorf("bankrec: mark line matched: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", nil, conflict("bankrec: statement line %s is not UNMATCHED", m.StatementLineID)
	}
	if m.TargetType == TargetJournal && m.TargetRefID != "" {
		// A journal explains one line: bank_matches has no unique index for JOURNAL targets (0036 covers PAYMENT and
		// LODGEMENT only), so a transaction lock on the journal id serialises the check and the insert.
		if _, lerr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "bankrec-journal|"+strings.ToLower(m.TargetRefID)); lerr != nil {
			return "", nil, fmt.Errorf("bankrec: journal lock: %w", lerr)
		}
		var held bool
		if qerr := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM bank_matches WHERE unmatched_at IS NULL
			                 AND ((target_type = 'JOURNAL' AND target_ref_id::text = $1) OR journal_id::text = $1))`,
			strings.ToLower(m.TargetRefID)).Scan(&held); qerr != nil {
			return "", nil, fmt.Errorf("bankrec: journal live match: %w", qerr)
		}
		if held {
			return "", nil, conflict("bankrec: that journal already explains another statement line")
		}
	}
	// The ids the claim may touch: all of them, or only those the caller reaches. A hidden id is never
	// updated and never named. Hoisted so the claimed facts read at the tail sees the same set.
	var claimable []string
	if len(claimPaymentIDs) > 0 {
		// The named payment rows first, ascending (the payments module's own lock, LOCK-ORDER.md): the
		// UPDATE below takes the payments counter on its first row, and must not hold it while it waits
		// for another row a reversal holds.
		if lerr := payments.LockPaymentRowsTx(ctx, tx, claimPaymentIDs); lerr != nil {
			return "", nil, fmt.Errorf("bankrec: lock payments: %w", lerr)
		}
		hidden := map[string]bool{}
		branches, berr := payments.PaymentBranchesTx(ctx, tx, claimPaymentIDs)
		if berr != nil {
			return "", nil, fmt.Errorf("bankrec: read the payment branches: %w", berr)
		}
		claimable = nil
		for _, pid := range claimPaymentIDs {
			if b, ok := branches[strings.ToLower(pid)]; ok && reach(b) {
				claimable = append(claimable, pid)
			} else {
				hidden[strings.ToLower(pid)] = true
			}
		}
		// A reversed payment is refused here (SF-2b) EXCEPT a CARD (SF-2d round 4, ruling 52.2): the reversal is a ledger
		// act that does not touch the gateway, so the processor settles the captured card anyway, and the settlement
		// line has to be explained. The money did arrive; the claim moves the payment to DEPOSITED and the match posts
		// the clearing (Dr 1000 / Cr 1010) that the reversal's credit to 2310 leaves standing. Every other tender
		// that was reversed (cash, cheque, EFT) is never matched: no deposit of it can be real.
		rows, cerr := tx.Query(ctx,
			`UPDATE payments
			   SET status = 'DEPOSITED',
			       metadata = metadata || jsonb_build_object('bank_statement_line_id', $2::text),
			       updated_at = now()
			 WHERE id = ANY($1::uuid[]) AND status = 'RECEIVED' AND (reversed_at IS NULL OR method = 'CARD')
			 RETURNING id::text`,
			claimable, m.StatementLineID)
		if cerr != nil {
			return "", nil, fmt.Errorf("bankrec: claim payment: %w", cerr)
		}
		moved := map[string]bool{}
		for rows.Next() {
			var pid string
			if serr := rows.Scan(&pid); serr != nil {
				rows.Close()
				return "", nil, fmt.Errorf("bankrec: claim payment: %w", serr)
			}
			moved[strings.ToLower(pid)] = true
		}
		rows.Close()
		if rerr := rows.Err(); rerr != nil {
			return "", nil, fmt.Errorf("bankrec: claim payment: %w", rerr)
		}
		var unmoved []string
		for _, pid := range claimPaymentIDs {
			if !moved[strings.ToLower(pid)] {
				unmoved = append(unmoved, pid)
			}
		}
		if len(unmoved) > 0 {
			// The UPDATE above decided: its row lock and its WHERE, re-checked after any concurrent
			// commit (a reversal stamps reversed_at and leaves the status RECEIVED), refused these rows,
			// and RETURNING says which moved. No read of the payments table follows to say which of the
			// three causes it was (a second read of another module's table would be a new cross-module
			// access), so the sentence names all three and lists the payments that did not move, by
			// payment number (read through the payments module). The sentence's first words are the desk's stable
			// match (money-desk bankrec-logic.ts).
			// The numbers come through the payments module, which owns the table (no read here). The
			// dealer reads a payment number, never an id.
			// Only a payment the caller reaches is looked up for its number; a hidden one reads as missing.
			var visible []string
			for _, pid := range unmoved {
				if !hidden[strings.ToLower(pid)] {
					visible = append(visible, pid)
				}
			}
			numbers, nerr := payments.PaymentNumbersTx(ctx, tx, visible)
			if nerr != nil {
				return "", nil, fmt.Errorf("bankrec: read the refused payment numbers: %w", nerr)
			}
			labels := make([]string, 0, len(unmoved))
			for _, pid := range unmoved {
				if hidden[strings.ToLower(pid)] {
					labels = append(labels, payments.PaymentLabel(nil, pid))
					continue
				}
				labels = append(labels, payments.PaymentLabel(numbers, pid))
			}
			return "", nil, conflict("bankrec: payment already claimed, reversed or not RECEIVED: %s (claimed %d of %d)",
				strings.Join(labels, ", "), len(moved), len(claimPaymentIDs))
		}
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO bank_matches
		  (statement_line_id, target_type, target_ref_id, method, rule_id, journal_id, amount_cents, matched_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id::text`,
		m.StatementLineID, m.TargetType, nullStr(m.TargetRefID), m.Method, nullStr(m.RuleID),
		nullStr(m.JournalID), m.AmountCents, nullStr(m.MatchedBy)).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return "", nil, conflict("bankrec: line %s / target %s already has a live match", m.StatementLineID, m.TargetRefID)
		}
		return "", nil, fmt.Errorf("bankrec: insert match: %w", err)
	}
	// The facts of what the claim moved, read under the same lock, for the caller's amount and method
	// checks and its clearing journal (FW1-05).
	var claimed []MatchPaymentFact
	if len(claimable) > 0 {
		facts, ferr := payments.PaymentFactsTx(ctx, tx, claimable)
		if ferr != nil {
			return "", nil, ferr
		}
		for _, pid := range claimable {
			if f, ok := facts[strings.ToLower(pid)]; ok {
				claimed = append(claimed, MatchPaymentFact{
					PaymentID:   pid,
					BranchID:    f.BranchID,
					Method:      f.Method,
					AmountCents: f.AmountCents,
				})
			}
		}
	}
	return id, claimed, nil
}

// SetMatchJournal writes the posted clearing journal id back onto a match row (the journal is posted after the
// match is claimed, so a rejected duplicate never posts a journal).
func (r *Repository) SetMatchJournal(ctx context.Context, matchID, journalID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if journalID == "" {
		return nil
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE bank_matches SET journal_id = $2 WHERE id = $1`, matchID, journalID); err != nil {
		return fmt.Errorf("bankrec: set match journal: %w", err)
	}
	return nil
}

// SetMatchJournalTx is SetMatchJournal on the caller's transaction (FW1-05: the journal id lands inside the
// match's own transaction so a roll back leaves no journal-less match row).
func (r *Repository) SetMatchJournalTx(ctx context.Context, tx pgx.Tx, matchID, journalID string) error {
	if journalID == "" {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bank_matches SET journal_id = $2 WHERE id = $1`, matchID, journalID); err != nil {
		return fmt.Errorf("bankrec: set match journal: %w", err)
	}
	return nil
}

// ListMatches returns the full match history for a statement line (live + unmatched), newest first.
func (r *Repository) ListMatches(ctx context.Context, lineID string) ([]Match, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, statement_line_id::text, target_type, COALESCE(target_ref_id::text, ''), method,
		       COALESCE(rule_id::text, ''), COALESCE(journal_id::text, ''), amount_cents,
		       COALESCE(matched_by::text, ''), matched_at, unmatched_at
		FROM bank_matches WHERE statement_line_id = $1 ORDER BY matched_at DESC`, lineID)
	if err != nil {
		return nil, fmt.Errorf("bankrec: list matches: %w", err)
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.ID, &m.StatementLineID, &m.TargetType, &m.TargetRefID, &m.Method,
			&m.RuleID, &m.JournalID, &m.AmountCents, &m.MatchedBy, &m.MatchedAt, &m.UnmatchedAt); err != nil {
			return nil, fmt.Errorf("bankrec: scan match: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetLiveMatch returns the live (unmatched_at IS NULL) match by id.
func (r *Repository) GetLiveMatch(ctx context.Context, matchID string) (Match, error) {
	if r.pool == nil {
		return Match{}, errNoDB
	}
	var m Match
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, statement_line_id::text, target_type, COALESCE(target_ref_id::text, ''), method,
		       COALESCE(rule_id::text, ''), COALESCE(journal_id::text, ''), amount_cents,
		       COALESCE(matched_by::text, ''), matched_at, unmatched_at
		FROM bank_matches WHERE id = $1`, matchID).
		Scan(&m.ID, &m.StatementLineID, &m.TargetType, &m.TargetRefID, &m.Method, &m.RuleID, &m.JournalID,
			&m.AmountCents, &m.MatchedBy, &m.MatchedAt, &m.UnmatchedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Match{}, errNotFound
	}
	if err != nil {
		return Match{}, fmt.Errorf("bankrec: get live match: %w", err)
	}
	return m, nil
}

// PeekMatchTarget reads a match's target without locking: live is false for a match that does not exist or is
// already unmatched. A match row's target fields never change, so the read cannot go stale.
func (r *Repository) PeekMatchTarget(ctx context.Context, matchID string) (targetType, targetRef string, live bool, err error) {
	if r.pool == nil {
		return "", "", false, errNoDB
	}
	err = r.pool.QueryRow(ctx,
		`SELECT target_type, COALESCE(target_ref_id::text, '') FROM bank_matches WHERE id::text = $1 AND unmatched_at IS NULL`,
		matchID).Scan(&targetType, &targetRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("bankrec: read the match target: %w", err)
	}
	return targetType, targetRef, true, nil
}

// MatchLineDate returns the txn_date of the statement line for a match id (used so an unmatch's reversing
// clearing journal carries the statement line's date, FW1-04).
func (r *Repository) MatchLineDate(ctx context.Context, matchID string) (time.Time, bool, error) {
	if r.pool == nil {
		return time.Time{}, false, errNoDB
	}
	var d time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT l.txn_date
		  FROM bank_matches m
		  JOIN bank_statement_lines l ON l.id = m.statement_line_id
		 WHERE m.id::text = $1`, matchID).Scan(&d)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("bankrec: read match line date: %w", err)
	}
	return d, true, nil
}

// UnmatchFacts is what an unmatch read about the match it just reversed, all under its own transaction
// (FW1-05): the target the reversal mirrors, the line amount it moved, and later rounds add the fee and
// branch facts the reversing journal needs.
type UnmatchFacts struct {
	TargetType  string
	TargetRef   string
	AmountCents int64
}

// UnmatchReach is Unmatch with both of its fences, in ONE body under ONE payment row lock: the caller's branch reach
// (SF-2g round 2) and the refusal of a reversed payment (SF-2d round 2). For a PAYMENT target, inside the transaction
// and before anything is written: the live match's target is read, the payment row is taken (payments.LockPaymentRowsTx,
// FOR NO KEY UPDATE, which a reversal's FOR UPDATE queues behind), the payment's branch is read through the payments
// module under that lock and checked against the reach (a payment outside it answers exactly as a match that is not
// live), then the payments module says whether it was reversed (a reversed payment's deposit cannot be unmatched).
// The lock order is the payment row (taken whatever the reach), then the match, the line and the payment update.
// A nil reach REFUSES (SF-2d round 5); a dealer wide caller passes AllBranches.
func (r *Repository) UnmatchReach(ctx context.Context, matchID, userID string, reach func(branchID string) bool) (targetType, targetRef string, amount int64, err error) {
	if reach == nil {
		return "", "", 0, errNoReach
	}
	if r.pool == nil {
		return "", "", 0, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", 0, fmt.Errorf("bankrec: begin unmatch: %w", err)
	}
	defer tx.Rollback(ctx)
	facts, err := r.UnmatchReachTx(ctx, tx, matchID, userID, reach)
	if err != nil {
		return "", "", 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", 0, fmt.Errorf("bankrec: commit unmatch: %w", err)
	}
	return facts.TargetType, facts.TargetRef, facts.AmountCents, nil
}

// UnmatchReachTx is the unmatch body on the CALLER's transaction (FW1-05: the unmatch and its reversing
// journal share one transaction, so the service opens the tx, unmatches here, posts the reversal through
// the JournalPosterTx arm, and commits once). Same guards, same locks, same order as UnmatchReach.
func (r *Repository) UnmatchReachTx(ctx context.Context, tx pgx.Tx, matchID, userID string, reach func(branchID string) bool) (UnmatchFacts, error) {
	var out UnmatchFacts
	if reach == nil {
		return out, errNoReach
	}

	// The lock order of every bankrec writer (LOCK-ORDER.md, SF-2j round 2): the reconciliation row of the line's month
	// FOR SHARE and the shared form of completion's advisory key FIRST, then the payment row, then the match, the line
	// and the payment update. The live match is read once (its target and its line's month, which never change), with
	// no lock yet.
	var peekType, peekRef, recAcct string
	var recYear, recMonth int
	err := tx.QueryRow(ctx,
		`SELECT m.target_type, COALESCE(m.target_ref_id::text, ''), l.bank_account_id::text,
		        EXTRACT(YEAR FROM l.txn_date)::int, EXTRACT(MONTH FROM l.txn_date)::int
		   FROM bank_matches m JOIN bank_statement_lines l ON l.id = m.statement_line_id
		  WHERE m.id::text = $1 AND m.unmatched_at IS NULL`,
		matchID).Scan(&peekType, &peekRef, &recAcct, &recYear, &recMonth)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, conflict("bankrec: match %s is not live (already unmatched)", matchID)
	}
	if err != nil {
		return out, fmt.Errorf("bankrec: read the match to unmatch: %w", err)
	}
	recStatus, rerr0 := lockReconMonthShared(ctx, tx, recAcct, recYear, recMonth)
	if rerr0 != nil {
		return out, rerr0
	}

	// One body for all the fences, under the ONE payment row lock (SF-2g round 2 and SF-2d round 2): the payment row
	// is taken (ascending, the payments module's own lock, which a reversal's FOR UPDATE queues behind), and before
	// anything is written: first the caller's REACH fence (a payment outside the caller's branches answers exactly
	// as a match that is not live), then the COMPLETED month refusal, then the REVERSED refusal (a deposit whose
	// payment was reversed cannot be unmatched, except a CARD, see below). The month is refused only after the reach
	// is checked, so a caller out of reach learns nothing about a month it cannot see.
	if peekType == TargetPayment && peekRef != "" {
		if lerr := payments.LockPaymentRowsTx(ctx, tx, []string{peekRef}); lerr != nil {
			return out, fmt.Errorf("bankrec: lock payment: %w", lerr)
		}
		branches, berr := payments.PaymentBranchesTx(ctx, tx, []string{peekRef})
		if berr != nil {
			return out, fmt.Errorf("bankrec: read the payment branch: %w", berr)
		}
		if b, ok := branches[strings.ToLower(peekRef)]; !ok || !reach(b) {
			return out, conflict("bankrec: match %s is not live (already unmatched)", matchID)
		}
	}
	if recStatus == ReconCompleted {
		return out, conflict("bankrec: that match sits in %d-%02d, which is already reconciled and completed, so it cannot be unmatched", recYear, recMonth)
	}
	if peekType == TargetPayment && peekRef != "" {
		reversed, method, rerr := payments.PaymentReversedTx(ctx, tx, peekRef)
		if rerr != nil {
			return out, rerr
		}
		// A reversed CARD may be unmatched (SF-2d round 7, S3's P2): the unmatch only moves Dr 1000 / Cr 1010 back,
		// the card is claimable again (round 4) and 2310 is untouched, so a wrong match can be corrected. Every
		// other reversed tender can never be claimed again, so its bank line could not be explained again.
		if reversed && method != "CARD" {
			return out, conflict("bankrec: this deposit cannot be unmatched: the payment was reversed, and a reversed cheque or cash payment cannot be matched again")
		}
	}

	var lineID string
	err = tx.QueryRow(ctx,
		`UPDATE bank_matches SET unmatched_at = now(), unmatched_by = $2
		 WHERE id = $1 AND unmatched_at IS NULL
		 RETURNING statement_line_id::text, target_type, COALESCE(target_ref_id::text, ''), amount_cents`,
		matchID, nullStr(userID)).Scan(&lineID, &out.TargetType, &out.TargetRef, &out.AmountCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, conflict("bankrec: match %s is not live (already unmatched)", matchID)
	}
	if err != nil {
		return out, fmt.Errorf("bankrec: unmatch: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bank_statement_lines SET status = 'UNMATCHED' WHERE id = $1 AND status = 'MATCHED'`, lineID); err != nil {
		return out, fmt.Errorf("bankrec: revert line: %w", err)
	}
	if out.TargetType == TargetPayment && out.TargetRef != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE payments SET status = 'RECEIVED', updated_at = now()
			 WHERE id = $1::uuid AND status = 'DEPOSITED'`, out.TargetRef); err != nil {
			return out, fmt.Errorf("bankrec: release payment: %w", err)
		}
	}
	return out, nil
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// centsText is the dealer's dollars; the repository shares the service's writer.
func centsText(cents int64) string { return dollars(cents) }

// errMonthCompleted refuses a match of a line inside a COMPLETED reconciliation's month. The auto run skips such a
// line instead of stopping.
var errMonthCompleted = conflict("bankrec: that statement line sits in a month that is already reconciled and completed, so it cannot be matched")

// reconKey is the advisory key CompleteReconciliation and every other writer that must see a month's status share. The
// prefix keeps it from ever hashing like another module's text (SF-2j round 2).
func reconKey(acct string, year, month int) string {
	return fmt.Sprintf("bankrec-recon|%s|%d|%d", acct, year, month)
}

// lockReconMonthShared takes the reconciliation row of (account, year, month) FOR SHARE, which conflicts with
// completion's FOR UPDATE, then the shared form of its advisory key, and answers the status the row reads under that
// lock ("" when there is no row yet). Completion first: the caller waits and reads COMPLETED. The caller first: a
// completion waits for the caller's commit and its recount sees what the caller wrote. No row yet: a row made later
// is OPEN and its completion needs the exclusive key, which this shared hold blocks until the caller commits. Every
// bankrec writer takes this pair before any payment row (LOCK-ORDER.md).
func lockReconMonthShared(ctx context.Context, tx pgx.Tx, acct string, year, month int) (string, error) {
	var status string
	serr := tx.QueryRow(ctx,
		`SELECT status FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2 AND month = $3 FOR SHARE`,
		acct, year, month).Scan(&status)
	if serr != nil && !errors.Is(serr, pgx.ErrNoRows) {
		return "", fmt.Errorf("bankrec: read the reconciliation: %w", serr)
	}
	if _, lerr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 0))`, reconKey(acct, year, month)); lerr != nil {
		return "", fmt.Errorf("bankrec: recon advisory lock: %w", lerr)
	}
	return status, nil
}

// - match rules -

// ApprovedRules returns the live (APPROVED) rules with their specs decoded.
func (r *Repository) ApprovedRules(ctx context.Context) ([]MatchRule, error) {
	return r.rulesWhere(ctx, "status = 'APPROVED'")
}

// AllRules returns every rule version (for the versioning UI).
func (r *Repository) AllRules(ctx context.Context) ([]MatchRule, error) {
	return r.rulesWhere(ctx, "TRUE")
}

func (r *Repository) rulesWhere(ctx context.Context, where string) ([]MatchRule, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, code::text, name, spec, priority, version, status, COALESCE(supersedes_rule_id::text, '')
		FROM bank_match_rules WHERE `+where+` ORDER BY priority, code, version DESC`)
	if err != nil {
		return nil, fmt.Errorf("bankrec: list rules: %w", err)
	}
	defer rows.Close()
	var out []MatchRule
	for rows.Next() {
		var rule MatchRule
		var spec []byte
		if err := rows.Scan(&rule.ID, &rule.Code, &rule.Name, &spec, &rule.Priority, &rule.Version, &rule.Status, &rule.SupersedesID); err != nil {
			return nil, fmt.Errorf("bankrec: scan rule: %w", err)
		}
		if err := json.Unmarshal(spec, &rule.Spec); err != nil {
			return nil, fmt.Errorf("bankrec: decode rule spec: %w", err)
		}
		decodeSpec(&rule)
		out = append(out, rule)
	}
	return out, rows.Err()
}

// ApproveNewRuleVersion supersedes the current APPROVED rule for a code with a new APPROVED version (or v1 if
// none), retiring the old, atomically (AC5). Returns the new rule.
func (r *Repository) ApproveNewRuleVersion(ctx context.Context, in RuleInput, approvedBy string) (MatchRule, error) {
	if r.pool == nil {
		return MatchRule{}, errNoDB
	}
	specJSON, err := json.Marshal(in.Spec)
	if err != nil {
		return MatchRule{}, fmt.Errorf("bankrec: encode spec: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return MatchRule{}, fmt.Errorf("bankrec: begin rule: %w", err)
	}
	defer tx.Rollback(ctx)

	var oldID string
	var oldVersion int
	err = tx.QueryRow(ctx,
		`SELECT id::text, version FROM bank_match_rules WHERE code = $1 AND status = 'APPROVED'`, in.Code).
		Scan(&oldID, &oldVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return MatchRule{}, fmt.Errorf("bankrec: find approved rule: %w", err)
	}
	if oldID != "" {
		if _, uerr := tx.Exec(ctx,
			`UPDATE bank_match_rules SET status = 'RETIRED', updated_at = now() WHERE id = $1`, oldID); uerr != nil {
			return MatchRule{}, fmt.Errorf("bankrec: retire old rule: %w", uerr)
		}
	}
	priority := in.Priority
	if priority == 0 {
		priority = 100
	}
	var out MatchRule
	var spec []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO bank_match_rules (code, name, spec, priority, version, status, supersedes_rule_id, approved_by, approved_at)
		VALUES ($1, $2, $3, $4, $5, 'APPROVED', $6, $7, now())
		RETURNING id::text, code::text, name, spec, priority, version, status, COALESCE(supersedes_rule_id::text, '')`,
		in.Code, in.Name, specJSON, priority, oldVersion+1, nullStr(oldID), nullStr(approvedBy)).
		Scan(&out.ID, &out.Code, &out.Name, &spec, &out.Priority, &out.Version, &out.Status, &out.SupersedesID)
	if err != nil {
		if isUniqueViolation(err) {
			return MatchRule{}, conflict("bankrec: another APPROVED version of %q already exists", in.Code)
		}
		return MatchRule{}, fmt.Errorf("bankrec: insert rule: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MatchRule{}, fmt.Errorf("bankrec: commit rule: %w", err)
	}
	_ = json.Unmarshal(spec, &out.Spec)
	decodeSpec(&out)
	return out, nil
}

// - reconciliations -

type reconRowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// monthLineStats holds the bank-owned statement line and match facts for one (account, year, month) needed by
// computeReconFigures (FW1-04).
type monthLineStats struct {
	TotalLines            int
	MatchedLines          int
	UnmatchedLines        int
	LineSumCents          int64
	PriorCompletedClosing *int64
	ClearedJournalIDs     map[string]bool
}

// MonthLineStats reads the statement line counts, line sum, prior completed month's closing balance, and
// cleared journal IDs through the end of (year, month) for an account.
func (r *Repository) MonthLineStats(ctx context.Context, accountID string, year, month int) (monthLineStats, error) {
	if r.pool == nil {
		return monthLineStats{}, errNoDB
	}
	return monthLineStatsOn(ctx, r.pool, accountID, year, month)
}

func monthLineStatsOn(ctx context.Context, q reconRowQuerier, accountID string, year, month int) (monthLineStats, error) {
	var st monthLineStats
	if err := q.QueryRow(ctx, `
		SELECT COUNT(*)::int,
		       COUNT(*) FILTER (WHERE status = 'MATCHED')::int,
		       COUNT(*) FILTER (WHERE status = 'UNMATCHED')::int,
		       COALESCE(SUM(amount_cents), 0)::bigint
		  FROM bank_statement_lines
		 WHERE bank_account_id = $1::uuid
		   AND EXTRACT(YEAR FROM txn_date) = $2
		   AND EXTRACT(MONTH FROM txn_date) = $3`,
		accountID, year, month).Scan(&st.TotalLines, &st.MatchedLines, &st.UnmatchedLines, &st.LineSumCents); err != nil {
		return monthLineStats{}, fmt.Errorf("bankrec: month line stats: %w", err)
	}
	prevY, prevM := year, month-1
	if month == 1 {
		prevY, prevM = year-1, 12
	}
	var priorClosing int64
	if err := q.QueryRow(ctx, `
		SELECT statement_balance_cents
		  FROM bank_reconciliations
		 WHERE bank_account_id = $1::uuid
		   AND year = $2 AND month = $3
		   AND status = 'COMPLETED'`,
		accountID, prevY, prevM).Scan(&priorClosing); err == nil {
		st.PriorCompletedClosing = &priorClosing
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return monthLineStats{}, fmt.Errorf("bankrec: read prior completed closing balance: %w", err)
	}
	if st.PriorCompletedClosing == nil {
		var cutoverClosing int64
		if err := q.QueryRow(ctx, `
			SELECT a.cutover_statement_balance_cents
			  FROM bank_accounts a
			 WHERE a.id = $1::uuid
			   AND a.cutover_date IS NOT NULL
			   AND a.cutover_statement_balance_cents IS NOT NULL
			   AND (EXTRACT(YEAR FROM a.cutover_date) < $2
			        OR (EXTRACT(YEAR FROM a.cutover_date) = $2 AND EXTRACT(MONTH FROM a.cutover_date) <= $3))
			   AND NOT EXISTS (
			       SELECT 1 FROM bank_reconciliations r
			        WHERE r.bank_account_id = a.id
			          AND (r.year > EXTRACT(YEAR FROM a.cutover_date)::int
			               OR (r.year = EXTRACT(YEAR FROM a.cutover_date)::int AND r.month > EXTRACT(MONTH FROM a.cutover_date)::int))
			          AND (r.year < $4 OR (r.year = $4 AND r.month < $5))
			   )`,
			accountID, prevY, prevM, year, month).Scan(&cutoverClosing); err == nil {
			st.PriorCompletedClosing = &cutoverClosing
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return monthLineStats{}, fmt.Errorf("bankrec: read cutover opening balance: %w", err)
		}
	}
	rows, err := q.Query(ctx, `
		SELECT COALESCE(m.target_ref_id::text, ''), COALESCE(m.journal_id::text, '')
		  FROM bank_matches m
		  JOIN bank_statement_lines l ON l.id = m.statement_line_id
		 WHERE l.bank_account_id = $1::uuid
		   AND m.unmatched_at IS NULL
		   AND (EXTRACT(YEAR FROM l.txn_date) < $2
		        OR (EXTRACT(YEAR FROM l.txn_date) = $2 AND EXTRACT(MONTH FROM l.txn_date) <= $3))`,
		accountID, year, month)
	if err != nil {
		return monthLineStats{}, fmt.Errorf("bankrec: read cleared journals through month end: %w", err)
	}
	defer rows.Close()
	st.ClearedJournalIDs = map[string]bool{}
	for rows.Next() {
		var targetRef, journalID string
		if serr := rows.Scan(&targetRef, &journalID); serr != nil {
			return monthLineStats{}, fmt.Errorf("bankrec: scan cleared journal: %w", serr)
		}
		if targetRef != "" {
			st.ClearedJournalIDs[targetRef] = true
		}
		if journalID != "" {
			st.ClearedJournalIDs[journalID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return monthLineStats{}, err
	}
	return st, nil
}

// GetOpenReconciliationByPeriod returns the OPEN reconciliation for (accountID, year, month).
func (r *Repository) GetOpenReconciliationByPeriod(ctx context.Context, accountID string, year, month int) (Reconciliation, error) {
	if r.pool == nil {
		return Reconciliation{}, errNoDB
	}
	var rec Reconciliation
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, bank_account_id::text, year, month, statement_balance_cents, statement_balance_derived,
		       gl_balance_cents, variance_cents, status, completed_by::text, completed_at
		  FROM bank_reconciliations
		 WHERE bank_account_id = $1::uuid AND year = $2 AND month = $3`,
		accountID, year, month).
		Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents, &rec.StatementBalanceDerived,
			&rec.GLBalanceCents, &rec.VarianceCents, &rec.Status, &rec.CompletedBy, &rec.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, errNotFound
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: get reconciliation by period: %w", err)
	}
	if rec.Status == ReconCompleted {
		return Reconciliation{}, conflict("bankrec: reconciliation for %d-%02d is already COMPLETED", year, month)
	}
	return rec, nil
}

// UpsertReconciliation opens the monthly reconciliation with the computed balances + variance. An open with 0 sets
// statement_balance_derived TRUE, and an open with a typed figure sets it FALSE, always.
func (r *Repository) UpsertReconciliation(ctx context.Context, rec Reconciliation) (Reconciliation, error) {
	if r.pool == nil {
		return Reconciliation{}, errNoDB
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO bank_reconciliations
		  (bank_account_id, year, month, statement_balance_cents, statement_balance_derived, gl_balance_cents, variance_cents, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'OPEN')
		ON CONFLICT (bank_account_id, year, month) DO UPDATE SET
		  statement_balance_derived = EXCLUDED.statement_balance_derived,
		  statement_balance_cents = EXCLUDED.statement_balance_cents,
		  gl_balance_cents = EXCLUDED.gl_balance_cents,
		  variance_cents = EXCLUDED.variance_cents,
		  updated_at = now()
		WHERE bank_reconciliations.status = 'OPEN'
		RETURNING id::text, bank_account_id::text, year, month, statement_balance_cents, statement_balance_derived,
		          gl_balance_cents, variance_cents, status, completed_by::text, completed_at`,
		rec.BankAccountID, rec.Year, rec.Month, rec.StatementBalanceCents, rec.StatementBalanceDerived, rec.GLBalanceCents, rec.VarianceCents).
		Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents, &rec.StatementBalanceDerived,
			&rec.GLBalanceCents, &rec.VarianceCents, &rec.Status, &rec.CompletedBy, &rec.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, conflict("bankrec: reconciliation for %d-%02d is already COMPLETED", rec.Year, rec.Month)
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: upsert reconciliation: %w", err)
	}
	return rec, nil
}

// UpdateOpenReconciliationFigures updates an existing OPEN reconciliation's computed statement_balance_cents,
// gl_balance_cents, and variance_cents in place while keeping statement_balance_derived unchanged.
func (r *Repository) UpdateOpenReconciliationFigures(ctx context.Context, accountID string, year, month int, stmtBalanceCents, glBalanceCents, varianceCents int64) (Reconciliation, error) {
	if r.pool == nil {
		return Reconciliation{}, errNoDB
	}
	var rec Reconciliation
	err := r.pool.QueryRow(ctx, `
		UPDATE bank_reconciliations
		   SET statement_balance_cents = $4::bigint,
		       gl_balance_cents = $5::bigint,
		       variance_cents = $6::bigint,
		       updated_at = now()
		 WHERE bank_account_id = $1::uuid AND year = $2 AND month = $3 AND status = 'OPEN'
		RETURNING id::text, bank_account_id::text, year, month, statement_balance_cents,
		          statement_balance_derived, gl_balance_cents, variance_cents, status, completed_by::text, completed_at`,
		accountID, year, month, stmtBalanceCents, glBalanceCents, varianceCents).
		Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents,
			&rec.StatementBalanceDerived, &rec.GLBalanceCents, &rec.VarianceCents, &rec.Status, &rec.CompletedBy, &rec.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var status string
		if serr := r.pool.QueryRow(ctx,
			`SELECT status FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2 AND month = $3`,
			accountID, year, month).Scan(&status); serr == nil && status == ReconCompleted {
			return Reconciliation{}, conflict("bankrec: reconciliation for %d-%02d is already COMPLETED", year, month)
		}
		return Reconciliation{}, errNotFound
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: refresh reconciliation: %w", err)
	}
	return rec, nil
}

// GetReconciliation returns one reconciliation.
func (r *Repository) GetReconciliation(ctx context.Context, id string) (Reconciliation, error) {
	if r.pool == nil {
		return Reconciliation{}, errNoDB
	}
	var rec Reconciliation
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, bank_account_id::text, year, month, statement_balance_cents, statement_balance_derived,
		       gl_balance_cents, variance_cents, status, completed_by::text, completed_at
		FROM bank_reconciliations WHERE id = $1`, id).
		Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents, &rec.StatementBalanceDerived,
			&rec.GLBalanceCents, &rec.VarianceCents, &rec.Status, &rec.CompletedBy, &rec.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, errNotFound
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: get reconciliation: %w", err)
	}
	return rec, nil
}

// ListReconciliations returns reconciliations, optionally filtered by account.
func (r *Repository) ListReconciliations(ctx context.Context, accountID string) ([]Reconciliation, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, bank_account_id::text, year, month, statement_balance_cents, statement_balance_derived,
		       gl_balance_cents, variance_cents, status, completed_by::text, completed_at
		FROM bank_reconciliations WHERE ($1 = '' OR bank_account_id = $1::uuid)
		ORDER BY year DESC, month DESC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("bankrec: list reconciliations: %w", err)
	}
	defer rows.Close()
	var out []Reconciliation
	for rows.Next() {
		var rec Reconciliation
		if err := rows.Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents,
			&rec.StatementBalanceDerived, &rec.GLBalanceCents, &rec.VarianceCents, &rec.Status, &rec.CompletedBy, &rec.CompletedAt); err != nil {
			return nil, fmt.Errorf("bankrec: scan reconciliation: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CountUnmatchedInMonth returns how many UNMATCHED lines the account still has for the given month (the
// completion guard).
func (r *Repository) CountUnmatchedInMonth(ctx context.Context, accountID string, year, month int) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM bank_statement_lines
		WHERE bank_account_id = $1 AND status = 'UNMATCHED'
		  AND EXTRACT(YEAR FROM txn_date) = $2 AND EXTRACT(MONTH FROM txn_date) = $3`,
		accountID, year, month).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("bankrec: count unmatched in month: %w", err)
	}
	return n, nil
}

// CompleteReconciliation marks a reconciliation COMPLETED and emits bank.recon.completed in the SAME tx (so
// the event is never published without the state change, or vice versa). Guarded on status OPEN.
//
// Lock and concurrency note (FW1-04):
//  1. The bank_reconciliations row is locked FOR UPDATE and the per-(account, year, month) exclusive advisory
//     lock is taken before any statement line, match, or ledger read.
//  2. The month's bank_statement_lines are locked FOR UPDATE so no match, unmatch, or import in the month can
//     race completion.
//  3. The shared computeReconFigures callback reads the GL closing balance and book entries immediately before
//     the UPDATE + outbox INSERT + tx.Commit, keeping the window between the ledger read and commit minimal.
//     Because gl postings do not take bankrec's advisory lock, a concurrent GL post committed in that sub-ms
//     window between the read and tx.Commit is not frozen by bankrec's lock and would surface on the next
//     month's reconciliation.
//
// CompleteReconciliation is the repository-level completion entry point; Service.CompleteReconciliation calls
// completeReconciliation with the shared computeReconFigures callback.
func (r *Repository) CompleteReconciliation(
	ctx context.Context,
	id, glAccountCode, completedBy string,
	recheck func(ctx context.Context, matches []JournalMatchFact) error,
	ledger func(ctx context.Context, year, month int) (int64, error),
) (Reconciliation, error) {
	var compute func(ctx context.Context, year, month int, storedStmt int64, derived bool, stats monthLineStats) (reconFigures, error)
	if ledger != nil {
		compute = func(ctx context.Context, year, month int, storedStmt int64, derived bool, stats monthLineStats) (reconFigures, error) {
			stmt := storedStmt
			if derived {
				stmt = stats.LineSumCents
				if stats.PriorCompletedClosing != nil {
					stmt += *stats.PriorCompletedClosing
				}
			}
			glBal, err := ledger(ctx, year, month)
			if err != nil {
				return reconFigures{}, err
			}
			return reconFigures{
				OpeningBalanceCents:      stats.PriorCompletedClosing,
				StatementBalanceCents:    stmt,
				StatementBalanceDerived:  derived,
				AdjustedBankBalanceCents: stmt,
				GLBalanceCents:           glBal,
				VarianceCents:            stmt - glBal,
				MatchedLinesCount:        stats.MatchedLines,
			}, nil
		}
	}
	return r.completeReconciliation(ctx, id, BankAccount{GLAccountCode: glAccountCode}, completedBy, recheck, compute)
}

func (r *Repository) completeReconciliation(
	ctx context.Context,
	id string,
	acct BankAccount,
	completedBy string,
	recheck func(ctx context.Context, matches []JournalMatchFact) error,
	compute func(ctx context.Context, year, month int, storedStmt int64, derived bool, stats monthLineStats) (reconFigures, error),
) (Reconciliation, error) {
	if r.pool == nil {
		return Reconciliation{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: begin complete: %w", err)
	}
	defer tx.Rollback(ctx)

	var acctID string
	var year, month int
	var status string
	var storedStmt int64
	var derived bool
	err = tx.QueryRow(ctx, `
		SELECT bank_account_id::text, year, month, status, statement_balance_cents, statement_balance_derived
		  FROM bank_reconciliations WHERE id = $1 FOR UPDATE`, id).
		Scan(&acctID, &year, &month, &status, &storedStmt, &derived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, errNotFound
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: lock reconciliation: %w", err)
	}
	if status != ReconOpen {
		return Reconciliation{}, conflict("bankrec: reconciliation %s is not OPEN", id)
	}
	lockKey := reconKey(acctID, year, month)
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: recon advisory lock: %w", err)
	}
	var totalLines, unmatched int
	err = tx.QueryRow(ctx, `
		WITH locked AS (
			SELECT id, status FROM bank_statement_lines
			WHERE bank_account_id = $1::uuid
			  AND EXTRACT(YEAR FROM txn_date) = $2 AND EXTRACT(MONTH FROM txn_date) = $3
			FOR UPDATE
		)
		SELECT COUNT(*)::int, COUNT(*) FILTER (WHERE status = 'UNMATCHED')::int FROM locked`, acctID, year, month).
		Scan(&totalLines, &unmatched)
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: recount unmatched: %w", err)
	}
	if unmatched > 0 {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, %d UNMATCHED line(s) remain for %d-%02d", unmatched, year, month)
	}
	if !acct.TrackingOnly && totalLines == 0 {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, no statement lines have been imported for %d-%02d", year, month)
	}

	// Chronological order guard (FW1-04): an earlier OPEN reconciliation on the same bank account cannot be
	// skipped past, and an earlier month cannot be signed off after a later month is already COMPLETED.
	var earlierOpenY, earlierOpenM int
	if err := tx.QueryRow(ctx, `
		SELECT year, month FROM bank_reconciliations
		 WHERE bank_account_id = $1::uuid AND status = 'OPEN'
		   AND (year < $2 OR (year = $2 AND month < $3))
		 ORDER BY year, month LIMIT 1`,
		acctID, year, month).Scan(&earlierOpenY, &earlierOpenM); err == nil {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, an earlier month (%d-%02d) is still open; sign off months in chronological order",
			earlierOpenY, earlierOpenM)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, fmt.Errorf("bankrec: check earlier open reconciliations: %w", err)
	}
	var laterCompY, laterCompM int
	if err := tx.QueryRow(ctx, `
		SELECT year, month FROM bank_reconciliations
		 WHERE bank_account_id = $1::uuid AND status = 'COMPLETED'
		   AND (year > $2 OR (year = $2 AND month > $3))
		 ORDER BY year, month LIMIT 1`,
		acctID, year, month).Scan(&laterCompY, &laterCompM); err == nil {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, a later month (%d-%02d) is already completed; months must be signed off in chronological order",
			laterCompY, laterCompM)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, fmt.Errorf("bankrec: check later completed reconciliations: %w", err)
	}

	// The month's live matches (both JOURNAL targets and clearing journals on PAYMENT/LODGEMENT matches), read
	// with the lines locked above, are rechecked through the GL seam (SF-2j round 2 + FW1-04): a journal or
	// clearing journal reversed since the match refuses the sign off.
	if recheck != nil {
		jrows, qerr := tx.Query(ctx, `
			SELECT m.target_type, COALESCE(m.target_ref_id::text, ''), COALESCE(m.journal_id::text, ''), l.description, l.amount_cents
			  FROM bank_matches m JOIN bank_statement_lines l ON l.id = m.statement_line_id
			 WHERE l.bank_account_id = $1::uuid AND m.unmatched_at IS NULL
			   AND (m.target_type = 'JOURNAL' OR m.journal_id IS NOT NULL)
			   AND EXTRACT(YEAR FROM l.txn_date) = $2 AND EXTRACT(MONTH FROM l.txn_date) = $3
			 ORDER BY l.txn_date, l.id`, acctID, year, month)
		if qerr != nil {
			return Reconciliation{}, fmt.Errorf("bankrec: read the journal matches: %w", qerr)
		}
		var matches []JournalMatchFact
		for jrows.Next() {
			var m JournalMatchFact
			if serr := jrows.Scan(&m.TargetType, &m.JournalID, &m.ClearingJournalID, &m.LineDescription, &m.LineAmountCents); serr != nil {
				jrows.Close()
				return Reconciliation{}, fmt.Errorf("bankrec: scan a journal match: %w", serr)
			}
			matches = append(matches, m)
		}
		jrows.Close()
		if jerr := jrows.Err(); jerr != nil {
			return Reconciliation{}, jerr
		}
		if rerr := recheck(ctx, matches); rerr != nil {
			return Reconciliation{}, rerr
		}
	}

	stats, err := monthLineStatsOn(ctx, tx, acctID, year, month)
	if err != nil {
		return Reconciliation{}, err
	}
	if compute == nil {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, the ledger balance cannot be read")
	}
	figs, err := compute(ctx, year, month, storedStmt, derived, stats)
	if err != nil {
		return Reconciliation{}, err
	}

	// Prior completed month closing-to-opening continuity guard (FW1-04): when the immediately preceding month
	// on this bank account is COMPLETED and the bookkeeper entered a stated closing balance, that closing
	// balance must equal the prior month's closing balance plus this month's statement line net.
	if stats.PriorCompletedClosing != nil && !derived {
		expectedClosing := *stats.PriorCompletedClosing + stats.LineSumCents
		if figs.StatementBalanceCents != expectedClosing {
			return Reconciliation{}, unprocessable(
				"bankrec: cannot complete, the statement closing balance (%s) does not equal the prior month's closing balance (%s) plus this month's statement lines (%s = %s)",
				centsText(figs.StatementBalanceCents), centsText(*stats.PriorCompletedClosing), centsText(stats.LineSumCents), centsText(expectedClosing))
		}
	}

	if figs.VarianceCents != 0 {
		if figs.DepositsInTransitCents == 0 && figs.OutstandingChequesCents == 0 {
			return Reconciliation{}, unprocessable("bankrec: cannot complete, the statement reads %s and the ledger reads %s (%s apart); they must agree, refresh the figures and match what is open",
				centsText(figs.StatementBalanceCents), centsText(figs.GLBalanceCents), centsText(absInt64(figs.VarianceCents)))
		}
		return Reconciliation{}, unprocessable("bankrec: cannot complete, the adjusted bank balance reads %s (statement %s, deposits in transit %s, outstanding cheques %s) and the ledger reads %s (%s apart); they must agree, refresh the figures and match what is open",
			centsText(figs.AdjustedBankBalanceCents), centsText(figs.StatementBalanceCents), centsText(figs.DepositsInTransitCents), centsText(figs.OutstandingChequesCents), centsText(figs.GLBalanceCents), centsText(absInt64(figs.VarianceCents)))
	}

	var rec Reconciliation
	err = tx.QueryRow(ctx, `
		UPDATE bank_reconciliations
		SET status = 'COMPLETED', completed_by = $2, completed_at = now(), updated_at = now(),
		    statement_balance_cents = $3, gl_balance_cents = $4, variance_cents = $5
		WHERE id = $1 AND status = 'OPEN'
		RETURNING id::text, bank_account_id::text, year, month, statement_balance_cents, statement_balance_derived,
		          gl_balance_cents, variance_cents, status, completed_by::text, completed_at`,
		id, nullStr(completedBy), figs.StatementBalanceCents, figs.GLBalanceCents, figs.VarianceCents).
		Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents, &rec.StatementBalanceDerived,
			&rec.GLBalanceCents, &rec.VarianceCents, &rec.Status, &rec.CompletedBy, &rec.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, conflict("bankrec: reconciliation %s is not OPEN", id)
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: complete reconciliation: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"bank_account_id":         rec.BankAccountID,
		"gl_account_code":         acct.GLAccountCode,
		"year":                    rec.Year,
		"month":                   rec.Month,
		"statement_balance_cents": rec.StatementBalanceCents,
		"gl_balance_cents":        rec.GLBalanceCents,
		"variance_cents":          rec.VarianceCents,
	})
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ($1, $2)`, TopicReconCompleted, payload); err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: emit recon completed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: commit complete: %w", err)
	}
	rec.applyFigures(figs)
	return rec, nil
}

// MonthLineSum returns the signed sum of statement-line amounts for an account in a month (the default
// statement balance when the caller does not supply the real closing balance).
func (r *Repository) MonthLineSum(ctx context.Context, accountID string, year, month int) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var sum int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM bank_statement_lines
		WHERE bank_account_id = $1
		  AND EXTRACT(YEAR FROM txn_date) = $2 AND EXTRACT(MONTH FROM txn_date) = $3`,
		accountID, year, month).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("bankrec: month line sum: %w", err)
	}
	return sum, nil
}

// decodeSpec maps the JSONB spec into the rule's typed matcher fields.
func decodeSpec(rule *MatchRule) {
	s := rule.Spec
	rule.Kind = specStr(s, "kind")
	rule.Target = specStr(s, "target")
	rule.ToleranceCents = specInt(s, "tolerance_cents")
	rule.DescriptorILike = specStr(s, "descriptor_ilike")
	rule.DateWindowDays = int(specInt(s, "date_window_days"))
	rule.AllowFeeSplit = specBool(s, "allow_fee_split")
	rule.FeeAccountCode = specStr(s, "fee_account_code")
}

func specStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}
func specInt(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}
func specBool(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}
