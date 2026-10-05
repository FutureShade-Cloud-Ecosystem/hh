package quote

// order_payment.go, QD-5: the counter's payment on a converted quote's order. The design is in
// QD5-DESIGN.md. The whole action is ONE transaction on ONE connection: the conversion (for a customer
// with no credit), the order's facts, the invoice and the payment are written on it and commit
// together, or none of them exists.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// OrderPaymentInput is one payment taken at the counter.
type OrderPaymentInput struct {
	Method         string
	AmountCents    int64
	Reference      string
	IdempotencyKey string
}

// OrderPaymentResult is what the till's receipt needs.
type OrderPaymentResult struct {
	OrderID           string    `json:"order_id"`
	OrderNo           string    `json:"order_no"`
	InvoiceID         string    `json:"invoice_id"`
	InvoiceNo         string    `json:"invoice_no"`
	InvoiceTotalCents int64     `json:"invoice_total_cents"`
	PaymentID         string    `json:"payment_id"`
	PaymentNo         string    `json:"payment_no"`
	Method            string    `json:"method"`
	AmountCents       int64     `json:"amount_cents"`
	ReceivedAt        time.Time `json:"received_at"`
	Reference         string    `json:"reference"`
	ReceivedBy        string    `json:"received_by"`
	BalanceCents      int64     `json:"balance_cents"`
	Replayed          bool      `json:"replayed"`
	Converted         bool      `json:"converted"`
}

// AgreedInvoice is an order's invoice at the agreed totals: the document when it exists, else what
// would be raised.
type AgreedInvoice struct {
	Exists       bool
	InvoiceID    string
	InvoiceNo    string
	CustomerID   string
	BranchID     string
	TotalCents   int64
	BalanceCents int64
}

// OrderInvoicer is the billing and order seam the door needs, every method on the door's one
// transaction: the order may have been written in it a moment ago and is not yet visible to any other
// connection.
type OrderInvoicer interface {
	AgreedInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (AgreedInvoice, error)
	RaiseAgreedInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (AgreedInvoice, error)
}

// CounterReceiptInput is one already taken payment to settle against one invoice.
type CounterReceiptInput struct {
	CustomerID     string
	InvoiceID      string
	BranchID       string
	Method         string
	Reference      string
	IdempotencyKey string
	ReceivedBy     string
	AmountCents    int64
}

// CounterReceipt is the settle's answer: the payment as stored, the reference and the receiver
// included, also when the key replays an earlier payment.
type CounterReceipt struct {
	PaymentID   string
	PaymentNo   string
	Method      string
	AmountCents int64
	ReceivedAt  time.Time
	Reference   string
	ReceivedBy  string
	Replayed    bool
}

// CounterPayer is the payments seam the door needs, on the door's one transaction.
type CounterPayer interface {
	// KeyTakenTx says whether a payment already holds the key, so the door can tell a replay, which it
	// answers with the stored payment, from a first payment, which it holds to the invoice's balance.
	KeyTakenTx(ctx context.Context, tx pgx.Tx, key string) (bool, error)
	// LockPaymentsFeedSeqTx takes the payments feed sequence row lock on the door's transaction: the
	// payments module owns the mechanism, and the door takes it BEFORE it raises anything so that it
	// holds the payments sequence row before the invoices one, the one order every writer that both
	// bills and settles follows, and a settle beside the door queues behind it instead of deadlocking
	// with it on the two rows (40P01, an aborted counter action whose card charge already happened).
	LockPaymentsFeedSeqTx(ctx context.Context, tx pgx.Tx) error
	TakePaymentTx(ctx context.Context, tx pgx.Tx, in CounterReceiptInput) (CounterReceipt, error)
}

// OrderPaymentConflictError is a refusal that names its reason in words and answers 409: the quote
// or order is in a state that cannot take this payment, or the key was used for a different payment.
type OrderPaymentConflictError struct{ Msg string }

func (e *OrderPaymentConflictError) Error() string { return e.Msg }

// NewOrderPaymentConflict is the constructor the composition root's adapters use to carry an
// order or billing refusal across the seam without the quote package importing either module.
func NewOrderPaymentConflict(msg string) error { return &OrderPaymentConflictError{Msg: msg} }

// yardManagerRole is the one role the cash customer's combined action admits beyond the selling roles.
const yardManagerRole = "YARD_MANAGER"

// counterTenders are the tenders the payments settle accepts at a desk (payments.CounterTenders).
// Declared here because the quote package imports no money module; the settle validates the same
// set again, so a drift here is a refusal in the wrong place, never a wrong posting.
var counterTenders = map[string]bool{"CASH": true, "CARD": true, "CHEQUE": true, "EFT": true}

// paymentKeyPattern bounds the caller's key: it is namespaced by the quote and stored by payments.
var paymentKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

const maxPaymentReferenceLen = 80

// orderPaymentStore is the persistence the door needs beyond the staff store, asserted like the
// conversion's pricing context so a store that predates it fails the door closed.
type orderPaymentStore interface {
	// InOrderPaymentTx runs fn in ONE transaction on ONE pool connection. The first statement takes the
	// quote's transaction scoped advisory lock, so same quote and same key calls queue behind each
	// other without holding a second connection, and the lock leaves with the transaction. fn's ctx
	// carries the transaction for the conversion; tx is the same transaction for the seams.
	InOrderPaymentTx(ctx context.Context, quoteID string, fn func(ctx context.Context, tx pgx.Tx) error) error
	OrderPaymentFactsTx(ctx context.Context, tx pgx.Tx, quoteID string) (OrderPaymentFacts, error)
}

// OrderPaymentFacts is what the door reads off the quote under its per quote lock: the status, whose
// quote and which branch, the agreed figures and whether they agree with the lines, the customer's
// credit posture and the orders the quote's acceptances became (the quote's own provenance).
type OrderPaymentFacts struct {
	Status           string
	CustomerID       string
	BranchID         string
	SubtotalCents    int64
	TaxCents         int64
	FeeCents         int64
	TotalCents       int64
	LinesCents       int64
	CreditLimitCents int64
	Orders           []PaymentOrderRef
}

// PaymentOrderRef names one order a quote became.
type PaymentOrderRef struct{ ID, No string }

// WithOrderInvoicer injects the order and billing seam.
func (s *StaffService) WithOrderInvoicer(i OrderInvoicer) *StaffService { s.invoicer = i; return s }

// WithCounterPayer injects the payments seam.
func (s *StaffService) WithCounterPayer(p CounterPayer) *StaffService { s.payer = p; return s }

// moneyWords renders cents for a refusal a person reads at the counter.
func moneyWords(cents int64) string { return money(cents) }

// TakeOrderPayment is the counter's payment on a converted quote's order (QD5-DESIGN.md).
//
// The gate holds Work or above on the Counter Till. This method holds what the gate cannot: an
// active caller, reach to the quote's branch, the shape of the request, and that the quote is
// converted (or is a cash customer's accepted quote, which this one action converts, bills and
// pays). Everything is written in one transaction, so any refusal or fault leaves nothing behind.
func (s *StaffService) TakeOrderPayment(ctx context.Context, idOrNo string, in OrderPaymentInput) (OrderPaymentResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	if caller.Status != identity.StatusActive {
		return OrderPaymentResult{}, ErrForbidden
	}
	in.Method = strings.ToUpper(strings.TrimSpace(in.Method))
	in.Reference = strings.TrimSpace(in.Reference)
	if !counterTenders[in.Method] {
		return OrderPaymentResult{}, httpx.BadRequestf("method must be one of CASH, CARD, CHEQUE, EFT")
	}
	if !paymentKeyPattern.MatchString(in.IdempotencyKey) {
		return OrderPaymentResult{}, httpx.BadRequestf(
			"a payment needs an idempotency key of 1 to 64 letters, digits, dot, colon, dash or underscore")
	}
	if in.AmountCents <= 0 {
		return OrderPaymentResult{}, httpx.BadRequestf("the payment amount must be more than zero")
	}
	if len(in.Reference) > maxPaymentReferenceLen {
		return OrderPaymentResult{}, httpx.BadRequestf("the payment reference is limited to %d characters", maxPaymentReferenceLen)
	}
	ps, ok := s.store.(orderPaymentStore)
	if !ok || s.invoicer == nil || s.payer == nil {
		return OrderPaymentResult{}, ErrEditNotConfigured
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err := s.store.ResolveQuoteID(ctx, idOrNo, rGlobal, rBranches)
	if err != nil {
		return OrderPaymentResult{}, err
	}

	// A client that hangs up does not cancel the action halfway: the transaction either commits whole
	// or rolls back whole, and a repeat with the same key answers whichever happened.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), actionTimeout)
	defer cancel()
	var res OrderPaymentResult
	err = ps.InOrderPaymentTx(wctx, quoteID, func(txCtx context.Context, tx pgx.Tx) error {
		var err error
		res, err = s.takeOrderPaymentTx(txCtx, tx, ps, caller, quoteID, in)
		return err
	})
	if err != nil {
		return OrderPaymentResult{}, err
	}
	return res, nil
}

// takeOrderPaymentTx is the action, on the transaction the store opened and holds the quote's lock in.
// Every read and every write here goes through that transaction; nothing in here calls out of the
// process.
func (s *StaffService) takeOrderPaymentTx(ctx context.Context, tx pgx.Tx, ps orderPaymentStore,
	caller identity.StaffContext, quoteID string, in OrderPaymentInput) (OrderPaymentResult, error) {

	pf, err := ps.OrderPaymentFactsTx(ctx, tx, quoteID)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	if !caller.CanAccessBranch(pf.BranchID) {
		return OrderPaymentResult{}, ErrForbidden
	}

	cash := pf.CreditLimitCents <= 0
	converted := false
	var orderID, orderNo string
	switch pf.Status {
	case "CONVERTED":
		switch len(pf.Orders) {
		case 0:
			return OrderPaymentResult{}, &OrderPaymentConflictError{Msg: "this quote is converted but no order is recorded against it; look it up before taking payment"}
		case 1:
			orderID, orderNo = pf.Orders[0].ID, pf.Orders[0].No
		default:
			return OrderPaymentResult{}, &OrderPaymentConflictError{Msg: fmt.Sprintf(
				"this quote became %d orders; take the payment from the order's own document", len(pf.Orders))}
		}
	case "ACCEPTED":
		if !cash {
			return OrderPaymentResult{}, &OrderPaymentConflictError{
				Msg: "convert this quote to an order first; a customer with a credit account converts through the conversion door and then pays"}
		}
		// The customer with no credit: this one action converts, bills and pays. Converting an
		// ACCEPTED quote at its agreed totals as part of taking its payment asks no more authority
		// than ringing a sale, so the Yard manager, who rings sales on the till, is admitted here
		// (the principal's ruling on decision 3); nothing in the action is editable. The separate
		// conversion of a quote, with no payment, keeps the selling role rule and still refuses it.
		if !identity.CanSellOnCredit(caller) && caller.RoleCode != yardManagerRole {
			return OrderPaymentResult{}, &NotSellingRoleError{RoleCode: caller.RoleCode}
		}
		if in.AmountCents != pf.TotalCents {
			return OrderPaymentResult{}, httpx.BadRequestf(
				"a customer with no credit account pays the whole agreed total of %s, a balance left on the invoice would be credit the customer does not have",
				moneyWords(pf.TotalCents))
		}
		if pf.LinesCents != pf.SubtotalCents || pf.SubtotalCents+pf.FeeCents+pf.TaxCents != pf.TotalCents {
			return OrderPaymentResult{}, &OrderPaymentConflictError{
				Msg: "the quote's total does not agree with its lines, so it cannot be converted and paid until that is looked at"}
		}
		// The conversion on this transaction, through the one conversion path, with the option that
		// tells the credit gate this order is paid at the counter in this same action. The conversion
		// re-asserts the total under its own lock.
		res, err := runConvert(withTx(ctx, tx), s.convertStore(), s.audit, pf.CustomerID, quoteID, caller.UserID,
			ConvertOptions{paidAtCounter: true, paidTotalCents: pf.TotalCents})
		if err != nil {
			return OrderPaymentResult{}, err
		}
		converted = true
		orderID, orderNo = res.OrderID, res.OrderNo
	default:
		return OrderPaymentResult{}, &OrderPaymentConflictError{Msg: fmt.Sprintf(
			"this quote is %s; only a converted quote's order takes a payment", strings.ToLower(pf.Status))}
	}

	// The agreed invoice: the document when it exists, else what would be raised, read on this
	// transaction (the order may be this action's own). The amount is held to it before anything is
	// raised; a refusal here rolls the whole action back.
	agreed, err := s.invoicer.AgreedInvoiceTx(ctx, tx, orderID)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	paymentKey := namespacedPaymentKey(quoteID, in.IdempotencyKey)
	replay, err := s.payer.KeyTakenTx(ctx, tx, paymentKey)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	if !replay {
		// Held to what the invoice still owes, not to its total: once a deposit is on it, the rest is the
		// balance whatever the customer's credit has become since. A customer with no credit pays all of
		// it at once; nobody pays more than it. Nothing owed is its own answer: a cashier who tries
		// another payment on a paid order is told the order is paid, never asked for a balance of
		// $0.00.
		if cash && in.AmountCents != agreed.BalanceCents {
			if agreed.BalanceCents == 0 {
				return OrderPaymentResult{}, httpx.BadRequestf("The order is paid in full.")
			}
			if agreed.BalanceCents == agreed.TotalCents {
				return OrderPaymentResult{}, httpx.BadRequestf(
					"a customer with no credit account pays the whole agreed total of %s", moneyWords(agreed.TotalCents))
			}
			return OrderPaymentResult{}, httpx.BadRequestf(
				"a customer with no credit account pays the whole balance of %s", moneyWords(agreed.BalanceCents))
		}
		if in.AmountCents > agreed.BalanceCents {
			return OrderPaymentResult{}, httpx.BadRequestf(
				"the payment is more than the balance of %s", moneyWords(agreed.BalanceCents))
		}
	}
	// The payments sequence row is taken BEFORE the invoice is raised (the sequence order fix): the
	// settle below takes the payments feed's sequence row and then the invoices feed's, and so does
	// every other writer that both bills and settles (the register settle, the POS ring, the payments
	// settle itself). Taking the payments row first puts this action on that one order, so a settle
	// running beside it queues behind this transaction on the payments row instead of holding it
	// while this action holds the invoices row: the two rows in opposite orders is the 40P01 the
	// review reproduced, and the side Postgres aborts here is an action whose card charge has
	// already happened. The settle's own numbering later re-takes the same lock this transaction
	// already holds.
	if err := s.payer.LockPaymentsFeedSeqTx(ctx, tx); err != nil {
		return OrderPaymentResult{}, err
	}
	inv, err := s.invoicer.RaiseAgreedInvoiceTx(ctx, tx, orderID)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	rcpt, err := s.payer.TakePaymentTx(ctx, tx, CounterReceiptInput{
		CustomerID:     pf.CustomerID,
		InvoiceID:      inv.InvoiceID,
		BranchID:       pf.BranchID,
		Method:         in.Method,
		Reference:      in.Reference,
		IdempotencyKey: paymentKey,
		ReceivedBy:     caller.UserID,
		AmountCents:    in.AmountCents,
	})
	if err != nil {
		return OrderPaymentResult{}, err
	}
	if rcpt.Replayed && (rcpt.Method != in.Method || rcpt.AmountCents != in.AmountCents) {
		return OrderPaymentResult{}, &OrderPaymentConflictError{
			Msg: "this idempotency key took a different payment; use a new key for a new payment"}
	}
	after, err := s.invoicer.AgreedInvoiceTx(ctx, tx, orderID)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	return OrderPaymentResult{
		OrderID: orderID, OrderNo: orderNo,
		InvoiceID: inv.InvoiceID, InvoiceNo: inv.InvoiceNo, InvoiceTotalCents: inv.TotalCents,
		PaymentID: rcpt.PaymentID, PaymentNo: rcpt.PaymentNo, Method: rcpt.Method,
		AmountCents: rcpt.AmountCents, ReceivedAt: rcpt.ReceivedAt, Reference: rcpt.Reference,
		ReceivedBy:   rcpt.ReceivedBy,
		BalanceCents: after.BalanceCents, Replayed: rcpt.Replayed, Converted: converted,
	}, nil
}

// actionTimeout bounds one payment action, lock wait included.
const actionTimeout = 60 * time.Second

// namespacedPaymentKey scopes the caller's key to the quote, so a key used on one quote can never
// replay another quote's payment.
func namespacedPaymentKey(quoteID, key string) string {
	return "quote-order-payment:" + quoteID + ":" + key
}
