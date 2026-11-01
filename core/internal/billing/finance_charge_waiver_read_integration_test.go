//go:build integration

// finance_charge_waiver_read_integration_test.go, wave 3 FC-1c: the Controller's read of finance charge
// waivers for the screen, by branch or by customer, with the same gate and branch reach as the run's
// doors.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1CWaiverRead
package billing

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const fcWaiversPath = "/api/v1/ar/finance-charge-waivers"

type fcWaiverList struct {
	Items []struct {
		CustomerID   string `json:"customer_id"`
		CustomerName string `json:"customer_name"`
		AccountNo    string `json:"account_no"`
		Reason       string `json:"reason"`
		Active       bool   `json:"active"`
		WaivedBy     string `json:"waived_by"`
		WaivedAt     string `json:"waived_at"`
		ClearedBy    string `json:"cleared_by"`
		ClearedAt    string `json:"cleared_at"`
	} `json:"items"`
}

func fcReadWaivers(t *testing.T, f *fcRoutes, auth erpidentity.StaffAuthority, query string) (fcWaiverList, int, string) {
	t.Helper()
	rec := f.call(auth, "GET", fcWaiversPath+query, "")
	var out fcWaiverList
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("W3-FC1c red: the waiver list is not json: %v (%s)", err, rec.Body.String())
		}
	}
	return out, rec.Code, rec.Body.String()
}

// A branch's waivers, a customer's, the cleared one kept, and who, why and when on each.
func TestIntegration_W3FC1CWaiverRead_ByBranchAndByCustomer(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	a := r.customer(r.branch, "WRA")
	b := r.customer(r.branch, "WRB")
	elsewhere := r.customer(r.other, "WRC")
	for cust, reason := range map[string]string{a: "hardship", b: "goodwill", elsewhere: "other branch"} {
		if rec := f.call(fcController, "PUT", fcWaiverURL(cust), `{"reason":"`+reason+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("W3-FC1c red: set the waiver for %s = %d (%s)", reason, rec.Code, rec.Body.String())
		}
	}
	if rec := f.call(fcController, "POST", fcWaiverURL(b)+"/clear", `{"reason":"paid up"}`); rec.Code != http.StatusOK {
		t.Fatalf("W3-FC1c red: clear = %d (%s)", rec.Code, rec.Body.String())
	}

	got, code, body := fcReadWaivers(t, f, fcController, "?branch_id="+strings.ToUpper(r.branch))
	if code != http.StatusOK || len(got.Items) != 2 {
		t.Fatalf("W3-FC1c red: the branch's waivers = %d (%s), want 2 (the other branch's is not here)", code, body)
	}
	byCust := map[string]int{got.Items[0].CustomerID: 0, got.Items[1].CustomerID: 1}
	ia, ib := got.Items[byCust[a]], got.Items[byCust[b]]
	if ia.Reason != "hardship" || !ia.Active || ia.WaivedBy != fc1Controller || ia.WaivedAt == "" || ia.ClearedAt != "" ||
		ia.CustomerName != "FC1 WRA" || !strings.HasPrefix(strings.ToUpper(ia.AccountNo), "BILV-") {
		t.Fatalf("W3-FC1c red: the active waiver reads %+v", ia)
	}
	if ib.Reason != "goodwill" || ib.Active || ib.ClearedBy != fc1Controller || ib.ClearedAt == "" {
		t.Fatalf("W3-FC1c red: the cleared waiver reads %+v, want inactive with who and when it was cleared", ib)
	}

	one, code, body := fcReadWaivers(t, f, fcController, "?customer_id="+strings.ToUpper(elsewhere))
	if code != http.StatusOK || len(one.Items) != 1 || one.Items[0].CustomerID != elsewhere || one.Items[0].Reason != "other branch" {
		t.Fatalf("W3-FC1c red: a customer's waiver = %d (%s), want that customer's one row in canonical form", code, body)
	}
	none, code, _ := fcReadWaivers(t, f, fcController, "?branch_id="+r.branch+"&customer_id="+elsewhere)
	if code != http.StatusOK || len(none.Items) != 0 {
		t.Fatalf("W3-FC1c red: a customer asked for at a branch it is not at answered %d items (code %d)", len(none.Items), code)
	}
}

// Bad ids are 400 in words, a read with neither id is 400, and the gate refuses the rest.
func TestIntegration_W3FC1CWaiverRead_BadIdsAndTheGate(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	for q, words := range map[string]string{"?branch_id=nope": "is not a uuid", "?customer_id=nope": "is not a uuid", "": "name a branch_id or a customer_id"} {
		_, code, body := fcReadWaivers(t, f, fcController, q)
		if code != http.StatusBadRequest || !strings.Contains(body, words) {
			t.Fatalf("W3-FC1c red: GET waivers%s = %d (%s), want 400 in words", q, code, body)
		}
	}
	clerk := erpidentity.StaffAuthority{RoleID: "COUNTER_SALES", Tier: erpidentity.TierFrontline, Scope: erpidentity.ScopeBranch, Branches: []string{r.branch}}
	for name, who := range map[string]erpidentity.StaffAuthority{"clerk": clerk, "hr": fcHR} {
		if _, code, body := fcReadWaivers(t, f, who, "?branch_id="+r.branch); code != http.StatusForbidden {
			t.Fatalf("W3-FC1c red: %s read the waivers = %d (%s), want 403", name, code, body)
		}
	}
	// A General Manager may read what only the Controller may set.
	if _, code, body := fcReadWaivers(t, f, fcGM, "?branch_id="+r.branch); code != http.StatusOK {
		t.Fatalf("W3-FC1c red: the General Manager's read = %d (%s), want 200", code, body)
	}
}

// The reach, at the service: a branch outside the caller's own is refused, a customer read shows only
// what the caller's branches own, an unstamped caller is refused.
func TestIntegration_W3FC1CWaiverRead_TheReachIsEnforced(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	mine := r.customer(r.branch, "WRMINE")
	theirs := r.customer(r.other, "WRTHEIRS")
	for _, c := range []string{mine, theirs} {
		if rec := f.call(fcController, "PUT", fcWaiverURL(c), `{"reason":"x"}`); rec.Code != http.StatusOK {
			t.Fatalf("W3-FC1c red: set a waiver = %d", rec.Code)
		}
	}
	scoped := identity.ContextWithStaff(r.ctx, identity.StaffContext{
		UserID: fc1Controller, RoleCode: "FINANCIAL_CONTROLLER", Scope: identity.ScopeBranch,
		Branches: map[string]bool{r.branch: true}, Status: identity.StatusActive,
	})
	if _, err := f.svc.Waivers(scoped, r.other, ""); err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("W3-FC1c red: a read at a branch outside the reach = %v, want the reach refusal", err)
	}
	got, err := f.svc.Waivers(scoped, "", theirs)
	if err != nil || len(got) != 0 {
		t.Fatalf("W3-FC1c red: a customer outside the reach answered %v, %v, want nothing", got, err)
	}
	got, err = f.svc.Waivers(scoped, "", mine)
	if err != nil || len(got) != 1 {
		t.Fatalf("W3-FC1c red: a customer inside the reach answered %v, %v, want its one waiver", got, err)
	}
	if _, err := f.svc.Waivers(r.ctx, r.branch, ""); err == nil {
		t.Fatalf("W3-FC1c red: an unstamped caller read the waivers")
	}
}
