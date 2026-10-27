//go:build integration

package pos

// returns_dispositions_integration_test.go, sub item 10.3: the three choices on a returned line
// (RESTOCK, DAMAGED, NO_RETURN), the write off of damaged goods (the Q4 ruling) and the D1 fix, a
// RESTOCK line on goods that never left the shelf moves no stock (the Q5 ruling: credit only).

import (
	"context"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// recordingRestocker is the stock seam as a witness: every restock the return asks for, verbatim.
type recordingRestocker struct {
	mu    sync.Mutex
	calls []RestockInput
}

func (r *recordingRestocker) RestockForReturnTx(_ context.Context, _ pgx.Tx, in RestockInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, in)
	return nil
}

func (r *recordingRestocker) ReturnMoveValuesTx(context.Context, pgx.Tx, string) ([]inventory.ShipLineValue, error) {
	return nil, nil
}

func (r *recordingRestocker) ShippedValueForOrderLineTx(context.Context, pgx.Tx, string, string, float64) (inventory.ShipLineValue, bool, error) {
	return inventory.ShipLineValue{}, false, nil
}

func (r *recordingRestocker) qtyFor(returnID string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var q float64
	for _, c := range r.calls {
		if c.ReturnID == returnID {
			for _, l := range c.Lines {
				q += l.QtyBase
			}
		}
	}
	return q
}

type dispFixture struct {
	t         *testing.T
	pool      *pgxpool.Pool
	ctx       context.Context
	svc       *Service
	stock     *recordingRestocker
	productID string
	branchID  string
	custID    string
	orders    []string
}

func newDispFixture(t *testing.T) *dispFixture {
	t.Helper()
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	suffix := fmt.Sprintf("disp-%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := pool.QueryRow(ctx, `
INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
VALUES ('DSP-'||$1, 'Dispositions proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	stock := &recordingRestocker{}
	f := &dispFixture{t: t, pool: pool, ctx: ctx, productID: productID, branchID: branchID, custID: custID, stock: stock,
		svc: realPosSvc(t, pool, stubCharger{}).WithRoleConfig(modifiedDefault(t, roleconfig.SettingRefundApprovalLimitEnabled, false)).WithRestocker(stock).WithOpenQuantityReleaser(order.NewRepository(pool))}
	t.Cleanup(func() {
		c := context.Background()
		for _, oid := range f.orders {
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'order_id' = $1`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_return_lines WHERE return_id IN (SELECT id FROM pos_returns WHERE order_id = $1::uuid)`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_returns WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, oid)
		}
		_, _ = pool.Exec(c, `DELETE FROM return_notes WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})
	return f
}

// ring rings one account sale of the paver and answers the order id and its one line.
func (f *dispFixture) ring(saleType string, qty float64) (orderID, lineID string) {
	f.t.Helper()
	sale, err := f.svc.Sale(f.ctx, SaleInput{
		IdempotencyKey: key(), BranchID: f.branchID, CustomerID: f.custID,
		SaleType: saleType, Tender: TenderAccount,
		Lines: []LineInput{{ProductID: f.productID, QtyEntered: qty, UOMEntered: "SF"}},
	})
	if err != nil {
		f.t.Fatalf("ring the %s sale: %v", saleType, err)
	}
	f.orders = append(f.orders, sale.OrderID)
	if err := f.pool.QueryRow(f.ctx, `SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, sale.OrderID).Scan(&lineID); err != nil {
		f.t.Fatalf("the sale's line: %v", err)
	}
	return sale.OrderID, lineID
}

func (f *dispFixture) note(orders ...ReturnAcrossOrderInput) (ReturnNote, error) {
	return f.svc.ReturnAcross(f.ctx, ReturnAcrossInput{
		IdempotencyKey: key(), CustomerID: f.custID, Orders: orders,
		RefundMethod: RefundAccountCredit, Reason: "dispositions proof",
	})
}

func (f *dispFixture) events(topic, returnID string) int64 {
	f.t.Helper()
	var n int64
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM events_outbox WHERE topic = $1 AND payload->>'return_id' = $2`, topic, returnID).Scan(&n); err != nil {
		f.t.Fatalf("count %s: %v", topic, err)
	}
	return n
}

func (f *dispFixture) lineFacts(returnID string) (disposition string, restocked, writtenOff float64) {
	f.t.Helper()
	if err := f.pool.QueryRow(f.ctx, `SELECT disposition, restocked_base::float8, written_off_base::float8 FROM pos_return_lines WHERE return_id = $1::uuid`, returnID).
		Scan(&disposition, &restocked, &writtenOff); err != nil {
		f.t.Fatalf("the return line's facts: %v", err)
	}
	return disposition, restocked, writtenOff
}

// D1: a RESTOCK line on an order whose goods never left the shelf moves no stock, books no COGS
// reversal and still credits the money in full; the single order path carries the same fix.
func TestIntegration_Dispositions_RestockMovesNoStockForGoodsThatNeverLeftTheShelf(t *testing.T) {
	f := newDispFixture(t)
	laterOrder, laterLine := f.ring(SaleCollectLater, 4)

	// The single order path, whole-return restock flag on.
	ret, err := f.svc.Return(f.ctx, ReturnInput{
		IdempotencyKey: key(), OrderID: laterOrder, RefundMethod: RefundAccountCredit, Restock: true,
		Lines: []ReturnLineInput{{OrderLineID: laterLine, QtyBase: 4, ReasonCode: "OTHER"}},
	})
	if err != nil {
		t.Fatalf("the single return: %v", err)
	}
	if ret.TotalCents <= 0 {
		t.Fatalf("the credit is the money in full: %+v", ret)
	}
	if q := f.stock.qtyFor(ret.ReturnID); q != 0 {
		t.Fatalf("goods that never left the shelf must not be restocked, the seam heard %.4f", q)
	}
	if n := f.events("order.returned", ret.ReturnID); n != 0 {
		t.Fatalf("no COGS reversal for goods whose COGS was never booked, got %d order.returned", n)
	}
	if _, restocked, _ := f.lineFacts(ret.ReturnID); restocked != 0 {
		t.Fatalf("the line records no restocked quantity, got %.4f", restocked)
	}

	// The note path with an explicit RESTOCK line carries the same fix.
	order2, line2 := f.ring(SaleCollectLater, 3)
	note, err := f.note(ReturnAcrossOrderInput{OrderID: order2, Lines: []ReturnLineInput{{OrderLineID: line2, QtyBase: 3, ReasonCode: "OTHER", Disposition: DispositionRestock}}})
	if err != nil {
		t.Fatalf("the note: %v", err)
	}
	child := note.Returns[0]
	if child.TotalCents <= 0 || f.stock.qtyFor(child.ReturnID) != 0 || f.events("order.returned", child.ReturnID) != 0 {
		t.Fatalf("a RESTOCK line on an unfulfilled order is credit only: total %d, stock %.4f, events %d",
			child.TotalCents, f.stock.qtyFor(child.ReturnID), f.events("order.returned", child.ReturnID))
	}

	// A DAMAGED line on goods that never left the shelf writes nothing off either.
	order3, line3 := f.ring(SaleCollectLater, 2)
	note, err = f.note(ReturnAcrossOrderInput{OrderID: order3, Lines: []ReturnLineInput{{OrderLineID: line3, QtyBase: 2, ReasonCode: "DAMAGED", Disposition: DispositionDamaged}}})
	if err != nil {
		t.Fatalf("the damaged note: %v", err)
	}
	if n := f.events("pos.return.written_off", note.Returns[0].ReturnID); n != 0 {
		t.Fatalf("goods that never left the shelf cannot be damaged, got %d write off events", n)
	}
}

// The three choices on goods that left the shelf: each child books what its disposition says, the
// event payloads carry exactly the lines the choice touches, the money reversal is whole in all three.
func TestIntegration_Dispositions_TheThreeChoices(t *testing.T) {
	f := newDispFixture(t)
	oR, lR := f.ring(SaleCollectNow, 5)
	oD, lD := f.ring(SaleCollectNow, 4)
	oN, lN := f.ring(SaleCollectNow, 3)

	note, err := f.note(
		ReturnAcrossOrderInput{OrderID: oR, Lines: []ReturnLineInput{{OrderLineID: lR, QtyBase: 5, ReasonCode: "OTHER", Disposition: DispositionRestock}}},
		ReturnAcrossOrderInput{OrderID: oD, Lines: []ReturnLineInput{{OrderLineID: lD, QtyBase: 4, ReasonCode: "DAMAGED", Disposition: DispositionDamaged}}},
		ReturnAcrossOrderInput{OrderID: oN, Lines: []ReturnLineInput{{OrderLineID: lN, QtyBase: 3, ReasonCode: "OTHER", Disposition: DispositionNoReturn}}},
	)
	if err != nil {
		t.Fatalf("the mixed note: %v", err)
	}
	byOrder := map[string]Return{}
	var sum int64
	for _, r := range note.Returns {
		byOrder[r.OrderID] = r
		sum += r.TotalCents
		if n := f.events("pos.return.committed", r.ReturnID); n != 1 {
			t.Fatalf("each child books its money reversal once, got %d", n)
		}
	}
	if sum != note.TotalCents {
		t.Fatalf("the children sum to the note: %d vs %d", sum, note.TotalCents)
	}

	r := byOrder[oR]
	if d, restocked, off := f.lineFacts(r.ReturnID); d != DispositionRestock || restocked != 5 || off != 0 {
		t.Fatalf("RESTOCK: disposition %s restocked %.4f written off %.4f", d, restocked, off)
	}
	if f.stock.qtyFor(r.ReturnID) != 5 || f.events("order.returned", r.ReturnID) != 1 || f.events("pos.return.written_off", r.ReturnID) != 0 {
		t.Fatal("RESTOCK puts the goods back and reverses the COGS, nothing is written off")
	}

	d := byOrder[oD]
	if disp, restocked, off := f.lineFacts(d.ReturnID); disp != DispositionDamaged || restocked != 0 || off != 4 {
		t.Fatalf("DAMAGED: disposition %s restocked %.4f written off %.4f", disp, restocked, off)
	}
	if f.stock.qtyFor(d.ReturnID) != 0 || f.events("order.returned", d.ReturnID) != 0 || f.events("pos.return.written_off", d.ReturnID) != 1 {
		t.Fatal("DAMAGED moves no stock to the shelf, reverses no COGS to inventory and writes the goods off once")
	}
	var payloadQty float64
	if err := f.pool.QueryRow(f.ctx, `SELECT (payload->'lines'->0->>'qty_base')::float8 FROM events_outbox WHERE topic = 'pos.return.written_off' AND payload->>'return_id' = $1`, d.ReturnID).Scan(&payloadQty); err != nil || payloadQty != 4 {
		t.Fatalf("the write off event names the goods written off (%.4f, %v)", payloadQty, err)
	}

	n := byOrder[oN]
	if disp, restocked, off := f.lineFacts(n.ReturnID); disp != DispositionNoReturn || restocked != 0 || off != 0 {
		t.Fatalf("NO_RETURN: disposition %s restocked %.4f written off %.4f", disp, restocked, off)
	}
	if f.stock.qtyFor(n.ReturnID) != 0 || f.events("order.returned", n.ReturnID) != 0 || f.events("pos.return.written_off", n.ReturnID) != 0 {
		t.Fatal("NO_RETURN books the money reversal only")
	}
}

// A partly fulfilled line: only the part that left the shelf goes back; the rest of the return is
// credit only. The credit is the whole returned quantity's money.
func TestIntegration_Dispositions_PartlyFulfilledRestocksOnlyWhatLeft(t *testing.T) {
	f := newDispFixture(t)
	o, l := f.ring(SaleCollectLater, 6)
	if _, err := f.pool.Exec(f.ctx, `UPDATE order_lines SET fulfilled_base = 4 WHERE id = $1::uuid`, l); err != nil {
		t.Fatalf("mark four units fulfilled: %v", err)
	}
	note, err := f.note(ReturnAcrossOrderInput{OrderID: o, Lines: []ReturnLineInput{{OrderLineID: l, QtyBase: 5, ReasonCode: "OTHER", Disposition: DispositionRestock}}})
	if err != nil {
		t.Fatalf("the note: %v", err)
	}
	child := note.Returns[0]
	if got := f.stock.qtyFor(child.ReturnID); got != 4 {
		t.Fatalf("only the four units that left the shelf go back, the seam heard %.4f", got)
	}
	if _, restocked, _ := f.lineFacts(child.ReturnID); restocked != 4 {
		t.Fatalf("the line records the four restocked units, got %.4f", restocked)
	}
	// The credit is the five units' money.
	prev, err := f.svc.PreviewReturnAcross(f.ctx, ReturnPreviewInput{CustomerID: f.custID, Orders: []ReturnAcrossOrderInput{{OrderID: o, Lines: []ReturnLineInput{{OrderLineID: l, QtyBase: 1}}}}})
	if err != nil {
		t.Fatalf("the remainder preview: %v", err)
	}
	if child.TotalCents <= 0 || prev.TotalCents <= 0 {
		t.Fatalf("both the return and the remaining unit carry money: %d, %d", child.TotalCents, prev.TotalCents)
	}
	// A second return of the last unit finds nothing physical left (4 of 4 went back): credit only.
	next, err := f.note(ReturnAcrossOrderInput{OrderID: o, Lines: []ReturnLineInput{{OrderLineID: l, QtyBase: 1, ReasonCode: "OTHER", Disposition: DispositionRestock}}})
	if err != nil {
		t.Fatalf("the second note: %v", err)
	}
	if got := f.stock.qtyFor(next.Returns[0].ReturnID); got != 0 {
		t.Fatalf("the unfulfilled unit is credit only, the seam heard %.4f", got)
	}
}

// The whole-return restock flag still means what it did where no line carries a disposition: true
// is RESTOCK on every line, false is NO_RETURN; a disposition names only the three words.
func TestIntegration_Dispositions_TheFlagStillMeansWhatItDidAndUnknownWordsAreRefused(t *testing.T) {
	f := newDispFixture(t)
	o1, l1 := f.ring(SaleCollectNow, 2)
	ret, err := f.svc.Return(f.ctx, ReturnInput{
		IdempotencyKey: key(), OrderID: o1, RefundMethod: RefundAccountCredit, Restock: true,
		Lines: []ReturnLineInput{{OrderLineID: l1, QtyBase: 2, ReasonCode: "OTHER"}},
	})
	if err != nil {
		t.Fatalf("the restock flag return: %v", err)
	}
	if d, restocked, _ := f.lineFacts(ret.ReturnID); d != DispositionRestock || restocked != 2 || f.stock.qtyFor(ret.ReturnID) != 2 {
		t.Fatalf("restock true is RESTOCK on every line: %s %.4f", d, restocked)
	}
	o2, l2 := f.ring(SaleCollectNow, 2)
	ret, err = f.svc.Return(f.ctx, ReturnInput{
		IdempotencyKey: key(), OrderID: o2, RefundMethod: RefundAccountCredit, Restock: false,
		Lines: []ReturnLineInput{{OrderLineID: l2, QtyBase: 2, ReasonCode: "OTHER"}},
	})
	if err != nil {
		t.Fatalf("the no restock return: %v", err)
	}
	if d, restocked, off := f.lineFacts(ret.ReturnID); d != DispositionNoReturn || restocked != 0 || off != 0 || f.stock.qtyFor(ret.ReturnID) != 0 {
		t.Fatalf("restock false is NO_RETURN on every line: %s %.4f %.4f", d, restocked, off)
	}

	o3, l3 := f.ring(SaleCollectNow, 2)
	bad := []ReturnLineInput{{OrderLineID: l3, QtyBase: 1, ReasonCode: "OTHER", Disposition: "SCRAP"}}
	_, err = f.note(ReturnAcrossOrderInput{OrderID: o3, Lines: bad})
	wantRefusalStatus(t, err, http.StatusUnprocessableEntity)
	_, err = f.svc.PreviewReturnAcross(f.ctx, ReturnPreviewInput{CustomerID: f.custID, Orders: []ReturnAcrossOrderInput{{OrderID: o3, Lines: bad}}})
	wantRefusalStatus(t, err, http.StatusUnprocessableEntity)
	_, err = f.svc.Return(f.ctx, ReturnInput{IdempotencyKey: key(), OrderID: o3, RefundMethod: RefundAccountCredit, Lines: bad})
	wantRefusalStatus(t, err, http.StatusUnprocessableEntity)
}

func wantRefusalStatus(t *testing.T, err error, want int) {
	t.Helper()
	var ce clientError
	if !errors.As(err, &ce) || ce.status != want {
		t.Fatalf("want a %d refusal in words, got %v", want, err)
	}
}

// fixedCost is the cost basis seam as a witness: every line costs 700 cents.
type fixedCost struct{}

func (fixedCost) UnitCostCents(_ context.Context, _ string, _ float64) (int64, bool, error) {
	return 700, true, nil
}

// The mixed note on the real books through the real consumer: every journal balances, the money
// reversals sum to the note, the restocked child reverses COGS to inventory, the damaged child moves
// its cost from COGS to 5200, the kept child books no stock side at all, and the netted cost
// legs are exactly what the choices say.
func TestIntegration_Dispositions_TheMixedNoteBooksBalance(t *testing.T) {
	f := newDispFixture(t)
	oR, lR := f.ring(SaleCollectNow, 5)
	oD, lD := f.ring(SaleCollectNow, 4)
	oN, lN := f.ring(SaleCollectNow, 3)
	note, err := f.note(
		ReturnAcrossOrderInput{OrderID: oR, Lines: []ReturnLineInput{{OrderLineID: lR, QtyBase: 5, ReasonCode: "OTHER", Disposition: DispositionRestock}}},
		ReturnAcrossOrderInput{OrderID: oD, Lines: []ReturnLineInput{{OrderLineID: lD, QtyBase: 4, ReasonCode: "DAMAGED", Disposition: DispositionDamaged}}},
		ReturnAcrossOrderInput{OrderID: oN, Lines: []ReturnLineInput{{OrderLineID: lN, QtyBase: 3, ReasonCode: "OTHER", Disposition: DispositionNoReturn}}},
	)
	if err != nil {
		t.Fatalf("the mixed note: %v", err)
	}
	glSvc := gl.NewService(gl.NewRepository(f.pool)).WithInvoiceReader(billing.NewRepository(f.pool)).WithCostBasis(fixedCost{})
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(f.pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	drainAll(t, dispatcher)

	byOrder := map[string]Return{}
	for _, r := range note.Returns {
		byOrder[r.OrderID] = r
	}
	// net sums debit minus credit on one account over the named journals.
	net := func(code string, keys ...string) int64 {
		var n int64
		if err := f.pool.QueryRow(f.ctx, `
SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id
 WHERE j.idempotency_key = ANY($1::text[]) AND a.code = $2`, keys, code).Scan(&n); err != nil {
			t.Fatalf("net %s: %v", code, err)
		}
		return n
	}
	journalCount := func(key string) int {
		var n int
		if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", key, err)
		}
		return n
	}
	var allKeys []string
	for _, r := range note.Returns {
		for _, topic := range []string{"pos.return.committed", "order.returned", "pos.return.written_off"} {
			allKeys = append(allKeys, topic+":"+r.ReturnID)
		}
	}
	// Every journal the note produced balances to the cent.
	var unbalanced int
	if err := f.pool.QueryRow(f.ctx, `
SELECT count(*) FROM (SELECT j.id FROM gl_journals j JOIN gl_journal_lines l ON l.journal_id = j.id
                       WHERE j.idempotency_key = ANY($1::text[])
                       GROUP BY j.id HAVING SUM(COALESCE(l.debit_cents,0)) <> SUM(COALESCE(l.credit_cents,0))) x`, allKeys).Scan(&unbalanced); err != nil || unbalanced != 0 {
		t.Fatalf("%d of the note's journals are unbalanced (%v)", unbalanced, err)
	}
	// The money reversals sum to the note: the receivable credited by the whole note.
	var committedKeys []string
	for _, r := range note.Returns {
		committedKeys = append(committedKeys, "pos.return.committed:"+r.ReturnID)
	}
	if got := -net("1100", committedKeys...); got != note.TotalCents {
		t.Fatalf("the money reversals credit %d to the receivable, the note is %d", got, note.TotalCents)
	}
	// Restock: one COGS reversal journal for the restocked child, none for the others.
	if journalCount("order.returned:"+byOrder[oR].ReturnID) != 1 ||
		journalCount("order.returned:"+byOrder[oD].ReturnID) != 0 || journalCount("order.returned:"+byOrder[oN].ReturnID) != 0 {
		t.Fatal("only the restocked child reverses COGS to inventory")
	}
	// Damaged: one write off journal for the damaged child, none for the others.
	if journalCount("pos.return.written_off:"+byOrder[oD].ReturnID) != 1 ||
		journalCount("pos.return.written_off:"+byOrder[oR].ReturnID) != 0 || journalCount("pos.return.written_off:"+byOrder[oN].ReturnID) != 0 {
		t.Fatal("only the damaged child writes goods off")
	}
	// The books say what the choices say: inventory takes the restocked cost, the damage expense the
	// damaged cost, COGS nets to minus both, the kept goods leave COGS and stock alone.
	if got := net("1200", allKeys...); got != 700 {
		t.Fatalf("inventory is debited the restocked goods' cost once, got %d", got)
	}
	if got := net("5200", allKeys...); got != 700 {
		t.Fatalf("the damage expense is debited the damaged goods' cost once, got %d", got)
	}
	if got := net("5000", allKeys...); got != -1400 {
		t.Fatalf("COGS is credited exactly the restocked and the written off cost, got %d", got)
	}
	// The books as a whole: all debits equal all credits across the note's journals.
	var dr, cr int64
	if err := f.pool.QueryRow(f.ctx, `
SELECT COALESCE(SUM(COALESCE(l.debit_cents,0)),0), COALESCE(SUM(COALESCE(l.credit_cents,0)),0)
  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.idempotency_key = ANY($1::text[])`, allKeys).Scan(&dr, &cr); err != nil || dr != cr || dr == 0 {
		t.Fatalf("the note's books do not balance: debits %d credits %d (%v)", dr, cr, err)
	}
}
