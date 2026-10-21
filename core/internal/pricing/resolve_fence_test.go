package pricing

// resolve_fence_test.go (UX-89): pricing resolve and the low margin report hold each line to the caller's reach
// before anything is resolved. A branch the caller is not granted is a 403 in words; a named customer the
// caller cannot reach is a 404 (the account read's answer); a line naming neither, or only what the caller
// reaches, is answered as before.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeReach map[string]bool

func (f fakeReach) CustomerReachable(_ context.Context, id string) (bool, error) { return f[id], nil }

func fenceDo(h *Handler, path, body string, call func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	call(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func TestResolveFence_BranchAndCustomer(t *testing.T) {
	h := NewHandler(scanSvc(&fakeScan{}), nil).WithCustomerReach(fakeReach{"cust-a": true})
	caller := branchCaller(wallTrenton)
	resolve := func(body string) *httptest.ResponseRecorder {
		return fenceDo(h, "/api/v1/pricing/resolve", body, func(w http.ResponseWriter, r *http.Request) { h.resolve(w, r, caller) })
	}
	if rec := resolve(`{"product_id":"p","branch_id":"` + wallOther + `"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a branch outside the grants: want 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := resolve(`{"product_id":"p","customer_id":"cust-b"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("an unreachable account with no branch: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := resolve(`{"product_id":"p","customer_id":"cust-b","branch_id":"` + wallTrenton + `"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("an unreachable account at the caller's own branch: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	// Past the wall the request meets the resolver, which has no such product: any answer but the wall's two.
	for _, body := range []string{
		`{"product_id":"p","customer_id":"cust-a","branch_id":"` + wallTrenton + `"}`,
		`{"product_id":"p"}`,
	} {
		if rec := resolve(body); rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound {
			t.Fatalf("a reachable line must pass the wall, got %d: %s", rec.Code, rec.Body.String())
		}
	}
}

func TestLowMarginReportFence_AnyLineOutOfReachRefusesTheBatch(t *testing.T) {
	h := NewHandler(scanSvc(&fakeScan{}), nil).WithCustomerReach(fakeReach{"cust-a": true})
	caller := branchCaller(wallTrenton)
	report := func(body string) *httptest.ResponseRecorder {
		return fenceDo(h, "/api/v1/pricing/reports/low-margin", body, func(w http.ResponseWriter, r *http.Request) { h.lowMarginReport(w, r, caller) })
	}
	ok := `{"product_id":"p","customer_id":"cust-a","branch_id":"` + wallTrenton + `"}`
	if rec := report(`{"lines":[` + ok + `,{"product_id":"p","branch_id":"` + wallOther + `"}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a line at an ungranted branch: want 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := report(`{"lines":[` + ok + `,{"product_id":"p","customer_id":"cust-b"}]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("a line naming an unreachable account: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
