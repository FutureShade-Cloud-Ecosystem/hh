//go:build integration

// terms_wire_integration_test.go, the whole road of the terms master (wave 3, TM-1).
//
// The customer module owns the master's table and reads it; the billing module dates the
// document by the terms and never touches the table. Nothing in between is a hand-off of
// numbers: the composition root binds the customer module's terms read into the billing
// writer's terms seam, and the invoice that comes out is dated by the terms the customer is
// sold on.
//
// The two ends are proved apart as well: the master and the mapping in the customer module, the
// writer's own dating in the billing module, and this one that holds the whole road together.
//
//	Run: DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/hardscapeos?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -count=1 -run TestIntegration_TheInvoiceIsDatedByTheCustomersTerms
package main

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// tm1Branch is the yard's own branch (migration 0003).
const tm1Branch = "5d1b3f6e-0000-4000-9000-000000000001"

// tm1OrderSource answers the order-facts seam with one fixed billable order, so the terms read
// and the writer's dating are the only things under test.
type tm1OrderSource struct{ customer string }

func (s tm1OrderSource) InvoiceableOrder(context.Context, string, []string) (billing.OrderInvoiceFacts, error) {
	return billing.OrderInvoiceFacts{
		CustomerID: s.customer, BranchID: tm1Branch, ClosesOrder: true, TermsDays: 30,
		Lines: []billing.OrderInvoiceLine{{
			LineNo: 1, Description: "Pavers", QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: 5_000, PriceUOM: "EA", LineTotalCents: 5_000,
		}},
	}, nil
}

func (tm1OrderSource) MarkGroupsInvoiced(context.Context, []string, string, time.Time) error {
	return nil
}

// TestIntegration_TheInvoiceIsDatedByTheCustomersTerms holds the whole road: a customer put on
// 2/10 net 30 in the customer module, an invoice raised for that customer through the production
// writer, and the document dated by the term the customer is sold on.
func TestIntegration_TheInvoiceIsDatedByTheCustomersTerms(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "s4fin2tm1")

	repo := customer.NewRepository(pool)
	svc := customer.NewService(repo).WithWriter(repo)
	acct, err := svc.CreateCustomer(ctx, customer.CreateInput{AccountNo: "TM1-ROAD", Name: "The Whole Road"})
	if err != nil {
		t.Fatalf("create the customer: %v", err)
	}
	if _, err := svc.SetTerms(ctx, acct.CustomerID, customer.Terms2PctNet30); err != nil {
		t.Fatalf("put the customer on 2/10 net 30: %v", err)
	}

	// The production writer: the composition root's own builder, with the terms seam bound to
	// the customer module's master.
	at := time.Date(2026, 1, 5, 2, 30, 0, 0, time.UTC)
	writer := newInvoiceWriter(pool).
		WithOrderSource(tm1OrderSource{customer: acct.CustomerID}).
		WithClock(func() time.Time { return at })
	doc, err := writer.InvoiceOrder(ctx, "")
	if err != nil {
		t.Fatalf("raise the invoice for the customer on 2/10 net 30: %v", err)
	}

	var issue, due string
	var discount *string
	var code *string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text, discount_date::text, terms_code FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due, &discount, &code); err != nil {
		t.Fatalf("read the raised invoice: %v", err)
	}
	// The clock is at 21:30 in the yard's own house time on the fourth; the UTC day is the fifth.
	if issue != "2026-01-04" {
		t.Fatalf("the invoice is dated the house day: got %s, want 2026-01-04", issue)
	}
	// The term's own net days, counted from that same day.
	if due != "2026-02-03" {
		t.Fatalf("the invoice is due the term's 30 house days later: got %s, want 2026-02-03", due)
	}
	// The term's own discount window.
	if discount == nil || *discount != "2026-01-14" {
		t.Fatalf("the discount runs until the term's tenth house day: got %v, want 2026-01-14", discount)
	}
	if code == nil || *code != customer.Terms2PctNet30 {
		t.Fatalf("the invoice carries the code it was dated by: got %v, want %s", code, customer.Terms2PctNet30)
	}
}

// TestIntegration_Cust1008_InvoicesOnFreshSeededDBAndWithNoCode proves S2's case: on a fresh
// migrated and seeded database, invoicing CUST-1008 (60 days) through the production writer and
// the real terms read succeeds and is due in 60 house days, and when the customer carries no
// terms code on non-standard days, TermsFor answers the days with no code and the invoice writer
// writes a NULL terms_code.
func TestIntegration_Cust1008_InvoicesOnFreshSeededDBAndWithNoCode(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "s4fin2tm1c1008")
	seedBody, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("read dibbits_seed.sql: %v", err)
	}
	if _, err := pool.Exec(ctx, string(seedBody)); err != nil {
		t.Fatalf("apply dibbits_seed.sql: %v", err)
	}

	const cust1008 = "c7000000-0000-4000-9000-000000000008"
	at := time.Date(2026, 1, 5, 2, 30, 0, 0, time.UTC)
	writer := newInvoiceWriter(pool).
		WithOrderSource(tm1OrderSource{customer: cust1008}).
		WithClock(func() time.Time { return at })

	doc, err := writer.InvoiceOrder(ctx, "cust1008-seeded")
	if err != nil {
		t.Fatalf("raise the invoice for CUST-1008 on a fresh migrated and seeded database: %v", err)
	}
	var issue, due string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due); err != nil {
		t.Fatalf("read the raised invoice for CUST-1008: %v", err)
	}
	if issue != "2026-01-04" {
		t.Fatalf("the invoice is dated the house day: got %s, want 2026-01-04", issue)
	}
	if due != "2026-03-05" {
		t.Fatalf("CUST-1008 is due 60 house days later: got %s, want 2026-03-05", due)
	}

	// With no code on the customer and non-standard days (60), TermsFor answers the days with
	// no code and the invoice writer writes a NULL terms_code.
	if _, err := pool.Exec(ctx, `UPDATE customers SET terms_code = NULL WHERE id = $1::uuid`, cust1008); err != nil {
		t.Fatalf("clear CUST-1008 terms_code: %v", err)
	}
	repo := customer.NewRepository(pool)
	svc := customer.NewService(repo).WithWriter(repo)
	terms, err := svc.TermsFor(ctx, cust1008)
	if err != nil {
		t.Fatalf("read the terms of uncoded CUST-1008: %v", err)
	}
	if terms.Code != "" {
		t.Fatalf("an uncoded 60-day customer answers no code: got %q, want empty", terms.Code)
	}
	if terms.NetDays != 60 {
		t.Fatalf("an uncoded 60-day customer answers 60 net days: got %d, want 60", terms.NetDays)
	}

	uncodedDoc, err := writer.InvoiceOrder(ctx, "cust1008-uncoded")
	if err != nil {
		t.Fatalf("raise the invoice for uncoded CUST-1008: %v", err)
	}
	var uncodedDue string
	var uncodedCode *string
	if err := pool.QueryRow(ctx,
		`SELECT due_date::text, terms_code FROM invoices WHERE id = $1::uuid`,
		uncodedDoc.InvoiceID).Scan(&uncodedDue, &uncodedCode); err != nil {
		t.Fatalf("read the uncoded invoice for CUST-1008: %v", err)
	}
	if uncodedDue != "2026-03-05" {
		t.Fatalf("the uncoded 60-day invoice is due 60 house days later: got %s, want 2026-03-05", uncodedDue)
	}
	if uncodedCode != nil {
		t.Fatalf("the invoice writer writes a NULL terms_code when TermsFor answers no code: got %q", *uncodedCode)
	}
}

// TestIntegration_TheSeededCustomersCarryMasterTermsCodes proves that after migrate and seed,
// every customer with payment_terms_days carries a terms_code that names a row in payment_terms.
func TestIntegration_TheSeededCustomersCarryMasterTermsCodes(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "s4fin2tm1seed")
	seedBody, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("read dibbits_seed.sql: %v", err)
	}
	if _, err := pool.Exec(ctx, string(seedBody)); err != nil {
		t.Fatalf("apply dibbits_seed.sql: %v", err)
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM customers WHERE payment_terms_days IS NOT NULL`).Scan(&total); err != nil {
		t.Fatalf("count customers with days: %v", err)
	}
	if total == 0 {
		t.Fatal("migrated and seeded database has no customers with payment_terms_days")
	}

	rows, err := pool.Query(ctx, `
		SELECT c.account_no, c.payment_terms_days, COALESCE(c.terms_code, ''), COALESCE(t.code, ''), COALESCE(t.net_days, -1)
		  FROM customers c
		  LEFT JOIN payment_terms t ON t.code = c.terms_code
		 WHERE c.payment_terms_days IS NOT NULL
		   AND (c.terms_code IS NULL OR t.code IS NULL OR t.net_days <> c.payment_terms_days)
		 ORDER BY c.account_no`)
	if err != nil {
		t.Fatalf("query customers missing a master terms code: %v", err)
	}
	defer rows.Close()

	var bad []string
	for rows.Next() {
		var acct string
		var days, masterDays int
		var code, masterCode string
		if err := rows.Scan(&acct, &days, &code, &masterCode, &masterDays); err != nil {
			t.Fatalf("scan customer row: %v", err)
		}
		bad = append(bad, acct+"(days="+itoa(days)+",code="+code+",master="+masterCode+")")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate customer rows: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("seeded customers with days missing a valid master terms_code: %v", bad)
	}
}
