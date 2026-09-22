//go:build integration

// customer_reader_integration_test.go, W5-C item 4: the order→customer reader that closes the W3-D
// declared gap ("CustomerForOrder is a DECLARED GAP, not an oversight", cmd/server/main.go).
//
// The gap was never that the mapping was hard. It was that every order→customer path internal/order
// exposed already TOOK the customer id (GetOrder(customerID, orderID)), so a delivery path holding only
// an order id could not ask the question, and messagingprovider must not JOIN orders itself. These
// tests pin the three behaviours the composition root will bind against.
package order

import (
	"context"
	"errors"
	"testing"
)

// TestIntegration_CustomerForOrder_ResolvesTheOwningCustomer is the W5-C named test for item 4: the
// reader answers with the order's OWNING customer id, from an order id alone.
func TestIntegration_CustomerForOrder_ResolvesTheOwningCustomer(t *testing.T) {
	pool := intPool(t)
	cust, ord := seedOrder(t, pool, "CONFIRMED")
	repo := NewRepository(pool)

	got, err := repo.CustomerForOrder(context.Background(), ord)
	if err != nil {
		t.Fatalf("CustomerForOrder(%s): %v", ord, err)
	}
	if got != cust {
		t.Fatalf("CustomerForOrder resolved %q, want the owning customer %q", got, cust)
	}
}

// TestIntegration_CustomerForOrder_MissingAndMalformedAreBothErrNotFound pins the no-existence-leak
// rule this seam inherits from OrderBranch/OrderBrief: a well-formed id for an order that does not
// exist and a garbage id are INDISTINGUISHABLE to the caller. A delivery path must not double as an
// order-existence oracle, and a malformed id must never reach the uuid column as a 22P02 → 500.
func TestIntegration_CustomerForOrder_MissingAndMalformedAreBothErrNotFound(t *testing.T) {
	repo := NewRepository(intPool(t))
	ctx := context.Background()

	// A well-formed uuid that is not an order.
	if _, err := repo.CustomerForOrder(ctx, "3b191000-0000-4000-9000-0000000000ff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an absent order must be ErrNotFound, got %v", err)
	}
	// Garbage, rejected BEFORE the query, so no SQLSTATE 22P02 escapes as a 500.
	for _, bad := range []string{"", "not-a-uuid", "'; DROP TABLE orders; --"} {
		if _, err := repo.CustomerForOrder(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Fatalf("a malformed id (%q) must be ErrNotFound, got %v", bad, err)
		}
	}
}

// TestIntegration_CustomerForOrder_IsNotTenantScoped states the ruling out loud so a later reader does
// not "fix" it into a scoped signature. The seam is bound to a server-side delivery path that holds an
// order id and nothing else; requiring the customer id would restore the exact circularity that left
// sms.dispatch.notice resolving nobody for two waves.
func TestIntegration_CustomerForOrder_IsNotTenantScoped(t *testing.T) {
	pool := intPool(t)
	custA, ordA := seedOrder(t, pool, "CONFIRMED")
	custB, ordB := seedOrder(t, pool, "CONFIRMED")
	if custA == custB {
		t.Fatal("fixture invariant: the two orders must belong to different customers")
	}
	repo := NewRepository(pool)
	ctx := context.Background()

	// No caller identity is supplied and each order still resolves to its own owner, the two never
	// cross, which is what makes an unscoped read safe here.
	gotA, err := repo.CustomerForOrder(ctx, ordA)
	if err != nil {
		t.Fatalf("order A: %v", err)
	}
	gotB, err := repo.CustomerForOrder(ctx, ordB)
	if err != nil {
		t.Fatalf("order B: %v", err)
	}
	if gotA != custA || gotB != custB {
		t.Fatalf("each order must resolve to ITS OWN customer; got A=%q (want %q), B=%q (want %q)", gotA, custA, gotB, custB)
	}
}
