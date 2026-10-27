//go:build integration

// write_terms_integration_test.go, PR 608's P1 (FROM-PRINCIPAL 95): the payment terms a customer
// create carries must be WHAT THE CALLER SENT. A picker shaped create (the Counter Till's and the
// Quote Desk's both post account_no, name and customer_type and nothing else) stores the column's
// own 30 day default, never 0: with terms 0 now read as Cash On Demand at the invoice writer, a
// create-by-omission would make every counter made account Cash On Demand without anyone choosing
// it, and its invoices due on issue.
//
// The file pins the two ends of that road. This package owns the create: a picker shaped create
// stores 30, an explicit 0 is kept. The invoice end (the writer's terms 0 reads as Cash On Demand)
// is the round 1 test this branch already carries, invoice_house_day_integration_test.go. The
// join between them, orderBillingHeaderOn reading the created row verbatim, is order module SQL
// over this table with no customer create in it, so the two ends together hold the whole road.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/customer/... -run TestIntegration_CustomerWriteTerms
package customer

import (
	"context"
	"testing"
)

func TestIntegration_CustomerWriteTerms_NilMeansThirty(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	// THE PICKER'S SHAPE: account_no, name, customer_type. No payment_terms_days.
	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo:    "TERMS-NIL-" + randSuffix(),
		Name:         "Picker Created Co",
		CustomerType: "CONTRACTOR",
	})
	if err != nil {
		t.Fatalf("create the customer the picker's way: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, acct.CustomerID)
	})

	if acct.PaymentTermsDays != 30 {
		t.Fatalf("a create with no terms stores the 30 day default: got %d", acct.PaymentTermsDays)
	}
	var stored int
	if err := pool.QueryRow(ctx,
		`SELECT payment_terms_days FROM customers WHERE id = $1`, acct.CustomerID).Scan(&stored); err != nil {
		t.Fatalf("read the created account's terms: %v", err)
	}
	if stored != 30 {
		t.Fatalf("the stored terms are the 30 day default, not 0: got %d", stored)
	}
}

func TestIntegration_CustomerWriteTerms_ZeroIsKept(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	zero := 0
	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo:        "TERMS-ZERO-" + randSuffix(),
		Name:             "Cash On Demand By Choice",
		CustomerType:     "CONTRACTOR",
		PaymentTermsDays: &zero,
	})
	if err != nil {
		t.Fatalf("create an explicit Cash On Demand account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, acct.CustomerID)
	})
	var stored int
	if err := pool.QueryRow(ctx,
		`SELECT payment_terms_days FROM customers WHERE id = $1`, acct.CustomerID).Scan(&stored); err != nil {
		t.Fatalf("read the created account's terms: %v", err)
	}
	if stored != 0 {
		t.Fatalf("an explicit 0 is Cash On Demand by choice and is kept: got %d", stored)
	}
}
