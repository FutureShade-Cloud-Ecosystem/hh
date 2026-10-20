//go:build integration

package parity

// The receivables world the slice 7a tests plant for themselves (never assuming the seeded one): two branches of
// their own, two customers, invoices of every kind the aging and the register treat differently (an old open
// one, a partly paid one, a paid one, a draft, a credit memo, one not yet due), payments of every status the
// workspace list and the payment history treat differently (received, bounced, void, refunded), allocations
// including one that crosses branches (a payment taken at one branch applied to an invoice of the other).

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

type arWorld struct {
	a, b         string // the test's own branches
	custA, custB string
	tag          string
	invoices     map[string]string // short name -> id
	payments     map[string]string
}

func plantARWorld(t *testing.T, env *testsupport.Env) *arWorld {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	w := &arWorld{tag: tag, invoices: map[string]string{}, payments: map[string]string{}}
	w.a = plantBranch(t, env, tag+"A", "S7a fixture A")
	w.b = plantBranch(t, env, tag+"B", "S7a fixture B")
	cust := func(suffix, name string) string {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO customers (account_no, name) VALUES ($1, $2) RETURNING id::text`, "S7A-"+tag+suffix, name).Scan(&id); err != nil {
			t.Fatalf("plant customer: %v", err)
		}
		return id
	}
	w.custA, w.custB = cust("A", "Planted Customer A "+tag), cust("B", "Planted Customer B "+tag)

	inv := func(name, branch, customer, docType, status string, dueOffsetDays int, total, balance int64) {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO invoices (invoice_no, customer_id, branch_id, doc_type, status, issue_date, due_date, subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents)
			VALUES ($1, $2::uuid, $3::uuid, $4, $5, CURRENT_DATE - 120, CURRENT_DATE + $6::int, $7, 0, $8, $7, $9) RETURNING id::text`,
			fmt.Sprintf("INV-S7A-%s-%s", tag, name), customer, branch, docType, status, dueOffsetDays, total, total/10, balance).Scan(&id); err != nil {
			t.Fatalf("plant invoice %s: %v", name, err)
		}
		w.invoices[name] = id
	}
	// Branch a, customer A.
	inv("a-old", w.a, w.custA, "INVOICE", "FINALIZED", -75, 10000, 10000)
	inv("a-part", w.a, w.custA, "INVOICE", "PARTIALLY_PAID", -5, 8000, 2500)
	inv("a-paid", w.a, w.custA, "INVOICE", "PAID", -20, 3000, 0)
	inv("a-draft", w.a, w.custA, "INVOICE", "DRAFT", 30, 1200, 0)
	inv("a-memo", w.a, w.custA, "CREDIT_MEMO", "FINALIZED", -10, -2000, -2000)
	// Branch b, customer B (and one of customer A's, so an account lives at both branches).
	inv("b-soon", w.b, w.custB, "INVOICE", "FINALIZED", 10, 7000, 7000)
	inv("b-ancient", w.b, w.custB, "INVOICE", "FINALIZED", -100, 4000, 4000)
	inv("b-cust-a", w.b, w.custA, "INVOICE", "FINALIZED", -35, 5500, 5500)

	pay := func(name, branch, customer, method, status string, amount int64) {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, gateway_ref, payment_token)
			VALUES ($1, $2::uuid, $3, $4, $5, $6::uuid, $7, $8) RETURNING id::text`,
			fmt.Sprintf("PAY-S7A-%s-%s", tag, name), customer, method, amount, status, branch, "gw-secret-"+name, "tok-secret-"+name).Scan(&id); err != nil {
			t.Fatalf("plant payment %s: %v", name, err)
		}
		w.payments[name] = id
	}
	pay("a-cash", w.a, w.custA, "CASH", "RECEIVED", 5000)
	pay("a-cheque-bounced", w.a, w.custA, "CHEQUE", "BOUNCED", 3000)
	pay("a-cross", w.a, w.custA, "EFT", "DEPOSITED", 1500) // applied to a branch b invoice
	pay("a-unapplied", w.a, w.custA, "CARD", "RECEIVED", 900)
	pay("b-eft", w.b, w.custB, "EFT", "RECEIVED", 7000)
	pay("b-void", w.b, w.custB, "CASH", "VOID", 400)
	pay("b-refunded", w.b, w.custB, "CARD", "REFUNDED", 650)

	alloc := func(payment, invoice string, amount int64) {
		if _, err := env.Admin.Exec(ctx, `INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid, $2::uuid, $3)`,
			w.payments[payment], w.invoices[invoice], amount); err != nil {
			t.Fatalf("plant allocation: %v", err)
		}
	}
	alloc("a-cash", "a-part", 5000)
	alloc("a-cross", "b-cust-a", 1500)
	alloc("b-eft", "b-soon", 7000)
	alloc("b-refunded", "b-ancient", 650)

	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM payments WHERE payment_no LIKE $1`, "PAY-S7A-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_no LIKE $1`, "INV-S7A-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM customers WHERE account_no LIKE $1`, "S7A-"+tag+"%")
	})
	return w
}

func (w *arWorld) branches() []string { return []string{w.a, w.b} }
