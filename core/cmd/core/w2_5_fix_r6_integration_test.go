//go:build integration

package main

// w2_5_fix_r6_integration_test.go, W2-5 fix round 6: ONE instant for a fulfilment.
//
// S5's probe D found the defect these legs pin down. A delivery order's fulfilment writes
// the stock ledger's SHIP move and emits order.fulfilled, but the two carried DIFFERENT
// instants: the move took the transaction's clock, and the COGS journal the consumer posts
// took the DRAIN's clock. A month end that fell between the two read the stock relieved and
// 1200 unrelieved, so the close was refused by the whole sale, and no later month could
// repair it: the stock side moved in the earlier month, the book side in the later one.
//
// The fix reads ONE instant per fulfilment and stamps it on BOTH sides. The legs below are
// the proof, and they are not sentences about the fix: they run the real fulfilment through
// the order module's own road, the real drain, and the real close.
//
//   - TestW25R6TheMonthThatTiedClosesAfterTheDrainThatCameAfterItsEnd: the month that tied
//     at its end closes, because the drain's journal carries the fulfilment's own instant.
//   - TestW25R6AMonthOutOfTieAtItsEndStaysRefusedDespiteTheFulfilmentAndItsDrain: the
//     opposite discrepancy still refuses, naming the month's own figure.
//   - TestW25R6APOSCollectNowSaleStillPostsItsCOGSAtTheRingInstant: the POS ring's sale is
//     unchanged: its COGS is posted at the ring instant and its own month still ties.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// w25r6PlantDelivery plants a DELIVERY order for the leg's own product and answers its id.
// The order sits on CONFIRMED: the yard's delivery road moves it from there.
func (leg *w25Leg) w25r6PlantDelivery(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	var customerID string
	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text FROM customers WHERE payment_terms_days > 0 ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatalf("a seeded customer on terms: %v", err)
	}
	var orderID string
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('W2-5-R6-' || $1 || '-' || substr(md5(random()::text), 1, 8), $2, $3, 'CONFIRMED', 'DELIVERY', $4, $4)
		RETURNING id::text`, leg.suffix, customerID, leg.branchID, w25Sold*w25UnitCost).Scan(&orderID); err != nil {
		t.Fatalf("the delivery order: %v", err)
	}
	mustExec(t, ctx, leg.pool, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
			qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1, 1, $2, 'W2-5 r6 leg line', $3, 'EA', $3, $4, 'EA', $5)`,
		orderID, leg.productID, w25Sold, w25UnitCost, w25Sold*w25UnitCost)
	return orderID
}

// w25r6FulfilDelivery is the REAL fulfilment through the ORDER MODULE'S OWN ROAD.
//
// order.Service.AdvanceFromDelivery is the door the dispatch module's yard uses
// (wire_operations.go's dispatchFulfilAdapter). It moves the order to FULFILLED through the
// guarded event writer, and the writer's fulfilment lane runs INSIDE the order's status
// transaction: it claims the order's unfulfilled lines and relieves the stock through
// inventory.FulfilOrderLinesTx (order/reservations.go:263). Nothing here reaches into the
// inventory service to write a move: the order's own road does it.
func (leg *w25Leg) w25r6FulfilDelivery(t *testing.T, orderID string) {
	t.Helper()
	if err := leg.money.orderSvc.AdvanceFromDelivery(context.Background(), orderID, "w2-5-r6 delivery"); err != nil {
		t.Fatalf("the delivery road: %v", err)
	}
	var status string
	if err := leg.pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1::uuid`, orderID).Scan(&status); err != nil {
		t.Fatalf("the order's status: %v", err)
	}
	if status != "FULFILLED" {
		t.Fatalf("the order sits at %s after the delivery road, want FULFILLED", status)
	}
}

// w25r6MoveInstant answers the instant the SHIP move carries, and the value it moved.
func (leg *w25Leg) w25r6MoveInstant(t *testing.T, sourceRefID string) (time.Time, int64) {
	t.Helper()
	var at time.Time
	var value int64
	if err := leg.pool.QueryRow(context.Background(), `
		SELECT occurred_at, value_cents FROM stock_moves
		 WHERE source_ref_id = $1::uuid AND move_type = 'SHIP'
		 ORDER BY occurred_at LIMIT 1`, sourceRefID).Scan(&at, &value); err != nil {
		t.Fatalf("the SHIP move: %v", err)
	}
	return at, value
}

// w25r6EventInstant answers the instant the order.fulfilled event carries, and whether it
// carries one at all. Before this item the event carried NO instant: the consumer fell back
// to its own clock, and the journal landed wherever the drain happened.
func (leg *w25Leg) w25r6EventInstant(t *testing.T, orderID string) (time.Time, bool) {
	t.Helper()
	var raw []byte
	if err := leg.pool.QueryRow(context.Background(), `
		SELECT payload FROM events_outbox
		 WHERE topic = 'order.fulfilled' AND payload->>'order_id' = $1
		 ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&raw); err != nil {
		t.Fatalf("the order.fulfilled event: %v", err)
	}
	var p struct {
		OccurredAt *time.Time `json:"occurred_at"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("the order.fulfilled payload: %v", err)
	}
	if p.OccurredAt == nil {
		return time.Time{}, false
	}
	return *p.OccurredAt, true
}

// w25r6JournalInstant answers the instant the COGS journal carries, and the value it relieved.
func (leg *w25Leg) w25r6JournalInstant(t *testing.T, orderID string) (time.Time, int64) {
	t.Helper()
	var at time.Time
	var credit int64
	if err := leg.pool.QueryRow(context.Background(), `
		SELECT j.posted_at, COALESCE(l.credit_cents, 0) FROM gl_journals j
		 JOIN gl_journal_lines l ON l.journal_id = j.id
		 JOIN gl_accounts a ON a.id = l.account_id AND a.code = '1200'
		 WHERE j.idempotency_key = $1`, "order.fulfilled:"+orderID).Scan(&at, &credit); err != nil {
		t.Fatalf("the COGS journal: %v", err)
	}
	return at, credit
}

// w25r6CarryTheFulfilmentInstantInto moves the fulfilment's OWN instants into the leg's
// month: the SHIP move row and the event's carried instant, when the event carries one.
//
// This is the harness's standing fixture adjustment (the same one w25Move makes): the wall
// clock cannot reach the leg's year, so the leg stamps the rows the movement wrote with the
// movement's own instant. The leg never INVENTS an instant: it moves the one the production
// code wrote. So when the production event carries no instant, there is nothing to move and
// the journal lands where the drain happened, which is exactly the defect the leg names.
func (leg *w25Leg) w25r6CarryTheFulfilmentInstantInto(t *testing.T, sourceRefID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	// The ledger rows, through the harness's own re-date helper.
	w25RedateTheMovement(t, leg, sourceRefID, at)
	// The event's carried instant, moved only when the production event carries one.
	mustExec(t, ctx, leg.pool, `
		UPDATE events_outbox
		   SET payload = jsonb_set(payload, '{occurred_at}', to_jsonb($2::timestamptz), true)
		 WHERE topic = 'order.fulfilled' AND payload->>'order_id' = $1 AND payload ? 'occurred_at'`, sourceRefID, at)
}

// w25r6Post is one authenticated staff call over the served mux.
func (leg *w25Leg) w25r6Post(t *testing.T, path string, body any) (int, string) {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(http.MethodPost, path, rdr)
	req.Header.Set("Authorization", "Bearer "+leg.closeToken)
	req.Header.Set(authz.HeaderXOrg, leg.orgSlug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	leg.mux.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

// TestW25R6TheMonthThatTiedClosesAfterTheDrainThatCameAfterItsEnd is the red.
//
// The month ties at its end. Then the REAL fulfilment of a delivery order runs through the
// order module's own road, and the real drain posts the COGS the event books. On the code
// before this item the SHIP move and the COGS journal carried different instants, so the
// month end found them on different sides of itself and refused the month by the whole
// sale.
//
// After this item the fulfilment carries ONE instant and the journal inherits it: the
// month that tied at its end closes, and the close is the plain, whole close.
func TestW25R6TheMonthThatTiedClosesAfterTheDrainThatCameAfterItsEnd(t *testing.T) {
	leg := w25Setup(t, "r6")
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	openAt, moveAt, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The month ties at its end, through the production reads.
	stock, err := leg.money.inventorySvc.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("the company's stock value: %v", err)
	}
	gl1200, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", monthEnd, "")
	if err != nil {
		t.Fatalf("1200 at the month's end: %v", err)
	}
	if stock != gl1200 {
		t.Fatalf("the month does not tie at its end: the stock ledger holds %d cents, 1200 holds %d", stock, gl1200)
	}

	// THE REAL FULFILMENT, through the order module's own road.
	orderID := leg.w25r6PlantDelivery(t)
	leg.w25r6FulfilDelivery(t, orderID)

	moveInstant, moved := leg.w25r6MoveInstant(t, orderID)
	if moved != w25Sold*w25UnitCost {
		t.Fatalf("the fulfilment moved %d cents, want %d", moved, w25Sold*w25UnitCost)
	}

	// The instant AFTER the fulfilment and BEFORE the worker drains order.fulfilled.
	drainAt := time.Now()
	if !moveInstant.Before(drainAt) {
		t.Fatalf("the fulfilment's instant %s is not before the drain's %s", moveInstant, drainAt)
	}

	// The wall clock cannot reach the leg's year, so the leg carries the fulfilment's own
	// instants into the leg's next month, on the move row and on the event alike.
	leg.w25r6CarryTheFulfilmentInstantInto(t, orderID, moveAt)

	// THE REAL DRAIN.
	w25Drain(t, leg)

	// The close of the month that tied. On the code before this item the SHIP move and the
	// COGS journal carried different instants, so the month's end found the stock relieved
	// and 1200 unrelieved, and the refusal named the whole sale.
	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused after the drain that came after the month's end: %d %s", code, body)
	}
	if got := w25PeriodStatus(t, leg, w25Year, w25Month); got != "CLOSED" {
		t.Fatalf("the period sits at %s after the close, want CLOSED", got)
	}

	// The production invariant, on the rows the drain wrote: the COGS journal carries the
	// fulfilment's own instant, the same one the SHIP move and the event carry. The leg moved
	// the movement's own instants into the leg's month, so all three read the moved instant:
	// the journal follows the movement, instead of landing wherever the drain happened.
	carriedMoveInstant, _ := leg.w25r6MoveInstant(t, orderID)
	carriedEventInstant, stillCarried := leg.w25r6EventInstant(t, orderID)
	if !stillCarried {
		t.Fatalf("the order.fulfilled event carries no instant: the COGS journal falls back to the drain's clock, so the stock ledger and 1200 are cut at two different moments")
	}
	if !carriedEventInstant.Equal(carriedMoveInstant) {
		t.Fatalf("the movement carries two instants: the SHIP move at %s, the event at %s", carriedMoveInstant, carriedEventInstant)
	}
	journalInstant, relieved := leg.w25r6JournalInstant(t, orderID)
	if relieved != w25Sold*w25UnitCost {
		t.Fatalf("the COGS journal relieved %d cents, want %d", relieved, w25Sold*w25UnitCost)
	}
	if !journalInstant.Equal(carriedEventInstant) {
		t.Fatalf("the COGS journal is dated at %s, the fulfilment's own instant is %s: the stock ledger and 1200 are cut at two different moments", journalInstant, carriedEventInstant)
	}
}

// TestW25R6AMonthOutOfTieAtItsEndStaysRefusedDespiteTheFulfilmentAndItsDrain is the
// opposite discrepancy.
//
// The month was OUT of tie at its end: a book-only write down inside the month moved 1200
// without moving a single unit of stock. The next month corrected it. The close of the
// earlier month is still refused, and it names the month's OWN discrepancy, not the
// fulfilment that followed: the fix must not let a later correction, and a later month's
// movement, pass a month that was out of tie at its end.
func TestW25R6AMonthOutOfTieAtItsEndStaysRefusedDespiteTheFulfilmentAndItsDrain(t *testing.T) {
	leg := w25Setup(t, "r6refuse")
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	openAt, moveAt, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The write down, inside the month: 1200 moves, no stock does.
	w25BookOnly(t, leg, openAt, "r6-write-down", "write down", true)
	// The correction, in the next month.
	w25BookOnly(t, leg, moveAt, "r6-correction", "correction", false)

	// THE REAL FULFILMENT and THE REAL DRAIN, in the next month.
	orderID := leg.w25r6PlantDelivery(t)
	leg.w25r6FulfilDelivery(t, orderID)
	leg.w25r6CarryTheFulfilmentInstantInto(t, orderID, moveAt)
	w25Drain(t, leg)

	// The close of the earlier month: still refused, and the refusal names the month's own
	// discrepancy.
	code, body := w25Close(t, leg, w25Year, w25Month)
	if code == http.StatusOK {
		t.Fatalf("the close of a month that was out of tie at its end was allowed: the write down inside the month is still unaccounted for")
	}
	if !strings.Contains(body, "stock is out of tie with 1200") {
		t.Fatalf("the refusal does not name the stock tie: %s", body)
	}
	if !strings.Contains(body, "$20.00") {
		t.Fatalf("the refusal does not name the month's own figure: %s", body)
	}
	if got := w25PeriodStatus(t, leg, w25Year, w25Month); got == "CLOSED" {
		t.Fatalf("the period is CLOSED after a refused close")
	}
}

// TestW25R6APOSCollectNowSaleStillPostsItsCOGSAtTheRingInstant is the guard for the POS
// ring.
//
// The POS ring's COLLECT_NOW sale is unchanged by this item: its COGS is posted at the ring
// instant (the sale's own date, carried on the event), and the sale's own month still ties.
// The ring's sale has always carried its own instant on the event, so it was never the
// writer this item had to align.
func TestW25R6APOSCollectNowSaleStillPostsItsCOGSAtTheRingInstant(t *testing.T) {
	leg := w25Setup(t, "r6pos")
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	openAt, _, monthEnd := w25Instants(t, leg.pool)

	// The ring's product carries a list price, so the ring can price it, and its own base
	// unit, so the ring can convert the line it rings.
	mustExec(t, ctx, leg.pool, `
		INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
		VALUES ($1, 2500, 'EA') ON CONFLICT (product_id) DO UPDATE SET list_price_cents = 2500`, leg.productID)
	mustExec(t, ctx, leg.pool, `
		INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
		VALUES ($1, 'EA', 1, true) ON CONFLICT (product_id, uom_code) DO NOTHING`, leg.productID)

	// The till session the ring sells against.
	code, body := leg.w25r6Post(t, "/api/v1/till/sessions", map[string]any{
		"branch_id": leg.branchID, "station": "MAIN", "float_cents": 20000,
	})
	if code >= 300 {
		t.Fatalf("opening the till session: %d %s", code, body)
	}
	var tillID string
	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text FROM till_sessions WHERE branch_id = $1 AND status = 'OPEN' ORDER BY opened_at DESC LIMIT 1`,
		leg.branchID).Scan(&tillID); err != nil {
		t.Fatalf("the till session: %v", err)
	}
	var customerID string
	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text FROM customers WHERE payment_terms_days > 0 ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatalf("a seeded customer on terms: %v", err)
	}

	// THE RING'S SALE, over the served route.
	code, body = leg.w25r6Post(t, "/api/v1/pos/sales", map[string]any{
		"idempotency_key": "f25f6000-0000-4000-8000-000000000001",
		"branch_id":       leg.branchID,
		"till_session_id": tillID,
		"customer_id":     customerID,
		"tender":          "CASH",
		"sale_type":       "COLLECT_NOW",
		"lines": []map[string]any{
			{"product_id": leg.productID, "qty_entered": w25Sold, "uom_entered": "EA"},
		},
	})
	if code >= 300 {
		t.Fatalf("the ring's sale: %d %s", code, body)
	}
	var sale struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal([]byte(body), &sale); err != nil {
		t.Fatalf("the ring's sale body: %v", err)
	}
	if sale.OrderID == "" {
		t.Fatalf("the ring's sale carries no order id: %s", body)
	}

	// The drawer is counted, so the month is not waiting on it.
	leg.w25r6CountTheDrawer(t, tillID)

	// THE REAL DRAIN.
	w25Drain(t, leg)

	// The ring's own instant: the event carries the sale's date, and the COGS journal the
	// consumer posts carries that same one.
	eventInstant, carried := leg.w25r6EventInstant(t, sale.OrderID)
	if !carried {
		t.Fatalf("the ring's order.fulfilled event carries no instant: the COGS journal falls back to the drain's clock")
	}
	// The ring's own instant: the event carries the sale's date, and the COGS journal the
	// consumer posts carries that same one. The ring reads its clock in nanoseconds and the
	// books keep microseconds, so the two are compared at the books' precision.
	journalInstant, relieved := leg.w25r6JournalInstant(t, sale.OrderID)
	if relieved != w25Sold*w25UnitCost {
		t.Fatalf("the ring's COGS journal relieved %d cents, want %d", relieved, w25Sold*w25UnitCost)
	}
	if !journalInstant.Equal(eventInstant.Truncate(time.Microsecond)) {
		t.Fatalf("the ring's COGS journal is dated at %s, the ring's own instant is %s", journalInstant, eventInstant)
	}

	// The wall clock cannot reach the leg's year, so the leg carries the sale's own instants
	// into the leg's month, on the move row and on the event alike.
	leg.w25r6CarryTheFulfilmentInstantInto(t, sale.OrderID, openAt)

	// The month the sale belongs to ties at its end, and closes.
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)
	code, body = w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the sale's own month did not tie: %d %s", code, body)
	}
}

// w25r6CountTheDrawer counts the till drawer the ring sold against, so the month the sale
// belongs to is not still waiting on it.
func (leg *w25Leg) w25r6CountTheDrawer(t *testing.T, tillID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/till/sessions/"+tillID+"/zreport", nil)
	req.Header.Set("Authorization", "Bearer "+leg.closeToken)
	req.Header.Set(authz.HeaderXOrg, leg.orgSlug)
	rsp := httptest.NewRecorder()
	leg.mux.ServeHTTP(rsp, req)
	if rsp.Code >= 300 {
		t.Fatalf("the till Z report: %d %s", rsp.Code, rsp.Body.String())
	}
	var z struct {
		Cash struct {
			ExpectedCents     int64 `json:"expected_cents"`
			OpeningFloatCents int64 `json:"opening_float_cents"`
		} `json:"cash"`
	}
	if err := json.Unmarshal(rsp.Body.Bytes(), &z); err != nil {
		t.Fatalf("the till Z report body: %v", err)
	}
	code, body := leg.w25r6Post(t, "/api/v1/till/sessions/"+tillID+"/count", map[string]any{
		"tenders":             []map[string]any{{"method": "CASH", "actual_cents": z.Cash.ExpectedCents}},
		"closing_float_cents": z.Cash.OpeningFloatCents,
	})
	if code >= 300 {
		t.Fatalf("counting the drawer: %d %s", code, body)
	}
	// The count alone leaves the drawer counted but not completed, and a counted drawer
	// still blocks the month. The completion is the till's own door.
	code, body = leg.w25r6Post(t, "/api/v1/till/sessions/"+tillID+"/complete", map[string]any{"override_notes": ""})
	if code >= 300 {
		t.Fatalf("completing the drawer: %d %s", code, body)
	}
}
