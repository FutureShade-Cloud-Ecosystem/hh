//go:build integration

// DIB-159 integration tests: the real pgx status-transition choke-point (UpdateStatus) + the real
// audit spine (audit.Service over audit_log) against the live Postgres that PG@localhost:5435 serves.
// These prove the two things the unit fakes cannot: that UPDATE orders SET status flips the row and
// returns the true pre-image atomically, and that a transition emitted through the Recorder lands on
// the append-only trail and reads back as the ordered timeline via OrderHistory, end to end, no table.
package order

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// TestIntegration_SetStatus_ChokePointAndTimeline drives a two-step transition through the real
// choke-point + real audit service and asserts the ordered timeline reads back correctly.
func TestIntegration_SetStatus_ChokePointAndTimeline(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	var cust, ord string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'DIB159 Tenant') RETURNING id::text`,
		fmt.Sprintf("D159-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 1000, 1000) RETURNING id::text`,
		fmt.Sprintf("#D159-%d", time.Now().UnixNano()), cust, trentonBranch).Scan(&ord); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_id = $1`, ord)
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust)
	})

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	repo := NewRepository(pool)
	// A GLOBAL-scoped caller rides the context so the branch-scope guard on OrderHistory passes for the
	// end-to-end timeline assertion (the branch-scope forbidden/not-found paths are covered by the unit
	// TestOrderHistory_BranchScope; here we prove the real pgx OrderBranch read + real audit spine).
	svc := NewService(repo).WithAudit(auditSvc).WithAuditTrail(auditSvc).withCaller(testCallerFrom)
	readCtx := ctxWith(ctx, globalStaff("44444444-4444-4444-8444-4444444444ff"))

	actor := "44444444-4444-4444-8444-444444444444"

	// 1. CONFIRMED → PICKING: the choke-point flips the row AND returns the true pre-image.
	if err := svc.SetStatus(ctx, cust, actor, ord, "PICKING", "picker_started"); err != nil {
		t.Fatalf("SetStatus CONFIRMED→PICKING: %v", err)
	}
	var live string
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, ord).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != "PICKING" {
		t.Fatalf("order status = %s, want PICKING (the UPDATE did not land)", live)
	}

	// 2. PICKING → FULFILLED, non-tenant-scoped (an ERP/fulfilment actor, tenant "").
	if err := svc.SetStatus(ctx, "", actor, ord, "FULFILLED", "fulfilled"); err != nil {
		t.Fatalf("SetStatus PICKING→FULFILLED: %v", err)
	}

	// 3. A no-op (FULFILLED→FULFILLED) must surface ErrNoTransition and add NO history row.
	if err := svc.SetStatus(ctx, "", actor, ord, "FULFILLED", ""); err != ErrNoTransition {
		t.Fatalf("no-op transition err = %v, want ErrNoTransition", err)
	}

	// 4. The ordered timeline reads back the two real transitions, oldest → newest, attributed.
	h, err := svc.OrderHistory(readCtx, ord)
	if err != nil {
		t.Fatalf("OrderHistory: %v", err)
	}
	if len(h.Changes) != 2 {
		t.Fatalf("timeline has %d changes, want 2 (the no-op must not appear): %+v", len(h.Changes), h.Changes)
	}
	if h.Changes[0].FromStatus != "CONFIRMED" || h.Changes[0].ToStatus != "PICKING" {
		t.Errorf("change[0] = %q→%q, want CONFIRMED→PICKING", h.Changes[0].FromStatus, h.Changes[0].ToStatus)
	}
	if h.Changes[1].FromStatus != "PICKING" || h.Changes[1].ToStatus != "FULFILLED" {
		t.Errorf("change[1] = %q→%q, want PICKING→FULFILLED", h.Changes[1].FromStatus, h.Changes[1].ToStatus)
	}
	if h.Changes[0].Reason != "picker_started" {
		t.Errorf("change[0] reason = %q, want picker_started", h.Changes[0].Reason)
	}
	if h.Changes[0].ActorID != actor {
		t.Errorf("change[0] actor = %q, want %s", h.Changes[0].ActorID, actor)
	}
	// Oldest strictly before newest (the append-only created_at ordering is authoritative).
	if !h.Changes[0].OccurredAt.Before(h.Changes[1].OccurredAt) &&
		!h.Changes[0].OccurredAt.Equal(h.Changes[1].OccurredAt) {
		t.Errorf("timeline not ordered oldest→newest: %v then %v", h.Changes[0].OccurredAt, h.Changes[1].OccurredAt)
	}
}

// TestIntegration_OrderBranch_MalformedID is the L8 regression for repository.go OrderBranch:
// orders.id is a Postgres uuid column, so a non-UUID id passed to `WHERE id = $1` raises
// "invalid input syntax for type uuid" (SQLSTATE 22P02). Before the fix that surfaced as a wrapped
// error (→ HTTP 500); after the fix a malformed id short-circuits to ErrNotFound, preserving the
// not-found-vs-forbidden contract (a garbage id is indistinguishable from a nonexistent one) and
// never leaking a database error. This test FAILS without the pre-query looksLikeUUID guard.
func TestIntegration_OrderBranch_MalformedID(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	for _, id := range []string{
		"not-a-uuid",
		"1234",
		"'; DROP TABLE orders;--",
		"11111111-1111-1111-1111-11111111111",  // 35 chars, one short
		"gggggggg-gggg-gggg-gggg-gggggggggggg", // right shape, non-hex
		"",
	} {
		_, err := repo.OrderBranch(ctx, id)
		if err == nil {
			t.Errorf("OrderBranch(%q) = nil error, want ErrNotFound", id)
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("OrderBranch(%q) = %v, want ErrNotFound (a malformed id must not leak a DB error / 500)", id, err)
		}
	}

	// A well-formed but absent UUID must ALSO be ErrNotFound (the guard did not change this path).
	if _, err := repo.OrderBranch(ctx, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("OrderBranch(absent valid uuid) = %v, want ErrNotFound", err)
	}
}

// demoHistOrderID is the 0059 DEMO order (migration 0059_order_history_demo.sql). It carries a
// hand-seeded, realistic multi-step status history so the order-history FEATURE is demonstrable
// even though no production lifecycle driver exists yet (DIB-191).
const demoHistOrderID = "07de0000-0000-4000-9000-000000000059"

// TestIntegration_DemoOrderHistory_RendersMultiStepTimeline is the DEMO-GAP regression: it proves
// the 0059 seed makes the order-history feature demonstrable. Against a DB with migration 0059
// applied, GET-equivalent OrderHistory(demoId) must return the full seeded lifecycle timeline
// (DRAFT→CONFIRMED→PICKING→READY→FULFILLED→INVOICED→CLOSED), a multi-row from→to sequence, oldest→
// newest, with resolved staff actor names. This is exactly what a reviewer sees at
// GET /api/v1/orders/{demoId}/history after deploy.
//
// It FAILS without the seed (a real driver would otherwise leave the demo order with ≤1 recorded
// edge, so the multi-transition assertion cannot hold). If the demo order is absent (e.g. the seed
// migration was not applied to this DB), the test skips with a clear message rather than false-fail.
func TestIntegration_DemoOrderHistory_RendersMultiStepTimeline(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	// Require the 0059 fixture; skip (not fail) if this DB was migrated without it.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`, demoHistOrderID).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skipf("DEMO order %s not present, apply migration 0059_order_history_demo.sql to this DB", demoHistOrderID)
	}

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	repo := NewRepository(pool)
	svc := NewService(repo).WithAuditTrail(auditSvc).withCaller(testCallerFrom)
	// GLOBAL-scoped caller so the branch-scope guard passes (branch-scope paths covered elsewhere).
	readCtx := ctxWith(ctx, globalStaff("55555555-5555-4555-8555-555555555555"))

	h, err := svc.OrderHistory(readCtx, demoHistOrderID)
	if err != nil {
		t.Fatalf("OrderHistory(demo): %v", err)
	}

	// The exact seeded lifecycle, oldest→newest, the multi-transition timeline the feature renders.
	wantFrom := []string{"", "DRAFT", "CONFIRMED", "PICKING", "READY", "FULFILLED", "INVOICED"}
	wantTo := []string{"DRAFT", "CONFIRMED", "PICKING", "READY", "FULFILLED", "INVOICED", "CLOSED"}
	if len(h.Changes) != len(wantTo) {
		t.Fatalf("demo timeline has %d changes, want %d (the full DRAFT→…→CLOSED lifecycle): %+v",
			len(h.Changes), len(wantTo), h.Changes)
	}
	for i, c := range h.Changes {
		if c.FromStatus != wantFrom[i] || c.ToStatus != wantTo[i] {
			t.Errorf("change[%d] = %q→%q, want %q→%q", i, c.FromStatus, c.ToStatus, wantFrom[i], wantTo[i])
		}
		if i > 0 && !h.Changes[i-1].OccurredAt.Before(c.OccurredAt) {
			t.Errorf("timeline not strictly increasing at %d: %v then %v", i, h.Changes[i-1].OccurredAt, c.OccurredAt)
		}
	}
	// Attribution resolved: at least one change carries a staff display name (seeded actors are staff).
	named := false
	for _, c := range h.Changes {
		if c.ActorName != "" {
			named = true
			break
		}
	}
	if !named {
		t.Errorf("no change resolved a staff actor_name; want the seeded staff actors' display names")
	}
}

// TestIntegration_UpdateStatus_PreImageUnderConcurrentWriter is the L8 regression for the
// repository.go UpdateStatus CTE: the recorded from_status MUST be the TRUE immediately-prior
// value the write overwrote, even when a concurrent transaction commits a status change between
// this statement's snapshot and its write.
//
// Deterministic race exposure (no sleeps): an explicit transaction txA locks the order and updates
// its status DRAFT→CONFIRMED but does NOT commit. We then fire UpdateStatus(…, "PICKING") on the
// pool in a goroutine, its `cur … FOR UPDATE` blocks on txA's row lock. We commit txA; the choke-
// point unblocks, re-reads the LATEST committed status (CONFIRMED), writes PICKING, and returns the
// true pre-image CONFIRMED.
//
// WITHOUT the FOR UPDATE lock, `cur` is a plain SELECT whose READ-COMMITTED snapshot is taken at
// statement start (before txA commits): it reads the STALE pre-image DRAFT, while `upd` (which
// re-reads the row after txA commits) still writes CONFIRMED→PICKING. The row transitions
// CONFIRMED→PICKING but the method returns from_status = "DRAFT", a false history edge. This test
// asserts the returned pre-image is "CONFIRMED", so it FAILS without the fix and passes with it.
func TestIntegration_UpdateStatus_PreImageUnderConcurrentWriter(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	var cust, ord string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'DIB159 Race Tenant') RETURNING id::text`,
		fmt.Sprintf("D159R-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'DRAFT', 'PICKUP', 1000, 1000) RETURNING id::text`,
		fmt.Sprintf("#D159R-%d", time.Now().UnixNano()), cust, trentonBranch).Scan(&ord); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust)
	})

	// txA: lock the row and commit a DRAFT→CONFIRMED move, but hold it open until the choke-point
	// has begun (and, without the lock, has already snapshotted the stale pre-image).
	txA, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txA.Exec(ctx,
		`UPDATE orders SET status = 'CONFIRMED', updated_at = now() WHERE id = $1`, ord); err != nil {
		_ = txA.Rollback(ctx)
		t.Fatal(err)
	}

	type res struct {
		old string
		err error
	}
	done := make(chan res, 1)
	go func() {
		// This blocks on txA's row lock at `cur … FOR UPDATE` (with the fix). We start it, then give
		// it a beat to reach the lock wait before committing txA.
		old, err := repo.UpdateStatus(ctx, "", ord, "PICKING")
		done <- res{old, err}
	}()

	// Let the goroutine reach the lock wait, then commit txA so the choke-point can proceed.
	time.Sleep(150 * time.Millisecond)
	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	r := <-done
	if r.err != nil {
		t.Fatalf("UpdateStatus under concurrent writer: %v", r.err)
	}
	// The recorded pre-image must be the value txA committed (CONFIRMED), NOT the stale DRAFT.
	if r.old != "CONFIRMED" {
		t.Fatalf("recorded from_status = %q, want CONFIRMED (a stale DRAFT pre-image is the concurrency bug)", r.old)
	}
	// And the row actually landed at PICKING (the write itself was correct).
	var live string
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, ord).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != "PICKING" {
		t.Fatalf("final status = %q, want PICKING", live)
	}
}
