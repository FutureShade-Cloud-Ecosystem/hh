package pricing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// branchprice_handler_test.go: the branch price surface's decode legs, over
// the fake store, the floor preview handler test's shape. The integration
// legs (real schema, real trail, pool four) live in
// branchprice_integration_test.go.

// TestPutBranchPriceHandlerRefusesAMissingPrice: the contract makes
// list_price_cents required, so a body without the field and a body with it
// null are a bad request naming the field. A silent zero branch price would
// answer LIST 0 at the branch until someone noticed.
func TestPutBranchPriceHandlerRefusesAMissingPrice(t *testing.T) {
	h := NewHandler(NewService(bpStore(nil, nil)), nil)
	const productID = "a7000000-0000-4000-9000-000000000001"
	const branchID = "b7000000-0000-4000-9000-000000000001"
	for _, body := range []string{
		`{"price_uom":"EA"}`,
		`{"list_price_cents":null,"price_uom":"EA"}`,
	} {
		r := httptest.NewRequest(http.MethodPut,
			"/api/v1/pricing/branch-prices/"+productID+"/"+branchID, strings.NewReader(body))
		r.SetPathValue("productId", productID)
		r.SetPathValue("branchId", branchID)
		rec := httptest.NewRecorder()
		h.putBranchPrice(rec, r, guards.StaffCaller{})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s answered %d (%s), want 400: an absent or null list_price_cents must refuse, never store a zero price", body, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "list_price_cents") {
			t.Fatalf("body %s answered %q, the refusal must name the field", body, rec.Body.String())
		}
	}
}
