package staffcontext

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The rail's HTTP surface proves its three routes through the real Guards
// (the kit's DB-free double with a settable resolved authority) and the
// real stamp adapter with a linker double, so the halves the A3 re-point
// made load-bearing are both exercised: authority comes from the guard's
// resolution (a front-line caller cannot decide), and the ERP id comes
// from the linker (an unlinked caller is refused before anything is
// filed or decided, never attributed to nobody).
func TestApprovalsHandler_RoutesAndRePointHalves(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	bearer := sg.Bearer(t)

	const linkedID = "11111111-1111-1111-1111-111111111111"
	link := func(_ context.Context, _ string) (string, bool) { return linkedID, true }
	noLink := func(_ context.Context, _ string) (string, bool) { return "", false }

	f := &fakeStore{
		approval: ApprovalRequest{ID: "22222222-2222-2222-2222-222222222222", RequestedBy: "someone-else", BranchID: trt, Policy: PolicyMargin, Status: ApprovalPending},
	}
	h := NewApprovalsHandler(NewApprovals(f), link)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("X-Org", testkit.OrgNorthSlug)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		return rr
	}

	// The guard resolves a ruled rail approver; the linker supplies the
	// ERP id; the create leg files under that id. D7 moved the margin
	// policy to the quote module's door, so the caller facing create leg's
	// policy is the credit one, and the margin door's refusal is asserted
	// beside it.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: "global"})
	if rr := do(http.MethodPost, "/api/v1/staff/approvals", `{"branch_id":"`+trt+`","policy_type":"CREDIT_LIMIT_EXCEEDED","detail":"d"}`); rr.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (body %s)", rr.Code, rr.Body.String())
	}
	if rr := do(http.MethodPost, "/api/v1/staff/approvals", `{"branch_id":"`+trt+`","policy_type":"MARGIN_BELOW_MINIMUM","detail":"d"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("margin create = %d, want the quote desk door's 403 (body %s)", rr.Code, rr.Body.String())
	}

	// Front-line half: the same surface refuses the decide to a caller
	// the guard resolves without rail-approver authority, which is the
	// re-point working: the authority is the guard's resolution, read
	// from no scaffold table and no stamp of its own.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{trt}})
	if rr := do(http.MethodPost, "/api/v1/staff/approvals/22222222-2222-2222-2222-222222222222/decide", `{"approve":true}`); rr.Code != http.StatusForbidden {
		t.Fatalf("front-line decide = %d, want the authority model's 403 (body %s)", rr.Code, rr.Body.String())
	}

	// Approver half plus four-eyes: a ruled approver deciding their OWN
	// request is refused by the separation-of-duties check.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: "global"})
	f.approval.RequestedBy = linkedID
	if rr := do(http.MethodPost, "/api/v1/staff/approvals/22222222-2222-2222-2222-222222222222/decide", `{"approve":true}`); rr.Code != http.StatusForbidden {
		t.Fatalf("own-request decide = %d, want four-eyes 403 (body %s)", rr.Code, rr.Body.String())
	}
	f.approval.RequestedBy = "someone-else"

	// List leg answers through the same guard.
	list := do(http.MethodGet, "/api/v1/staff/approvals", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (body %s)", list.Code, list.Body.String())
	}
	// The contract's IdentityApprovalPage: an object whose required key items is an array, with the
	// cursor spelled as a string or null. The list used to answer a bare array.
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("list body does not decode as a one item page with a null cursor (err %v): %s", err, list.Body.String())
	}

	// Unlinked half: no linker row means no ERP id, and the surface
	// refuses the create and decide legs rather than attributing a rail
	// request or a money decision to nobody.
	hl := NewApprovalsHandler(NewApprovals(&fakeStore{approval: f.approval}), noLink)
	mux2 := http.NewServeMux()
	hl.RegisterRoutes(mux2, sg.Guards)
	do2 := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("X-Org", testkit.OrgNorthSlug)
		rr := httptest.NewRecorder()
		mux2.ServeHTTP(rr, r)
		return rr
	}
	if rr := do2(http.MethodPost, "/api/v1/staff/approvals", `{"branch_id":"`+trt+`","policy_type":"MARGIN_BELOW_MINIMUM","detail":"d"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unlinked create = %d, want the fail-closed 401 (body %s)", rr.Code, rr.Body.String())
	}
	if rr := do2(http.MethodPost, "/api/v1/staff/approvals/22222222-2222-2222-2222-222222222222/decide", `{"approve":true}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unlinked decide = %d, want the fail-closed 401 (body %s)", rr.Code, rr.Body.String())
	}

	// Anonymous half: no bearer, no route body.
	anon := httptest.NewRequest(http.MethodGet, "/api/v1/staff/approvals", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, anon)
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("anonymous list = %d, want the gate's refusal", rr.Code)
	}
}
