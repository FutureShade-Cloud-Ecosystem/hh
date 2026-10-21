//go:build integration

package pos

// counterprice_fence_integration_test.go: the counter prices read answers only for a branch the caller's resolved
// authority reaches. A scoped caller who names another branch is refused with the same 403 words create sale gives
// ("pos: caller cannot access branch <id>"), before any customer or price is read; a granted branch and a global
// caller are answered as before; a read naming no branch is the dealer wide picture and stays answered.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestIntegration_CounterPrices_BranchFence(t *testing.T) {
	pool := intPool(t)
	svc := realPosSvc(t, pool, stubCharger{})
	rows, err := pool.Query(context.Background(), `SELECT id::text FROM locations ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	var branches []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		branches = append(branches, id)
	}
	rows.Close()
	if len(branches) < 2 {
		t.Skip("the seeded world has fewer than two branches")
	}
	var productID, customerID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM products ORDER BY id LIMIT 1`).Scan(&productID); err != nil {
		t.Skipf("no seeded product: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM customers WHERE home_branch_id IS NULL OR home_branch_id::text = $1 ORDER BY id LIMIT 1`, branches[0]).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer the granted branch may trade with: %v", err)
	}
	in := func(branch string) CounterPriceInput {
		return CounterPriceInput{BranchID: branch, CustomerID: customerID, Items: []CounterPriceItem{{ProductID: productID, QtyEntered: 1}}}
	}
	scoped := staffCtx(t, pool, identity.ScopeBranch, branches[0])

	_, err = svc.CounterPrices(scoped, in(branches[1]))
	ce, ok := err.(interface{ HTTPStatus() int })
	if err == nil {
		t.Fatalf("a branch the caller is not granted must be refused, got an answer")
	}
	if ok && ce.HTTPStatus() != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %v", ce.HTTPStatus(), err)
	}
	if want := "pos: caller cannot access branch " + branches[1]; !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal words are create sale's: want %q, got %q", want, err.Error())
	}
	if _, err := svc.CounterPrices(scoped, in(branches[0])); err != nil {
		t.Fatalf("the granted branch is answered: %v", err)
	}
	if _, err := svc.CounterPrices(globalCtx(t, pool), in(branches[1])); err != nil {
		t.Fatalf("a global caller reaches every branch: %v", err)
	}
	if _, err := svc.CounterPrices(scoped, in("")); err != nil {
		t.Fatalf("a read naming no branch stays answered: %v", err)
	}
}
