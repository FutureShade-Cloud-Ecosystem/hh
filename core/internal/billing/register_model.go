package billing

// register_model.go, DIB-510: the STAFF-SIDE receivables register.
//
// -- WHAT THIS IS, AND WHAT ALREADY EXISTED ----------------------------------------------------
// Until now `invoices` had exactly two readers: the customer portal (portal/invoices.go, one
// tenant's own documents) and the AR month-end console (aging, holds, statement runs, ACCOUNT-level
// rollups, never the document list). Nobody at the counter or in the office could open "the invoice
// book" and work it. This file is the shape of that book.
//
// It reads what is already there. Every money figure below is a STORED column or a SQL aggregate over
// stored columns; nothing in this package computes a balance, a tax or a total, and nothing recomputes
// aging, the buckets stay agingSelect's business, for the reason customer_ar_read.go spends thirty
// lines on.
//
// -- THE ONE RULE THAT SHAPES THE WHOLE PAYLOAD ------------------------------------------------
// An INVOICE'S OWN FIGURES ARE NOT SENSITIVE. Subtotal, tax, total, balance, days overdue, the
// customer is holding that document; a counter clerk taking a cheque needs every one of them, and a
// screen that hid them would be hiding the customer's own paperwork from the person serving them.
//
// THE ACCOUNT'S POSITION IS. "This contractor is CA$81k into a CA$60k limit" is a fact about the
// customer's business rather than about this transaction. It is what internal/till's AR reads are
// already manager-gated for, in as many words, and it is gated here to the same tier, by BYTE
// ABSENCE at the marshal layer (ar_invoice_policy.account_position_visibility, fail closed), never by
// a CSS class. AccountPosition is therefore a POINTER on both the row and the detail: a refused
// caller receives no field at all, and the screen renders nothing because there is nothing to render.
//
// Cost and margin are not on this screen in any form, at any tier, under any policy value.

import "time"

// -- policy -------------------------------------------------------------------------------------

// RegisterPolicy is the ar_invoice_policy row for a branch (0178), resolved NULL-row-then-branch by
// the 0043/0173 idiom. It is the TYPED half of the D-105 hybrid, the knobs the SERVER enforces
// whatever the screen happens to show.
type RegisterPolicy struct {
	BranchID string `json:"branch_id,omitempty"`
	// AccountPositionVisibility: MANAGER_UP (default) | ALL_STAFF | HIDDEN.
	AccountPositionVisibility string `json:"account_position_visibility"`
	DueSoonDays               int    `json:"due_soon_days"`
	// ReminderTones is the ordered set of tones this dealer permits. A tone absent from it is
	// REFUSED by the send, not merely hidden by the screen (the 0173 line_discounts_enabled ruling).
	ReminderTones         []string `json:"reminder_tones"`
	ReminderCooldownHours int      `json:"reminder_cooldown_hours"`
	ShowCreditsDefault    bool     `json:"show_credits_default"`
	DefaultBranchScope    string   `json:"default_branch_scope,omitempty"`
	WorkspaceSplitPct     int      `json:"workspace_split_pct"`
	WorkspaceArrangement  string   `json:"workspace_arrangement"`
}

// Policy visibility values (0178 CHECK).
const (
	VisibilityManagerUp = "MANAGER_UP"
	VisibilityAllStaff  = "ALL_STAFF"
	VisibilityHidden    = "HIDDEN"
)

// Reminder tones (0175 CHECK). FINAL is deliberately last: a dealer trimming the list trims from the
// sharp end.
const (
	ToneFriendly = "FRIENDLY"
	ToneFirm     = "FIRM"
	ToneFinal    = "FINAL"
)

// -- the register -------------------------------------------------------------------------------

// AccountPosition is the ACCOUNT-LEVEL financial picture, the gated half of the payload. Every
// field is the whole account, not this branch's slice of it, for the reason customer_ar_read.go
// gives: a customer owes what a customer owes, and a branch-filtered balance appears on no statement.
type AccountPosition struct {
	OpenBalanceCents     int64 `json:"open_balance_cents"`
	CreditLimitCents     int64 `json:"credit_limit_cents"`
	AvailableCreditCents int64 `json:"available_credit_cents"`
	OverdueCents         int64 `json:"overdue_cents"`
}

// AccountStatus is the one-line account state that sits under the customer's name in the register.
// It is NOT sensitive: whether an account is stopped is operational, the POS credit gate already
// refuses the sale at the counter, so the person about to make that sale is entitled to know why.
const (
	AccountCurrent     = "CURRENT"         // nothing outstanding past due, no hold
	AccountCreditOK    = "CREDIT_APPROVED" // carrying a balance inside terms
	AccountCreditHold  = "CREDIT_HOLD"     // an ACTIVE credit_holds row
	AccountUnderReview = "UNDER_REVIEW"    // an open dispute somewhere on the account
)

// RegisterRow is one line of the invoice register.
//
// Status is the DERIVED document state the pill shows (see registerStatus): the stored status says
// FINALIZED for both a current invoice and one 91 days late, and the difference is the entire point
// of the screen. DaysOverdue is the server's arithmetic against the server's clock, a client that
// computed it from due_date would be aging invoices against the operator's laptop clock, which is
// how two people end up quoting different numbers down two phone lines.
type RegisterRow struct {
	ID            string    `json:"id"`
	InvoiceNo     string    `json:"invoice_no"`
	DocType       string    `json:"doc_type"` // INVOICE | CREDIT_MEMO
	CustomerID    string    `json:"customer_id"`
	CustomerName  string    `json:"customer_name"`
	AccountNo     string    `json:"account_no"`
	BranchID      string    `json:"branch_id"`
	OrderID       string    `json:"order_id,omitempty"`
	OrderNo       string    `json:"order_no,omitempty"`
	CustomerPO    string    `json:"customer_po,omitempty"`
	Salesperson   string    `json:"salesperson,omitempty"`
	IssueDate     time.Time `json:"issue_date"`
	DueDate       time.Time `json:"due_date"`
	TotalCents    int64     `json:"total_cents"`
	BalanceCents  int64     `json:"balance_cents"`
	HoldbackCents int64     `json:"holdback_cents,omitempty"`
	StoredStatus  string    `json:"stored_status"` // the 0009 CHECK value, unmodified
	Status        string    `json:"status"`        // the derived pill state
	DaysOverdue   int       `json:"days_overdue"`  // 0 unless past due
	AccountStatus string    `json:"account_status"`
	Disputed      bool      `json:"disputed"`
	// PromisedDate is the live promise against this document, when one exists, the fact that stops
	// the next caller chasing a customer who has already committed.
	PromisedDate  *time.Time `json:"promised_date,omitempty"`
	PromisedCents int64      `json:"promised_cents,omitempty"`
	// LastSentAt / Opened answer "has this customer actually seen it". Opened is a POINTER because
	// "we never emailed it" and "we emailed it and they have not opened it" are different answers and
	// a bool would collapse them into the accusatory one.
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	Opened     *bool      `json:"opened,omitempty"`
	// Account is ABSENT for a caller the policy refuses. Pointer, not zero value.
	Account *AccountPosition `json:"account,omitempty"`
}

// Derived register statuses (the pill vocabulary).
const (
	StatusOpen          = "OPEN"
	StatusDueSoon       = "DUE_SOON"
	StatusOverdue       = "OVERDUE"
	StatusPartiallyPaid = "PARTIALLY_PAID"
	StatusPaid          = "PAID"
	StatusDisputed      = "DISPUTED"
	StatusCreditHold    = "CREDIT_HOLD"
	StatusDraft         = "DRAFT"
	StatusVoid          = "VOID"
	StatusCredit        = "CREDIT"
)

// RegisterCounts is the count-bearing chip row. These are COUNTS OF THE SAME PREDICATES the filters
// apply, computed in one pass over the same rows, so a chip that says 8 and a filter that shows 7 is
// not a state this screen can reach.
type RegisterCounts struct {
	Open     int `json:"open"`
	DueSoon  int `json:"due_soon"`
	Overdue  int `json:"overdue"`
	Disputed int `json:"disputed"`
	Paid     int `json:"paid"`
	Credits  int `json:"credits"`
}

// RegisterFilter is the register query. Every field is optional; the zero value is "the whole book,
// open documents first".
type RegisterFilter struct {
	Search      string // invoice no / customer name / account no / order no / customer PO
	Chip        string // one of the derived statuses, or "" for no chip
	BranchID    string
	CustomerID  string
	Salesperson string
	IssuedFrom  *time.Time
	IssuedTo    *time.Time
	ShowCredits bool
	Limit       int
	Offset      int
	// All reads every row of the filter in ONE statement (the export): Limit and Offset are then
	// ignored, and the rows come from a single snapshot, so a payment landing mid-read cannot shift a
	// row between pages. Only the export sets it; the list read is always paged.
	All bool
}

// RegisterPage is the register read's answer.
type RegisterPage struct {
	Items  []RegisterRow  `json:"items"`
	Counts RegisterCounts `json:"counts"`
	Total  int            `json:"total"`
	AsOf   time.Time      `json:"as_of"`
	// SelectionCents is ABSENT here on purpose: the bulk bar's "3 selected · CA$X" figure is asked
	// for separately (registerSelection) rather than summed in the browser, because a number the
	// operator is about to act on must come from the same place the action will read it from.
}

// -- the detail ---------------------------------------------------------------------------------

// DetailLine is one invoice_lines row as the detail table renders it.
type DetailLine struct {
	LineNo         int    `json:"line_no"`
	ProductID      string `json:"product_id,omitempty"`
	SKU            string `json:"sku,omitempty"`
	ImageRef       string `json:"image_ref,omitempty"`
	Description    string `json:"description"`
	QtyEntered     string `json:"qty_entered"`
	UOMEntered     string `json:"uom_entered"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
}

// Totals is the detail's totals card. EVERY FIGURE IS THE SERVER'S.
//
// AppliedCents is SUM(payment_allocations) for this document, a stored aggregate, not
// total − balance. The two agree today and would diverge the moment a credit memo or an NSF reversal
// moved a balance without a payment row, and when they diverge the aggregate is the one that is
// actually true about payments received.
//
// PayableCents is balance − holdback floored at zero: the Bill 60 legally-retained portion is not
// collectable and the Record Payment control must not offer it. Same arithmetic as
// payments.PayableInvoices, deliberately, so the screen's ceiling and the write's ceiling cannot
// drift apart.
type Totals struct {
	SubtotalCents int64 `json:"subtotal_cents"`
	TaxCents      int64 `json:"tax_cents"`
	TotalCents    int64 `json:"total_cents"`
	AppliedCents  int64 `json:"applied_cents"`
	HoldbackCents int64 `json:"holdback_cents"`
	BalanceCents  int64 `json:"balance_cents"`
	PayableCents  int64 `json:"payable_cents"`
}

// CustomerBlock is the detail's identity card.
type CustomerBlock struct {
	CustomerID    string `json:"customer_id"`
	Name          string `json:"name"`
	AccountNo     string `json:"account_no"`
	ContactName   string `json:"contact_name,omitempty"`
	ContactEmail  string `json:"contact_email,omitempty"`
	ContactPhone  string `json:"contact_phone,omitempty"`
	TermsDays     int    `json:"terms_days"`
	AccountStatus string `json:"account_status"`
	// Account is the gated position, absent for a refused caller, exactly as on the row.
	Account *AccountPosition `json:"account,omitempty"`
}

// ProofPhoto is one delivery photograph's METADATA. The bytes come from the shipped dispatch byte
// route (GET /api/v1/dispatch/photos/{id}); this payload carries the id and never the image, so the
// register does not become a second image server with its own second set of access rules.
type ProofPhoto struct {
	ID         string    `json:"id"`
	Caption    string    `json:"caption,omitempty"`
	CapturedAt time.Time `json:"captured_at"`
}

// Proof is the "Order & proof" strip: who signed for this delivery and when, the photographs, and
// the way through to the full dispatch record.
//
// Delivered=false with a StopID is a scheduled-but-not-yet-delivered stop; no StopID at all means
// this invoice has no delivery behind it (a counter pickup), which is a true and ordinary answer and
// renders as nothing rather than as an absence.
type Proof struct {
	StopID       string       `json:"stop_id,omitempty"`
	OrderID      string       `json:"order_id,omitempty"`
	OrderNo      string       `json:"order_no,omitempty"`
	Delivered    bool         `json:"delivered"`
	SignedBy     string       `json:"signed_by,omitempty"`
	SignedAt     *time.Time   `json:"signed_at,omitempty"`
	SignatureRef string       `json:"signature_ref,omitempty"`
	Address      string       `json:"address,omitempty"`
	Photos       []ProofPhoto `json:"photos"`
}

// ActivityEntry is one line of the detail's timeline. Kind is the vocabulary the screen renders:
// EMAILED / VIEWED / REMINDER / RECEIPT / STATEMENT / PAYMENT / DELIVERED / PROMISE / DISPUTE /
// ISSUED. The timeline INTERLEAVES sources (0175 sends, payment allocations, the delivery stop, the
// promise and dispute tables) at read time, the order/activity.go posture, for the same reason: a
// single "activity" table would have needed every one of those writers to remember to feed it.
type ActivityEntry struct {
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	Detail      string    `json:"detail,omitempty"`
	AmountCents int64     `json:"amount_cents,omitempty"`
	Actor       string    `json:"actor,omitempty"`
}

// PromiseView is a recorded commitment (0176) as the detail shows it.
type PromiseView struct {
	ID            string     `json:"id"`
	PromisedCents int64      `json:"promised_cents"`
	PromisedDate  time.Time  `json:"promised_date"`
	Status        string     `json:"status"`
	PromisedBy    string     `json:"promised_by,omitempty"`
	Note          string     `json:"note,omitempty"`
	TakenBy       string     `json:"taken_by,omitempty"`
	TakenAt       time.Time  `json:"taken_at"`
	SettledAt     *time.Time `json:"settled_at,omitempty"`
}

// DisputeView is an open or resolved dispute (0177).
type DisputeView struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	Note        string     `json:"note,omitempty"`
	AmountCents int64      `json:"amount_cents"`
	RaisedBy    string     `json:"raised_by,omitempty"`
	RaisedAt    time.Time  `json:"raised_at"`
	Outcome     string     `json:"outcome,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

// InvoiceDetail is the whole right-hand panel in one read. One read rather than six because the
// panel is one thing an operator opens, and six round trips is six chances for the panel to render
// half a document.
type InvoiceDetail struct {
	ID           string          `json:"id"`
	InvoiceNo    string          `json:"invoice_no"`
	DocType      string          `json:"doc_type"`
	StoredStatus string          `json:"stored_status"`
	Status       string          `json:"status"`
	DaysOverdue  int             `json:"days_overdue"`
	IssueDate    time.Time       `json:"issue_date"`
	DueDate      time.Time       `json:"due_date"`
	BranchID     string          `json:"branch_id"`
	OrderID      string          `json:"order_id,omitempty"`
	OrderNo      string          `json:"order_no,omitempty"`
	CustomerPO   string          `json:"customer_po,omitempty"`
	Salesperson  string          `json:"salesperson,omitempty"`
	Customer     CustomerBlock   `json:"customer"`
	Lines        []DetailLine    `json:"lines"`
	Totals       Totals          `json:"totals"`
	Proof        *Proof          `json:"proof,omitempty"`
	Activity     []ActivityEntry `json:"activity"`
	Promise      *PromiseView    `json:"promise,omitempty"`
	Dispute      *DisputeView    `json:"dispute,omitempty"`
	// Tones the caller may actually send, per policy. The screen offers exactly these.
	ReminderTones []string `json:"reminder_tones"`
	// LastContactAt is the most recent send to this ACCOUNT (not this invoice), the cooldown fact.
	LastContactAt *time.Time `json:"last_contact_at,omitempty"`
	AsOf          time.Time  `json:"as_of"`
}

// -- writes -------------------------------------------------------------------------------------

// ReceiptInput records a payment TAKEN OFF-GATEWAY against one invoice.
type ReceiptInput struct {
	InvoiceID      string `json:"-"`
	Method         string `json:"method"` // CASH | CARD | CHEQUE | EFT
	AmountCents    int64  `json:"amount_cents"`
	Reference      string `json:"reference"` // cheque no / EFT trace, recorded, never invented
	IdempotencyKey string `json:"idempotency_key"`
}

// ReceiptResult is what the Record Payment control gets back. Replayed=true means the key had
// already been honoured and THIS REQUEST POSTED NOTHING, the guarantee a double-click needs.
type ReceiptResult struct {
	PaymentID    string `json:"payment_id"`
	PaymentNo    string `json:"payment_no"`
	AmountCents  int64  `json:"amount_cents"`
	BalanceCents int64  `json:"balance_cents"`
	Status       string `json:"status"`
	Replayed     bool   `json:"replayed"`
}

// ReminderInput is one deliberate chase.
type ReminderInput struct {
	InvoiceIDs     []string `json:"invoice_ids"`
	Tone           string   `json:"tone"`
	IdempotencyKey string   `json:"idempotency_key"`
}

// ReminderResult reports what actually went. Skipped names the documents that did not get one and
// why, a reminder run that silently dropped half its list is worse than one that refused.
type ReminderResult struct {
	Sent     int               `json:"sent"`
	Replayed int               `json:"replayed"`
	Skipped  map[string]string `json:"skipped,omitempty"`
	Tone     string            `json:"tone"`
}

// PromiseInput records a commitment.
type PromiseInput struct {
	InvoiceID      string `json:"-"`
	PromisedCents  int64  `json:"promised_cents"`
	PromisedDate   string `json:"promised_date"` // YYYY-MM-DD
	PromisedBy     string `json:"promised_by"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

// DisputeInput opens a dispute.
type DisputeInput struct {
	InvoiceID      string `json:"-"`
	Reason         string `json:"reason"`
	Note           string `json:"note"`
	AmountCents    int64  `json:"amount_cents"`
	IdempotencyKey string `json:"idempotency_key"`
}
