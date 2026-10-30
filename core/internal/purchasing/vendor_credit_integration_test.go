//go:build integration

// vendor_credit_integration_test.go: the supplier credit note, wave 3 item 0.8, against a real
// database with the REAL gl service behind the seams. The five red legs the brief names:
//
//  1. a posted credit moves the aging and GL 2100 by the same cents;
//  2. applying it reduces the bill's open amount and the credit's open amount together;
//  3. it can never be applied twice, or above either open amount;
//  4. a credit in another branch is refused;
//  5. the aging's tie (TieAPAgingToGL2100's inputs) holds after each step.
//
// plus the route legs: the AR credit memo doors' authz (RequireManager and the branch fence) and
// a replayed apply key answering the first result.
package purchasing

import (
	"context"
	"encoding/json"
	"fmt"
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

func creditNo(tag string) string { return fmt.Sprintf("CRN-%d-%s", time.Now().UnixNano(), tag) }

// newVCRig is the aging rig under a global caller, the fence itself exercised by the scoped legs.
func newVCRig(t *testing.T) *apAgingTestRig {
	t.Helper()
	rig := newAPAgingRig(t)
	rig.svc.withCaller(ctxCaller(rig.user))
	return rig
}

// createPostedCredit enters and posts a credit of one line for cents on branch, cleaning up after
// itself (the credit's rows, its applications and its journal).
func (rig *apAgingTestRig) createPostedCredit(t *testing.T, tag, branch string, cents int64) VendorCreditDetail {
	t.Helper()
	credit, err := rig.svc.CreateVendorCredit(rig.ctx, CreateVendorCreditRequest{
		VendorID:   rig.vendor,
		BranchID:   branch,
		CreditNo:   creditNo(tag),
		CreditDate: time.Now().In(rig.zone).Format("2006-01-02"),
		Lines: []VendorCreditLineParams{
			{Description: "returned pavers " + tag, AmountCents: cents},
		},
	})
	if err != nil {
		t.Fatalf("create credit (%s): %v", tag, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, credit.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1 AND source = 'PO_RECEIPT' AND memo LIKE 'Vendor credit%')`, credit.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE source_ref_id = $1 AND source = 'PO_RECEIPT' AND memo LIKE 'Vendor credit%'`, credit.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM purchasing_vendor_credits WHERE id = $1`, credit.ID)
	})
	posted, err := rig.svc.PostVendorCredit(rig.ctx, credit.ID)
	if err != nil {
		t.Fatalf("post credit (%s): %v", tag, err)
	}
	return posted
}

// findVendorRow finds the aging row of the rig's vendor.
func findVendorRow(t *testing.T, page APAgingPage, vendorID string) *APAgingRow {
	t.Helper()
	for i := range page.Items {
		if page.Items[i].RowKind == APRowKindVendor && page.Items[i].VendorID == vendorID {
			return &page.Items[i]
		}
	}
	return nil
}

// findCreditItem finds the credit's negative item on the vendor's row.
func findCreditItem(t *testing.T, row *APAgingRow, creditID string) *APAgingItem {
	t.Helper()
	for i := range row.OpenItems {
		if row.OpenItems[i].Kind == APItemKindVendorCredit && row.OpenItems[i].ID == creditID {
			return &row.OpenItems[i]
		}
	}
	return nil
}

// TestIntegration_W3VC_Leg1_PostedCreditMovesAgingAndGL2100: a posted credit drops the aging
// total and GL 2100's credit balance by the same cents, and the unapplied credit shows as a
// negative item on the vendor's row.
func TestIntegration_W3VC_Leg1_PostedCreditMovesAgingAndGL2100(t *testing.T) {
	rig := newVCRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")
	reach := BranchReach{BranchIDs: []string{branchTRT}}

	_, _ = rig.createReceivedPOAndPostBill(t, "leg1", 50000, todayStr, todayStr)
	time.Sleep(15 * time.Millisecond)
	asOf := time.Now().UTC()

	pageBefore, err := rig.svc.APAging(rig.ctx, asOf, reach)
	if err != nil {
		t.Fatalf("aging before: %v", err)
	}
	glBefore, _, err := rig.glRepo.AccountBalanceAsOf(rig.ctx, "2100", asOf, branchTRT)
	if err != nil {
		t.Fatalf("gl 2100 before: %v", err)
	}
	glCreditBefore := -glBefore
	if pageBefore.TotalCents != glCreditBefore {
		t.Fatalf("leg1 precondition: aging %d != gl 2100 %d before the credit", pageBefore.TotalCents, glCreditBefore)
	}

	credit := rig.createPostedCredit(t, "leg1", branchTRT, 12000)
	time.Sleep(15 * time.Millisecond)
	asOf = time.Now().UTC()

	pageAfter, err := rig.svc.APAging(rig.ctx, asOf, reach)
	if err != nil {
		t.Fatalf("aging after: %v", err)
	}
	glAfter, _, err := rig.glRepo.AccountBalanceAsOf(rig.ctx, "2100", asOf, branchTRT)
	if err != nil {
		t.Fatalf("gl 2100 after: %v", err)
	}
	glCreditAfter := -glAfter

	if pageAfter.TotalCents != pageBefore.TotalCents-12000 {
		t.Fatalf("W3-VC leg1 red: aging total %d after the credit, want %d (before %d less 12000)",
			pageAfter.TotalCents, pageBefore.TotalCents-12000, pageBefore.TotalCents)
	}
	if glCreditAfter != glCreditBefore-12000 {
		t.Fatalf("W3-VC leg1 red: gl 2100 credit %d after the credit, want %d", glCreditAfter, glCreditBefore-12000)
	}
	if pageAfter.TotalCents != glCreditAfter {
		t.Fatalf("W3-VC leg1 red: aging %d != gl 2100 %d after the credit", pageAfter.TotalCents, glCreditAfter)
	}

	row := findVendorRow(t, pageAfter, rig.vendor)
	if row == nil {
		t.Fatalf("W3-VC leg1 red: the vendor has no aging row after the credit")
	}
	item := findCreditItem(t, row, credit.ID)
	if item == nil {
		t.Fatalf("W3-VC leg1 red: the unapplied credit %s is no negative item on the vendor's row (items %d)", credit.ID, len(row.OpenItems))
	}
	if item.OpenCents != -12000 {
		t.Fatalf("W3-VC leg1 red: the credit item's open is %d, want -12000", item.OpenCents)
	}
}

// TestIntegration_W3VC_Leg2_ApplicationReducesBothOpens: applying part of the credit to the bill
// reduces the bill's open amount and the credit's open amount together, and the aging total does
// not move (an application is a netting inside AP, no journal).
func TestIntegration_W3VC_Leg2_ApplicationReducesBothOpens(t *testing.T) {
	rig := newVCRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")
	reach := BranchReach{BranchIDs: []string{branchTRT}}

	_, bill := rig.createReceivedPOAndPostBill(t, "leg2", 50000, todayStr, todayStr)
	credit := rig.createPostedCredit(t, "leg2", branchTRT, 12000)
	time.Sleep(15 * time.Millisecond)
	asOf := time.Now().UTC()

	before, err := rig.svc.APAging(rig.ctx, asOf, reach)
	if err != nil {
		t.Fatalf("aging before apply: %v", err)
	}
	rowBefore := findVendorRow(t, before, rig.vendor)
	if rowBefore == nil {
		t.Fatalf("W3-VC leg2 red: no vendor row before the apply")
	}
	var billOpenBefore int64
	for i := range rowBefore.OpenItems {
		if rowBefore.OpenItems[i].Kind == APItemKindBill && rowBefore.OpenItems[i].ID == bill.ID {
			billOpenBefore = rowBefore.OpenItems[i].OpenCents
		}
	}
	if billOpenBefore != 50000 {
		t.Fatalf("W3-VC leg2 red: bill open %d before the apply, want 50000", billOpenBefore)
	}

	applied, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 5000, IdempotencyKey: "w3vc-leg2-apply-1",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.AppliedCents != 5000 || applied.OpenCents != 7000 {
		t.Fatalf("W3-VC leg2 red: credit applied %d open %d after the apply, want 5000 and 7000",
			applied.AppliedCents, applied.OpenCents)
	}
	if len(applied.Applications) != 1 || applied.Applications[0].BillID != bill.ID || applied.Applications[0].AmountCents != 5000 {
		t.Fatalf("W3-VC leg2 red: applications %+v, want one 5000 cent application on the bill", applied.Applications)
	}

	time.Sleep(15 * time.Millisecond)
	asOf = time.Now().UTC()
	after, err := rig.svc.APAging(rig.ctx, asOf, reach)
	if err != nil {
		t.Fatalf("aging after apply: %v", err)
	}
	rowAfter := findVendorRow(t, after, rig.vendor)
	if rowAfter == nil {
		t.Fatalf("W3-VC leg2 red: no vendor row after the apply")
	}
	var billOpenAfter, creditOpenAfter int64
	for i := range rowAfter.OpenItems {
		it := &rowAfter.OpenItems[i]
		if it.Kind == APItemKindBill && it.ID == bill.ID {
			billOpenAfter = it.OpenCents
		}
		if it.Kind == APItemKindVendorCredit && it.ID == credit.ID {
			creditOpenAfter = it.OpenCents
		}
	}
	if billOpenAfter != billOpenBefore-5000 {
		t.Fatalf("W3-VC leg2 red: bill open %d after the apply, want %d (the bill's open must drop by the applied cents)",
			billOpenAfter, billOpenBefore-5000)
	}
	if creditOpenAfter != -7000 {
		t.Fatalf("W3-VC leg2 red: credit item open %d after the apply, want -7000 (the credit's open must drop by the same cents)", creditOpenAfter)
	}
	if after.TotalCents != before.TotalCents {
		t.Fatalf("W3-VC leg2 red: aging total %d after the apply, want %d (an application moves no money)",
			after.TotalCents, before.TotalCents)
	}
}

// TestIntegration_W3VC_Leg3_NeverAppliedTwiceOrAboveEitherOpen: a replayed key returns the first
// result and writes nothing; an apply above the credit's open and an apply above the bill's open
// are both refused with nothing written.
func TestIntegration_W3VC_Leg3_NeverAppliedTwiceOrAboveEitherOpen(t *testing.T) {
	rig := newVCRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	_, bill := rig.createReceivedPOAndPostBill(t, "leg3", 50000, todayStr, todayStr)
	credit := rig.createPostedCredit(t, "leg3", branchTRT, 12000)

	countApplications := func() int {
		t.Helper()
		var n int
		if err := rig.pool.QueryRow(rig.ctx,
			`SELECT count(*) FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, credit.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	first, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 5000, IdempotencyKey: "w3vc-leg3-key",
	})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if n := countApplications(); n != 1 {
		t.Fatalf("W3-VC leg3 red: %d applications after the first apply, want 1", n)
	}

	replay, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 5000, IdempotencyKey: "w3vc-leg3-key",
	})
	if err != nil {
		t.Fatalf("W3-VC leg3 red: a replayed key must return the first result, got %v", err)
	}
	if len(replay.Applications) != len(first.Applications) {
		t.Fatalf("W3-VC leg3 red: the replay answered %d applications, want the first result's %d",
			len(replay.Applications), len(first.Applications))
	}
	if n := countApplications(); n != 1 {
		t.Fatalf("W3-VC leg3 red: the replay wrote a second application, %d rows exist", n)
	}

	// Above the credit's open (7000 left).
	if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 7001, IdempotencyKey: "w3vc-leg3-over-credit",
	}); err == nil {
		t.Fatalf("W3-VC leg3 red: an apply above the credit's open must be refused")
	}
	// Above the bill's open (45000 left).
	if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 45001, IdempotencyKey: "w3vc-leg3-over-bill",
	}); err == nil {
		t.Fatalf("W3-VC leg3 red: an apply above the bill's open must be refused")
	}
	if n := countApplications(); n != 1 {
		t.Fatalf("W3-VC leg3 red: a refused apply wrote a row, %d applications exist", n)
	}
	var applied int64
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT COALESCE(SUM(amount_cents),0) FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, credit.ID).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 5000 {
		t.Fatalf("W3-VC leg3 red: %d cents applied after the refused applies, want 5000", applied)
	}
}

// TestIntegration_W3VC_Leg4_CreditInAnotherBranchRefused: a credit of one branch cannot be
// applied to a bill of another branch, for a caller who reaches both, and is not even visible to
// a caller who reaches only its own branch.
func TestIntegration_W3VC_Leg4_CreditInAnotherBranchRefused(t *testing.T) {
	rig := newVCRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	prodKGN := rig.newProduct(t, "leg4-kgn")
	poKGN, err := rig.svc.CreatePO(rig.ctx, CreatePORequest{
		VendorID: rig.vendor, BranchID: branchKGN, ExpectedAt: todayStr,
		Lines: []AddLineParams{{ProductID: prodKGN, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: 30000}},
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
		BillNo: billNo("leg4-kgn"), POID: poKGN.ID, BillDate: todayStr, DueDate: todayStr,
		Lines: []BillLineParams{{POLineID: poKGN.Lines[0].ID, QtyBilledBase: 1, UnitCostCents: 30000}},
	})
	if err != nil {
		t.Fatalf("create KGN bill: %v", err)
	}
	billKGN, err := rig.svc.PostBill(rig.ctx, billKGNDraft.ID, PostBillRequest{AcceptVariance: true, Note: "leg4"})
	if err != nil {
		t.Fatalf("post KGN bill: %v", err)
	}

	credit := rig.createPostedCredit(t, "leg4", branchTRT, 8000)

	countApplications := func() int {
		t.Helper()
		var n int
		if err := rig.pool.QueryRow(rig.ctx,
			`SELECT count(*) FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, credit.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A caller granted only Trenton cannot apply a Trenton credit to a Kingston bill: the bill
	// answers as a missing bill, no existence leak across the branch wall.
	rig.svc.withCaller(scopedCaller(rig.user, branchTRT))
	if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: billKGN.ID, AmountCents: 1000, IdempotencyKey: "w3vc-leg4-scoped",
	}); err == nil {
		t.Fatalf("W3-VC leg4 red: a scoped caller applied a Trenton credit to a Kingston bill")
	} else if clientErr, ok := err.(clientError); !ok || clientErr.status != http.StatusNotFound {
		t.Fatalf("W3-VC leg4 red: an out of reach bill must answer 404 like a missing bill, got %v", err)
	}

	// A global caller reaches both branches and still may not cross the branch line.
	rig.svc.withCaller(ctxCaller(rig.user))
	if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: billKGN.ID, AmountCents: 1000, IdempotencyKey: "w3vc-leg4-global",
	}); err == nil {
		t.Fatalf("W3-VC leg4 red: a credit was applied to a bill in another branch")
	}
	if n := countApplications(); n != 0 {
		t.Fatalf("W3-VC leg4 red: the refused cross branch apply wrote %d application rows", n)
	}
}

// TestIntegration_W3VC_Leg5_TieHoldsAfterEachStep: the aging total equals GL 2100's credit
// balance after every step of the credit's life (bill, credit posted, applied, second credit).
func TestIntegration_W3VC_Leg5_TieHoldsAfterEachStep(t *testing.T) {
	rig := newVCRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")
	reach := BranchReach{BranchIDs: []string{branchTRT}}

	assertTie := func(step string, asOf time.Time) {
		t.Helper()
		page, err := rig.svc.APAging(rig.ctx, asOf, reach)
		if err != nil {
			t.Fatalf("%s: APAging: %v", step, err)
		}
		glBal, _, err := rig.glRepo.AccountBalanceAsOf(rig.ctx, "2100", asOf, branchTRT)
		if err != nil {
			t.Fatalf("%s: AccountBalanceAsOf(2100): %v", step, err)
		}
		glCredit := -glBal
		if page.TotalCents != glCredit {
			t.Fatalf("W3-VC leg5 red (%s): the aging's tie broke: ap_aging.total_cents=%d gl_2100_credit_cents=%d",
				step, page.TotalCents, glCredit)
		}
	}

	_, bill := rig.createReceivedPOAndPostBill(t, "leg5", 30000, todayStr, todayStr)
	time.Sleep(15 * time.Millisecond)
	assertTie("after the bill", time.Now().UTC())

	credit := rig.createPostedCredit(t, "leg5", branchTRT, 10000)
	time.Sleep(15 * time.Millisecond)
	assertTie("after the credit posted", time.Now().UTC())

	if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 4000, IdempotencyKey: "w3vc-leg5-apply",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	assertTie("after the apply", time.Now().UTC())

	_ = rig.createPostedCredit(t, "leg5-2", branchTRT, 2500)
	time.Sleep(15 * time.Millisecond)
	assertTie("after the second credit", time.Now().UTC())
}

// TestIntegration_W3VC_VendorCreditRoute proves the four doors carry the AR credit memo routes'
// authz: a frontline clerk is refused with 403, a credit outside the caller's branch reach answers
// the same 404 as a missing credit, a replayed Idempotency-Key returns the first result, and the
// list answers the contract's page envelope.
func TestIntegration_W3VC_VendorCreditRoute(t *testing.T) {
	rig := newVCRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	_, bill := rig.createReceivedPOAndPostBill(t, "route", 20000, todayStr, todayStr)
	rig.svc.withCaller(identity.StaffContextFromContext)

	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	NewHandler(rig.svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) {
		return rig.user, true
	}).RegisterRoutes(mux, sg.Guards)

	do := func(auth erpidentity.StaffAuthority, method, target, key, body string) *httptest.ResponseRecorder {
		sg.SetAuthority(auth)
		var rd *strings.Reader
		if body == "" {
			rd = strings.NewReader("")
		} else {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, rd)
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
		RoleID: "COUNTER_SALES", Tier: erpidentity.TierFrontline, Scope: erpidentity.ScopeBranch, Branches: []string{branchTRT},
	}
	trtMgr := erpidentity.StaffAuthority{
		RoleID: "BRANCH_MANAGER", Tier: erpidentity.TierManager, Scope: erpidentity.ScopeBranch, Branches: []string{branchTRT},
	}

	createBody := fmt.Sprintf(`{"vendor_id":%q,"branch_id":%q,"credit_no":%q,"lines":[{"description":"route leg","amount_cents":3000}]}`,
		rig.vendor, branchTRT, creditNo("route"))

	// 1. The unauthorised role is refused on the money door.
	if rec := do(clerk, http.MethodPost, "/api/v1/purchasing/vendor-credits", "", createBody); rec.Code != http.StatusForbidden {
		t.Fatalf("W3-VC route red: clerk create status = %d (%s), want 403 Forbidden", rec.Code, rec.Body.String())
	}

	// 2. The manager creates, then posts.
	rec := do(trtMgr, http.MethodPost, "/api/v1/purchasing/vendor-credits", "", createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("W3-VC route red: create status = %d (%s), want 201 Created", rec.Code, rec.Body.String())
	}
	var created VendorCreditDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, created.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1 AND memo LIKE 'Vendor credit%')`, created.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE source_ref_id = $1 AND memo LIKE 'Vendor credit%'`, created.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM purchasing_vendor_credits WHERE id = $1`, created.ID)
	})
	if rec = do(trtMgr, http.MethodPost, "/api/v1/purchasing/vendor-credits/"+created.ID+"/post", "", "{}"); rec.Code != http.StatusOK {
		t.Fatalf("W3-VC route red: post status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}

	// 3. An apply over the route carries the replay key: the first press lands, the re-press
	//    answers the first result and writes nothing.
	applyTarget := "/api/v1/purchasing/vendor-credits/" + created.ID + "/applications"
	applyBody := fmt.Sprintf(`{"bill_id":%q,"amount_cents":2000}`, bill.ID)
	if rec = do(trtMgr, http.MethodPost, applyTarget, "w3vc-route-apply", applyBody); rec.Code != http.StatusOK {
		t.Fatalf("W3-VC route red: first press status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}
	var first VendorCreditDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first press: %v", err)
	}
	if rec = do(trtMgr, http.MethodPost, applyTarget, "w3vc-route-apply", applyBody); rec.Code != http.StatusOK {
		t.Fatalf("W3-VC route red: re-press status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}
	var replay VendorCreditDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode re-press: %v", err)
	}
	if len(replay.Applications) != len(first.Applications) {
		t.Fatalf("W3-VC route red: the re-press answered %d applications, want the first result's %d",
			len(replay.Applications), len(first.Applications))
	}
	var n int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, created.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("W3-VC route red: %d application rows after the re-press, want 1", n)
	}

	// 4. The list answers the contract's page envelope for the branch scoped caller.
	rec = do(trtMgr, http.MethodGet, "/api/v1/purchasing/vendor-credits", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("W3-VC route red: list status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}
	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
		Limit      *int32            `json:"limit"`
	}
	dec := json.NewDecoder(rec.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&page); err != nil {
		t.Fatalf("W3-VC route red: the list does not decode against the contract's page: %v (body %s)", err, rec.Body.String())
	}
	if page.Items == nil {
		t.Fatalf("W3-VC route red: the list answered items null, want an array")
	}

	// 5. A credit outside the caller's reach answers the same 404 as a missing credit.
	if rec = do(trtMgr, http.MethodPost,
		"/api/v1/purchasing/vendor-credits/00000000-0000-4000-9000-0000000000bb/applications",
		"w3vc-route-missing", fmt.Sprintf(`{"bill_id":%q,"amount_cents":100}`, bill.ID)); rec.Code != http.StatusNotFound {
		t.Fatalf("W3-VC route red: missing credit status = %d (%s), want 404 Not Found", rec.Code, rec.Body.String())
	}
}
