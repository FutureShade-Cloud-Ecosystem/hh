package till

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// - lifecycle + method constants -
const (
	StatusOpen    = "OPEN"
	StatusCounted = "COUNTED"
	StatusClosed  = "CLOSED"
)

const (
	MethodCash   = "CASH"
	MethodCheque = "CHEQUE"
	MethodCard   = "CARD"
	MethodEFT    = "EFT"
	MethodOther  = "OTHER"
)

const (
	MovementPaidIn    = "PAID_IN"
	MovementPaidOut   = "PAID_OUT"
	MovementDrop      = "DROP"
	MovementLodgement = "LODGEMENT"
)

// - consumed seam (zero-trust interface, A-03 never reaches a sibling's tables) -
//
// SCAFFOLD-OWNED SHARED SEAM. CardCharger is RUN-PAY's card capture rail (the creds-free wrapper
// over payment.Service's Capture, Compensate and BindTx; creds resolve INSIDE RUN-PAY and never
// cross into the till). It is wired in the composition root (cmd/core) via a thin adapter so
// payment does NOT import till (no cycle).
//
// The seam's transaction discipline is its defining property (decision log row 786, the card
// charge design 3.3): Capture and Compensate run on the POOL and are never called inside a
// transaction (pos calls Capture before the sale transaction opens and Compensate after it
// returns, so no statement they make can wait on a connection the caller holds), and BindTx rides
// the SALE's transaction, taking the tx it is handed for every statement. The till's TakeTender
// binds a capture inside the sale transaction; it never charges one.
type CardCharger interface {
	// Capture charges one card leg at most once, on the pool: the claim, the dispatch and the
	// stamp are all committed before the caller sees the answer, and a leg whose attempt row
	// already exists answers from the row with no processor call. With ReadOnly set the call
	// never claims: an existing row answers from itself and a leg with no row answers
	// ErrCardLegUnattempted, so a ring whose earlier leg already failed can read its remaining
	// legs without charging a fresh one.
	Capture(ctx context.Context, in CardChargeInput) (CardCapture, error)
	// BindTx binds a captured attempt to the payments row the SALE's transaction just wrote,
	// guarding the status, the amount, the till session and the leg key. It rides tx and never
	// the pool; a zero-row bind fails the transaction.
	BindTx(ctx context.Context, tx pgx.Tx, in CardBind) error
	// Compensate reverses one captured leg on the pool (the void, with the refund as the
	// fallback): to VOIDED, or the committed and needs-a-manager answers the row gives.
	Compensate(ctx context.Context, attemptID string) (CardCompensation, error)
}

// CardChargeInput is one card leg the ring captures at the counter, before its sale transaction
// opens. AccountToken is the client-tokenized instrument: it goes to the processor and nowhere
// else, never persisted and never logged.
type CardChargeInput struct {
	TillSessionID          string
	BranchID, CustomerID   string
	AmountCents            int64
	LegKey, SaleKey        string
	LegSeq                 int
	AccountToken, CardType string
	CreatedBy              string
	ReadOnly               bool
}

// CardCapture is a leg's capture answer: the attempt row's id and the processor ref its capture
// stamped (Kind CardCaptureCaptured), or the already-committed sale's payment id
// (Kind CardCaptureBound, the replay shape). The attempt id is carried on the typed error answers
// too, so the caller can name the row a manager has to read.
type CardCapture struct {
	Kind         string
	AttemptID    string
	ProcessorRef string
	PaymentID    string
}

// The capture answer kinds.
const (
	CardCaptureCaptured = "CAPTURED"
	CardCaptureBound    = "BOUND"
)

// CardBind is the sale transaction's bind of a captured attempt to the payments row it just
// wrote: all four guards (the attempt id, the leg key, the till session, the amount) name the
// tender the bind belongs to.
type CardBind struct {
	AttemptID, PaymentID  string
	LegKey, TillSessionID string
	AmountCents           int64
}

// CardCompensation is a reversal's answer: no money remains (VOIDED), the sale's commit landed
// after all (COMMITTED, answer the sale and void nothing), or a person has to look
// (NEEDS_MANAGER).
type CardCompensation struct {
	Kind      string
	AttemptID string
}

// The compensation answer kinds.
const (
	CardCompVoided       = "VOIDED"
	CardCompCommitted    = "COMMITTED"
	CardCompNeedsManager = "NEEDS_MANAGER"
)

// - domain types -

// TillSession is one drawer session (0010 + 0028).
type TillSession struct {
	ID                string `json:"id"`
	BranchID          string `json:"branch_id"`
	Station           string `json:"station"`
	Status            string `json:"status"`
	OpenedBy          string `json:"opened_by"`
	FloatCents        int64  `json:"float_cents"`
	ClosingFloatCents int64  `json:"closing_float_cents,omitempty"`
	BlindClose        bool   `json:"blind_close"`
	OverrideNotes     string `json:"override_notes,omitempty"`
}

// TenderTotal is the per-method expected/actual/lodged child (0028 till_tender_totals).
type TenderTotal struct {
	Method        string `json:"method"`
	ExpectedCents int64  `json:"expected_cents"`
	ActualCents   int64  `json:"actual_cents"`
	VarianceCents int64  `json:"variance_cents"` // DERIVED: actual - expected
	LodgedCents   int64  `json:"lodged_cents"`
}

// Movement is a paid-in / paid-out / drop / lodgement (0028 till_movements).
type Movement struct {
	ID           string `json:"id"`
	MovementType string `json:"movement_type"`
	Method       string `json:"method"`
	AmountCents  int64  `json:"amount_cents"`
	Reason       string `json:"reason"`
	Reference    string `json:"reference"`
}

// DenominationCount is one optional denomination tally row (0028 till_denomination_counts).
type DenominationCount struct {
	Phase             string `json:"phase"` // OPEN | CLOSE
	DenominationCents int64  `json:"denomination_cents"`
	Quantity          int    `json:"quantity"`
}

// TillPolicy is the branch variance threshold + close grain (0028 till_policy).
type TillPolicy struct {
	BranchID               string `json:"branch_id"`
	VarianceThresholdCents int64  `json:"variance_threshold_cents"`
	CloseGrain             string `json:"close_grain"`
	RequireBlindClose      bool   `json:"require_blind_close"`
}

// ZReport is the expected vs actual vs over/short, revealed AFTER count (start float in the cash math).
type ZReport struct {
	SessionID         string        `json:"session_id"`
	PerTender         []TenderTotal `json:"per_tender"`
	Cash              CashSummary   `json:"cash"`
	ClosingFloatCents int64         `json:"closing_float_cents"`
}

// CashSummary is the derived cash reconciliation block.
type CashSummary struct {
	OpeningFloatCents int64 `json:"opening_float_cents"`
	ExpectedCents     int64 `json:"expected_cents"`
	CountedCents      int64 `json:"counted_cents"`
	OverShortCents    int64 `json:"over_short_cents"`
}

// - inputs -

// TenderInput is the keystone seam input A-19 passes for ONE tender.
//
// SCAFFOLD-OWNED SHARED SEAM (consumed by internal/pos).
type TenderInput struct {
	TillSessionID  string // the OPEN session this tender attaches to
	Method         string // CASH | CHEQUE | CARD | EFT | OTHER
	AmountCents    int64  // > 0
	CustomerID     string // optional (empty = cash walk-in)
	PaymentToken   string // CARD only, client-tokenized (Runner.js); never a PAN
	CardType       string // CARD only, DEBIT|CREDIT|PREPAID|UNKNOWN → payments.card_type
	SignatureRef   string // optional, opaque pointer to A-19's encrypted signature evidence
	IdempotencyKey string // client-generated UUID, the offline-first replay key
	Offline        bool   // tender taken with no connectivity (cash/cheque)
	// Capture is the CARD leg's captured charge (the attempt id and the processor ref the
	// pre-transaction capture loop stamped). A CARD tender without one is refused: TakeTender
	// binds a capture inside this transaction, it never takes one. The bind guards the attempt
	// against this input's own amount, till session and idempotency key (the leg key).
	Capture CardCapture
}

// TenderResult is the keystone seam OUTPUT A-19 receives for ONE tender: the persisted payments-row id
// plus the opaque gateway_ref. gateway_ref is the processor reference for a CARD capture; it is "" for
// CASH/CHEQUE/EFT/OTHER, which carry no gateway reference. The till is the SOLE writer of the payments
// row, so this result is the single source of truth A-19 threads into the Sale response + the
// payment.received event (the authoritative payments.gateway_ref column is written from the same value).
//
// SCAFFOLD-OWNED SHARED SEAM (consumed by internal/pos).
type TenderResult struct {
	PaymentID  string // payments.id of the RECEIVED row (idempotent: a replay returns the same id)
	GatewayRef string // payments.gateway_ref, the processor ref for CARD; "" for non-card tenders

	// Q-044 post-commit audit facts. The caller passes this result to RecordPaymentReceived AFTER its
	// enclosing money tx commits; Inserted=false on an idempotent replay so the receipt record stays
	// exactly-once per committed tender (recording mid-tx orphaned a row on rollback and duplicated
	// it on rollback-retry).
	PaymentNo   string // human payment number of the freshly inserted row ("" on a replay)
	Method      string // tender method as persisted
	AmountCents int64  // tendered amount as persisted
	BranchID    string // the session's branch (attribution on the receipt record)
	Inserted    bool   // true only when THIS call inserted the payments row (false on replay)
}

// RefundInput is the W7 refund sibling of TenderInput: ONE refund rail write inside the caller's tx
// (the POS return tx). CASH writes the REFUNDED payments row AND the PAID_OUT drawer movement against
// an OPEN session; CARD writes the REFUNDED payments row carrying the reversal's gateway ref (the
// gateway call itself happens in RUN-PAY via the pos-declared CardRefunder, the till never sees creds).
// The till stays the SOLE writer of payments + till_movements.
//
// SHARED SEAM (consumed by internal/pos).
type RefundInput struct {
	TillSessionID  string // CASH only, the OPEN drawer the cash leaves
	Method         string // CASH | CARD
	AmountCents    int64  // > 0 (status REFUNDED conveys direction; amount stays positive per the 0010 CHECK)
	BranchID       string // CARD: the payments.branch_id; CASH: must match the session's branch when set
	CustomerID     string // optional
	GatewayRef     string // CARD only, the reversal's processor ref
	Reference      string // return_no, lands on the movement (audit trail on the drawer)
	IdempotencyKey string // replay-safe (distinct from the original sale's key)
}

// RefundResult carries the persisted REFUNDED payments-row id.
type RefundResult struct {
	PaymentID string
}

// OpenTillInput declares the opening float (+ optional OPEN denominations).
type OpenTillInput struct {
	BranchID             string              `json:"branch_id"`
	Station              string              `json:"station"`
	FloatCents           int64               `json:"float_cents"`
	OpeningDenominations []DenominationCount `json:"opening_denominations"`
}

// MovementInput is a paid-in/out/drop/lodgement entry. A lodgement may name its payments, like a
// deposit slip (FROM-PRINCIPAL 128): the names are optional, and when given they must add up to the
// slip's amount, every name on this drawer, this method, still RECEIVED and not reversed.
type MovementInput struct {
	TillSessionID string   `json:"-"`
	MovementType  string   `json:"movement_type"`
	Method        string   `json:"method"`
	AmountCents   int64    `json:"amount_cents"`
	Reason        string   `json:"reason"`
	Reference     string   `json:"reference"`
	PaymentIDs    []string `json:"payment_ids"`
}

// CountInput is the blind per-tender actuals (+ optional CLOSE denominations).
type CountInput struct {
	TillSessionID        string              `json:"-"`
	Tenders              []TenderActual      `json:"tenders"`
	ClosingDenominations []DenominationCount `json:"closing_denominations"`
	ClosingFloatCents    int64               `json:"closing_float_cents"`
}

// TenderActual is one blind-counted method actual.
type TenderActual struct {
	Method      string `json:"method"`
	ActualCents int64  `json:"actual_cents"`
}

// CompleteInput reconciles + signs off (conditional manager gate).
type CompleteInput struct {
	TillSessionID string `json:"-"`
	OverrideNotes string `json:"override_notes"`
}

// - error taxonomy (mirrors internal/pricing) -
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

// ErrCardDeclined is the till's card-declined outcome, the gateway refused/failed the charge.
// Exported (errors.Is-able) so POS can distinguish a real decline (402 to the cashier) from other
// till failures (no open session, branch access, conflict), which must surface as themselves.
// Under the capture seam it is one of the typed capture answers (the adapter translates the
// payment module's into these), read from the attempt row on a retry with no processor call.
var ErrCardDeclined error = errCardDeclined

// IsCardDeclined reports whether err is (or wraps) ErrCardDeclined.
func IsCardDeclined(err error) bool { return errors.Is(err, errCardDeclined) }

// The typed capture answers the seam contract names. The composition root's adapter is the ONE
// place the payment module's own sentinels become these (the posTaxAdapter pattern), so a payment
// word never crosses into pos.
var (
	// ErrCardInFlight: the leg's charge is claimed or captured and not yet bound (a same-key
	// retry or a concurrent submit). Keep the key and ring again shortly.
	ErrCardInFlight = errors.New("till: the card charge for this leg is in flight")
	// ErrCardNeedsManager: the leg has no usable answer, or its reversal's does not: a manager
	// has to close the attempt before the card is charged again.
	ErrCardNeedsManager = errors.New("till: the card charge needs a manager")
	// ErrCardReversed: the leg was captured and then reversed. No money remains.
	ErrCardReversed = errors.New("till: the card charge was reversed")
	// ErrCardResolved: a manager closed the leg's attempt; its key never charges again.
	ErrCardResolved = errors.New("till: the card charge was closed by a manager")
	// ErrCardUnresolvedOnTill: the till guard's refusal, an earlier unresolved attempt for the
	// same amount at this till under another sale key.
	ErrCardUnresolvedOnTill = errors.New("till: an earlier card charge for this amount at this till has no answer yet")
	// ErrCardNotDispatched: nothing reached the processor (no config, a missing credential, a
	// refused key refresh). No money moved; the cause rides behind it.
	ErrCardNotDispatched = errors.New("till: the card charge was not dispatched")
	// ErrCardLegUnattempted: the read-only arm's answer for a leg no ring ever claimed.
	ErrCardLegUnattempted = errors.New("till: this card leg was never attempted")
)

var (
	errNotFound       = clientError{status: http.StatusNotFound, msg: "till: not found"}
	errConflict       = clientError{status: http.StatusConflict, msg: "till: conflict"}
	errOffline        = clientError{status: http.StatusUnprocessableEntity, msg: "till: card tender needs connectivity (cannot be taken offline)"}
	errNotImplemented = clientError{status: http.StatusNotImplemented, msg: "till: not yet implemented"}
	// errSharedForbidden is the refusal that answers with the shared role refusal body (authz.RefuseForbidden),
	// byte identical to every other role refusal and naming no branch.
	errSharedForbidden = errors.New("till: refused")
	errUnauthorized    = clientError{status: http.StatusUnauthorized, msg: "till: unauthenticated"}
	errCardDeclined    = clientError{status: http.StatusPaymentRequired, msg: "till: card declined"}
	// errAlreadyBounced is the idempotent-replay guard for the NSF flow: a payment already marked BOUNCED
	// cannot be bounced a second time (409). Returned by the store's in-tx FOR UPDATE status check.
	errAlreadyBounced = clientError{status: http.StatusConflict, msg: "till: payment already marked BOUNCED"}
	// errPaymentReversed refuses a bounce of a reversed payment (SF-2d round 7): a payment is reversed or bounced,
	// never both. Decided in the store's locked read, mirrored by the service's fast check.
	errPaymentReversed = clientError{status: http.StatusConflict, msg: "till: this payment was reversed, so it cannot be marked bounced (a payment is reversed or bounced, never both)"}
)

// unprocessable maps a SEMANTIC validation failure (well-formed request, value violates a rule) to 422
// , e.g. amount ≤ 0, opening denominations ≠ float, an over-threshold manager close with no note.
func unprocessable(format string, a ...any) error {
	return clientError{status: http.StatusUnprocessableEntity, msg: fmt.Sprintf(format, a...)}
}

// forbidden maps an authorization failure (branch scope, the non-manager over-threshold close) to 403.
func forbidden(format string, a ...any) error {
	return clientError{status: http.StatusForbidden, msg: fmt.Sprintf(format, a...)}
}

// - payments.status + charge-result + card_type vocabularies -
const (
	PaymentReceived = "RECEIVED"
	// PaymentPending is RESERVED (RUN-PAY's 0027) and NEVER written at the W4 gate: today card-offline
	// BLOCKS (D-A03-3). It is the future home of an offline-card "queued, not yet captured" tender, so
	// the queue-later flip is a behaviour change only, no migration.
	PaymentPending = "PENDING"
	// PaymentRefunded is the negative-direction record a POS return writes through RefundTender: the
	// amount stays positive (0010 amount CHECK), the status conveys direction; RECEIVED-filtered reads
	// (expected tenders, AR settles) never pick it up.
	PaymentRefunded = "REFUNDED"
)

// card_type values (payments.card_type, RUN-PAY's 0027 column; method stays single CARD, D-A03-4).
const (
	CardDebit   = "DEBIT"
	CardCredit  = "CREDIT"
	CardPrepaid = "PREPAID"
	CardUnknown = "UNKNOWN"
)

// PaymentRow is the row TakeTender persists. The till is the SOLE writer of payments. The raw
// PaymentToken/PAN is DELIBERATELY not a field here, only the tokenized instrument passes to the
// charger and only the opaque gateway_ref is stored (PCI SAQ-A; never a PAN, never a token on the row).
type PaymentRow struct {
	Method          string
	AmountCents     int64
	Status          string
	BranchID        string
	TillSessionID   string
	CustomerID      string
	GatewayRef      string
	CardType        string // CARD only, DEBIT|CREDIT|PREPAID|UNKNOWN; "" → SQL NULL
	ReceivedBy      string
	IdempotencyKey  string
	OfflineCaptured bool
	SignatureRef    string
}

// MovementRow is one till_movements insert (paid-in/out/drop/lodgement). A lodgement carries the
// deposit slip's named payment ids; the repository writes the slip's link rows and the DEPOSITED
// marks in the movement's own transaction.
type MovementRow struct {
	TillSessionID string
	MovementType  string
	Method        string
	AmountCents   int64
	Reason        string
	Reference     string
	CreatedBy     string
	PaymentIDs    []string
}

// SlipPayment is one named payment's read for a lodgement's deposit slip validation.
type SlipPayment struct {
	ID          string
	AmountCents int64
	Status      string
	Reversed    bool
}

// MovementTotals are the per-session movement sums of ONE tender method, used in the drawer math
// (the Z report's expected totals and the session detail read). The movement table's own vocabulary
// (0028) is movement_type PAID_IN/PAID_OUT/DROP/LODGEMENT per method CASH/CHEQUE/CARD/EFT/OTHER.
type MovementTotals struct {
	PaidIn    int64 // Σ PAID_IN of the method
	PaidOut   int64 // Σ (PAID_OUT + DROP) of the method
	Lodgement int64 // Σ LODGEMENT of the method (banked)
}

// SessionDetail is the operator reopen/lookup read (US-A03-10): the header + its tenders + movements.
type SessionDetail struct {
	TillSession
	Tenders   []TenderTotal `json:"tenders"`
	Movements []Movement    `json:"movements"`
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// - W5 A-05: NSF handling + credit holds + declined-attempt log (0035) -

// hold status vocabulary (0035 credit_holds.status).
const (
	HoldActive   = "ACTIVE"
	HoldReleased = "RELEASED"
)

// NSFPolicy is the runtime-config NSF fee + auto-hold policy (0035 nsf_policy). branch row overrides the
// org default. Every finance number is DATA (a placeholder pending the external accountant, Q-021).
type NSFPolicy struct {
	BranchID       string `json:"branch_id"`
	FeeCents       int64  `json:"fee_cents"`
	FeeTaxable     bool   `json:"fee_taxable"`
	FeeAccountCode string `json:"fee_account_code"`
	AutoHold       bool   `json:"auto_hold"`
	Notes          string `json:"notes"`
}

// NSFInput is the operator's "this cheque bounced" request. The fee/hold are resolved from nsf_policy at
// bounce time, the caller never supplies money.
type NSFInput struct {
	PaymentID string `json:"-"`
	Reason    string `json:"reason"`
}

// nsfReverseInput is the resolved instruction the store executes atomically (policy already applied).
type nsfReverseInput struct {
	PaymentID      string
	Reason         string
	ActorID        string
	FeeCents       int64
	FeeTaxable     bool
	FeeAccountCode string
	AutoHold       bool
}

// nsfReverseResult is what the atomic NSF tx reports back.
type nsfReverseResult struct {
	CustomerID       string
	BranchID         string
	Method           string
	AmountCents      int64
	ReversedCents    int64
	ReopenedInvoices int
	HoldPlaced       bool
}

// NSFResult is the handler-facing outcome of an NSF mark.
type NSFResult struct {
	PaymentID        string `json:"payment_id"`
	CustomerID       string `json:"customer_id,omitempty"`
	FeeCents         int64  `json:"fee_cents"`
	FeeTaxable       bool   `json:"fee_taxable"`
	ReversedCents    int64  `json:"reversed_cents"`
	ReopenedInvoices int    `json:"reopened_invoices"`
	HoldPlaced       bool   `json:"hold_placed"`
	Status           string `json:"status"` // BOUNCED
}

// BouncedPayment is one row in the AR-console bounced queue (payments joined to their NSF record).
type BouncedPayment struct {
	PaymentID   string    `json:"payment_id"`
	PaymentNo   string    `json:"payment_no"`
	CustomerID  string    `json:"customer_id,omitempty"`
	BranchID    string    `json:"branch_id"`
	Method      string    `json:"method"`
	AmountCents int64     `json:"amount_cents"`
	GatewayRef  string    `json:"gateway_ref,omitempty"`
	Reason      string    `json:"reason"`
	FeeCents    int64     `json:"fee_cents"`
	FeeTaxable  bool      `json:"fee_taxable"`
	BouncedAt   time.Time `json:"bounced_at"`
}

// CreditHold is one credit_holds row for the AR-console holds list.
type CreditHold struct {
	ID              string     `json:"id"`
	CustomerID      string     `json:"customer_id"`
	SourcePaymentID string     `json:"source_payment_id,omitempty"`
	Status          string     `json:"status"`
	Reason          string     `json:"reason"`
	ReleaseReason   string     `json:"release_reason,omitempty"`
	PlacedBy        string     `json:"placed_by,omitempty"`
	ReleasedBy      string     `json:"released_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	ReleasedAt      *time.Time `json:"released_at,omitempty"`
}

// ReleaseHoldInput is the Tier-1 credit-hold release (mandatory reason + audit).
type ReleaseHoldInput struct {
	HoldID string `json:"-"`
	Reason string `json:"reason"`
}

// PaymentAttempt is one declined/errored gateway attempt (DX-14; 0035 payment_attempts). A SUCCESSFUL
// capture is the payments row, never an attempt here.
type PaymentAttempt struct {
	IdempotencyKey string `json:"idempotency_key"`
	CustomerID     string `json:"customer_id"`
	AmountCents    int64  `json:"amount_cents"`
	Method         string `json:"method"`
	Processor      string `json:"processor"`
	Result         string `json:"result"` // DECLINED | ERROR
	Detail         string `json:"detail"`
	BranchID       string `json:"branch_id"`
	AttemptedBy    string `json:"-"` // stamped server-side from the authenticated caller, never client-supplied
}
