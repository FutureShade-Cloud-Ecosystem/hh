package billing

// register_money.go, the ONE seam between the receivables register and the money.
//
// -- THE MODULE-BOUNDARY CALL, STATED PLAINLY --------------------------------------------------
// The house rule is that reads cross modules through Go interfaces and writes cross as events. This
// write does not: `Record payment` on the register calls internal/payments' settle synchronously,
// through the interface declared in register_service.go, and this file is the adapter that satisfies
// it.
//
// It is the right call here and the reasoning is worth writing down rather than assuming:
//
//   1. internal/payments ALREADY writes billing's tables. Its package doc says so in as many words -
//      it owns portal_payment_methods and WRITES payments + payment_allocations + customer_transactions
//      "inside one atomic transaction", updating invoices.balance_cents as it goes. The traffic between
//      these two modules is not new; what is new is that it now goes in the honest direction, with
//      billing asking payments to do the thing payments owns.
//
//   2. An EVENT would be wrong for this specific write. The operator is standing at a desk with a
//      cheque, and the answer they need is "the balance is now CA$0.00", not "we have queued your
//      intention". A fire-and-forget receipt is a receipt that can silently not happen, and the one
//      thing a receivables screen may never do is tell somebody money was applied when it was not.
//
//   3. There is exactly ONE settle. The alternative to calling theirs is writing ours, and two
//      implementations of "apply money to an invoice" is how a ledger acquires two answers to one
//      question. RecordReceipt shares its transaction body with the shipped PayWithIntent, see
//      payments/counter_receipt.go.
//
// This file is deliberately thin: translation and nothing else. No policy, no validation, no
// arithmetic, those live in the service above it and in the payments module below it, which are the
// two places that can be tested.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// PaymentsReceiptWriter adapts *payments.Repository onto the register's money seam.
type PaymentsReceiptWriter struct{ repo *payments.Repository }

// NewPaymentsReceiptWriter binds the register to the payments settle.
func NewPaymentsReceiptWriter(repo *payments.Repository) *PaymentsReceiptWriter {
	return &PaymentsReceiptWriter{repo: repo}
}

// RecordReceipt hands one already-taken payment to the payments module and translates the answer.
func (w *PaymentsReceiptWriter) RecordReceipt(ctx context.Context, in ReceiptWrite) (ReceiptResult, error) {
	out, err := w.repo.RecordReceipt(ctx, payments.ReceiptRequest{
		CustomerID:     in.CustomerID,
		Method:         in.Method,
		Reference:      in.Reference,
		IdempotencyKey: in.IdempotencyKey,
		ReceivedBy:     in.ReceivedBy,
		BranchID:       in.BranchID,
		Allocations: []payments.Allocation{{
			InvoiceID:   in.InvoiceID,
			AmountCents: in.AmountCents,
		}},
	})
	if err != nil {
		return ReceiptResult{}, err
	}
	return ReceiptResult{
		PaymentID:   out.Payment.ID,
		PaymentNo:   out.Payment.PaymentNo,
		AmountCents: out.Payment.AmountCents,
		Status:      out.Payment.Status,
		Replayed:    out.Replayed,
	}, nil
}

// PayableCentsFor is the payments module's own ceiling, passed through unchanged. The register draws
// its Record Payment maximum from this so the screen's limit and the write's limit are one number.
func (w *PaymentsReceiptWriter) PayableCentsFor(ctx context.Context, invoiceID string) (int64, string, error) {
	return w.repo.PayableCentsFor(ctx, invoiceID)
}
