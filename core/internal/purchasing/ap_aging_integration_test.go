//go:build integration

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

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/landedcost"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type itLandedPayables struct{ svc *landedcost.Service }

func (a itLandedPayables) OpenPayablesAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]LandedCostPayable, error) {
	rows, err := a.svc.OpenPayablesAsOf(ctx, asOf, branchIDs, global)
	if err != nil {
		return nil, err
	}
	out := make([]LandedCostPayable, len(rows))
	for i, r := range rows {
		out[i] = LandedCostPayable{
			ID:               r.ID,
			POID:             r.POID,
			PORef:            r.PORef,
			BranchID:         r.BranchID,
			Currency:         r.Currency,
			FreightCadCents:  r.FreightCadCents,
			DutyCadCents:     r.DutyCadCents,
			HandlingCadCents: r.HandlingCadCents,
			TotalCadCents:    r.TotalCadCents,
			ChargeKind:       r.ChargeKind,
			PayeeLabel:       r.PayeeLabel,
			CreatedAt:        r.CreatedAt,
			UpdatedAt:        r.UpdatedAt,
		}
	}
	return out, nil
}

// itLandedClock mirrors itGLClock for landedcost's journal clock seam, so a landed
// cost payable counts from its apply journal's effective instant exactly as the
// production wiring answers it.
type itLandedClock struct{ svc *gl.Service }

func (a itLandedClock) JournalEffectiveInstants(ctx context.Context, journalIDs []string) (map[string]time.Time, error) {
	return a.svc.JournalEffectiveInstants(ctx, journalIDs)
}

func (a itLandedClock) JournalEffectiveInstantByKey(ctx context.Context, key string) (time.Time, bool, error) {
	return a.svc.JournalEffectiveInstantByKey(ctx, key)
}

type apAgingTestRig struct {
	*harness
	landedSvc *landedcost.Service
	glRepo    *gl.Repository
	zone      *time.Location
}

func newAPAgingRig(t *testing.T) *apAgingTestRig {
	t.Helper()
	h := newHarness(t)
	_, _ = h.pool.Exec(h.ctx, `DELETE FROM landed_costs WHERE id <> 'd0580000-0000-4000-9000-000000000099'::uuid`)
	_, _ = h.pool.Exec(h.ctx, `DELETE FROM gl_journals WHERE idempotency_key LIKE 'landedcost.%'`)
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load dealer zone: %v", err)
	}
	glRepo := gl.NewRepository(h.pool)
	glSvc := gl.NewService(glRepo)
	invSvc := inventory.NewService(inventory.NewRepository(h.pool)).
		WithGLPoster(invGLBridge{svc: glSvc}).
		WithRuleResolver(invRuleBridge{svc: glSvc})
	landedSvc := landedcost.NewService(landedcost.NewRepository(h.pool)).
		WithCapitalizer(landedBridge{svc: invSvc}).
		WithGLPoster(landedGLBridge{svc: glSvc}).
		WithJournalClock(itLandedClock{svc: glSvc})

	h.svc.WithLandedCoster(itLanded{svc: landedSvc}).
		WithLandedCostPayables(itLandedPayables{svc: landedSvc})

	return &apAgingTestRig{
		harness:   h,
		landedSvc: landedSvc,
		glRepo:    glRepo,
		zone:      zone,
	}
}

func (rig *apAgingTestRig) ensurePeriod(t *testing.T, y, m int) {
	t.Helper()
	if _, err := rig.pool.Exec(rig.ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`, y, m); err != nil {
		t.Fatalf("ensure fiscal period %d-%02d: %v", y, m, err)
	}
}

func (rig *apAgingTestRig) createReceivedPOAndPostBill(
	t *testing.T,
	tag string,
	unitCostCents int64,
	billDate string,
	dueDate string,
) (PODetail, BillDetail) {
	t.Helper()
	if d, err := time.Parse("2006-01-02", billDate); err == nil {
		rig.ensurePeriod(t, d.Year(), int(d.Month()))
	}
	now := time.Now().In(rig.zone)
	rig.ensurePeriod(t, now.Year(), int(now.Month()))

	prod := rig.newProduct(t, tag)
	po := rig.orderedPO(t, prod, 1, unitCostCents)
	lineID := po.Lines[0].ID
	if _, err := rig.svc.Receive(rig.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 1}},
	}); err != nil {
		t.Fatalf("receive PO (%s): %v", tag, err)
	}

	bill, err := rig.svc.CreateBill(rig.ctx, CreateBillRequest{
		BillNo:   billNo(tag),
		POID:     po.ID,
		BillDate: billDate,
		DueDate:  dueDate,
		Lines:    []BillLineParams{{POLineID: lineID, QtyBilledBase: 1, UnitCostCents: unitCostCents}},
	})
	if err != nil {
		t.Fatalf("create bill (%s): %v", tag, err)
	}
	posted, err := rig.svc.PostBill(rig.ctx, bill.ID, PostBillRequest{AcceptVariance: true, Note: "w2-9a test"})
	if err != nil {
		t.Fatalf("post bill (%s): %v", tag, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `
			DELETE FROM gl_journal_lines
			 WHERE journal_id IN (
				SELECT id FROM gl_journals
				 WHERE source_ref_id = $1
				    OR idempotency_key LIKE 'purchasing.bill.payment:%'
			 )`, posted.ID)
		_, _ = rig.pool.Exec(bg, `
			DELETE FROM gl_journals
			 WHERE source_ref_id = $1`, posted.ID)
	})
	return po, posted
}

func (rig *apAgingTestRig) createOpenLandedCost(t *testing.T, po PODetail, freightCents, dutyCents int64) string {
	t.Helper()
	now := time.Now().In(rig.zone)
	rig.ensurePeriod(t, now.Year(), int(now.Month()))

	var stockDocID string
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT stock_doc_id::text FROM purchasing_receipts WHERE po_id = $1 LIMIT 1`, po.ID).Scan(&stockDocID); err != nil {
		t.Fatalf("lookup stock_doc_id for PO %s: %v", po.ID, err)
	}
	lcID, err := rig.landedSvc.Create(rig.ctx, landedcost.CreateInput{
		POID:            stockDocID,
		PORef:           po.PONo,
		BranchID:        po.BranchID,
		Currency:        "CAD",
		FXRate:          1.0,
		FreightCents:    freightCents,
		DutyCents:       dutyCents,
		HandlingCents:   0,
		AllocationBasis: landedcost.BasisValue,
	})
	if err != nil {
		t.Fatalf("create landed cost: %v", err)
	}
	if err := rig.landedSvc.Apply(rig.ctx, lcID); err != nil {
		t.Fatalf("apply landed cost: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `
			DELETE FROM gl_journal_lines
			 WHERE journal_id IN (
				SELECT id FROM gl_journals
				 WHERE source_ref_id = $1
				    OR idempotency_key IN ('landedcost.apply:' || $1, 'landedcost.void:' || $1)
			 )`, lcID)
		_, _ = rig.pool.Exec(bg, `UPDATE landed_costs SET gl_journal_id = NULL WHERE id = $1::uuid`, lcID)
		_, _ = rig.pool.Exec(bg, `
			DELETE FROM gl_journals
			 WHERE source_ref_id = $1
			    OR idempotency_key IN ('landedcost.apply:' || $1, 'landedcost.void:' || $1)`, lcID)
	})
	return lcID
}

// TestIntegration_W29A_Sub1_APAgingRule proves the AP aging rule in purchasing's own seam:
//  1. Per vendor: posted bills less supplier payments as of the instant.
//  2. Landed cost payables: each open landed cost owed to its carrier or broker is its own row
//     marked as landed cost (is_landed_cost=true, row_kind=LANDED_COST), reached through the
//     landedcost seam and never folded into the vendor's bill row.
//  3. The five aging buckets (current, 1-30, 31-60, 61-90, 91+) and the dealer house day rule
//     (23:59:59 America/Toronto on day D is the next UTC day, and stays on day D).
func TestIntegration_W29A_Sub1_APAgingRule(t *testing.T) {
	rig := newAPAgingRig(t)
	reach := BranchReach{BranchIDs: []string{branchTRT}}

	// The reference instant is the last second of today's house day in America/Toronto, which is
	// already tomorrow in UTC: the dealer house day boundary, exercised with the five buckets. It
	// must also come after every event this test posts at the real clock: a landed cost counts from
	// its apply journal's effective instant (W2-9a round 2b), and that journal is posted now. A fixed
	// 22:30 failed every run after 22:30 house time, when the apply journal is later than the instant
	// it is read as of; the aging was right to leave it out (main's red on f8a3873e).
	nowLocal := time.Now().In(rig.zone)
	asOf := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 23, 59, 59, 0, rig.zone)
	asOfDay := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)

	dayStr := func(offsetDays int) string {
		return asOfDay.AddDate(0, 0, offsetDays).Format("2006-01-02")
	}

	// Five bills on the same vendor spanning all five bucket boundaries relative to asOfDay:
	// - Current: due today (offset 0, must stay CURRENT at the end of the house day even though UTC is tomorrow)
	// - Days 30: due 30 days ago (offset -30, upper bound of 1..30)
	// - Days 60: due 31 days ago (offset -31, lower bound of 31..60)
	// - Days 60 to 90: due 61 days ago (offset -61, lower bound of 61..90)
	// - Days 90 plus: due 91 days ago (offset -91, lower bound of 91+)
	poCur, billCur := rig.createReceivedPOAndPostBill(t, "sub1-cur", 10000, dayStr(0), dayStr(0))
	_, _ = rig.createReceivedPOAndPostBill(t, "sub1-d30", 20000, dayStr(-30), dayStr(-30))
	_, _ = rig.createReceivedPOAndPostBill(t, "sub1-d60", 30000, dayStr(-31), dayStr(-31))
	_, _ = rig.createReceivedPOAndPostBill(t, "sub1-d6090", 40000, dayStr(-61), dayStr(-61))
	_, _ = rig.createReceivedPOAndPostBill(t, "sub1-d90p", 50000, dayStr(-91), dayStr(-91))

	// Record a part supplier payment of 4000 cents on billCur before asOf, leaving 6000 cents open in CURRENT.
	payTime := asOf.Add(-2 * time.Hour)
	if _, err := rig.svc.PayBill(rig.ctx, billCur.ID, PayBillRequest{
		AmountCents:    4000,
		PaidAt:         payTime,
		Note:           "part supplier payment before asOf",
		IdempotencyKey: "w29a-sub1-part-" + billCur.ID,
	}); err != nil {
		t.Fatalf("PayBill: %v", err)
	}

	// Attach and apply an open landed cost of 7500 cents freight on poCur before asOf.
	lcID := rig.createOpenLandedCost(t, poCur, 7500, 0)
	_, _ = rig.pool.Exec(rig.ctx, `UPDATE landed_costs SET created_at = $2, updated_at = $2 WHERE id = $1::uuid`, lcID, payTime)

	page, err := rig.svc.APAging(rig.ctx, asOf, reach)
	if err != nil {
		t.Fatalf("APAging: %v", err)
	}

	// Expected totals:
	// Vendor bills open: 6000 (CURRENT) + 20000 (30) + 30000 (60) + 40000 (60_90) + 50000 (90+) = 146000
	// Landed cost open: 7500 (CURRENT, dated today)
	// Grand total: 153500
	if page.AsOf != dayStr(0) {
		t.Fatalf("W2-9a sub1 red: page.AsOf = %q, want house day %q", page.AsOf, dayStr(0))
	}
	if page.CurrentCents != 13500 || page.Days30Cents != 20000 || page.Days60Cents != 30000 ||
		page.Days60To90Cents != 40000 || page.Days90PlusCents != 50000 || page.TotalCents != 153500 {
		t.Fatalf("W2-9a sub1 red: buckets = (cur=%d d30=%d d60=%d d6090=%d d90p=%d total=%d), want (13500 20000 30000 40000 50000 153500)",
			page.CurrentCents, page.Days30Cents, page.Days60Cents, page.Days60To90Cents, page.Days90PlusCents, page.TotalCents)
	}

	var vendorRow, lcRow *APAgingRow
	for i := range page.Items {
		r := &page.Items[i]
		if !r.IsLandedCost && r.VendorID == rig.vendor {
			vendorRow = r
		}
		if r.IsLandedCost && r.LandedCostID == lcID {
			lcRow = r
		}
	}
	if vendorRow == nil {
		t.Fatalf("W2-9a sub1 red: vendor row for %s missing from AP aging", rig.vendor)
	}
	if vendorRow.TotalCents != 146000 || vendorRow.CurrentCents != 6000 {
		t.Fatalf("W2-9a sub1 red: vendor row total=%d current=%d, want total=146000 current=6000 (landed cost must not be folded into vendor row)",
			vendorRow.TotalCents, vendorRow.CurrentCents)
	}
	if lcRow == nil {
		t.Fatalf("W2-9a sub1 red: landed cost row %s missing from AP aging", lcID)
	}
	if lcRow.RowKind != APRowKindLandedCost || !lcRow.IsLandedCost || lcRow.TotalCents != 7500 || lcRow.CurrentCents != 7500 {
		t.Fatalf("W2-9a sub1 red: landed cost row = %+v, want row_kind=LANDED_COST is_landed_cost=true total=7500 current=7500", *lcRow)
	}

	bal, err := rig.svc.APBalanceAsOf(rig.ctx, asOf, branchTRT)
	if err != nil {
		t.Fatalf("APBalanceAsOf: %v", err)
	}
	if bal != 153500 {
		t.Fatalf("W2-9a sub1 red: APBalanceAsOf = %d, want 153500", bal)
	}
	_ = fmt.Sprintf
	_ = identity.ScopeGlobal
}

// TestIntegration_W29A_Sub2_APAgingTiesToGL2100 proves that the AP aging total
// equals GL 2100 as of the same instant, at any instant, after:
//  1. posted bills;
//  2. a part payment;
//  3. a full payment;
//  4. an open landed cost;
//  5. a landed cost voided.
func TestIntegration_W29A_Sub2_APAgingTiesToGL2100(t *testing.T) {
	h := newHarness(t)
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load dealer zone: %v", err)
	}
	glRepo := gl.NewRepository(h.pool)

	rig := &apAgingTestRig{
		harness:   h,
		landedSvc: h.landed,
		glRepo:    glRepo,
		zone:      zone,
	}

	reach := BranchReach{BranchIDs: []string{branchTRT}}
	todayStr := time.Now().In(zone).Format("2006-01-02")

	assertTie := func(step string, asOf time.Time, wantCents int64) {
		t.Helper()
		page, err := h.svc.APAging(h.ctx, asOf, reach)
		if err != nil {
			t.Fatalf("%s: APAging: %v", step, err)
		}
		glBal, _, err := glRepo.AccountBalanceAsOf(h.ctx, "2100", asOf, branchTRT)
		if err != nil {
			t.Fatalf("%s: AccountBalanceAsOf(2100): %v", step, err)
		}
		glLiabilityCents := -glBal
		if page.TotalCents != glLiabilityCents || page.TotalCents != wantCents {
			t.Fatalf("W2-9a sub2 red (%s): ap_aging.total_cents=%d gl_2100_credit_cents=%d, want %d",
				step, page.TotalCents, glLiabilityCents, wantCents)
		}
	}

	// 1. After posted bills: Bill A (50000 cents) and Bill B (30000 cents).
	poA, billA := rig.createReceivedPOAndPostBill(t, "sub2-billA", 50000, todayStr, todayStr)
	_, _ = rig.createReceivedPOAndPostBill(t, "sub2-billB", 30000, todayStr, todayStr)
	time.Sleep(15 * time.Millisecond)
	t1 := time.Now().UTC()
	assertTie("after bills", t1, 80000)

	// 2. After a part payment of 20000 cents on Bill A.
	time.Sleep(15 * time.Millisecond)
	tPayPart := time.Now().UTC()
	if _, err := h.svc.PayBill(h.ctx, billA.ID, PayBillRequest{
		AmountCents:    20000,
		PaidAt:         tPayPart,
		Note:           "sub2 part payment",
		IdempotencyKey: "w29a-sub2-part-" + billA.ID,
	}); err != nil {
		t.Fatalf("part PayBill: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	t2 := time.Now().UTC()
	assertTie("after part payment", t2, 60000)

	// 3. After a full payment of the remaining 30000 cents on Bill A.
	time.Sleep(15 * time.Millisecond)
	tPayFull := time.Now().UTC()
	if _, err := h.svc.PayBill(h.ctx, billA.ID, PayBillRequest{
		AmountCents:    30000,
		PaidAt:         tPayFull,
		Note:           "sub2 full payment",
		IdempotencyKey: "w29a-sub2-full-" + billA.ID,
	}); err != nil {
		t.Fatalf("full PayBill: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	t3 := time.Now().UTC()
	assertTie("after full payment", t3, 30000)

	// 4. After an open landed cost of 16000 cents (12500 freight + 3500 duty) on PO A.
	time.Sleep(15 * time.Millisecond)
	lcID := rig.createOpenLandedCost(t, poA, 12500, 3500)
	time.Sleep(15 * time.Millisecond)
	t4 := time.Now().UTC()
	assertTie("after open landed cost", t4, 46000)

	// 5. After the landed cost is voided.
	time.Sleep(15 * time.Millisecond)
	if err := h.landed.Void(h.ctx, lcID); err != nil {
		t.Fatalf("void landed cost: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	t5 := time.Now().UTC()
	assertTie("after landed cost voided", t5, 30000)

	// Re-prove the tie at every historical instant after all five events are on the books.
	assertTie("historical t1 after all events", t1, 80000)
	assertTie("historical t2 after all events", t2, 60000)
	assertTie("historical t3 after all events", t3, 30000)
	assertTie("historical t4 after all events", t4, 46000)
	assertTie("historical t5 after all events", t5, 30000)
}

// TestIntegration_W29A_Sub3_APAgingRoute proves GET /api/v1/purchasing/ap-aging:
// - default as_of is now;
// - ?as_of=<instant> evaluates open AP at that instant;
// - guard (RequireManager) refuses a frontline clerk with 403 and admits managers/leadership;
// - branch-fenced caller sees only its granted branches and receives 403 on an ungranted ?branch_id=;
// - response money fields are integer cents only.
func TestIntegration_W29A_Sub3_APAgingRoute(t *testing.T) {
	rig := newAPAgingRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	// Create one posted bill in Trenton (25000 cents) and one posted bill in Kingston (40000 cents).
	rig.svc.withCaller(ctxCaller(rig.user))
	_, billTRT := rig.createReceivedPOAndPostBill(t, "sub3-trt", 25000, todayStr, todayStr)

	prodKGN := rig.newProduct(t, "sub3-kgn")
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
		BillNo: billNo("sub3-kgn"), POID: poKGN.ID, BillDate: todayStr, DueDate: todayStr,
		Lines: []BillLineParams{{POLineID: poKGN.Lines[0].ID, QtyBilledBase: 1, UnitCostCents: 40000}},
	})
	if err != nil {
		t.Fatalf("create KGN bill: %v", err)
	}
	billKGN, err := rig.svc.PostBill(rig.ctx, billKGNDraft.ID, PostBillRequest{AcceptVariance: true, Note: "sub3 kgn"})
	if err != nil {
		t.Fatalf("post KGN bill: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1)`, billKGN.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE source_ref_id = $1`, billKGN.ID)
	})
	_ = billTRT

	// Restore HTTP request-context caller resolution.
	rig.svc.withCaller(identity.StaffContextFromContext)

	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	NewHandler(rig.svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) {
		return rig.user, true
	}).RegisterRoutes(mux, sg.Guards)

	callAs := func(auth erpidentity.StaffAuthority, path string) *httptest.ResponseRecorder {
		sg.SetAuthority(auth)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		req.Header.Set("X-Org", testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	clerkAuth := erpidentity.StaffAuthority{
		RoleID:   "COUNTER_SALES",
		Tier:     erpidentity.TierFrontline,
		Scope:    erpidentity.ScopeBranch,
		Branches: []string{branchTRT},
	}
	trtMgrAuth := erpidentity.StaffAuthority{
		RoleID:   "BRANCH_MANAGER",
		Tier:     erpidentity.TierManager,
		Scope:    erpidentity.ScopeBranch,
		Branches: []string{branchTRT},
	}
	globalAuth := erpidentity.StaffAuthority{
		RoleID: "CONTROLLER",
		Tier:   erpidentity.TierLeadership,
		Scope:  erpidentity.ScopeGlobal,
	}

	// 1. Frontline clerk is refused with 403 Forbidden (RequireManager).
	recClerk := callAs(clerkAuth, "/api/v1/purchasing/ap-aging")
	if recClerk.Code != http.StatusForbidden {
		t.Fatalf("W2-9a sub3 red: clerk status = %d (%s), want 403 Forbidden", recClerk.Code, recClerk.Body.String())
	}

	// 2. Branch-fenced manager (TRT only) with default as_of=now sees only TRT (25000 cents), never KGN (40000 cents).
	recTRTMgr := callAs(trtMgrAuth, "/api/v1/purchasing/ap-aging")
	if recTRTMgr.Code != http.StatusOK {
		t.Fatalf("W2-9a sub3 red: TRT manager status = %d (%s), want 200 OK", recTRTMgr.Code, recTRTMgr.Body.String())
	}
	var trtPage APAgingPage
	if err := json.Unmarshal(recTRTMgr.Body.Bytes(), &trtPage); err != nil {
		t.Fatalf("decode TRT manager page: %v", err)
	}
	if trtPage.AsOf != todayStr || trtPage.TotalCents != 25000 {
		t.Fatalf("W2-9a sub3 red: TRT manager page = (as_of=%q total_cents=%d), want (%q 25000)",
			trtPage.AsOf, trtPage.TotalCents, todayStr)
	}

	// 3. Branch-fenced manager asking for an ungranted branch (?branch_id=branchKGN) is refused with 403 Forbidden.
	recTRTForb := callAs(trtMgrAuth, "/api/v1/purchasing/ap-aging?branch_id="+branchKGN)
	if recTRTForb.Code != http.StatusForbidden {
		t.Fatalf("W2-9a sub3 red: TRT manager requesting KGN status = %d, want 403 Forbidden", recTRTForb.Code)
	}

	// 4. Global controller with ?as_of=<RFC3339 instant> sees both branches (65000 cents), and all cents fields are JSON integers.
	asOfParam := time.Now().UTC().Format(time.RFC3339Nano)
	recGlobal := callAs(globalAuth, "/api/v1/purchasing/ap-aging?as_of="+asOfParam)
	if recGlobal.Code != http.StatusOK {
		t.Fatalf("W2-9a sub3 red: global controller status = %d (%s), want 200 OK", recGlobal.Code, recGlobal.Body.String())
	}
	var globalPage APAgingPage
	if err := json.Unmarshal(recGlobal.Body.Bytes(), &globalPage); err != nil {
		t.Fatalf("decode global page: %v", err)
	}
	if globalPage.TotalCents != 65000 || globalPage.CurrentCents != 65000 {
		t.Fatalf("W2-9a sub3 red: global total=%d current=%d, want 65000", globalPage.TotalCents, globalPage.CurrentCents)
	}

	// Verify integer cents only in the raw JSON payload (no decimal points on any *_cents field).
	dec := json.NewDecoder(strings.NewReader(recGlobal.Body.String()))
	dec.UseNumber()
	var rawMap map[string]any
	if err := dec.Decode(&rawMap); err != nil {
		t.Fatalf("raw decode: %v", err)
	}
	assertIntegerCentsJSON(t, rawMap)
}

func assertIntegerCentsJSON(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if strings.HasSuffix(k, "_cents") {
				num, ok := val.(json.Number)
				if !ok || strings.Contains(num.String(), ".") {
					t.Fatalf("W2-9a sub3 red: field %q = %v (%T), want integer cents only", k, val, val)
				}
			}
			assertIntegerCentsJSON(t, val)
		}
	case []any:
		for _, elem := range x {
			assertIntegerCentsJSON(t, elem)
		}
	}
}

// TestIntegration_W29A_Sub4_PrincipalReds_OpenLandedCostAndAsOfPayments proves
// the two principal reds:
//  1. An open landed cost must appear on its own marked line in the AP aging
//     (missing open landed cost fails the test and breaks the GL 2100 tie).
//  2. A bill paid AFTER the as_of date still shows as open on that date, while
//     a bill paid BEFORE the as_of date does not show as open on that date.
func TestIntegration_W29A_Sub4_PrincipalReds_OpenLandedCostAndAsOfPayments(t *testing.T) {
	rig := newAPAgingRig(t)
	reach := BranchReach{BranchIDs: []string{branchTRT}}

	// Derive all instants anchored on the FIRST OF THE CURRENT MONTH so the test never ages into a fixed-date
	// bomb and never stamps a posting journal outside the month it was filed in: the round 2b rule counts a
	// document from its posting journal's effective instant, and a journal whose document date sits in a month
	// other than its filed period counts only from that period's end. Every JOURNAL stamp below (the bills'
	// document dates, the payments' paid_at, the landed cost's apply journal) therefore sits in the current,
	// open month, while the row stamps that carry no journal (the landed cost's created day, the buckets' due
	// dates) may reach back further:
	// - F = the first of the current month; asOfCutoff = F+10 (end of house day)
	// - Bill document dates = F-35 (the previous month, ensured OPEN, so the journals are not late filed)
	// - Bill PaidBefore: 35000 cents, due F-35+5... due 35 days before the cutoff, paid in full at F+7 (before the cutoff)
	// - Bill PaidAfter:  60000 cents, due F-35 (45 days past due at the cutoff -> DAYS_60), paid in full at F+15 (after the cutoff)
	// - Open Landed Cost: 9500 cents duty, created F (10 days before the cutoff -> DAYS_30), apply journal stamped F+1
	// - asOfAfterBothPaid = F+20 (after both payments).
	nowLocal := time.Now().In(rig.zone)
	firstOfMonth := time.Date(nowLocal.Year(), nowLocal.Month(), 1, 0, 0, 0, 0, rig.zone)
	dayAt := func(daysOffset int, hour, min, sec, nsec int) time.Time {
		d := firstOfMonth.AddDate(0, 0, daysOffset)
		rig.ensurePeriod(t, d.Year(), int(d.Month()))
		return time.Date(d.Year(), d.Month(), d.Day(), hour, min, sec, nsec, rig.zone)
	}

	billDateStr := dayAt(-35, 12, 0, 0, 0).Format("2006-01-02")
	dueBeforeStr := dayAt(-25, 12, 0, 0, 0).Format("2006-01-02")
	dueAfterStr := dayAt(-35, 12, 0, 0, 0).Format("2006-01-02")

	poBefore, billPaidBefore := rig.createReceivedPOAndPostBill(t, "sub4-paid-before", 35000, billDateStr, dueBeforeStr)
	_, billPaidAfter := rig.createReceivedPOAndPostBill(t, "sub4-paid-after", 60000, billDateStr, dueAfterStr)

	paidBeforeAt := dayAt(7, 14, 0, 0, 0)
	if _, err := rig.svc.PayBill(rig.ctx, billPaidBefore.ID, PayBillRequest{
		AmountCents:    35000,
		PaidAt:         paidBeforeAt,
		Note:           "paid before as_of cutoff",
		IdempotencyKey: "w29a-sub4-before-" + billPaidBefore.ID,
	}); err != nil {
		t.Fatalf("PayBill(billPaidBefore): %v", err)
	}

	paidAfterAt := dayAt(15, 14, 0, 0, 0)
	if _, err := rig.svc.PayBill(rig.ctx, billPaidAfter.ID, PayBillRequest{
		AmountCents:    60000,
		PaidAt:         paidAfterAt,
		Note:           "paid after as_of cutoff",
		IdempotencyKey: "w29a-sub4-after-" + billPaidAfter.ID,
	}); err != nil {
		t.Fatalf("PayBill(billPaidAfter): %v", err)
	}

	lcID := rig.createOpenLandedCost(t, poBefore, 0, 9500)
	lcAppliedAt := dayAt(1, 11, 0, 0, 0)
	if _, err := rig.pool.Exec(rig.ctx, `
		UPDATE landed_costs
		   SET created_at = $2, updated_at = $2
		 WHERE id = $1::uuid`, lcID, lcAppliedAt); err != nil {
		t.Fatalf("stamp landed_cost dates: %v", err)
	}
	if _, err := rig.pool.Exec(rig.ctx, `
		UPDATE gl_journals
		   SET posted_at = $2
		 WHERE idempotency_key = 'landedcost.apply:' || $1`, lcID, lcAppliedAt); err != nil {
		t.Fatalf("stamp landed_cost journal posted_at: %v", err)
	}

	asOfCutoff := dayAt(10, 23, 59, 59, int(time.Second-time.Nanosecond))
	pageCutoff, err := rig.svc.APAging(rig.ctx, asOfCutoff, reach)
	if err != nil {
		t.Fatalf("APAging(asOfCutoff): %v", err)
	}

	var foundLandedCost bool
	var foundPaidBefore bool
	var foundPaidAfter bool
	var paidAfterOpenCents int64
	var paidAfterBucket string

	for _, row := range pageCutoff.Items {
		if row.IsLandedCost && row.LandedCostID == lcID {
			foundLandedCost = true
		}
		for _, item := range row.OpenItems {
			if item.ID == billPaidBefore.ID {
				foundPaidBefore = true
			}
			if item.ID == billPaidAfter.ID {
				foundPaidAfter = true
				paidAfterOpenCents = item.OpenCents
				paidAfterBucket = item.Bucket
			}
		}
	}

	if !foundLandedCost {
		t.Errorf("W2-9a sub4 red (open landed cost missing): open landed cost %s (9500 cents duty) missing from AP aging at cutoff", lcID)
	}
	if foundPaidBefore {
		t.Errorf("W2-9a sub4 red (bill paid before showing): bill %s paid before cutoff must not show as open at cutoff", billPaidBefore.BillNo)
	}
	if !foundPaidAfter || paidAfterOpenCents != 60000 || paidAfterBucket != APBucketDays60 {
		t.Errorf("W2-9a sub4 red (bill paid after missing): bill %s paid after cutoff must still show as open (60000 cents in DAYS_60) at cutoff, got found=%v open_cents=%d bucket=%q",
			billPaidAfter.BillNo, foundPaidAfter, paidAfterOpenCents, paidAfterBucket)
	}
	if pageCutoff.TotalCents != 69500 || pageCutoff.Days30Cents != 9500 || pageCutoff.Days60Cents != 60000 {
		t.Errorf("W2-9a sub4 red (totals at cutoff): got total=%d d30=%d d60=%d, want total=69500 d30=9500 d60=60000",
			pageCutoff.TotalCents, pageCutoff.Days30Cents, pageCutoff.Days60Cents)
	}

	// And after both bills are paid (F+20), billPaidAfter no longer shows, leaving only the open landed cost (9500 cents).
	asOfAfterBoth := dayAt(20, 23, 59, 59, int(time.Second-time.Nanosecond))
	pageAfterBoth, err := rig.svc.APAging(rig.ctx, asOfAfterBoth, reach)
	if err != nil {
		t.Fatalf("APAging(asOfAfterBoth): %v", err)
	}
	if pageAfterBoth.TotalCents != 9500 {
		t.Errorf("W2-9a sub4 red (totals after both paid): got total=%d, want 9500 (both bills paid before asOfAfterBoth, only open landed cost remains)", pageAfterBoth.TotalCents)
	}
}
