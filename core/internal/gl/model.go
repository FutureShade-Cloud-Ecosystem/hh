package gl

// model.go, the GL domain types (DTOs projected for the HTTP surface + tests), the input shapes, the
// internal journalDraft used by the posting path, and the error taxonomy (mirrors internal/till / pricing).
// All money is integer cents (BIGINT); CAD only.

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"
)

// - lifecycle + source vocabularies -

// Journal DB status (0007 CHECK: DRAFT|POSTED|VOID). There is NO PENDING_APPROVAL column, the maker-checker
// "awaiting approval" state is DERIVED (a DRAFT above the gl_policy threshold, approved_by still NULL) and is
// surfaced only in the API projection as StatusPendingApproval. See journal.go / deviations.
const (
	StatusDraft           = "DRAFT"
	StatusPosted          = "POSTED"
	StatusVoid            = "VOID"
	StatusPendingApproval = "PENDING_APPROVAL" // derived API label only; never written to gl_journals.status
)

// Journal source (0007 CHECK set).
const (
	SourceManual      = "MANUAL"
	SourceInvoice     = "INVOICE"
	SourcePayment     = "PAYMENT"
	SourceFulfillment = "FULFILLMENT"
	SourcePOReceipt   = "PO_RECEIPT"
	SourceAdjustment  = "ADJUSTMENT"
	SourceClosing     = "CLOSING"
)

// Normal-balance orientation (0007 gl_accounts.normal_balance).
const (
	NormalDebit  = "DEBIT"
	NormalCredit = "CREDIT"
)

// RetainedEarningsCode is the 3900 equity account the year-end close sweeps net income into.
const RetainedEarningsCode = "3900"

// - projected domain types -

// Account is a chart-of-accounts row (0007 gl_accounts).
type Account struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	AccountType   string `json:"account_type"`
	NormalBalance string `json:"normal_balance"`
	ParentID      string `json:"parent_id,omitempty"`
	IsActive      bool   `json:"is_active"`
}

// JournalLine is one leg of a journal, projected for the API (account addressed by code + name).
type JournalLine struct {
	LineNo      int    `json:"line_no"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name,omitempty"`
	DebitCents  int64  `json:"debit_cents"`
	CreditCents int64  `json:"credit_cents"`
	Memo        string `json:"memo,omitempty"`
}

// Journal is a journal header + its lines, projected for the API. DerivedStatus carries the maker-checker
// label (PENDING_APPROVAL) that has no DB column; Status is the raw gl_journals.status.
type Journal struct {
	ID                string        `json:"id"`
	FiscalYear        int           `json:"fiscal_year"`
	EntryNo           int           `json:"entry_no"`
	PeriodID          string        `json:"period_id"`
	Source            string        `json:"source"`
	SourceRefID       string        `json:"source_ref_id,omitempty"`
	Memo              string        `json:"memo"`
	Status            string        `json:"status"`
	DerivedStatus     string        `json:"derived_status"`
	PostedAt          *time.Time    `json:"posted_at,omitempty"`
	BranchID          string        `json:"branch_id,omitempty"`
	ApprovedBy        string        `json:"approved_by,omitempty"`
	ReversesJournalID string        `json:"reverses_journal_id,omitempty"`
	IdempotencyKey    string        `json:"idempotency_key,omitempty"`
	CreatedBy         string        `json:"created_by,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	Lines             []JournalLine `json:"lines"`
	// LatePosted, derived on read (FW1-01, ruling 82.2): the journal's document date does not
	// fall in its own period's month, the closed month it was posted past into the earliest open
	// one. A system journal is never refused for a closed month; this marker is what the
	// Controller's late posted list reads.
	LatePosted bool `json:"late_posted"`
}

// TotalCents is the journal's debit total (== credit total when balanced), the maker-checker gate reads it.
func (j Journal) TotalCents() int64 {
	var d int64
	for _, l := range j.Lines {
		d += l.DebitCents
	}
	return d
}

// Policy is the GL policy row (0032 gl_policy). BranchID "" is the org-default row.
type Policy struct {
	BranchID                 string `json:"branch_id,omitempty"`
	JEApprovalThresholdCents int64  `json:"je_approval_threshold_cents"`
	FiscalYearStartMonth     int    `json:"fiscal_year_start_month"`
	CostBasisStrategy        string `json:"cost_basis_strategy"`
	ExcludeSpecialBuys       bool   `json:"exclude_special_buys"`
	Notes                    string `json:"notes"`
}

// Period is a fiscal period (0007 fiscal_periods).
type Period struct {
	ID       string     `json:"id"`
	Year     int        `json:"year"`
	Month    int        `json:"month"`
	Status   string     `json:"status"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
}

// TrialBalanceRow is one account's debit/credit/net for a period window (built from v_gl_account_balances).
type TrialBalanceRow struct {
	AccountCode   string `json:"account_code"`
	AccountName   string `json:"account_name"`
	AccountType   string `json:"account_type"`
	NormalBalance string `json:"normal_balance"`
	DebitCents    int64  `json:"debit_cents"`
	CreditCents   int64  `json:"credit_cents"`
	BalanceCents  int64  `json:"balance_cents"` // net debit (Σdebit − Σcredit)
}

// PLBranchRow is one account's P&L net for a fiscal year under one branch stamp ("" = company level),
// the year end close's per branch input (FW1-09b).
type PLBranchRow struct {
	AccountCode   string
	AccountName   string
	AccountType   string
	NormalBalance string
	BranchID      string
	NetDebitCents int64
}

// TieoutState is a subledger tie-out result with the persisted first-seen discrepancy age (0032 gl_tieout_state).
type TieoutState struct {
	Subledger      string     `json:"subledger"`
	AccountCode    string     `json:"account_code"`
	BranchID       string     `json:"branch_id,omitempty"`
	SubledgerCents int64      `json:"subledger_cents"`
	GLCents        int64      `json:"gl_cents"`
	DeltaCents     int64      `json:"delta_cents"`
	InTie          bool       `json:"in_tie"`
	OutOfTieSince  *time.Time `json:"out_of_tie_since,omitempty"`
	AgeDays        int        `json:"age_days"`
	LastCheckedAt  time.Time  `json:"last_checked_at"`
}

// RecurringTemplate is a recurring-JE template (0032 gl_recurring_templates); /run materializes a DRAFT.
type RecurringTemplate struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Memo     string             `json:"memo"`
	Schedule string             `json:"schedule"`
	NextRun  string             `json:"next_run,omitempty"`
	IsActive bool               `json:"is_active"`
	Lines    []JournalLineInput `json:"lines"`
}

// RuleRef is one posting rule projected for ValidatePostingRules.
type RuleRef struct {
	EventType   string
	LineRole    string
	AccountCode string
	AccountOK   bool // resolves to an existing + active account
}

// ExportRow is one GP-parity export line.
type ExportRow struct {
	EntryNo     int
	PostedAt    string
	AccountCode string
	AccountName string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

// - inputs -

// CreateJournalInput builds a manual DRAFT journal.
type CreateJournalInput struct {
	BranchID string             `json:"branch_id"`
	Memo     string             `json:"memo"`
	Source   string             `json:"source"`
	Lines    []JournalLineInput `json:"lines"`
}

// JournalLineInput is one leg of a manual/recurring journal (exactly one of debit/credit > 0).
type JournalLineInput struct {
	AccountCode string `json:"account_code"`
	DebitCents  int64  `json:"debit_cents"`
	CreditCents int64  `json:"credit_cents"`
	Memo        string `json:"memo"`
}

// CreateAccountInput adds a chart-of-accounts row.
type CreateAccountInput struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	AccountType   string `json:"account_type"`
	NormalBalance string `json:"normal_balance"`
	ParentID      string `json:"parent_id"`
}

// JournalFilter is the journal list query.
type JournalFilter struct {
	Status      string
	BranchID    string
	SourceRefID string // filter journals by the upstream source ref (invoice/payment/order/return/session id)
	// DIB-204: inclusive date-range filter on posted_at. The frontend's default year/month picker
	// resolves to a first-of-month → last-of-month range; a custom range passes arbitrary bounds.
	// "" = unbounded on that side. Both are YYYY-MM-DD (validated in the handler before they arrive).
	DateFrom string
	DateTo   string
	Limit    int
	// Cursor pagination (HH-3.2 B4): AfterCursor resumes the list strictly
	// after (CursorFiscalYear, CursorEntryNo), the list's stable-ordering
	// key (ORDER BY fiscal_year DESC, entry_no DESC; entry_no is unique
	// within a fiscal year, so the pair is unique per journal).
	AfterCursor      bool
	CursorFiscalYear int
	CursorEntryNo    int
	// LatePosted, when set, filters to journals posted past a closed month into a later open one
	// (the Controller's late posted list). Nil reads both.
	LatePosted *bool
}

// - internal posting draft (the shared persist path: manual create, outbox consumer, PostExternal) -

// journalDraft is the caller-neutral journal the store persists. Status is DRAFT (manual create) or POSTED
// (outbox consumer / PostExternal / reversing / closing). OccurredAt selects the fiscal period.
type journalDraft struct {
	Source            string
	SourceRefID       string
	IdempotencyKey    string
	Memo              string
	BranchID          string
	Status            string
	ApprovedBy        string
	ReversesJournalID string
	CreatedBy         string
	OccurredAt        time.Time
	Lines             []draftLine
}

type draftLine struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

func (d journalDraft) debitTotal() int64 {
	var t int64
	for _, l := range d.Lines {
		t += l.DebitCents
	}
	return t
}

func (d journalDraft) creditTotal() int64 {
	var t int64
	for _, l := range d.Lines {
		t += l.CreditCents
	}
	return t
}

func (d journalDraft) balanced() bool { return d.debitTotal() == d.creditTotal() }

// - error taxonomy (mirrors internal/till) -

type badRequestError struct{ msg string }

func (e badRequestError) Error() string    { return e.msg }
func badReq(format string, a ...any) error { return badRequestError{msg: fmt.Sprintf(format, a...)} }

type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }
func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

var (
	errNotFound      = clientError{status: http.StatusNotFound, msg: "gl: not found"}
	errNoDB          = clientError{status: http.StatusServiceUnavailable, msg: "gl: database not configured"}
	errUnauthorized  = clientError{status: http.StatusUnauthorized, msg: "gl: unauthenticated"}
	errClosedPeriod  = clientError{status: http.StatusConflict, msg: "gl: fiscal period is CLOSED"}
	errUnbalanced    = clientError{status: http.StatusUnprocessableEntity, msg: "gl: journal is unbalanced (Σdebit ≠ Σcredit)"}
	errAccountInUse  = clientError{status: http.StatusConflict, msg: "gl: account is referenced by DRAFT journal(s); cannot deactivate"}
	errFourEyes      = clientError{status: http.StatusForbidden, msg: "gl: an approver must differ from the journal's creator (four-eyes)"}
	errNotController = clientError{status: http.StatusForbidden, msg: "gl: only the Financial Controller may reopen a closed period"}
	// errForbidden is DIB-392's horizontal-read boundary: a manager asking for a branch's (or the whole
	// org's) ledger that their scope does not reach.
	errForbidden      = clientError{status: http.StatusForbidden, msg: "gl: caller cannot access this branch"}
	errReasonRequired = clientError{status: http.StatusUnprocessableEntity, msg: "gl: a reason is required"}
)

func unprocessable(format string, a ...any) error {
	return clientError{status: http.StatusUnprocessableEntity, msg: fmt.Sprintf(format, a...)}
}

// ErrorHTTPStatus extracts the HTTP status a GL clientError carries (e.g. ApproveJournal's 409 when a
// journal is no longer DRAFT, or errFourEyes' 403). It is the boundary accessor a main.go adapter uses to
// translate a GL error into a sibling module's error taxonomy, the JE decide→GL bridge surfaces GL's
// 409/403 faithfully through identity's handler instead of collapsing it to a generic 500. ok=false for a
// non-client (infra) error, which the caller then treats as a 500/generic.
func ErrorHTTPStatus(err error) (int, bool) {
	var ce clientError
	if errors.As(err, &ce) {
		return ce.status, true
	}
	return 0, false
}

func abs64(x int64) int64 {
	if x == math.MinInt64 {
		return math.MaxInt64 // clamp: -MinInt64 is not representable as int64
	}
	if x < 0 {
		return -x
	}
	return x
}
