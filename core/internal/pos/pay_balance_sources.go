package pos

// pay_balance_sources.go, the reads and writes the Pay balance door (item 9.4)
// reaches the other modules' tables through. The orders, customers and
// contacts, invoices, and payments and allocations tables are not pos's: this
// module declares the narrow seam it needs, and the composition root binds it
// to the owning modules' repositories (cmd/core wire_pay_balance.go). Every
// method takes the caller's transaction, so the pool reads open one read only
// transaction and the take's locked reads and writes ride the take's own, in
// the money lock order of payments/LOCK-ORDER.md.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// PayBalanceOrderRow is the order module's answer for one order: its own
// header, never its money or its customer.
type PayBalanceOrderRow struct {
	OrderID, OrderNo, CustomerID, BranchID string
	Fulfilment, Status, Job                string
	TotalCents                             int64
	// PastPicking is the order module's own ruling that the order has reached
	// or passed the picking rung.
	PastPicking bool
}

// PayBalanceCustomerRow is the customer module's answer: the name and terms.
type PayBalanceCustomerRow struct {
	Name      string
	TermsDays int
}

// PayBalanceInvoiceMoney is the billing module's answer for one order's live
// INVOICE documents: the open balances, what they bill, what they have taken.
type PayBalanceInvoiceMoney struct {
	OpenCents, InvoicedCents, PaidCents int64
}

// PayBalanceOpenInvoice is one open invoice of an order, locked by billing.
type PayBalanceOpenInvoice struct {
	InvoiceID, InvoiceNo string
	BalanceCents         int64
}

// PayBalanceKeyedPayment is the payments module's row for an idempotency key.
type PayBalanceKeyedPayment struct {
	PaymentID, Method string
	AmountCents       int64
	FirstInvoiceID    string
}

// PayBalanceInvoicePayment is one RECEIVED payment allocated to one invoice.
type PayBalanceInvoicePayment struct {
	InvoiceID, PaymentID, Method string
	AmountCents                  int64
}

// PayBalanceAllocation is one payment slice applied to one invoice.
type PayBalanceAllocation struct {
	PaymentID, InvoiceID, CustomerID, BranchID, AllocatedBy string
	AmountCents                                             int64
}

// PayBalanceSources is the whole seam, one method per owning module read or
// write the door makes.
type PayBalanceSources interface {
	// The order module.
	PayBalanceOrderCandidates(ctx context.Context, tx pgx.Tx, like string, customerIDs, branchIDs []string, limit, offset int) ([]PayBalanceOrderRow, error)
	PayBalanceOrderFacts(ctx context.Context, tx pgx.Tx, orderID string) (PayBalanceOrderRow, bool, error)
	LockPayBalanceOrder(ctx context.Context, tx pgx.Tx, orderID string) (PayBalanceOrderRow, bool, error)
	// The customer module.
	MatchPayBalanceCustomers(ctx context.Context, tx pgx.Tx, like string, limit int) ([]string, error)
	PayBalanceCustomers(ctx context.Context, tx pgx.Tx, ids []string) (map[string]PayBalanceCustomerRow, error)
	// The billing module.
	PayBalanceInvoiceMoney(ctx context.Context, tx pgx.Tx, orderIDs []string) (map[string]PayBalanceInvoiceMoney, error)
	PayBalanceInvoiceIDs(ctx context.Context, tx pgx.Tx, orderIDs []string) (map[string][]string, error)
	PayBalanceOrderForInvoice(ctx context.Context, tx pgx.Tx, invoiceID string) (string, error)
	LockPayBalanceInvoices(ctx context.Context, tx pgx.Tx, orderID string) ([]PayBalanceOpenInvoice, error)
	// PayBalanceHold is billing's pay before release hold read, the ONE owed
	// number the find, the pre read and the take's answer share.
	PayBalanceHold(ctx context.Context, tx pgx.Tx, orderID string) (held bool, owedCents, paidCents, totalCents int64, err error)
	// The payments module.
	PayBalancePaymentByKey(ctx context.Context, tx pgx.Tx, key string) (PayBalanceKeyedPayment, bool, error)
	LockPayBalancePaymentsSequence(ctx context.Context, tx pgx.Tx) error
	PayBalanceReceivedPayments(ctx context.Context, tx pgx.Tx, invoiceIDs []string) ([]PayBalanceInvoicePayment, error)
	AllocatePayBalance(ctx context.Context, tx pgx.Tx, a PayBalanceAllocation) error
}
