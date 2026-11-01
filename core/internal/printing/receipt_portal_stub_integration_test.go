//go:build integration

// The portal rail's own simulated stub (payments.StubGateway, the DEFAULT gateway) reaching the
// printed receipt. Its CARD payments settle invoices through payment_allocations and reach
// ReadReceiptTenders through the ALLOCATION arm, so an account or COD sale settled on the stub
// must print the same simulated word a till stub capture prints: the customer's paper must not
// claim a card charge that never reached a processor.
//
// Run with: DATABASE_URL=... go test -tags integration ./internal/printing/
package printing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/jackc/pgx/v5/pgxpool"
)

const portalSimulatedWord = "Simulated card: nothing reached a processor."

// portalPayRig seeds a tenant with one FINALIZED invoice, so the settle leg can pay it through
// the REAL stub via the service's own settle path and leave rows the receipt's allocation arm
// actually reads (the payRig idiom from the payments acceptance suite).
type portalPayRig struct {
	pool       *pgxpool.Pool
	svc        *payments.Service
	customerID string
	invoiceID  string
	methodID   string
}

func newPortalPayRig(t *testing.T) *portalPayRig {
	t.Helper()
	ctx := context.Background()
	pool := intPool(t)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()%1_000_000_000)
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('PORTALACC-'||$1, 'Portal Stub Receipt '||$1, 'CONTRACTOR', $2::uuid, 1000000) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
		// payment_intents rows go BEFORE payments: a settle leaves the intent SETTLED, and the
		// SETTLED intent's own CHECK forbids a null payment, so deleting payments first fails on
		// that CHECK (the intents' payment column cannot SET NULL while the intent stands) and
		// the chain stops with the customer and its rows behind, the errors swallowed. Children
		// before parents from here down.
		_, _ = pool.Exec(c, `DELETE FROM payment_intents WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id IN
			(SELECT id FROM payments WHERE customer_id = $1::uuid)`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM portal_payment_methods WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	var invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents)
		 VALUES ('INV-PORTAL-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30, 40000, 0, 40000, 40000)
		 RETURNING id::text`, suffix, customerID, branchID).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	svc := payments.NewService(payments.NewRepository(pool), payments.NewStubGateway())
	m, err := svc.AddMethod(ctx, customerID, payments.AddMethodRequest{Nonce: "nonce-" + suffix, MakeDefault: true})
	if err != nil {
		t.Fatalf("vault a method through the stub: %v", err)
	}
	return &portalPayRig{pool: pool, svc: svc, customerID: customerID, invoiceID: invoiceID, methodID: m.ID}
}

// The tie: a settlement the REAL portal stub ran must print the word. The service settles the
// invoice through the allocation arm carrying the gateway's own minted ref, the receipt read by
// INVOICE (the allocation arm, not the sale's payment id) must stamp that CARD row Simulated,
// and the rendered paper must carry the word exactly once, on that row.
func TestIntegration_Receipt_PortalStubSettlementPrintsTheWord(t *testing.T) {
	rig := newPortalPayRig(t)
	ctx := context.Background()

	pay, err := rig.svc.Pay(ctx, rig.customerID, payments.PayRequest{
		InvoiceIDs: []string{rig.invoiceID}, PaymentMethodID: rig.methodID, ExpectedCents: 40000,
	})
	if err != nil {
		t.Fatalf("portal stub pay: %v", err)
	}
	if pay.Status != "RECEIVED" {
		t.Fatalf("fixture assumption broken: payment status %q, want RECEIVED", pay.Status)
	}
	if !payments.IsStubChargeRef(pay.GatewayRef) {
		t.Fatalf("the settle persisted %q, which the exported shape check (prefix %q) does not name: the mint and the discriminator have drifted", pay.GatewayRef, payments.StubChargeRefPrefix)
	}

	tenders, err := NewRepository(rig.pool).ReadReceiptTenders(ctx, "", rig.invoiceID)
	if err != nil {
		t.Fatalf("read tenders: %v", err)
	}
	if len(tenders) != 1 {
		t.Fatalf("want the one card row through the allocation arm, got %d", len(tenders))
	}
	if tenders[0].Method != "CARD" {
		t.Fatalf("the settled row must read CARD, got %q", tenders[0].Method)
	}
	if !tenders[0].Simulated {
		t.Fatalf("a CARD settlement minted by the real portal stub (ref %q) must stamp Simulated, the paper claims a live charge", pay.GatewayRef)
	}

	html, err := RenderReceiptHTML(Receipt{
		OrderNo: "#SO-PORTAL", TaxLabel: "HST (13%)",
		SoldAt: time.Now(), PrintedAt: time.Now(),
		Tenders: tenders,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if n := strings.Count(html, portalSimulatedWord); n != 1 {
		t.Fatalf("the portal stub settlement's receipt must carry the word exactly once, got %d", n)
	}
}

// insertPortalCard plants a bare payments row (the scan leg's fixture idiom): the stamping
// decision over the row's own method and ref is what the guard legs pin.
func insertPortalCard(t *testing.T, pool *pgxpool.Pool, method, gatewayRef string) string {
	t.Helper()
	var branchID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	id := newUUID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO payments (id, payment_no, method, amount_cents, status, branch_id, gateway_ref)
		 VALUES ($1::uuid, $2, $3, 5000, 'RECEIVED', $4::uuid, $5)`,
		id, "PMT-PORTAL-"+id[:8], method, branchID, gatewayRef); err != nil {
		t.Fatalf("insert %s payment: %v", method, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, id)
	})
	return id
}

// The live leg: the live gateway writes the processor's own opaque TransID verbatim, a shape
// not ours to bound (the verified fixture's is digits). A live charge must stamp nothing and
// print no word anywhere on the paper.
func TestIntegration_Receipt_LiveRunShapedRefStampsNothing(t *testing.T) {
	pool := intPool(t)
	id := insertPortalCard(t, pool, "CARD", "8812340077")
	tenders, err := NewRepository(pool).ReadReceiptTenders(context.Background(), id, "")
	if err != nil {
		t.Fatalf("read tenders: %v", err)
	}
	if len(tenders) != 1 {
		t.Fatalf("want the one card row, got %d", len(tenders))
	}
	if tenders[0].Simulated {
		t.Fatal("a CARD capture carrying a live processor's own id must NOT stamp Simulated, a live charge is a live charge")
	}
	html, err := RenderReceiptHTML(Receipt{Tenders: tenders, SoldAt: time.Now(), PrintedAt: time.Now()})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "Simulated card") {
		t.Fatal("a live card charge must print no simulated word anywhere on the receipt")
	}
}

// The bare prefix is not the discriminator, the exact construction is: a ref that begins with
// the stub's prefix but carries a LONGER hex tail is a live processor shape collision, and it
// must stamp nothing. Widening the stamp to a bare prefix match fails this leg.
func TestIntegration_Receipt_LongHexChRefStampsNothing(t *testing.T) {
	pool := intPool(t)
	ref := payments.StubChargeRefPrefix + "0123456789abcdef0123456789abcdef"
	id := insertPortalCard(t, pool, "CARD", ref)
	tenders, err := NewRepository(pool).ReadReceiptTenders(context.Background(), id, "")
	if err != nil {
		t.Fatalf("read tenders: %v", err)
	}
	if len(tenders) != 1 {
		t.Fatalf("want the one card row, got %d", len(tenders))
	}
	if tenders[0].Simulated {
		t.Fatalf("the CARD ref %q is not the stub's construction (the mint carries exactly 16 hex chars, this carries 32) and must NOT stamp Simulated", ref)
	}
	html, err := RenderReceiptHTML(Receipt{Tenders: tenders, SoldAt: time.Now(), PrintedAt: time.Now()})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "Simulated card") {
		t.Fatal("a near-miss live shape must print no simulated word anywhere on the receipt")
	}
}

// The word belongs to a CARD row: a NON-CARD tender carrying a ref of the stub's own mint shape
// is a bank settlement with an unlucky ref, not a simulated card charge, and it must stamp
// nothing and print no word. Dropping the CARD guard from the portal disjunct fails this leg.
func TestIntegration_Receipt_NonCardRowWithStubShapeStampsNothing(t *testing.T) {
	pool := intPool(t)
	res, err := payments.NewStubGateway().Charge(context.Background(), payments.ChargeRequest{
		CustomerID:     "cust-non-card-guard",
		AmountCents:    5000,
		IdempotencyKey: "idem-non-card-guard",
	})
	if err != nil {
		t.Fatalf("stub charge: %v", err)
	}
	if !payments.IsStubChargeRef(res.GatewayRef) {
		t.Fatalf("fixture assumption broken: %q is not the stub's mint shape", res.GatewayRef)
	}
	id := insertPortalCard(t, pool, "EFT", res.GatewayRef)
	tenders, err := NewRepository(pool).ReadReceiptTenders(context.Background(), id, "")
	if err != nil {
		t.Fatalf("read tenders: %v", err)
	}
	if len(tenders) != 1 {
		t.Fatalf("want the one EFT row, got %d", len(tenders))
	}
	if tenders[0].Method != "EFT" {
		t.Fatalf("fixture assumption broken: method %q, want EFT", tenders[0].Method)
	}
	if tenders[0].Simulated {
		t.Fatalf("a non CARD row (method %q) must never stamp Simulated, even carrying a ref of the stub's own mint shape (%q)", tenders[0].Method, res.GatewayRef)
	}
	html, err := RenderReceiptHTML(Receipt{Tenders: tenders, SoldAt: time.Now(), PrintedAt: time.Now()})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "Simulated card") {
		t.Fatal("a non CARD tender must print no simulated word anywhere on the receipt")
	}
}
