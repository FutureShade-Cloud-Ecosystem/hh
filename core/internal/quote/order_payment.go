package quote

// order_payment.go, QD-5: the counter's payment on a converted quote's order. The design is in
// QD5-DESIGN.md, written before this file.

import (
	"context"
	"errors"
	"time"
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

var errOrderPaymentNotBuilt = errors.New("quote: order payment not built")

// WithOrderInvoicer injects the order and billing seam.
func (s *StaffService) WithOrderInvoicer(i OrderInvoicer) *StaffService { s.invoicer = i; return s }

// WithCounterPayer injects the payments seam.
func (s *StaffService) WithCounterPayer(p CounterPayer) *StaffService { s.payer = p; return s }

// TakeOrderPayment is the door (stub, red first).
func (s *StaffService) TakeOrderPayment(ctx context.Context, idOrNo string, in OrderPaymentInput) (OrderPaymentResult, error) {
	return OrderPaymentResult{}, errOrderPaymentNotBuilt
}
