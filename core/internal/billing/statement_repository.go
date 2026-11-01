package billing

// statement_repository.go, the pgx side of DIB-166 (see statements.go for the design).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
)

// StatementPreviewLine is one open-invoice row the C8 S3 statement preview lists. The bucket
// label is derived server-side from days_overdue, never computed in the browser.
type StatementPreviewLine struct {
	InvoiceID    string    `json:"invoice_id"`
	InvoiceNo    string    `json:"invoice_no"`
	DueDate      time.Time `json:"due_date"`
	BalanceCents int64     `json:"balance_cents"`
	DaysOverdue  int       `json:"days_overdue"`
	BucketLabel  string    `json:"bucket_label"`

	// PositionWithheld is set by the handler when the dealer's account position policy refuses this
	// caller. MarshalJSON then leaves days_overdue and bucket_label off the wire.
	PositionWithheld bool `json:"-"`
}

// MarshalJSON writes the statement preview line, omitting days_overdue and bucket_label when the
// caller is refused by the account position policy.
func (l StatementPreviewLine) MarshalJSON() ([]byte, error) {
	type plain StatementPreviewLine
	w := struct {
		plain
		DaysOverdue *int    `json:"days_overdue,omitempty"`
		BucketLabel *string `json:"bucket_label,omitempty"`
	}{plain: plain(l)}
	if !l.PositionWithheld {
		w.DaysOverdue, w.BucketLabel = &l.DaysOverdue, &l.BucketLabel
	}
	return json.Marshal(w)
}

// StatementPreviewAging is the 5-bucket breakdown the C8 S3 statement preview card carries.
// The five buckets sum to OpenCents and the four late buckets sum to OverdueCents; both
// checks happen at the handler, server-side, before the panel renders. ReconciliationToThePenny
// is the predicate the handler asks.
type StatementPreviewAging struct {
	CurrentCents    int64 `json:"current_cents"`
	Days30Cents     int64 `json:"days_30_cents"`
	Days60Cents     int64 `json:"days_60_cents"`
	Days90Cents     int64 `json:"days_90_cents"`
	Days90PlusCents int64 `json:"days_90_plus_cents"`
	OpenCents       int64 `json:"open_cents"`
	OverdueCents    int64 `json:"overdue_cents"`
	// HoldbackCents is the holdback the account's open invoices still withhold: owed later, on its own
	// line, and in none of the buckets or OpenCents above (RT-2).
	HoldbackCents int64 `json:"holdback_cents"`
}

// BillingStatementPreview is the wire shape the preview handler returns. Reconciles is the
// server-side assertion (sum of buckets equals openCents and sum of late buckets equals
// overdueCents) so a drift fails the read before the panel renders.
type BillingStatementPreview struct {
	Statement  Statement              `json:"statement"`
	OpenLines  []StatementPreviewLine `json:"open_lines"`
	Aging      StatementPreviewAging  `json:"aging"`
	Reconciled bool                   `json:"reconciled"`

	// PositionWithheld is set by the handler when the dealer's account position policy refuses this
	// caller. MarshalJSON then omits the aging block from the wire.
	PositionWithheld bool `json:"-"`
}

// MarshalJSON writes the statement preview, omitting the aging breakdown when the caller is refused
// by the account position policy.
func (p BillingStatementPreview) MarshalJSON() ([]byte, error) {
	type plain BillingStatementPreview
	w := struct {
		plain
		Aging *StatementPreviewAging `json:"aging,omitempty"`
	}{plain: plain(p)}
	if !p.PositionWithheld {
		w.Aging = &p.Aging
	}
	return json.Marshal(w)
}

// PaylinkDeliveryRow is the row RecordPaylinkDelivery writes to ar_invoice_deliveries. The
// invoice_id is optional (the URL is scoped to a customer; an unscoped URL pins to a whole
// account), amount_cents is part of the URL token and the idempotency key, not a column on
// ar_invoice_deliveries (0175), so it is consumed by the URL builder and not by SQL.
type PaylinkDeliveryRow struct {
	CustomerID  string
	InvoiceID   *string
	AmountCents *int64
	Channel     string
	Recipient   string
	PaylinkURL  string
	IdemKey     string
	SentBy      string
	SentAt      time.Time
}

// StatementLines returns the account's activity up to asOf, oldest first, with a running balance.
//
// It unions the two things a customer recognises on a statement, invoices they were billed and
// payments they made, from customer_transactions, which is the AR subledger both already write to.
// Reading the subledger rather than joining invoices and payments separately is what makes a reversed
// payment (DIB-170) appear correctly: the reversal is a subledger row like any other, so it shows up
// as a line on the statement instead of silently altering an earlier one.
//
// reach cuts the subledger rows by the branch they were posted at, so a statement made for a branch
// reach lists that reach's lines and its running balance follows the same cut as its closing figure
// (the aging the run read with the same reach). A row with no branch is the whole book's, not any
// branch's, and is left out of a reach limited statement.
func (r *Repository) StatementLines(ctx context.Context, customerID string, asOf time.Time, reach BranchReach) ([]StatementLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	zone, err := r.zoneFor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx,
		`WITH activity AS (
			SELECT t.occurred_at, t.created_at, t.txn_type, t.amount_cents,
			       COALESCE(i.invoice_no::text, '') AS invoice_no, COALESCE(t.memo,'') AS memo, t.source_type,
			       COALESCE(ri.invoice_no::text, '') AS reverses_no
			  FROM customer_transactions t
			  LEFT JOIN invoices i ON i.id = t.source_ref_id AND t.source_type = 'invoice'
			  LEFT JOIN invoices ri ON ri.id = i.reverses_invoice_id AND ri.customer_id = t.customer_id
			 WHERE t.customer_id = $1::uuid AND (t.occurred_at AT TIME ZONE $4::text)::date <= $2::date
			   AND ($3::uuid[] IS NULL OR t.branch_id = ANY($3::uuid[]))
			UNION ALL
			SELECT (i.issue_date::timestamp AT TIME ZONE $4::text) AS occurred_at,
			       i.created_at,
			       i.doc_type AS txn_type,
			       i.total_cents AS amount_cents,
			       i.invoice_no::text AS invoice_no,
			       i.invoice_no::text AS memo, '' AS source_type, '' AS reverses_no
			  FROM invoices i
			 WHERE i.customer_id = $1::uuid
			   AND i.status IN ('FINALIZED', 'PARTIALLY_PAID')
			   AND i.balance_cents <> 0
			   AND i.issue_date <= $2::date
			   AND ($3::uuid[] IS NULL OR i.branch_id = ANY($3::uuid[]))
			   AND NOT EXISTS (
			       SELECT 1 FROM customer_transactions ct
			        WHERE ct.customer_id = i.customer_id
			          AND ct.source_type = 'invoice'
			          AND ct.source_ref_id = i.id
			          AND ct.txn_type IN ('INVOICE', 'CREDIT_MEMO')
			   )
			UNION ALL
			-- The holdback an invoice withheld on the statement date is its own line: money the customer
			-- owes later, not now, so the statement's running balance and closing figure are the net owed
			-- (the aging's figure). It is the retention ledger's sum to the end of the statement day
			-- (invoice_holdback_as_of), for every invoice issued by then, whatever its status or holdback is
			-- today: a credit, a write off or a release taken after the date never rewrites the statement.
			SELECT h.occurred_at, h.created_at, 'HOLDBACK' AS txn_type, -h.held AS amount_cents,
			       h.invoice_no, 'Holdback withheld on ' || h.invoice_no AS memo, '' AS source_type, '' AS reverses_no
			  FROM (
			    SELECT (i.issue_date::timestamp AT TIME ZONE $4::text) AS occurred_at,
			           i.created_at + interval '1 microsecond' AS created_at,
			           i.invoice_no::text AS invoice_no,
			           invoice_holdback_as_of(i.id,
			             (($2::date + 1)::timestamp AT TIME ZONE $4::text) - interval '1 microsecond') AS held
			      FROM invoices i
			     WHERE i.customer_id = $1::uuid
			       AND i.doc_type = 'INVOICE'
			       AND i.status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
			       AND i.issue_date <= $2::date
			       AND ($3::uuid[] IS NULL OR i.branch_id = ANY($3::uuid[]))
			  ) h
			 WHERE h.held > 0
			UNION ALL
			-- A holdback released by the statement date is said so, in the words the preview uses: the
			-- invoice's line above carries the whole total, and without this the customer would see the
			-- "Holdback withheld" line vanish with no word of why. It moves no money (the amount is zero), so
			-- the running balance and the closing figure are the aging's, unchanged.
			SELECT r.occurred_at, r.created_at, 'HOLDBACK_RELEASED' AS txn_type, 0::bigint AS amount_cents,
			       r.invoice_no, r.invoice_no || ' (holdback released ' || to_char(r.rel_on, 'YYYY-MM-DD') || ')' AS memo, '' AS source_type, '' AS reverses_no
			  FROM (
			    SELECT (invoice_released_on(i.id, (($2::date + 1)::timestamp AT TIME ZONE $4::text) - interval '1 microsecond')::timestamp AT TIME ZONE $4::text) AS occurred_at,
			           i.created_at + interval '2 microsecond' AS created_at,
			           i.invoice_no::text AS invoice_no,
			           invoice_released_on(i.id, (($2::date + 1)::timestamp AT TIME ZONE $4::text) - interval '1 microsecond') AS rel_on,
			           invoice_released_cents_as_of(i.id, (($2::date + 1)::timestamp AT TIME ZONE $4::text) - interval '1 microsecond') AS rel_cents
			      FROM invoices i
			     WHERE i.customer_id = $1::uuid
			       AND i.doc_type = 'INVOICE'
			       AND i.status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
			       AND i.issue_date <= $2::date
			       AND ($3::uuid[] IS NULL OR i.branch_id = ANY($3::uuid[]))
			  ) r
			 WHERE r.rel_cents > 0
		)
		SELECT occurred_at, txn_type, amount_cents, invoice_no, memo, source_type, reverses_no
		  FROM activity
		 ORDER BY occurred_at, created_at`, customerID, houseClockIn(zone, asOf), reach.SQLArg(), zone.Name())
	if err != nil {
		return nil, fmt.Errorf("billing: statement lines: %w", err)
	}
	defer rows.Close()

	var out []StatementLine
	var running int64
	for rows.Next() {
		var (
			at         time.Time
			txnType    string
			cents      int64
			invNo      string
			memo       string
			srcType    string
			reversesNo string
		)
		if err := rows.Scan(&at, &txnType, &cents, &invNo, &memo, &srcType, &reversesNo); err != nil {
			return nil, fmt.Errorf("billing: scan statement line: %w", err)
		}
		ln := StatementLine{Date: statementLineDate(zone, at), DocNo: invNo, Description: statementDescription(txnType, srcType, memo, invNo, reversesNo)}
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

// statementLineDate is the dealer's day an activity line printed under. The cut off above is in the
// house zone, so the date shown must be too, or a late evening sale prints a day after its cut off.
func statementLineDate(zone houseday.Zone, at time.Time) time.Time { return houseClockIn(zone, at) }

// What the plain statement says for a ledger row (item 254). A customer document never carries a clerk's
// free text or an internal id, so a row reads in fixed words chosen by its transaction type and source, and
// only a memo on the explicit allow list below passes through as written. A new or reworded writer therefore
// reads as fixed words, never as a leak; to show a new memo, add its exact shape here. The ledger keeps the
// full memo for staff views.
var (
	// safeMemoShapes are memos that carry only the account's own document numbers and fixed words.
	safeMemoShapes = []*regexp.Regexp{
		regexp.MustCompile(`^NSF fee$`),
		regexp.MustCompile(`^(voided receipt|reversal|NSF reversal): early payment discount given back$`),
		regexp.MustCompile(`^early payment discount \([#A-Za-z0-9._/-]+\)$`),
		regexp.MustCompile(`^overpaid receipt left on account \([#A-Za-z0-9._/-]+\)$`),
		regexp.MustCompile(`^Holdback withheld on [#A-Za-z0-9._/-]+$`),
		regexp.MustCompile(`^[#A-Za-z0-9._/-]+ \(holdback released \d{4}-\d{2}-\d{2}\)$`),
		regexp.MustCompile(`^Credit memo [#A-Za-z0-9-]+$`),
		regexp.MustCompile(`^finance charge for [A-Za-z]+ \d{4}$`),
	}
)

// statementKnownWords are the undo and credit writes whose memo is a fixed prefix plus the clerk's reason,
// given words of their own so the customer reads what happened.
var statementKnownWords = []struct{ prefix, words string }{
	{"voided receipt: ", "Payment voided"},
	{"reversal: ", "Payment reversed"},
	{"re-applied from a reversed payment: ", "Payment applied"},
	{"NSF reversal of payment ", "Returned payment"},
	{"NSF reversal: ", "Returned payment"},
	{"credit memo applied: ", "Credit memo applied"},
	{"credit applied to ", "Credit applied"},
	{"credit application reversed: ", "Credit application reversed"},
	{"credit memo restored: ", "Credit memo restored"},
	{"write off reversed ", "Write-off reversed"},
	{"write off ", "Write-off"},
}

// statementDescription turns a subledger row into a line a customer can read without a glossary. Nothing a
// clerk typed and no internal id reaches it unless the memo is an allow listed shape or is the document's own
// number.
func statementDescription(txnType, sourceType, memo, docNo, reversesNo string) string {
	if memo != "" && memo == docNo {
		return memo
	}
	for _, re := range safeMemoShapes {
		if re.MatchString(memo) {
			return memo
		}
	}
	// The known prefixes run before the credit memo branch: a counter return credit applied or reversed copies
	// its CREDIT_MEMO type onto the offsetting row, and that row says what happened, not "Credit memo". The
	// prefixes are lower case and can never start a credit memo's own number.
	for _, f := range statementKnownWords {
		if strings.HasPrefix(memo, f.prefix) {
			return f.words
		}
	}
	if txnType == "CREDIT_MEMO" {
		// The row's own document number and the reversed invoice's number, both read from the invoices by
		// id (the joined docNo and reversesNo); nothing is taken from the memo's text.
		if docNo != "" {
			if reversesNo != "" {
				return docNo + ", reverses " + reversesNo
			}
			return docNo
		}
		return "Credit memo"
	}
	switch txnType {
	case "INVOICE":
		return "Invoice"
	case "PAYMENT":
		switch sourceType {
		case "payment_void":
			return "Payment voided"
		case "payment_reversal":
			return "Payment reversed"
		case "payment_reapply":
			return "Payment applied"
		}
		return "Payment received, thank you"
	case "NSF":
		return "Returned payment"
	case "WRITE_OFF":
		return "Write-off"
	case "HOLDBACK":
		return "Holdback withheld"
	case "HOLDBACK_RELEASED":
		return "Holdback released"
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
		  WHERE id = NULLIF($1, '')::uuid OR ($1 = '' AND loc_type = 'BRANCH')
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

// LatestStatementIn is LatestStatement inside a branch reach: only a statement of a run filed under a
// branch of the reach (a run of the whole book has no branch and is every branch's, so a reach
// limited caller does not read it).
func (r *Repository) LatestStatementIn(ctx context.Context, customerID string, reach BranchReach) (Statement, bool, error) {
	if r.pool == nil {
		return Statement{}, false, errNoDB
	}
	var s Statement
	err := r.pool.QueryRow(ctx,
		`SELECT st.id::text, st.run_id::text, st.customer_id::text, st.as_of_date, st.current_cents, st.days_30_cents,
		        st.days_60_cents, st.days_90_plus_cents, st.closing_balance_cents, st.html, st.delivery_status, st.created_at
		   FROM ar_statements st
		   JOIN ar_statement_runs ru ON ru.id = st.run_id
		  WHERE st.customer_id = $1::uuid
		    AND ($2::uuid[] IS NULL OR ru.branch_id = ANY($2::uuid[]))
		  ORDER BY st.as_of_date DESC, st.created_at DESC LIMIT 1`, customerID, reach.SQLArg()).
		Scan(&s.ID, &s.RunID, &s.CustomerID, &s.AsOfDate, &s.CurrentCents, &s.Days30Cents,
			&s.Days60Cents, &s.Days90PlusCents, &s.ClosingCents, &s.HTML, &s.DeliveryStatus, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Statement{}, false, nil
		}
		return Statement{}, false, fmt.Errorf("billing: latest statement in reach: %w", err)
	}
	return s, true, nil
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

// PreviewStatement reads a single ar_statements row, asserting the row belongs to the
// customer the URL named. A statement of a different customer is ErrNotFound, the same
// posture customer_ar_read.go applies on its existence reads: no existence leak across
// the account wall.
//
// The customers join is not necessary here: the row carries customer_id, and the predicate
// `customer_id = $2::uuid` is the only filter. The read lives in statement_repository.go
// because that file is the allowlisted owner of ar_statements; the customers predicate is
// on the SAME row (the ar_statements row, billing's own).
func (r *Repository) PreviewStatement(ctx context.Context, statementID, customerID string) (Statement, error) {
	if r.pool == nil {
		return Statement{}, errNoDB
	}
	var s Statement
	err := r.pool.QueryRow(ctx,
		`SELECT st.id::text, st.run_id::text, st.customer_id::text, COALESCE(c.home_branch_id::text, ''),
		        st.as_of_date, st.current_cents, st.days_30_cents,
		        st.days_60_cents, st.days_90_plus_cents, st.closing_balance_cents, '', st.delivery_status, st.created_at
		   FROM ar_statements st
		   LEFT JOIN customers c ON c.id = st.customer_id
		  WHERE st.id = $1::uuid AND st.customer_id = $2::uuid`, statementID, customerID).
		Scan(&s.ID, &s.RunID, &s.CustomerID, &s.HomeBranchID, &s.AsOfDate, &s.CurrentCents, &s.Days30Cents,
			&s.Days60Cents, &s.Days90PlusCents, &s.ClosingCents, &s.HTML, &s.DeliveryStatus, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Statement{}, ErrNotFound
		}
		return Statement{}, fmt.Errorf("billing: read statement: %w", err)
	}
	return s, nil
}

// PreviewLines reads the open invoices the statement preview lists, with the days-overdue
// figure derived from as_of (the statement's own date). The bucket label is computed
// server-side from days_overdue by the handler, never in the browser.
//
// The query joins customers only via the customer_id predicate (customers.id is referenced
// to scope, the row read is the invoices row, billing's own). The invoices table is
// billing's own, declared in the allowlist for statement_repository.go's parent callers.
func (r *Repository) PreviewLines(ctx context.Context, customerID string, asOf time.Time) ([]StatementPreviewLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	zone, err := r.zoneFor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx,
		`WITH inv AS (
			SELECT i.id, i.invoice_no, i.due_date,
			       to_char(invoice_released_on(i.id, now()), 'YYYY-MM-DD') AS rel_on,
			       i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0)) AS net,
			       invoice_released_cents_as_of(i.id, now()) AS rel_cents,
			       GREATEST(invoice_released_on(i.id, now()) + GREATEST(i.due_date - i.issue_date, 0), i.due_date) AS rel_due
			  FROM invoices i
			 WHERE i.customer_id = $1::uuid
			   AND i.status IN ('FINALIZED','PARTIALLY_PAID')
			   AND i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0)) <> 0
		), open_lines AS (
			SELECT inv.id::text AS doc_id,
			       inv.invoice_no::text || x.label AS doc_no,
			       x.due_date,
			       x.bal AS balance_cents,
			       GREATEST(0, (($2::date - x.due_date))::int) AS days_overdue
			  FROM inv
			  CROSS JOIN LATERAL (VALUES
			        (inv.net - LEAST(inv.rel_cents, GREATEST(inv.net, 0)), inv.due_date, ''),
			        (LEAST(inv.rel_cents, GREATEST(inv.net, 0)), inv.rel_due, ' (holdback released ' || COALESCE(inv.rel_on, '') || ')')) AS x(bal, due_date, label)
			 WHERE x.bal <> 0
			UNION ALL
			SELECT COALESCE(ct.source_ref_id, ct.id)::text AS doc_id,
			       COALESCE(NULLIF(MIN(ct.memo), ''), MIN(ct.txn_type)) AS doc_no,
			       MIN((ct.occurred_at AT TIME ZONE $3::text)::date) AS due_date,
			       SUM(ct.amount_cents) AS balance_cents,
			       GREATEST(0, (($2::date - MIN((ct.occurred_at AT TIME ZONE $3::text)::date)))::int) AS days_overdue
			  FROM customer_transactions ct
			 WHERE ct.customer_id = $1::uuid
			   AND ct.txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF')
			   AND ct.source_type <> 'invoice'
			 GROUP BY ct.source_type, COALESCE(ct.source_ref_id, ct.id)
			HAVING SUM(ct.amount_cents) <> 0
		)
		SELECT doc_id, doc_no, due_date, balance_cents, days_overdue
		  FROM open_lines
		 ORDER BY due_date ASC, doc_id ASC`, customerID, houseClockIn(zone, asOf), zone.Name())
	if err != nil {
		return nil, fmt.Errorf("billing: list statement preview lines: %w", err)
	}
	defer rows.Close()
	out := []StatementPreviewLine{}
	for rows.Next() {
		var l StatementPreviewLine
		if err := rows.Scan(&l.InvoiceID, &l.InvoiceNo, &l.DueDate, &l.BalanceCents, &l.DaysOverdue); err != nil {
			return nil, fmt.Errorf("billing: scan statement preview line: %w", err)
		}
		l.BucketLabel = BucketForLabel(l.DaysOverdue)
		out = append(out, l)
	}
	return out, rows.Err()
}

// PreviewAging reads the 5-bucket aging for the preview, inside the customer_ar_read.go
// discipline: the SAME five buckets the workspace and the AR console open with, no
// re-bucketing. The handler reconciles to the penny: sum of buckets equals openCents and
// sum of late buckets equals overdueCents.
func (r *Repository) PreviewAging(ctx context.Context, customerID string) (StatementPreviewAging, error) {
	if r.pool == nil {
		return StatementPreviewAging{}, errNoDB
	}
	zone, err := r.zoneFor(ctx)
	if err != nil {
		return StatementPreviewAging{}, err
	}
	today := houseClockIn(zone, time.Now()).Format("2006-01-02")
	var a StatementPreviewAging
	err = r.pool.QueryRow(ctx,
		`WITH inv AS (
			SELECT balance_cents - LEAST(GREATEST(holdback_cents, 0), GREATEST(balance_cents, 0)) AS net, due_date,
			       invoice_released_cents_as_of(id, now()) AS rel_cents,
			       GREATEST(invoice_released_on(id, now()) + GREATEST(due_date - issue_date, 0), due_date) AS rel_due
			  FROM invoices
			 WHERE customer_id = $1::uuid
			   AND status IN ('FINALIZED','PARTIALLY_PAID')
			   AND balance_cents - LEAST(GREATEST(holdback_cents, 0), GREATEST(balance_cents, 0)) <> 0
		), open_ar AS (
			SELECT x.bal AS balance_cents, x.due_date
			  FROM inv
			  CROSS JOIN LATERAL (VALUES
			        (inv.net - LEAST(inv.rel_cents, GREATEST(inv.net, 0)), inv.due_date),
			        (LEAST(inv.rel_cents, GREATEST(inv.net, 0)), inv.rel_due)) AS x(bal, due_date)
			 WHERE x.bal <> 0
			UNION ALL
			SELECT SUM(amount_cents) AS balance_cents,
			       MIN((occurred_at AT TIME ZONE $3::text)::date) AS due_date
			  FROM customer_transactions
			 WHERE customer_id = $1::uuid
			   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF')
			   AND source_type <> 'invoice'
			 GROUP BY source_type, COALESCE(source_ref_id, id)
			HAVING SUM(amount_cents) <> 0
		)
		SELECT COALESCE(SUM(CASE WHEN ($2::date - due_date) <= 0 THEN balance_cents ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN ($2::date - due_date) BETWEEN 1 AND 30 THEN balance_cents ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN ($2::date - due_date) BETWEEN 31 AND 60 THEN balance_cents ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN ($2::date - due_date) BETWEEN 61 AND 90 THEN balance_cents ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN ($2::date - due_date) > 90 THEN balance_cents ELSE 0 END), 0),
		       COALESCE(SUM(balance_cents), 0),
		       COALESCE(SUM(CASE WHEN ($2::date - due_date) > 0 THEN balance_cents ELSE 0 END), 0),
		       (SELECT COALESCE(SUM(GREATEST(holdback_cents, 0)), 0) FROM invoices
		         WHERE customer_id = $1::uuid AND doc_type = 'INVOICE' AND status IN ('FINALIZED','PARTIALLY_PAID'))
		  FROM open_ar`, customerID, today, zone.Name()).
		Scan(&a.CurrentCents, &a.Days30Cents, &a.Days60Cents, &a.Days90Cents, &a.Days90PlusCents,
			&a.OpenCents, &a.OverdueCents, &a.HoldbackCents)
	if err != nil {
		return StatementPreviewAging{}, fmt.Errorf("billing: read statement preview aging: %w", err)
	}
	return a, nil
}

// RecordPaylinkDelivery writes the ar_invoice_deliveries row, idempotent on the
// deterministic key. kind = STATEMENT is the existing 0175 bucket a paylink delivery
// fits under: it is a notice about the customer's account state, and tone = ” satisfies
// ar_invoice_deliveries_tone_is_reminder (0175).
func (r *Repository) RecordPaylinkDelivery(ctx context.Context, row PaylinkDeliveryRow) error {
	if r.pool == nil {
		return errNoDB
	}
	invoiceID := row.CustomerID
	if row.InvoiceID != nil && *row.InvoiceID != "" {
		invoiceID = *row.InvoiceID
	}
	var balanceCents int64
	if row.AmountCents != nil && *row.AmountCents > 0 {
		balanceCents = *row.AmountCents
	}
	var sentBy any
	if row.SentBy != "" {
		sentBy = row.SentBy
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO ar_invoice_deliveries
		   (invoice_id, customer_id, invoice_no, kind, tone, channel, to_address,
		    balance_cents, idempotency_key, sent_by, sent_at)
		 VALUES ($1::uuid, $2::uuid, '', 'STATEMENT', '', $3, $4, $5, $6, $7::uuid, $8)
		 ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING`,
		invoiceID, row.CustomerID, row.Channel, row.Recipient,
		balanceCents, row.IdemKey, sentBy, row.SentAt)
	if err != nil {
		return fmt.Errorf("billing: record paylink delivery: %w", err)
	}
	return nil
}
