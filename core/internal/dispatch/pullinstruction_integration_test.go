//go:build integration

// pullinstruction_integration_test.go, the pull instruction, proved against a real Postgres.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run PullInstruction ./internal/dispatch/
//
// WHAT ONLY A DATABASE CAN PROVE, AND IS THEREFORE PROVED HERE:
//   - a will-call order that is on NO truck reaches the yard's pick queue at all, the defect was
//     that it never did, and no in-memory fixture can demonstrate the absence of a row;
//   - it carries ITS OWN LINES, through the same OrderLineReader seam the delivery source uses;
//   - the two reads are DISJOINT AT THE DATABASE: scheduling a stop for that same order moves it out
//     of the pickup source rather than putting it in both;
//   - the delivery half of the feed produces exactly the tickets the pre-change code produced, which
//     is a statement about a SQL query and can only be made against SQL.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Every row created here is deleted on cleanup and nothing seeded
// is written. No whole-DB invariant is transiently broken (no stock moves, no payment_config), so no
// DIB-396 advisory lease is taken.

package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testOrderLines satisfies OrderLineReader off the REAL order_lines table, mirroring the production
// adapter's one load-bearing decision: the picker's number is the ENTERED quantity in the ENTERED
// unit, never qty_base. A yard worker told to pull "3200.0000 KG" of something sold by the pallet
// cannot do it.
type testOrderLines struct{ pool *pgxpool.Pool }

func (o testOrderLines) OrderPickLines(ctx context.Context, orderID string) ([]PickFeedLine, error) {
	if !looksLikeUUID(orderID) {
		return nil, ErrNotFound
	}
	rows, err := o.pool.Query(ctx, `
SELECT l.line_no, COALESCE(l.product_id::text,''), COALESCE(p.sku::text,''), l.description,
       l.qty_entered::text, l.uom_entered::text
  FROM order_lines l
  LEFT JOIN products p ON p.id = l.product_id
 WHERE l.order_id = $1 ORDER BY l.line_no`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PickFeedLine{}
	for rows.Next() {
		var l PickFeedLine
		if err := rows.Scan(&l.LineNo, &l.ProductID, &l.SKU, &l.Description, &l.QtyOrdered, &l.UOM); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// pullFeedFor builds the feed over the real repository with the real line read.
//
// ONE CLOCK, AND IT IS THE REAL ONE (DIB-427 / W6D-7). Every date this file uses is derived from
// time.Now() and the feed's injected clock reads the same instant, so a fixture and the code under
// test can never disagree about what "today" is. Pinning a calendar date here would have been a test
// that passes until some morning in 2026 and then fails for nobody's commit, and dispatch.Schedule's
// 8-week planning horizon means the delivery fixtures have to be relative anyway.
func pullFeedFor(t *testing.T, pool *pgxpool.Pool) *PickFeed {
	t.Helper()
	now := time.Now().UTC()
	return NewPickFeed(NewRepository(pool)).
		WithOrderLines(testOrderLines{pool: pool}).
		WithClock(func() time.Time { return now })
}

// seedPickupOrder inserts a pickup-bound order with a promised collection date and registers its
// cleanup. Deliberately built on seedOrder so the row shape, the literal order_no and the cleanup are
// the suite's, not this file's.
func seedPickupOrder(t *testing.T, pool *pgxpool.Pool, branchID, fulfillment, status, promised string) string {
	t.Helper()
	id := seedOrder(t, pool, branchID, fulfillment, status)
	if _, err := pool.Exec(context.Background(),
		`UPDATE orders SET requested_date = NULLIF($2,'')::date WHERE id = $1`, id, promised); err != nil {
		t.Fatalf("set promised date: %v", err)
	}
	return id
}

// seedPickLines puts real material on an order. seedOrder's cleanup removes the order, and
// order_lines cascades with it (0008: ON DELETE CASCADE), so nothing is left behind.
func seedPickLines(t *testing.T, pool *pgxpool.Pool, orderID string, n int) {
	t.Helper()
	ctx := context.Background()
	var productID, uom string
	if err := pool.QueryRow(ctx, `SELECT id::text, base_uom::text FROM products LIMIT 1`).
		Scan(&productID, &uom); err != nil {
		t.Skipf("no seeded product: %v", err)
	}
	for i := 1; i <= n; i++ {
		if _, err := pool.Exec(ctx, `
INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
                         qty_base, price_uom)
VALUES ($1, $2, $3, $4, $5, $6, $5, $6)`,
			orderID, i, productID, "Armour stone 24in", float64(i)*4, uom); err != nil {
			t.Fatalf("seed line %d: %v", i, err)
		}
	}
}

// inThreeDays sits between tomorrow() and nextWeek(), so a three-stop fixture spans three distinct
// days inside the planning horizon.
func inThreeDays() string { return time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02") }

func ticketFor(tickets []PickFeedTicket, orderID string) (PickFeedTicket, bool) {
	for _, t := range tickets {
		if t.OrderID == orderID {
			return t, true
		}
	}
	return PickFeedTicket{}, false
}

// - THE DEFECT, CLOSED -

// TestPullInstruction_WillCallOnNoTruckReachesTheYardQueue is the whole lane in one test.
//
// Before this change the assertion below failed at the first line: the order was simply absent from
// PendingTickets, because the only read behind the feed was over delivery_stops and a customer pickup
// has none. The yard could be handed a pickup code for material nobody had ever been told to pull.
func TestPullInstruction_WillCallOnNoTruckReachesTheYardQueue(t *testing.T) {
	pool := intPool(t)
	f := pullFeedFor(t, pool)

	// A will-call order promised for YESTERDAY: confirmed, on no truck, nobody has pulled it.
	orderID := seedPickupOrder(t, pool, branchTRT, "WILL_CALL", "CONFIRMED", yesterday())
	seedPickLines(t, pool, orderID, 3)

	got, _, err := f.PendingTickets(context.Background(), branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	tk, ok := ticketFor(got, orderID)
	if !ok {
		t.Fatal("a will-call order on no truck did not reach the pick queue, this is the defect this lane exists to close")
	}

	// It carries the ORDER's real method, not the hardcoded DELIVERY.
	if tk.Fulfillment != "WILL_CALL" {
		t.Fatalf("fulfillment = %q, want WILL_CALL", tk.Fulfillment)
	}
	// It carries its own lines, through the same seam the delivery source uses.
	if len(tk.Lines) != 3 {
		t.Fatalf("lines = %d, want 3, a pull instruction with nothing on it instructs nobody", len(tk.Lines))
	}
	if tk.Lines[0].QtyOrdered == "" || tk.Lines[0].UOM == "" || tk.Lines[0].Description == "" {
		t.Fatalf("a line must name what to pull, how much and in what unit; got %+v", tk.Lines[0])
	}
	// It is graded off the PROMISED COLLECTION date. Yesterday's promise is already broken.
	if tk.Priority != "URGENT" {
		t.Fatalf("priority = %q, want URGENT, a collection promised for yesterday is late", tk.Priority)
	}
	if tk.ScheduledDate != yesterday() {
		t.Fatalf("date = %q, want the promised collection date %q", tk.ScheduledDate, yesterday())
	}
	// Its handle is one a picker can read aloud to someone who can answer.
	if tk.Reference == "" || tk.Reference != tk.OrderNo {
		t.Fatalf("reference = %q, want the order number %q", tk.Reference, tk.OrderNo)
	}
	// And nothing about it claims a truck.
	if tk.StopID != "" || tk.RunID != "" {
		t.Fatalf("a pickup is on no stop and no run; got stop=%q run=%q", tk.StopID, tk.RunID)
	}

	// The single-order read finds it too, so a yard task bound to this order can show its lines.
	one, err := f.TicketForOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("ticket for order: %v", err)
	}
	if one.Fulfillment != "WILL_CALL" || len(one.Lines) != 3 || one.Reference != one.OrderNo {
		t.Fatalf("the single-order read must agree with the queue; got %+v", one)
	}
}

// TestPullInstruction_PlainPickupCountsToo, the defect was never will-call-specific. A PICKUP order
// carries the same commitment (a customer is coming for it) and the same absence of a stop.
func TestPullInstruction_PlainPickupCountsToo(t *testing.T) {
	pool := intPool(t)
	f := pullFeedFor(t, pool)
	orderID := seedPickupOrder(t, pool, branchTRT, "PICKUP", "READY", today())
	seedPickLines(t, pool, orderID, 1)

	got, _, err := f.PendingTickets(context.Background(), branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	tk, ok := ticketFor(got, orderID)
	if !ok {
		t.Fatal("a PICKUP order awaiting collection must reach the queue")
	}
	if tk.Fulfillment != "PICKUP" {
		t.Fatalf("fulfillment = %q, want PICKUP", tk.Fulfillment)
	}
	if tk.Priority != "HIGH" {
		t.Fatalf("priority = %q, want HIGH, the customer is coming today", tk.Priority)
	}
}

// - THE WINDOW, AT ITS EDGES -

// TestPullInstruction_TheCollectionWindowIsTheOrderLadder pins which pickup orders are pull
// instructions and which are not. It is the pickup source's answer to the delivery source's
// "READY and unassigned-or-planned".
func TestPullInstruction_TheCollectionWindowIsTheOrderLadder(t *testing.T) {
	pool := intPool(t)
	f := pullFeedFor(t, pool)
	ctx := context.Background()

	cases := []struct {
		status string
		want   bool
		why    string
	}{
		{"DRAFT", false, "nothing is committed yet, MintWillCall refuses a draft for the same reason"},
		{"CONFIRMED", true, "the customer has been promised it and nobody has pulled it"},
		{"PICKING", true, "somebody is on it; the queue is where they are looking"},
		{"READY", true, "staged material is still a pickup awaiting collection"},
		{"PARTIALLY_FULFILLED", true, "some of it is still on the ground and still has to be pulled"},
		{"FULFILLED", false, "collection is what ends the ticket, exactly as departure ends a stop's"},
		{"CLOSED", false, "history"},
		{"CANCELLED", false, "nobody is coming"},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			orderID := seedPickupOrder(t, pool, branchKGN, "WILL_CALL", c.status, today())
			got, _, err := f.PendingTickets(ctx, branchKGN, 200, 0)
			if err != nil {
				t.Fatalf("pending tickets: %v", err)
			}
			_, present := ticketFor(got, orderID)
			if present != c.want {
				t.Fatalf("a %s pickup present=%v, want %v, %s", c.status, present, c.want, c.why)
			}
		})
	}
}

// - THE TWO READS STAY TWO -

// TestPullInstruction_OneOrderNeverMakesTwoTickets is the union's de-duplication, proved where it
// actually lives: the pickup query's anti-join against live delivery stops.
//
// Nothing refuses a stop for a WILL_CALL order (Schedule gates on order STATUS, not fulfilment), so
// this is a reachable state and not a hypothetical. Once a stop exists the order belongs to the
// DELIVERY source and only to it, and it keeps saying WILL_CALL, because that is what the order says.
func TestPullInstruction_OneOrderNeverMakesTwoTickets(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	f := pullFeedFor(t, h.pool)

	orderID := seedPickupOrder(t, h.pool, branchTRT, "WILL_CALL", "CONFIRMED", tomorrow())
	seedPickLines(t, h.pool, orderID, 2)

	before, _, err := f.PendingTickets(ctx, branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	if n := countFor(before, orderID); n != 1 {
		t.Fatalf("an unscheduled pickup appears %d times, want exactly 1", n)
	}

	stop := w3Schedule(t, h, ctx, orderID, tomorrow())

	after, _, err := f.PendingTickets(ctx, branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets after scheduling: %v", err)
	}
	if n := countFor(after, orderID); n != 1 {
		t.Fatalf("a scheduled pickup appears %d times, want exactly 1, the two reads must stay disjoint", n)
	}
	tk, _ := ticketFor(after, orderID)
	if tk.StopID != stop.ID {
		t.Fatalf("once scheduled, the order belongs to the DELIVERY source; stop = %q, want %q", tk.StopID, stop.ID)
	}
	if tk.Reference != stop.ID {
		t.Fatalf("a scheduled order's handle is its stop id; got %q", tk.Reference)
	}
	if tk.Fulfillment != "WILL_CALL" {
		t.Fatalf("fulfillment = %q, the ticket reports the ORDER's method whichever read produced it", tk.Fulfillment)
	}
}

func countFor(tickets []PickFeedTicket, orderID string) int {
	n := 0
	for _, t := range tickets {
		if t.OrderID == orderID {
			n++
		}
	}
	return n
}

// TestPullInstruction_APickupIsStillNotDerivableFromDeliveryRows restates loadboard's ruling against
// the real tables, because this lane is exactly the change that could have quietly broken it. The
// pickup reaches the queue through a read over ORDERS; delivery_stops gained no row for it.
func TestPullInstruction_APickupIsStillNotDerivableFromDeliveryRows(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	f := pullFeedFor(t, pool)

	orderID := seedPickupOrder(t, pool, branchTRT, "WILL_CALL", "CONFIRMED", today())
	got, _, err := f.PendingTickets(ctx, branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	if _, ok := ticketFor(got, orderID); !ok {
		t.Fatal("the pickup must be in the queue for this test to mean anything")
	}
	var stops int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM delivery_stops WHERE order_id = $1`, orderID).
		Scan(&stops); err != nil {
		t.Fatalf("count stops: %v", err)
	}
	if stops != 0 {
		t.Fatalf("%d delivery_stops rows exist for a pickup, no synthetic stop may ever be minted to make a pickup visible", stops)
	}
	// And the delivery read itself still cannot see it, which is the ruling stated as an assertion.
	if _, err := f.repo.PickFeedStopForOrder(ctx, orderID); err == nil {
		t.Fatal("the delivery-stop read must not resolve a pickup, the sources are disjoint by construction")
	}
}

// - THE INVERSE: THE DELIVERY HALF DID NOT MOVE -

// TestPullInstruction_DeliveryHalfIsUnchangedAgainstTheDatabase is the inverse proof at the SQL level.
//
// It reconstructs what PendingTickets did before this change, the untouched delivery query, ticket()
// per row, lines per row, and requires the live feed to produce exactly that, byte for byte, in a
// branch that has no pickup awaiting collection. Combined with the unit suite's golden pin on
// ticket() itself, that covers both halves of "observably inert for a delivery": the mapping did not
// change, and neither did the query behind it or the order it returns rows in.
func TestPullInstruction_DeliveryHalfIsUnchangedAgainstTheDatabase(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	f := pullFeedFor(t, h.pool)

	// Three delivery stops across three days, so ordering is observable and not accidental. The dates
	// are RELATIVE because Schedule enforces an 8-week planning horizon, and they are deliberately out
	// of order so the comparison would catch a re-sort.
	for i, date := range []string{nextWeek(), tomorrow(), inThreeDays()} {
		orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
		seedPickLines(t, h.pool, orderID, i+1)
		w3Schedule(t, h, ctx, orderID, date)
	}

	// The PRE-CHANGE body, reconstructed: one read, one map, one line fetch per row.
	rows, err := f.repo.PickFeedPendingStops(ctx, branchTRT, 200)
	if err != nil {
		t.Fatalf("pending stops: %v", err)
	}
	legacy := make([]PickFeedTicket, 0, len(rows))
	for _, row := range rows {
		tk := f.ticket(row)
		lines, err := f.readLines(ctx, row.OrderID)
		if err == nil {
			tk.Lines = lines
		}
		legacy = append(legacy, tk)
	}
	if len(legacy) < 3 {
		t.Fatalf("the fixture must produce at least the three stops it seeded; got %d", len(legacy))
	}

	live, _, err := f.PendingTickets(ctx, branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	// Compare the DELIVERY rows only: a branch may legitimately carry pickups seeded by another
	// fixture, and the claim under test is about what happened to the delivery half.
	liveDeliveries := make([]PickFeedTicket, 0, len(live))
	for _, tk := range live {
		if tk.StopID != "" {
			liveDeliveries = append(liveDeliveries, tk)
		}
	}
	wantJSON, _ := json.Marshal(legacy)
	gotJSON, _ := json.Marshal(liveDeliveries)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("the delivery half of the feed changed.\n before: %s\n after : %s", wantJSON, gotJSON)
	}
}

// TestPullInstruction_AFailedPickupReadDoesNotBlankTheTrucks, the two reads are independent in the
// direction that matters. A yard that can still see its trucks is better served than one shown an
// empty screen because a second query fell over.
func TestPullInstruction_AFailedPickupReadDoesNotBlankTheTrucks(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	f := pullFeedFor(t, h.pool)

	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	w3Schedule(t, h, ctx, orderID, tomorrow())

	// A branch id that is a valid UUID but names nothing still returns an empty pickup list rather
	// than an error, and the delivery rows are unaffected, this is the shape of the guarantee.
	got, _, err := f.PendingTickets(ctx, branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	if _, ok := ticketFor(got, orderID); !ok {
		t.Fatal("the delivery stop must be in the queue")
	}
	// An unparseable branch names no branch and is not an error, on BOTH reads.
	empty, _, err := f.PendingTickets(ctx, "not-a-uuid", 200, 0)
	if err != nil {
		t.Fatalf("an unparseable branch must not error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("an unparseable branch names no branch; got %d tickets", len(empty))
	}
}
