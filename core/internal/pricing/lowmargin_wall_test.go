package pricing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// The report's branch wall. A branch scoped caller (the Branch manager, the Sales manager) sees only
// the documents of the branches it is granted: with no parameters, as with any; a branch_id outside
// its grant is a 403 in words; a salesperson_id never widens it. A global caller is unchanged.

const (
	wallTrenton = "5d1b3f6e-0000-4000-9000-000000000001"
	wallOther   = "5d1b3f6e-0000-4000-9000-000000000002"
)

func wallLine(id, branch, salesperson string) LowMarginSaleLine {
	ln := docLine(DocTypeQuote, "q-"+id, id, 850)
	ln.BranchID, ln.SalespersonID = branch, salesperson
	return ln
}

func wallScan() *fakeScan {
	return &fakeScan{lines: []LowMarginSaleLine{
		wallLine("here", wallTrenton, "sp-here"),
		wallLine("there", wallOther, "sp-there"),
	}}
}

func wallGet(h *Handler, caller guards.StaffCaller, query string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.lowMarginScan(rec, httptest.NewRequest(http.MethodGet, "/api/v1/pricing/reports/low-margin/scan"+query, nil), caller)
	return rec
}

func wallItems(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, it := range page.Items {
		out = append(out, it["line_id"].(string))
	}
	return out
}

func TestLowMarginWall_ABranchCallerWithNoParametersSeesItsOwnBranchesOnly(t *testing.T) {
	scan := wallScan()
	h := NewHandler(scanSvc(scan), nil)
	if got := wallItems(t, wallGet(h, branchCaller(wallTrenton), "")); !eq(got, []string{"here"}) {
		t.Errorf("a Trenton caller sees %v, want only the Trenton document", got)
	}
	if got := wallItems(t, wallGet(h, branchCaller(wallTrenton, wallOther), "")); !eq(got, []string{"here", "there"}) && !eq(got, []string{"there", "here"}) {
		t.Errorf("a two branch caller sees %v, want both", got)
	}
}

func TestLowMarginWall_ABranchOutsideTheGrantIs403InWords(t *testing.T) {
	scan := wallScan()
	h := NewHandler(scanSvc(scan), nil)
	rec := wallGet(h, branchCaller(wallTrenton), "?branch_id="+wallOther)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 || !json.Valid(rec.Body.Bytes()) {
		t.Errorf("the refusal must say why: %q", rec.Body.String())
	}
	if scan.got.DocType != "" {
		t.Errorf("the feed was asked for a refused branch: %+v", scan.got)
	}
	if got := wallItems(t, wallGet(h, branchCaller(wallTrenton), "?branch_id="+wallTrenton)); !eq(got, []string{"here"}) {
		t.Errorf("the granted branch named: %v", got)
	}
}

func TestLowMarginWall_ASalespersonFilterCannotWidenIt(t *testing.T) {
	h := NewHandler(scanSvc(wallScan()), nil)
	if got := wallItems(t, wallGet(h, branchCaller(wallTrenton), "?salesperson_id=5d1b3f6e-0000-4000-9000-0000000000aa")); len(got) != 0 {
		t.Errorf("an unknown salesperson: %v", got)
	}
	// The other branch's salesperson, named by a Trenton caller: the wall still holds (the fake
	// matches the salesperson by id, so name the one id the fixtures carry).
	scan := &fakeScan{lines: []LowMarginSaleLine{wallLine("there", wallOther, "5d1b3f6e-0000-4000-9000-0000000000bb")}}
	h = NewHandler(scanSvc(scan), nil)
	if got := wallItems(t, wallGet(h, branchCaller(wallTrenton), "?salesperson_id=5d1b3f6e-0000-4000-9000-0000000000bb")); len(got) != 0 {
		t.Errorf("another branch's salesperson listed through the filter: %v", got)
	}
}

func TestLowMarginWall_ACallerWithNoGrantedBranchSeesNothing(t *testing.T) {
	h := NewHandler(scanSvc(wallScan()), nil)
	if got := wallItems(t, wallGet(h, branchCaller(), "")); len(got) != 0 {
		t.Errorf("a branch caller with no grant sees %v, want nothing", got)
	}
	// The zero caller is no authority at all: it must not read as global.
	if got := wallItems(t, wallGet(h, guards.StaffCaller{}, "")); len(got) != 0 {
		t.Errorf("an unresolved caller sees %v, want nothing", got)
	}
}

func TestLowMarginWall_AGlobalCallerIsUnchanged(t *testing.T) {
	scan := wallScan()
	h := NewHandler(scanSvc(scan), nil)
	if got := wallItems(t, wallGet(h, globalCaller(), "")); len(got) != 2 {
		t.Errorf("a global caller sees %v, want both branches", got)
	}
	if scan.got.BranchIDs != nil {
		t.Errorf("a global caller must not be narrowed: %v", scan.got.BranchIDs)
	}
	if got := wallItems(t, wallGet(h, globalCaller(), "?branch_id="+wallOther)); !eq(got, []string{"there"}) {
		t.Errorf("a global caller names any branch: %v", got)
	}
}
