package billing

// customer_ar_workspace_store.go, the customer account workspace read and the
// customer_ar_notes read + write that the workspace-activity tab renders.
//
// -- WHY THIS FILE EXISTS SEPARATELY --------------------------------------------------------------
// CustomerAging and CustomerActivity in customer_ar_read.go are the building blocks an external
// module calls for ONE fact each. The workspace is a composed panel that no other module needs,
// and pulling the read here keeps customer_ar_read.go's "the load-bearing assertion is one
// fact, not a panel" rule intact. The aging comes back through CustomerAging, the activity
// through CustomerActivity, and this file only adds the workspace-shaped joins: the header
// card, the open/closed/disputed invoice lists, payments, the credit notes (CREDIT_MEMO
// invoices with their own balance), the statements list and the notes CRUD the activity tab
// calls POST for.
//
// -- WHAT THIS FILE DOES NOT DO ------------------------------------------------------------------
// It does not re-bucket the aging. It asks CustomerAging, the AR console's own read, and
// renders that, exactly the discipline customer_ar_read.go's header spends thirty lines on.
// It does not re-time the activity. It asks CustomerActivity, the AR subledger read, and
// renders that. Two reads, one panel: when the aging rules or the activity vocabulary
// change tomorrow, the workspace follows for the same reason the rest of the system does.
//
// -- customer_ar_notes ----------------------------------------------
// The free-text seam behind the activity tab's POST and behind the workspace's right-hand
// "pinned note" summary card. Outbound copy never lives here (that is messaging); a note
// is a fact a collections person leaves for the next collections person. The row's pinned
// flag is the only state that gets a unique constraint, because two pinned notes on one
// account is a UI bug, not a richer record.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// -- workspace payload ---------------------------------------------------------------------------

// CustomerWorkspaceHeader is the account card the workspace's top bar carries: name, account
// code, status badge, credit limit, utilization bar, terms, primary AP contact, last payment
// and the 5-bucket aging that opens with it.
//
// OpenCents is the same figure CustomerAging opens with (TotalCents, every open invoice balance,
// current and late). The workspace does not re-sum it, by the same rule the file header opens
// with. AvailableCreditCents is the same CreditLimitCents - OpenCents computation the account
// position card on the invoice detail carries, deliberately: the workspace account card and
// the invoice detail account card cannot quote different available figures for the same
// account.
//
// UtilizationBps is OpenCents * 10000 / CreditLimitCents, the basis points figure the bar
// renders against. A dealer with a zero limit gets 0; a dealer whose account is over the
// limit gets 10000+ (the bar reads "over limit" past 10000, an honest answer).
type CustomerWorkspaceHeader struct {
	CustomerID           string     `json:"customer_id"`
	Name                 string     `json:"name"`
	AccountNo            string     `json:"account_no"`
	Status               string     `json:"status"` // CURRENT | CREDIT_APPROVED | CREDIT_HOLD | UNDER_REVIEW
	TermsDays            int        `json:"terms_days"`
	CreditLimitCents     int64      `json:"credit_limit_cents"`
	OpenCents            int64      `json:"open_cents"`
	AvailableCreditCents int64      `json:"available_credit_cents"`
	OverdueCents         int64      `json:"overdue_cents"`
	UtilizationBps       int        `json:"utilization_bps"` // 0..10000+, basis points of the limit used
	PrimaryAPContactName string     `json:"primary_ap_contact_name,omitempty"`
	PrimaryAPContactEmail string    `json:"primary_ap_contact_email,omitempty"`
	PrimaryAPContactPhone string    `json:"primary_ap_contact_phone,omitempty"`
	LastPaymentAt        *time.Time `json:"last_payment_at,omitempty"`
	LastPaymentCents     int64      `json:"last_payment_cents"`
	Aging                CustomerWorkspaceAging `json:"aging"`
}

// CustomerWorkspaceAging is the 5-bucket breakdown the Overview tab opens with. It is the
// SAME five buckets agingSelect renders (finstmt.agingBucketOrder); any panel that names
// "5-bucket aging" reads from the same SQL aggregate. Reconciliation to the penny with S0
// (server reconciliation primitive) means: the five buckets here sum to OpenCents and the
// arrears buckets sum to OverdueCents, both checked at read time so a drift fails the read.
type CustomerWorkspaceAging struct {
	CurrentCents    int64 `json:"current_cents"`
	Days30Cents     int64 `json:"days_30_cents"`
	Days60Cents     int64 `json:"days_60_cents"`
	Days90Cents     int64 `json:"days_90_cents"`
	Days90PlusCents int64 `json:"days_90_plus_cents"`
	OpenCents       int64 `json:"open_cents"`
	OverdueCents    int64 `json:"overdue_cents"`
}

// ReconcilesToThePenny is the load-bearing assertion the workspace makes of its own 5-bucket
// row: the five buckets sum to OpenCents, and the four late buckets sum to OverdueCents.
// Reconciliation is checked at read time, not on the screen, because the screen is the
// thing we are about to be argued with.
func (a CustomerWorkspaceAging) ReconcilesToThePenny() bool {
	sum := a.CurrentCents + a.Days30Cents + a.Days60Cents + a.Days90Cents + a.Days90PlusCents
	if sum != a.OpenCents {
		return false
	}
	return a.Days30Cents+a.Days60Cents+a.Days90Cents+a.Days90PlusCents == a.OverdueCents
}

// CustomerWorkspaceInvoice is one invoice row the workspace's Invoices tab carries. Three
// kinds land here: open, closed and disputed. The "open" list mixes current and overdue so
// the operator does not have to switch tabs to find them, and the bucket the row sits in is
// DaysOverdueBucket ("Current" / "1 to 30" / "31 to 60" / "61 to 90" / "91 and over") so the
// word on screen matches the bucket the Overview tab opened with.
type CustomerWorkspaceInvoice struct {
	ID                string    `json:"id"`
	InvoiceNo         string    `json:"invoice_no"`
	DocType           string    `json:"doc_type"` // INVOICE | CREDIT_MEMO
	Status            string    `json:"status"`   // stored: DRAFT/FINALIZED/PARTIALLY_PAID/PAID/VOID
	IssueDate         time.Time `json:"issue_date"`
	DueDate           time.Time `json:"due_date"`
	TotalCents        int64     `json:"total_cents"`
	BalanceCents      int64     `json:"balance_cents"`
	DaysOverdue       int       `json:"days_overdue"`
	DaysOverdueBucket string    `json:"days_overdue_bucket"`
	IsDisputed        bool      `json:"is_disputed"`
	HasOpenPromise    bool      `json:"has_open_promise"`
}

// CustomerWorkspacePayment is one payment the Payments & Credits tab carries, with the
// running unapplied figure so the inline "Apply Credit Note" modal can pick the right row.
// The payment trail is a payments module concern; the workspace reads it through the same
// payment_allocations join the invoice detail uses, so the workspace and the invoice detail
// cannot disagree about whether a payment was applied.
type CustomerWorkspacePayment struct {
	ID             string    `json:"id"`
	PaymentNo      string    `json:"payment_no"`
	ReceivedAt     time.Time `json:"received_at"`
	AmountCents    int64     `json:"amount_cents"`
	Method         string    `json:"method"`          // CASH / CARD / CHEQUE / EFT / OTHER
	UnappliedCents int64     `json:"unapplied_cents"` // AmountCents - SUM(payment_allocations.amount_cents)
	BranchID       string    `json:"branch_id,omitempty"`
}

// CustomerWorkspaceCreditNote is a CREDIT_MEMO invoice row the Payments & Credits tab lists
// separately from payments. "Unapplied" here is a derived figure: total_cents minus the
// applied (the negative-balance the credit memo carries). A credit memo the dealer has not
// yet applied is the credit the operator wants surfaced, because that is the row that can
// move money today.
type CustomerWorkspaceCreditNote struct {
	ID             string    `json:"id"`
	MemoNo         string    `json:"memo_no"`
	IssuedAt       time.Time `json:"issued_at"`
	AmountCents    int64     `json:"amount_cents"`
	UnappliedCents int64     `json:"unapplied_cents"`
	Reason         string    `json:"reason,omitempty"`
}

// CustomerWorkspaceStatement is one statement row from ar_statements, the Statements tab's
// list. Newest first. as_of_date and the four buckets are stored at statement-generation
// time (0105's "a statement is an artefact, not a query" rule), so the row carries the
// figures that went out the door on that date.
type CustomerWorkspaceStatement struct {
	ID                 string    `json:"id"`
	RunID              string    `json:"run_id"`
	Mode               string    `json:"mode"`              // MASS | INTERIM
	AsOfDate           time.Time `json:"as_of_date"`
	ClosingBalanceCents int64    `json:"closing_balance_cents"`
	DeliveryStatus     string    `json:"delivery_status"`   // NOT_QUEUED / QUEUED / SENT / FAILED
	CreatedAt          time.Time `json:"created_at"`
}

// CustomerWorkspaceNote is one customer_ar_notes row, the Activity tab's note list. The
// pinned flag is the only one that gets a unique constraint (0405's partial unique index);
// the body is what a collections person wrote.
type CustomerWorkspaceNote struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`    // NOTE / CALL / VISIT / EMAIL_RECEIVED
	Body       string    `json:"body"`
	AuthorID   string    `json:"author_id,omitempty"`
	AuthorName string    `json:"author_name,omitempty"`
	Pinned     bool      `json:"pinned"`
	CreatedAt  time.Time `json:"created_at"`
}

// CustomerWorkspace is the one read the GET handler returns, the whole panel in one round
// trip. Five sub-payloads, each one a list the corresponding tab renders. A nil list
// marshals as null in Go and as a missing field in JSON, so every list is initialised to
// its empty slice before being filled, by the same rule the register's handler applies.
type CustomerWorkspace struct {
	Header         CustomerWorkspaceHeader       `json:"header"`
	InvoicesOpen    []CustomerWorkspaceInvoice    `json:"invoices_open"`
	InvoicesClosed  []CustomerWorkspaceInvoice    `json:"invoices_closed"`
	InvoicesDisputed []CustomerWorkspaceInvoice   `json:"invoices_disputed"`
	Payments       []CustomerWorkspacePayment    `json:"payments"`
	CreditNotes    []CustomerWorkspaceCreditNote `json:"credit_notes"`
	Statements     []CustomerWorkspaceStatement  `json:"statements"`
	Notes          []CustomerWorkspaceNote       `json:"notes"`
	Activity       []ARActivity                  `json:"activity"` // last 10 across invoices/payments/credits/promises/disputes
	AsOf           time.Time                     `json:"as_of"`
}

// -- workspace store seam ------------------------------------------------------------------------

// WorkspaceStore is the read seam behind GET /api/v1/billing/ar/customers/{id}/workspace.
// Repository implements it; tests can fake one without faking the whole repository.
type WorkspaceStore interface {
	WorkspaceHeader(ctx context.Context, customerID string) (CustomerWorkspaceHeader, error)
	WorkspaceInvoices(ctx context.Context, customerID string) (open, closed, disputed []CustomerWorkspaceInvoice, err error)
	WorkspacePayments(ctx context.Context, customerID string) ([]CustomerWorkspacePayment, error)
	WorkspaceCreditNotes(ctx context.Context, customerID string) ([]CustomerWorkspaceCreditNote, error)
	WorkspaceStatements(ctx context.Context, customerID string) ([]CustomerWorkspaceStatement, error)
	WorkspaceNotes(ctx context.Context, customerID string, limit int) ([]CustomerWorkspaceNote, error)
	AddNote(ctx context.Context, customerID, authorID string, in AddNoteInput) (CustomerWorkspaceNote, error)
}

// Compile-time check that Repository satisfies the seam, so a signature drift fails the
// build rather than the route registration.
var _ WorkspaceStore = (*Repository)(nil)

// poolHandle exposes the connection pool to the workspace handler when one needs its
// own transaction. Today AddNote is on the seam, so no external handler does, but the
// helper stays here so the day a workspace-only transaction lands the seam does not
// widen. A unit-test fake is the obvious type to fail at the type assertion.
func (r *Repository) poolHandle() *pgxpool.Pool { return r.pool }

// WorkspaceInvoices returns three lists: open (FINALIZED + PARTIALLY_PAID with non-zero
// balance, current and overdue), closed (PAID + VOID), and disputed (FINALIZED +
// PARTIALLY_PAID that have an OPEN dispute row). A disputed open invoice appears in BOTH
// open and disputed, because the screen that renders "open" and the screen that renders
// "disputed" answer different questions and an operator looking at one still has to know
// about the other. Each row carries the 5-bucket label the Overview tab opened with.
//
// CREDIT_MEMO invoices (0146's doc_type widening) are routed to WorkspaceCreditNotes
// instead, because the credit notes view answers a different question (unapplied balance,
// apply to which invoice) and mixing the two in one list would put credit memos under
// "open invoices" in a way the operator has to unlearn.
func (r *Repository) WorkspaceInvoices(ctx context.Context, customerID string) (open, closed, disputed []CustomerWorkspaceInvoice, err error) {
	if r.pool == nil {
		return nil, nil, nil, errNoDB
	}
	if !looksLikeUUIDish(customerID) {
		return nil, nil, nil, nil
	}
	zone, zerr := r.zoneFor(ctx)
	if zerr != nil {
		return nil, nil, nil, zerr
	}
	_ = zone
	asOf, err := r.clockAt(ctx, time.Now())
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT i.id::text, i.invoice_no::text, i.doc_type, i.status, i.issue_date, i.due_date,
		        i.total_cents, i.balance_cents,
		        GREATEST(0, ((($2::date - i.due_date))::int)),
		        EXISTS (SELECT 1 FROM ar_invoice_disputes d WHERE d.invoice_id = i.id AND d.status = 'OPEN'),
		        EXISTS (SELECT 1 FROM ar_promises_to_pay pr WHERE pr.invoice_id = i.id AND pr.status = 'OPEN')
		   FROM invoices i
		  WHERE i.customer_id = $1::uuid
		    AND i.doc_type = 'INVOICE'
		  ORDER BY i.issue_date DESC`, customerID, asOf)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("billing: workspace invoices: %w", err)
	}
	defer rows.Close()
	open = []CustomerWorkspaceInvoice{}
	closed = []CustomerWorkspaceInvoice{}
	disputed = []CustomerWorkspaceInvoice{}
	for rows.Next() {
		var inv CustomerWorkspaceInvoice
		if err := rows.Scan(&inv.ID, &inv.InvoiceNo, &inv.DocType, &inv.Status, &inv.IssueDate, &inv.DueDate,
			&inv.TotalCents, &inv.BalanceCents, &inv.DaysOverdue, &inv.IsDisputed, &inv.HasOpenPromise); err != nil {
			return nil, nil, nil, fmt.Errorf("billing: scan workspace invoice: %w", err)
		}
		inv.DaysOverdueBucket = BucketForLabel(inv.DaysOverdue)
		switch {
		case inv.IsDisputed:
			disputed = append(disputed, inv)
			if inv.Status == "FINALIZED" || inv.Status == "PARTIALLY_PAID" {
				open = append(open, inv)
			}
		case inv.Status == "FINALIZED" || inv.Status == "PARTIALLY_PAID":
			open = append(open, inv)
		default:
			closed = append(closed, inv)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	return open, closed, disputed, nil
}

// BucketForLabel answers the 5-bucket label an invoice row carries. The boundaries match
// finstmt.agingBucketOrder, by design: the row label and the Overview tab label are the
// same words. Same shape the customer_ar_read.go AgingRow.OldestOverdueBucket returns,
// just enumerated for the 5-bucket bar.
func BucketForLabel(daysOverdue int) string {
	switch {
	case daysOverdue <= 0:
		return "Current"
	case daysOverdue <= 30:
		return "1 to 30"
	case daysOverdue <= 60:
		return "31 to 60"
	case daysOverdue <= 90:
		return "61 to 90"
	default:
		return "91 and over"
	}
}

// WorkspaceCreditNotes lists the account's credit memos (CREDIT_MEMO invoices, 0146's
// widening of doc_type). "Unapplied" here is the balance the credit memo still carries:
// balance_cents is stored as a NEGATIVE on a credit memo, the same shape the AR subledger
// sees, and -balance_cents is the figure the Apply Credit Note modal binds. A credit
// memo whose balance is zero is still rendered, with the applied-in-full state implicit in
// the zero; that is the truth the row carries.
//
// A VOID credit memo is filtered out: a voided credit is a refund we did not actually
// issue, and surfacing it would let the operator try to apply money that is not there.
func (r *Repository) WorkspaceCreditNotes(ctx context.Context, customerID string) ([]CustomerWorkspaceCreditNote, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUIDish(customerID) {
		return nil, nil
	}
	out := []CustomerWorkspaceCreditNote{}
	rows, err := r.pool.Query(ctx,
		`SELECT i.id::text, i.invoice_no::text, i.issue_date, i.total_cents, i.balance_cents,
		        COALESCE(i.metadata->>'reason', '')
		   FROM invoices i
		  WHERE i.customer_id = $1::uuid
		    AND i.doc_type = 'CREDIT_MEMO'
		    AND i.status <> 'VOID'
		  ORDER BY i.issue_date DESC
		  LIMIT 200`, customerID)
	if err != nil {
		return nil, fmt.Errorf("billing: workspace credit notes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n CustomerWorkspaceCreditNote
		if err := rows.Scan(&n.ID, &n.MemoNo, &n.IssuedAt, &n.AmountCents, &n.UnappliedCents, &n.Reason); err != nil {
			return nil, fmt.Errorf("billing: scan workspace credit note: %w", err)
		}
		// balance_cents on a CREDIT_MEMO is negative; unapplied is its absolute value.
		if n.UnappliedCents < 0 {
			n.UnappliedCents = -n.UnappliedCents
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// WorkspaceStatements lists the account's statements from ar_statements, newest first.
// The figure on each row is the figure that went out the door (0105's artefact rule).
func (r *Repository) WorkspaceStatements(ctx context.Context, customerID string) ([]CustomerWorkspaceStatement, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUIDish(customerID) {
		return nil, nil
	}
	out := []CustomerWorkspaceStatement{}
	rows, err := r.pool.Query(ctx,
		`SELECT st.id::text, st.run_id::text, run.mode::text, st.as_of_date, st.closing_balance_cents,
		        st.delivery_status, st.created_at
		   FROM ar_statements st
		   JOIN ar_statement_runs run ON run.id = st.run_id
		  WHERE st.customer_id = $1::uuid
		  ORDER BY st.as_of_date DESC, st.created_at DESC
		  LIMIT 100`, customerID)
	if err != nil {
		return nil, fmt.Errorf("billing: workspace statements: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s CustomerWorkspaceStatement
		if err := rows.Scan(&s.ID, &s.RunID, &s.Mode, &s.AsOfDate, &s.ClosingBalanceCents,
			&s.DeliveryStatus, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("billing: scan workspace statement: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// -- the write -----------------------------------------------------------------------------------

// AddNoteInput is the POST body. Body is required, kind defaults to NOTE, pinned defaults
// to false (and is enforced at most one per account by 0405's partial unique index).
type AddNoteInput struct {
	Body   string `json:"body"`
	Kind   string `json:"kind,omitempty"` // NOTE|CALL|VISIT|EMAIL_RECEIVED
	Pinned bool   `json:"pinned,omitempty"`
}

// AddNoteOutput is the POST response: the saved note with author and pin state materialised.
type AddNoteOutput struct {
	Note CustomerWorkspaceNote `json:"note"`
}

// AddNote writes one customer_ar_notes row for the account. The author is the caller; the
// row's created_at / updated_at are server time (the row default).
//
// Pinned=true first UN-pins any existing pinned note on the same account, then INSERTs the
// new one. The partial unique index 0405 declares would otherwise refuse the second pinned
// row, so this is the order: write the unpin, then write the new pin. The two statements
// run inside a transaction so a failure between them leaves the account pinned the way it
// was. Author is recorded; if it is empty (a unit test, or an external caller), the row is
// still written with author_id NULL.
func (r *Repository) AddNote(ctx context.Context, customerID, authorID string, in AddNoteInput) (CustomerWorkspaceNote, error) {
	if r.pool == nil {
		return CustomerWorkspaceNote{}, errNoDB
	}
	if !looksLikeUUIDish(customerID) {
		return CustomerWorkspaceNote{}, httpx.BadRequestf("invalid customer id")
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return CustomerWorkspaceNote{}, httpx.BadRequestf("note body is required")
	}
	if len(body) > 4096 {
		return CustomerWorkspaceNote{}, httpx.BadRequestf("note body exceeds 4096 characters")
	}
	kind := strings.ToUpper(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = "NOTE"
	}
	switch kind {
	case "NOTE", "CALL", "VISIT", "EMAIL_RECEIVED":
	default:
		return CustomerWorkspaceNote{}, httpx.BadRequestf("note kind must be NOTE|CALL|VISIT|EMAIL_RECEIVED")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return CustomerWorkspaceNote{}, fmt.Errorf("billing: begin add note: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if in.Pinned {
		if _, err := tx.Exec(ctx,
			`UPDATE customer_ar_notes SET pinned = FALSE, updated_at = now()
			  WHERE customer_id = $1::uuid AND pinned = TRUE`, customerID); err != nil {
			return CustomerWorkspaceNote{}, fmt.Errorf("billing: unpin prior note: %w", err)
		}
	}
	var (
		noteID    string
		createdAt time.Time
	)
	err = tx.QueryRow(ctx,
		`INSERT INTO customer_ar_notes (customer_id, author_id, body, kind, pinned)
		 VALUES ($1::uuid, NULLIF($2,'')::uuid, $3, $4, $5)
		 RETURNING id::text, created_at`,
		customerID, authorID, body, kind, in.Pinned).
		Scan(&noteID, &createdAt)
	if err != nil {
		return CustomerWorkspaceNote{}, fmt.Errorf("billing: insert note: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CustomerWorkspaceNote{}, fmt.Errorf("billing: commit add note: %w", err)
	}
	author := r.resolveWorkspaceNoteAuthor(ctx, authorID)
	return CustomerWorkspaceNote{
		ID: noteID, Kind: kind, Body: body, AuthorID: authorID, AuthorName: author,
		Pinned: in.Pinned, CreatedAt: createdAt,
	}, nil
}