//go:build integration

package payments_test

// alloc_reversal_rows_integration_test.go, W2-6A: the allocation row's KIND.
//
// A bounce and a void used to DELETE their payment's allocation rows: the payment's split across
// invoices died with them, and an as-of aging rebuilt from the dated acts found no allocation for a
// cheque that was good at the as-of date, so the invoice aged at a date it had been paid.
//
// Migration 0450 gives the row a kind instead. An APPLICATION is money applied to an invoice, and
// every row that exists today is an APPLICATION. A REVERSAL is the row that takes an application
// back: it sits on the same payment and the same invoice, it carries the exact negative, it is
// dated at the undo, and it names the application it reverses.
//
// This file is the migration's own contract read off the real schema, and the void's own leg: the
// void keeps the allocations it undoes.
//
// Run: make reset && go test -tags integration ./internal/payments/...
import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

func TestIntegration_W2_6A_AnAllocationRowCarriesItsKind(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.registerReceipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)

	// Every row that exists today reads as an APPLICATION.
	var kind string
	var amount int64
	if err := w.r.obs.QueryRow(ctx,
		`SELECT kind, amount_cents FROM payment_allocations
		  WHERE payment_id = $1::uuid AND invoice_id = $2::uuid`, orig, inv).Scan(&kind, &amount); err != nil {
		t.Fatalf("read the allocation row: %v", err)
	}
	if kind != "APPLICATION" {
		t.Fatalf("the allocation row reads kind %q, want APPLICATION", kind)
	}
	if amount != 10_000 {
		t.Fatalf("the allocation row reads %d cents, want 10000", amount)
	}

	// The amount check is kind-aware, not dropped: an application is positive, as it always was.
	fresh := w.r.invoice("W26AKIND", 1_000_000)
	if _, err := w.r.pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid, $2::uuid, -1)`,
		orig, fresh); err == nil {
		t.Fatalf("a negative application row was accepted: the amount check must stay kind-aware")
	}
}

func TestIntegration_W2_6A_AReversalRowNamesTheApplicationItReverses(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.registerReceipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)

	var appID string
	if err := w.r.obs.QueryRow(ctx,
		`SELECT id::text FROM payment_allocations WHERE payment_id = $1::uuid AND invoice_id = $2::uuid`,
		orig, inv).Scan(&appID); err != nil {
		t.Fatalf("read the application row: %v", err)
	}

	// A reversal that names no application is refused.
	if _, err := w.r.pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, kind)
		 VALUES ($1::uuid, $2::uuid, -10000, 'REVERSAL')`, orig, inv); err == nil {
		t.Fatalf("a reversal row that names no application was accepted")
	}
	// A reversal that does not carry the application's exact negative is refused.
	if _, err := w.r.pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, kind, reverses_allocation_id)
		 VALUES ($1::uuid, $2::uuid, -9999, 'REVERSAL', $3::uuid)`, orig, inv, appID); err == nil {
		t.Fatalf("a reversal row carrying -9999 against an application of 10000 was accepted")
	}
	// A reversal that names another payment's application is refused.
	other := w.registerReceipt("CASH", 10_000)
	var otherApp string
	if err := w.r.obs.QueryRow(ctx,
		`SELECT id::text FROM payment_allocations WHERE payment_id = $1::uuid`, other).Scan(&otherApp); err != nil {
		t.Fatalf("read the other application row: %v", err)
	}
	if _, err := w.r.pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, kind, reverses_allocation_id)
		 VALUES ($1::uuid, $2::uuid, -10000, 'REVERSAL', $3::uuid)`, orig, inv, otherApp); err == nil {
		t.Fatalf("a reversal row naming another payment's application was accepted")
	}

	// The right one is accepted: the same payment, the same invoice, the exact negative, naming the
	// application it reverses.
	if _, err := w.r.pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, kind, reverses_allocation_id)
		 VALUES ($1::uuid, $2::uuid, -10000, 'REVERSAL', $3::uuid)`, orig, inv, appID); err != nil {
		t.Fatalf("the reversal row that names its application was refused: %v", err)
	}
}

func TestIntegration_W2_6A_TheVoidKeepsTheAllocationsItUndoes(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.registerReceipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()

	repo := payments.NewRepository(w.r.pool)
	at := time.Now()
	if _, err := repo.VoidPayment(ctx, payments.VoidInput{
		PaymentID: orig, Reason: "keyed against the wrong figures", ActorID: w.r.userID, IsManager: true,
	}, at, houseday.DefaultZone()); err != nil {
		t.Fatalf("void: %v", err)
	}

	// The application is still there, and the undo is written beside it as a reversal row: the same
	// payment and the same invoice, the exact negative, dated at the void's own instant, naming the
	// application it reverses.
	var appID, revID string
	var appAmt, revAmt int64
	var revKind string
	var revAt time.Time
	var revRef *string
	if err := w.r.obs.QueryRow(ctx,
		`SELECT a.id::text, a.amount_cents, r.id::text, r.kind, r.amount_cents, r.created_at,
		        r.reverses_allocation_id::text
		   FROM payment_allocations a
		   JOIN payment_allocations r
		     ON r.payment_id = a.payment_id AND r.invoice_id = a.invoice_id AND r.kind = 'REVERSAL'
		  WHERE a.payment_id = $1::uuid AND a.kind = 'APPLICATION'`, orig).
		Scan(&appID, &appAmt, &revID, &revKind, &revAmt, &revAt, &revRef); err != nil {
		t.Fatalf("read the allocation history: %v", err)
	}
	if appAmt != 10_000 {
		t.Fatalf("the application row reads %d cents, want 10000", appAmt)
	}
	if revKind != "REVERSAL" {
		t.Fatalf("the undo row reads kind %q, want REVERSAL", revKind)
	}
	if revAmt != -10_000 {
		t.Fatalf("the reversal row reads %d cents, want -10000 (the exact negative of the application)", revAmt)
	}
	if revRef == nil || *revRef != appID {
		t.Fatalf("the reversal row names %v, want the application row %s", revRef, appID)
	}
	if d := revAt.Sub(at); d > time.Minute || d < -time.Minute {
		t.Fatalf("the reversal row is dated %s and the void %s: the undo must carry the void's own instant", revAt, at)
	}

	// The figures the void moved did not move differently: the invoice is open again at its full
	// balance and nothing is held in 2310.
	var bal int64
	var invStatus string
	if err := w.r.obs.QueryRow(ctx,
		`SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, inv).Scan(&bal, &invStatus); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if bal != 1_000_000 || invStatus != "FINALIZED" {
		t.Fatalf("the invoice reads %s/%d after the void, want FINALIZED/1000000", invStatus, bal)
	}
	held, err := repo.HeldFor(ctx, orig, nil)
	if err != nil {
		t.Fatalf("held read: %v", err)
	}
	if held.HeldCents != 0 {
		t.Fatalf("a voided receipt holds %d cents, want 0: the money never left the building", held.HeldCents)
	}
}
