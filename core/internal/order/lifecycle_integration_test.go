//go:build integration

// DIB-191 integration tests: the order-lifecycle drivers against the live Postgres that
// PG@localhost:5435 serves. These prove, end to end, through the SAME production code paths
// cmd/server/main.go wires, the two things the unit fakes cannot:
//
//	T6  each ruled-in transition is driven by its PRODUCTION driver (the real notify.Dispatcher
//	    delivering a real events_outbox row to the LifecycleConsumer observer; the real HTTP
//	    Handler over the real pgx repository), NOT the 0059 demo fixture, and the orders.status
//	    column actually flips while an ORDER_STATUS_CHANGED row lands on the audit spine.
//	T7  the order-history timeline reads back the full who/when/reason chain, oldest→newest.
//
// The event drivers register EXACTLY as production does: as OBSERVERS on topics a primary owner
// (GL in production) owns. Here a no-op primary owner stands in for GL so the dispatcher settles
// the row and then fires our observer, the observer wiring and idempotency are the code under
// test, not GL's posting.
package order

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/jackc/pgx/v5/pgxpool"
)

// intActor / intManager are well-formed UUIDs for the acting staff (audit_log.actor_id is a bare
// uuid column with no FK, so any valid UUID attributes a timeline row without seeding a user).
const (
	intActor   = "3b191000-0000-4000-9000-000000000001"
	intManager = "3b191000-0000-4000-9000-000000000002"
	// CARRY NOTE (HH-3.2 fold): otherBranch is declared in the A4-carried
	// lifecycle_seed_integration_test.go (same literal).
)

// seedOrder lives in the A4-carried lifecycle_seed_integration_test.go (the
// fold dedup; the bodies were identical at the pin).

// emitOutbox inserts a PENDING outbox row on topic with payload and returns the generated event id
// (which the LifecycleConsumer rides into the timeline row's reason).
func emitOutbox(t *testing.T, pool *pgxpool.Pool, orderID, topic, payload string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO events_outbox (topic, payload, status) VALUES ($1, $2::jsonb, 'PENDING') RETURNING event_id::text`,
		topic, payload).Scan(&id); err != nil {
		t.Fatalf("emit %s: %v", topic, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM events_outbox WHERE event_id = $1`, id) })
	return id
}

// emitOutboxWithID re-emits a row carrying a SPECIFIC event id (a redelivery of an already-seen
// event), so the idempotency assertion drives the exact same source id a second time.
func emitOutboxWithID(t *testing.T, pool *pgxpool.Pool, orderID, topic, eventID, payload string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO events_outbox (event_id, topic, payload, status) VALUES ($1::uuid, $2, $3::jsonb, 'PENDING')
		 ON CONFLICT (event_id) DO UPDATE SET status = 'PENDING', published_at = NULL`,
		eventID, topic, payload); err != nil {
		t.Fatalf("re-emit %s: %v", topic, err)
	}
}

// drainOutbox drains the dispatcher until the outbox quiesces (bounded), mirroring main.go's ticker.
func drainOutbox(t *testing.T, d *notify.Dispatcher) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 16; i++ {
		if d.DrainOnce(ctx) == 0 {
			return
		}
	}
	t.Fatalf("outbox did not quiesce after 16 drain passes")
}

// liveStatus lives in the seed file (the fold dedup).

// invoke drives one Handler method with a StaffContext-stamped request (reusing the handler_test
// `req` builder) and returns the recorder, the real HTTP boundary over the real repository.
func invoke(t *testing.T, fn func(http.ResponseWriter, *http.Request, guards.StaffCaller), sc identity.StaffContext, orderID, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	fn(rr, req(t, &sc, orderID, body), guards.StaffCaller{})
	return rr
}

// TestIntegration_Lifecycle_EventDrivers_AdvanceAndTimeline covers T1/T2/T6/T7 for the event path:
// a real outbox order.fulfilled row, delivered by the real dispatcher to the LifecycleConsumer
// observer, flips CONFIRMED→FULFILLED with a system-attributed timeline row whose reason is the
// source event id; then a real ar.invoice.finalized row (invoice_id only) resolves through the
// billing seam and flips FULFILLED→INVOICED. The timeline reads back the ordered chain (T7).
func TestIntegration_Lifecycle_EventDrivers_AdvanceAndTimeline(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	cust, ord := seedOrder(t, pool, "CONFIRMED")

	// A FINALIZED invoice bound to the order, so the ar.invoice.finalized driver's billing-seam
	// resolution (invoice_id → order_id) has a real row to read.
	var inv string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date, total_cents, balance_cents)
		 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 1000, 1000) RETURNING id::text`,
		fmt.Sprintf("INV-D191-%d", time.Now().UnixNano()), cust, ord, trentonBranch).Scan(&inv); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, inv) })

	// -- real services, wired exactly like cmd/server/main.go ------------------------------
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	repo := NewRepository(pool)
	svc := NewService(repo).WithAudit(auditSvc).WithAuditTrail(auditSvc)
	billingRepo := billing.NewRepository(pool)

	// The dispatcher over the real outbox store. Register a no-op primary owner for each finance
	// topic (GL owns these in production) so the row settles and our OBSERVER then fires, the
	// exact ordering production relies on.
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	noop := func(context.Context, notify.Event) error { return nil }
	dispatcher.Register("order.fulfilled", noop)
	dispatcher.Register("ar.invoice.finalized", noop)
	NewLifecycleConsumer(svc, billingRepo, nil).RegisterHandlers(dispatcher)

	// -- T1: emit a real order.fulfilled outbox row and drain it through the dispatcher. --
	fulfilledEvt := emitOutbox(t, pool, ord, "order.fulfilled", fmt.Sprintf(`{"order_id":%q}`, ord))
	drainOutbox(t, dispatcher)

	if got := liveStatus(t, pool, ord); got != "FULFILLED" {
		t.Fatalf("status after order.fulfilled = %q, want FULFILLED (the observer did not advance the row)", got)
	}

	// -- T6 idempotency (P5): a redelivered order.fulfilled (at-least-once) arrives while the order is
	//     STILL FULFILLED, the real redelivery window, since it is a later ar.invoice.finalized (a
	//     distinct, later outbox row) that moves the order onward. The choke-point sees target==current
	//     and no-ops (ErrNoTransition, swallowed), writing NO second row. --
	emitOutboxWithID(t, pool, ord, "order.fulfilled", fulfilledEvt, fmt.Sprintf(`{"order_id":%q}`, ord))
	drainOutbox(t, dispatcher)
	if got := liveStatus(t, pool, ord); got != "FULFILLED" {
		t.Fatalf("status after a redelivered order.fulfilled = %q, want FULFILLED (unchanged)", got)
	}

	// -- T2: emit a real ar.invoice.finalized row (invoice_id only), resolved via the seam. --
	invoicedEvt := emitOutbox(t, pool, ord, "ar.invoice.finalized", fmt.Sprintf(`{"invoice_id":%q}`, inv))
	drainOutbox(t, dispatcher)

	if got := liveStatus(t, pool, ord); got != "INVOICED" {
		t.Fatalf("status after ar.invoice.finalized = %q, want INVOICED (the billing seam did not resolve the order)", got)
	}

	// -- T7: the timeline reads back the ordered system-attributed chain with the source event ids. --
	readCtx := identity.ContextWithStaff(ctx, globalStaff(intActor))
	h, err := svc.OrderHistory(readCtx, ord)
	if err != nil {
		t.Fatalf("OrderHistory: %v", err)
	}
	if len(h.Changes) != 2 {
		t.Fatalf("timeline has %d changes, want 2 (FULFILLED, INVOICED): %+v", len(h.Changes), h.Changes)
	}
	if h.Changes[0].FromStatus != "CONFIRMED" || h.Changes[0].ToStatus != "FULFILLED" {
		t.Errorf("change[0] = %q→%q, want CONFIRMED→FULFILLED", h.Changes[0].FromStatus, h.Changes[0].ToStatus)
	}
	if h.Changes[1].FromStatus != "FULFILLED" || h.Changes[1].ToStatus != "INVOICED" {
		t.Errorf("change[1] = %q→%q, want FULFILLED→INVOICED", h.Changes[1].FromStatus, h.Changes[1].ToStatus)
	}
	// System-attributed (actor "" → NULL actor_id → no resolved name) with the source event id as reason.
	if h.Changes[0].ActorID != "" {
		t.Errorf("change[0] actor = %q, want \"\" (a system/event transition)", h.Changes[0].ActorID)
	}
	if h.Changes[0].Reason != fulfilledEvt {
		t.Errorf("change[0] reason = %q, want the source event id %q", h.Changes[0].Reason, fulfilledEvt)
	}
	if h.Changes[1].Reason != invoicedEvt {
		t.Errorf("change[1] reason = %q, want the source event id %q", h.Changes[1].Reason, invoicedEvt)
	}
	if h.Changes[0].OccurredAt.After(h.Changes[1].OccurredAt) {
		t.Errorf("timeline not ordered oldest→newest: %v then %v", h.Changes[0].OccurredAt, h.Changes[1].OccurredAt)
	}
}

// TestIntegration_Lifecycle_ManualDrivers_HandlerEndToEnd covers T3/T5/T6/T7 for the manual path:
// the real HTTP Handler over the real pgx repository drives CONFIRMED→PICKING→READY in-branch (200s
// that flip the row and append attributed timeline rows), refuses a cross-branch caller (403) and a
// backward move (409), and the timeline reads back the ordered who/when/reason chain.
func TestIntegration_Lifecycle_ManualDrivers_HandlerEndToEnd(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	// Born DRAFT, the quote-convert / portal-reorder shape that ruling D2's confirm action serves.
	_, ord := seedOrder(t, pool, "DRAFT")

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc)
	h := NewHandler(svc)

	staff := staffAt(intActor, trentonBranch) // frontline, in-branch, the routine picker/ready actor

	// CONFIRM (ruling D2): the DRAFT on-ramp onto the fulfilment runway.
	if rr := invoke(t, h.confirm, staff, ord, ""); rr.Code != http.StatusOK {
		t.Fatalf("confirm: code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := liveStatus(t, pool, ord); got != "CONFIRMED" {
		t.Fatalf("status after confirm = %q, want CONFIRMED", got)
	}

	// PICKING (T3 happy path in-branch).
	if rr := invoke(t, h.markPicking, staff, ord, ""); rr.Code != http.StatusOK {
		t.Fatalf("markPicking: code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := liveStatus(t, pool, ord); got != "PICKING" {
		t.Fatalf("status after picking = %q, want PICKING", got)
	}

	// READY.
	if rr := invoke(t, h.markReady, staff, ord, ""); rr.Code != http.StatusOK {
		t.Fatalf("markReady: code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := liveStatus(t, pool, ord); got != "READY" {
		t.Fatalf("status after ready = %q, want READY", got)
	}

	// T5 forward-only: READY→PICKING is refused with a 409, and the row does not move.
	if rr := invoke(t, h.markPicking, staff, ord, ""); rr.Code != http.StatusConflict {
		t.Fatalf("backward READY→PICKING: code = %d, want 409", rr.Code)
	}
	if got := liveStatus(t, pool, ord); got != "READY" {
		t.Fatalf("status after a refused backward move = %q, want READY (unchanged)", got)
	}

	// T3 cross-branch: a caller scoped to another branch is forbidden (403), row unchanged.
	if rr := invoke(t, h.markReady, staffAt(intActor, otherBranch), ord, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("cross-branch caller: code = %d, want 403", rr.Code)
	}

	// T7: the timeline reads back the full DRAFT→CONFIRMED→PICKING→READY runway, oldest→newest,
	// each edge attributed to the acting caller.
	readCtx := identity.ContextWithStaff(ctx, globalStaff(intActor))
	hist, err := svc.OrderHistory(readCtx, ord)
	if err != nil {
		t.Fatalf("OrderHistory: %v", err)
	}
	wantFrom := []string{"DRAFT", "CONFIRMED", "PICKING"}
	wantTo := []string{"CONFIRMED", "PICKING", "READY"}
	if len(hist.Changes) != len(wantTo) {
		t.Fatalf("timeline has %d changes, want %d (CONFIRMED, PICKING, READY): %+v",
			len(hist.Changes), len(wantTo), hist.Changes)
	}
	for i, c := range hist.Changes {
		if c.FromStatus != wantFrom[i] || c.ToStatus != wantTo[i] {
			t.Errorf("change[%d] = %q→%q, want %q→%q", i, c.FromStatus, c.ToStatus, wantFrom[i], wantTo[i])
		}
		if c.ActorID != intActor {
			t.Errorf("change[%d] actor = %q, want the caller %q", i, c.ActorID, intActor)
		}
		if i > 0 && hist.Changes[i-1].OccurredAt.After(c.OccurredAt) {
			t.Errorf("timeline not ordered oldest→newest at %d", i)
		}
	}
}

// TestIntegration_POSCashSale_DeterministicTerminalState is the B2/R1 regression through the REAL
// dispatcher and the REAL pgx repository (audit H1).
//
// A POS ring emits order.fulfilled and ar.invoice.finalized inside ONE transaction, so both outbox rows
// get the same occurred_at (now() is the transaction timestamp) and the claim query, ORDER BY
// occurred_at ... FOR UPDATE SKIP LOCKED, has no tiebreaker. Drain order is therefore arbitrary in
// production. This drives BOTH orderings against real rows and asserts the order settles INVOICED
// either way, with no backward edge on the timeline.
func TestIntegration_POSCashSale_DeterministicTerminalState(t *testing.T) {
	for _, invoiceFirst := range []bool{false, true} {
		name := "fulfilled drains first"
		if invoiceFirst {
			name = "invoiced drains first"
		}
		t.Run(name, func(t *testing.T) {
			pool := intPool(t)
			ctx := context.Background()
			cust, ord := seedOrder(t, pool, "CONFIRMED")

			var inv string
			if err := pool.QueryRow(ctx,
				`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date, total_cents, balance_cents)
				 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 1000, 1000) RETURNING id::text`,
				fmt.Sprintf("INV-D191R1-%d", time.Now().UnixNano()), cust, ord, trentonBranch).Scan(&inv); err != nil {
				t.Fatalf("seed invoice: %v", err)
			}
			t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, inv) })

			auditSvc := audit.NewService(audit.NewRepository(pool), nil)
			svc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc)
			dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
			noop := func(context.Context, notify.Event) error { return nil }
			dispatcher.Register("order.fulfilled", noop)
			dispatcher.Register("ar.invoice.finalized", noop)
			NewLifecycleConsumer(svc, billing.NewRepository(pool), nil).RegisterHandlers(dispatcher)

			// Emit and drain ONE event at a time so the ordering under test is the one that actually
			// happens (with equal occurred_at the claim order would otherwise be arbitrary, which is
			// precisely the production hazard being guarded against).
			emitFulfilled := func() {
				emitOutbox(t, pool, ord, "order.fulfilled", fmt.Sprintf(`{"order_id":%q}`, ord))
				drainOutbox(t, dispatcher)
			}
			emitInvoiced := func() {
				emitOutbox(t, pool, ord, "ar.invoice.finalized", fmt.Sprintf(`{"invoice_id":%q}`, inv))
				drainOutbox(t, dispatcher)
			}
			if invoiceFirst {
				emitInvoiced()
				emitFulfilled()
			} else {
				emitFulfilled()
				emitInvoiced()
			}

			if got := liveStatus(t, pool, ord); got != "INVOICED" {
				t.Fatalf("terminal status = %q, want INVOICED regardless of drain order", got)
			}

			// No backward edge may appear on the timeline.
			readCtx := identity.ContextWithStaff(ctx, globalStaff(intActor))
			h, err := svc.OrderHistory(readCtx, ord)
			if err != nil {
				t.Fatalf("OrderHistory: %v", err)
			}
			for _, c := range h.Changes {
				if statusRank[c.FromStatus] >= statusRank[c.ToStatus] {
					t.Errorf("timeline contains a non-forward edge %s→%s", c.FromStatus, c.ToStatus)
				}
			}
		})
	}
}

// TestIntegration_EventDrivers_NoResurrection asserts a real event delivered against a CANCELLED order
// leaves it CANCELLED, through the real dispatcher and repository.
func TestIntegration_EventDrivers_NoResurrection(t *testing.T) {
	pool := intPool(t)
	_, ord := seedOrder(t, pool, "CANCELLED")

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	dispatcher.Register("order.fulfilled", func(context.Context, notify.Event) error { return nil })
	dispatcher.Register("ar.invoice.finalized", func(context.Context, notify.Event) error { return nil })
	NewLifecycleConsumer(svc, billing.NewRepository(pool), nil).RegisterHandlers(dispatcher)

	emitOutbox(t, pool, ord, "order.fulfilled", fmt.Sprintf(`{"order_id":%q}`, ord))
	drainOutbox(t, dispatcher)

	if got := liveStatus(t, pool, ord); got != "CANCELLED" {
		t.Fatalf("status = %q, want CANCELLED (a terminal order must never be resurrected by an event)", got)
	}
}

// TestIntegration_CancelPredicate_AgainstRealRows drives the B2/R2 cancel predicate through the REAL
// billing/pos Repository readers against REAL invoices/pos_sale rows (B3-3).
//
// This is the piece the unit tests structurally cannot cover: the fakes assert the SERVICE's branching,
// but the actual SQL, `status IN ('FINALIZED','PARTIALLY_PAID','PAID')` and `status = 'COMMITTED'`, had
// no direct coverage, and the unit "VOID does not block" case was a tautology (a fake told to report
// false reports false). Here the DISTINCTION IS MADE BY POSTGRES: the same code path is driven four
// times, differing only in a status string in a real row, so a predicate that over-matched (blocking on
// VOID/VOIDED) or under-matched (failing to block on FINALIZED/COMMITTED) is caught.
//
// Each case asserts BOTH the reader's boolean and the resulting HTTP outcome, so a reader that is right
// while the wiring is wrong (or vice versa) cannot hide.
func TestIntegration_CancelPredicate_AgainstRealRows(t *testing.T) {
	cases := []struct {
		name       string
		invoice    string // invoices.status to seed, "" for none
		sale       string // pos_sale.status to seed, "" for none
		wantBlock  bool   // what the reader must report
		wantStatus int    // the HTTP outcome of a manager cancel
		wantFinal  string // the order's status afterwards
	}{
		{"finalized invoice blocks", "FINALIZED", "", true, http.StatusConflict, "CONFIRMED"},
		{"paid invoice blocks", "PAID", "", true, http.StatusConflict, "CONFIRMED"},
		{"void invoice does not block", "VOID", "", false, http.StatusOK, "CANCELLED"},
		{"draft invoice does not block", "DRAFT", "", false, http.StatusOK, "CANCELLED"},
		{"committed sale blocks", "", "COMMITTED", true, http.StatusConflict, "CONFIRMED"},
		{"voided sale does not block", "", "VOIDED", false, http.StatusOK, "CANCELLED"},
		{"nothing attached does not block", "", "", false, http.StatusOK, "CANCELLED"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := intPool(t)
			ctx := context.Background()
			cust, ord := seedOrder(t, pool, "CONFIRMED")

			if tc.invoice != "" {
				var inv string
				if err := pool.QueryRow(ctx,
					`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date, total_cents, balance_cents)
					 VALUES ($1,$2,$3,$4,$5,CURRENT_DATE,1000,1000) RETURNING id::text`,
					fmt.Sprintf("INV-B33-%d", time.Now().UnixNano()), cust, ord, trentonBranch, tc.invoice).Scan(&inv); err != nil {
					t.Fatalf("seed %s invoice: %v", tc.invoice, err)
				}
				t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM invoices WHERE id=$1`, inv) })
			}
			if tc.sale != "" {
				// pos_sale.order_id is ON DELETE RESTRICT, so this cleanup must run BEFORE seedOrder's
				// order delete, t.Cleanup is LIFO and this is registered later, so it does.
				var sale string
				if err := pool.QueryRow(ctx,
					`INSERT INTO pos_sale (idempotency_key, order_id, branch_id, tender, status)
					 VALUES (gen_random_uuid(), $1, $2, 'CASH', $3) RETURNING id::text`,
					ord, trentonBranch, tc.sale).Scan(&sale); err != nil {
					t.Fatalf("seed %s pos_sale: %v", tc.sale, err)
				}
				t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM pos_sale WHERE id=$1`, sale) })
			}

			billingRepo := billing.NewRepository(pool)
			posRepo := pos.NewRepository(pool)

			// 1. The readers themselves, against the real rows.
			hasInv, err := billingRepo.OrderHasFinalizedInvoice(ctx, ord)
			if err != nil {
				t.Fatalf("OrderHasFinalizedInvoice: %v", err)
			}
			hasSale, err := posRepo.OrderHasCommittedPOSSale(ctx, ord)
			if err != nil {
				t.Fatalf("OrderHasCommittedPOSSale: %v", err)
			}
			if got := hasInv || hasSale; got != tc.wantBlock {
				t.Fatalf("predicate says blocked=%v (invoice=%v sale=%v), want %v",
					got, hasInv, hasSale, tc.wantBlock)
			}

			// 2. The same predicate through the real service + handler.
			auditSvc := audit.NewService(audit.NewRepository(pool), nil)
			svc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc).
				WithCancelGuards(billingRepo, posRepo)
			h := NewHandler(svc)

			rr := invoke(t, h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"B3-3 predicate check"}`)
			if rr.Code != tc.wantStatus {
				t.Fatalf("cancel code = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if got := liveStatus(t, pool, ord); got != tc.wantFinal {
				t.Fatalf("order status = %q, want %q", got, tc.wantFinal)
			}

			// 3. A blocked cancel must point the operator at the returns flow, not fail opaquely.
			if tc.wantBlock && !strings.Contains(rr.Body.String(), "returns flow") {
				t.Errorf("409 body should name the POS returns flow, got %s", rr.Body.String())
			}
		})
	}
}

// TestIntegration_Lifecycle_ManagerCancel covers T4/T7 for the cancel path: a non-manager in-branch
// caller is forbidden (403), a manager with a reason cancels (200 → CANCELLED) and the reason is
// persisted on the timeline; a manager with no reason is a 400.
func TestIntegration_Lifecycle_ManagerCancel(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	_, ord := seedOrder(t, pool, "CONFIRMED")

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	// B2/R2: the cancel predicate is wired with the REAL seams (billing + pos repositories), exactly as
	// main.go does. A clean seeded order has no finalized invoice and no committed pos_sale, so the
	// predicate proves "no money attached" against real rows and the cancel is allowed.
	svc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc).
		WithCancelGuards(billing.NewRepository(pool), pos.NewRepository(pool))
	h := NewHandler(svc)

	// T4: a frontline (non-manager) in-branch caller cannot cancel, 403, row unchanged.
	if rr := invoke(t, h.cancel, staffAt(intActor, trentonBranch), ord, `{"reason":"changed mind"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("non-manager cancel: code = %d, want 403", rr.Code)
	}
	if got := liveStatus(t, pool, ord); got != "CONFIRMED" {
		t.Fatalf("status after a refused cancel = %q, want CONFIRMED (unchanged)", got)
	}

	// T4: a manager must supply a reason, a blank body is a 400.
	if rr := invoke(t, h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":""}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("manager cancel without a reason: code = %d, want 400", rr.Code)
	}

	// T4: a manager with a reason cancels, 200 → CANCELLED, reason persisted.
	const reason = "customer cancelled the order"
	if rr := invoke(t, h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"`+reason+`"}`); rr.Code != http.StatusOK {
		t.Fatalf("manager cancel: code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := liveStatus(t, pool, ord); got != "CANCELLED" {
		t.Fatalf("status after cancel = %q, want CANCELLED", got)
	}

	readCtx := identity.ContextWithStaff(ctx, globalStaff(intActor))
	hist, err := svc.OrderHistory(readCtx, ord)
	if err != nil {
		t.Fatalf("OrderHistory: %v", err)
	}
	if len(hist.Changes) != 1 || hist.Changes[0].ToStatus != "CANCELLED" {
		t.Fatalf("timeline = %+v, want a single CONFIRMED→CANCELLED change", hist.Changes)
	}
	if hist.Changes[0].Reason != reason {
		t.Errorf("cancel reason = %q, want %q (persisted on the timeline)", hist.Changes[0].Reason, reason)
	}
	if hist.Changes[0].ActorID != intManager {
		t.Errorf("cancel actor = %q, want the manager %q", hist.Changes[0].ActorID, intManager)
	}
}

// TestIntegration_ResolveOrderID_ByOrderNo proves DIB-379/F3: the terminal's "Open order by id" (and
// every transition it drives) must accept the order's VISIBLE order_no as an alternate key to the
// UUID, both with its stored leading '#' (as the terminal renders it, "#SO-…") and without it (a
// cashier reading it off a receipt/screen, or dictating it, is likelier to drop the '#'). Drives the
// SAME real Handler + pgx Repository the other lifecycle tests in this file use, a read, a write
// (confirm), and the not-found floor for a garbage order_no, all through the real HTTP boundary.
func TestIntegration_ResolveOrderID_ByOrderNo(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	_, ord := seedOrder(t, pool, "DRAFT")

	var orderNo string
	if err := pool.QueryRow(ctx, `SELECT order_no::text FROM orders WHERE id=$1`, ord).Scan(&orderNo); err != nil {
		t.Fatalf("read order_no: %v", err)
	}
	if !strings.HasPrefix(orderNo, "#") {
		t.Fatalf("expected the stored order_no to carry a leading '#' (pos/reorder both write it that way), got %q", orderNo)
	}

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc)
	h := NewHandler(svc)
	staff := staffAt(intActor, trentonBranch)

	// GET by the order_no WITH its stored '#' resolves to the same order the UUID would.
	rr := invoke(t, h.orderRead, staff, orderNo, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("read by order_no %q: code=%d body=%s", orderNo, rr.Code, rr.Body.String())
	}
	var view OrderView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if view.ID != ord {
		t.Fatalf("resolved id = %q, want %q", view.ID, ord)
	}

	// GET by the order_no WITHOUT the '#', the form a cashier is likelier to type, resolves identically.
	rr2 := invoke(t, h.orderRead, staff, strings.TrimPrefix(orderNo, "#"), "")
	if rr2.Code != http.StatusOK {
		t.Fatalf("read by order_no sans '#': code=%d body=%s", rr2.Code, rr2.Body.String())
	}

	// A transition driven by order_no (not just a read) resolves too, confirm the DRAFT via its
	// visible number, exactly as a cashier pasting it into the lookup box would trigger downstream.
	if rr := invoke(t, h.confirm, staff, orderNo, ""); rr.Code != http.StatusOK {
		t.Fatalf("confirm by order_no: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := liveStatus(t, pool, ord); got != "CONFIRMED" {
		t.Fatalf("status after confirm-by-order_no = %q, want CONFIRMED", got)
	}

	// An unknown order_no is not-found (never a 500 from an invalid-UUID cast), indistinguishable
	// from a garbage/unknown UUID, matching the existing not-found-vs-forbidden contract.
	if rr := invoke(t, h.markPicking, staff, "SO-does-not-exist-999999", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown order_no: code=%d, want 404, body=%s", rr.Code, rr.Body.String())
	}
}
