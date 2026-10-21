package bankrec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

var errNoDB = errors.New("bankrec: database not configured")

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
		       COALESCE(l.unmatched, 0)
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
		if err := rows.Scan(&a.ID, &a.Name, &a.AccountType, &a.GLAccountCode, &a.Institution, &a.Last4, &a.IsActive, &a.UnmatchedLines); err != nil {
			return nil, fmt.Errorf("bankrec: scan account: %w", err)
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
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, name, account_type, gl_account_code::text, institution, last4, is_active
		 FROM bank_accounts WHERE id = $1`, id,
	).Scan(&a.ID, &a.Name, &a.AccountType, &a.GLAccountCode, &a.Institution, &a.Last4, &a.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return BankAccount{}, errNotFound
	}
	if err != nil {
		return BankAccount{}, fmt.Errorf("bankrec: get account: %w", err)
	}
	a.TrackingOnly = a.AccountType == AccountCreditCard
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
// import id + counts. The UNIQUE(bank_account_id, dedupe_hash) index makes a re-import a no-op (AC1).
func (r *Repository) ImportLines(ctx context.Context, accountID, profileID, filename string, lines []RawStatementLine, importedBy string) (importID string, inserted int, err error) {
	if r.pool == nil {
		return "", 0, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("bankrec: begin import: %w", err)
	}
	defer tx.Rollback(ctx)

	importID, err = r.InsertImportAudit(ctx, tx, accountID, profileID, filename, ImportStatusImported, "", len(lines), importedBy)
	if err != nil {
		return "", 0, err
	}
	for _, l := range lines {
		tag, ierr := tx.Exec(ctx, `
			INSERT INTO bank_statement_lines
			  (bank_account_id, import_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'UNMATCHED')
			ON CONFLICT (bank_account_id, dedupe_hash) DO NOTHING`,
			accountID, importID, l.TxnDate, l.Description, l.Reference, l.AmountCents, l.DedupeHash)
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

	tag, err := tx.Exec(ctx,
		`UPDATE bank_statement_lines SET status = 'MATCHED' WHERE id = $1 AND status = 'UNMATCHED'`, m.StatementLineID)
	if err != nil {
		return "", fmt.Errorf("bankrec: mark line matched: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", conflict("bankrec: statement line %s is not UNMATCHED", m.StatementLineID)
	}
	if len(claimPaymentIDs) > 0 {
		// The named payment rows first, ascending (the payments module's own lock, LOCK-ORDER.md): the
		// UPDATE below takes the payments counter on its first row, and must not hold it while it waits
		// for another row a reversal holds.
		if lerr := payments.LockPaymentRowsTx(ctx, tx, claimPaymentIDs); lerr != nil {
			return "", fmt.Errorf("bankrec: lock payments: %w", lerr)
		}
		// The ids the claim may touch: all of them, or only those the caller reaches. A hidden id is never
		// updated and never named.
		claimable := claimPaymentIDs
		hidden := map[string]bool{}
		branches, berr := payments.PaymentBranchesTx(ctx, tx, claimPaymentIDs)
		if berr != nil {
			return "", fmt.Errorf("bankrec: read the payment branches: %w", berr)
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
			return "", fmt.Errorf("bankrec: claim payment: %w", cerr)
		}
		moved := map[string]bool{}
		for rows.Next() {
			var pid string
			if serr := rows.Scan(&pid); serr != nil {
				rows.Close()
				return "", fmt.Errorf("bankrec: claim payment: %w", serr)
			}
			moved[strings.ToLower(pid)] = true
		}
		rows.Close()
		if rerr := rows.Err(); rerr != nil {
			return "", fmt.Errorf("bankrec: claim payment: %w", rerr)
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
				return "", fmt.Errorf("bankrec: read the refused payment numbers: %w", nerr)
			}
			labels := make([]string, 0, len(unmoved))
			for _, pid := range unmoved {
				if hidden[strings.ToLower(pid)] {
					labels = append(labels, payments.PaymentLabel(nil, pid))
					continue
				}
				labels = append(labels, payments.PaymentLabel(numbers, pid))
			}
			return "", conflict("bankrec: payment already claimed, reversed or not RECEIVED: %s (claimed %d of %d)",
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
			return "", conflict("bankrec: line %s / target %s already has a live match", m.StatementLineID, m.TargetRefID)
		}
		return "", fmt.Errorf("bankrec: insert match: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("bankrec: commit match: %w", err)
	}
	return id, nil
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

	// One body for both fences, under the ONE payment row lock (SF-2g round 2 and SF-2d round 2). The live match's
	// target is read, the payment row is taken (ascending, the payments module's own lock, which a reversal's FOR
	// UPDATE queues behind), and before anything is written: first the caller's REACH fence (a payment outside the
	// caller's branches answers exactly as a match that is not live), then the REVERSED refusal (a deposit whose
	// payment was reversed cannot be unmatched: the reversal already took the money back out, so reverting the
	// payment and reversing the clearing journal again would take it out twice).
	var peekType, peekRef string
	err = tx.QueryRow(ctx,
		`SELECT target_type, COALESCE(target_ref_id::text, '') FROM bank_matches WHERE id::text = $1 AND unmatched_at IS NULL`,
		matchID).Scan(&peekType, &peekRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, conflict("bankrec: match %s is not live (already unmatched)", matchID)
	}
	if err != nil {
		return "", "", 0, fmt.Errorf("bankrec: read the match to unmatch: %w", err)
	}
	if peekType == TargetPayment && peekRef != "" {
		if lerr := payments.LockPaymentRowsTx(ctx, tx, []string{peekRef}); lerr != nil {
			return "", "", 0, fmt.Errorf("bankrec: lock payment: %w", lerr)
		}
		branches, berr := payments.PaymentBranchesTx(ctx, tx, []string{peekRef})
		if berr != nil {
			return "", "", 0, fmt.Errorf("bankrec: read the payment branch: %w", berr)
		}
		if b, ok := branches[strings.ToLower(peekRef)]; !ok || !reach(b) {
			return "", "", 0, conflict("bankrec: match %s is not live (already unmatched)", matchID)
		}
		reversed, rerr := payments.PaymentReversedTx(ctx, tx, peekRef)
		if rerr != nil {
			return "", "", 0, rerr
		}
		if reversed {
			return "", "", 0, conflict("bankrec: this deposit cannot be unmatched: the payment was reversed, so its money already left through the reversal")
		}
	}

	var lineID string
	err = tx.QueryRow(ctx,
		`UPDATE bank_matches SET unmatched_at = now(), unmatched_by = $2
		 WHERE id = $1 AND unmatched_at IS NULL
		 RETURNING statement_line_id::text, target_type, COALESCE(target_ref_id::text, ''), amount_cents`,
		matchID, nullStr(userID)).Scan(&lineID, &targetType, &targetRef, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, conflict("bankrec: match %s is not live (already unmatched)", matchID)
	}
	if err != nil {
		return "", "", 0, fmt.Errorf("bankrec: unmatch: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bank_statement_lines SET status = 'UNMATCHED' WHERE id = $1 AND status = 'MATCHED'`, lineID); err != nil {
		return "", "", 0, fmt.Errorf("bankrec: revert line: %w", err)
	}
	if targetType == TargetPayment && targetRef != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE payments SET status = 'RECEIVED', updated_at = now()
			 WHERE id = $1::uuid AND status = 'DEPOSITED'`, targetRef); err != nil {
			return "", "", 0, fmt.Errorf("bankrec: release payment: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", 0, fmt.Errorf("bankrec: commit unmatch: %w", err)
	}
	return targetType, targetRef, amount, nil
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

// UpsertReconciliation opens/updates the monthly reconciliation with the computed balances + variance.
func (r *Repository) UpsertReconciliation(ctx context.Context, rec Reconciliation) (Reconciliation, error) {
	if r.pool == nil {
		return Reconciliation{}, errNoDB
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO bank_reconciliations
		  (bank_account_id, year, month, statement_balance_cents, gl_balance_cents, variance_cents, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'OPEN')
		ON CONFLICT (bank_account_id, year, month) DO UPDATE SET
		  statement_balance_cents = EXCLUDED.statement_balance_cents,
		  gl_balance_cents = EXCLUDED.gl_balance_cents,
		  variance_cents = EXCLUDED.variance_cents,
		  updated_at = now()
		WHERE bank_reconciliations.status = 'OPEN'
		RETURNING id::text, bank_account_id::text, year, month, statement_balance_cents, gl_balance_cents,
		          variance_cents, status, completed_at`,
		rec.BankAccountID, rec.Year, rec.Month, rec.StatementBalanceCents, rec.GLBalanceCents, rec.VarianceCents).
		Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents, &rec.GLBalanceCents,
			&rec.VarianceCents, &rec.Status, &rec.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, conflict("bankrec: reconciliation for %d-%02d is already COMPLETED", rec.Year, rec.Month)
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: upsert reconciliation: %w", err)
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
		SELECT id::text, bank_account_id::text, year, month, statement_balance_cents, gl_balance_cents,
		       variance_cents, status, completed_at
		FROM bank_reconciliations WHERE id = $1`, id).
		Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents, &rec.GLBalanceCents,
			&rec.VarianceCents, &rec.Status, &rec.CompletedAt)
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
		SELECT id::text, bank_account_id::text, year, month, statement_balance_cents, gl_balance_cents,
		       variance_cents, status, completed_at
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
			&rec.GLBalanceCents, &rec.VarianceCents, &rec.Status, &rec.CompletedAt); err != nil {
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
func (r *Repository) CompleteReconciliation(ctx context.Context, id, glAccountCode, completedBy string) (Reconciliation, error) {
	if r.pool == nil {
		return Reconciliation{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: begin complete: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the reconciliation row, then RE-COUNT unmatched lines for the month INSIDE this tx (closing the
	// TOCTOU between the service's pre-check and the state flip). We FOR UPDATE the month's existing lines so a
	// concurrent unmatch (which flips a MATCHED line back to UNMATCHED) cannot slip past completion, and take a
	// per-account advisory xact lock so a concurrent import serializes against completion rather than racing it.
	var acctID string
	var year, month int
	var status string
	err = tx.QueryRow(ctx,
		`SELECT bank_account_id::text, year, month, status FROM bank_reconciliations WHERE id = $1 FOR UPDATE`, id).
		Scan(&acctID, &year, &month, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, errNotFound
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: lock reconciliation: %w", err)
	}
	if status != ReconOpen {
		return Reconciliation{}, conflict("bankrec: reconciliation %s is not OPEN", id)
	}
	lockKey := fmt.Sprintf("%s|%d|%d", acctID, year, month)
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: recon advisory lock: %w", err)
	}
	var unmatched int
	err = tx.QueryRow(ctx, `
		WITH locked AS (
			SELECT id, status FROM bank_statement_lines
			WHERE bank_account_id = $1::uuid
			  AND EXTRACT(YEAR FROM txn_date) = $2 AND EXTRACT(MONTH FROM txn_date) = $3
			FOR UPDATE
		)
		SELECT COUNT(*) FROM locked WHERE status = 'UNMATCHED'`, acctID, year, month).Scan(&unmatched)
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: recount unmatched: %w", err)
	}
	if unmatched > 0 {
		return Reconciliation{}, unprocessable("bankrec: cannot complete, %d UNMATCHED line(s) remain for %d-%02d", unmatched, year, month)
	}

	var rec Reconciliation
	err = tx.QueryRow(ctx, `
		UPDATE bank_reconciliations
		SET status = 'COMPLETED', completed_by = $2, completed_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'OPEN'
		RETURNING id::text, bank_account_id::text, year, month, statement_balance_cents, gl_balance_cents,
		          variance_cents, status, completed_at`,
		id, nullStr(completedBy)).
		Scan(&rec.ID, &rec.BankAccountID, &rec.Year, &rec.Month, &rec.StatementBalanceCents, &rec.GLBalanceCents,
			&rec.VarianceCents, &rec.Status, &rec.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, conflict("bankrec: reconciliation %s is not OPEN", id)
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("bankrec: complete reconciliation: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"bank_account_id":         rec.BankAccountID,
		"gl_account_code":         glAccountCode,
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
