package bankrec

import (
	"fmt"
	"net/http"
	"time"
)

// - GL account codes the clearing journals hit (0007/0032) -
const (
	AcctBank         = "1000" // RBC operating bank (bank-side, posted on statement match)
	AcctCardClearing = "1010" // card settlement clearing (RUN-PAY captures land here first)
	AcctUndeposited  = "1020" // undeposited funds (cash/cheque lodgements sit here until banked)
)

// - statuses / enums (mirror the 0033 CHECK sets) -
const (
	ImportStatusImported = "IMPORTED"
	ImportStatusFailed   = "FAILED"

	LineUnmatched = "UNMATCHED"
	LineMatched   = "MATCHED"
	LineExcluded  = "EXCLUDED"

	TargetPayment   = "PAYMENT"
	TargetLodgement = "LODGEMENT"
	TargetJournal   = "JOURNAL"
	TargetGroup     = "GROUP"

	MethodAuto   = "AUTO"
	MethodManual = "MANUAL"

	RuleDraft    = "DRAFT"
	RuleApproved = "APPROVED"
	RuleRetired  = "RETIRED"

	ReconOpen      = "OPEN"
	ReconCompleted = "COMPLETED"

	AccountBank       = "BANK"
	AccountCreditCard = "CREDIT_CARD"
)

// TopicReconCompleted is the outbox topic emitted when a monthly reconciliation is signed off (a GL posted
// topic, see gl.PostedTopics). The payload is documented on emitReconCompleted.
const TopicReconCompleted = "bank.recon.completed"

// - matcher rule kinds (bank_match_rules.spec.kind) -
const (
	KindGatewayRefExact   = "gateway_ref_exact"   // card settlement, payment.gateway_ref == line.reference
	KindLodgementRefExact = "lodgement_ref_exact" // cash/cheque lodgement, reference exact, zero tolerance
	KindDescriptorFuzzy   = "descriptor_fuzzy"    // RUN settlement, descriptor ILIKE + date window (+ fee split)
)

// - read models (JSON responses) -

// BankAccount is one seeded real bank/card account bound to its GL code (0033 bank_accounts). CREDIT_CARD
// accounts (Corporate Visa 2110-2117) are TRACKING-ONLY at W5, no Visa spend is booked.
type BankAccount struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	AccountType    string `json:"account_type"`
	GLAccountCode  string `json:"gl_account_code"`
	Institution    string `json:"institution"`
	Last4          string `json:"last4"`
	IsActive       bool   `json:"is_active"`
	TrackingOnly   bool   `json:"tracking_only"` // true for CREDIT_CARD at W5
	UnmatchedLines int    `json:"unmatched_lines"`
}

// StatementLine is one parsed bank line (0033 bank_statement_lines). AmountCents is signed (+credit/−debit).
type StatementLine struct {
	ID            string    `json:"id"`
	BankAccountID string    `json:"bank_account_id"`
	ImportID      string    `json:"import_id,omitempty"`
	TxnDate       time.Time `json:"txn_date"`
	Description   string    `json:"description"`
	Reference     string    `json:"reference"`
	AmountCents   int64     `json:"amount_cents"`
	DedupeHash    string    `json:"dedupe_hash"`
	Status        string    `json:"status"`
	// CreatedAt is scanned for the cursor key only (HH-3.2 B5): the list's
	// stable-ordering basis is (txn_date, created_at, id), so the handler
	// mints next_cursor from the last kept row's created_at. It is not
	// serialized; the item's wire shape is the pin's.
	CreatedAt time.Time `json:"-"`
}

// ImportResult is the summary an import run returns (idempotent re-import → Inserted 0, Duplicates high).
type ImportResult struct {
	ImportID   string `json:"import_id"`
	Status     string `json:"status"`
	Parsed     int    `json:"parsed"`
	Inserted   int    `json:"inserted"`
	Duplicates int    `json:"duplicates"`
	Error      string `json:"error,omitempty"`
}

// MatchRule is a parsed bank_match_rules row + its decoded spec. The matcher reads the typed fields.
type MatchRule struct {
	ID           string         `json:"id"`
	Code         string         `json:"code"`
	Name         string         `json:"name"`
	Priority     int            `json:"priority"`
	Version      int            `json:"version"`
	Status       string         `json:"status"`
	Spec         map[string]any `json:"spec"`
	SupersedesID string         `json:"supersedes_rule_id,omitempty"`
	// decoded spec fields
	Kind            string `json:"-"`
	Target          string `json:"-"`
	ToleranceCents  int64  `json:"-"`
	DescriptorILike string `json:"-"`
	DateWindowDays  int    `json:"-"`
	AllowFeeSplit   bool   `json:"-"`
	FeeAccountCode  string `json:"-"`
}

// Match is one bank_matches row (live or historical).
type Match struct {
	ID              string     `json:"id"`
	StatementLineID string     `json:"statement_line_id"`
	TargetType      string     `json:"target_type"`
	TargetRefID     string     `json:"target_ref_id,omitempty"`
	Method          string     `json:"method"`
	RuleID          string     `json:"rule_id,omitempty"`
	JournalID       string     `json:"journal_id,omitempty"`
	AmountCents     int64      `json:"amount_cents"`
	MatchedBy       string     `json:"matched_by,omitempty"`
	MatchedAt       time.Time  `json:"matched_at"`
	UnmatchedAt     *time.Time `json:"unmatched_at,omitempty"`
}

// Reconciliation is one monthly per-account reconciliation (0033 bank_reconciliations).
type Reconciliation struct {
	ID                      string     `json:"id"`
	BankAccountID           string     `json:"bank_account_id"`
	Year                    int        `json:"year"`
	Month                   int        `json:"month"`
	StatementBalanceCents   int64      `json:"statement_balance_cents"`
	StatementBalanceDerived bool       `json:"-"`
	GLBalanceCents          int64      `json:"gl_balance_cents"`
	VarianceCents           int64      `json:"variance_cents"`
	Status                  string     `json:"status"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
}

// - inputs (request bodies) -

// ImportInput is the statement-import request. Content is the raw CSV (or other) export bytes.
type ImportInput struct {
	BankAccountID string `json:"bank_account_id"`
	ProfileCode   string `json:"profile_code"`
	Filename      string `json:"filename"`
	Content       string `json:"content"`
}

// MatchInput is either an AUTO run (Mode "auto" over a window) or a MANUAL pairing (Mode "manual").
type MatchInput struct {
	Mode          string `json:"mode"` // "auto" | "manual"
	BankAccountID string `json:"bank_account_id"`
	From          string `json:"from"` // RFC3339 date (auto)
	To            string `json:"to"`   // RFC3339 date (auto)

	StatementLineID string `json:"statement_line_id"` // manual
	TargetType      string `json:"target_type"`       // manual
	TargetRefID     string `json:"target_ref_id"`

	// CanAccessBranch is the caller's branch reach, set by the handler from the guard-resolved authority and never
	// read from the request (SF-2g). A payment or lodgement target outside it answers exactly as a missing one. It
	// MUST be set: a nil reach REFUSES the match (SF-2d round 5); a dealer wide internal caller passes AllBranches.
	CanAccessBranch func(branchID string) bool `json:"-"`            // manual
	AmountCents     int64                      `json:"amount_cents"` // manual (optional; defaults to line amount)
}

// UnmatchInput stamps a live match unmatched (history kept) and reverts its line to UNMATCHED.
type UnmatchInput struct {
	MatchID string `json:"match_id"`
}

// RuleInput proposes a new APPROVED rule version (supersedes the current APPROVED for the code).
type RuleInput struct {
	Code     string         `json:"code"`
	Name     string         `json:"name"`
	Spec     map[string]any `json:"spec"`
	Priority int            `json:"priority"`
}

// ReconInput opens (prepares) a monthly reconciliation: it computes the GL balance + variance.
type ReconInput struct {
	BankAccountID         string `json:"bank_account_id"`
	Year                  int    `json:"year"`
	Month                 int    `json:"month"`
	StatementBalanceCents int64  `json:"statement_balance_cents"`
}

// - error taxonomy (mirrors internal/till) -
type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }
func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

func unprocessable(format string, a ...any) error {
	return clientError{status: http.StatusUnprocessableEntity, msg: fmt.Sprintf(format, a...)}
}
func badRequest(format string, a ...any) error {
	return clientError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}
func conflict(format string, a ...any) error {
	return clientError{status: http.StatusConflict, msg: fmt.Sprintf(format, a...)}
}

var (
	errNotFound = clientError{status: http.StatusNotFound, msg: "bankrec: not found"}
	// errNoReach refuses a call that carries no branch reach (SF-2d round 5): a nil reach never means "no fence".
	errNoReach      = clientError{status: http.StatusForbidden, msg: "bankrec: no branch reach was given"}
	errUnauthorized = clientError{status: http.StatusUnauthorized, msg: "bankrec: unauthenticated"}
)

// AllBranches is the explicit allow all reach: a dealer wide internal caller (a test, a batch job that is not acting
// for a branch caller) passes it by name, so that "reaches every branch" is something a caller says and a nil reach
// never silently means. The handlers pass the caller's own reach and never this.
func AllBranches(string) bool { return true }
