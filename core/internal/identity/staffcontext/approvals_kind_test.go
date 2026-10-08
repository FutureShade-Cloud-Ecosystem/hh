package staffcontext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The approvals list per kind (Manager Home, 0381 lane): the same list the caller already reads,
// narrowed to one policy kind, and only for a kind the caller could decide. A kind the caller holds
// no authority over is refused rather than answered empty, so a surface offering a tab per kind has
// one honest source for which tabs to draw.

// kindList plants one pending request of every kind in one branch, filed by a salesperson (the
// margin arm's requester) so a branch scoped Sales manager can decide the margin one.
func kindList() []ApprovalRequest {
	var out []ApprovalRequest
	for i, k := range []PolicyType{PolicyMargin, PolicyCredit, PolicyCOD, PolicyJEThreshold, PolicyFreightAboveQuote} {
		r := filerRequest("INSIDE_SALES")
		r.ID = filerReqID[:len(filerReqID)-2] + "k" + string(rune('0'+i))
		r.Policy = k
		out = append(out, r)
	}
	return out
}

func TestApprovalsByKind_ANarrowedListForAKindTheCallerDecides(t *testing.T) {
	s := NewApprovals(&filerListStore{list: kindList()}).WithFloorApprovers(qd1FloorApprovers())
	for _, k := range []PolicyType{PolicyMargin, PolicyJEThreshold, PolicyFreightAboveQuote, PolicyCredit, PolicyCOD} {
		got, err := s.ListApprovalsByKind(context.Background(), qd1GM("gm1"), "", k)
		if err != nil {
			t.Fatalf("%s as the General Manager: %v", k, err)
		}
		if len(got) != 1 || got[0].Policy != k {
			t.Errorf("%s list holds %+v, want exactly the one %s request", k, got, k)
		}
	}
	// No kind named is the unchanged list.
	all, err := s.ListApprovalsByKind(context.Background(), qd1GM("gm1"), "", "")
	if err != nil || len(all) != len(kindList()) {
		t.Fatalf("no kind: %d requests (%v), want all %d", len(all), err, len(kindList()))
	}
}

func TestApprovalsByKind_AKindTheCallerCannotDecideIsRefused(t *testing.T) {
	s := NewApprovals(&filerListStore{list: kindList()}).WithFloorApprovers(qd1FloorApprovers())
	sm := qd1SM("sm1", trt)

	got, err := s.ListApprovalsByKind(context.Background(), sm, "", PolicyMargin)
	if err != nil || len(got) != 1 {
		t.Fatalf("the Sales manager's margin list: %d (%v), want the one floor request", len(got), err)
	}
	for _, k := range []PolicyType{PolicyJEThreshold, PolicyFreightAboveQuote, PolicyCredit, PolicyCOD} {
		_, err := s.ListApprovalsByKind(context.Background(), sm, "", k)
		if status, ok := ApprovalErrorStatus(err); !ok || status != http.StatusForbidden {
			t.Errorf("%s as the Sales manager: %v, want a 403", k, err)
		}
	}
	// A clerk decides nothing, so every kind is refused.
	clerk := clerkCtx()
	if _, err := s.ListApprovalsByKind(context.Background(), clerk, "", PolicyMargin); err == nil {
		t.Error("a front-line caller was handed a kind list")
	}
}

func TestApprovalsByKind_AnUnknownKindIsABadRequest(t *testing.T) {
	s := NewApprovals(&filerListStore{list: kindList()})
	_, err := s.ListApprovalsByKind(context.Background(), qd1GM("gm1"), "", PolicyType("NOT_A_KIND"))
	if status, ok := ApprovalErrorStatus(err); !ok || status != http.StatusBadRequest {
		t.Fatalf("unknown kind: %v, want a 400", err)
	}
}

func TestApprovalsHandler_KindQuery(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	bearer := sg.Bearer(t)
	link := func(_ context.Context, _ string) (string, bool) {
		return "11111111-1111-1111-1111-111111111111", true
	}
	h := NewApprovalsHandler(NewApprovals(&fakeStore{}), link)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	get := func(q string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/staff/approvals"+q, nil)
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("X-Org", testkit.OrgNorthSlug)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		return rr.Code
	}
	sg.SetAuthority(identity.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: "global"})
	if code := get("?kind=NOT_A_KIND"); code != http.StatusBadRequest {
		t.Errorf("unknown kind = %d, want 400", code)
	}
	if code := get("?kind=JE_OVER_THRESHOLD"); code != http.StatusOK {
		t.Errorf("a decided kind = %d, want 200", code)
	}
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{trt}})
	if code := get("?kind=JE_OVER_THRESHOLD"); code != http.StatusForbidden {
		t.Errorf("a kind the caller cannot decide = %d, want 403", code)
	}
}
