package main

// wire_pay_balance.go, item 9.4: the adapter that binds the POS Pay balance
// door's seam (pos.PayBalanceSources) to the owning modules' repositories, so
// the door reads and writes the orders, customers, invoices and payments
// tables only through the modules that own them.

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

// payBalanceSources satisfies pos.PayBalanceSources over the four owners.
type payBalanceSources struct {
	orders    *order.Repository
	customers *customer.Repository
	billing   *billing.Repository
	payments  *payments.Repository
}

func newPayBalanceSources(pool *pgxpool.Pool) payBalanceSources {
	return payBalanceSources{
		orders: order.NewRepository(pool), customers: customer.NewRepository(pool),
		billing: holdBillingRepo(pool), payments: payments.NewRepository(pool),
	}
}

func orderRow(o order.PayBalanceOrder) pos.PayBalanceOrderRow {
	return pos.PayBalanceOrderRow{
		OrderID: o.OrderID, OrderNo: o.OrderNo, CustomerID: o.CustomerID, BranchID: o.BranchID,
		Fulfilment: o.Fulfilment, Status: o.Status, Job: o.Job, TotalCents: o.TotalCents, PastPicking: o.PastPicking,
	}
}

func (a payBalanceSources) PayBalanceOrderCandidates(ctx context.Context, tx pgx.Tx, like string, customerIDs, branchIDs []string, limit, offset int) ([]pos.PayBalanceOrderRow, error) {
	rows, err := a.orders.PayBalanceOrderCandidates(ctx, tx, like, customerIDs, branchIDs, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]pos.PayBalanceOrderRow, 0, len(rows))
	for _, o := range rows {
		out = append(out, orderRow(o))
	}
	return out, nil
}

func (a payBalanceSources) PayBalanceOrderFacts(ctx context.Context, tx pgx.Tx, orderID string) (pos.PayBalanceOrderRow, bool, error) {
	o, ok, err := a.orders.PayBalanceOrderFacts(ctx, tx, orderID)
	return orderRow(o), ok, err
}

func (a payBalanceSources) LockPayBalanceOrder(ctx context.Context, tx pgx.Tx, orderID string) (pos.PayBalanceOrderRow, bool, error) {
	o, ok, err := a.orders.LockPayBalanceOrderTx(ctx, tx, orderID)
	return orderRow(o), ok, err
}

func (a payBalanceSources) MatchPayBalanceCustomers(ctx context.Context, tx pgx.Tx, like string, limit int) ([]string, error) {
	return a.customers.MatchPayBalanceCustomers(ctx, tx, like, limit)
}

func (a payBalanceSources) PayBalanceCustomers(ctx context.Context, tx pgx.Tx, ids []string) (map[string]pos.PayBalanceCustomerRow, error) {
	rows, err := a.customers.PayBalanceCustomers(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]pos.PayBalanceCustomerRow, len(rows))
	for id, c := range rows {
		out[id] = pos.PayBalanceCustomerRow{Name: c.Name, TermsDays: c.TermsDays}
	}
	return out, nil
}

func (a payBalanceSources) PayBalanceInvoiceMoney(ctx context.Context, tx pgx.Tx, orderIDs []string) (map[string]pos.PayBalanceInvoiceMoney, error) {
	rows, err := a.billing.OrderInvoiceMoney(ctx, tx, orderIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]pos.PayBalanceInvoiceMoney, len(rows))
	for id, m := range rows {
		out[id] = pos.PayBalanceInvoiceMoney{OpenCents: m.OpenCents, InvoicedCents: m.InvoicedCents, PaidCents: m.PaidCents}
	}
	return out, nil
}

func (a payBalanceSources) PayBalanceInvoiceIDs(ctx context.Context, tx pgx.Tx, orderIDs []string) (map[string][]string, error) {
	return a.billing.OrderInvoiceIDs(ctx, tx, orderIDs)
}

func (a payBalanceSources) PayBalanceOrderForInvoice(ctx context.Context, tx pgx.Tx, invoiceID string) (string, error) {
	return a.billing.OrderIDForInvoiceTx(ctx, tx, invoiceID)
}

func (a payBalanceSources) LockPayBalanceInvoices(ctx context.Context, tx pgx.Tx, orderID string) ([]pos.PayBalanceOpenInvoice, error) {
	rows, err := a.billing.LockOpenOrderInvoicesTx(ctx, tx, orderID)
	if err != nil {
		return nil, err
	}
	out := make([]pos.PayBalanceOpenInvoice, 0, len(rows))
	for _, inv := range rows {
		out = append(out, pos.PayBalanceOpenInvoice{InvoiceID: inv.InvoiceID, InvoiceNo: inv.InvoiceNo, BalanceCents: inv.BalanceCents})
	}
	return out, nil
}

func (a payBalanceSources) PayBalanceHold(ctx context.Context, tx pgx.Tx, orderID string) (bool, int64, int64, int64, error) {
	return a.billing.PickHoldForOrderTx(ctx, tx, orderID)
}

func (a payBalanceSources) PayBalancePaymentByKey(ctx context.Context, tx pgx.Tx, key string) (pos.PayBalanceKeyedPayment, bool, error) {
	p, ok, err := a.payments.PaymentByKeyTx(ctx, tx, key)
	return pos.PayBalanceKeyedPayment{PaymentID: p.PaymentID, Method: p.Method, AmountCents: p.AmountCents, FirstInvoiceID: p.FirstInvoiceID}, ok, err
}

func (a payBalanceSources) LockPayBalancePaymentsSequence(ctx context.Context, tx pgx.Tx) error {
	return a.payments.LockPaymentsSequenceTx(ctx, tx)
}

func (a payBalanceSources) PayBalanceReceivedPayments(ctx context.Context, tx pgx.Tx, invoiceIDs []string) ([]pos.PayBalanceInvoicePayment, error) {
	rows, err := a.payments.ReceivedPaymentsForInvoices(ctx, tx, invoiceIDs)
	if err != nil {
		return nil, err
	}
	out := make([]pos.PayBalanceInvoicePayment, 0, len(rows))
	for _, p := range rows {
		out = append(out, pos.PayBalanceInvoicePayment{InvoiceID: p.InvoiceID, PaymentID: p.PaymentID, Method: p.Method, AmountCents: p.AmountCents})
	}
	return out, nil
}

func (a payBalanceSources) AllocatePayBalance(ctx context.Context, tx pgx.Tx, s pos.PayBalanceAllocation) error {
	return a.payments.AllocateToInvoiceTx(ctx, tx, payments.OrderPayment{
		PaymentID: s.PaymentID, InvoiceID: s.InvoiceID, CustomerID: s.CustomerID, BranchID: s.BranchID,
		AmountCents: s.AmountCents, AllocatedBy: s.AllocatedBy,
	})
}
