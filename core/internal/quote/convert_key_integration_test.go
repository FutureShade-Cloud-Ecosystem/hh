//go:build integration

// convert_key_integration_test.go, PR 296 round 3 (FROM-PRINCIPAL 121): the staff convert route takes an
// optional Idempotency-Key. A repeat under one key answers the FIRST order and writes nothing, whatever
// the dealer's multi accept switch says; the same key with another body is refused in words; without a
// key the switch's own behaviour stays as designed (a second order, DIB-491).
package quote

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	convKeyA = "5c1d2e3f-4a50-4b61-8c72-93a4b5c6d7e8"
	convKeyB = "6d2e3f40-5b61-4c72-9d83-a4b5c6d7e8f9"
)

// convertKeyFixture: an ACCEPTED quote whose customer can take the order, the multi accept switch as asked.
func convertKeyFixture(t *testing.T, multiAccept bool) (svc *StaffService, quoteID string, count func() int) {
	t.Helper()
	pool := intPool(t)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, multiAccept)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	var cust string
	if err := pool.QueryRow(context.Background(), `SELECT customer_id::text FROM quotes WHERE id=$1`, quoteID).Scan(&cust); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 1_000_00)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM quote_accept_provenance WHERE quote_id = $1`, quoteID)
	})
	if _, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true, true); err != nil {
		t.Fatalf("accept: %v", err)
	}
	return svc, quoteID, func() int { return len(familyOrders(t, pool, quoteID)) }
}

func TestIntegration_ConvertKey_RepeatAnswersFirstOrderWithMultiAcceptOn(t *testing.T) {
	svc, quoteID, orders := convertKeyFixture(t, true)
	ctx := qCtxAs(intStaff(intTrenton))
	first, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{IdempotencyKey: convKeyA})
	if err != nil {
		t.Fatalf("first convert: %v", err)
	}
	second, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{IdempotencyKey: convKeyA})
	if err != nil {
		t.Fatalf("repeat under the same key: %v", err)
	}
	if second.OrderID != first.OrderID || second.OrderNo != first.OrderNo {
		t.Fatalf("the repeat must answer the first order %+v, got %+v", first, second)
	}
	if n := orders(); n != 1 {
		t.Fatalf("two converts under one key made %d orders, want 1", n)
	}
	// Upper case is the same key.
	third, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{IdempotencyKey: strings.ToUpper(convKeyA)})
	if err != nil || third.OrderID != first.OrderID {
		t.Fatalf("the same key in upper case must replay: %+v, %v", third, err)
	}
}

func TestIntegration_ConvertKey_RepeatAnswersFirstOrderWithMultiAcceptOff(t *testing.T) {
	svc, quoteID, orders := convertKeyFixture(t, false)
	ctx := qCtxAs(intStaff(intTrenton))
	first, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{IdempotencyKey: convKeyA})
	if err != nil {
		t.Fatalf("first convert: %v", err)
	}
	second, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{IdempotencyKey: convKeyA})
	if err != nil || second.OrderID != first.OrderID {
		t.Fatalf("the repeat must answer the first order with the switch off too: %+v, %v", second, err)
	}
	if n := orders(); n != 1 {
		t.Fatalf("got %d orders, want 1", n)
	}
	// A different key on a converted quote is the switch's own business: refused when it is off.
	if _, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{IdempotencyKey: convKeyB}); err == nil {
		t.Fatal("a new key on a converted quote with the switch off must be refused")
	}
}

func TestIntegration_ConvertKey_NoKeyKeepsTheSwitchsBehaviour(t *testing.T) {
	svc, quoteID, orders := convertKeyFixture(t, true)
	ctx := qCtxAs(intStaff(intTrenton))
	for i := 0; i < 2; i++ {
		if _, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{}); err != nil {
			t.Fatalf("convert %d: %v", i+1, err)
		}
	}
	if n := orders(); n != 2 {
		t.Fatalf("with the switch on and no key a second convert mints a second order, got %d", n)
	}
}

func TestIntegration_ConvertKey_SameKeyOtherBodyIsRefusedInWords(t *testing.T) {
	svc, quoteID, orders := convertKeyFixture(t, true)
	ctx := qCtxAs(intStaff(intTrenton))
	if _, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{IdempotencyKey: convKeyA, Fulfillment: FulfillPickup}); err != nil {
		t.Fatalf("first convert: %v", err)
	}
	_, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{IdempotencyKey: convKeyA, Fulfillment: FulfillWillCall})
	if !errors.Is(err, ErrConvertKeyReused) {
		t.Fatalf("the same key with another fulfilment must be ErrConvertKeyReused, got %v", err)
	}
	if !strings.Contains(err.Error(), "different conversion") {
		t.Errorf("the refusal must say so in words: %q", err.Error())
	}
	if n := orders(); n != 1 {
		t.Fatalf("a refused reuse made %d orders, want 1", n)
	}
}

func TestIntegration_ConvertKey_NotAUUIDIsABadRequest(t *testing.T) {
	svc, quoteID, orders := convertKeyFixture(t, true)
	if _, err := svc.ConvertQuote(qCtxAs(intStaff(intTrenton)), quoteID, ConvertOptions{IdempotencyKey: "not-a-key"}); err == nil {
		t.Fatal("a key that is not a UUID must be refused")
	}
	if n := orders(); n != 0 {
		t.Fatalf("got %d orders, want 0", n)
	}
}
