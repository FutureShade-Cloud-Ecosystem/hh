package billing

// statement_repository.go, the pgx side of DIB-166 (see statements.go for the design).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// StatementLines returns the account's activity up to asOf, oldest first, with a running balance.
//
// It unions the two things a customer recognises on a statement, invoices they were billed and
// payments they made, from customer_transactions, which is the AR subledger both already write to.
// Reading the subledger rather than joining invoices and payments separately is what makes a reversed
// payment (DIB-170) appear correctly: the reversal is a subledger row like any other, so it shows up
// as a line on the statement instead of silently altering an earlier one.
func (r *Repository) StatementLines(ctx context.Context, customerID string, asOf time.Time) ([]StatementLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT t.occurred_at, t.txn_type, t.amount_cents, COALESCE(i.invoice_no::text, ''), COALESCE(t.memo,'')
		   FROM customer_transactions t
		   LEFT JOIN invoices i ON i.id = t.source_ref_id AND t.source_type = 'invoice'
		  WHERE t.customer_id = $1::uuid AND t.occurred_at::date <= $2::date
		  ORDER BY t.occurred_at, t.created_at`, customerID, asOf)
	if err != nil {
		return nil, fmt.Errorf("billing: statement lines: %w", err)
	}
	defer rows.Close()

	var out []StatementLine
	var running int64
	for rows.Next() {
		var (
			at      time.Time
			txnType string
			cents   int64
			invNo   string
			memo    string
		)
		if err := rows.Scan(&at, &txnType, &cents, &invNo, &memo); err != nil {
			return nil, fmt.Errorf("billing: scan statement line: %w", err)
		}
		ln := StatementLine{Date: at, DocNo: invNo, Description: statementDescription(txnType, memo)}
		// The subledger's sign convention: a charge raises AR (positive), a payment lowers it
		// (negative). The statement shows both as positive numbers in their own column, which is what
		// a reader expects, so the sign is translated here rather than shown raw.
		if cents >= 0 {
			ln.ChargeCents = cents
		} else {
			ln.CreditCents = -cents
		}
		running += cents
		ln.BalanceRun = running
		out = append(out, ln)
	}
	return out, rows.Err()
}

// statementDescription turns a subledger row into a line a customer can read without a glossary.
func statementDescription(txnType, memo string) string {
	if memo != "" {
		return memo
	}
	switch txnType {
	case "INVOICE":
		return "Invoice"
	case "PAYMENT":
		return "Payment received, thank you"
	case "CREDIT_MEMO":
		return "Credit memo"
	case "NSF":
		return "Returned payment"
	case "WRITE_OFF":
		return "Write-off"
	default:
		return "Adjustment"
	}
}

// BranchHeader resolves the branch letterhead. branchID "" resolves the first BRANCH, which is the
// right answer for a consolidated run at a single-branch dealer and a documented approximation at a
// multi-branch one (a consolidated statement has no single letterhead to be correct about).
func (r *Repository) BranchHeader(ctx context.Context, branchID string) (StatementHeader, error) {
	if r.pool == nil {
		return StatementHeader{}, errNoDB
	}
	var h StatementHeader
	var addr, phone *string
	err := r.pool.QueryRow(ctx,
		`SELECT name, COALESCE(address->>'line1',''), COALESCE(address->>'phone','')
		   FROM locations
		  WHERE ($1 <> '' AND id = $1::uuid) OR ($1 = '' AND loc_type = 'BRANCH')
		  ORDER BY (id::text = $1) DESC
		  LIMIT 1`, branchID).Scan(&h.BranchName, &addr, &phone)
	if err != nil {
		return StatementHeader{}, fmt.Errorf("billing: branch header: %w", err)
	}
	if addr != nil {
		h.Address = *addr
	}
	if phone != nil {
		h.Phone = *phone
	}
	return h, nil
}

// CreateRun opens a statement run.
func (r *Repository) CreateRun(ctx context.Context, mode, branchID string, asOf time.Time, actorID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var branch, actor any
	if branchID != "" {
		branch = branchID
	}
	if actorID != "" {
		actor = actorID
	}
	var id string
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO ar_statement_runs (mode, branch_id, as_of_date, status, requested_by)
		 VALUES ($1, $2::uuid, $3::date, 'RUNNING', $4::uuid) RETURNING id::text`,
		mode, branch, asOf, actor).Scan(&id); err != nil {
		return "", fmt.Errorf("billing: create statement run: %w", err)
	}
	return id, nil
}

// SaveStatement persists one statement, enqueues its print job, and, when a notice is supplied AND
// an outbox is wired, hands the notification to that outbox IN THE SAME TRANSACTION.
//
// Same transaction is the whole point of the seam: a customer must never be told about a statement by
// a transaction that rolled back, which is the identical discipline the yard's stage notice follows.
// With no outbox wired (the shipped default) the statement is still marked QUEUED, so a later wiring
// can find and drain exactly the set that was meant to go out.
func (r *Repository) SaveStatement(ctx context.Context, runID string, s Statement, notice *StatementNotice, outbox StatementOutbox) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("billing: save statement: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO ar_statements (run_id, customer_id, as_of_date, current_cents, days_30_cents,
		                            days_60_cents, days_90_plus_cents, closing_balance_cents, html, delivery_status)
		 VALUES ($1::uuid, $2::uuid, $3::date, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id::text`,
		runID, s.CustomerID, s.AsOfDate, s.CurrentCents, s.Days30Cents, s.Days60Cents,
		s.Days90PlusCents, s.ClosingCents, s.HTML, s.DeliveryStatus).Scan(&id); err != nil {
		return "", fmt.Errorf("billing: insert statement: %w", err)
	}

	// The print job. print_queue gained a STATEMENT doc_type in 0105 rather than this module growing a
	// private print path, 0088 built that table as "the one substrate every printed document goes
	// through", and a statement is exactly the BROWSER-target shape it already carries.
	var branchID string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(home_branch_id::text, (SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1))
		   FROM customers WHERE id = $1::uuid`, s.CustomerID).Scan(&branchID); err != nil {
		return "", fmt.Errorf("billing: resolve statement branch: %w", err)
	}
	var printJobID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO print_queue (doc_type, ref_id, branch_id, render_target, status)
		 VALUES ('STATEMENT', $1::uuid, $2::uuid, 'BROWSER', 'QUEUED') RETURNING id::text`,
		id, branchID).Scan(&printJobID); err != nil {
		return "", fmt.Errorf("billing: enqueue statement print job: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE ar_statements SET print_job_id = $2::uuid WHERE id = $1::uuid`, id, printJobID); err != nil {
		return "", fmt.Errorf("billing: link print job: %w", err)
	}

	if notice != nil && outbox != nil {
		n := *notice
		n.StatementID = id
		if err := outbox.EnqueueStatementTx(ctx, tx, n); err != nil {
			return "", fmt.Errorf("billing: enqueue statement notice: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("billing: save statement: commit: %w", err)
	}
	return id, nil
}

// FinishRun stamps a run's outcome.
func (r *Repository) FinishRun(ctx context.Context, runID, status string, generated, queued int, runErr string) error {
	if r.pool == nil {
		return errNoDB
	}
	if len(runErr) > 1000 {
		runErr = runErr[:1000]
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE ar_statement_runs
		    SET status = $2, generated_count = $3, queued_count = $4, error = $5, finished_at = now()
		  WHERE id = $1::uuid`, runID, status, generated, queued, runErr)
	if err != nil {
		return fmt.Errorf("billing: finish statement run: %w", err)
	}
	return nil
}

// RecordRunNoticeCounts stamps 0148's notice accounting (see StatementRun's field comments for what
// each number means and why queued_count alone could not answer the operator's question).
//
// A SEPARATE statement from FinishRun, not extra columns on it, for two reasons: FinishRun's
// signature is part of statementStore and is implemented by every unit fake in the package, and the
// counts must be recordable independently of the run's outcome so a future FAILED run can still say
// how far its notifications got.
func (r *Repository) RecordRunNoticeCounts(ctx context.Context, runID string, enabled bool, queued, suppressed int) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE ar_statement_runs
		    SET notices_enabled = $2, notice_queued_count = $3, notice_suppressed_count = $4
		  WHERE id = $1::uuid`, runID, enabled, queued, suppressed)
	if err != nil {
		return fmt.Errorf("billing: record statement run notice counts: %w", err)
	}
	return nil
}

// GetRun reads a run with its statements. HTML is omitted from the list read, a month-end run is
// hundreds of documents and nobody wants them all in one response; fetch one statement by id for that.
func (r *Repository) GetRun(ctx context.Context, runID string) (StatementRun, []Statement, error) {
	if r.pool == nil {
		return StatementRun{}, nil, errNoDB
	}
	var run StatementRun
	var branch *string
	var finished *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT id::text, mode, branch_id::text, as_of_date, status, generated_count, queued_count, error,
		        created_at, finished_at, notices_enabled, notice_queued_count, notice_suppressed_count
		   FROM ar_statement_runs WHERE id = $1::uuid`, runID).
		Scan(&run.ID, &run.Mode, &branch, &run.AsOfDate, &run.Status, &run.GeneratedCount,
			&run.QueuedCount, &run.Error, &run.CreatedAt, &finished,
			&run.NoticesEnabled, &run.NoticeQueuedCount, &run.NoticeSuppressedCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StatementRun{}, nil, ErrNotFound
		}
		return StatementRun{}, nil, fmt.Errorf("billing: get statement run: %w", err)
	}
	run.NoticeAccountingRecorded = run.NoticeAccountingHolds()
	if branch != nil {
		run.BranchID = *branch
	}
	run.FinishedAt = finished

	rows, err := r.pool.Query(ctx,
		`SELECT s.id::text, s.customer_id::text, COALESCE(c.name,''), s.as_of_date, s.current_cents,
		        s.days_30_cents, s.days_60_cents, s.days_90_plus_cents, s.closing_balance_cents,
		        s.delivery_status, s.created_at
		   FROM ar_statements s LEFT JOIN customers c ON c.id = s.customer_id
		  WHERE s.run_id = $1::uuid ORDER BY s.closing_balance_cents DESC`, runID)
	if err != nil {
		return run, nil, fmt.Errorf("billing: list statements: %w", err)
	}
	defer rows.Close()
	var out []Statement
	for rows.Next() {
		var s Statement
		s.RunID = runID
		if err := rows.Scan(&s.ID, &s.CustomerID, &s.CustomerName, &s.AsOfDate, &s.CurrentCents,
			&s.Days30Cents, &s.Days60Cents, &s.Days90PlusCents, &s.ClosingCents,
			&s.DeliveryStatus, &s.CreatedAt); err != nil {
			return run, nil, fmt.Errorf("billing: scan statement: %w", err)
		}
		out = append(out, s)
	}
	return run, out, rows.Err()
}

// CustomerInReach reports whether an account has any invoice in the branch reach: the cut a branch
// caller's statement reads are made by.
func (r *Repository) CustomerInReach(ctx context.Context, customerID string, reach BranchReach) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var in bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM invoices WHERE customer_id = $1::uuid
		                  AND ($2::uuid[] IS NULL OR branch_id = ANY($2::uuid[])))`,
		customerID, reach.SQLArg()).Scan(&in); err != nil {
		return false, fmt.Errorf("billing: customer in reach: %w", err)
	}
	return in, nil
}

// LatestStatement returns the most recent statement for a customer, HTML included.
func (r *Repository) LatestStatement(ctx context.Context, customerID string) (Statement, bool, error) {
	if r.pool == nil {
		return Statement{}, false, errNoDB
	}
	var s Statement
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, run_id::text, customer_id::text, as_of_date, current_cents, days_30_cents,
		        days_60_cents, days_90_plus_cents, closing_balance_cents, html, delivery_status, created_at
		   FROM ar_statements WHERE customer_id = $1::uuid
		  ORDER BY as_of_date DESC, created_at DESC LIMIT 1`, customerID).
		Scan(&s.ID, &s.RunID, &s.CustomerID, &s.AsOfDate, &s.CurrentCents, &s.Days30Cents,
			&s.Days60Cents, &s.Days90PlusCents, &s.ClosingCents, &s.HTML, &s.DeliveryStatus, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Statement{}, false, nil
		}
		return Statement{}, false, fmt.Errorf("billing: latest statement: %w", err)
	}
	return s, true, nil
}
