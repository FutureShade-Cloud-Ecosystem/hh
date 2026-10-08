package pos

// repository_pay_balance.go, the pgx side of item 9.4 (Pay balance at the
// counter, pay_balance.go). The module owns none of the tables the door
// reads or writes, so nothing here is SQL: each read and write goes through
// the owning module's seam (pay_balance_sources.go), on a read only
// transaction for the advisory reads and on the take's own for the locked
// read and the settle, which follow the one money lock order
// (payments/LOCK-ORDER.md) with the order row in front.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// settleInvoice is one payment slice applied to one invoice inside the take's
// transaction.
type settleInvoice struct {
	PaymentID, InvoiceID string
	CustomerID, BranchID string
	OrderID              string
	Method               string
	GatewayRef           string
	AmountCents          int64
	AllocatedBy          string
}

var errPayBalanceSourcesUnwired = errors.New("pos: the pay balance sources are not wired")

// WithPayBalanceSources binds the owning modules' seams the Pay balance door
// reads and writes through. Unwired, the door answers an error, never an empty
// or clear answer.
func (r *Repository) WithPayBalanceSources(s PayBalanceSources) *Repository {
	r.payBalance = s
	return r
}

// payBalanceRead runs fn on one read only repeatable read transaction, so the
// several owning modules' answers to one advisory read share a snapshot.
func (r *Repository) payBalanceRead(ctx context.Context, fn func(tx pgx.Tx, src PayBalanceSources) error) error {
	if r.pool == nil {
		return errNoPool
	}
	if r.payBalance == nil {
		return errPayBalanceSourcesUnwired
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("pos: pay balance read begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return fn(tx, r.payBalance)
}

// owedOf is the ONE number the door and the hold share: the open balances of
// the order's live invoices plus the uninvoiced remainder of its total.
func owedOf(m PayBalanceInvoiceMoney, totalCents int64) int64 {
	owed := m.OpenCents
	if rest := totalCents - m.InvoicedCents; rest > 0 {
		owed += rest
	}
	return owed
}

// payBalanceCandidatePage is the page the find walks the order module's
// candidates in, and payBalanceCandidatePages how many pages it walks before it
// answers what it has: an order owes by the money modules' arithmetic, which
// the order module cannot filter on, so the find pages until it has enough
// owing orders or runs out of candidates.
const (
	payBalanceCandidatePage  = 50
	payBalanceCandidatePages = 10
	payBalanceCustomerReach  = 500
)

// FindOwingOrders answers the orders that owe money and match the term: an
// order number (the visible #SO form and bare), a customer name, or a phone.
// branchIDs nil reaches every branch (a global caller); otherwise the order's
// branch must be one of them, which is the whole fence: another branch's
// order is simply not in the answer, and neither is a cancelled order (not
// this door's, the delta review's P2-A).
func (r *Repository) FindOwingOrders(ctx context.Context, term string, branchIDs []string, limit int) ([]OwingOrder, error) {
	if limit <= 0 {
		limit = 20
	}
	like := "%" + escapeLikeWildcards(term) + "%"
	var out []OwingOrder
	err := r.payBalanceRead(ctx, func(tx pgx.Tx, src PayBalanceSources) error {
		customerIDs, err := src.MatchPayBalanceCustomers(ctx, tx, like, payBalanceCustomerReach)
		if err != nil {
			return err
		}
		for page := 0; page < payBalanceCandidatePages && len(out) < limit; page++ {
			cands, err := src.PayBalanceOrderCandidates(ctx, tx, like, customerIDs, branchIDs, payBalanceCandidatePage, page*payBalanceCandidatePage)
			if err != nil {
				return err
			}
			if len(cands) == 0 {
				break
			}
			orderIDs := make([]string, 0, len(cands))
			custSeen := map[string]bool{}
			custIDs := make([]string, 0, len(cands))
			for _, c := range cands {
				orderIDs = append(orderIDs, c.OrderID)
				if !custSeen[c.CustomerID] {
					custSeen[c.CustomerID] = true
					custIDs = append(custIDs, c.CustomerID)
				}
			}
			money, err := src.PayBalanceInvoiceMoney(ctx, tx, orderIDs)
			if err != nil {
				return err
			}
			custs, err := src.PayBalanceCustomers(ctx, tx, custIDs)
			if err != nil {
				return err
			}
			for _, c := range cands {
				m := money[c.OrderID]
				owed := owedOf(m, c.TotalCents)
				if owed <= 0 || len(out) >= limit {
					continue
				}
				cust := custs[c.CustomerID]
				out = append(out, OwingOrder{
					OrderID: c.OrderID, OrderNo: c.OrderNo, CustomerID: c.CustomerID, CustomerName: cust.Name,
					Job: c.Job, Fulfilment: c.Fulfilment, OrderTotalCents: c.TotalCents,
					PaidCents: m.PaidCents, StillOwedCents: owed,
					HeldFromPicking: cust.TermsDays <= 0, pastPicking: c.PastPicking,
				})
			}
			if len(cands) < payBalanceCandidatePage {
				break
			}
		}
		if len(out) == 0 {
			return nil
		}
		// The money trail, for the page: each payment applied to the order's
		// invoices, with its method and amount, attached AFTER the rows are
		// collected.
		orderIDs := make([]string, 0, len(out))
		for _, o := range out {
			orderIDs = append(orderIDs, o.OrderID)
		}
		trail, err := payBalanceTrail(ctx, tx, src, orderIDs)
		if err != nil {
			return err
		}
		for i := range out {
			out[i].Payments = trail[out[i].OrderID]
			if out[i].Payments == nil {
				out[i].Payments = []OwingPayment{}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// payBalanceTrail reads the money trail of each order on the transaction: the
// billing module names the orders' invoices, the payments module the RECEIVED
// payments allocated to them, oldest first.
func payBalanceTrail(ctx context.Context, tx pgx.Tx, src PayBalanceSources, orderIDs []string) (map[string][]OwingPayment, error) {
	byOrder, err := src.PayBalanceInvoiceIDs(ctx, tx, orderIDs)
	if err != nil {
		return nil, err
	}
	var invoiceIDs []string
	orderOf := map[string]string{}
	for orderID, ids := range byOrder {
		for _, id := range ids {
			invoiceIDs = append(invoiceIDs, id)
			orderOf[id] = orderID
		}
	}
	trail := map[string][]OwingPayment{}
	if len(invoiceIDs) == 0 {
		return trail, nil
	}
	pays, err := src.PayBalanceReceivedPayments(ctx, tx, invoiceIDs)
	if err != nil {
		return nil, err
	}
	for _, p := range pays {
		orderID := orderOf[p.InvoiceID]
		trail[orderID] = append(trail[orderID], OwingPayment{PaymentID: p.PaymentID, Method: p.Method, AmountCents: p.AmountCents})
	}
	return trail, nil
}

// escapeLikeWildcards makes the term's own LIKE wildcards literal (the
// backslash is Postgres' default LIKE escape): a customer typed "%", or an
// underscore inside a reference, and the find matched every owing order in
// reach instead of the one whose text carries the character.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLikeWildcards(term string) string { return likeEscaper.Replace(term) }

// ReadOwingOrder is the advisory read of one order: its facts and the one
// number it owes (the open balances plus the uninvoiced remainder). found
// false is the door's 404 (a cancelled order among them, never owing); an
// order that owes nothing is a different answer, the caller refuses it in
// words.
func (r *Repository) ReadOwingOrder(ctx context.Context, orderID string) (payBalanceOrderFacts, bool, error) {
	var f payBalanceOrderFacts
	found := false
	err := r.payBalanceRead(ctx, func(tx pgx.Tx, src PayBalanceSources) error {
		o, ok, err := src.PayBalanceOrderFacts(ctx, tx, orderID)
		if err != nil || !ok || o.Status == payBalanceOrderCancelled {
			return err
		}
		custs, err := src.PayBalanceCustomers(ctx, tx, []string{o.CustomerID})
		if err != nil {
			return err
		}
		money, err := src.PayBalanceInvoiceMoney(ctx, tx, []string{o.OrderID})
		if err != nil {
			return err
		}
		m := money[o.OrderID]
		f = payBalanceOrderFacts{
			OrderID: o.OrderID, OrderNo: o.OrderNo, CustomerID: o.CustomerID, CustomerName: custs[o.CustomerID].Name,
			BranchID: o.BranchID, Fulfilment: o.Fulfilment, Status: o.Status,
			TermsDays: custs[o.CustomerID].TermsDays, TotalCents: o.TotalCents, InvoicedCents: m.InvoicedCents,
			// The pool read carries no invoice list: balanceCents() over these
			// facts is the same one number (open balances plus remainder) the
			// locked read re-derives under the locks.
			Invoices: []payBalanceInvoice{{BalanceCents: m.OpenCents}},
		}
		found = true
		return nil
	})
	if err != nil {
		return payBalanceOrderFacts{}, false, err
	}
	return f, found, nil
}

// FindPayBalancePaymentByKey answers the payments row a take's key already
// wrote, with the order its allocation paid (the replay answer's facts).
func (r *Repository) FindPayBalancePaymentByKey(ctx context.Context, key string) (payBalanceReplayRow, bool, error) {
	var row payBalanceReplayRow
	found := false
	err := r.payBalanceRead(ctx, func(tx pgx.Tx, src PayBalanceSources) error {
		p, ok, err := src.PayBalancePaymentByKey(ctx, tx, key)
		if err != nil || !ok {
			return err
		}
		row = payBalanceReplayRow{PaymentID: p.PaymentID, Method: p.Method, AmountCents: p.AmountCents}
		if p.FirstInvoiceID != "" {
			if row.OrderID, err = src.PayBalanceOrderForInvoice(ctx, tx, p.FirstInvoiceID); err != nil {
				return err
			}
		}
		found = true
		return nil
	})
	if err != nil {
		return payBalanceReplayRow{}, false, err
	}
	return row, found, nil
}

// LockPayBalanceOrderTx is the one-order writer's locked read, in the money
// lock order: the order row, the payments sequence row without advancing it,
// then the order's open invoice rows FOR NO KEY UPDATE ascending. The invoice
// lock predicate is the same one the advisory pre read's sums use (live
// INVOICE documents with a positive open balance), so the balance the locks
// re-derive is the balance the pre read refused or captured against.
func (r *Repository) LockPayBalanceOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (payBalanceOrderFacts, error) {
	if r.payBalance == nil {
		return payBalanceOrderFacts{}, errPayBalanceSourcesUnwired
	}
	src := r.payBalance
	o, ok, err := src.LockPayBalanceOrder(ctx, tx, orderID)
	if err != nil {
		return payBalanceOrderFacts{}, err
	}
	if !ok || o.Status == payBalanceOrderCancelled {
		return payBalanceOrderFacts{}, ErrPayBalanceOrderNotFound
	}
	f := payBalanceOrderFacts{
		OrderID: o.OrderID, OrderNo: o.OrderNo, CustomerID: o.CustomerID, BranchID: o.BranchID,
		Fulfilment: o.Fulfilment, Status: o.Status, TotalCents: o.TotalCents,
	}
	custs, err := src.PayBalanceCustomers(ctx, tx, []string{o.CustomerID})
	if err != nil {
		return payBalanceOrderFacts{}, err
	}
	cust, ok := custs[o.CustomerID]
	if !ok {
		return payBalanceOrderFacts{}, fmt.Errorf("pos: pay balance customer %s not found", o.CustomerID)
	}
	f.CustomerName, f.TermsDays = cust.Name, cust.TermsDays
	money, err := src.PayBalanceInvoiceMoney(ctx, tx, []string{orderID})
	if err != nil {
		return payBalanceOrderFacts{}, err
	}
	f.InvoicedCents = money[orderID].InvoicedCents
	if err := src.LockPayBalancePaymentsSequence(ctx, tx); err != nil {
		return payBalanceOrderFacts{}, err
	}
	open, err := src.LockPayBalanceInvoices(ctx, tx, orderID)
	if err != nil {
		return payBalanceOrderFacts{}, err
	}
	for _, inv := range open {
		f.Invoices = append(f.Invoices, payBalanceInvoice{InvoiceID: inv.InvoiceID, InvoiceNo: inv.InvoiceNo, BalanceCents: inv.BalanceCents})
	}
	return f, nil
}

// PayBalanceHoldTx is the hold's own reading on the caller's transaction, the
// very read billing's pick hold makes (the one SQL number): owed is the open
// balances plus the uninvoiced remainder, held is zero day terms with money
// outstanding. The take's answer carries these figures, read after its money
// moved.
func (r *Repository) PayBalanceHoldTx(ctx context.Context, tx pgx.Tx, orderID string) (payBalanceHoldView, error) {
	if r.payBalance == nil {
		return payBalanceHoldView{}, errPayBalanceSourcesUnwired
	}
	held, owed, _, _, err := r.payBalance.PayBalanceHold(ctx, tx, orderID)
	if err != nil {
		return payBalanceHoldView{}, fmt.Errorf("pos: pay balance hold read: %w", err)
	}
	return payBalanceHoldView{OwedCents: owed, Held: held}, nil
}

// SettleInvoiceTx applies one payment slice to one invoice through the
// payments module's settle (one spelling of applying money to an invoice; a
// second would be how the ledger acquires two answers to one question).
func (r *Repository) SettleInvoiceTx(ctx context.Context, tx pgx.Tx, s settleInvoice) error {
	if r.payBalance == nil {
		return errPayBalanceSourcesUnwired
	}
	return r.payBalance.AllocatePayBalance(ctx, tx, PayBalanceAllocation{
		PaymentID: s.PaymentID, InvoiceID: s.InvoiceID, CustomerID: s.CustomerID, BranchID: s.BranchID,
		AllocatedBy: s.AllocatedBy, AmountCents: s.AmountCents,
	})
}

// OrderPaymentsTrailTx reads the money trail on the caller's transaction.
func (r *Repository) OrderPaymentsTrailTx(ctx context.Context, tx pgx.Tx, orderID string) ([]OwingPayment, error) {
	if r.payBalance == nil {
		return nil, errPayBalanceSourcesUnwired
	}
	trail, err := payBalanceTrail(ctx, tx, r.payBalance, []string{orderID})
	if err != nil {
		return nil, err
	}
	out := trail[orderID]
	if out == nil {
		out = []OwingPayment{}
	}
	return out, nil
}

// OrderPaymentsTrail is the pool twin.
func (r *Repository) OrderPaymentsTrail(ctx context.Context, orderID string) ([]OwingPayment, error) {
	var out []OwingPayment
	err := r.payBalanceRead(ctx, func(tx pgx.Tx, src PayBalanceSources) error {
		trail, err := payBalanceTrail(ctx, tx, src, []string{orderID})
		if err != nil {
			return err
		}
		out = trail[orderID]
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []OwingPayment{}
	}
	return out, nil
}
