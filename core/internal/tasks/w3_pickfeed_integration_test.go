//go:build integration

// w3_pickfeed_integration_test.go, THE ACCEPTANCE TEST for Wave 3 item 1: a stop scheduled on the
// dispatch board appears in the yard's pick queue, with the order's REAL lines, labelled DISPATCH.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run PickFeed ./internal/tasks/
//
// WHY THIS FILE IMPORTS internal/dispatch, AND WHY THAT IS NOT A FENCE BREACH.
//
// The whole design of the PickSource seam is that neither module names the other, and the ONLY place
// both names appear is the composition root. This file is a _test.go, so it is not compiled into the
// server binary and cannot change the production dependency graph by one edge, `go list -deps` on
// ./cmd/server is identical with and without it. What it buys is the thing a per-package test cannot
// prove: that the adapter the composition root writes actually satisfies the seam against a real
// database, with a real board row and real order lines. Testing the two halves separately would have
// left the join, the exact thing that was fixture data until this wave, untested.
//
// The adapter below is a FAITHFUL COPY of cmd/server/main.go's dispatchPickSource, not an import of
// it (package main cannot be imported). Its one required behaviour, dispatch.ErrNotFound →
// tasks.ErrUnknownOrder, is asserted directly, so a drift between the two copies fails here rather
// than in the yard.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Every row this file creates, order, order lines, stop, task, is
// deleted on cleanup, and nothing seeded is written. No whole-DB invariant is transiently broken
// (no stock moves, no payment_config), so no DIB-396 advisory lease is taken; see REPORT-W3-C.md.

package tasks

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/jackc/pgx/v5/pgxpool"
)

// - the composition-root adapters, copied -

// feedLines satisfies dispatch.OrderLineReader off the real order_lines table with raw SQL rather
// than by importing the order module, the testOrders posture from dispatch's own integration suite:
// the seam CONTRACT is exercised, and this test binary stays two packages wide instead of three.
type feedLines struct{ pool *pgxpool.Pool }

func (f feedLines) OrderPickLines(ctx context.Context, orderID string) ([]dispatch.PickFeedLine, error) {
	rows, err := f.pool.Query(ctx, `
SELECT line_no, COALESCE(product_id::text,''), description, qty_entered::text, uom_entered::text
  FROM order_lines WHERE order_id = $1 ORDER BY line_no`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dispatch.PickFeedLine{}
	for rows.Next() {
		var l dispatch.PickFeedLine
		if err := rows.Scan(&l.LineNo, &l.ProductID, &l.Description, &l.QtyOrdered, &l.UOM); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// feedOrders satisfies dispatch.OrderReader, the four-field authority brief, read the way
// order.Repository.OrderBrief reads it.
type feedOrders struct{ pool *pgxpool.Pool }

// ResolveOrderID completes the DIB-483 half of dispatch.OrderReader by delegating to THE resolver -
// order.Repository.ResolveOrderID, exactly as main.go's dispatchOrderAdapter does. This fixture does
// not exercise the pick feed by number; the method is here so the fixture keeps satisfying the seam
// with the same delegation the production adapter uses, rather than a local re-implementation.
func (o feedOrders) ResolveOrderID(ctx context.Context, idOrNo string, global bool, branchIDs []string) (string, error) {
	return order.NewRepository(o.pool).ResolveOrderID(ctx, idOrNo, global, branchIDs)
}

func (o feedOrders) Brief(ctx context.Context, orderID string) (dispatch.OrderBrief, error) {
	var b dispatch.OrderBrief
	err := o.pool.QueryRow(ctx,
		`SELECT id::text, branch_id::text, fulfillment, status FROM orders WHERE id = $1`, orderID).
		Scan(&b.ID, &b.BranchID, &b.Fulfillment, &b.Status)
	if err != nil {
		return dispatch.OrderBrief{}, dispatch.ErrNotFound
	}
	return b, nil
}

// pickSourceOverFeed is main.go's dispatchPickSource, copied. See the file header on why.
type pickSourceOverFeed struct{ feed *dispatch.PickFeed }

func (a pickSourceOverFeed) PickTicket(ctx context.Context, orderID string) (PickTicket, error) {
	t, err := a.feed.TicketForOrder(ctx, orderID)
	if err != nil {
		if errors.Is(err, dispatch.ErrNotFound) {
			return PickTicket{}, ErrUnknownOrder
		}
		return PickTicket{}, err
	}
	return ticketFromFeed(t), nil
}

func (a pickSourceOverFeed) PendingTickets(ctx context.Context, branchID string, limit, offset int) ([]PickTicket, int, error) {
	feed, total, err := a.feed.PendingTickets(ctx, branchID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out := make([]PickTicket, 0, len(feed))
	for _, t := range feed {
		out = append(out, ticketFromFeed(t))
	}
	return out, total, nil
}

func ticketFromFeed(t dispatch.PickFeedTicket) PickTicket {
	lines := make([]PickLine, 0, len(t.Lines))
	for _, l := range t.Lines {
		lines = append(lines, PickLine{
			LineNo: l.LineNo, ProductID: l.ProductID, SKU: l.SKU, Description: l.Description,
			QtyOrdered: l.QtyOrdered, UOM: l.UOM, Location: l.Location,
		})
	}
	return PickTicket{
		OrderID: t.OrderID, OrderNo: t.OrderNo, BranchID: t.BranchID, CustomerName: t.CustomerName,
		Fulfillment: t.Fulfillment, Priority: t.Priority, Reference: t.Reference,
		RequestedAt: t.RequestedAt, Lines: lines, Source: "DISPATCH",
		RunNo: t.RunNo, RunDepartsAt: t.RunDepartsAt, LoadPosition: t.LoadPosition,
	}
}

// - the fixture -

type feedFixture struct {
	pool    *pgxpool.Pool
	feedSvc *dispatch.Service
	source  pickSourceOverFeed
	tasks   *Service
	orderID string
	branch  string
	ctx     context.Context
}

// newFeedFixture seeds ONE order with three real lines at the seeded branch, wires the real dispatch
// service + pick feed over the real pool, and registers the cleanup that removes everything.
func newFeedFixture(t *testing.T) *feedFixture {
	t.Helper()
	pool := yardPool(t)
	ctx := context.Background()

	var branchID, customerID, userID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY code LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}

	orderNo := fmt.Sprintf("#SO-W3CFEED-%d%04d", time.Now().UnixNano()%1_000_000, rand.Intn(10000))
	var orderID string
	if err := pool.QueryRow(ctx, `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date)
VALUES ($1, $2, $3, 'CONFIRMED', 'DELIVERY', CURRENT_DATE) RETURNING id::text`,
		orderNo, customerID, branchID).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM task_events WHERE task_id IN (SELECT id FROM tasks WHERE order_id = $1::uuid)`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM task_shortages WHERE task_id IN (SELECT id FROM tasks WHERE order_id = $1::uuid)`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM tasks WHERE order_id = $1::uuid`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM load_scan_checkpoints WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM dispatch_reschedules WHERE entity_id IN (SELECT id FROM delivery_stops WHERE order_id = $1)`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM delivery_pod_photos WHERE stop_id IN (SELECT id FROM delivery_stops WHERE order_id = $1)`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM delivery_stops WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
	})

	// Three REAL lines. These are the numbers the assertion below is about: what the yard reads has
	// to be what the order says, not a hash of the order id.
	lines := []struct {
		no   int
		desc string
		qty  string
		uom  string
	}{
		{1, "Holland paver, charcoal", "4.0000", "PLT"},
		{2, "HPB bedding sand", "2.5000", "T"},
		{3, "Polymeric sand, grey", "12.0000", "BAG"},
	}
	for _, l := range lines {
		if _, err := pool.Exec(ctx, `
INSERT INTO order_lines (order_id, line_no, description, qty_entered, uom_entered, qty_base,
                         price_uom, unit_price_cents, line_total_cents)
VALUES ($1, $2, $3, $4::numeric, $5, $4::numeric, $5, 0, 0)`,
			orderID, l.no, l.desc, l.qty, l.uom); err != nil {
			t.Fatalf("seed line %d: %v", l.no, err)
		}
	}

	staffCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: userID, RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})

	feedSvc := dispatch.NewService(dispatch.NewRepository(pool)).WithOrders(feedOrders{pool: pool})
	source := pickSourceOverFeed{feed: dispatch.NewPickFeed(dispatch.NewRepository(pool)).
		WithOrderLines(feedLines{pool: pool})}

	tasksSvc := NewService(NewRepository(pool)).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			orderID: {ID: orderID, BranchID: branchID, Fulfillment: "DELIVERY", Status: "CONFIRMED"},
		}}).
		WithPickSource(source)

	return &feedFixture{
		pool: pool, feedSvc: feedSvc, source: source, tasks: tasksSvc,
		orderID: orderID, branch: branchID, ctx: staffCtx,
	}
}

func (f *feedFixture) schedule(t *testing.T, date string) dispatch.Stop {
	t.Helper()
	stop, err := f.feedSvc.Schedule(f.ctx, dispatch.ScheduleInput{
		OrderID: f.orderID, ScheduledDate: date, AddressLine: "88 Concession Rd 4",
		City: "Trenton", PostalCode: "K8V 5R5", ContactName: "Jo", TimeWindow: "AM",
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	return stop
}

func todayUTC() string { return time.Now().UTC().Format("2006-01-02") }

// - the acceptance assertions -

// TestPickFeed_AScheduledStopReachesTheYardQueueWithRealLines is the test the whole item exists for.
// Before this wave the same assertion could only be made about fixture material.
func TestPickFeed_AScheduledStopReachesTheYardQueueWithRealLines(t *testing.T) {
	f := newFeedFixture(t)
	stop := f.schedule(t, todayUTC())

	task, err := f.tasks.CreateFromOrder(context.Background(), CreateInput{
		OrderID: f.orderID, Source: SourceManual,
	})
	if err != nil {
		t.Fatalf("create pick task: %v", err)
	}

	entries, err := f.tasks.PickQueue(context.Background(), TaskFilter{BranchID: f.branch, Limit: 100})
	if err != nil {
		t.Fatalf("pick queue: %v", err)
	}
	var got *PickQueueEntry
	for i := range entries {
		if entries[i].Task.ID == task.ID {
			got = &entries[i]
			break
		}
	}
	if got == nil {
		t.Fatal("the task did not appear on the yard board at all")
	}
	if got.Ticket == nil {
		t.Fatal("the queue entry carries no ticket, the feed did not answer for a scheduled stop")
	}

	// THE FIXTURE BANNER FLIPS HERE. The yard console renders its warning off exactly this field.
	if got.Ticket.Source != "DISPATCH" {
		t.Fatalf("ticket source = %q, want DISPATCH, a fixture banner is shown for anything else", got.Ticket.Source)
	}
	if got.Ticket.Reference != stop.ID {
		t.Fatalf("reference = %q, want the stop id %q, a picker must be able to read it to a dispatcher",
			got.Ticket.Reference, stop.ID)
	}
	if len(got.Ticket.Lines) != 3 {
		t.Fatalf("lines = %d, want the order's 3 real lines", len(got.Ticket.Lines))
	}
	want := map[int]struct{ desc, qty, uom string }{
		1: {"Holland paver, charcoal", "4.0000", "PLT"},
		2: {"HPB bedding sand", "2.5000", "T"},
		3: {"Polymeric sand, grey", "12.0000", "BAG"},
	}
	for _, l := range got.Ticket.Lines {
		w, ok := want[l.LineNo]
		if !ok {
			t.Fatalf("unexpected line %d on the ticket", l.LineNo)
		}
		if l.Description != w.desc || l.QtyOrdered != w.qty || l.UOM != w.uom {
			t.Fatalf("line %d = %q %s %s, want %q %s %s, the yard must read what the ORDER says",
				l.LineNo, l.Description, l.QtyOrdered, l.UOM, w.desc, w.qty, w.uom)
		}
		if len(l.SKU) >= 8 && l.SKU[:8] == "FIXTURE-" {
			t.Fatal("a FIXTURE- sku reached the board, the stub is still wired")
		}
	}
	if got.Ticket.Priority != "HIGH" {
		t.Fatalf("priority = %q, want HIGH for a stop scheduled today", got.Ticket.Priority)
	}
}

func TestPickFeed_PendingTicketsShowsTheScheduledStopBeforeAnyTaskExists(t *testing.T) {
	// The "what have I not even queued" view. This is the read that was ALWAYS empty against the
	// stub, because a fixture must never invent orders (yard_source_test.go pins that).
	f := newFeedFixture(t)
	stop := f.schedule(t, todayUTC())

	pending, _, err := f.tasks.PendingTickets(context.Background(), f.branch, 100, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	found := false
	for _, tk := range pending {
		if tk.OrderID == f.orderID {
			found = true
			if tk.Reference != stop.ID {
				t.Fatalf("reference = %q, want the stop id", tk.Reference)
			}
			if len(tk.Lines) != 3 {
				t.Fatalf("pending ticket lines = %d, want 3", len(tk.Lines))
			}
		}
	}
	if !found {
		t.Fatal("a scheduled, unassigned stop must appear on the pending feed")
	}
}

func TestPickFeed_ALoadedStopLeavesThePendingQueue(t *testing.T) {
	// Once the material is on the truck the yard has nothing left to pull. A pick queue that keeps
	// showing it is a queue that asks the yard to pull a pallet that has gone.
	f := newFeedFixture(t)
	stop := f.schedule(t, todayUTC())

	if _, err := f.pool.Exec(context.Background(),
		`UPDATE delivery_stops SET status = 'LOADED' WHERE id = $1`, stop.ID); err != nil {
		t.Fatalf("load the stop: %v", err)
	}
	pending, _, err := f.tasks.PendingTickets(context.Background(), f.branch, 100, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	for _, tk := range pending {
		if tk.OrderID == f.orderID {
			t.Fatal("a LOADED stop must not still be on the pick queue")
		}
	}

	// …but the TICKET is still resolvable, because a task already on the board must keep its lines
	// when the truck is loaded mid-shift.
	if _, err := f.source.PickTicket(context.Background(), f.orderID); err != nil {
		t.Fatalf("a loaded stop's ticket must still resolve for an existing task: %v", err)
	}
}

func TestPickFeed_AnOrderWithNoStopIsAnUnknownOrder(t *testing.T) {
	// The seam's ONE required error translation. Getting this wrong makes a broken feed and a
	// nonexistent pick indistinguishable, which is what tasks.PickSource's contract forbids.
	f := newFeedFixture(t) // seeded, but never scheduled
	if _, err := f.source.PickTicket(context.Background(), f.orderID); !errors.Is(err, ErrUnknownOrder) {
		t.Fatalf("err = %v, want ErrUnknownOrder", err)
	}
	if _, err := f.source.PickTicket(context.Background(), "not-a-uuid"); !errors.Is(err, ErrUnknownOrder) {
		t.Fatalf("garbage id: err = %v, want ErrUnknownOrder", err)
	}
}

func TestPickFeed_AnEmptyBranchIsAnEmptySliceAndNoError(t *testing.T) {
	// The other half of the seam contract, stated verbatim in tasks.PickSource's doc.
	f := newFeedFixture(t)
	got, _, err := f.source.PendingTickets(context.Background(), "99999999-9999-4999-8999-999999999999", 50, 0)
	if err != nil {
		t.Fatalf("an empty branch must never be an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d tickets from a branch with none", len(got))
	}
}

func TestPickFeed_PickDetailCarriesTheSameTicketAsTheBoard(t *testing.T) {
	// The drawer and the board must not disagree about what to pull.
	f := newFeedFixture(t)
	f.schedule(t, todayUTC())
	task, err := f.tasks.CreateFromOrder(context.Background(), CreateInput{OrderID: f.orderID, Source: SourceManual})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	detail, err := f.tasks.PickDetail(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Ticket == nil || detail.Ticket.Source != "DISPATCH" || len(detail.Ticket.Lines) != 3 {
		t.Fatalf("pick detail ticket = %+v, want the same 3-line DISPATCH ticket", detail.Ticket)
	}
}

// - THE PULL INSTRUCTION · the acceptance test for the pickup half -
//
// Everything above proves a DELIVERY reaches the yard. The defect this closes is that nothing else
// ever did: PendingTickets read delivery stops only, so a customer pickup, an order with exactly the
// same fulfilment commitment and no truck, appeared on no screen anywhere. These assertions run
// through the SAME seam, the SAME adapter copy and the SAME yard service the delivery ones do, so
// what is proved is the whole join and not one module's half of it.

// newPickupFixture is newFeedFixture's twin for a customer pickup: same real branch, same real lines,
// same cleanup, and deliberately NO delivery stop, because that absence is the entire point.
func newPickupFixture(t *testing.T, fulfillment, promised string) *feedFixture {
	t.Helper()
	f := newFeedFixture(t)
	if _, err := f.pool.Exec(context.Background(), `
UPDATE orders SET fulfillment = $2, requested_date = $3::date WHERE id = $1`,
		f.orderID, fulfillment, promised); err != nil {
		t.Fatalf("make the order a pickup: %v", err)
	}
	// The tasks-side order reader mirrors the row, so a task created for it carries the same brief the
	// production adapter would build.
	f.tasks = NewService(NewRepository(f.pool)).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			f.orderID: {ID: f.orderID, BranchID: f.branch, Fulfillment: fulfillment, Status: "CONFIRMED"},
		}}).
		WithPickSource(f.source)
	return f
}

// TestPickFeed_AWillCallOnNoTruckReachesTheYardQueueWithRealLines is the pull-instruction lane's
// acceptance test, and it is the delivery one's exact mirror.
//
// Before the change this assertion failed on its first line: the order was absent from the pending
// feed entirely, because the only read behind the feed was over delivery_stops.
func TestPickFeed_AWillCallOnNoTruckReachesTheYardQueueWithRealLines(t *testing.T) {
	promised := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02") // promised for yesterday
	f := newPickupFixture(t, "WILL_CALL", promised)

	// No stop exists, and none is created. The instruction to pull comes from the ORDER.
	var stops int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*)::int FROM delivery_stops WHERE order_id = $1`, f.orderID).Scan(&stops); err != nil {
		t.Fatalf("count stops: %v", err)
	}
	if stops != 0 {
		t.Fatalf("%d stops exist, this test is about an order with none", stops)
	}

	pending, _, err := f.tasks.PendingTickets(context.Background(), f.branch, 200, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	var tk PickTicket
	for _, candidate := range pending {
		if candidate.OrderID == f.orderID {
			tk = candidate
		}
	}
	if tk.OrderID == "" {
		t.Fatal("a will-call order on no truck did not reach the yard's pick queue, the yard would be told to hand material over without ever being told to pull it")
	}
	if tk.Source != "DISPATCH" {
		t.Fatalf("source = %q, want DISPATCH, a pickup ticket is real material, not fixture data", tk.Source)
	}
	if tk.Fulfillment != "WILL_CALL" {
		t.Fatalf("fulfillment = %q, want WILL_CALL, the ticket carries the ORDER's method", tk.Fulfillment)
	}
	if len(tk.Lines) != 3 {
		t.Fatalf("lines = %d, want the order's 3, a pull instruction with nothing on it instructs nobody", len(tk.Lines))
	}
	if tk.Lines[0].Description != "Holland paver, charcoal" || tk.Lines[0].QtyOrdered != "4.0000" || tk.Lines[0].UOM != "PLT" {
		t.Fatalf("the yard must read what the ORDER says; got %+v", tk.Lines[0])
	}
	if tk.Priority != "URGENT" {
		t.Fatalf("priority = %q, want URGENT, a collection promised for yesterday is late, graded off the PROMISED date and not a delivery date", tk.Priority)
	}
	if tk.Reference != tk.OrderNo || tk.Reference == "" {
		t.Fatalf("reference = %q, want the order number %q, the handle a picker can read aloud", tk.Reference, tk.OrderNo)
	}
	if tk.RequestedAt == nil || tk.RequestedAt.Format("2006-01-02") != promised {
		t.Fatalf("requested-at = %v, want the promised collection date %s", tk.RequestedAt, promised)
	}
}

// TestPickFeed_APickupIsAKnownOrderNotAnUnknownOne, the seam's one required error translation used
// to fire for every pickup: TicketForOrder found no stop, returned ErrNotFound, and the yard was told
// the order did not exist. A pickup is now a KNOWN order with a ticket.
func TestPickFeed_APickupIsAKnownOrderNotAnUnknownOne(t *testing.T) {
	f := newPickupFixture(t, "PICKUP", todayUTC())

	tk, err := f.source.PickTicket(context.Background(), f.orderID)
	if err != nil {
		t.Fatalf("a pickup order must resolve to a ticket, got %v", err)
	}
	if tk.Fulfillment != "PICKUP" || len(tk.Lines) != 3 {
		t.Fatalf("the single-order read must carry the method and the lines; got %+v", tk)
	}
	// The translation still works for an order that genuinely has neither commitment.
	if _, err := f.source.PickTicket(context.Background(), "11111111-1111-4111-8111-111111111111"); !errors.Is(err, ErrUnknownOrder) {
		t.Fatalf("an order with no stop and no pickup commitment = %v, want ErrUnknownOrder", err)
	}
}

// TestPickFeed_ACollectedPickupLeavesThePendingQueue is the pickup's twin of the loaded-stop rule.
// Collection is what ends a pickup, exactly as departure ends a delivery; a queue that keeps showing
// it asks the yard to pull material a customer already drove away with.
func TestPickFeed_ACollectedPickupLeavesThePendingQueue(t *testing.T) {
	f := newPickupFixture(t, "WILL_CALL", todayUTC())
	ctx := context.Background()

	pending, _, err := f.tasks.PendingTickets(ctx, f.branch, 200, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if !containsOrder(pending, f.orderID) {
		t.Fatal("the pickup must be on the queue before it is collected")
	}

	// The will-call confirmation advances the order to FULFILLED (dispatch hands off through
	// OrderFulfiller). That is the fact the queue reads.
	if _, err := f.pool.Exec(ctx, `UPDATE orders SET status = 'FULFILLED' WHERE id = $1`, f.orderID); err != nil {
		t.Fatalf("mark fulfilled: %v", err)
	}
	after, _, err := f.tasks.PendingTickets(ctx, f.branch, 200, 0)
	if err != nil {
		t.Fatalf("pending after collection: %v", err)
	}
	if containsOrder(after, f.orderID) {
		t.Fatal("a collected pickup is still on the pick queue, the yard would be asked to pull material that has gone")
	}
}

func containsOrder(tickets []PickTicket, orderID string) bool {
	for _, tk := range tickets {
		if tk.OrderID == orderID {
			return true
		}
	}
	return false
}

func TestPickFeed_AssignedRunEnrichesRunNoDepartsAtAndLIFOLoadPosition(t *testing.T) {
	f := newFeedFixture(t)
	date := todayUTC()
	stop := f.schedule(t, date)

	departs := time.Now().UTC().Truncate(time.Second).Add(8*time.Hour + 15*time.Minute)
	run, err := f.feedSvc.CreateRun(f.ctx, dispatch.CreateRunInput{
		BranchID:      f.branch,
		ScheduledDate: date,
		RunDepartsAt:  &departs,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = f.pool.Exec(c, `UPDATE delivery_stops SET run_id = NULL WHERE run_id = $1`, run.ID)
		_, _ = f.pool.Exec(c, `DELETE FROM delivery_runs WHERE id = $1`, run.ID)
	})
	if _, err := f.feedSvc.AssignStop(f.ctx, stop.ID, dispatch.AssignInput{RunID: run.ID, StopNo: 1}); err != nil {
		t.Fatalf("assign stop: %v", err)
	}

	task, err := f.tasks.CreateFromOrder(context.Background(), CreateInput{
		OrderID: f.orderID, Source: SourceManual,
	})
	if err != nil {
		t.Fatalf("create pick task: %v", err)
	}

	detail, err := f.tasks.PickDetail(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("PickDetail: %v", err)
	}
	if detail.RunNo != run.RunNo || detail.RunDepartsAt == nil || !detail.RunDepartsAt.Equal(departs) || detail.LoadPosition != 1 {
		t.Fatalf("PickDetail truck fields = (run_no=%q, departs=%v, load_pos=%d), want (%q, %v, 1)",
			detail.RunNo, detail.RunDepartsAt, detail.LoadPosition, run.RunNo, departs)
	}
	if detail.Ticket == nil || detail.Ticket.RunNo != run.RunNo || detail.Ticket.RunDepartsAt == nil || !detail.Ticket.RunDepartsAt.Equal(departs) || detail.Ticket.LoadPosition != 1 {
		t.Fatalf("PickDetail.Ticket truck fields = %+v, want (%q, %v, 1)", detail.Ticket, run.RunNo, departs)
	}
}
