package quote

// order_payment.go, QD-5: the counter's payment on a converted quote's order. The design is in
// QD5-DESIGN.md, written before this file.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

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

// OrderInvoicer is the billing and order seam the door needs.
type OrderInvoicer interface {
	AgreedInvoice(ctx context.Context, orderID string) (AgreedInvoice, error)
	RaiseAgreedInvoice(ctx context.Context, orderID string) (AgreedInvoice, error)
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

// CounterReceipt is the settle's answer.
type CounterReceipt struct {
	PaymentID   string
	PaymentNo   string
	Method      string
	AmountCents int64
	ReceivedAt  time.Time
	Replayed    bool
}

// CounterPayer is the payments seam the door needs.
type CounterPayer interface {
	TakePayment(ctx context.Context, in CounterReceiptInput) (CounterReceipt, error)
}

// OrderPaymentConflictError is a refusal that names its reason in words and answers 409: the quote
// or order is in a state that cannot take this payment, or the key was used for a different payment.
type OrderPaymentConflictError struct{ Msg string }

func (e *OrderPaymentConflictError) Error() string { return e.Msg }

// NewOrderPaymentConflict is the constructor the composition root's adapters use to carry an
// order or billing refusal across the seam without the quote package importing either module.
func NewOrderPaymentConflict(msg string) error { return &OrderPaymentConflictError{Msg: msg} }

// PaymentNotRecordedError says the action stopped between two commits: the order and invoice exist
// and the payment was not recorded. Repeating the same request, with the same key, finishes it.
type PaymentNotRecordedError struct {
	OrderNo, InvoiceNo string
	Cause              error
}

func (e *PaymentNotRecordedError) Error() string {
	return "quote: order " + e.OrderNo + " and invoice " + e.InvoiceNo +
		" exist but the payment was not recorded; repeat the same request with the same key to finish it"
}
func (e *PaymentNotRecordedError) Unwrap() error { return e.Cause }

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
	OrderPaymentFacts(ctx context.Context, quoteID string) (OrderPaymentFacts, error)
	LockOrderPayments(ctx context.Context, quoteID string) (unlock func(), err error)
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
// pays). Everything checkable is checked before the first write, so a refusal leaves nothing.
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

	// One payment action at a time per quote, so a replay waits for the first call and then takes the
	// settle's replay path, and two different keys race for the balance one after the other.
	unlock, err := ps.LockOrderPayments(ctx, quoteID)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	defer unlock()
	// From here the action is not cancelled by a client that hangs up: it either finishes or stops at
	// a commit boundary with a keyed state a repeat completes.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), actionTimeout)
	defer cancel()

	pf, err := ps.OrderPaymentFacts(wctx, quoteID)
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
		// The customer with no credit: this one action converts, bills and pays. The conversion half
		// keeps its own role rule, so a role that cannot convert cannot do it through here either.
		if !identity.CanSellOnCredit(caller) {
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
		// Everything above is read before the first write. The conversion re-asserts the total under
		// its own lock, and tells the credit gate this order is paid at the counter in this action.
		res, err := runConvert(wctx, s.convertStore(), s.audit, pf.CustomerID, quoteID, caller.UserID,
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

	// The agreed invoice: the document when it exists, else what would be raised. The amount is held
	// to it before anything is raised, so a refused amount leaves no invoice behind.
	agreed, err := s.invoicer.AgreedInvoice(wctx, orderID)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	if cash && in.AmountCents != agreed.TotalCents {
		return OrderPaymentResult{}, httpx.BadRequestf(
			"a customer with no credit account pays the whole agreed total of %s", moneyWords(agreed.TotalCents))
	}
	if in.AmountCents > agreed.TotalCents {
		return OrderPaymentResult{}, httpx.BadRequestf(
			"the payment is more than the agreed total of %s", moneyWords(agreed.TotalCents))
	}
	inv, err := s.invoicer.RaiseAgreedInvoice(wctx, orderID)
	if err != nil {
		return OrderPaymentResult{}, s.unfinished(converted, orderNo, "", err)
	}
	rcpt, err := s.payer.TakePayment(wctx, CounterReceiptInput{
		CustomerID:     pf.CustomerID,
		InvoiceID:      inv.InvoiceID,
		BranchID:       pf.BranchID,
		Method:         in.Method,
		Reference:      in.Reference,
		IdempotencyKey: namespacedPaymentKey(quoteID, in.IdempotencyKey),
		ReceivedBy:     caller.UserID,
		AmountCents:    in.AmountCents,
	})
	if err != nil {
		var br httpx.BadRequestError
		if errors.As(err, &br) {
			return OrderPaymentResult{}, err
		}
		return OrderPaymentResult{}, s.unfinished(converted, orderNo, inv.InvoiceNo, err)
	}
	if rcpt.Replayed && (rcpt.Method != in.Method || rcpt.AmountCents != in.AmountCents) {
		return OrderPaymentResult{}, &OrderPaymentConflictError{
			Msg: "this idempotency key took a different payment; use a new key for a new payment"}
	}
	after, err := s.invoicer.AgreedInvoice(wctx, orderID)
	if err != nil {
		return OrderPaymentResult{}, err
	}
	return OrderPaymentResult{
		OrderID: orderID, OrderNo: orderNo,
		InvoiceID: inv.InvoiceID, InvoiceNo: inv.InvoiceNo, InvoiceTotalCents: inv.TotalCents,
		PaymentID: rcpt.PaymentID, PaymentNo: rcpt.PaymentNo, Method: rcpt.Method,
		AmountCents: rcpt.AmountCents, ReceivedAt: rcpt.ReceivedAt, Reference: in.Reference,
		BalanceCents: after.BalanceCents, Replayed: rcpt.Replayed, Converted: converted,
	}, nil
}

// actionTimeout bounds one payment action once it has begun writing.
const actionTimeout = 60 * time.Second

// unfinished wraps a fault between two commits: when the action had already written something this
// request (a conversion, or an invoice), say exactly what exists. A fault before any write is just
// that fault.
func (s *StaffService) unfinished(converted bool, orderNo, invoiceNo string, cause error) error {
	if !converted && invoiceNo == "" {
		return cause
	}
	return &PaymentNotRecordedError{OrderNo: orderNo, InvoiceNo: invoiceNo, Cause: cause}
}

// namespacedPaymentKey scopes the caller's key to the quote, so a key used on one quote can never
// replay another quote's payment.
func namespacedPaymentKey(quoteID, key string) string {
	return "quote-order-payment:" + quoteID + ":" + key
}
