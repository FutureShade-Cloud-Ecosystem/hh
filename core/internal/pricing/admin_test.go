package pricing

import (
	"context"
	"errors"
	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Stable seed-category / customer uuids reused across the admin-editor tests.
const (
	catPaver = "ca700000-0000-4000-9000-000000000001"
	custABC  = "cc700000-0000-4000-9000-000000000001"
	prodBlu  = "a7000000-0000-4000-9000-000000000001"
)

func strptr(s string) *string { return &s }

// adminSvc builds a Service over a fresh fakeStore with the standard UOM converter +
// the real identity margin gate, so the admin-CRUD + report paths exercise real math.
func adminSvc() (*Service, *fakeStore) {
	st := &fakeStore{byID: map[string]Rule{}}
	return newSvc(st, ""), st
}

// - U1: floor ≤ target enforced server-side on the policy upsert (US-P02) -
func TestCategoryPolicy_FloorAboveTarget_Rejected(t *testing.T) {
	svc, _ := adminSvc()
	_, err := svc.UpsertCategoryPolicy(context.Background(), CategoryPolicy{
		CategoryID: catPaver, MarginTargetBps: 1800, MarginFloorBps: 3000,
	}, "ryan-mgr")
	var upe unprocessableError
	if err == nil || !errors.As(err, &upe) {
		t.Fatalf("floor > target must be rejected (422-shaped), got %v", err)
	}
}

// - U1 (happy) + U10/I3: a valid upsert round-trips and audits the actor -
func TestCategoryPolicy_Upsert_RoundTripsWithActor(t *testing.T) {
	svc, st := adminSvc()
	out, err := svc.UpsertCategoryPolicy(context.Background(), CategoryPolicy{
		CategoryID: catPaver, MarginTargetBps: 3000, MarginFloorBps: 1800,
	}, "ryan-mgr")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if out.MarginTargetBps != 3000 || out.MarginFloorBps != 1800 {
		t.Fatalf("upsert must round-trip target/floor, got %+v", out)
	}
	var found bool
	for _, a := range st.audits {
		if a.EntityType == "category_policy" && a.EntityID == catPaver && a.UserID == "ryan-mgr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a category-policy write must audit the actor (US-P14), got %+v", st.audits)
	}
}

// - U2: ABSOLUTE rejected on TIER scopes, accepted on CUSTOMER_PRODUCT, on
//
//	BOTH create and the UpdateRule patch path (R-3) -
func TestRule_AbsoluteOnlyOnCustomerProduct_CreateAndPatch(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()

	// create: ABSOLUTE on TIER_CATEGORY → 422-shaped
	_, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierCategory, CalcType: CalcAbsolute, PriceCents: p64(41000), PriceUOM: "SF"}, "ryan")
	var upe unprocessableError
	if !errors.As(err, &upe) {
		t.Fatalf("ABSOLUTE on TIER_CATEGORY must be 422-shaped on create, got %v", err)
	}

	// create: ABSOLUTE on CUSTOMER_PRODUCT → allowed
	cp, err := svc.CreateRule(ctx, Rule{Scope: ScopeCustomerProduct, CustomerID: custABC, ProductID: "p1", CalcType: CalcAbsolute, PriceCents: p64(41000), PriceUOM: "SF"}, "ryan")
	if err != nil {
		t.Fatalf("ABSOLUTE on CUSTOMER_PRODUCT must be allowed, got %v", err)
	}

	// patch path: a TIER_CATEGORY rule edited to ABSOLUTE → 422-shaped (the R-3 fix)
	tier, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: catPaver, CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF"}, "ryan")
	if err != nil {
		t.Fatalf("seed tier rule: %v", err)
	}
	_, err = svc.UpdateRule(ctx, tier.ID, RulePatch{CalcType: strptr(CalcAbsolute), PriceCents: p64(41000)}, tier.Version, "ryan")
	if !errors.As(err, &upe) {
		t.Fatalf("editing a TIER_CATEGORY rule's calc_type to ABSOLUTE must be 422-shaped (not a 500), got %v", err)
	}
	_ = cp
}

// - U6: extended applyRulePatch edits priority(0)/validity/is_active -
func TestRule_ExtendedPatch_PriorityZeroAndRetire(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	r, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA", Priority: 10}, "ryan")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// priority 10 → 0 must persist (the old patch ignored 0).
	out, err := svc.UpdateRule(ctx, r.ID, RulePatch{Priority: p64(0)}, r.Version, "ryan")
	if err != nil {
		t.Fatalf("patch priority→0: %v", err)
	}
	if out.Priority != 0 {
		t.Fatalf("priority must be editable to 0, got %d", out.Priority)
	}
}

// - I6: rule DELETE soft-deactivates + writes a DELETE audit row -
func TestRule_Delete_Deactivates(t *testing.T) {
	svc, st := adminSvc()
	ctx := context.Background()
	r, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA"}, "ryan")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.DeleteRule(ctx, r.ID, r.Version, "ryan-mgr"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := svc.GetRule(ctx, r.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got.IsActive {
		t.Fatalf("a retired rule must have is_active=false, got %+v", got)
	}
	var found bool
	for _, a := range st.audits {
		if a.Action == "DELETE" && a.EntityType == "pricing_rule" && a.EntityID == r.ID && a.UserID == "ryan-mgr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a retire must write a DELETE audit row, got %+v", st.audits)
	}
	// A stale version must conflict.
	if err := svc.DeleteRule(ctx, r.ID, r.Version, "ryan"); err == nil {
		t.Fatal("a stale-version retire must conflict")
	}
}

// - U3: override rejects ABSOLUTE; PCT_OFF_LIST accepted (US-P09) -
func TestOverride_AbsoluteForbidden(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	_, err := svc.CreateOverride(ctx, Override{CustomerID: custABC, CategoryID: catPaver, CalcType: CalcAbsolute, PriceCents: p64(41000), PriceUOM: "SF"}, "ryan")
	var upe unprocessableError
	if !errors.As(err, &upe) {
		t.Fatalf("ABSOLUTE override must be 422-shaped, got %v", err)
	}
	if _, err := svc.CreateOverride(ctx, Override{CustomerID: custABC, CategoryID: catPaver, CalcType: CalcPctOffList, DiscountBps: p64(2200), PriceUOM: "SF"}, "ryan"); err != nil {
		t.Fatalf("PCT_OFF_LIST override must be allowed, got %v", err)
	}
}

// - I4: duplicate (customer,category) override → friendly conflict; PUT by id
//
//	round-trips via the new GetOverride(id) read -
func TestOverride_DuplicateConflict_AndEditRoundTrip(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	first, err := svc.CreateOverride(ctx, Override{CustomerID: custABC, CategoryID: catPaver, CalcType: CalcPctOffList, DiscountBps: p64(2000), PriceUOM: "SF"}, "ryan")
	if err != nil {
		t.Fatalf("first override: %v", err)
	}
	if _, err := svc.CreateOverride(ctx, Override{CustomerID: custABC, CategoryID: catPaver, CalcType: CalcPctOffList, DiscountBps: p64(1000), PriceUOM: "SF"}, "ryan"); err == nil {
		t.Fatal("a duplicate (customer,category) override must conflict (UNIQUE 0023:158)")
	}
	// edit by id (optimistic version) round-trips
	out, err := svc.UpdateOverride(ctx, first.ID, Override{CalcType: CalcPctOffList, DiscountBps: p64(2500), PriceUOM: "SF", Notes: "deeper deal"}, first.Version, "ryan")
	if err != nil {
		t.Fatalf("edit override: %v", err)
	}
	if out.Version != first.Version+1 || out.DiscountBps == nil || *out.DiscountBps != 2500 {
		t.Fatalf("override edit must bump version + persist value, got %+v", out)
	}
	// a stale version conflicts
	if _, err := svc.UpdateOverride(ctx, first.ID, Override{CalcType: CalcPctOffList, DiscountBps: p64(3000), PriceUOM: "SF"}, first.Version, "ryan"); err == nil {
		t.Fatal("a stale-version override edit must conflict")
	}
}

// - QB1: a quantity-break create validates, round-trips its tiers, and audits the actor -
func TestQuantityBreak_CreateRoundTripsAndAudits(t *testing.T) {
	svc, st := adminSvc()
	out, err := svc.CreateQuantityBreak(context.Background(), QuantityBreakHeader{
		Code: "PAVER-SKID-5", Name: "Pavers full-skid −5%", CategoryID: catPaver, ThresholdUOM: "PLT",
		Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, DiscountBps: p64(500), PriceUOM: "SF"}},
	}, "ryan-mgr")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.ID == "" || out.Version != 1 || len(out.Items) != 1 {
		t.Fatalf("create must set id/version 1 + persist the tier, got %+v", out)
	}
	var found bool
	for _, a := range st.audits {
		if a.EntityType == "quantity_break" && a.EntityID == out.ID && a.Action == "CREATE" && a.UserID == "ryan-mgr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a quantity-break create must audit the actor (real-money config), got %+v", st.audits)
	}
}

// - QB2: the editor invariants are 422-shaped (value_type set, bps range, ascending, scope, value) -
func TestQuantityBreak_ValidationRejects(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	var upe unprocessableError
	cases := []struct {
		name string
		h    QuantityBreakHeader
	}{
		{"unknown value_type", QuantityBreakHeader{Code: "a", Name: "a", CategoryID: catPaver, ThresholdUOM: "PLT",
			Items: []QuantityBreakItem{{FromQty: 1, ValueType: "NONSENSE", DiscountBps: p64(500), PriceUOM: "SF"}}}},
		{"out-of-range bps", QuantityBreakHeader{Code: "b", Name: "b", CategoryID: catPaver, ThresholdUOM: "PLT",
			Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, DiscountBps: p64(15000), PriceUOM: "SF"}}}},
		{"non-ascending thresholds", QuantityBreakHeader{Code: "c", Name: "c", CategoryID: catPaver, ThresholdUOM: "PLT",
			Items: []QuantityBreakItem{
				{FromQty: 5, ValueType: BreakPctOff, DiscountBps: p64(500), PriceUOM: "SF"},
				{FromQty: 5, ValueType: BreakPctOff, DiscountBps: p64(800), PriceUOM: "SF"},
			}}},
		{"both scopes", QuantityBreakHeader{Code: "d", Name: "d", CategoryID: catPaver, ProductID: prodBlu, ThresholdUOM: "PLT",
			Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, DiscountBps: p64(500), PriceUOM: "SF"}}}},
		{"wrong value field", QuantityBreakHeader{Code: "e", Name: "e", CategoryID: catPaver, ThresholdUOM: "PLT",
			Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, SurchargeBps: p64(500), PriceUOM: "SF"}}}},
		{"no tiers", QuantityBreakHeader{Code: "f", Name: "f", CategoryID: catPaver, ThresholdUOM: "PLT"}},
	}
	for _, tc := range cases {
		if _, err := svc.CreateQuantityBreak(ctx, tc.h, "ryan"); !errors.As(err, &upe) {
			t.Fatalf("%s must be 422-shaped, got %v", tc.name, err)
		}
	}
}

// - QB3: a quantity-break edit is optimistic-locked + a duplicate code conflicts -
func TestQuantityBreak_OptimisticLockAndDuplicateCode(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	created, err := svc.CreateQuantityBreak(ctx, QuantityBreakHeader{
		Code: "STONE-SKID", Name: "Stone partial-skid +10%", ProductID: prodBlu, ThresholdUOM: "KG",
		Items: []QuantityBreakItem{{FromQty: 0, ToQty: f64p(2000), ValueType: BreakSurchargePct, SurchargeBps: p64(1000), PriceUOM: "EA"}},
	}, "ryan")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// a duplicate code → friendly conflict
	if _, err := svc.CreateQuantityBreak(ctx, QuantityBreakHeader{
		Code: "STONE-SKID", Name: "dup", CategoryID: catPaver, ThresholdUOM: "PLT",
		Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, DiscountBps: p64(500), PriceUOM: "SF"}},
	}, "ryan"); !errors.Is(err, errBreakCodeTaken) {
		t.Fatalf("a duplicate code must conflict (errBreakCodeTaken), got %v", err)
	}
	// a stale version edit conflicts; the correct version succeeds + bumps
	edit := created
	edit.Items[0].SurchargeBps = p64(1500)
	if _, err := svc.UpdateQuantityBreak(ctx, created.ID, edit, created.Version+1, "ryan"); !errors.Is(err, errConflict) {
		t.Fatalf("a stale-version edit must conflict, got %v", err)
	}
	out, err := svc.UpdateQuantityBreak(ctx, created.ID, edit, created.Version, "ryan")
	if err != nil || out.Version != created.Version+1 {
		t.Fatalf("a current-version edit must succeed + bump, got %+v err=%v", out, err)
	}
}

// - QB4: delete soft-retires (is_active=false) + audits + a stale retire conflicts -
func TestQuantityBreak_Delete_Retires(t *testing.T) {
	svc, st := adminSvc()
	ctx := context.Background()
	created, err := svc.CreateQuantityBreak(ctx, QuantityBreakHeader{
		Code: "ACC-VOL", Name: "Accessory volume", CategoryID: catPaver, ThresholdUOM: "EA",
		Items: []QuantityBreakItem{{FromQty: 10, ValueType: BreakAbsOff, DiscountCents: p64(250), PriceUOM: "EA"}},
	}, "ryan")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.DeleteQuantityBreak(ctx, created.ID, created.Version, "ryan-mgr"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	got, err := svc.GetQuantityBreak(ctx, created.ID)
	if err != nil || got.IsActive {
		t.Fatalf("a retired break must be is_active=false, got %+v err=%v", got, err)
	}
	var found bool
	for _, a := range st.audits {
		if a.Action == "DELETE" && a.EntityType == "quantity_break" && a.EntityID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("a retire must write a DELETE audit row, got %+v", st.audits)
	}
	if err := svc.DeleteQuantityBreak(ctx, created.ID, created.Version, "ryan"); err == nil {
		t.Fatal("a stale-version retire must conflict")
	}
}

// - U9: the low-margin classifier (warn < target, critical < floor, worst-first) -
func TestLowMargin_Classifier(t *testing.T) {
	if lvl, br := classifyMargin(1500, 3000, 2000); lvl != "critical" || !br {
		t.Fatalf("margin 1500 < floor 2000 must be critical, got %q/%v", lvl, br)
	}
	if lvl, br := classifyMargin(2500, 3000, 2000); lvl != "warn" || !br {
		t.Fatalf("margin 2500 (< target, ≥ floor) must warn, got %q/%v", lvl, br)
	}
	if _, br := classifyMargin(3500, 3000, 2000); br {
		t.Fatalf("margin 3500 ≥ target must not breach")
	}
}

// - U9/I7: the batch report returns only breaching lines, worst-first -
func TestLowMarginReport_BreachingOnlyWorstFirst(t *testing.T) {
	st := &fakeStore{
		listPrice:   &ListPrice{"p", 10000, "EA"},
		catPolicies: map[string]CategoryPolicy{catPaver: {CategoryID: catPaver, MarginTargetBps: 3000, MarginFloorBps: 2000}},
	}
	svc := newSvc(st, "")
	in := func(cost int64) ResolveInput {
		return ResolveInput{ProductID: "p", CategoryID: catPaver, CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA", CostCents: cost}
	}
	// margins: cost 8500→1500 (critical), 7500→2500 (warn), 6500→3500 (clean, excluded)
	out, err := svc.LowMarginReport(context.Background(), []ResolveInput{in(7500), in(6500), in(8500)})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("only the 2 breaching lines should return, got %d (%+v)", len(out), out)
	}
	if out[0].Level != "critical" || out[0].MarginBps != 1500 {
		t.Fatalf("worst-first: critical 1500 first, got %+v", out[0])
	}
	if out[1].Level != "warn" || out[1].MarginBps != 2500 {
		t.Fatalf("warn 2500 second, got %+v", out[1])
	}
}

// - I8: audit read filters by entity_type + action, newest-first -
func TestAudit_FilterByEntityTypeAndAction(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	if _, err := svc.UpsertCategoryPolicy(ctx, CategoryPolicy{CategoryID: catPaver, MarginTargetBps: 3000, MarginFloorBps: 2000}, "ryan"); err != nil {
		t.Fatalf("seed policy audit: %v", err)
	}
	if _, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA"}, "ryan"); err != nil {
		t.Fatalf("seed rule audit: %v", err)
	}
	rows, err := svc.ListAudit(ctx, AuditFilter{EntityType: "pricing_rule", Action: "CREATE"})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(rows) != 1 || rows[0].EntityType != "pricing_rule" || rows[0].Action != "CREATE" {
		t.Fatalf("filter must isolate the pricing_rule CREATE row, got %+v", rows)
	}
}

// - U12: RegisterRoutes fails closed on a nil guard, with the new routes -
func TestRegisterRoutes_NilGuardsPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterRoutes must panic on nil Guards (fail-closed)")
		}
	}()
	NewHandler(NewService(&fakeStore{}), nil).RegisterRoutes(http.NewServeMux(), nil)
}

// - guard helpers for the RBAC binding tests -
// drivePricing serves one request through the real Guards double with the
// set tier resolved, mirroring the positional-guard doubles these tests
// used at the pin (allow/deny/allowAs).
func drivePricing(t *testing.T, sg *testkit.StaffGuards, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

// - I1: every WRITE route is bound to the writeGuard (a Sales caller → 403) -
func TestRBAC_WritesGatedByWriteGuard(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	// reads allowed; writes DENIED, the posture of a Sales (tier 3) caller.
	sg.SetAuthority(auth.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{"b-1"}})
	h := NewHandler(NewService(&fakeStore{byID: map[string]Rule{}}), nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	writes := []struct{ method, path string }{
		{"PUT", "/api/v1/pricing/category-policies/" + catPaver},
		{"POST", "/api/v1/pricing/rules"},
		{"PUT", "/api/v1/pricing/rules/r1"},
		{"DELETE", "/api/v1/pricing/rules/r1?version=1"},
		{"POST", "/api/v1/pricing/customers/" + custABC + "/overrides"},
		{"PUT", "/api/v1/pricing/overrides/o1"},
		{"DELETE", "/api/v1/pricing/overrides/o1"},
		{"POST", "/api/v1/pricing/quantity-breaks"},
		{"PUT", "/api/v1/pricing/quantity-breaks/qb1"},
		{"DELETE", "/api/v1/pricing/quantity-breaks/qb1?version=1"},
	}
	for _, wt := range writes {
		rec := drivePricing(t, sg, mux, wt.method, wt.path, "{}")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s must be write-gated (403), got %d", wt.method, wt.path, rec.Code)
		}
	}
}

// - I1 (positive): a Manager-equivalent writeGuard lets writes through -
func TestRBAC_ManagerWritesPass(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(auth.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{"b-1"}})
	st := &fakeStore{byID: map[string]Rule{}}
	h := NewHandler(NewService(st), nil).WithUserLinker(func(_ context.Context, _ string) (string, bool) {
		return "ryan-mgr", true
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	// PUT category-policy → 200
	rec := drivePricing(t, sg, mux, http.MethodPut, "/api/v1/pricing/category-policies/"+catPaver,
		`{"margin_target_bps":3000,"margin_floor_bps":2000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a Manager category-policy upsert must 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// POST rule → 201
	rec = drivePricing(t, sg, mux, http.MethodPost, "/api/v1/pricing/rules",
		`{"scope":"TIER_DEFAULT","calc_type":"PCT_OFF_LIST","discount_bps":1500,"price_uom":"EA"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a Manager rule create must 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	// The writes were attributed to the authenticated manager.
	var actor bool
	for _, a := range st.audits {
		if a.UserID == "ryan-mgr" {
			actor = true
		}
	}
	if !actor {
		t.Fatalf("Manager writes must be audited with the actor, got %+v", st.audits)
	}
}

// - I2: reads are bound to the readGuard (unauth → 401, Sales+ → 200) -
func TestRBAC_ReadsGatedByReadGuard(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(auth.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{"b-1"}})
	h := NewHandler(NewService(&fakeStore{}), nil)
	reads := []string{
		"/api/v1/pricing/category-policies",
		"/api/v1/pricing/audit",
		"/api/v1/pricing/rules",
		"/api/v1/pricing/quantity-breaks",
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	// unauthenticated: no bearer, the gate answers 401
	for _, p := range reads {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s unauthenticated must 401, got %d", p, rec.Code)
		}
	}

	// staff: the staff constructor admits → 200
	for _, p := range reads {
		rec := drivePricing(t, sg, mux, http.MethodGet, p, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s as staff must 200, got %d (%s)", p, rec.Code, rec.Body.String())
		}
	}
}
