//go:build integration

// Integration tests for DIB-64 / DIB-79 against a real, migrated Postgres.
//
//	DATABASE_URL=... go test -tags integration ./internal/dispatch/ -count=1
//
// WHAT THESE COVER THAT THE UNIT TESTS CANNOT
//
//   - the BRANCH FENCE on every endpoint, driven through the real mux with a real guard-stamped
//     caller, because a fence tested at the service only proves the service is fenced, not the route;
//   - 403-by-id vs 404-by-number, which is a property of two different code paths agreeing;
//   - the 0090/0091 CHECK constraints, which are the backstop behind every service guard and which no
//     amount of Go testing exercises;
//   - the manifest actually landing in print_queue as DELIVERY_DOC, through the REAL
//     printing.QueueService, 0088's CHECK is the thing being proven, and a fake would prove nothing.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Every test registers a cleanup that removes its own stops, runs,
// photos, tokens, signatures, print jobs, fleet rows and orders, so the suite is order-independent and
// re-runnable. The ONE thing not rolled back is run_no_sequences.last_value: it is a monotonic counter
// exactly like order_no_seq, decrementing it would race a concurrent mint, and no assertion in this
// file depends on a run number's ORDINAL, only on its FORMAT.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	identity2 "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/printing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	branchTRT = "5d1b3f6e-0000-4000-9000-000000000001"
	branchKGN = "5d1b3f6e-0000-4000-9000-000000000002"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5432/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seededUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	return id
}

// staffCtx stamps a guard-shaped caller exactly as identity.RequireStaff does in production. A GLOBAL
// scope reaches everything; a BRANCH scope reaches only the branches named.
func staffCtx(t *testing.T, pool *pgxpool.Pool, scope identity.Scope, branches ...string) context.Context {
	t.Helper()
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: seededUser(t, pool), RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
		Scope: scope, Status: identity.StatusActive, Branches: granted,
	})
}

func globalCtx(t *testing.T, pool *pgxpool.Pool) context.Context {
	t.Helper()
	return staffCtx(t, pool, identity.ScopeGlobal)
}

// - seams -

// testOrders satisfies OrderReader off the real orders table, the same four fields
// order.Repository.OrderBrief returns, so the seam contract is exercised rather than mocked away.
type testOrders struct{ pool *pgxpool.Pool }

// ResolveOrderID completes the DIB-483 seam by delegating to THE resolver, the real
// order.Repository.ResolveOrderID, not a local re-implementation. That is the point of the test
// fixture as much as of the production adapter: if this fake parsed its own numbers, the integration
// suite would be proving that dispatch works with a resolver nobody ships.
func (o testOrders) ResolveOrderID(ctx context.Context, idOrNo string, global bool, branchIDs []string) (string, error) {
	return order.NewRepository(o.pool).ResolveOrderID(ctx, idOrNo, global, branchIDs)
}

func (o testOrders) Brief(ctx context.Context, orderID string) (OrderBrief, error) {
	if !looksLikeUUID(orderID) {
		return OrderBrief{}, ErrNotFound
	}
	var b OrderBrief
	err := o.pool.QueryRow(ctx,
		`SELECT id::text, branch_id::text, fulfillment, status FROM orders WHERE id = $1`, orderID).
		Scan(&b.ID, &b.BranchID, &b.Fulfillment, &b.Status)
	if err != nil {
		return OrderBrief{}, ErrNotFound
	}
	return b, nil
}

// recordingSigner stands in for the DIB-386 provider and ASSERTS THE CONTRACT: a capture must carry a
// non-empty name, an explicit affirmative, the POD purpose and a subject. The real provider (covered
// by pos's own suite) refuses when any of those is missing; this proves dispatch never asks it to.
type recordingSigner struct {
	t     *testing.T
	calls []TypedAttestation
	fail  error
}

func (s *recordingSigner) CaptureTyped(_ context.Context, in TypedAttestation) (string, error) {
	if s.fail != nil {
		return "", s.fail
	}
	s.t.Helper()
	if strings.TrimSpace(in.TypedName) == "" {
		s.t.Fatal("dispatch asked for a signature with a blank name, the provider would refuse")
	}
	if !in.Affirmed {
		s.t.Fatal("dispatch asked for a signature with no affirmative, the provider would refuse")
	}
	if in.Purpose != SignaturePurposePOD {
		s.t.Fatalf("unexpected signature purpose %q", in.Purpose)
	}
	if in.RefID == "" {
		s.t.Fatal("an attestation must name what it is signing")
	}
	s.calls = append(s.calls, in)
	return fmt.Sprintf("sig_test_%d", len(s.calls)), nil
}

// recordingFulfiller stands in for order.Service.AdvanceFromEvent.
type recordingFulfiller struct {
	orders []string
	fail   error
}

func (f *recordingFulfiller) MarkFulfilled(_ context.Context, orderID, _ string) error {
	if f.fail != nil {
		return f.fail
	}
	f.orders = append(f.orders, orderID)
	return nil
}

// realPrinter wires the ACTUAL W1a print queue. The point of the manifest ticket is that DELIVERY_DOC
// rides the shipped substrate; a fake queue would assert nothing about 0088's CHECK.
type realPrinter struct{ q *printing.QueueService }

func (p realPrinter) Enqueue(ctx context.Context, in PrintJobRequest) (string, error) {
	job, err := p.q.Enqueue(ctx, printing.EnqueueInput{
		DocType: in.DocType, RefID: in.RefID, BranchID: in.BranchID, RenderTarget: in.RenderTarget,
	})
	if err != nil {
		return "", err
	}
	return job.ID, nil
}

type harness struct {
	pool   *pgxpool.Pool
	svc    *Service
	signer *recordingSigner
	fulfil *recordingFulfiller
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := intPool(t)
	signer := &recordingSigner{t: t}
	fulfil := &recordingFulfiller{}
	svc := NewService(NewRepository(pool)).
		WithOrders(testOrders{pool: pool}).
		WithSignatures(signer).
		WithFulfiller(fulfil).
		WithPrinting(realPrinter{q: printing.NewQueueService(printing.NewRepository(pool))})
	return &harness{pool: pool, svc: svc, signer: signer, fulfil: fulfil}
}

// - fixtures -

func uniqueSuffix() string {
	return fmt.Sprintf("%d%04d", time.Now().UnixNano()%1_000_000, rand.Intn(10000))
}

// seedOrder inserts a test order and registers the cleanup that removes it and everything dispatch
// hangs off it. The order_no is literal (not next_order_no) so the test never touches the branch
// order-number counter another suite may assert on.
func seedOrder(t *testing.T, pool *pgxpool.Pool, branchID, fulfillment, status string) string {
	t.Helper()
	ctx := context.Background()
	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	orderNo := "#SO-DISPATCHTEST-" + uniqueSuffix()
	var id string
	err := pool.QueryRow(ctx, `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date)
VALUES ($1, $2, $3, $4, $5, CURRENT_DATE) RETURNING id::text`,
		orderNo, customerID, branchID, status, fulfillment).Scan(&id)
	if err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM delivery_pod_photos WHERE stop_id IN (SELECT id FROM delivery_stops WHERE order_id = $1)`, id)
		_, _ = pool.Exec(c, `DELETE FROM pos_signature WHERE ref_type = 'delivery_stop' AND ref_id IN (SELECT id FROM delivery_stops WHERE order_id = $1)`, id)
		_, _ = pool.Exec(c, `DELETE FROM delivery_stops WHERE order_id = $1`, id)
		_, _ = pool.Exec(c, `DELETE FROM will_call_pickups WHERE order_id = $1`, id)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, id)
	})
	return id
}

// seedRun opens a run through the service (so numbering and validation are the real ones) and
// registers its cleanup.
func seedRun(t *testing.T, h *harness, ctx context.Context, branchID, date string, withCrew bool) Run {
	t.Helper()
	in := CreateRunInput{BranchID: branchID, ScheduledDate: date}
	if withCrew {
		in.TruckID, in.DriverID = firstTruck(t, h.pool, branchID), firstDriver(t, h.pool, branchID)
	}
	run, err := h.svc.CreateRun(ctx, in)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.pool.Exec(c, `UPDATE delivery_stops SET run_id = NULL WHERE run_id = $1`, run.ID)
		_, _ = h.pool.Exec(c, `DELETE FROM print_queue WHERE ref_id = $1`, run.ID)
		_, _ = h.pool.Exec(c, `DELETE FROM delivery_runs WHERE id = $1`, run.ID)
	})
	return run
}

func firstTruck(t *testing.T, pool *pgxpool.Pool, branchID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM dispatch_trucks WHERE branch_id = $1 AND is_active ORDER BY code LIMIT 1`, branchID).Scan(&id); err != nil {
		t.Skipf("no seeded truck at %s: %v", branchID, err)
	}
	return id
}

func firstDriver(t *testing.T, pool *pgxpool.Pool, branchID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM dispatch_drivers WHERE branch_id = $1 AND is_active ORDER BY name LIMIT 1`, branchID).Scan(&id); err != nil {
		t.Skipf("no seeded driver at %s: %v", branchID, err)
	}
	return id
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

// - the happy path, end to end -

// TestLifecycle_ScheduleToDeliveredToHandoff walks the whole physical leg: schedule a confirmed
// order, put it on a crewed run, dispatch (which loads it), drive, capture the POD, and confirm the
// order was handed off toward invoicing.
func TestLifecycle_ScheduleToDeliveredToHandoff(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")

	lat, lng := 44.1100, -77.5800
	stop, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID: orderID, ScheduledDate: today(), AddressLine: "88 Concession Rd 4",
		City: "Trenton", PostalCode: "K8V 5R5", Latitude: &lat, Longitude: &lng,
		ContactName: "Jo", TimeWindow: "AM",
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if stop.Status != StopReady {
		t.Fatalf("a new stop starts READY, got %s", stop.Status)
	}
	if stop.BranchID != branchTRT {
		t.Fatalf("the stop's branch must be copied from the ORDER, got %s", stop.BranchID)
	}

	run := seedRun(t, h, ctx, branchTRT, today(), true)
	if !strings.HasPrefix(run.RunNo, "#RUN-TRT-") {
		t.Fatalf("run numbers are branch-prefixed; got %q", run.RunNo)
	}
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}

	detail, err := h.svc.DispatchRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if detail.Status != RunDispatched {
		t.Fatalf("run should be DISPATCHED, got %s", detail.Status)
	}
	// Dispatching MEANS the stops went on the truck.
	if detail.Stops[0].Status != StopLoaded {
		t.Fatalf("dispatch should load the run's READY stops, got %s", detail.Stops[0].Status)
	}
	// The depot coordinate is seeded (0093) and the stop is geocoded, so an estimate exists.
	if detail.EstimatedKm == nil || *detail.EstimatedKm <= 0 {
		t.Fatal("a geocoded stop at a branch with a depot should produce a distance estimate")
	}

	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}
	podded, err := h.svc.CapturePOD(ctx, stop.ID, PODInput{ReceivedBy: "Jo Ramirez", Affirmed: true})
	if err != nil {
		t.Fatalf("pod: %v", err)
	}
	if podded.Status != StopDelivered {
		t.Fatalf("a captured POD delivers the stop, got %s", podded.Status)
	}
	if podded.PODSignedRef == "" || podded.PODAt == nil {
		t.Fatal("a delivered stop must carry the signature ref and the time it was captured")
	}
	if podded.PODReceivedBy != "Jo Ramirez" {
		t.Fatalf("the display name should be the attested name, got %q", podded.PODReceivedBy)
	}
	if len(h.signer.calls) != 1 || h.signer.calls[0].RefType != RefTypeDeliveryStop {
		t.Fatalf("the POD must go through the attestation seam; calls=%+v", h.signer.calls)
	}
	// The invoice handoff: this was the order's last live stop.
	if len(h.fulfil.orders) != 1 || h.fulfil.orders[0] != orderID {
		t.Fatalf("the delivered order should have been handed off; got %v", h.fulfil.orders)
	}
	if podded.HandoffAt == nil {
		t.Fatal("handoff_at must be stamped so a delivery billing never heard about is queryable")
	}

	// And the run closes.
	closed, err := h.svc.CompleteRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if closed.Status != RunCompleted {
		t.Fatalf("run should be COMPLETED, got %s", closed.Status)
	}
}

// - lifecycle guards -

// TestGuard_IllegalStopTransitionsRejected drives the illegal moves through the SERVICE against real
// rows, not just the matrix: the guard has to survive the round trip.
func TestGuard_IllegalStopTransitionsRejected(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, err := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	// READY → EN_ROUTE: never went on a truck.
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err == nil {
		t.Fatal("READY → EN_ROUTE must be refused")
	}
	// READY → LOADED with no run: nothing to load onto.
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopLoaded, ""); err != ErrStopUnassigned {
		t.Fatalf("an unassigned stop cannot be loaded; got %v", err)
	}
	// DELIVERED is not reachable on the status path at all, only through CapturePOD.
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopDelivered, ""); err != ErrPODRequired {
		t.Fatalf("DELIVERED must demand a POD; got %v", err)
	}

	run := seedRun(t, h, ctx, branchTRT, today(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopLoaded, ""); err != nil {
		t.Fatalf("load: %v", err)
	}
	// LOADED → EN_ROUTE on a run that never left.
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != ErrRunNotDispatched {
		t.Fatalf("a stop cannot be en route from a run that has not left; got %v", err)
	}
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}
	// EN_ROUTE → LOADED: backward.
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopLoaded, ""); err != ErrIllegalTransition {
		t.Fatalf("backward moves must be refused; got %v", err)
	}
	// FAILED needs a reason.
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopFailed, "   "); err == nil {
		t.Fatal("a failed stop must carry a reason")
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopFailed, "nobody on site"); err != nil {
		t.Fatalf("fail with a reason: %v", err)
	}
	// Terminal.
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != ErrIllegalTransition {
		t.Fatalf("a FAILED stop is terminal; got %v", err)
	}
}

// TestGuard_DeliveredRequiresPOD covers BOTH halves of the rule: the service refuses, and the 0090
// CHECK refuses a hand-written UPDATE that tries to go around the service.
func TestGuard_DeliveredRequiresPOD(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, _ := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()})

	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopDelivered, ""); err != ErrPODRequired {
		t.Fatalf("service must refuse an unevidenced delivery; got %v", err)
	}
	// The database backstop: no other writer can produce one either.
	_, err := h.pool.Exec(context.Background(),
		`UPDATE delivery_stops SET status = 'DELIVERED' WHERE id = $1`, stop.ID)
	if err == nil {
		t.Fatal("the 0090 CHECK must refuse a DELIVERED row with no signature ref")
	}
	if !strings.Contains(err.Error(), "delivered_needs_pod") {
		t.Fatalf("expected the POD check to fire, got %v", err)
	}
}

// TestGuard_PODRefusalsMirrorTheProvider, a blank name is not a signature, and the affirmative is a
// separate act. Both refuse BEFORE anything is captured or written.
func TestGuard_PODRefusalsMirrorTheProvider(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, _ := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()})
	run := seedRun(t, h, ctx, branchTRT, today(), true)
	_, _ = h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID})
	_, _ = h.svc.DispatchRun(ctx, run.ID)
	_, _ = h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, "")

	if _, err := h.svc.CapturePOD(ctx, stop.ID, PODInput{ReceivedBy: "  ", Affirmed: true}); err == nil {
		t.Error("a blank name must be refused")
	}
	if _, err := h.svc.CapturePOD(ctx, stop.ID, PODInput{ReceivedBy: "Jo", Affirmed: false}); err == nil {
		t.Error("an unaffirmed capture must be refused")
	}
	if len(h.signer.calls) != 0 {
		t.Fatal("a refused capture must not reach the provider at all")
	}
	after, _ := h.svc.StopDetail(ctx, stop.ID)
	if after.Status != StopEnRoute {
		t.Fatalf("a refused POD must leave the stop untouched, got %s", after.Status)
	}
}

// TestGuard_DispatchNeedsCrewAndStops, "who took it" is the first question anyone asks.
func TestGuard_DispatchNeedsCrewAndStops(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	bare := seedRun(t, h, ctx, branchTRT, today(), false)
	if _, err := h.svc.DispatchRun(ctx, bare.ID); err != ErrCrewRequired {
		t.Fatalf("a crewless run must not dispatch; got %v", err)
	}
	crewed := seedRun(t, h, ctx, branchTRT, today(), true)
	if _, err := h.svc.DispatchRun(ctx, crewed.ID); err == nil {
		t.Fatal("an empty run has nothing to dispatch")
	}
}

// TestGuard_CompleteNeedsEveryStopFinished.
func TestGuard_CompleteNeedsEveryStopFinished(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, _ := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()})
	run := seedRun(t, h, ctx, branchTRT, today(), true)
	_, _ = h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID})
	_, _ = h.svc.DispatchRun(ctx, run.ID)

	if _, err := h.svc.CompleteRun(ctx, run.ID); err == nil {
		t.Fatal("a run with an unfinished stop must not complete")
	}
	_, _ = h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, "")
	_, _ = h.svc.AdvanceStop(ctx, stop.ID, StopFailed, "gate locked")
	if _, err := h.svc.CompleteRun(ctx, run.ID); err != nil {
		t.Fatalf("every stop is terminal now: %v", err)
	}
	// A completed run is terminal.
	if _, err := h.svc.CompleteRun(ctx, run.ID); err != ErrIllegalTransition {
		t.Fatalf("a COMPLETED run is terminal; got %v", err)
	}
	if _, err := h.svc.CancelRun(ctx, run.ID); err != ErrIllegalTransition {
		t.Fatalf("a COMPLETED run cannot be cancelled; got %v", err)
	}
}

// TestGuard_DispatchedRunCannotBeCancelled, a departed truck is a fact, not a plan.
func TestGuard_DispatchedRunCannotBeCancelled(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, _ := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()})
	run := seedRun(t, h, ctx, branchTRT, today(), true)
	_, _ = h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID})
	_, _ = h.svc.DispatchRun(ctx, run.ID)
	if _, err := h.svc.CancelRun(ctx, run.ID); err != ErrIllegalTransition {
		t.Fatalf("a DISPATCHED run must not be cancellable; got %v", err)
	}
}

// TestGuard_ScheduleRefusesUnschedulableOrders.
func TestGuard_ScheduleRefusesUnschedulableOrders(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	for _, tc := range []struct{ status, why string }{
		{"DRAFT", "a draft order has not been agreed to"},
		{"CANCELLED", "a cancelled order is not going anywhere"},
		{"CLOSED", "a closed order already went"},
	} {
		orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", tc.status)
		if _, err := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()}); err == nil {
			t.Errorf("%s: %s", tc.status, tc.why)
		}
	}
	// And a double-schedule is refused by the live-stop guard.
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	if _, err := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()}); err != nil {
		t.Fatalf("first schedule: %v", err)
	}
	if _, err := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()}); err == nil {
		t.Error("an order with a live stop must not be scheduled twice")
	}
}

// - the branch fence, over HTTP -

// testMux builds the real route table through the real Guards with the
// kit's DB-free authority double set to the Trenton caller's shape and a
// linker that supplies the same seeded ERP user id the pin's stamp carried
// (HH-3.2 B5: the positional stamp guard became the Guards constructor set
// plus the module's stamp adapter, so the production shape minus the
// verifier is now expressed exactly this way).
func testMux(t *testing.T, h *harness, ctx context.Context) *http.ServeMux {
	t.Helper()
	sc, ok := identity.StaffContextFromContext(ctx)
	if !ok {
		t.Fatal("testMux needs a staff-stamped context")
	}
	branches := make([]string, 0, len(sc.Branches))
	for b := range sc.Branches {
		branches = append(branches, b)
	}
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity2.StaffAuthority{
		RoleID:   "logistics_manager",
		Tier:     int(sc.Tier),
		Scope:    identity2.ScopeBranch,
		Branches: branches,
	})
	mux := http.NewServeMux()
	NewHandler(h.svc, nil).
		WithUserLinker(func(context.Context, string) (string, bool) { return sc.UserID, true }).
		RegisterRoutes(mux, sg.Guards)
	NewHandler(h.svc, nil).RegisterPublicRoutes(mux)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// TestFence_EveryEndpointRefusesACrossBranchCaller is the acceptance this bundle owes: a
// Trenton-scoped caller reaching Kingston rows gets 403 on every id-addressed endpoint, and the
// number lookup gets 404 (never 403, see service.RunByNo).
func TestFence_EveryEndpointRefusesACrossBranchCaller(t *testing.T) {
	h := newHarness(t)
	adminCtx := globalCtx(t, h.pool)

	// Build a complete Kingston delivery: order, stop, run, POD, photo, will-call token.
	kgnOrder := seedOrder(t, h.pool, branchKGN, "DELIVERY", "CONFIRMED")
	kgnStop, err := h.svc.Schedule(adminCtx, ScheduleInput{OrderID: kgnOrder, ScheduledDate: today()})
	if err != nil {
		t.Fatalf("seed kingston stop: %v", err)
	}
	kgnRun := seedRun(t, h, adminCtx, branchKGN, today(), true)
	if _, err := h.svc.AssignStop(adminCtx, kgnStop.ID, AssignInput{RunID: kgnRun.ID}); err != nil {
		t.Fatalf("seed kingston assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(adminCtx, kgnRun.ID); err != nil {
		t.Fatalf("seed kingston dispatch: %v", err)
	}
	if _, err := h.svc.AdvanceStop(adminCtx, kgnStop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("seed kingston en route: %v", err)
	}
	photo, err := h.svc.AddPhoto(adminCtx, kgnStop.ID, "image/png", "", []byte("\x89PNG-not-really"))
	if err != nil {
		t.Fatalf("seed kingston photo: %v", err)
	}

	// The caller under test: Trenton only.
	trtCtx := staffCtx(t, h.pool, identity.ScopeBranch, branchTRT)
	mux := testMux(t, h, trtCtx)

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"board", "GET", "/api/v1/dispatch/board?branch_id=" + branchKGN + "&date=" + today(), "", http.StatusForbidden},
		{"trucks", "GET", "/api/v1/dispatch/trucks?branch_id=" + branchKGN, "", http.StatusForbidden},
		{"create truck", "POST", "/api/v1/dispatch/trucks", `{"branch_id":"` + branchKGN + `","code":"X-1"}`, http.StatusForbidden},
		{"drivers", "GET", "/api/v1/dispatch/drivers?branch_id=" + branchKGN, "", http.StatusForbidden},
		{"create driver", "POST", "/api/v1/dispatch/drivers", `{"branch_id":"` + branchKGN + `","name":"X"}`, http.StatusForbidden},
		{"runs", "GET", "/api/v1/dispatch/runs?branch_id=" + branchKGN, "", http.StatusForbidden},
		{"create run", "POST", "/api/v1/dispatch/runs", `{"branch_id":"` + branchKGN + `","scheduled_date":"` + today() + `"}`, http.StatusForbidden},
		{"run detail", "GET", "/api/v1/dispatch/runs/" + kgnRun.ID, "", http.StatusForbidden},
		{"run crew", "POST", "/api/v1/dispatch/runs/" + kgnRun.ID + "/crew", `{}`, http.StatusForbidden},
		{"run dispatch", "POST", "/api/v1/dispatch/runs/" + kgnRun.ID + "/dispatch", "", http.StatusForbidden},
		{"run complete", "POST", "/api/v1/dispatch/runs/" + kgnRun.ID + "/complete", "", http.StatusForbidden},
		{"run cancel", "POST", "/api/v1/dispatch/runs/" + kgnRun.ID + "/cancel", "", http.StatusForbidden},
		{"manifest", "GET", "/api/v1/dispatch/runs/" + kgnRun.ID + "/manifest", "", http.StatusForbidden},
		{"manifest print", "POST", "/api/v1/dispatch/runs/" + kgnRun.ID + "/manifest/print", "", http.StatusForbidden},
		{"schedule", "POST", "/api/v1/dispatch/stops", `{"order_id":"` + kgnOrder + `","scheduled_date":"` + today() + `"}`, http.StatusForbidden},
		{"stop detail", "GET", "/api/v1/dispatch/stops/" + kgnStop.ID, "", http.StatusForbidden},
		{"stop assign", "POST", "/api/v1/dispatch/stops/" + kgnStop.ID + "/assign", `{}`, http.StatusForbidden},
		{"stop status", "POST", "/api/v1/dispatch/stops/" + kgnStop.ID + "/status", `{"status":"FAILED","reason":"x"}`, http.StatusForbidden},
		{"stop pod", "POST", "/api/v1/dispatch/stops/" + kgnStop.ID + "/pod", `{"received_by":"X","affirmed":true}`, http.StatusForbidden},
		{"photo read", "GET", "/api/v1/dispatch/photos/" + photo.ID, "", http.StatusForbidden},
		{"will-call mint", "POST", "/api/v1/dispatch/will-call", `{"order_id":"` + kgnOrder + `"}`, http.StatusForbidden},
		{"will-call list", "GET", "/api/v1/dispatch/will-call?order_id=" + kgnOrder, "", http.StatusForbidden},
		// The NUMBER lookup is the exception, and it is the whole reason it has its own path: 403 here
		// would confirm that #RUN-KGN-n exists at a branch this caller cannot see.
		{"run by number", "GET", "/api/v1/dispatch/run-by-no/" + strings.TrimPrefix(kgnRun.RunNo, "#"), "", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(t, mux, tc.method, tc.path, tc.body)
			if w.Code != tc.want {
				t.Fatalf("%s %s: want %d, got %d (%s)", tc.method, tc.path, tc.want, w.Code, w.Body.String())
			}
		})
	}

	// Nothing above may have MUTATED the Kingston delivery.
	after, err := h.svc.StopDetail(adminCtx, kgnStop.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if after.Status != StopEnRoute {
		t.Fatalf("a refused cross-branch caller changed the stop: %s", after.Status)
	}
}

// TestFence_ScopedCallerCannotEnumerateAllBranches, the empty-branch 403 (the landedcost posture).
func TestFence_ScopedCallerCannotEnumerateAllBranches(t *testing.T) {
	h := newHarness(t)
	scoped := staffCtx(t, h.pool, identity.ScopeBranch, branchTRT)
	mux := testMux(t, h, scoped)
	for _, path := range []string{
		"/api/v1/dispatch/board",
		"/api/v1/dispatch/runs",
		"/api/v1/dispatch/trucks",
		"/api/v1/dispatch/drivers",
	} {
		if w := do(t, mux, "GET", path, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s with no branch_id: want 403, got %d (%s)", path, w.Code, w.Body.String())
		}
	}
	// A GLOBAL caller may omit the filter, and then still needs a branch for these branch-keyed
	// reads, which is a 400 (say what to do), not a 403.
	globalMux := testMux(t, h, globalCtx(t, h.pool))
	if w := do(t, globalMux, "GET", "/api/v1/dispatch/board", ""); w.Code != http.StatusBadRequest {
		t.Errorf("global caller with no branch_id: want 400, got %d (%s)", w.Code, w.Body.String())
	}
}

// TestFence_UnauthenticatedIsRefused, no stamped caller anywhere on the staff surface.
func TestFence_UnauthenticatedIsRefused(t *testing.T) {
	h := newHarness(t)
	mux := testMux(t, h, context.Background())
	if w := do(t, mux, "GET", "/api/v1/dispatch/board?branch_id="+branchTRT, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (%s)", w.Code, w.Body.String())
	}
}

// TestFence_MissingIdIs404NotAnError, a garbage or absent id is NOT FOUND, never a 500.
func TestFence_MissingIdIs404NotAnError(t *testing.T) {
	h := newHarness(t)
	mux := testMux(t, h, globalCtx(t, h.pool))
	for _, path := range []string{
		"/api/v1/dispatch/stops/not-a-uuid",
		"/api/v1/dispatch/stops/11111111-1111-4111-8111-111111111111",
		"/api/v1/dispatch/runs/not-a-uuid",
		"/api/v1/dispatch/runs/11111111-1111-4111-8111-111111111111",
		"/api/v1/dispatch/photos/11111111-1111-4111-8111-111111111111",
		"/api/v1/dispatch/run-by-no/RUN-TRT-99999999",
	} {
		if w := do(t, mux, "GET", path, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: want 404, got %d (%s)", path, w.Code, w.Body.String())
		}
	}
}

// TestFence_CrossBranchAssignmentRefused, a run and a stop must share a branch, and so must a truck
// and its run. Otherwise the fence is decorative.
func TestFence_CrossBranchAssignmentRefused(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	trtOrder := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	trtStop, _ := h.svc.Schedule(ctx, ScheduleInput{OrderID: trtOrder, ScheduledDate: today()})
	kgnRun := seedRun(t, h, ctx, branchKGN, today(), true)

	if _, err := h.svc.AssignStop(ctx, trtStop.ID, AssignInput{RunID: kgnRun.ID}); err == nil {
		t.Error("a Trenton stop must not be assigned to a Kingston run")
	}
	// A Kingston truck on a Trenton run.
	if _, err := h.svc.CreateRun(ctx, CreateRunInput{
		BranchID: branchTRT, ScheduledDate: today(),
		TruckID: firstTruck(t, h.pool, branchKGN), DriverID: firstDriver(t, h.pool, branchTRT),
	}); err == nil {
		t.Error("a Kingston truck must not be crewed onto a Trenton run")
	}
}

// - POD photos -

func TestPhotos_TypeAndSizeAndTiming(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, _ := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: today()})

	// A photo before the truck left is not proof of delivery.
	if _, err := h.svc.AddPhoto(ctx, stop.ID, "image/png", "", []byte("x")); err == nil {
		t.Error("a READY stop has nothing to photograph yet")
	}
	run := seedRun(t, h, ctx, branchTRT, today(), true)
	_, _ = h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID})
	_, _ = h.svc.DispatchRun(ctx, run.ID)
	_, _ = h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, "")

	if _, err := h.svc.AddPhoto(ctx, stop.ID, "application/pdf", "", []byte("%PDF")); err == nil {
		t.Error("a pdf is not a proof photo")
	}
	if _, err := h.svc.AddPhoto(ctx, stop.ID, "image/png", "", nil); err == nil {
		t.Error("an empty photo must be refused")
	}
	if _, err := h.svc.AddPhoto(ctx, stop.ID, "image/png", "", make([]byte, MaxPhotoBytes+1)); err == nil {
		t.Error("an oversize photo must be refused before it is written")
	}
	// A real JPEG magic prefix: the byte_size the service declares must equal what 0091's
	// octet_length CHECK measures, and a multi-byte binary payload is where a text-vs-bytea slip
	// would show up.
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF-test")
	meta, err := h.svc.AddPhoto(ctx, stop.ID, "image/jpeg", "pallet at the side gate", jpeg)
	if err != nil {
		t.Fatalf("add photo: %v", err)
	}
	if meta.ByteSize != len(jpeg) {
		t.Fatalf("the declared size must be the real size; want %d got %d", len(jpeg), meta.ByteSize)
	}
	got, err := h.svc.Photo(ctx, meta.ID)
	if err != nil {
		t.Fatalf("read photo: %v", err)
	}
	if string(got.Bytes) != string(jpeg) || got.ByteSize != len(jpeg) {
		t.Fatalf("the stored bytes must round-trip exactly; got %d bytes", got.ByteSize)
	}
	detail, _ := h.svc.StopDetail(ctx, stop.ID)
	if len(detail.Photos) != 1 || detail.Photos[0].Caption != "pallet at the side gate" {
		t.Fatalf("the stop detail should list one captioned photo, got %+v", detail.Photos)
	}
	// The list read must not drag the bytes along.
	if detail.Photos[0].ByteSize != len(jpeg) {
		t.Fatalf("photo metadata should carry the size, got %d", detail.Photos[0].ByteSize)
	}
}

// - manifest → print_queue -

// TestManifest_RendersAndEnqueuesAsDeliveryDoc proves the run manifest rides the SHIPPED print queue
// under the doc type 0088 defined for it. If the enum ever narrows, this fails at the CHECK.
func TestManifest_RendersAndEnqueuesAsDeliveryDoc(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	lat, lng := 44.12, -77.60
	stop, _ := h.svc.Schedule(ctx, ScheduleInput{
		OrderID: orderID, ScheduledDate: today(), AddressLine: "88 Concession Rd 4",
		City: "Trenton", Latitude: &lat, Longitude: &lng,
	})
	run := seedRun(t, h, ctx, branchTRT, today(), true)
	_, _ = h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID})

	m, err := h.svc.Manifest(ctx, run.ID)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.BranchName == "" || len(m.Stops) != 1 || m.EstimatedKm == nil {
		t.Fatalf("manifest should name the branch, list the stop and estimate the distance: %+v", m)
	}
	html, err := RenderManifestHTML(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, run.RunNo) || !strings.Contains(html, "88 Concession Rd 4") {
		t.Fatal("the rendered manifest must carry the run number and the stop address")
	}

	jobID, err := h.svc.PrintManifest(ctx, run.ID)
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	var docType, refID, status string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT doc_type, ref_id::text, status FROM print_queue WHERE id = $1`, jobID).
		Scan(&docType, &refID, &status); err != nil {
		t.Fatalf("the print job should exist: %v", err)
	}
	if docType != "DELIVERY_DOC" || refID != run.ID || status != "QUEUED" {
		t.Fatalf("want a QUEUED DELIVERY_DOC for the run; got %s/%s/%s", docType, refID, status)
	}
}

// - will-call (DIB-79) -

func TestWillCall_MintScanConfirm(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "WILL_CALL", "READY")

	minted, err := h.svc.MintWillCall(ctx, orderID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if minted.Token == "" {
		t.Fatal("the raw token is returned exactly once, at mint")
	}
	// Only the hash is stored.
	var stored string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT token_hash FROM will_call_pickups WHERE id = $1`, minted.ID).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored == minted.Token || len(stored) != 64 {
		t.Fatal("the raw token must never be stored")
	}

	// The public scan carries the labels and nothing else.
	view, err := h.svc.PublicWillCall(context.Background(), minted.Token)
	if err != nil {
		t.Fatalf("public view: %v", err)
	}
	if view.OrderNo == "" || view.BranchName == "" || view.Status != "ACTIVE" {
		t.Fatalf("the public payload is incomplete: %+v", view)
	}

	// The two refusals.
	if _, err := h.svc.ConfirmWillCall(context.Background(), minted.Token, "   ", true); err == nil {
		t.Error("a blank name must be refused")
	}
	if _, err := h.svc.ConfirmWillCall(context.Background(), minted.Token, "Sam Okafor", false); err == nil {
		t.Error("an unaffirmed confirmation must be refused")
	}

	confirmed, err := h.svc.ConfirmWillCall(context.Background(), minted.Token, "Sam Okafor", true)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.Status != "CONFIRMED" || confirmed.CollectedBy != "Sam Okafor" {
		t.Fatalf("the confirmation should be recorded: %+v", confirmed)
	}
	if len(h.signer.calls) != 1 || h.signer.calls[0].RefType != RefTypeWillCall {
		t.Fatalf("a pickup rides the same attestation seam as a POD; got %+v", h.signer.calls)
	}
	if len(h.fulfil.orders) != 1 {
		t.Fatal("a collected order is fulfilled and billing should hear about it")
	}
	// A second scan of the same square is spent, 410, not a second confirmation.
	if _, err := h.svc.ConfirmWillCall(context.Background(), minted.Token, "Someone Else", true); err != ErrTokenGone {
		t.Fatalf("a spent token must be GONE; got %v", err)
	}
	// And every scan was logged.
	var events int
	_ = h.pool.QueryRow(context.Background(),
		`SELECT count(*)::int FROM will_call_pickup_events WHERE pickup_id = $1`, minted.ID).Scan(&events)
	if events < 2 {
		t.Fatalf("both the VIEW and the CONFIRM should be logged; got %d events", events)
	}
}

func TestWillCall_TokenTaxonomy(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)

	// No such token, ever → 404.
	if _, err := h.svc.PublicWillCall(context.Background(), "never-minted"); err != ErrTokenNotFound {
		t.Fatalf("an unknown token is NOT FOUND; got %v", err)
	}
	// Expired → 410, so the holder learns the CODE died.
	orderID := seedOrder(t, h.pool, branchTRT, "WILL_CALL", "READY")
	minted, err := h.svc.MintWillCall(ctx, orderID, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE will_call_pickups SET expires_at = now() - interval '1 day' WHERE id = $1`, minted.ID); err != nil {
		t.Fatalf("age the token: %v", err)
	}
	if _, err := h.svc.PublicWillCall(context.Background(), minted.Token); err != ErrTokenGone {
		t.Fatalf("an expired token is GONE; got %v", err)
	}

	// Revoked → 410 too.
	order2 := seedOrder(t, h.pool, branchTRT, "WILL_CALL", "READY")
	m2, err := h.svc.MintWillCall(ctx, order2, 0)
	if err != nil {
		t.Fatalf("mint 2: %v", err)
	}
	if err := h.svc.RevokeWillCall(ctx, m2.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := h.svc.PublicWillCall(context.Background(), m2.Token); err != ErrTokenGone {
		t.Fatalf("a revoked token is GONE; got %v", err)
	}
	// Revoking twice is a stated conflict, not a silent success.
	if err := h.svc.RevokeWillCall(ctx, m2.ID); err == nil {
		t.Error("a second revoke must say the token is already dead")
	}
}

// TestWillCall_OnlyForWillCallOrders, a delivery order gets a POD from the driver; minting a
// self-confirmation square for it would create a second, weaker path to "someone took this".
func TestWillCall_OnlyForWillCallOrders(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	delivery := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	if _, err := h.svc.MintWillCall(ctx, delivery, 0); err == nil {
		t.Error("a DELIVERY order must not get a will-call pickup code")
	}
	draft := seedOrder(t, h.pool, branchTRT, "WILL_CALL", "DRAFT")
	if _, err := h.svc.MintWillCall(ctx, draft, 0); err == nil {
		t.Error("a DRAFT order must not get a pickup code")
	}
	// One live square per order.
	ok := seedOrder(t, h.pool, branchTRT, "WILL_CALL", "READY")
	if _, err := h.svc.MintWillCall(ctx, ok, 0); err != nil {
		t.Fatalf("first mint: %v", err)
	}
	if _, err := h.svc.MintWillCall(ctx, ok, 0); err == nil {
		t.Error("a second live token would put two valid squares in the same yard")
	}
}

// TestWillCall_PublicPayloadLeaksNothing, the payload is written on the assumption the reader is not
// authenticated, because they are not.
func TestWillCall_PublicPayloadLeaksNothing(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	orderID := seedOrder(t, h.pool, branchTRT, "WILL_CALL", "READY")
	minted, err := h.svc.MintWillCall(ctx, orderID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	mux := testMux(t, h, context.Background()) // NO staff context, this is the public path
	w := do(t, mux, "GET", "/api/public/v1/will-call/"+minted.Token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("the public scan must work unauthenticated; got %d (%s)", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, forbidden := range []string{"cost", "margin", "total_cents", "subtotal_cents", "price", "token", "token_hash", "branch_id", "order_id"} {
		if _, present := payload[forbidden]; present {
			t.Errorf("the public payload must not carry %q", forbidden)
		}
	}
	body := w.Body.String()
	if strings.Contains(body, minted.Token) {
		t.Error("the public payload must not echo the token back")
	}
	// A dead token answers 410 over HTTP too.
	_ = h.svc.RevokeWillCall(ctx, minted.ID)
	if w := do(t, mux, "GET", "/api/public/v1/will-call/"+minted.Token, ""); w.Code != http.StatusGone {
		t.Fatalf("a dead token is 410 GONE; got %d", w.Code)
	}
	if w := do(t, mux, "GET", "/api/public/v1/will-call/never-minted", ""); w.Code != http.StatusNotFound {
		t.Fatalf("an unknown token is 404; got %d", w.Code)
	}
}

// - driver surface -

// TestMyRuns_ResolvesTheDriverFromTheLogin, never from a parameter, so one driver cannot read
// another's day.
func TestMyRuns_ResolvesTheDriverFromTheLogin(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	userID := seededUser(t, h.pool)

	// A staff user with no driver record is told so, rather than shown an empty day.
	if _, err := h.svc.MyRuns(ctx, today()); err != ErrNotFound {
		t.Fatalf("an unlinked user is NOT FOUND; got %v", err)
	}

	driverID, err := h.svc.CreateDriver(ctx, Driver{BranchID: branchTRT, Name: "Test Driver " + uniqueSuffix(), UserID: userID})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.pool.Exec(c, `UPDATE delivery_runs SET driver_id = NULL WHERE driver_id = $1`, driverID)
		_, _ = h.pool.Exec(c, `DELETE FROM dispatch_drivers WHERE id = $1`, driverID)
	})

	run, err := h.svc.CreateRun(ctx, CreateRunInput{
		BranchID: branchTRT, ScheduledDate: today(),
		TruckID: firstTruck(t, h.pool, branchTRT), DriverID: driverID,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM delivery_runs WHERE id = $1`, run.ID)
	})

	mine, err := h.svc.MyRuns(ctx, today())
	if err != nil {
		t.Fatalf("my runs: %v", err)
	}
	found := false
	for _, r := range mine {
		if r.ID == run.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the driver's own run should be in their day; got %d runs", len(mine))
	}
}

// - fleet -

func TestFleet_CreateAndList(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	code := "TEST-" + uniqueSuffix()
	id, err := h.svc.CreateTruck(ctx, Truck{BranchID: branchTRT, Code: code, Name: "Test truck", TruckType: "DUMP"})
	if err != nil {
		t.Fatalf("create truck: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM dispatch_trucks WHERE id = $1`, id)
	})
	// A duplicate code at the same branch is a stated conflict, not a 500 carrying a constraint name.
	if _, err := h.svc.CreateTruck(ctx, Truck{BranchID: branchTRT, Code: code}); err == nil {
		t.Error("a duplicate truck code at one branch must be refused")
	}
	if _, err := h.svc.CreateTruck(ctx, Truck{BranchID: branchTRT, Code: "X" + uniqueSuffix(), TruckType: "SPACESHIP"}); err == nil {
		t.Error("an unknown truck type must be refused before the CHECK sees it")
	}
	trucks, err := h.svc.Trucks(ctx, branchTRT, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, tr := range trucks {
		if tr.BranchID != branchTRT {
			t.Fatalf("the list leaked a %s truck", tr.BranchID)
		}
	}
}

// - 0185 · the commitment, over the real route table -
//
// THREE THINGS THE BRIEF ASKS THIS FILE TO PROVE, and they are proved against a real Postgres and the
// real mux because each one is a property of code paths AGREEING, which a service-level test cannot
// show: a capability the route registration might have been refactored around, a CHECK constraint
// behind a service guard, and a state machine whose two halves are written by different functions.

// roleCtx stamps a guard-shaped caller with a chosen ROLE. The commit capability is a role allowlist
// (identity.CanCommitDelivery), so the tier is not enough, the code is what the gate reads.
func roleCtx(t *testing.T, pool *pgxpool.Pool, role string, tier identity.Tier) context.Context {
	t.Helper()
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: seededUser(t, pool), RoleCode: role, Tier: tier,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
}

// TestCommit_ASalespersonsForgedCommitIsRefusedOverTheWire is item D, end to end. The salesperson's
// board draws no Commit control, and that is not what stops them. They can POST the route directly,
// and the server refuses on capability, at the door, before any load is touched.
func TestCommit_ASalespersonsForgedCommitIsRefusedOverTheWire(t *testing.T) {
	h := newHarness(t)
	dispatcher := roleCtx(t, h.pool, "LOGISTICS_MANAGER", identity.TierManager)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, err := h.svc.Schedule(dispatcher, ScheduleInput{OrderID: orderID, ScheduledDate: today()})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if stop.CommitState != CommitRequested {
		t.Fatalf("a stop with no state named is an ASK, got %s", stop.CommitState)
	}

	sales := testMux(t, h, roleCtx(t, h.pool, "INSIDE_SALES", identity.TierFrontline))

	// 1. The commit route itself.
	res := do(t, sales, http.MethodPost, "/api/v1/dispatch/stops/"+stop.ID+"/commit", `{"scheduled_date":""}`)
	if res.Code != http.StatusForbidden {
		t.Fatalf("a salesperson's commit must answer 403, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "request") {
		t.Fatalf("the refusal must say what they CAN do: %s", res.Body.String())
	}

	// 2. THE BACK DOOR. Putting a load on a truck IS the commit, so the assign route has to hold the
	// same capability, otherwise the gate is one HTTP call wide.
	run := seedRun(t, h, dispatcher, branchTRT, today(), true)
	res = do(t, sales, http.MethodPost, "/api/v1/dispatch/stops/"+stop.ID+"/assign",
		`{"run_id":"`+run.ID+`"}`)
	if res.Code != http.StatusForbidden {
		t.Fatalf("a salesperson assigning a load to a truck is a commit and must answer 403, got %d: %s",
			res.Code, res.Body.String())
	}

	// 3. THE OTHER BACK DOOR. Scheduling a stop that claims to be born COMMITTED.
	other := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	res = do(t, sales, http.MethodPost, "/api/v1/dispatch/stops",
		`{"order_id":"`+other+`","scheduled_date":"`+today()+`","commit_state":"COMMITTED"}`)
	if res.Code != http.StatusForbidden {
		t.Fatalf("a stop asking to be born committed must answer 403, got %d: %s", res.Code, res.Body.String())
	}
	// And 0184's field name is not a way around it either.
	res = do(t, sales, http.MethodPost, "/api/v1/dispatch/stops",
		`{"order_id":"`+other+`","scheduled_date":"`+today()+`","date_state":"COMMITTED"}`)
	if res.Code != http.StatusForbidden {
		t.Fatalf("the legacy field name must not bypass the capability, got %d: %s", res.Code, res.Body.String())
	}

	// The load never moved. A refused commit leaves no trace.
	detail, err := h.svc.StopDetail(dispatcher, stop.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	fresh := detail.Stop
	if fresh.CommitState != CommitRequested || fresh.RunID != "" {
		t.Fatalf("a refused commit must change nothing; got state=%s run=%q", fresh.CommitState, fresh.RunID)
	}
}

// TestReopen_TheRouteEnforcesTheManagerCapability is the other half of the brief's server-side proof.
// The mux's RequireManager is the coarse half; canReopenLoad is the real one, and a SALES_MANAGER -
// who passes RequireManager, must still be refused.
func TestReopen_TheRouteEnforcesTheManagerCapability(t *testing.T) {
	h := newHarness(t)
	dispatcher := roleCtx(t, h.pool, "LOGISTICS_MANAGER", identity.TierManager)
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, err := h.svc.Schedule(dispatcher, ScheduleInput{OrderID: orderID, ScheduledDate: today()})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	run := seedRun(t, h, dispatcher, branchTRT, today(), true)
	if _, err := h.svc.AssignStop(dispatcher, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	committedDetail, _ := h.svc.StopDetail(dispatcher, stop.ID)
	committed := committedDetail.Stop
	if committed.CommitState != CommitCommitted {
		t.Fatalf("putting a load on a truck IS the commit; got %s", committed.CommitState)
	}

	// A YARD HAND may build all day and may not reopen.
	yard := testMux(t, h, roleCtx(t, h.pool, "YARD_TEAM", identity.TierFrontline))
	if res := do(t, yard, http.MethodPost, "/api/v1/dispatch/stops/"+stop.ID+"/reopen",
		`{"reason":"a better fit came in"}`); res.Code != http.StatusForbidden {
		t.Fatalf("a yard hand's reopen must answer 403, got %d: %s", res.Code, res.Body.String())
	}
	// A SALES MANAGER passes the mux's manager guard and is still not dispatch.
	salesMgr := testMux(t, h, roleCtx(t, h.pool, "SALES_MANAGER", identity.TierManager))
	if res := do(t, salesMgr, http.MethodPost, "/api/v1/dispatch/stops/"+stop.ID+"/reopen",
		`{"reason":"a better fit came in"}`); res.Code != http.StatusForbidden {
		t.Fatalf("a sales manager passes RequireManager and must STILL be refused, got %d: %s",
			res.Code, res.Body.String())
	}
	// A reason is required, and the refusal says so in words rather than through a CHECK.
	mgr := testMux(t, h, dispatcher)
	if res := do(t, mgr, http.MethodPost, "/api/v1/dispatch/stops/"+stop.ID+"/reopen",
		`{"reason":"   "}`); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a reopen with no reason must answer 422, got %d: %s", res.Code, res.Body.String())
	}

	// AND THE DISPATCH MANAGER'S REOPEN IS ONE CALL THAT WORKS. Ryan Bostock's case, answered.
	res := do(t, mgr, http.MethodPost, "/api/v1/dispatch/stops/"+stop.ID+"/reopen",
		`{"reason":"an order came in that is a better fit for this load"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("a dispatch manager's reopen must succeed, got %d: %s", res.Code, res.Body.String())
	}
	afterDetail, _ := h.svc.StopDetail(dispatcher, stop.ID)
	after := afterDetail.Stop
	if after.CommitState != CommitRequested {
		t.Fatalf("a reopened load is back to REQUESTED, got %s", after.CommitState)
	}
	if after.ReopenedAt == nil || after.ReopenReason == "" {
		t.Fatalf("the reopen carries who/when/why or it explains nothing: %+v", after)
	}
	// The commit stamp SURVIVES. "Committed on the 2nd, reopened on the 3rd" is a real history and
	// clearing it would leave the reopen looking like the load was never promised.
	if after.CommittedAt == nil {
		t.Fatal("the commit stamp must survive the reopen, the history is the point")
	}
}

// TestCommit_BuildingIsFreeAndOnlyChangingACommittedLoadIsGated re-proves, against the real database,
// the thing the previous dispatch lane's integration run caught: a too-wide reading of the freeze
// stopped a dispatcher at 06:45 from building the next day's truck.
//
// 0185 removed the clock from the gate entirely, so the property is now structural rather than a
// carve-out, but it is exactly the property that would be lost first if the gate were ever widened
// again, so it gets its own test with its own name.
func TestCommit_BuildingIsFreeAndOnlyChangingACommittedLoadIsGated(t *testing.T) {
	h := newHarness(t)
	dispatcher := roleCtx(t, h.pool, "LOGISTICS_MANAGER", identity.TierManager)
	yardCtx := roleCtx(t, h.pool, "YARD_TEAM", identity.TierFrontline)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")

	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, err := h.svc.Schedule(dispatcher, ScheduleInput{OrderID: orderID, ScheduledDate: tomorrow})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	run := seedRun(t, h, dispatcher, branchTRT, tomorrow, true)

	// 06:45, TOMORROW'S TRUCK, A FRONTLINE YARD HAND. This is the exact case 0184 refused.
	if _, err := h.svc.AssignStop(yardCtx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("building tomorrow's truck must be free: %v", err)
	}
	// Recording what it weighs is free too, the yard telling the truth about the plan.
	kg := 4200.0
	if _, err := h.svc.SetLoadFacts(yardCtx, stop.ID, LoadFactsInput{WeightKg: &kg}); err != nil {
		t.Fatalf("recording a weight must never be gated: %v", err)
	}

	// It is now COMMITTED, because putting it on a truck IS the commit.
	builtDetail, _ := h.svc.StopDetail(dispatcher, stop.ID)
	built := builtDetail.Stop
	if built.CommitState != CommitCommitted {
		t.Fatalf("assignment commits; got %s", built.CommitState)
	}

	// AND NOW THE SAME YARD HAND IS GATED, on CHANGING it, and only on that.
	if _, err := h.svc.AssignStop(yardCtx, stop.ID, AssignInput{RunID: ""}); err == nil {
		t.Fatal("taking a committed load off its truck must be gated")
	}
	// The dispatch manager passes, which is the client's rule: who may pass, not who must press a
	// button first.
	if _, err := h.svc.AssignStop(dispatcher, stop.ID, AssignInput{RunID: ""}); err != nil {
		t.Fatalf("a dispatch manager may change a committed load directly: %v", err)
	}

	// A SECOND load, still an ask, is untouched by any of it, the gate is per-load, not per-truck.
	otherID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	other, err := h.svc.Schedule(dispatcher, ScheduleInput{OrderID: otherID, ScheduledDate: tomorrow})
	if err != nil {
		t.Fatalf("schedule second: %v", err)
	}
	if _, err := h.svc.AssignStop(yardCtx, other.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("a truck already carrying a commitment must still accept new loads: %v", err)
	}
}

// TestWorkingView_TheStagingLaneReachesAFortnight is R5's other 48: the daily workflow view reaches
// "almost a couple weeks", and it is a different number from the eight-week planning horizon.
func TestWorkingView_TheStagingLaneReachesAFortnight(t *testing.T) {
	h := newHarness(t)
	ctx := roleCtx(t, h.pool, "LOGISTICS_MANAGER", identity.TierManager)
	day := func(n int) string { return time.Now().UTC().AddDate(0, 0, n).Format("2006-01-02") }

	near := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	mid := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	far := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	for _, c := range []struct {
		order string
		when  int
	}{{near, 0}, {mid, 9}, {far, 30}} {
		if _, err := h.svc.Schedule(ctx, ScheduleInput{OrderID: c.order, ScheduledDate: day(c.when)}); err != nil {
			t.Fatalf("schedule +%d: %v", c.when, err)
		}
	}

	view, err := h.svc.LoadBoard(ctx, branchTRT, day(0))
	if err != nil {
		t.Fatalf("load board: %v", err)
	}
	if view.WorkingDays != 14 || view.WorkingTo != day(13) {
		t.Fatalf("the working view is a fortnight from the board's date; got %d days to %s",
			view.WorkingDays, view.WorkingTo)
	}
	staged := map[string]bool{}
	for _, s := range view.Staging {
		staged[s.OrderID] = true
	}
	if !staged[near] {
		t.Fatal("today's waiting work is in the working view")
	}
	if !staged[mid] {
		t.Fatal("work nine days out is in the working view, that is the whole point of the fortnight")
	}
	if staged[far] {
		t.Fatal("work a month out is NOT in the daily workflow view, the hundred irrelevant items")
	}
	// AND THE CARD AGREES WITH THE LANE, exactly. A count that can disagree with what is drawn under
	// it is worse than no count.
	if view.Counts.Unassigned != len(view.Staging) {
		t.Fatalf("the unassigned card counts the staging lane exactly; got %d vs %d",
			view.Counts.Unassigned, len(view.Staging))
	}
	// The PLANNING horizon is still eight weeks and is still a different number.
	if view.Policy.CommitHorizonWeeks*7 == view.Policy.WorkingViewDays {
		t.Fatal("the working view and the planning horizon must not be the same number")
	}
	// A load a month out is still SCHEDULABLE, the working view narrows what is SHOWN, never what is
	// allowed. Conflating those would have made the fix a new bug.
	if _, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID: seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED"), ScheduledDate: day(40),
	}); err != nil {
		t.Fatalf("the planning horizon still reaches eight weeks: %v", err)
	}
}
