//go:build integration

// credit_integration_test.go, DIB-387 against a LIVE Postgres: the quote→order conversion must consult
// the hold-aware credit gate BEFORE it writes an order, and a refusal must leave the database exactly as
// it found it (no orders row, no order_lines, the quote still ACCEPTED).
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/quote/... -run Convert -count=1
package quote

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

// gatedRepo is the production wiring under test: the real repository with the real hold-aware gate over
// the real till repository, the SAME composition cmd/server/main.go builds.
func gatedRepo(pool *pgxpool.Pool) *Repository {
	return NewRepository(pool).WithCreditGate(till.NewHoldAwareCreditGate(till.NewRepository(pool)))
}

// CARRY NOTE (HH-3.2 fold): setCreditLimit lives in the A4-carried
// credit_helpers_integration_test.go; the pin duplicate is dropped.

// placeHold writes the ACTIVE credit_holds row (0035) the gate reads, and cleans it up.
func placeHold(t *testing.T, pool *pgxpool.Pool, customerID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO credit_holds (customer_id, status, reason) VALUES ($1, 'ACTIVE', 'DIB-387 test')`,
		customerID); err != nil {
		t.Fatalf("place credit hold: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM credit_holds WHERE customer_id = $1`, customerID)
	})
}

// cleanupConvertedOrders lives in the helpers file (the fold dedup).

func ordersForQuote(t *testing.T, pool *pgxpool.Pool, quoteID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM orders WHERE quote_id = $1`, quoteID).Scan(&n); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	return n
}

func quoteStatus(t *testing.T, pool *pgxpool.Pool, quoteID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM quotes WHERE id = $1`, quoteID).Scan(&s); err != nil {
		t.Fatalf("read quote status: %v", err)
	}
	return s
}

// TestIntegration_ConvertRefusedOnActiveCreditHold is the DIB-387 acceptance: an ACCEPTED quote whose
// customer carries an ACTIVE credit hold must NOT convert, and must write nothing.
func TestIntegration_ConvertRefusedOnActiveCreditHold(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00) // ample headroom: only the hold can refuse
	placeHold(t, pool, cust)

	_, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})

	var blocked ErrCreditBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("convert under an ACTIVE credit hold must be refused; got err=%v", err)
	}
	if blocked.Policy() != string(identity.PolicyCOD) {
		t.Errorf("blocking policy = %q, want %q", blocked.Policy(), identity.PolicyCOD)
	}
	// The BFF maps ErrInvalidTransition to 409; the block must ride that mapping, not fall through to 500.
	if !errors.Is(err, ErrInvalidTransition) {
		t.Error("a credit-blocked conversion must reach the portal as a 409, not a 500")
	}
	// The whole point: nothing was written.
	if n := ordersForQuote(t, pool, quoteID); n != 0 {
		t.Errorf("a refused conversion wrote %d orders row(s); want 0", n)
	}
	if s := quoteStatus(t, pool, quoteID); s != "ACCEPTED" {
		t.Errorf("a refused conversion left the quote %q; want it still ACCEPTED", s)
	}
}

// TestIntegration_ConvertAllowedWithoutHold is the allow leg: the same quote, same money, no hold.
func TestIntegration_ConvertAllowedWithoutHold(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)

	res, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("a customer with no hold and headroom must convert: %v", err)
	}
	if res.OrderID == "" || res.OrderNo == "" {
		t.Fatalf("convert returned an empty result: %+v", res)
	}
	if n := ordersForQuote(t, pool, quoteID); n != 1 {
		t.Errorf("orders written = %d, want exactly 1", n)
	}
	if s := quoteStatus(t, pool, quoteID); s != "CONVERTED" {
		t.Errorf("quote status = %q, want CONVERTED", s)
	}
}

// TestIntegration_ConvertAfterHoldReleased proves the gate reads the LIVE hold state (status='ACTIVE'),
// not merely the presence of a credit_holds row: a RELEASED hold must not block.
func TestIntegration_ConvertAfterHoldReleased(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)
	placeHold(t, pool, cust)
	if _, err := pool.Exec(context.Background(),
		`UPDATE credit_holds SET status='RELEASED', released_at=now() WHERE customer_id=$1`, cust); err != nil {
		t.Fatalf("release hold: %v", err)
	}

	if _, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{}); err != nil {
		t.Fatalf("a released hold must not block the conversion: %v", err)
	}
	if n := ordersForQuote(t, pool, quoteID); n != 1 {
		t.Errorf("orders written = %d, want exactly 1", n)
	}
}

// TestIntegration_ConvertRefusedOverCreditLimit covers the gate's OTHER leg, the identity credit
// decision the hold-aware gate defers to, through the same conversion path.
func TestIntegration_ConvertRefusedOverCreditLimit(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 500) // the seeded quote totals 2000 cents, over the limit

	_, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	var blocked ErrCreditBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("a conversion beyond the credit limit must be refused; got %v", err)
	}
	if blocked.Policy() != string(identity.PolicyCredit) {
		t.Errorf("blocking policy = %q, want %q", blocked.Policy(), identity.PolicyCredit)
	}
	if n := ordersForQuote(t, pool, quoteID); n != 0 {
		t.Errorf("a refused conversion wrote %d orders row(s); want 0", n)
	}

	// On the default box (account_position_visibility = MANAGER_UP), a frontline seller
	// converting through StaffService gets the outcome in words and no credit limit or open balance figure.
	svc := shareSvc(t, pool, decisionNow())
	frontCtx := decisionCaller("INSIDE_SALES", intTrenton)
	_, frontErr := svc.ConvertQuote(frontCtx, quoteID, ConvertOptions{})
	var frontBlocked ErrCreditBlocked
	if !errors.As(frontErr, &frontBlocked) {
		t.Fatalf("staff convert beyond credit limit must return ErrCreditBlocked; got %v", frontErr)
	}
	frontMsg := CreditRefusalMessage(frontBlocked)
	if !strings.Contains(frontMsg, "this order is over the customer's credit limit") {
		t.Errorf("refused caller's message must state the outcome in words: %q", frontMsg)
	}
	for _, leak := range []string{"$5.00", "500 cents", "1500 cents", "credit_limit", "open_balance"} {
		if strings.Contains(frontMsg, leak) {
			t.Errorf("refused caller's message leaked %q: %q", leak, frontMsg)
		}
	}
	if frontBlocked.Blocked != nil {
		if _, ok := frontBlocked.Blocked.Data["credit_limit"]; ok {
			t.Errorf("refused caller's block Data must not carry credit_limit: %+v", frontBlocked.Blocked.Data)
		}
		if _, ok := frontBlocked.Blocked.Data["open_balance"]; ok {
			t.Errorf("refused caller's block Data must not carry open_balance: %+v", frontBlocked.Blocked.Data)
		}
	}
}
