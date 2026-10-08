//go:build integration

package pos

// pay_balance_bridge_integration_test.go, the integration legs' copy of the
// composition root's wiring (cmd/core wire_pay_balance.go and
// wire_pick_hold.go): the Pay balance seam bound to the owning modules'
// repositories, and a billing repository whose pick hold reads the order's
// total and the customer's terms through them.

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// testBillingRepo is a billing repository with the pick hold sources bound.
func testBillingRepo(pool *pgxpool.Pool) *billing.Repository {
	return billing.NewRepository(pool).WithPickHoldSources(order.NewRepository(pool), customer.NewRepository(pool))
}

// testPosRepo is a pos repository with the Pay balance sources bound.
func testPosRepo(pool *pgxpool.Pool) *Repository {
	return NewRepository(pool).WithPayBalanceSources(testPayBalanceSources{
		orders: order.NewRepository(pool), customers: customer.NewRepository(pool),
		billing: testBillingRepo(pool), payments: payments.NewRepository(pool),
	})
}

type testPayBalanceSources struct {
	orders    *order.Repository
	customers *customer.Repository
	billing   *billing.Repository
	payments  *payments.Repository
}

func testOrderRow(o order.PayBalanceOrder) PayBalanceOrderRow {
	return PayBalanceOrderRow{
		OrderID: o.OrderID, OrderNo: o.OrderNo, CustomerID: o.CustomerID, BranchID: o.BranchID,
		Fulfilment: o.Fulfilment, Status: o.Status, Job: o.Job, TotalCents: o.TotalCents, PastPicking: o.PastPicking,
	}
}

func (a testPayBalanceSources) PayBalanceCustomerInvoiced(ctx context.Context, tx pgx.Tx, customerIDs []string, limit int) ([]PayBalanceInvoicedOrder, bool, error) {
	rows, truncated, err := a.billing.CustomerOrderInvoiceFigures(ctx, tx, customerIDs, limit)
	out := make([]PayBalanceInvoicedOrder, 0, len(rows))
	for _, f := range rows {
		out = append(out, PayBalanceInvoicedOrder{OrderID: f.OrderID, OpenCents: f.OpenCents, InvoicedCents: f.InvoicedCents})
	}
	return out, truncated, err
}

func (a testPayBalanceSources) PayBalanceOrderCandidates(ctx context.Context, tx pgx.Tx, like string, customerIDs, branchIDs []string, invoiced []PayBalanceInvoicedOrder, limit, offset int) ([]PayBalanceOrderRow, error) {
	figures := make([]order.PayBalanceInvoiced, 0, len(invoiced))
	for _, f := range invoiced {
		figures = append(figures, order.PayBalanceInvoiced{OrderID: f.OrderID, OpenCents: f.OpenCents, InvoicedCents: f.InvoicedCents})
	}
	rows, err := a.orders.PayBalanceOrderCandidates(ctx, tx, like, customerIDs, branchIDs, figures, limit, offset)
	out := make([]PayBalanceOrderRow, 0, len(rows))
	for _, o := range rows {
		out = append(out, testOrderRow(o))
	}
	return out, err
}

func (a testPayBalanceSources) PayBalanceOrderFacts(ctx context.Context, tx pgx.Tx, orderID string) (PayBalanceOrderRow, bool, error) {
	o, ok, err := a.orders.PayBalanceOrderFacts(ctx, tx, orderID)
	return testOrderRow(o), ok, err
}

func (a testPayBalanceSources) LockPayBalanceOrder(ctx context.Context, tx pgx.Tx, orderID string) (PayBalanceOrderRow, bool, error) {
	o, ok, err := a.orders.LockPayBalanceOrderTx(ctx, tx, orderID)
	return testOrderRow(o), ok, err
}

func (a testPayBalanceSources) MatchPayBalanceCustomers(ctx context.Context, tx pgx.Tx, like string, limit int) ([]string, error) {
	return a.customers.MatchPayBalanceCustomers(ctx, tx, like, limit)
}

func (a testPayBalanceSources) PayBalanceCustomers(ctx context.Context, tx pgx.Tx, ids []string) (map[string]PayBalanceCustomerRow, error) {
	rows, err := a.customers.PayBalanceCustomers(ctx, tx, ids)
	out := make(map[string]PayBalanceCustomerRow, len(rows))
	for id, c := range rows {
		out[id] = PayBalanceCustomerRow{Name: c.Name, TermsDays: c.TermsDays}
	}
	return out, err
}

func (a testPayBalanceSources) PayBalanceInvoiceMoney(ctx context.Context, tx pgx.Tx, orderIDs []string) (map[string]PayBalanceInvoiceMoney, error) {
	rows, err := a.billing.OrderInvoiceMoney(ctx, tx, orderIDs)
	out := make(map[string]PayBalanceInvoiceMoney, len(rows))
	for id, m := range rows {
		out[id] = PayBalanceInvoiceMoney{OpenCents: m.OpenCents, InvoicedCents: m.InvoicedCents, PaidCents: m.PaidCents}
	}
	return out, err
}

func (a testPayBalanceSources) PayBalanceInvoiceIDs(ctx context.Context, tx pgx.Tx, orderIDs []string) (map[string][]string, error) {
	return a.billing.OrderInvoiceIDs(ctx, tx, orderIDs)
}

func (a testPayBalanceSources) PayBalanceOrderForInvoice(ctx context.Context, tx pgx.Tx, invoiceID string) (string, error) {
	return a.billing.OrderIDForInvoiceTx(ctx, tx, invoiceID)
}

func (a testPayBalanceSources) LockPayBalanceInvoices(ctx context.Context, tx pgx.Tx, orderID string) ([]PayBalanceOpenInvoice, error) {
	rows, err := a.billing.LockOpenOrderInvoicesTx(ctx, tx, orderID)
	out := make([]PayBalanceOpenInvoice, 0, len(rows))
	for _, inv := range rows {
		out = append(out, PayBalanceOpenInvoice{InvoiceID: inv.InvoiceID, InvoiceNo: inv.InvoiceNo, BalanceCents: inv.BalanceCents})
	}
	return out, err
}

func (a testPayBalanceSources) PayBalanceHold(ctx context.Context, tx pgx.Tx, orderID string) (bool, int64, int64, int64, error) {
	return a.billing.PickHoldForOrderTx(ctx, tx, orderID)
}

func (a testPayBalanceSources) PayBalancePaymentByKey(ctx context.Context, tx pgx.Tx, key string) (PayBalanceKeyedPayment, bool, error) {
	p, ok, err := a.payments.PaymentByKeyTx(ctx, tx, key)
	return PayBalanceKeyedPayment{PaymentID: p.PaymentID, Method: p.Method, AmountCents: p.AmountCents, FirstInvoiceID: p.FirstInvoiceID}, ok, err
}

func (a testPayBalanceSources) LockPayBalancePaymentsSequence(ctx context.Context, tx pgx.Tx) error {
	return a.payments.LockPaymentsSequenceTx(ctx, tx)
}

func (a testPayBalanceSources) PayBalanceReceivedPayments(ctx context.Context, tx pgx.Tx, invoiceIDs []string) ([]PayBalanceInvoicePayment, error) {
	rows, err := a.payments.ReceivedPaymentsForInvoices(ctx, tx, invoiceIDs)
	out := make([]PayBalanceInvoicePayment, 0, len(rows))
	for _, p := range rows {
		out = append(out, PayBalanceInvoicePayment{InvoiceID: p.InvoiceID, PaymentID: p.PaymentID, Method: p.Method, AmountCents: p.AmountCents})
	}
	return out, err
}

func (a testPayBalanceSources) AllocatePayBalance(ctx context.Context, tx pgx.Tx, s PayBalanceAllocation) error {
	return a.payments.AllocateToInvoiceTx(ctx, tx, payments.OrderPayment{
		PaymentID: s.PaymentID, InvoiceID: s.InvoiceID, CustomerID: s.CustomerID, BranchID: s.BranchID,
		AmountCents: s.AmountCents, AllocatedBy: s.AllocatedBy,
	})
}
