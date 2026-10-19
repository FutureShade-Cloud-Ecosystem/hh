package inventory

// reorder_points_fence_test.go: the managed-lines read answers the caller's branch reach and no
// other, the same fence the shortfall worklist carries (HH-417). It used to hand branch_id straight
// to the repository, so a branch scoped buyer read every branch's managed lines and a named branch
// out of reach was a 200.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// fakeReorderPointStore records what the read actually asked the persistence seam for.
type fakeReorderPointStore struct {
	*fakeStore
	rows       []ReorderPoint
	gotReach   BranchReach
	gotBranch  string
	calledWith int
}

// ListReorderPoints applies the same fence the SQL applies, so the page a unit test reads is the page
// a real repository would serve for that reach.
func (f *fakeReorderPointStore) ListReorderPoints(_ context.Context, reach BranchReach, branchID string, _ int) ([]ReorderPoint, error) {
	f.gotReach, f.gotBranch, f.calledWith = reach, branchID, f.calledWith+1
	var out []ReorderPoint
	for _, r := range f.rows {
		if branchID != "" && r.BranchID != branchID {
			continue
		}
		if !reach.covers(r.BranchID) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func newReorderPointsSvc(rows []ReorderPoint) (*Handler, *fakeReorderPointStore) {
	st := &fakeReorderPointStore{fakeStore: newFakeStore(), rows: rows}
	return NewHandler(NewService(nil).withStore(st), slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func buyerReqAt(target string, branches ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return r.WithContext(identity.ContextWithStaff(r.Context(), identity.StaffContext{
		UserID: "d7000000-0000-4000-9000-000000000011", RoleCode: "PROCUREMENT_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: granted,
	}))
}

func globalReq(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	return r.WithContext(identity.ContextWithStaff(r.Context(), identity.StaffContext{
		UserID: "u-global", RoleCode: "PROCUREMENT_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}))
}

func readReorderPoints(t *testing.T, h *Handler, r *http.Request) (int, []ReorderPoint) {
	t.Helper()
	w := httptest.NewRecorder()
	h.listReorderPoints(w, r, guards.StaffCaller{})
	if w.Code != http.StatusOK {
		return w.Code, nil
	}
	var body struct {
		ReorderPoints []ReorderPoint `json:"reorder_points"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body is not the page: %v (%s)", err, w.Body.String())
	}
	return w.Code, body.ReorderPoints
}

func TestListReorderPoints_AFencedBranchBuyerGetsOnlyItsOwnBranches(t *testing.T) {
	rows := []ReorderPoint{{SKU: "OWN", BranchID: trt470}, {SKU: "OTHER", BranchID: aberdn}}
	h, st := newReorderPointsSvc(rows)

	code, got := readReorderPoints(t, h, buyerReqAt("/api/v1/inventory/reorder-points", trt470))
	if code != http.StatusOK {
		t.Fatalf("an unnamed read answered %d, want 200", code)
	}
	if st.gotBranch != "" {
		t.Errorf("an unnamed read still named branch %q, the reach set is what should fence it", st.gotBranch)
	}
	if st.calledWith == 0 {
		t.Fatalf("the read never reached the store: the fence swallowed an ordinary call")
	}
	if !st.gotReach.covers(trt470) || st.gotReach.covers(aberdn) {
		t.Errorf("the reach handed to the query is %v, want only the caller's own grant", st.gotReach)
	}
	for _, r := range got {
		if r.BranchID == aberdn {
			t.Errorf("the page carries another branch's managed line: %+v", r)
		}
	}
}

func TestListReorderPoints_AnOutOfReachNamedBranchIsA403(t *testing.T) {
	h, st := newReorderPointsSvc(nil)
	code, _ := readReorderPoints(t, h, buyerReqAt("/api/v1/inventory/reorder-points?branch_id="+aberdn, trt470))
	if code != http.StatusForbidden {
		t.Fatalf("a named branch out of reach answered %d, want 403", code)
	}
	if st.calledWith != 0 {
		t.Errorf("an out of reach read still queried the store")
	}
}

func TestListReorderPoints_ABuyerWithNoGrantsGetsAnEmptyPage(t *testing.T) {
	h, st := newReorderPointsSvc([]ReorderPoint{{SKU: "ANY", BranchID: trt470}})
	rec := httptest.NewRecorder()
	h.listReorderPoints(rec, buyerReqAt("/api/v1/inventory/reorder-points"), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("a buyer with no grants answered %d, want 200", rec.Code)
	}
	if want := `{"reorder_points":[]}` + "\n"; rec.Body.String() != want {
		t.Errorf("a buyer with no grants got %s, want %s (an empty array, never null, and no query)", rec.Body.String(), want)
	}
	if st.calledWith != 0 {
		t.Errorf("a buyer with no grants still queried the store")
	}
}

func TestListReorderPoints_AGlobalCallerReadsEverything(t *testing.T) {
	rows := []ReorderPoint{{SKU: "A", BranchID: trt470}, {SKU: "B", BranchID: aberdn}}
	h, st := newReorderPointsSvc(rows)
	code, got := readReorderPoints(t, h, globalReq("/api/v1/inventory/reorder-points"))
	if code != http.StatusOK || len(got) != 2 {
		t.Fatalf("a global unnamed read answered %d with %d rows, want 200 and all branches", code, len(got))
	}
	if !st.gotReach.Global {
		t.Errorf("the reach handed to the query is not global: %v", st.gotReach)
	}
	code, got = readReorderPoints(t, h, globalReq("/api/v1/inventory/reorder-points?branch_id="+aberdn))
	if code != http.StatusOK || len(got) != 1 || got[0].BranchID != aberdn {
		t.Fatalf("a global read naming one branch answered %d with %+v, want 200 and that branch", code, got)
	}
}
