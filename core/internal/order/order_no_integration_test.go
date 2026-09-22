//go:build integration

// order_no_integration_test.go, DIB-388 branch-aware document numbering, end to end against a live
// Postgres. The property that matters to a customer is not the format: it is that the number they are
// shown RESOLVES BACK to their order. So every leg here mints a number the production way and then
// resolves it through order.Repository.ResolveOrderID, the same call the /orders/{idOrNo} routes make.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/order/... -run OrderNo -count=1
package order

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/jackc/pgx/v5/pgxpool"
)

const kingstonBranch = "5d1b3f6e-0000-4000-9000-000000000002" // seeded KGN branch (0003)

// branchNo asserts the shape a branch-coded number must have: '#SO-<CODE>-<n>'.
func assertBranchCoded(t *testing.T, orderNo, code string) {
	t.Helper()
	want := "#SO-" + code + "-"
	if !strings.HasPrefix(orderNo, want) {
		t.Fatalf("order_no %q does not carry the %s branch prefix %q", orderNo, code, want)
	}
	if strings.TrimPrefix(orderNo, want) == "" {
		t.Fatalf("order_no %q has a branch prefix but no ordinal", orderNo)
	}
}

// seedCreditworthyCustomer makes an account that clears the DIB-387 conversion gate, so this file's
// assertions stay about NUMBERING.
func seedCreditworthyCustomer(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents)
		 VALUES ($1, 'DIB-388 tenant', 'CONTRACTOR', 50000000) RETURNING id::text`,
		fmt.Sprintf("D388-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`, cust)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM quote_lines WHERE quote_id IN (SELECT id FROM quotes WHERE customer_id = $1)`, cust)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust
}

// seedAcceptedQuote writes an ACCEPTED quote at `branch` for the customer.
func seedAcceptedQuote(t *testing.T, pool *pgxpool.Pool, cust, branch string) string {
	t.Helper()
	var quoteID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2::uuid, $3::uuid, 'ACCEPTED', 2000, 0, 2000) RETURNING id::text`,
		fmt.Sprintf("#Q-D388-%d", time.Now().UnixNano()), cust, branch).Scan(&quoteID); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	return quoteID
}

// TestIntegration_OrderNo_ConvertedQuoteResolvesByNumber is the DIB-388 acceptance: convert a quote,
// then find the resulting order BY ITS NUMBER. Before 0087 the conversion minted a bare "4", which
// ResolveOrderID (normalizing to "#4") could never find, the customer's order was unreachable.
func TestIntegration_OrderNo_ConvertedQuoteResolvesByNumber(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	cust := seedCreditworthyCustomer(t, pool)

	for _, tc := range []struct{ branch, code string }{
		{trentonBranch, "TRT"},
		{kingstonBranch, "KGN"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			quoteID := seedAcceptedQuote(t, pool, cust, tc.branch)
			res, err := quote.NewRepository(pool).Convert(ctx, cust, quoteID, quote.ConvertOptions{})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			assertBranchCoded(t, res.OrderNo, tc.code)

			// The whole point: the number the customer is shown resolves to the order.
			got, err := repo.ResolveOrderID(ctx, res.OrderNo, true, nil)
			if err != nil {
				t.Fatalf("resolve %q: %v", res.OrderNo, err)
			}
			if got != res.OrderID {
				t.Errorf("resolve(%q) = %s, want %s", res.OrderNo, got, res.OrderID)
			}
			// …and so does the form a cashier dictates off a receipt, without the '#'.
			bare := strings.TrimPrefix(res.OrderNo, "#")
			if got, err := repo.ResolveOrderID(ctx, bare, true, nil); err != nil || got != res.OrderID {
				t.Errorf("resolve(%q) = (%s, %v), want (%s, nil)", bare, got, err, res.OrderID)
			}
			// Branch scoping survives the format change: a caller who cannot reach the branch gets the
			// same ErrNotFound a missing order gives, never an existence oracle.
			other := kingstonBranch
			if tc.branch == kingstonBranch {
				other = trentonBranch
			}
			if _, err := repo.ResolveOrderID(ctx, res.OrderNo, false, []string{other}); err != ErrNotFound {
				t.Errorf("cross-branch resolve err = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestIntegration_OrderNo_ReorderDraftIsBranchCoded covers the reorder writer (order/reorder.go).
func TestIntegration_OrderNo_ReorderDraftIsBranchCoded(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	cust, src := seedCustomerAndOrder(t, pool)

	res, err := repo.CreateReorderDraft(ctx, ReorderDraft{
		CustomerID: cust, BranchID: trentonBranch, Fulfillment: "PICKUP",
		SourceOrderID: src, IdempotencyKey: fmt.Sprintf("d388-%d", time.Now().UnixNano()),
		SubtotalCents: 9990,
		Lines: []ReorderDraftLine{{
			LineNo: 1, Description: "Repriced line", QtyEntered: "10", UOMEntered: "EA",
			QtyBase: "10", UnitPriceCents: 999, PriceUOM: "EA", LineTotalCents: 9990,
		}},
	})
	if err != nil {
		t.Fatalf("create reorder draft: %v", err)
	}
	assertBranchCoded(t, res.OrderNo, "TRT")
	got, err := repo.ResolveOrderID(ctx, res.OrderNo, true, nil)
	if err != nil || got != res.OrderID {
		t.Fatalf("resolve(%q) = (%s, %v), want (%s, nil)", res.OrderNo, got, err, res.OrderID)
	}
}

// TestIntegration_OrderNo_LegacyNumbersStillResolve pins the compatibility half of the rule: the
// pre-0087 global "#SO-N" form keeps resolving, with and without the '#'. A renumbering that orphaned
// every historical order would be a worse bug than the one being fixed.
func TestIntegration_OrderNo_LegacyNumbersStillResolve(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	cust := seedCreditworthyCustomer(t, pool)

	legacy := fmt.Sprintf("#SO-%d", time.Now().UnixNano()%100000)
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'DRAFT', 'PICKUP', 100, 100) RETURNING id::text`,
		legacy, cust, trentonBranch).Scan(&id); err != nil {
		t.Fatalf("seed legacy order: %v", err)
	}
	for _, form := range []string{legacy, strings.TrimPrefix(legacy, "#")} {
		got, err := repo.ResolveOrderID(ctx, form, true, nil)
		if err != nil || got != id {
			t.Errorf("legacy resolve(%q) = (%s, %v), want (%s, nil)", form, got, err, id)
		}
	}
}

// TestIntegration_OrderNo_MintContract pins next_order_no itself: per-branch counters that do not share
// a series, and a hard refusal (not a bare number) for a branch it cannot name.
func TestIntegration_OrderNo_MintContract(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	mint := func(branch string) string {
		t.Helper()
		var no string
		if err := pool.QueryRow(ctx, `SELECT next_order_no($1)`, branch).Scan(&no); err != nil {
			t.Fatalf("next_order_no(%s): %v", branch, err)
		}
		return no
	}

	t1, t2 := mint(trentonBranch), mint(trentonBranch)
	k1 := mint(kingstonBranch)
	assertBranchCoded(t, t1, "TRT")
	assertBranchCoded(t, t2, "TRT")
	assertBranchCoded(t, k1, "KGN")
	if t1 == t2 {
		t.Errorf("two mints on one branch returned the same number %q", t1)
	}
	if strings.TrimPrefix(t2, "#SO-TRT-") == strings.TrimPrefix(k1, "#SO-KGN-") &&
		strings.TrimPrefix(t2, "#SO-TRT-") == "" {
		t.Error("branch ordinals must come from per-branch counters")
	}

	// An unknown branch must RAISE, an order that cannot be branch-stamped must not be written.
	var no string
	err := pool.QueryRow(ctx, `SELECT next_order_no('00000000-0000-4000-8000-000000000000'::uuid)`).Scan(&no)
	if err == nil {
		t.Errorf("next_order_no on an unknown branch returned %q; want an error", no)
	}
}
