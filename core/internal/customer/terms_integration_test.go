//go:build integration

package customer

// terms_integration_test.go, the TERMS MASTER legs (wave 3, TM-1).
//
//  · a customer whose old terms_days is 30 reads as NET30 (red leg 2)
//  · a customer on zero days reads as RECEIPT
//  · a customer whose days name no standard term keeps its own days as a custom net, in words
//  · a customer created on the days is stored with the code, and the master row is there
//  · the seeded master and the Go mapping agree
//  · a customer put on a code of the master carries that code and the term's own net days

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// termsDays is the pointer the create payload takes.
func termsDays(d int) *int { return &d }

// tm1Nonce is 8 hex characters of entropy with no clock in it, so this file's accounts stay off
// every other fixture's rows on the shared gate database.
func tm1Nonce() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fixed"
	}
	return hex.EncodeToString(b[:])
}

// tm1AccountNo is one account number no other run has used.
func tm1AccountNo(prefix string) string { return prefix + "-" + tm1Nonce() }

// tm1Cleanup removes the account a test created, so the shared gate database keeps only the rows
// a failing test left behind.
func tm1Cleanup(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, id)
	})
}

func TestIntegration_TermsFor_TheOldTermsDaysReadAsTheNetCode(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	// The customer the ERP knew before the master: thirty days on the account, no code.
	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: tm1AccountNo("TM1-NET30"), Name: "Net Thirty Yard", PaymentTermsDays: termsDays(30),
	})
	if err != nil {
		t.Fatalf("create the customer on thirty days: %v", err)
	}
	tm1Cleanup(t, pool, acct.CustomerID)
	if acct.PaymentTermsDays != 30 {
		t.Fatalf("the created customer carries %d days, want 30", acct.PaymentTermsDays)
	}

	terms, err := svc.TermsFor(ctx, acct.CustomerID)
	if err != nil {
		t.Fatalf("read the terms of the customer on thirty days: %v", err)
	}
	if terms.Code != TermsNet30 {
		t.Fatalf("a customer on thirty days reads as %q, want %q", terms.Code, TermsNet30)
	}
	if terms.NetDays != 30 {
		t.Fatalf("%s carries %d net days, want 30", terms.Code, terms.NetDays)
	}
	if terms.Name != "Net 30" {
		t.Fatalf("%s is named %q, want %q", terms.Code, terms.Name, "Net 30")
	}
	if terms.DiscountBps != 0 || terms.DiscountDays != 0 {
		t.Fatalf("%s carries a discount %dbps over %d days, want none", terms.Code, terms.DiscountBps, terms.DiscountDays)
	}
}

func TestIntegration_TermsFor_CashOnDemandIsReceipt(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: tm1AccountNo("TM1-RECEIPT"), Name: "Paid At The Counter", PaymentTermsDays: termsDays(0),
	})
	if err != nil {
		t.Fatalf("create the customer on zero days: %v", err)
	}
	tm1Cleanup(t, pool, acct.CustomerID)
	terms, err := svc.TermsFor(ctx, acct.CustomerID)
	if err != nil {
		t.Fatalf("read the terms of the customer on zero days: %v", err)
	}
	if terms.Code != TermsReceipt {
		t.Fatalf("a customer on zero days reads as %q, want %q", terms.Code, TermsReceipt)
	}
	if terms.NetDays != 0 {
		t.Fatalf("%s carries %d net days, want 0 (due on receipt)", terms.Code, terms.NetDays)
	}
}

func TestIntegration_TermsFor_DaysThatNameNoTermStayACustomNet(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	// Sixty days names no standard term. The customer keeps its own days, named in words, and
	// is never put on some other term's code.
	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: tm1AccountNo("TM1-NET60"), Name: "Sixty Day Yard", PaymentTermsDays: termsDays(60),
	})
	if err != nil {
		t.Fatalf("create the customer on sixty days: %v", err)
	}
	tm1Cleanup(t, pool, acct.CustomerID)
	terms, err := svc.TermsFor(ctx, acct.CustomerID)
	if err != nil {
		t.Fatalf("read the terms of the customer on sixty days: %v", err)
	}
	if terms.Code == TermsNet30 || terms.Code == TermsNet45 {
		t.Fatalf("a customer on sixty days was put on %q, a term that is not its own", terms.Code)
	}
	if terms.Code != CustomTermsCode(60) {
		t.Fatalf("a customer on sixty days reads as %q, want %q", terms.Code, CustomTermsCode(60))
	}
	if terms.NetDays != 60 {
		t.Fatalf("%s carries %d net days, want 60 (its own)", terms.Code, terms.NetDays)
	}
	if terms.Name != "Net 60" {
		t.Fatalf("%s is named %q, want %q", terms.Code, terms.Name, "Net 60")
	}

	// The custom net is a row in the master: the code a customer carries names a term that
	// exists.
	var name string
	var netDays int
	if err := pool.QueryRow(ctx, `SELECT name, net_days FROM payment_terms WHERE code = $1`,
		CustomTermsCode(60)).Scan(&name, &netDays); err != nil {
		t.Fatalf("the custom net is not a row of the master: %v", err)
	}
	if name != "Net 60" || netDays != 60 {
		t.Fatalf("the custom net row is %q over %d days, want %q over 60", name, netDays, "Net 60")
	}
}

func TestIntegration_TermsFor_TheCreatedCustomerCarriesItsCode(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: tm1AccountNo("TM1-CODED"), Name: "Coded Yard", PaymentTermsDays: termsDays(45),
	})
	if err != nil {
		t.Fatalf("create the customer on forty five days: %v", err)
	}
	tm1Cleanup(t, pool, acct.CustomerID)
	var code string
	if err := pool.QueryRow(ctx, `SELECT terms_code FROM customers WHERE id = $1`,
		acct.CustomerID).Scan(&code); err != nil {
		t.Fatalf("read the stored terms code: %v", err)
	}
	if code != TermsNet45 {
		t.Fatalf("the stored terms code is %q, want %q", code, TermsNet45)
	}
}

func TestIntegration_SetTerms_PutsTheCustomerOnTheCode(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: tm1AccountNo("TM1-DISCOUNT"), Name: "Discount Yard", PaymentTermsDays: termsDays(30),
	})
	if err != nil {
		t.Fatalf("create the customer on thirty days: %v", err)
	}
	tm1Cleanup(t, pool, acct.CustomerID)
	if _, err := svc.SetTerms(ctx, acct.CustomerID, Terms2PctNet30); err != nil {
		t.Fatalf("put the customer on 2/10 net 30: %v", err)
	}
	terms, err := svc.TermsFor(ctx, acct.CustomerID)
	if err != nil {
		t.Fatalf("read the terms of the customer on 2/10 net 30: %v", err)
	}
	if terms.Code != Terms2PctNet30 {
		t.Fatalf("the customer reads as %q, want %q", terms.Code, Terms2PctNet30)
	}
	if terms.NetDays != 30 {
		t.Fatalf("%s carries %d net days, want 30", terms.Code, terms.NetDays)
	}
	if terms.DiscountBps != 200 || terms.DiscountDays != 10 {
		t.Fatalf("%s carries %dbps over %d days, want 200bps over 10 days", terms.Code, terms.DiscountBps, terms.DiscountDays)
	}

	// A code nobody can read is refused in words, never stored.
	if _, err := svc.SetTerms(ctx, acct.CustomerID, "NET-NOT-A-TERM"); err == nil {
		t.Fatal("a customer was put on a code that names no term")
	}
}

func TestIntegration_TermsFor_TheSeededMasterAndTheMappingAgree(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	// Every standard term the master is seeded with is in the Go mapping, with the same name and
	// the same days: the migration's seed and the live rule cannot drift apart.
	for _, want := range standardTerms {
		got, err := svc.TermsByCode(ctx, want.Code)
		if err != nil {
			t.Fatalf("read the seeded term %s: %v", want.Code, err)
		}
		if got.Name != want.Name {
			t.Fatalf("the master names %s %q, the mapping names it %q", want.Code, got.Name, want.Name)
		}
		if got.NetDays != want.Days {
			t.Fatalf("the master gives %s %d net days, the mapping gives it %d", want.Code, got.NetDays, want.Days)
		}
	}
	// The days a customer carries name the term for those days.
	for _, want := range standardTerms {
		if want.Code == Terms1PctNet30 || want.Code == Terms2PctNet30 {
			continue // the two discount terms share net 30 with NET30 and are reached by their code
		}
		code, _ := TermsForDays(want.Days)
		if code != want.Code {
			t.Fatalf("%d days name %q, want %q", want.Days, code, want.Code)
		}
	}
}

// TestIntegration_TermsFor_TheLoadDoorWritesTheCodeBesideTheDays is the leg that keeps the two
// in step: the load door writes a customer's payment terms days, and the code that names those
// days goes beside them, so a customer whose code and days disagreed could be dated by one door
// and not the other.
func TestIntegration_TermsFor_TheLoadDoorWritesTheCodeBesideTheDays(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	// The staff write, the way the account is made before the loader ever sees it: a customer on
	// forty five days, carrying NET45.
	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: tm1AccountNo("TM1-LOADED"), Name: "Loaded Yard", PaymentTermsDays: termsDays(45),
	})
	if err != nil {
		t.Fatalf("create the customer on forty five days: %v", err)
	}
	tm1Cleanup(t, pool, acct.CustomerID)

	// The loader's write: the same account, the days the record carries. The loader writes on
	// the caller's transaction, the way the load door's frame hands it one.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the loader's transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := (CustomerLoadWriter{}).Apply(ctx, tx, load.Write{
		LegacyID: acct.AccountNo,
		OrgID:    "4442a800-0000-4000-8000-00000000e001",
		Record: map[string]any{
			"account_no": acct.AccountNo, "name": "Loaded Yard", "payment_terms_days": 45,
		},
	}); err != nil {
		t.Fatalf("the load door writes the customer: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the loader's transaction: %v", err)
	}
	var code *string
	var days int
	if err := pool.QueryRow(ctx, `SELECT terms_code, payment_terms_days FROM customers WHERE id = $1`,
		acct.CustomerID).Scan(&code, &days); err != nil {
		t.Fatalf("read the loaded customer: %v", err)
	}
	if code == nil || *code != TermsNet45 {
		t.Fatalf("the loaded customer carries the code %v, want %q", code, TermsNet45)
	}
	if days != 45 {
		t.Fatalf("the loaded customer carries %d days, want 45", days)
	}
}
