//go:build integration

// p063_statement_words_integration_test.go, 0.63: the plain statement a customer reads (the printed, emailed
// and portal documents all read billing.StatementLines) carries fixed words for every undo and credit write,
// never a clerk's free text and never an internal id. Each leg drives the REAL door with a planted reason
// carrying a marker, a figure, another invoice's number and a uuid, then reads the plain statement. A staff
// read (the customer AR read) keeps the full memo. The billing package cannot import payments or till, so the
// legs live here.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

const p063Reason = "P063LEAK keyed 77777 against INVSECRET-9 aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee by mistake"

var p063UUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

type p063World struct {
	pool       *pgxpool.Pool
	cust       string
	invA, invB string
	pay        *payments.Repository
	bill       *billing.Repository
}

func p063Exec(t *testing.T, pool *pgxpool.Pool, what, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func p063Pool(t *testing.T, suffix string) *pgxpool.Pool {
	t.Helper()
	pool := freshMigratedDB(t, suffix)
	t.Cleanup(func() {
		pool.Close()
		admin, err := pgxpool.New(context.Background(), swapDatabase(os.Getenv("DATABASE_URL"), "postgres"))
		if err != nil {
			t.Errorf("p063: connect to drop the leg database: %v", err)
			return
		}
		defer admin.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS demoseed_it_"+suffix+" WITH (FORCE)"); err != nil {
			t.Errorf("p063: drop the leg database: %v", err)
		}
	})
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	p063Exec(t, pool, "apply the seed", string(body))
	p063Exec(t, pool, "plant the org", `INSERT INTO org (id, slug, name) VALUES ($1, 'p063-'||$2::text, 'P063 Org')`, cmRoundNorthOrg, suffix)
	p063Exec(t, pool, "stamp the branches", `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, cmRoundNorthOrg)
	return pool
}

// p063Plant seeds one customer with two invoices (30000 and 20000) issued 45 days ago, as ledger rows the
// statement lists, on a database that already holds the seed.
func p063Plant(t *testing.T, pool *pgxpool.Pool) *p063World {
	t.Helper()
	w := &p063World{pool: pool, pay: payments.NewRepository(pool), bill: billing.NewRepository(pool)}
	ctx := context.Background()
	tag := fmt.Sprintf("P063-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 5000000) RETURNING id::text`, tag, "P063 "+tag, trentonBranchID).Scan(&w.cust); err != nil {
		t.Fatalf("seed the customer: %v", err)
	}
	t.Cleanup(func() { w.cleanup(t) })
	var running int64
	for _, inv := range []struct {
		dst    *string
		suffix string
		total  int64
	}{{&w.invA, "-A", 30000}, {&w.invB, "-B", 20000}} {
		running += inv.total
		if err := pool.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, branch_id, org_id, status, issue_date, due_date, subtotal_cents, tax_cents,
                      total_cents, balance_cents, finalized_at, doc_type)
VALUES ($1, $2::uuid, $3::uuid, (SELECT org_id FROM locations WHERE id = $3::uuid), 'FINALIZED', current_date - 45, current_date - 15, $4, 0, $4, $4,
        now() - interval '45 days', 'INVOICE')
RETURNING id::text`, tag+inv.suffix, w.cust, trentonBranchID, inv.total).Scan(inv.dst); err != nil {
			t.Fatalf("seed invoice %s: %v", inv.suffix, err)
		}
		p063Exec(t, pool, "seed the INVOICE ledger row", `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'INVOICE', $2, $6, 'invoice', $3::uuid, $4::uuid, $5,
        (current_date - 45)::timestamp AT TIME ZONE 'America/Toronto')`,
			w.cust, inv.total, *inv.dst, trentonBranchID, tag+inv.suffix, running)
	}
	return w
}

func (w *p063World) cleanup(t *testing.T) {
	c := context.Background()
	for _, q := range []struct{ table, sql string }{
		{"events_outbox", `DELETE FROM events_outbox WHERE payload->>'payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`},
		{"payment_reapplications", `DELETE FROM payment_reapplications WHERE customer_id = $1::uuid`},
		{"payment_allocations", `DELETE FROM payment_allocations WHERE kind = 'REVERSAL' AND payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
		{"payment_allocations", `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
		{"payments", `UPDATE payments SET reversed_by_payment_id = NULL WHERE customer_id = $1::uuid AND reversed_by_payment_id IS NOT NULL`},
		{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid AND reverses_payment_id IS NOT NULL`},
		{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid`},
		{"customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`},
		{"credit_application_keys", `DELETE FROM credit_application_keys WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`},
		{"invoice_lines", `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`},
		{"invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`},
		{"customers", `DELETE FROM customers WHERE id = $1::uuid`},
	} {
		if _, err := w.pool.Exec(c, q.sql, w.cust); err != nil {
			t.Errorf("cleanup %s: %v", q.table, err)
		}
	}
}

func (w *p063World) receipt(t *testing.T) payments.Payment {
	t.Helper()
	p, err := w.pay.Pay(context.Background(), w.cust, "CHEQUE", "",
		[]payments.Allocation{{InvoiceID: w.invA, AmountCents: 15000}, {InvoiceID: w.invB, AmountCents: 5000}})
	if err != nil {
		t.Fatalf("the receipt door: %v", err)
	}
	return p
}

// lines reads the plain statement, fails if any line leaks, and returns the lines. It also ties the closing
// figure to the customer's open invoice balances, so a change of words never moves a figure.
func (w *p063World) lines(t *testing.T, label string, fees ...int64) []billing.StatementLine {
	t.Helper()
	ctx := context.Background()
	ls, err := w.bill.StatementLines(ctx, w.cust, time.Now().Add(time.Hour), billing.BranchReach{Global: true})
	if err != nil {
		t.Fatalf("%s: statement lines: %v", label, err)
	}
	for _, l := range ls {
		for _, bad := range []string{"P063LEAK", "77777", "INVSECRET", "keyed", "mistake", "NSF reversal", "voided receipt", "reversal:", "re-applied", "credit applied to", "credit memo applied", "credit application reversed", "credit memo restored"} {
			if strings.Contains(l.Description, bad) || strings.Contains(l.DocNo, "INVSECRET") {
				t.Errorf("%s: a plain statement line carries %q: %q (doc %q)", label, bad, l.Description, l.DocNo)
			}
		}
		if p063UUID.MatchString(l.Description) {
			t.Errorf("%s: a plain statement line carries a uuid: %q", label, l.Description)
		}
		if strings.HasPrefix(l.Description, "Payment received") && l.ChargeCents > 0 {
			t.Errorf("%s: a charge line fell back to the receipt words: %q", label, l.Description)
		}
	}
	var open int64
	if err := w.pool.QueryRow(ctx, `SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1::uuid`, w.cust).Scan(&open); err != nil {
		t.Fatalf("%s: open balances: %v", label, err)
	}
	for _, f := range fees { // an NSF fee is a ledger charge that is not an invoice's balance
		open += f
	}
	if len(ls) == 0 || ls[len(ls)-1].BalanceRun != open {
		t.Errorf("%s: closing %v does not tie to the open invoice balances %d", label, ls, open)
	}
	return ls
}

func p063Has(t *testing.T, label string, ls []billing.StatementLine, want string) {
	t.Helper()
	for _, l := range ls {
		if l.Description == want {
			return
		}
	}
	t.Errorf("%s: no line reads %q in %+v", label, want, ls)
}

// staffMemo is what the customer AR read (staff) shows for the customer: the full memo survives.
func (w *p063World) staffKeepsReason(t *testing.T, label string) {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND memo LIKE '%P063LEAK%'`, w.cust).Scan(&n); err != nil {
		t.Fatalf("%s: read the ledger: %v", label, err)
	}
	if n == 0 {
		t.Errorf("%s: the ledger no longer keeps the clerk's reason", label)
	}
}

func (w *p063World) bounce(t *testing.T, payID string) {
	t.Helper()
	var userID string
	if err := w.pool.QueryRow(context.Background(), `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("pick a seeded user: %v", err)
	}
	ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
		UserID: userID, Tier: staffcontext.TierManager, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE",
	})
	if _, err := till.NewService(till.NewRepository(w.pool)).MarkNSF(ctx, till.NSFInput{PaymentID: payID, Reason: p063Reason}); err != nil {
		t.Fatalf("the NSF door: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM credit_holds WHERE source_payment_id = $1::uuid`,
			`DELETE FROM payment_nsf WHERE payment_id = $1::uuid`,
		} {
			if _, err := w.pool.Exec(context.Background(), q, payID); err != nil {
				t.Errorf("cleanup of the bounce: %v", err)
			}
		}
	})
}

func (w *p063World) memo(t *testing.T, invoiceID string, cents int64, key string) string {
	t.Helper()
	svc, _ := cmRoundService(t, w.pool)
	var userID string
	if err := w.pool.QueryRow(context.Background(), `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("pick a seeded user: %v", err)
	}
	res, err := svc.Issue(staffcontext.ContextWithStaffUserID(context.Background(), userID), billing.CreditMemoRouteRequest{
		CustomerID: w.cust, BranchID: trentonBranchID, InvoiceID: invoiceID, AmountCents: cents,
		Reason: p063Reason, IdempotencyKey: key,
	}, billing.CreditMemoCaller{OrgID: cmRoundNorthOrg, Scope: staffcontext.ScopeGlobal})
	if err != nil {
		t.Fatalf("the credit memo door: %v", err)
	}
	t.Cleanup(func() {
		p063Exec(t, w.pool, "memo journals", `DELETE FROM gl_journals WHERE idempotency_key = $1`, billing.CreditMemoJournalKeyPrefix+res.CreditMemoID)
	})
	return res.CreditMemoID
}

func TestIntegration_P063_PlainStatementWords(t *testing.T) {
	pool := p063Pool(t, "p063words")
	ctx := context.Background()

	t.Run("Void", func(t *testing.T) {
		w := p063Plant(t, pool)
		p := w.receipt(t)
		if _, err := w.pay.VoidPayment(ctx, payments.VoidInput{PaymentID: p.ID, Reason: p063Reason, IsManager: true}, time.Now(), houseday.DefaultZone()); err != nil {
			t.Fatalf("the void door: %v", err)
		}
		ls := w.lines(t, "void")
		p063Has(t, "void", ls, "Payment voided")
		w.staffKeepsReason(t, "void")
	})

	t.Run("ReverseThenReapply", func(t *testing.T) {
		w := p063Plant(t, pool)
		p := w.receipt(t)
		if _, err := w.pay.ReversePayment(ctx, payments.ReverseInput{PaymentID: p.ID, Reason: p063Reason, IsManager: true}, time.Now()); err != nil {
			t.Fatalf("the reversal door: %v", err)
		}
		ls := w.lines(t, "reversal")
		p063Has(t, "reversal", ls, "Payment reversed")
		if _, err := w.pay.ReapplyHeld(ctx, payments.ReapplyInput{
			PaymentID: p.ID, Reason: p063Reason, IdempotencyKey: "p063-reapply-" + w.cust, IsManager: true,
			Allocations: []payments.Allocation{{InvoiceID: w.invA, AmountCents: 15000}},
		}, time.Now()); err != nil {
			t.Fatalf("the reapply door: %v", err)
		}
		ls = w.lines(t, "reapply")
		p063Has(t, "reapply", ls, "Payment applied")
		w.staffKeepsReason(t, "reversal")
	})

	t.Run("NSF", func(t *testing.T) {
		w := p063Plant(t, pool)
		p := w.receipt(t)
		w.bounce(t, p.ID)
		ls := w.lines(t, "nsf", 4500)
		p063Has(t, "nsf", ls, "Returned payment")
	})

	t.Run("CreditApplyAndReverse", func(t *testing.T) {
		w := p063Plant(t, pool)
		memoID := w.memo(t, w.invA, 4000, "p063-memo-"+w.cust)
		ls := w.lines(t, "credit memo")
		p063Has(t, "credit memo", ls, ls[len(ls)-1].Description) // the memo line is the last; checked for leaks above
		app, err := w.bill.ApplyCredit(ctx, billing.ApplyCreditInput{
			CustomerID: w.cust, CreditKind: billing.CreditKindMemo, CreditID: memoID, InvoiceID: w.invB,
			AmountCents: 4000, Reason: p063Reason, IdempotencyKey: "p063-apply-" + w.cust, OrgID: cmRoundNorthOrg,
		}, time.Now())
		if err != nil {
			t.Fatalf("the credit apply door: %v", err)
		}
		ls = w.lines(t, "credit applied")
		p063Has(t, "credit applied", ls, "Credit applied")
		p063Has(t, "credit applied", ls, "Credit memo applied")
		if _, err := w.bill.ReverseCreditApplication(ctx, billing.ReverseCreditApplicationInput{
			InvoiceID: w.invB, ApplicationID: app.ApplicationID, Reason: p063Reason, OrgID: cmRoundNorthOrg,
		}, time.Now()); err != nil {
			t.Fatalf("the credit reverse door: %v", err)
		}
		ls = w.lines(t, "credit reversed")
		p063Has(t, "credit reversed", ls, "Credit application reversed")
		p063Has(t, "credit reversed", ls, "Credit memo restored")
		w.staffKeepsReason(t, "credit")
	})
}
