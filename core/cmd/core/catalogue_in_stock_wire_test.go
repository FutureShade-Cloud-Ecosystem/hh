package main

// catalogue_in_stock_wire_test.go pins the catalogue in_stock_only filter's
// production wiring at the composition root (HH-190): product.Service
// resolves the filter through a StockReader, and with none wired the
// service fails closed to an empty list, so a fold that forgets the seam
// ships a filter that answers nothing for every caller. The module's own
// suites inject a fake reader and cannot see that. This test drives the
// REAL wiring (wireMoney over the scratch database) and the REGISTERED
// route GET /api/v1/products, with one product stocked at the caller's
// branch and one not.
//
// It gates on HH_CORE_TEST_DATABASE_URL like the other composition suites:
// absent the variable it skips politely. The planted products and balance
// are tagged and removed after, so a shared scratch database survives.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// plantCatalogueStock plants two active products under one unique tag, the
// first with positive on-hand at branchID and the second with no stock row
// at all, and returns their ids and the tag the catalogue search narrows by.
func plantCatalogueStock(t *testing.T, pool *pgxpool.Pool, branchID string) (stocked, unstocked, tag string) {
	t.Helper()
	ctx := context.Background()
	tag = fmt.Sprintf("hh190-%d", time.Now().UnixNano())
	plant := func(suffix string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO products (sku, name, base_uom, price_uom) VALUES ($1, $2, 'EA', 'EA') RETURNING id::text`,
			tag+"-"+suffix, tag+" "+suffix).Scan(&id); err != nil {
			t.Fatalf("plant product %s: %v", suffix, err)
		}
		return id
	}
	stocked, unstocked = plant("stocked"), plant("unstocked")
	// Registered straight after the products exist, so a failed stock insert below cannot strand them.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM stock_balances WHERE product_id = $1`, stocked)
		_, _ = pool.Exec(context.Background(), `DELETE FROM products WHERE id = ANY($1::uuid[])`, []string{stocked, unstocked})
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base) VALUES ($1, $2, $2, 25)`,
		stocked, branchID); err != nil {
		t.Fatalf("plant stock balance: %v", err)
	}
	return stocked, unstocked, tag
}

// listCatalogueIDs drives the registered GET /api/v1/products and returns
// the sorted product ids it answers.
func listCatalogueIDs(t *testing.T, mux *http.ServeMux, query url.Values) []string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/products?"+query.Encode(), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalogue route = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode catalogue body: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	sort.Strings(ids)
	return ids
}

// catalogueStatus drives the registered GET /api/v1/products and returns only the status code.
func catalogueStatus(mux *http.ServeMux, query url.Values) int {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products?"+query.Encode(), nil))
	return rec.Code
}

func sameIDs(got []string, want ...string) bool {
	sort.Strings(want)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestCatalogueInStockWiring_FilterAnswersTheStockedProductOnly(t *testing.T) {
	pool := wiringPool(t)
	// Two migration-planted branches: the first holds the stock, the second never does.
	var branchID, otherBranchID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations ORDER BY id LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no migration-planted branch: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations WHERE id::text <> $1 ORDER BY id LIMIT 1`, branchID).Scan(&otherBranchID); err != nil {
		t.Fatalf("no second migration-planted branch: %v", err)
	}
	stocked, unstocked, tag := plantCatalogueStock(t, pool, branchID)

	mux := http.NewServeMux()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sg := testkit.NewStaffGuards(t)
	wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())

	on := url.Values{"q": {tag}, "in_stock_only": {"true"}, "branch_id": {branchID}}
	if got := listCatalogueIDs(t, mux, on); !sameIDs(got, stocked) {
		t.Fatalf("in_stock_only=true at the stocked branch answered %v, want exactly the stocked product [%s] "+
			"(the unstocked one is %s): the fold never wired the StockReader, so the filter fails closed to nothing",
			got, stocked, unstocked)
	}

	off := url.Values{"q": {tag}, "branch_id": {branchID}}
	if got := listCatalogueIDs(t, mux, off); !sameIDs(got, stocked, unstocked) {
		t.Fatalf("filter off answered %v, want both planted products [%s %s]", got, stocked, unstocked)
	}

	// Another branch holds none of it: the filter is scoped to the branch asked for.
	other := url.Values{"q": {tag}, "in_stock_only": {"true"}, "branch_id": {otherBranchID}}
	if got := listCatalogueIDs(t, mux, other); len(got) != 0 {
		t.Fatalf("in_stock_only=true at a branch with no stock answered %v, want nothing", got)
	}

	// No branch asked for: in stock at any branch, so the stocked product answers.
	anywhere := url.Values{"q": {tag}, "in_stock_only": {"true"}}
	if got := listCatalogueIDs(t, mux, anywhere); !sameIDs(got, stocked) {
		t.Fatalf("in_stock_only=true with no branch answered %v, want exactly the stocked product [%s]", got, stocked)
	}

	// The branch text lands on a uuid column on an unauthenticated route: a malformed one is a 400, not a 500.
	bad := url.Values{"q": {tag}, "in_stock_only": {"true"}, "branch_id": {"not-a-uuid"}}
	if code := catalogueStatus(mux, bad); code != http.StatusBadRequest {
		t.Fatalf("in_stock_only=true with a malformed branch_id = %d, want 400", code)
	}
}
