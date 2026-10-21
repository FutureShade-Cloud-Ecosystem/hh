//go:build integration

// activity_integration_test.go, DIB-492/493 against the live Postgres: the real 0161 order_notes
// writes (idempotent insert, replay convergence, edit attribution stamped by the DATABASE clock),
// the real immediate-documents projection over invoices/payments, and the end-to-end merged
// activity read over the real audit spine, everything the in-memory fakes cannot prove.
package order

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// activityFixture seeds a customer + order (+ one staff user for attribution) in the Trenton branch
// and cleans all of it up.
func activityFixture(t *testing.T) (ctx context.Context, repo *Repository, svc *Service, cust, ord, staffID string) {
	t.Helper()
	pool := intPool(t)
	ctx = context.Background()

	nano := time.Now().UnixNano()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'DIB492 Tenant') RETURNING id::text`,
		fmt.Sprintf("D492-%d", nano)).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 125000, 125000) RETURNING id::text`,
		fmt.Sprintf("#D492-%d", nano), cust, trentonBranch).Scan(&ord); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, status) VALUES ($1, 'Tanya Ho', 'ACTIVE') RETURNING id::text`,
		fmt.Sprintf("d492-%d@test.local", nano)).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM order_notes WHERE order_id = $1`, ord)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_id = $1`, ord)
		_, _ = pool.Exec(ctx, `DELETE FROM payment_reapplications WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_transactions WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `UPDATE payments SET reversed_by_payment_id = NULL WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1)`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE order_id = $1)`, ord)
		_, _ = pool.Exec(ctx, `DELETE FROM payments WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM invoices WHERE order_id = $1`, ord)
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, staffID)
	})

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	repo = NewRepository(pool)
	svc = NewService(repo).WithAudit(auditSvc).WithAuditTrail(auditSvc).withCaller(testCallerFrom)
	return ctx, repo, svc, cust, ord, staffID
}

// TestIntegration_OrderNotes_IdempotentInsertAndEdit proves the 0161 write path end to end:
// one key writes ONE row ever (the retried POST converges on it, flagged replayed), a fresh key
// writes a second row, and an edit stamps edited_by + a database-clock edited_at while leaving the
// author untouched.
func TestIntegration_OrderNotes_IdempotentInsertAndEdit(t *testing.T) {
	ctx, repo, svc, _, ord, staffID := activityFixture(t)
	pool := intPool(t)
	writeCtx := ctxWith(ctx, globalStaff(staffID))

	n1, err := svc.AddOrderNote(writeCtx, ord, "phone", "customer phoned, hold the flagstone until the 14th", "d492-key-1")
	if err != nil {
		t.Fatalf("AddOrderNote: %v", err)
	}
	if n1.Replayed || n1.AuthorID != staffID || n1.AuthorName != "Tanya Ho" {
		t.Fatalf("first note = %+v, want fresh, authored by the session caller with the display name resolved", n1)
	}

	// The retried POST (same one-time key) converges on the SAME row.
	n2, err := svc.AddOrderNote(writeCtx, ord, "phone", "customer phoned, hold the flagstone until the 14th", "d492-key-1")
	if err != nil {
		t.Fatalf("replay AddOrderNote: %v", err)
	}
	if !n2.Replayed || n2.ID != n1.ID {
		t.Fatalf("replay = %+v, want the original note (%s) flagged replayed", n2, n1.ID)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_notes WHERE order_id = $1`, ord).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("order_notes rows = %d, want exactly 1, the unique key must stop the duplicate", count)
	}

	// A fresh key writes a second note.
	if _, err := svc.AddOrderNote(writeCtx, ord, "in-person", "spoke at the counter", "d492-key-2"); err != nil {
		t.Fatalf("second note: %v", err)
	}

	// Edit: the body changes, the channel survives when omitted, and the edit is attributed.
	edited, err := svc.EditOrderNote(writeCtx, ord, n1.ID, "customer phoned, hold until the 21st (revised)", "")
	if err != nil {
		t.Fatalf("EditOrderNote: %v", err)
	}
	if edited.Body != "customer phoned, hold until the 21st (revised)" || edited.Channel != "PHONE" {
		t.Fatalf("edited = %+v, want the new body with the channel preserved", edited)
	}
	if edited.EditedBy != staffID || edited.EditedByName != "Tanya Ho" || edited.EditedAt == nil {
		t.Fatalf("edited = %+v, want edited_by/edited_at stamped with the display name resolved", edited)
	}
	if edited.AuthorID != staffID || edited.CreatedAt.IsZero() {
		t.Fatalf("edited = %+v, the author/created_at must survive an edit", edited)
	}

	// A note id from ANOTHER order answers not-found, no cross-order reach.
	var otherOrd string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 SELECT '#D492B-' || $1, customer_id, branch_id, 'CONFIRMED', 'PICKUP', 1, 1 FROM orders WHERE id = $2
		 RETURNING id::text`, fmt.Sprint(time.Now().UnixNano()), ord).Scan(&otherOrd); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EditOrderNote(writeCtx, otherOrd, n1.ID, "hijack", ""); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("cross-order note edit err = %v, want ErrNoteNotFound", err)
	}
	_ = repo // fixture symmetry
}

// TestIntegration_OrderActivity_EndToEnd drives the real audit choke-point, raises a real invoice +
// payment against the order, records a note, and asserts the ONE merged read: newest first, human
// sentences, notes interleaved and attributed, exactly what the activity panel renders.
func TestIntegration_OrderActivity_EndToEnd(t *testing.T) {
	ctx, _, svc, cust, ord, staffID := activityFixture(t)
	pool := intPool(t)
	readCtx := ctxWith(ctx, globalStaff(staffID))

	// 1. A real status transition through the choke-point (emits the audit row).
	if err := svc.SetStatus(ctx, cust, staffID, ord, "PICKING", "picker_started"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	// 2. A finalized invoice + a payment applied to it (the immediate documents).
	var inv string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
		                       total_cents, balance_cents, finalized_at, created_by)
		 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE + 30, 125000, 25000, now(), $5)
		 RETURNING id::text`,
		fmt.Sprintf("INV-D492-%d", time.Now().UnixNano()), cust, ord, trentonBranch, staffID).Scan(&inv); err != nil {
		t.Fatal(err)
	}
	var pay string
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, branch_id, received_by)
		 VALUES ($1, $2, 'CHEQUE', 100000, $3, $4) RETURNING id::text`,
		fmt.Sprintf("PMT-D492-%d", time.Now().UnixNano()), cust, trentonBranch, staffID).Scan(&pay); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, allocated_by)
		 VALUES ($1, $2, 100000, $3)`, pay, inv, staffID); err != nil {
		// 0043 added allocated_by; if this DB predates it the test cannot make its attribution point.
		t.Fatalf("seed allocation: %v", err)
	}

	// 3. A human note on the same stream.
	if _, err := svc.AddOrderNote(readCtx, ord, "phone", "customer confirmed the cheque is in the mail", "d492-e2e-note"); err != nil {
		t.Fatalf("AddOrderNote: %v", err)
	}

	out, err := svc.OrderActivityFor(readCtx, ord, true)
	if err != nil {
		t.Fatalf("OrderActivityFor: %v", err)
	}

	find := func(summary string) *ActivityRow {
		for i := range out.Rows {
			if out.Rows[i].Summary == summary {
				return &out.Rows[i]
			}
		}
		return nil
	}
	move := find("moved the order from Confirmed to Picking")
	if move == nil {
		t.Fatalf("no status-move row; rows: %+v", out.Rows)
	}
	if move.ActorName != "Tanya Ho" {
		t.Errorf("status-move actor = %q, want the resolved display name Tanya Ho", move.ActorName)
	}
	issued := findPrefix(out.Rows, "issued invoice INV-D492-")
	if issued == nil {
		t.Fatalf("no issued-invoice row; rows: %+v", out.Rows)
	}
	if issued.ActorName != "Tanya Ho" {
		t.Errorf("invoice actor = %q, want Tanya Ho (created_by join)", issued.ActorName)
	}
	if !strings.Contains(issued.Summary, "for $1250.00") {
		t.Errorf("invoice summary = %q, want the money rendered ($1250.00)", issued.Summary)
	}
	paid := findPrefix(out.Rows, "applied a $1000.00 payment (cheque) to invoice INV-D492-")
	if paid == nil {
		t.Fatalf("no payment row; rows: %+v", out.Rows)
	}
	if paid.ActorName != "Tanya Ho" {
		t.Errorf("payment actor = %q, want Tanya Ho (allocated_by join)", paid.ActorName)
	}
	note := find("note taken via phone call")
	if note == nil {
		t.Fatalf("no note row; rows: %+v", out.Rows)
	}
	if note.Kind != "note" || note.Body != "customer confirmed the cheque is in the mail" ||
		note.ActorName != "Tanya Ho" || note.Channel != "PHONE" {
		t.Errorf("note row = %+v, want an attributed PHONE note with its body", note)
	}

	// Newest first, throughout.
	for i := 1; i < len(out.Rows); i++ {
		if out.Rows[i-1].OccurredAt.Before(out.Rows[i].OccurredAt) {
			t.Fatalf("rows not newest-first at %d: %v then %v (%q then %q)",
				i, out.Rows[i-1].OccurredAt, out.Rows[i].OccurredAt, out.Rows[i-1].Summary, out.Rows[i].Summary)
		}
	}
}

// TestIntegration_OrderActivity_BranchScope pins the horizontal boundary against the real resolver:
// a staffer granted only another branch gets 403-shaped ErrForbidden for this order's activity and
// notes, and can leak nothing.
func TestIntegration_OrderActivity_BranchScope(t *testing.T) {
	ctx, _, svc, _, ord, staffID := activityFixture(t)
	// kingston != trenton (0003 seeds); any other seeded branch works for the denial.
	outsider := ctxWith(ctx, staffAt(staffID, "5d1b3f6e-0000-4000-9000-000000000002"))

	if _, err := svc.OrderActivityFor(outsider, ord, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-branch activity err = %v, want ErrForbidden", err)
	}
	if _, err := svc.AddOrderNote(outsider, ord, "phone", "should not land", "d492-scope-key"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-branch note err = %v, want ErrForbidden", err)
	}
	var count int
	if err := intPool(t).QueryRow(ctx, `SELECT count(*) FROM order_notes WHERE order_id = $1`, ord).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a refused cross-branch note landed anyway (%d rows)", count)
	}
}

// findPrefix returns the first row whose summary starts with the prefix.
func findPrefix(rows []ActivityRow, prefix string) *ActivityRow {
	for i := range rows {
		if strings.HasPrefix(rows[i].Summary, prefix) {
			return &rows[i]
		}
	}
	return nil
}

// SF-2r round 2: a slice of a reversed payment's held money applied to an invoice of the order shows in the order's
// activity beside the allocation rows, with its date, amount, tender, the reversed payment it came from and who applied
// it, and only to a caller who holds the payment trail.
func TestIntegration_OrderActivity_AReappliedSliceShows(t *testing.T) {
	ctx, _, svc, cust, ord, staffID := activityFixture(t)
	pool := intPool(t)
	readCtx := ctxWith(ctx, globalStaff(staffID))
	var inv string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date, subtotal_cents, tax_cents,
		                       total_cents, balance_cents, finalized_at, created_by)
		 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE + 30, 125000, 0, 125000, 125000, now(), $5) RETURNING id::text`,
		fmt.Sprintf("INV-RA-%d", time.Now().UnixNano()), cust, ord, trentonBranch, staffID).Scan(&inv); err != nil {
		t.Fatal(err)
	}
	repo := payments.NewRepository(pool)
	p, err := repo.Pay(ctx, cust, "CARD", fmt.Sprintf("gw-ra-%d", time.Now().UnixNano()), []payments.Allocation{{InvoiceID: inv, AmountCents: 10_000}})
	if err != nil {
		t.Fatalf("take the payment: %v", err)
	}
	money := payments.NewReverseService(repo)
	if _, err := money.Reverse(ctx, payments.ReverseInput{PaymentID: p.ID, Reason: "wrong account", ActorID: staffID, IsManager: true}); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if _, err := money.Reapply(ctx, payments.ReapplyInput{PaymentID: p.ID, Reason: "rung on the wrong account", ActorID: staffID, IdempotencyKey: "ra-order-" + inv,
		Allocations: []payments.Allocation{{InvoiceID: inv, AmountCents: 10_000}}, IsManager: true}); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	svc = svc.WithReapplied(NewPaymentsReappliedReader(repo))
	slice := func(withPayments bool) []ActivityRow {
		out, err := svc.OrderActivityFor(readCtx, ord, withPayments)
		if err != nil {
			t.Fatalf("OrderActivityFor: %v", err)
		}
		var rows []ActivityRow
		for _, r := range out.Rows {
			if strings.Contains(r.Summary, "re applied from a reversed payment") {
				rows = append(rows, r)
			}
		}
		return rows
	}
	got := slice(true)
	if len(got) != 1 {
		t.Fatalf("the order's activity carries %d re applied rows, want 1", len(got))
	}
	r := got[0]
	if !strings.Contains(r.Summary, "$100.00") || !strings.Contains(r.Summary, "(card)") || !strings.Contains(r.Summary, "INV-RA-") || !strings.Contains(r.Summary, p.PaymentNo) {
		t.Errorf("the row reads %q, want the amount, tender, invoice and the reversed payment's number", r.Summary)
	}
	if r.ActorName != "Tanya Ho" || r.OccurredAt.Before(time.Now().Add(-time.Minute)) {
		t.Errorf("the row is by %q at %v, want the applier's name and now", r.ActorName, r.OccurredAt)
	}
	if without := slice(false); len(without) != 0 {
		t.Errorf("a caller without the payment trail reads %d re applied rows, want none", len(without))
	}
}
