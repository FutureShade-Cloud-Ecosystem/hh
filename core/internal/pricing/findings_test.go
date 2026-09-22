package pricing

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// findings_test.go holds the fails-before/passes-after regression tests for the A-07b code-review
// findings. DB-backed proofs (the SQL-level atomic lock, the FK 422, the 0025 CHECK, the scope-edit
// collision) live in integration_test.go; these are the DB-free service/handler unit proofs.

// - Finding #1: discount/markup bps outside 0..10000 is rejected with a 422 -
func TestRule_BpsOutOfRange_Rejected(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	var upe unprocessableError

	// A 15000-bps ("150% off") rule create must 422 (pre-fix it was accepted and drove a NEGATIVE price).
	if _, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(15000), PriceUOM: "EA"}, "ryan"); !errors.As(err, &upe) {
		t.Fatalf("discount_bps 15000 must be 422-shaped, got %v", err)
	}
	// A negative bps is equally invalid.
	if _, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierDefault, CustomerType: "RETAIL", CalcType: CalcPctOffList, DiscountBps: p64(-100), PriceUOM: "EA"}, "ryan"); !errors.As(err, &upe) {
		t.Fatalf("negative discount_bps must be 422-shaped, got %v", err)
	}
	// MARKUP on a customer override is bounded too.
	if _, err := svc.CreateOverride(ctx, Override{CustomerID: custABC, CategoryID: catPaver, CalcType: CalcMarkupOnCost, MarkupBps: p64(15000), PriceUOM: "SF"}, "ryan"); !errors.As(err, &upe) {
		t.Fatalf("override markup_bps 15000 must be 422-shaped, got %v", err)
	}
	// The PATCH path is guarded too (not just create).
	r, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierDefault, CustomerType: "BUILDER", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA"}, "ryan")
	if err != nil {
		t.Fatalf("seed valid rule: %v", err)
	}
	if _, err := svc.UpdateRule(ctx, r.ID, RulePatch{DiscountBps: p64(20000)}, r.Version, "ryan"); !errors.As(err, &upe) {
		t.Fatalf("editing discount_bps to 20000 must be 422-shaped, got %v", err)
	}
	// And the resolver can NEVER return a negative price: the tightest allowed discount (100% =
	// 10000 bps) floors at 0, so a bounded value is the strongest discount possible.
	if got := applyPctOff(10000, maxCalcBps); got < 0 {
		t.Fatalf("100%% off must floor at 0, never negative; got %d", got)
	}
}

// - Finding #2: editing a rule's scope clears the stale scope-key dims (server-side normalize) -
func TestRule_ScopeEdit_ClearsStaleDims(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	// A CUSTOMER_PRODUCT rule carries customer_id + product_id.
	cp, err := svc.CreateRule(ctx, Rule{Scope: ScopeCustomerProduct, CustomerID: custABC, ProductID: catPaver, CalcType: CalcPctOffList, DiscountBps: p64(1000), PriceUOM: "SF"}, "ryan")
	if err != nil {
		t.Fatalf("create CUSTOMER_PRODUCT: %v", err)
	}
	// Re-scope it to TIER_CATEGORY. The stale customer_id/product_id MUST be cleared server-side,
	// otherwise the uq_pricing_rules_active key keeps the old dims and the rule no longer collides
	// with a real same-(type,category) peer (the integration test proves the 409).
	out, err := svc.UpdateRule(ctx, cp.ID, RulePatch{
		Scope: strptr(ScopeTierCategory), CustomerType: strptr("CONTRACTOR"), CategoryID: strptr(catPaver),
	}, cp.Version, "ryan")
	if err != nil {
		t.Fatalf("re-scope to TIER_CATEGORY: %v", err)
	}
	if out.CustomerID != "" || out.ProductID != "" {
		t.Fatalf("scope edit must clear stale customer_id/product_id, got customer_id=%q product_id=%q", out.CustomerID, out.ProductID)
	}
	if out.Scope != ScopeTierCategory || out.CategoryID != catPaver || out.CustomerType != "CONTRACTOR" {
		t.Fatalf("the new scope dims must remain set, got %+v", out)
	}
}

// - Finding #3: a second concurrent save (same stale version) gets a 409 via the atomic guard -
func TestRule_AtomicOptimisticLock_SecondSaveConflicts(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	r, err := svc.CreateRule(ctx, Rule{Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA"}, "ryan")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stale := r.Version // both writers read version 1
	if _, err := svc.UpdateRule(ctx, r.ID, RulePatch{DiscountBps: p64(2000)}, stale, "writer-a"); err != nil {
		t.Fatalf("writer A must commit: %v", err)
	}
	// Writer B raced with the SAME stale version. The atomic WHERE version=$expected makes its
	// update match 0 rows → 409 (not a lost update). The read-then-check the service used to do
	// could not catch this.
	if _, err := svc.UpdateRule(ctx, r.ID, RulePatch{DiscountBps: p64(2500)}, stale, "writer-b"); !errors.Is(err, errConflict) {
		t.Fatalf("the second concurrent save must 409 (atomic lock), got %v", err)
	}
}

// - Finding #4: DeleteOverride is version-guarded (stale delete → 409) -
func TestOverride_Delete_VersionGuard(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	o, err := svc.CreateOverride(ctx, Override{CustomerID: custABC, CategoryID: catPaver, CalcType: CalcPctOffList, DiscountBps: p64(2000), PriceUOM: "SF"}, "ryan")
	if err != nil {
		t.Fatalf("create override: %v", err)
	}
	// A stale delete (the caller's version is behind) must 409, not blindly destroy the row.
	if err := svc.DeleteOverride(ctx, o.ID, o.Version+99, "ryan"); !errors.Is(err, errConflict) {
		t.Fatalf("a stale-version delete must 409, got %v", err)
	}
	// The current version deletes.
	if err := svc.DeleteOverride(ctx, o.ID, o.Version, "ryan"); err != nil {
		t.Fatalf("current-version delete must succeed: %v", err)
	}
	// A repeat (row gone) is a 404.
	if err := svc.DeleteOverride(ctx, o.ID, o.Version, "ryan"); !errors.Is(err, errNotFound) {
		t.Fatalf("deleting a gone row must 404, got %v", err)
	}
}

// - Finding #5: a low-margin preview NEVER writes to the audit log -
func TestLowMarginReport_NoAuditMutation(t *testing.T) {
	st := &fakeStore{
		listPrice:   &ListPrice{"p", 10000, "EA"},
		catPolicies: map[string]CategoryPolicy{catPaver: {CategoryID: catPaver, MarginTargetBps: 3000, MarginFloorBps: 2000, EnforcementDisabled: true}},
	}
	svc := newSvc(st, "")
	// An enforcement-disabled category with a below-floor margin (cost 9000 → 1000 bps). A LIVE
	// resolve would append a BYPASS audit row; the read-only preview must NOT (dry-run path).
	out, err := svc.LowMarginReport(context.Background(), []ResolveInput{
		{ProductID: "p", CategoryID: catPaver, CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA", CostCents: 9000, UserID: "ryan"},
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(out) != 1 || out[0].Level != "critical" {
		t.Fatalf("the disabled-enforcement low-margin line should still classify as critical, got %+v", out)
	}
	if len(st.audits) != 0 {
		t.Fatalf("a preview must write ZERO audit rows (no BYPASS), got %+v", st.audits)
	}
}

// - Finding #6: a bad preview line is SURFACED as invalid, never silently dropped -
func TestLowMarginReport_InvalidLineSurfaced(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"p", 10000, "EA"}}
	svc := newSvc(st, "")
	out, err := svc.LowMarginReport(context.Background(), []ResolveInput{
		{ProductID: "bad", CategoryID: catPaver, CustomerType: "RETAIL", QtyEntered: 0, UOMEntered: "EA", CostCents: 5000},
		{ProductID: "p", CategoryID: catPaver, CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA", CostCents: 9500}, // margin 500 → breaching
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	// The qty=0 line must appear as an INVALID entry (visible), not be absent.
	var invalid *LowMarginEntry
	for i := range out {
		if out[i].ProductID == "bad" {
			invalid = &out[i]
		}
	}
	if invalid == nil {
		t.Fatalf("a qty=0 line must surface as an invalid entry, got %+v", out)
	}
	if !invalid.Invalid || invalid.Reason == "" {
		t.Fatalf("the dropped line must be marked Invalid with a reason, got %+v", *invalid)
	}
	// A valid breaching line still surfaces too (the invalid one did not sink the batch).
	if len(out) != 2 {
		t.Fatalf("both the invalid and the breaching line must surface, got %d (%+v)", len(out), out)
	}
}

// - Finding #7: clearing branch_id + a validity bound BROADENS a rule -
func TestRule_ClearBranchAndValidity_Broadens(t *testing.T) {
	svc, _ := adminSvc()
	ctx := context.Background()
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	r, err := svc.CreateRule(ctx, Rule{
		Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: catPaver,
		BranchID: "bb700000-0000-4000-9000-000000000001", CalcType: CalcPctOffList, DiscountBps: p64(1800),
		PriceUOM: "SF", ValidTo: &past,
	}, "ryan")
	if err != nil {
		t.Fatalf("create branch+expiry rule: %v", err)
	}
	if r.BranchID == "" || r.ValidTo == nil {
		t.Fatalf("seed invariant: the rule must start branch-scoped + time-boxed, got %+v", r)
	}
	// Clear the branch (empty string) and clear valid_to (ClearValidTo). The rule broadens to all
	// branches and always-valid, the gap a nil pointer ("leave unchanged") could not express.
	empty := ""
	out, err := svc.UpdateRule(ctx, r.ID, RulePatch{BranchID: &empty, ClearValidTo: true}, r.Version, "ryan")
	if err != nil {
		t.Fatalf("clear branch + validity: %v", err)
	}
	if out.BranchID != "" {
		t.Fatalf("branch_id must clear (broaden to all branches), got %q", out.BranchID)
	}
	if out.ValidTo != nil {
		t.Fatalf("valid_to must clear (broaden to always-valid), got %v", out.ValidTo)
	}
	if !withinValidity(out, time.Now()) {
		t.Fatalf("a rule with cleared validity must be valid now, got %+v", out)
	}
}

// - Finding #8: a date-only valid_to is inclusive of the WHOLE day (off-by-one fix) -
func TestValidity_DateOnlyValidTo_InclusiveWholeDay(t *testing.T) {
	raw := "2026-06-29"
	// The handler parses a bare-date valid_to to the START OF THE NEXT DAY (exclusive upper bound).
	vt, clear, ok := parseValidityBound(httptest.NewRecorder(), &raw, "valid_to", true)
	if !ok || clear || vt == nil {
		t.Fatalf("a bare date valid_to must parse to a value, got ok=%v clear=%v vt=%v", ok, clear, vt)
	}
	if want := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC); !vt.Equal(want) {
		t.Fatalf("date-only valid_to must parse to next-day midnight %v, got %v", want, *vt)
	}
	// A rule valid_to = today is STILL valid at noon today (the off-by-one would have expired it).
	noon := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	if !withinValidity(Rule{ValidTo: vt}, noon) {
		t.Fatalf("a rule valid_to=today must be valid at noon today")
	}
	// Sanity: the buggy start-of-day bound would (wrongly) exclude noon today.
	buggy := time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)
	if withinValidity(Rule{ValidTo: &buggy}, noon) {
		t.Fatalf("sanity: a start-of-day valid_to should be exclusive at noon (proving the fix matters)")
	}
	// valid_from keeps START-of-day (inclusive from midnight), no end-of-day shift.
	vf, _, ok := parseValidityBound(httptest.NewRecorder(), &raw, "valid_from", false)
	if !ok || vf == nil || !vf.Equal(time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("valid_from must stay start-of-day, got %v", vf)
	}
}

// - Finding #11: a garbage scope is rejected at the service with a 422 (not a DB-CHECK 500) -
func TestRule_GarbageScope_Rejected(t *testing.T) {
	svc, _ := adminSvc()
	var upe unprocessableError
	if _, err := svc.CreateRule(context.Background(), Rule{Scope: "NONSENSE", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA"}, "ryan"); !errors.As(err, &upe) {
		t.Fatalf("a garbage scope must be 422-shaped, got %v", err)
	}
}

// - Finding #12: a no-policy category is graded against the DEFAULT TARGET band, not the floor -
func TestLowMarginReport_NoPolicy_DefaultTargetBand(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"p", 10000, "EA"}}
	svc := newSvc(st, "")
	// No category policy. cost 8000 → margin 2000 bps, which sits BELOW the default target (3000)
	// but ABOVE the default floor (1200) → "warn". Pre-fix the target defaulted to the floor const
	// (1200), so 2000 ≥ 1200 looked "clean" and the line was silently excluded.
	out, err := svc.LowMarginReport(context.Background(), []ResolveInput{
		{ProductID: "p", CategoryID: "ca700000-0000-4000-9000-000000000009", CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA", CostCents: 8000},
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("a below-default-target line must surface as warn, got %d (%+v)", len(out), out)
	}
	if out[0].Level != "warn" || out[0].TargetBps != defaultTargetBps {
		t.Fatalf("expected warn against the default target %d, got %+v", defaultTargetBps, out[0])
	}
}

// - Finding #13: re-saving an identical category policy writes NO audit row -
func TestCategoryPolicy_NoOpUpdate_NoAudit(t *testing.T) {
	svc, st := adminSvc()
	ctx := context.Background()
	p := CategoryPolicy{CategoryID: catPaver, MarginTargetBps: 3000, MarginFloorBps: 2000}
	if _, err := svc.UpsertCategoryPolicy(ctx, p, "ryan"); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	afterCreate := len(st.audits)
	if afterCreate == 0 {
		t.Fatalf("the initial policy create must write an audit row")
	}
	// Re-save the IDENTICAL policy, a no-op UPDATE must not emit the synthetic margin_target_bps row.
	if _, err := svc.UpsertCategoryPolicy(ctx, p, "ryan"); err != nil {
		t.Fatalf("re-save identical policy: %v", err)
	}
	if len(st.audits) != afterCreate {
		t.Fatalf("a no-op identical re-save must write ZERO new audit rows, got %d new", len(st.audits)-afterCreate)
	}
	// A genuine change still audits.
	p2 := p
	p2.MarginTargetBps = 3200
	if _, err := svc.UpsertCategoryPolicy(ctx, p2, "ryan"); err != nil {
		t.Fatalf("real change: %v", err)
	}
	if len(st.audits) == afterCreate {
		t.Fatalf("a real field change must still write an audit row")
	}
}
