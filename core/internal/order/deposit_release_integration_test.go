//go:build integration

// deposit_release_integration_test.go, the deposit release end to end against
// the real database: a credit customer's special order ringed at the till with
// the supplier's deposit billed to the account (the 9.5 shape), the ring's two
// documents finalised through the REAL gl posting consumer, the order's
// invoice beating the yard to INVOICED exactly as the runtime drain orders it,
// and then the fulfilment road (AdvanceFromDelivery, the door the dispatch
// handoff and the will-call collection call) releasing the deposit: a
// deposit.released outbox row the same gl consumer posts as DR 2300 / CR
// revenue, after which 2300 has returned to zero and the revenue recognised
// for the sale equals its pre-tax total.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run DepositRelease ./internal/order/
package order

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_DepositRelease_TheRingTheBeatAndTheFulfilment(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	// The 9.5 ring shape: a seeded SF product, a branch, a credit customer.
	var productID, branchID string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text FROM products p
		  JOIN pricing_list_prices lp ON lp.product_id = p.id AND lp.price_uom = 'SF'
		 WHERE p.is_active AND p.base_uom = 'SF' LIMIT 1`).Scan(&productID); err != nil {
		t.Skipf("no seeded SF-priceable product: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	var user string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&user); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	suffix := fmt.Sprintf("ordr-%d", time.Now().UnixNano())
	var cust string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('ORDR-'||$1, 'Order deposit release proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		suffix).Scan(&cust); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}

	// The real till services, exactly the shape the pos suite's realPosSvc
	// wires (an on-account ring captures no card, so no charger is needed).
	cat := product.NewService(product.NewRepository(pool))
	priceSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(cat).
		WithUOMConverter(cat).
		WithMarginGate(pricing.NewIdentityMarginGate())
	tillSvc := till.NewService(till.NewRepository(pool))
	posSvc := pos.NewService(pos.NewRepository(pool)).
		WithLineResolver(priceSvc).
		WithTenderTaker(tillSvc).
		WithCreditGate(pos.NewIdentityCreditGate()).
		WithClock(time.Now)
	ringCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: user, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE", Branches: map[string]bool{},
	})
	// ringKey is a fresh UUIDv4 idempotency key (pos_sale.idempotency_key is a
	// uuid column), the pos suite's key() shape.
	ringKey := func() string {
		var b [16]byte
		_, _ = crand.Read(b[:])
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	}()
	out, err := posSvc.Sale(ringCtx, pos.SaleInput{
		IdempotencyKey: ringKey, BranchID: branchID, CustomerID: cust,
		SaleType: pos.SaleCollectLater, Tender: pos.TenderAccount,
		SupplierDepositCents: 4000,
		Lines:                []pos.LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the ring: %v", err)
	}
	dep := out.SupplierDepositInvoiceID
	if dep == "" || out.SupplierDepositCents != 4000 {
		t.Fatalf("the ring's deposit facts: %+v", out)
	}
	t.Cleanup(func() {
		c := context.Background()
		// The outbox rows this sale and its release wrote (settled or not), so
		// a rerun's counts stand alone. A payload without invoice_id reads as
		// NULL and never matches.
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE (payload->>'invoice_id')::uuid = ANY($1::uuid[])`, []string{dep, out.InvoiceID})
		for _, key := range []string{
			"deposit.released:" + dep, "ar.invoice.finalized:" + dep, "ar.invoice.finalized:" + out.InvoiceID,
		} {
			_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key = $1`, key)
			_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, key)
		}
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id = ANY($1::uuid[])`, []string{dep, out.InvoiceID})
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, cust)
		// pos_sale.order_id is ON DELETE RESTRICT, so the sale rows go first.
		_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE order_id = $1`, out.OrderID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, out.OrderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, out.OrderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})

	// Real services over the real rows: the gl posting consumer as the PRIMARY
	// owner of the finance topics (production's wiring), the lifecycle consumer
	// as the observer, the real dispatcher draining the real outbox.
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	orderSvc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc).
		WithDepositReleaser(billing.NewInvoiceWriter(pool))
	billingRepo := billing.NewRepository(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).WithInvoiceReader(billingRepo)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	NewLifecycleConsumer(orderSvc, billingRepo, nil).RegisterHandlers(dispatcher)

	// The ring's two documents finalise: the deposit into 2300, the rest into
	// revenue and tax, and the order advances to INVOICED, the beat.
	drainOutbox(t, dispatcher)
	if got := liveStatus(t, pool, out.OrderID); got != "INVOICED" {
		t.Fatalf("after the ring's drain the order is %s, want INVOICED (the beat)", got)
	}
	// docNet sums debit minus credit for one account over the sale's three
	// journals. A leg carries exactly one of debit/credit non-null (the 0007
	// CHECK), so each side coalesces or every row would read as nothing.
	docNet := func(code string) int64 {
		var net int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
			  FROM gl_journal_lines l
			  JOIN gl_journals j ON j.id = l.journal_id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.idempotency_key IN ($1, $2, $3) AND a.code = $4`,
			"ar.invoice.finalized:"+dep, "ar.invoice.finalized:"+out.InvoiceID, "deposit.released:"+dep, code).Scan(&net); err != nil {
			t.Fatalf("doc net %s: %v", code, err)
		}
		return net
	}
	if got := docNet("2300"); got != -4000 {
		t.Fatalf("before the fulfilment 2300 nets %d, want the -4000 liability", got)
	}

	// A PARTIAL state releases nothing: nothing has been fulfilled yet, so no
	// deposit.released row exists for this order's deposit document.
	var pending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox WHERE topic = 'deposit.released'
		  AND (payload->>'invoice_id')::uuid = $1`, dep).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("%d deposit.released rows before any fulfilment, want none", pending)
	}

	// P2-2: a task rule's plain event move to FULFILLED (declined here, the
	// order sits INVOICED) is not a delivery: nothing is released or recorded.
	if err := orderSvc.AdvanceFromEvent(ctx, out.OrderID, "FULFILLED", "task-rule"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("the task rule move answers %v, want ErrIllegalTransition", err)
	}
	if got, _ := orderSvc.DeliveredOrderIDs(ctx, []string{out.OrderID}); len(got) != 0 {
		t.Fatalf("a task rule recorded a delivery: %v", got)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox WHERE topic = 'deposit.released'
		  AND (payload->>'invoice_id')::uuid = $1`, dep).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("a task rule's move wrote %d deposit.released rows, want none", pending)
	}

	// The fulfilment road: the material has left, the yard's MarkFulfilled is
	// declined by the ladder (the ring's invoice beat it), and the release
	// fires anyway.
	err = orderSvc.AdvanceFromDelivery(ctx, out.OrderID, "dispatch:test-stop")
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("the beat order answers %v, want ErrIllegalTransition", err)
	}
	if got, _ := orderSvc.DeliveredOrderIDs(ctx, []string{out.OrderID}); len(got) != 1 {
		t.Fatalf("the delivery was not recorded on the order: %v", got)
	}
	var released int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox WHERE topic = 'deposit.released'
		  AND (payload->>'invoice_id')::uuid = $1`, dep).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if released != 1 {
		t.Fatalf("the fulfilment wrote %d deposit.released rows for the deposit document, want the one", released)
	}
	drainOutbox(t, dispatcher)

	// The books after the whole life: 2300 returned to zero, the revenue for
	// the sale equals its pre-tax total, and the three journals balance.
	if got := docNet("2300"); got != 0 {
		t.Fatalf("after the fulfilment 2300 nets %d, want 0 (the deposit released)", got)
	}
	if got := docNet("4000"); got != -out.SubtotalCents {
		t.Fatalf("the revenue for the sale nets %d, want its pre-tax total %d", got, out.SubtotalCents)
	}
	if dr, cr := docNetDebitsAndCredits(t, pool, dep, out.InvoiceID); dr != cr {
		t.Fatalf("the sale's journals do not balance: dr=%d cr=%d", dr, cr)
	}

	// A redelivered fulfilment (a second MarkFulfilled reaching the same
	// order) re-reaches the emission and the ledger dedupes it.
	if err := orderSvc.AdvanceFromDelivery(ctx, out.OrderID, "dispatch:test-stop"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("the second fulfilment answers %v, want ErrIllegalTransition", err)
	}
	drainOutbox(t, dispatcher)
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`,
		"deposit.released:"+dep).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("after the redelivery the release stands at %d journals, want the one", n)
	}
	if got := docNet("2300"); got != 0 {
		t.Fatalf("after the redelivery 2300 nets %d, want 0", got)
	}
}

// docNetDebitsAndCredits sums the sale's three journals' legs.
func docNetDebitsAndCredits(t *testing.T, pool *pgxpool.Pool, dep, rest string) (int64, int64) {
	t.Helper()
	var dr, cr int64
	err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0)),0), COALESCE(SUM(COALESCE(l.credit_cents,0)),0)
		  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		 WHERE j.idempotency_key IN ($1, $2, $3)`,
		"ar.invoice.finalized:"+dep, "ar.invoice.finalized:"+rest, "deposit.released:"+dep).Scan(&dr, &cr)
	if err != nil {
		t.Fatalf("the sale's journals: %v", err)
	}
	return dr, cr
}
