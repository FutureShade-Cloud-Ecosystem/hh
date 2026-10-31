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
// THE ACCOUNT'S POSITION IS. "This contractor is $81k into a $60k limit" is a fact about the
// customer's business rather than about this transaction. It is what internal/till's AR reads are
// already manager-gated for, in as many words, and it is gated here to the same tier, by BYTE
// ABSENCE at the marshal layer (ar_invoice_policy.account_position_visibility, fail closed), never by
// a CSS class. AccountPosition is therefore a POINTER on both the row and the detail: a refused
// caller receives no field at all, and the screen renders nothing because there is nothing to render.
//
// Cost and margin are not on this screen in any form, at any tier, under any policy value.

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"

	"encoding/json"
	"time"
)

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
	// BucketLabel is the C8 5-bucket label (Current / 1 to 30 / 31 to 60 / 61 to 90 / 91 and over),
	// derived from DaysOverdue against the same server asOf clock that picked DaysOverdue itself.
	// Equal to the empty string for non-past-due rows (Current), the same shape as the table cell
	// shows when DaysOverdue == 0. The single source of truth lives in BucketLabelFor.
	BucketLabel string `json:"bucket_label,omitempty"`
	// BucketKey is the CANONICAL C8 bucket key for the row (CURRENT / 1_30 / 31_60 / 61_90 /
	// 90_PLUS), machine-readable. The display label sits in BucketLabel; the key is for joins and
	// filtering on the server side.
	BucketKey     string `json:"bucket_key,omitempty"`
	AccountStatus string `json:"account_status"`
	Disputed      bool   `json:"disputed"`
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

	// PositionWithheld is set by the service when the dealer's account position policy refuses this
	// caller. MarshalJSON then leaves days_overdue, bucket_label, bucket_key and account off the wire
	// while keeping the invoice's own document figures.
	PositionWithheld bool `json:"-"`
}

// MarshalJSON writes the register row, omitting the past-due aging and account position fields when
// the caller is refused by the account position policy. The refusal also covers the DERIVED pill:
// OVERDUE and DUE_SOON are the past due flag told as a word, so a refused caller's `status` carries
// the invoice's STORED status instead (row 3172, no past due flag on any route).
func (r RegisterRow) MarshalJSON() ([]byte, error) {
	type plain RegisterRow
	w := struct {
		plain
		Status      string           `json:"status"`
		DaysOverdue *int             `json:"days_overdue,omitempty"`
		BucketLabel string           `json:"bucket_label,omitempty"`
		BucketKey   string           `json:"bucket_key,omitempty"`
		Account     *AccountPosition `json:"account,omitempty"`
	}{plain: plain(r)}
	if r.PositionWithheld {
		w.Status = r.StoredStatus
	} else {
		w.Status = r.Status
		w.DaysOverdue = &r.DaysOverdue
		w.BucketLabel = r.BucketLabel
		w.BucketKey = r.BucketKey
		w.Account = r.Account
	}
	return json.Marshal(w)
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

// C8 register filter chips. The legacy strings below stay valid for backward compatibility with
// every wire payload and stored URL bookmark that already names them.
const (
	ChipAll      = "all"
	ChipPastDue  = "past_due"
	ChipCurrent  = "current"
	ChipOnHold   = "on_hold"
	ChipOpen     = "open"     // legacy: open documents, any state
	ChipOverdue  = "overdue"  // legacy
	ChipPromised = "promised" // legacy
	ChipDisputed = "disputed" // legacy
	ChipPaid     = "paid"     // legacy
)

// C8 5-bucket keys (the SQL aging buckets). The WIRE label (BucketLabel) and the JSON key
// (BucketKey) read the same row from two angles; the bucket math lives in BucketLabelFor and the
// only place that changes when finance retunes the bands.
const (
	BucketKeyCurrent = "CURRENT"
	BucketKey1To30   = "1_30"
	BucketKey31To60  = "31_60"
	BucketKey61To90  = "61_90"
	BucketKey90Plus  = "90_PLUS"
)

// BucketLabelCurrent is the canonical human label for the CURRENT band. The other labels are
// spelled inline in BucketLabelFor so a typo in one is impossible to miss when they sit next to
// each other. Do not refactor that into a table for the sake of one if.
const BucketLabelCurrent = "Current"

// BucketLabelFor is the SINGLE source of truth for which C8 5-bucket band a days-past-due count
// sits in. Returns (label, key) so the wire payload and the join key can never disagree. The
// boundary convention matches the aging bucket math the SQL projection uses: inclusive at the
// TOP of each band, so "31" lands in 31 to 60, never in 1 to 30. Negative or zero days (i.e. not
// yet due) return the Current band.
func BucketLabelFor(daysPastDue int) (label, key string) {
	switch {
	case daysPastDue <= 0:
		return BucketLabelCurrent, BucketKeyCurrent
	case daysPastDue <= 30:
		return "1 to 30", BucketKey1To30
	case daysPastDue <= 60:
		return "31 to 60", BucketKey31To60
	case daysPastDue <= 90:
		return "61 to 90", BucketKey61To90
	default:
		return "91 and over", BucketKey90Plus
	}
}

// C8BucketOrder is the canonical order in which the 5-bucket summary is read and summed. It is
// the SAME order the AgingSummary grand total is derived from, so any panel that lists the
// buckets in display order MUST read from this slice and nowhere else.
var C8BucketOrder = []string{BucketKeyCurrent, BucketKey1To30, BucketKey31To60, BucketKey61To90, BucketKey90Plus}

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
	// Reach is the caller's branches, set by the service from the stamped caller and never taken from
	// a request. The zero value reaches nothing, so a filter built anywhere else reads an empty book.
	Reach BranchReach
}

// RegisterPage is the register read's answer.
type RegisterPage struct {
	Items  []RegisterRow  `json:"items"`
	Counts RegisterCounts `json:"counts"`
	Total  int            `json:"total"`
	AsOf   time.Time      `json:"as_of"`
	// SelectionCents is ABSENT here on purpose: the bulk bar's "3 selected · $X" figure is asked
	// for separately (registerSelection) rather than summed in the browser, because a number the
	// operator is about to act on must come from the same place the action will read it from.
	// PositionWithheld is set by the service when the dealer's account position policy refuses this
	// caller. MarshalJSON then leaves the overdue and due_soon keys off counts, and the CSV export
	// reads it to drop the Days overdue column. It never reaches the wire itself.
	PositionWithheld bool `json:"-"`
}

// MarshalJSON writes the register page. When the caller is refused by the account position policy,
// counts carries no overdue and no due_soon key: the count of a withheld predicate is the predicate
// told in numbers (row 3172), so the refused page counts only what it shows.
func (p RegisterPage) MarshalJSON() ([]byte, error) {
	type plain RegisterPage
	w := struct {
		plain
		Counts json.RawMessage `json:"counts"`
	}{plain: plain(p)}
	if p.PositionWithheld {
		c := struct {
			Open     int `json:"open"`
			Disputed int `json:"disputed"`
			Paid     int `json:"paid"`
			Credits  int `json:"credits"`
		}{Open: p.Counts.Open, Disputed: p.Counts.Disputed, Paid: p.Counts.Paid, Credits: p.Counts.Credits}
		b, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		w.Counts = b
	} else {
		b, err := json.Marshal(p.Counts)
		if err != nil {
			return nil, err
		}
		w.Counts = b
	}
	return json.Marshal(w)
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
	// PriceUOM is the unit the unit price is per. It feeds the printed document only (json "-"):
	// putting it on the wire is an API contract change that is not this struct's to make.
	PriceUOM       string `json:"-"`
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
	// DiscountCents is the early payment discount the document's live applications took (TM-2):
	// total less applied less discount is the balance (less credits), so a discounted invoice
	// reads total 100000, applied 98000, discount 2000, balance 0 and the figures explain
	// themselves.
	DiscountCents int64 `json:"discount_cents"`
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

// OpenCreditSummary is one open credit memo (MEMO) or unapplied account credit (ACCOUNT)
// on the customer's account at this invoice's branch, ready to apply from the receivables desk.
type OpenCreditSummary struct {
	CreditKind  string    `json:"credit_kind"`
	CreditID    string    `json:"credit_id"`
	Reference   string    `json:"reference"`
	Memo        string    `json:"memo,omitempty"`
	IssuedAt    time.Time `json:"issued_at"`
	AmountCents int64     `json:"amount_cents"`
	OpenCents   int64     `json:"open_cents"`
}

// CreditApplicationView is one credit application recorded on an invoice, active or reversed.
type CreditApplicationView struct {
	ApplicationID   string `json:"application_id"`
	CreditKind      string `json:"credit_kind"`
	CreditID        string `json:"credit_id"`
	CreditReference string `json:"credit_reference,omitempty"`
	AmountCents     int64  `json:"amount_cents"`
	Reason          string `json:"reason"`
	AppliedBy       string `json:"applied_by,omitempty"`
	AppliedAt       string `json:"applied_at"`
	Reversed        bool   `json:"reversed"`
	ReversalReason  string `json:"reversal_reason,omitempty"`
	ReversedBy      string `json:"reversed_by,omitempty"`
	ReversedAt      string `json:"reversed_at,omitempty"`
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
	LastContactAt      *time.Time              `json:"last_contact_at,omitempty"`
	OpenCredits        []OpenCreditSummary     `json:"open_credits,omitempty"`
	CreditApplications []CreditApplicationView `json:"credit_applications,omitempty"`
	AsOf               time.Time               `json:"as_of"`

	// PositionWithheld is set by the service when the dealer's account position policy refuses this
	// caller. MarshalJSON then leaves days_overdue off the wire.
	PositionWithheld bool `json:"-"`
}

// MarshalJSON writes the invoice detail, omitting days_overdue when the caller is refused by the
// account position policy. The refusal also covers the DERIVED pill: a refused caller's `status`
// carries the invoice's STORED status, never the derived OVERDUE or DUE_SOON (row 3172).
func (d InvoiceDetail) MarshalJSON() ([]byte, error) {
	type plain InvoiceDetail
	w := struct {
		plain
		Status      string `json:"status"`
		DaysOverdue *int   `json:"days_overdue,omitempty"`
	}{plain: plain(d)}
	if d.PositionWithheld {
		w.Status = d.StoredStatus
	} else {
		w.Status = d.Status
		w.DaysOverdue = &d.DaysOverdue
	}
	return json.Marshal(w)
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

// ApplyPaymentInput records ONE received payment against a list of the account's open invoices and
// asks the server to split it oldest first. The split is never the caller's to compute.
type ApplyPaymentInput struct {
	CustomerID     string   `json:"customer_id"`
	Method         string   `json:"method"` // CASH | CARD | CHEQUE | EFT
	AmountCents    int64    `json:"amount_cents"`
	Reference      string   `json:"reference"`
	InvoiceIDs     []string `json:"invoice_ids"`
	IdempotencyKey string   `json:"idempotency_key"`
	Preview        bool     `json:"preview"`
	// LateDiscountReason asks for the early payment discount OUTSIDE its window on the invoices
	// this payment names (TM-2): the manager's own words, recorded with the approval when the
	// caller holds the approval wall and refused in words when not. Empty asks nothing, and a
	// payment inside the window discounts automatically whatever this holds.
	LateDiscountReason string `json:"late_discount_reason"`

	// DiscountApproval is the route's own stamp of the ask above: who is asking, the words, and
	// whether the caller passed the approval wall. Set by the handler, never read from the body.
	DiscountApproval *payments.DiscountApproval `json:"-"`
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
