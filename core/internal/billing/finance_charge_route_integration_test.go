//go:build integration

// finance_charge_route_integration_test.go, wave 3 FC-1b: the Controller's doors on the finance charge
// run. The gate is the leadership tier narrowed to the Controller roles (HR is leadership and not a
// Controller), every id from the caller is parsed and canonicalised, a branch outside the caller's
// reach is refused, a press then a replay charges once, and the waiver is set and cleared through the
// route and recorded.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1B
package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// fcAuditStub records every audit act, so a leg can read what was recorded and by whom.
type fcAuditStub struct {
	mu   sync.Mutex
	acts []audit.SensitiveAccess
}

func (s *fcAuditStub) Record(_ context.Context, a audit.SensitiveAccess) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acts = append(s.acts, a)
	return nil
}

func (s *fcAuditStub) count(action string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.acts {
		if a.Action == action {
			n++
		}
	}
	return n
}

// fcRoutes is the served handler over the rig, with the audit stub on the run and on the waiver
// door, the way the composition root binds them.
type fcRoutes struct {
	rig   *fcRig
	sg    *testkit.StaffGuards
	mux   *http.ServeMux
	audit *fcAuditStub
	svc   *FinanceChargeService
}

func fcRoutesNew(t *testing.T, r *fcRig) *fcRoutes {
	t.Helper()
	stub := &fcAuditStub{}
	repo := NewRepository(r.pool)
	svc := NewFinanceChargeService(
		r.writer(nil).WithAudit(stub),
		NewFinanceChargeWaiverService(repo).WithAudit(stub),
		repo)
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	NewFinanceChargeRouteHandler(svc).WithUserLinker(func(context.Context, string) (string, bool) {
		return fc1Controller, true
	}).RegisterRoutes(mux, sg.Guards)
	return &fcRoutes{rig: r, sg: sg, mux: mux, audit: stub, svc: svc}
}

func (f *fcRoutes) call(auth erpidentity.StaffAuthority, method, path, body string) *httptest.ResponseRecorder {
	f.sg.SetAuthority(auth)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.sg.Bearer(f.rig.t))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

var (
	fcController = erpidentity.StaffAuthority{RoleID: "FINANCIAL_CONTROLLER", Tier: erpidentity.TierLeadership, Scope: erpidentity.ScopeGlobal}
	fcGM         = erpidentity.StaffAuthority{RoleID: "GENERAL_MANAGER", Tier: erpidentity.TierLeadership, Scope: erpidentity.ScopeGlobal}
	fcHR         = erpidentity.StaffAuthority{RoleID: "HR", Tier: erpidentity.TierLeadership, Scope: erpidentity.ScopeGlobal}
)

const (
	fcRunPath    = "/api/v1/ar/finance-charges"
	fcWaiverPath = "/api/v1/ar/customers/%s/finance-charge-waiver"
)

func fcWaiverURL(customer string) string { return strings.Replace(fcWaiverPath, "%s", customer, 1) }

// The gate: only the Controller roles press or read a run; a manager, a clerk and HR are refused in
// every verb, and a refused caller leaves no row, no document and no audit act.
func TestIntegration_W3FC1B_TheGateRefusesEveryoneButTheController(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	cust := r.customer(r.branch, "GATE")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "GATE")

	branchMgr := erpidentity.StaffAuthority{RoleID: "BRANCH_MANAGER", Tier: erpidentity.TierManager, Scope: erpidentity.ScopeBranch, Branches: []string{r.branch}}
	clerk := erpidentity.StaffAuthority{RoleID: "COUNTER_SALES", Tier: erpidentity.TierFrontline, Scope: erpidentity.ScopeBranch, Branches: []string{r.branch}}
	press := `{"branch_id":"` + r.branch + `","month":"` + fc1Month + `"}`
	for name, who := range map[string]erpidentity.StaffAuthority{"clerk": clerk, "branch manager": branchMgr, "hr": fcHR} {
		for _, c := range []struct{ method, path, body string }{
			{"POST", fcRunPath, press},
			{"GET", fcRunPath + "?branch_id=" + r.branch, ""},
			{"PUT", fcWaiverURL(cust), `{"reason":"x"}`},
			{"POST", fcWaiverURL(cust) + "/clear", `{"reason":"x"}`},
		} {
			if rec := f.call(who, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Fatalf("W3-FC1b red: %s on %s %s = %d (%s), want 403", name, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
	}
	if rows := r.chargeRows(r.branch); len(rows) != 0 {
		t.Fatalf("W3-FC1b red: a refused caller left charge rows %v", rows)
	}
	if n := r.chargeInvoiceCount(r.branch); n != 0 {
		t.Fatalf("W3-FC1b red: a refused caller raised %d charge documents", n)
	}
	if _, ok, err := NewRepository(r.pool).FinanceChargeWaiver(r.ctx, cust); err != nil || ok {
		t.Fatalf("W3-FC1b red: a refused caller left a waiver (%v, %v)", ok, err)
	}
	if f.audit.count(ActFinanceChargeRun)+f.audit.count(ActFinanceChargeWaive) != 0 {
		t.Fatalf("W3-FC1b red: a refused caller was audited as having acted")
	}
}

// Every id the caller sends is parsed: a bad branch, month, customer or reason is a 400 in words and
// never a 500, and nothing is written.
func TestIntegration_W3FC1B_BadInputIsA400InWords(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	cust := r.customer(r.branch, "BAD")
	for _, c := range []struct{ name, method, path, body, words string }{
		{"press, branch not a uuid", "POST", fcRunPath, `{"branch_id":"not-a-branch","month":"` + fc1Month + `"}`, "not a uuid"},
		{"press, no branch", "POST", fcRunPath, `{"month":"` + fc1Month + `"}`, "branch_id is required"},
		{"press, no month", "POST", fcRunPath, `{"branch_id":"` + r.branch + `"}`, "YYYY-MM"},
		{"press, month 2026-13", "POST", fcRunPath, `{"branch_id":"` + r.branch + `","month":"2026-13"}`, "YYYY-MM"},
		{"press, body not json", "POST", fcRunPath, `nope`, ""},
		{"list, branch not a uuid", "GET", fcRunPath + "?branch_id=not-a-branch", "", "not a uuid"},
		{"list, no branch", "GET", fcRunPath, "", "branch_id is required"},
		{"waive, customer not a uuid", "PUT", fcWaiverURL("not-a-customer"), `{"reason":"x"}`, "not a uuid"},
		{"waive, no reason", "PUT", fcWaiverURL(cust), `{"reason":"  "}`, "reason"},
		{"clear, customer not a uuid", "POST", fcWaiverURL("not-a-customer") + "/clear", `{"reason":"x"}`, "not a uuid"},
		{"clear, nothing to clear", "POST", fcWaiverURL(cust) + "/clear", `{"reason":"x"}`, "no active finance charge waiver"},
	} {
		rec := f.call(fcController, c.method, c.path, c.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("W3-FC1b red: %s = %d (%s), want 400", c.name, rec.Code, rec.Body.String())
		}
		if c.words != "" && !strings.Contains(rec.Body.String(), c.words) {
			t.Fatalf("W3-FC1b red: %s answered %q, want words naming %q", c.name, rec.Body.String(), c.words)
		}
	}
	if rows := r.chargeRows(r.branch); len(rows) != 0 {
		t.Fatalf("W3-FC1b red: a bad request wrote charge rows %v", rows)
	}
}

// A press of a month that has not ended is refused in words (422), never run.
func TestIntegration_W3FC1B_AMonthThatHasNotEndedIsRefused(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	rec := f.call(fcController, "POST", fcRunPath, `{"branch_id":"`+r.branch+`","month":"2099-01"}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "2099-02-01") {
		t.Fatalf("W3-FC1b red: a future month = %d (%s), want 422 naming the next house day", rec.Code, rec.Body.String())
	}
}

// The reach: a branch outside the caller's own branches is refused (403), at the service, whichever
// door called it. An unstamped caller is refused too: the route never runs as an internal caller.
func TestIntegration_W3FC1B_ABranchOutsideTheReachIsRefused(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	cust := r.customer(r.branch, "REACH")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "REACH")

	scoped := identity.ContextWithStaff(r.ctx, identity.StaffContext{
		UserID: fc1Controller, RoleCode: "FINANCIAL_CONTROLLER", Scope: identity.ScopeBranch,
		Branches: map[string]bool{r.other: true}, Status: identity.StatusActive,
	})
	if _, err := f.svc.Press(scoped, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month}); err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("W3-FC1b red: a press at a branch outside the reach = %v, want the reach refusal", err)
	}
	if _, err := f.svc.Runs(scoped, r.branch); err == nil {
		t.Fatalf("W3-FC1b red: a read of a branch outside the reach was answered")
	}
	if _, err := f.svc.Press(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month}); err == nil {
		t.Fatalf("W3-FC1b red: an unstamped caller pressed a run")
	}
	// Inside the reach it runs.
	if _, err := f.svc.Press(identity.ContextWithStaff(r.ctx, identity.StaffContext{
		UserID: fc1Controller, RoleCode: "FINANCIAL_CONTROLLER", Scope: identity.ScopeBranch,
		Branches: map[string]bool{r.branch: true}, Status: identity.StatusActive,
	}), FinanceChargeRequest{BranchID: r.branch, Month: fc1Month}); err != nil {
		t.Fatalf("W3-FC1b red: a press inside the reach: %v", err)
	}
	// And through the served route a branch scoped leadership caller is refused at the other branch.
	scopedAuth := erpidentity.StaffAuthority{RoleID: "FINANCIAL_CONTROLLER", Tier: erpidentity.TierLeadership, Scope: erpidentity.ScopeBranch, Branches: []string{r.other}}
	if rec := f.call(scopedAuth, "POST", fcRunPath, `{"branch_id":"`+r.branch+`","month":"`+fc1Month+`"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("W3-FC1b red: a branch scoped caller at another branch = %d (%s), want 403", rec.Code, rec.Body.String())
	}
}

// A press, then a replay, then the same branch typed in upper case: one charge, answered again.
// The press is recorded, the read lists the run with its charge, and the branch id is canonical in
// the answer.
func TestIntegration_W3FC1B_APressThenAReplayChargesOnce(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	cust := r.customer(r.branch, "PRESS")
	r.tradeInvoice(cust, r.branch, fc1Due31, 1_000_000, "PRESS")
	body := `{"branch_id":"` + r.branch + `","month":"` + fc1Month + `"}`

	rec := f.call(fcController, "POST", fcRunPath, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("W3-FC1b red: the press = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var first FinanceChargeRunResult
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil || first.Charged != 1 || first.ChargedCents != 15_000 {
		t.Fatalf("W3-FC1b red: the press answered %+v (%v), want 1 charge of 15000", first, err)
	}
	if f.audit.count(ActFinanceChargeRun) != 1 {
		t.Fatalf("W3-FC1b red: the press was audited %d times, want 1", f.audit.count(ActFinanceChargeRun))
	}

	for name, b := range map[string]string{
		"replay":     body,
		"upper case": `{"branch_id":"` + strings.ToUpper(r.branch) + `","month":"` + fc1Month + `"}`,
	} {
		rec = f.call(fcController, "POST", fcRunPath, b)
		var again FinanceChargeRunResult
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &again) != nil || again.Charged != 0 || again.AlreadyCharged != 1 || again.BranchID != r.branch {
			t.Fatalf("W3-FC1b red: the %s = %d %+v (%s), want 200, nothing charged, 1 already charged, branch %s", name, rec.Code, again, rec.Body.String(), r.branch)
		}
	}
	if n := r.chargeInvoiceCount(r.branch); n != 1 {
		t.Fatalf("W3-FC1b red: %d charge documents after a press and two replays, want 1", n)
	}

	// The read: the branch's runs, newest month first, each with its charges.
	rec = f.call(fcController, "GET", fcRunPath+"?branch_id="+strings.ToUpper(r.branch), "")
	var list struct {
		Items []FinanceChargeRunMonth `json:"items"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Items) != 1 {
		t.Fatalf("W3-FC1b red: the read = %d (%s), want one month", rec.Code, rec.Body.String())
	}
	m := list.Items[0]
	if m.Month != fc1Month || m.BranchID != r.branch || m.Charged != 1 || m.ChargedCents != 15_000 || len(m.Charges) != 1 {
		t.Fatalf("W3-FC1b red: the month read as %+v, want %s at %s with one charge of 15000", m, fc1Month, r.branch)
	}
	c := m.Charges[0]
	if c.CustomerID != cust || c.PastDueCents != 1_000_000 || c.ChargeCents != 15_000 || c.InvoiceID == "" || c.InvoiceNo == "" {
		t.Fatalf("W3-FC1b red: the charge read as %+v", c)
	}
}

// The waiver through the route: set by the Controller alone (a General Manager passes the route's
// gate and is refused in words), the customer is then skipped by a press, the clear releases the
// customer, and both acts are recorded on the row and in the audit trail.
func TestIntegration_W3FC1B_TheWaiverIsSetAndClearedThroughTheRoute(t *testing.T) {
	r := fcRigNew(t)
	f := fcRoutesNew(t, r)
	waived := r.customer(r.branch, "WAIVED")
	r.tradeInvoice(waived, r.branch, fc1Due31, 1_000_000, "WAIVED")

	rec := f.call(fcGM, "PUT", fcWaiverURL(waived), `{"reason":"goodwill"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Financial Controller") {
		t.Fatalf("W3-FC1b red: a General Manager's waiver = %d (%s), want 403 in words naming the Controller", rec.Code, rec.Body.String())
	}
	if _, ok, _ := NewRepository(r.pool).FinanceChargeWaiver(r.ctx, waived); ok {
		t.Fatalf("W3-FC1b red: a refused waiver left a row")
	}

	rec = f.call(fcController, "PUT", fcWaiverURL(strings.ToUpper(waived)), `{"reason":"  goodwill, settled by phone  "}`)
	var view FinanceChargeWaiverView
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || !view.Active || view.CustomerID != waived || view.Reason != "goodwill, settled by phone" || view.WaivedBy != fc1Controller {
		t.Fatalf("W3-FC1b red: the waiver = %d %+v (%s), want it active, canonical, trimmed and by %s", rec.Code, view, rec.Body.String(), fc1Controller)
	}
	if f.audit.count(ActFinanceChargeWaive) != 1 {
		t.Fatalf("W3-FC1b red: the waiver was audited %d times, want 1", f.audit.count(ActFinanceChargeWaive))
	}

	// A press skips the waived customer.
	press := `{"branch_id":"` + r.branch + `","month":"` + fc1Month + `"}`
	rec = f.call(fcController, "POST", fcRunPath, press)
	var run FinanceChargeRunResult
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &run) != nil || run.Charged != 0 {
		t.Fatalf("W3-FC1b red: a press with the only customer waived = %d %+v, want nothing charged", rec.Code, run)
	}

	// The clear releases the customer: recorded, and the next press charges.
	rec = f.call(fcController, "POST", fcWaiverURL(waived)+"/clear", `{"reason":"balance is not goodwill after all"}`)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.Active || view.ClearedBy != fc1Controller || view.ClearedAt == "" {
		t.Fatalf("W3-FC1b red: the clear = %d %+v (%s), want it released by %s", rec.Code, view, rec.Body.String(), fc1Controller)
	}
	if f.audit.count(ActFinanceChargeWaiveCleared) != 1 {
		t.Fatalf("W3-FC1b red: the clear was audited %d times, want 1", f.audit.count(ActFinanceChargeWaiveCleared))
	}
	rec = f.call(fcController, "POST", fcRunPath, press)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &run) != nil || run.Charged != 1 || run.ChargedCents != 15_000 {
		t.Fatalf("W3-FC1b red: a press after the clear = %d %+v, want the customer charged 15000", rec.Code, run)
	}
}
