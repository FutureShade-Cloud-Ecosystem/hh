//go:build integration

// dib483_fulfillment_integration_test.go, M3-G12-WILLCALL-AT-CONVERT against a LIVE Postgres.
//
// The unit tests pin the SQL the splice builds. This file pins what the DATABASE ends up holding,
// which is the fact dispatch actually reads: dispatch.MintWillCall refuses any order whose
// orders.fulfillment is not 'WILL_CALL' (dispatch/willcall.go:84). Before DIB-483 a converted quote
// could never satisfy that check, because the conversion never wrote the column and 0008's default is
// 'PICKUP', so the will-call rack tag was unreachable from the only path a quote takes to an order.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/quote/... -run DIB483 -count=1
package quote

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fulfillmentOf reads the column dispatch gates on.
func fulfillmentOf(t *testing.T, pool *pgxpool.Pool, orderID string) string {
	t.Helper()
	var f string
	if err := pool.QueryRow(context.Background(),
		`SELECT fulfillment FROM orders WHERE id = $1`, orderID).Scan(&f); err != nil {
		t.Fatalf("read fulfillment: %v", err)
	}
	return f
}

// TestIntegration_DIB483_UnspecifiedFulfillmentIsUnchanged is the ZERO-DELTA acceptance at the
// database. A conversion that chooses nothing must produce the row it always produced, PICKUP, from
// the column default, with no help from this feature.
//
// This is the test that dies if someone "tidies" ConvertOptions by defaulting Fulfillment to PICKUP in
// Go: the row would still say PICKUP, so a value assertion alone would pass, which is why the unit
// half asserts the SQL is byte-identical and this half asserts the outcome. Together they pin both
// the statement and the result.
func TestIntegration_DIB483_UnspecifiedFulfillmentIsUnchanged(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)

	res, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got := fulfillmentOf(t, pool, res.OrderID); got != "PICKUP" {
		t.Fatalf("an unspecified conversion wrote fulfillment %q, want PICKUP from the 0008 column "+
			"default, the conversion is no longer byte-identical when the caller says nothing", got)
	}
}

// TestIntegration_DIB483_WillCallAtConvertReachesTheColumn is the feature. A conversion that chooses
// WILL_CALL produces an order dispatch will mint a rack tag for.
func TestIntegration_DIB483_WillCallAtConvertReachesTheColumn(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)

	res, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID,
		ConvertOptions{Fulfillment: FulfillWillCall})
	if err != nil {
		t.Fatalf("will-call convert: %v", err)
	}
	if got := fulfillmentOf(t, pool, res.OrderID); got != "WILL_CALL" {
		t.Fatalf("fulfillment = %q, want WILL_CALL, dispatch.MintWillCall refuses anything else", got)
	}
	// the rest of the conversion is untouched by the choice: the quote is CONVERTED and exactly one
	// order exists. A fulfilment flag must not become a second way to change the transaction.
	if s := quoteStatus(t, pool, quoteID); s != "CONVERTED" {
		t.Fatalf("quote status = %q, want CONVERTED", s)
	}
	if n := ordersForQuote(t, pool, quoteID); n != 1 {
		t.Fatalf("orders for quote = %d, want 1", n)
	}
	if !strings.HasPrefix(res.OrderNo, "#SO-") {
		t.Fatalf("order_no %q lost its branch-coded prefix", res.OrderNo)
	}
}

// TestIntegration_DIB483_ExplicitPickupIsAlsoWritten: choosing PICKUP explicitly must land the same
// value the default would have. It is the boring case, and it is the one that proves the splice is not
// quietly inverted.
func TestIntegration_DIB483_ExplicitPickupIsAlsoWritten(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)

	res, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID,
		ConvertOptions{Fulfillment: FulfillPickup})
	if err != nil {
		t.Fatalf("pickup convert: %v", err)
	}
	if got := fulfillmentOf(t, pool, res.OrderID); got != "PICKUP" {
		t.Fatalf("explicit PICKUP wrote %q", got)
	}
}

// TestIntegration_DIB483_CreditRefusalStillWritesNothingWithAFulfillmentChoice: the choice must not
// open a path around the credit gate. A blocked conversion that asked for WILL_CALL leaves the
// database exactly as it found it, same as DIB-387's acceptance, now with the new field in play.
func TestIntegration_DIB483_CreditRefusalStillWritesNothingWithAFulfillmentChoice(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)
	placeHold(t, pool, cust)

	_, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID,
		ConvertOptions{Fulfillment: FulfillWillCall})
	if err == nil {
		t.Fatal("a held customer converted to a will-call order, the fulfilment choice bypassed the credit gate")
	}
	var blocked ErrCreditBlocked
	if !asCreditBlocked(err, &blocked) {
		t.Fatalf("refusal was %v, want ErrCreditBlocked carrying its policy", err)
	}
	if n := ordersForQuote(t, pool, quoteID); n != 0 {
		t.Fatalf("a refused will-call conversion still wrote %d order(s)", n)
	}
	if s := quoteStatus(t, pool, quoteID); s != "ACCEPTED" {
		t.Fatalf("quote status = %q after a refusal, want ACCEPTED", s)
	}
	// and the refusal SAYS why, in the same words the counter and the portal both render.
	if msg := CreditRefusalMessage(blocked); strings.Contains(msg, "invalid state transition") {
		t.Fatalf("the refusal degraded to the generic message: %q", msg)
	}
}

// asCreditBlocked is errors.As with the concrete type spelled out, so the assertion above reads as
// "the refusal is a credit block carrying a verdict" rather than as generic error plumbing.
func asCreditBlocked(err error, target *ErrCreditBlocked) bool { return errors.As(err, target) }
