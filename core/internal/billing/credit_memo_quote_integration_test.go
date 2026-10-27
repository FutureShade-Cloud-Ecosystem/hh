//go:build integration

package billing

// credit_memo_quote_integration_test.go: THE QUOTE DOOR (FW1-12 round 3).
//
// The desk used to show the invoice's BALANCE as the cap on a credit. The balance is what is owed,
// with tax and after payments; the core takes a PRE TAX amount, capped by what the invoice has left
// to credit. So the cap on the desk was a confident wrong number, and on a paid invoice it was
// $0.00 while the invoice still had room.
//
// The quote door answers the room: what the invoice has left to credit, before tax and with it, and
// what the amount comes to. These legs are over the REAL seams and the REAL plan, so they say what
// the core actually does.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestIntegration_CreditMemoQuote_TheRoomIsTheRemainingSubtotalBeforeTaxAndWithTax is the room line:
// the room is the invoice's own subtotal (what has not been credited yet), and the room with tax.
func TestIntegration_CreditMemoQuote_TheRoomIsTheRemainingSubtotalBeforeTaxAndWithTax(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, trentonBranch)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})
	cmStampInvoiceOrg(t, pool, inv)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = trentonBranch

	quote, err := svc.Quote(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 10_000,
	}, cmTrentonOnlyCaller())
	if err != nil {
		t.Fatalf("quote the credit: %v", err)
	}
	if quote.RemainingSubtotalCents != 100_000 {
		t.Fatalf("the room before tax is %d, want the invoice's remaining subtotal 100000", quote.RemainingSubtotalCents)
	}
	if quote.RemainingTaxCents != 13_000 {
		t.Fatalf("the room's tax is %d, want 13000", quote.RemainingTaxCents)
	}
	if quote.RemainingTotalCents != 113_000 {
		t.Fatalf("the room with tax is %d, want 113000", quote.RemainingTotalCents)
	}
	// The credit: the amount, and the tax pro-rated from what is left.
	if quote.SubtotalCents != 10_000 {
		t.Fatalf("the credit is %d before tax, want the amount typed 10000", quote.SubtotalCents)
	}
	if quote.TotalCents != quote.SubtotalCents+quote.TaxCents {
		t.Fatalf("the credit with tax is %d, want %d (the subtotal plus the tax)", quote.TotalCents, quote.SubtotalCents+quote.TaxCents)
	}
	if quote.TaxCents <= 0 {
		t.Fatalf("the credit carries no tax (%d): a partial credit's tax is pro-rated from what is left", quote.TaxCents)
	}
	if !strings.Contains(quote.Message, "$100.00") {
		t.Fatalf("the quote's words do not name the amount: %q", quote.Message)
	}
}

// TestIntegration_CreditMemoQuote_APaidInvoiceStillHasItsRoom is the paid line: what has been PAID is
// not what has been CREDITED, so a paid invoice still has a room, and the room is not $0.00.
func TestIntegration_CreditMemoQuote_APaidInvoiceStillHasItsRoom(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, trentonBranch)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 0, finalized: true,
	})
	cmStampInvoiceOrg(t, pool, inv)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = trentonBranch

	quote, err := svc.Quote(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 10_000,
	}, cmTrentonOnlyCaller())
	if err != nil {
		t.Fatalf("quote the credit of a paid invoice: %v", err)
	}
	if quote.RemainingSubtotalCents != 100_000 {
		t.Fatalf("the room of a paid invoice is %d before tax, want 100000, not the $0.00 balance", quote.RemainingSubtotalCents)
	}
	if quote.RemainingTotalCents != 113_000 {
		t.Fatalf("the room of a paid invoice is %d with tax, want 113000", quote.RemainingTotalCents)
	}
}

// TestIntegration_CreditMemoQuote_AnAmountOverTheRoomIsRefusedInThePlansOwnWords is the cap line: the
// quote never invents a cap, and an amount over the room is refused in the same words the credit
// itself is refused in, which name both figures.
func TestIntegration_CreditMemoQuote_AnAmountOverTheRoomIsRefusedInThePlansOwnWords(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, trentonBranch)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})
	cmStampInvoiceOrg(t, pool, inv)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = trentonBranch

	_, err := svc.Quote(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 200_000,
	}, cmTrentonOnlyCaller())
	if err == nil {
		t.Fatal("fw1-12a r3 red: an amount over the room was quoted")
	}
	var bad interface{ Error() string }
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal is not an error: %v", err)
	}
	words := err.Error()
	if !strings.Contains(words, "has left to credit") {
		t.Fatalf("the refusal does not name the cap: %q", words)
	}
	if !strings.Contains(words, "$1,000.00") || !strings.Contains(words, "$1,130.00") {
		t.Fatalf("the refusal does not name the room before tax and with tax: %q", words)
	}
}

// TestIntegration_CreditMemoQuote_TheQuoteIsHeldToTheSameCustomerWall is the fence line: the quote is
// the credit's own fence, so an account the caller cannot reach answers as not found, exactly as it
// does on the credit door.
func TestIntegration_CreditMemoQuote_TheQuoteIsHeldToTheSameCustomerWall(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, kingstonBranch)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})
	cmStampInvoiceOrg(t, pool, inv)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = kingstonBranch

	_, err := svc.Quote(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 10_000,
	}, cmTrentonOnlyCaller())
	if err == nil {
		t.Fatal("fw1-12a r3 red: the quote of a customer the caller cannot reach was answered")
	}
	if !cmHouseAnswerIs(err) {
		t.Fatalf("fw1-12a r3 red: the quote answered %q, want the house's %q", err.Error(), "customer not found")
	}
}
