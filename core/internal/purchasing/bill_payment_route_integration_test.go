//go:build integration

package purchasing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestIntegration_W29AR2_BillPaymentRoute proves POST /api/v1/purchasing/bills/{id}/payments:
//  1. a frontline clerk is refused with 403 (RequireManager, paying a vendor bill is
//     a money door like the post beside it);
//  2. a bill in a branch outside the caller's reach answers the same 404 as a missing
//     bill, no existence leak across the branch wall;
//  3. a re-press under the same Idempotency-Key header returns the first payment and
//     writes nothing, and a keyless press is a 400.
func TestIntegration_W29AR2_BillPaymentRoute(t *testing.T) {
	rig := newAPAgingRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	rig.svc.withCaller(ctxCaller(rig.user))
	_, billTRT := rig.createReceivedPOAndPostBill(t, "r3-trt", 25000, todayStr, todayStr)
	routeKey := "w29ar2-route-pay-" + billTRT.ID
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "purchasing.bill.payment:"+routeKey)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+routeKey)
	})

	// A posted bill in Kingston, outside a Trenton caller's reach.
	prodKGN := rig.newProduct(t, "r3-kgn")
	poKGN, err := rig.svc.CreatePO(rig.ctx, CreatePORequest{
		VendorID: rig.vendor, BranchID: branchKGN, ExpectedAt: todayStr,
		Lines: []AddLineParams{{ProductID: prodKGN, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: 40000}},
	})
	if err != nil {
		t.Fatalf("create KGN PO: %v", err)
	}
	rig.cleanupPO(t, poKGN.ID)
	poKGN, err = rig.svc.Transition(rig.ctx, poKGN.ID, StatusOrdered)
	if err != nil {
		t.Fatalf("order KGN PO: %v", err)
	}
	if _, err := rig.svc.Receive(rig.ctx, poKGN.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: poKGN.Lines[0].ID, QtyBase: 1}},
	}); err != nil {
		t.Fatalf("receive KGN PO: %v", err)
	}
	billKGNDraft, err := rig.svc.CreateBill(rig.ctx, CreateBillRequest{
		BillNo: billNo("r3-kgn"), POID: poKGN.ID, BillDate: todayStr, DueDate: todayStr,
		Lines: []BillLineParams{{POLineID: poKGN.Lines[0].ID, QtyBilledBase: 1, UnitCostCents: 40000}},
	})
	if err != nil {
		t.Fatalf("create KGN bill: %v", err)
	}
	if _, err := rig.svc.PostBill(rig.ctx, billKGNDraft.ID, PostBillRequest{AcceptVariance: true, Note: "r3 kgn"}); err != nil {
		t.Fatalf("post KGN bill: %v", err)
	}

	// Restore HTTP request-context caller resolution, the way the route serves.
	rig.svc.withCaller(identity.StaffContextFromContext)

	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	NewHandler(rig.svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) {
		return rig.user, true
	}).RegisterRoutes(mux, sg.Guards)

	post := func(auth erpidentity.StaffAuthority, billID, key, body string) *httptest.ResponseRecorder {
		sg.SetAuthority(auth)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/purchasing/bills/"+billID+"/payments", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		req.Header.Set("X-Org", testkit.OrgNorthSlug)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	clerk := erpidentity.StaffAuthority{
		RoleID:   "COUNTER_SALES",
		Tier:     erpidentity.TierFrontline,
		Scope:    erpidentity.ScopeBranch,
		Branches: []string{branchTRT},
	}
	trtMgr := erpidentity.StaffAuthority{
		RoleID:   "BRANCH_MANAGER",
		Tier:     erpidentity.TierManager,
		Scope:    erpidentity.ScopeBranch,
		Branches: []string{branchTRT},
	}

	body := `{"amount_cents":10000,"note":"r3 route"}`

	// 1. The unauthorised role is refused.
	if rec := post(clerk, billTRT.ID, "w29ar2-route-clerk", body); rec.Code != http.StatusForbidden {
		t.Fatalf("W2-9a r2 route red: clerk status = %d (%s), want 403 Forbidden", rec.Code, rec.Body.String())
	}

	// 2. A bill outside the caller's branch reach is not found, the same 404 as a
	// missing bill, and a missing bill is a 404.
	if rec := post(trtMgr, billKGNDraft.ID, "w29ar2-route-kgn", body); rec.Code != http.StatusNotFound {
		t.Fatalf("W2-9a r2 route red: out of reach bill status = %d (%s), want 404 Not Found", rec.Code, rec.Body.String())
	}
	if rec := post(trtMgr, "00000000-0000-4000-9000-0000000000aa", "w29ar2-route-missing", body); rec.Code != http.StatusNotFound {
		t.Fatalf("W2-9a r2 route red: missing bill status = %d (%s), want 404 Not Found", rec.Code, rec.Body.String())
	}

	// 3. The re-press under the same key returns the first payment and writes nothing.
	rec1 := post(trtMgr, billTRT.ID, routeKey, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("W2-9a r2 route red: first press status = %d (%s), want 200 OK", rec1.Code, rec1.Body.String())
	}
	var d1 BillDetail
	if err := json.Unmarshal(rec1.Body.Bytes(), &d1); err != nil {
		t.Fatalf("decode first press: %v", err)
	}
	if len(d1.Instalments) != 1 || d1.Instalments[0].AmountCents != 10000 {
		t.Fatalf("W2-9a r2 route red: first press instalments = %+v, want one 10000 cent instalment", d1.Instalments)
	}
	rec2 := post(trtMgr, billTRT.ID, routeKey, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("W2-9a r2 route red: re-press status = %d (%s), want 200 OK", rec2.Code, rec2.Body.String())
	}
	var d2 BillDetail
	if err := json.Unmarshal(rec2.Body.Bytes(), &d2); err != nil {
		t.Fatalf("decode re-press: %v", err)
	}
	if len(d2.Instalments) != 1 || d2.Instalments[0].AmountCents != 10000 {
		t.Fatalf("W2-9a r2 route red: re-press instalments = %+v, want the first press's single instalment", d2.Instalments)
	}
	var n int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1`, billTRT.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("W2-9a r2 route red: %d instalment rows after the re-press, want 1", n)
	}
	var jn int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+routeKey).Scan(&jn); err != nil {
		t.Fatal(err)
	}
	if jn != 1 {
		t.Fatalf("W2-9a r2 route red: %d payment journals after the re-press, want 1", jn)
	}

	// 4. A keyless press is a 400.
	if rec := post(trtMgr, billTRT.ID, "", `{"amount_cents":1000}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("W2-9a r2 route red: keyless press status = %d (%s), want 400 Bad Request", rec.Code, rec.Body.String())
	}
}
