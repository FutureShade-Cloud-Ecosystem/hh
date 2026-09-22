//go:build integration

// dib510_receipt_integration_test.go, THE DOUBLE-POST GUARD, PROVED AGAINST THE REAL SCHEMA.
//
// The claim the receivables register makes is specific: pressing Record Payment twice with the same
// one-time key applies the money ONCE. A unit test cannot prove that, because the guard is not in Go -
// it is the partial unique index 0028 put on payments.idempotency_key, and the ON CONFLICT DO NOTHING
// that reads it. So this file runs the real repository against the migrated database and looks at the
// LEDGER afterwards rather than at the return value: one payment row, one allocation, one balance
// movement, one subledger entry.
//
// It also proves the thing that would make the guard useless: that the second call does not merely
// return early having already written a second allocation.
//
// Run: go test -tags integration ./internal/payments/...
package payments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const dib510Trenton = "5d1b3f6e-0000-4000-9000-000000000001"

// d510RandomTag keeps two concurrent runs off each other's UNIQUE indexes without putting a clock in
// a file that also pins instants, the DIB-427 rule the testaudit gate holds.
func d510RandomTag() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fixed"
	}
	return hex.EncodeToString(b[:])
}

func dib510Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
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

// dib510Fixture creates a test-owned customer with ONE open invoice and tears both down.
func dib510Fixture(t *testing.T, pool *pgxpool.Pool, balance int64) (customerID, invoiceID, tag string) {
	t.Helper()
	ctx := context.Background()
	tag = "D510-" + d510RandomTag()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
		tag, "DIB-510 receipt "+tag, dib510Trenton).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE - 40, CURRENT_DATE - 10,
		         $4, 0, $4, $4, now()) RETURNING id::text`,
		"INV-"+tag, customerID, dib510Trenton, balance).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id = $1::uuid`, invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1::uuid`, invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})
	return customerID, invoiceID, tag
}

func TestRecordReceipt_ARepeatedRecordPaymentDoesNotDoublePost(t *testing.T) {
	pool := dib510Pool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	customerID, invoiceID, tag := dib510Fixture(t, pool, 100000)

	key := "dib510-" + tag
	req := ReceiptRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ 1234",
		IdempotencyKey: key, BranchID: dib510Trenton,
		Allocations: []Allocation{{InvoiceID: invoiceID, AmountCents: 40000}},
	}

	first, err := repo.RecordReceipt(ctx, req)
	if err != nil {
		t.Fatalf("first receipt: %v", err)
	}
	if first.Replayed {
		t.Fatal("the first receipt is not a replay")
	}

	second, err := repo.RecordReceipt(ctx, req)
	if err != nil {
		t.Fatalf("second receipt (the double-click) must SUCCEED and report a replay, not error: %v", err)
	}
	if !second.Replayed {
		t.Fatal("the second receipt must be reported as a replay")
	}
	if second.Payment.ID != first.Payment.ID {
		t.Fatalf("a replay must answer with the SAME payment: %s vs %s", second.Payment.ID, first.Payment.ID)
	}

	// - the ledger is the witness, not the return value -
	var payments, allocations, subledger int
	var applied, balance int64
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid),
		        (SELECT COUNT(*) FROM payment_allocations WHERE invoice_id = $2::uuid),
		        (SELECT COUNT(*) FROM customer_transactions WHERE customer_id = $1::uuid AND txn_type = 'PAYMENT'),
		        (SELECT COALESCE(SUM(amount_cents),0) FROM payment_allocations WHERE invoice_id = $2::uuid),
		        (SELECT balance_cents FROM invoices WHERE id = $2::uuid),
		        (SELECT status FROM invoices WHERE id = $2::uuid)`,
		customerID, invoiceID).Scan(&payments, &allocations, &subledger, &applied, &balance, &status); err != nil {
		t.Fatalf("read back the ledger: %v", err)
	}
	if payments != 1 {
		t.Fatalf("exactly ONE payment row must exist, found %d", payments)
	}
	if allocations != 1 {
		t.Fatalf("exactly ONE allocation must exist, found %d", allocations)
	}
	if subledger != 1 {
		t.Fatalf("exactly ONE AR subledger row must exist, found %d", subledger)
	}
	if applied != 40000 {
		t.Fatalf("exactly 40000 cents must be applied, found %d", applied)
	}
	if balance != 60000 {
		t.Fatalf("the balance must have moved ONCE (100000 → 60000), found %d", balance)
	}
	if status != "PARTIALLY_PAID" {
		t.Fatalf("a part-settled invoice is PARTIALLY_PAID, found %s", status)
	}
}

func TestRecordReceipt_SettlesInFullAndStampsAttribution(t *testing.T) {
	pool := dib510Pool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	customerID, invoiceID, tag := dib510Fixture(t, pool, 25000)

	var staffID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE status = 'ACTIVE' LIMIT 1`).Scan(&staffID); err != nil {
		t.Skipf("no active user to attribute to: %v", err)
	}

	out, err := repo.RecordReceipt(ctx, ReceiptRequest{
		CustomerID: customerID, Method: "EFT", Reference: "TRACE-99",
		IdempotencyKey: "dib510-full-" + tag, ReceivedBy: staffID, BranchID: dib510Trenton,
		Allocations: []Allocation{{InvoiceID: invoiceID, AmountCents: 25000}},
	})
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if out.Payment.AmountCents != 25000 {
		t.Fatalf("the payment header carries the summed amount, got %d", out.Payment.AmountCents)
	}

	var status string
	var balance int64
	var receivedBy, allocatedBy, reference *string
	if err := pool.QueryRow(ctx,
		`SELECT i.status, i.balance_cents, p.received_by::text, pa.allocated_by::text,
		        p.metadata->>'reference'
		   FROM invoices i
		   JOIN payment_allocations pa ON pa.invoice_id = i.id
		   JOIN payments p ON p.id = pa.payment_id
		  WHERE i.id = $1::uuid`, invoiceID).
		Scan(&status, &balance, &receivedBy, &allocatedBy, &reference); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "PAID" || balance != 0 {
		t.Fatalf("a fully settled invoice is PAID with a zero balance, got %s/%d", status, balance)
	}
	// Q-044: WHO took it and WHO applied it. A counter receipt has both; a portal payment has neither.
	if receivedBy == nil || *receivedBy != staffID {
		t.Fatalf("received_by must be stamped with the staff caller, got %v", receivedBy)
	}
	if allocatedBy == nil || *allocatedBy != staffID {
		t.Fatalf("allocated_by must be stamped with the staff caller, got %v", allocatedBy)
	}
	if reference == nil || *reference != "TRACE-99" {
		t.Fatalf("the tender reference must be recorded verbatim, got %v", reference)
	}
}

func TestRecordReceipt_RefusesMoreThanTheInvoiceCanTake(t *testing.T) {
	pool := dib510Pool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	customerID, invoiceID, tag := dib510Fixture(t, pool, 10000)

	if _, err := repo.RecordReceipt(ctx, ReceiptRequest{
		CustomerID: customerID, Method: "CASH", IdempotencyKey: "dib510-over-" + tag,
		Allocations: []Allocation{{InvoiceID: invoiceID, AmountCents: 10001}},
	}); err == nil {
		t.Fatal("a receipt larger than the payable balance must be refused")
	}

	// And the refusal wrote nothing: no orphan payment header claiming the key.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM payments WHERE idempotency_key = $1`,
		"dib510-over-"+tag).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("a refused receipt must leave no payment row behind, found %d", n)
	}
}

// PayableCentsFor is the ceiling BOTH the screen and the write read. This proves they are one number
// , including the Bill 60 holdback exclusion, which is the case where a naive "balance" would offer
// money the customer is legally entitled to retain.
func TestPayableCentsFor_ExcludesTheHoldback(t *testing.T) {
	pool := dib510Pool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	customerID, invoiceID, _ := dib510Fixture(t, pool, 100000)

	if _, err := pool.Exec(ctx, `UPDATE invoices SET holdback_cents = 10000 WHERE id = $1::uuid`, invoiceID); err != nil {
		t.Fatalf("set holdback: %v", err)
	}
	payable, gotCustomer, err := repo.PayableCentsFor(ctx, invoiceID)
	if err != nil {
		t.Fatalf("payable: %v", err)
	}
	if payable != 90000 {
		t.Fatalf("payable must exclude the holdback (100000 − 10000), got %d", payable)
	}
	if gotCustomer != customerID {
		t.Fatalf("the payable read also resolves the owning account, got %s", gotCustomer)
	}
}
