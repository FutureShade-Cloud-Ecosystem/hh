package quote

// dib506_test.go, the Quote Builder pilot's proofs.
//
//   THE COST/MARGIN GATE (the critical one): the glance's payload is byte-absent, the literal
//   `{}`, for a frontline caller under MANAGER_UP, for EVERYONE under HIDDEN, and whenever the
//   policy cannot be read (fail closed). A line with no WAC row is ABSENT from the answer, never
//   a fake 100% margin.
//
//   THE SETTINGS POSTURE: quote policy knobs are 422-in-words through the shared kit validators;
//   the policy PUT is admin-gated in the service; the terms write validates in words before any
//   CHECK could 500 and rides the ratified DRAFT/SENT matrix statuses; the line discount refuses
//   out-of-range percentages and a dealer who switched discounts off.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// - fake: fakeStaffStore + conversion stub + the builder surface -

type dib506Store struct {
	*fakeStaffStore
	policy      QuotePolicy
	policyErr   error
	marginRows  []quoteMarginRow
	marginBr    string
	termsWrites int
	discWrites  int
	lastDisc    int
	lastTerms   TermsInput
}

func (s *dib506Store) Convert(context.Context, string, string, ConvertOptions) (ConvertResult, error) {
	return ConvertResult{}, nil
}

func (s *dib506Store) ReadQuotePolicy(context.Context, string) (QuotePolicy, error) {
	return s.policy, s.policyErr
}
func (s *dib506Store) UpsertQuotePolicy(_ context.Context, p QuotePolicy) error {
	s.policy = p
	return nil
}
func (s *dib506Store) ReadQuoteTerms(context.Context, string) (QuoteTerms, error) {
	return QuoteTerms{QuoteID: qOrg}, nil
}
func (s *dib506Store) UpdateTermsGuarded(_ context.Context, _ string, in TermsInput, _ []string, _ string, _ int64) (QuoteTerms, error) {
	s.termsWrites++
	s.lastTerms = in
	return QuoteTerms{QuoteID: qOrg, Fulfillment: in.Fulfillment, DeliveryFeeCents: in.DeliveryFeeCents}, nil
}
func (s *dib506Store) UpdateLineDiscountGuarded(_ context.Context, _ string, _ int, discPct int, _ []string, _ string, _ floorWritePlan, _ quoteDiscountHooks) (QuoteEditResult, error) {
	s.discWrites++
	s.lastDisc = discPct
	return QuoteEditResult{Status: "DRAFT"}, nil
}
func (s *dib506Store) ReadQuoteMarginRows(context.Context, string) (string, []quoteMarginRow, error) {
	return s.marginBr, s.marginRows, nil
}
func (s *dib506Store) ReadHSTRateBps(context.Context) int64 { return 1300 }
func (s *dib506Store) TopCatalogueApplications(context.Context, int) ([]string, error) {
	return []string{"Pavers", "Walls"}, nil
}

var _ staffStore = (*dib506Store)(nil)
var _ builderStore = (*dib506Store)(nil)

func newDib506Store(status string) *dib506Store {
	st := &dib506Store{
		fakeStaffStore: newFakeStaffStore(status),
		policy: QuotePolicy{
			CostVisibility: settings.VisibilityManagerUp, DefaultValidityDays: 30,
			LineDiscountsEnabled: true, SendEmailEnabled: true, IncludeProductImages: true,
			WorkspaceSplitPct: 58, WorkspaceArrangement: settings.ArrangementPrimaryLeft,
		},
		marginBr: qBranch,
	}
	st.ownerID = qUser
	return st
}

func dib506Caller(tier identity.Tier, scope identity.Scope) identity.StaffContext {
	return identity.StaffContext{
		UserID: qUser, RoleCode: "INSIDE_SALES", Tier: tier, Scope: scope,
		Branches: map[string]bool{qBranch: true}, Status: identity.StatusActive,
	}
}

func dib506Svc(store *dib506Store, caller identity.StaffContext) *StaffService {
	return NewStaffService(store).
		WithRoleConfig(qTestRoleConfig()).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithFloorArbiter(&unitFloorArbiter{}).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, true }).
		withNow(qNow)
}

func dib506Want422(t *testing.T, err error, frag string) {
	t.Helper()
	var se *settings.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want a 422 StatusError, got %v", err)
	}
	if se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d (%s)", se.Status, se.Msg)
	}
	if !strings.Contains(se.Msg, frag) {
		t.Fatalf("the refusal must carry words (%q); got %q", frag, se.Msg)
	}
}

// - THE GATE: byte-absence at the marshal layer -

func costRows() []quoteMarginRow {
	return []quoteMarginRow{
		{LineNo: 1, ProductID: qProd, QtyBase: 2, LineTotalCents: 2000, WACCents: 600, HasCost: true},
	}
}

func TestQuoteMarginGlance_FrontlineRole_FieldsByteAbsent(t *testing.T) {
	// D8: the byte-absent case is a role OFF the configuration's cost
	// list. Yard Team is frontline and off it (r3 X3): literal {}.
	store := newDib506Store("DRAFT")
	store.marginRows = costRows()
	off := dib506Caller(identity.TierFrontline, identity.ScopeBranch)
	off.RoleCode = "YARD_TEAM"
	svc := dib506Svc(store, off)

	out, err := svc.MarginGlance(context.Background(), qOrg)
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("a frontline role off the cost list must marshal the literal {}, cost BYTE-ABSENT; got %s", raw)
	}
}

func TestQuoteMarginGlance_Manager_GetsFields_NoCostLineAbsent(t *testing.T) {
	store := newDib506Store("DRAFT")
	store.marginRows = append(costRows(),
		quoteMarginRow{LineNo: 2, ProductID: qProd, QtyBase: 1, LineTotalCents: 1000, HasCost: false})
	svc := dib506Svc(store, dib506Caller(identity.TierManager, identity.ScopeBranch))

	out, err := svc.MarginGlance(context.Background(), qOrg)
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if out.MarginBps == nil || out.EstProfitCents == nil {
		t.Fatal("a manager under MANAGER_UP must receive the footer pair")
	}
	// Line 1: revenue 2000, cost 1200 → 40.00% margin, 800 profit.
	if *out.Lines[0].MarginBps != 4000 || *out.Lines[0].EstProfitCents != 800 {
		t.Fatalf("line 1 arithmetic: %+v", out.Lines[0])
	}
	// Line 2 has NO WAC row: its cost fields stay absent, never a fake 100% margin.
	if out.Lines[1].MarginBps != nil || out.Lines[1].CostCents != nil {
		t.Fatalf("a no-cost line must stay ABSENT: %+v", out.Lines[1])
	}
	if !out.Partial {
		t.Fatal("a footer that excludes a line must say Partial")
	}
	// The footer covers ONLY the known line.
	if *out.EstProfitCents != 800 {
		t.Fatalf("footer must cover only known lines; got %d", *out.EstProfitCents)
	}
}

func TestQuoteMarginGlance_OffTheList_RefusesEvenLeadership(t *testing.T) {
	// D8: the rule is the role configuration's cost_visibility list (X3),
	// no longer the branch policy's MANAGER_UP tier default. HR holds
	// leadership tier and is OFF the list: byte absent.
	store := newDib506Store("DRAFT")
	store.marginRows = costRows()
	hr := dib506Caller(identity.TierLeadership, identity.ScopeGlobal)
	hr.RoleCode = "HR"
	svc := dib506Svc(store, hr)

	out, err := svc.MarginGlance(context.Background(), qOrg)
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if raw, _ := json.Marshal(out); string(raw) != "{}" {
		t.Fatalf("a role off the cost list must receive the byte-absent answer: got %s", raw)
	}
}

func TestQuoteMarginGlance_FailClosed_WithoutConfiguration(t *testing.T) {
	// D8: the list lives in the role configuration; a service with none
	// wired answers no one (the nil gate fails closed, the till's posture).
	store := newDib506Store("DRAFT")
	store.marginRows = costRows()
	svc := NewStaffService(store).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return dib506Caller(identity.TierLeadership, identity.ScopeGlobal), true
		}).withNow(qNow)

	out, err := svc.MarginGlance(context.Background(), qOrg)
	if err != nil {
		t.Fatalf("glance must not surface the missing configuration as an error: %v", err)
	}
	if raw, _ := json.Marshal(out); string(raw) != "{}" {
		t.Fatalf("an unwired configuration must fail CLOSED: got %s", raw)
	}
}

func TestQuoteMarginGlance_ListAdmitsFrontline(t *testing.T) {
	// D8: Inside Sales is on the configuration's cost list (r3 X3), so the
	// frontline sales caller sees cost and margin with no policy change.
	store := newDib506Store("DRAFT")
	store.marginRows = costRows()
	store.policy.CostVisibility = settings.VisibilityManagerUp
	svc := dib506Svc(store, dib506Caller(identity.TierFrontline, identity.ScopeBranch))

	out, err := svc.MarginGlance(context.Background(), qOrg)
	if err != nil || out.MarginBps == nil {
		t.Fatalf("a frontline role on the cost list must see the figures: %+v %v", out, err)
	}
}

// - quote policy: kit-validated 422s + the admin gate -

func TestQuotePolicy_ValidationSpeaks422(t *testing.T) {
	_, err := validateQuotePolicy(QuotePolicy{CostVisibility: "EVERYONE"})
	dib506Want422(t, err, "MANAGER_UP, ALL_STAFF or HIDDEN")

	_, err = validateQuotePolicy(QuotePolicy{DefaultValidityDays: 900})
	dib506Want422(t, err, "default_validity_days")

	_, err = validateQuotePolicy(QuotePolicy{DefaultValidityDays: 30, DefaultDepositPct: 101})
	dib506Want422(t, err, "default_deposit_pct")

	_, err = validateQuotePolicy(QuotePolicy{DefaultValidityDays: 30, WorkspaceSplitPct: 5})
	dib506Want422(t, err, "between 20 and 80")

	_, err = validateQuotePolicy(QuotePolicy{DefaultValidityDays: 30,
		QuickChipSet: []string{"a,b"}})
	dib506Want422(t, err, "no comma")

	p, err := validateQuotePolicy(QuotePolicy{})
	if err != nil {
		t.Fatalf("the zero policy must normalize, not refuse: %v", err)
	}
	if p.CostVisibility != settings.VisibilityManagerUp || p.DefaultValidityDays != 30 ||
		p.WorkspaceSplitPct != settings.SplitDefault {
		t.Fatalf("normalization drifted: %+v", p)
	}
}

func TestUpdateQuotePolicy_AdminGateInService(t *testing.T) {
	store := newDib506Store("DRAFT")
	svc := dib506Svc(store, dib506Caller(identity.TierManager, identity.ScopeBranch))
	_, err := svc.UpdateQuotePolicy(context.Background(), QuotePolicy{})
	var notAdmin *NotSettingsAdminError
	if !errors.As(err, &notAdmin) {
		t.Fatalf("a branch manager must be refused with NotSettingsAdminError; got %v", err)
	}
}

// - terms: 422 in words + the matrix statuses -

func TestUpdateTerms_ValidationSpeaks422(t *testing.T) {
	store := newDib506Store("DRAFT")
	svc := dib506Svc(store, dib506Caller(identity.TierFrontline, identity.ScopeBranch))

	_, err := svc.UpdateTerms(context.Background(), qOrg, TermsInput{DepositPct: 101})
	dib506Want422(t, err, "deposit_pct")

	_, err = svc.UpdateTerms(context.Background(), qOrg, TermsInput{CustomerNotes: strings.Repeat("x", 501)})
	dib506Want422(t, err, "500 character")

	_, err = svc.UpdateTerms(context.Background(), qOrg, TermsInput{Fulfillment: "TELEPORT"})
	dib506Want422(t, err, "PICKUP or DELIVERY")

	_, err = svc.UpdateTerms(context.Background(), qOrg, TermsInput{SalespersonID: "not-a-uuid"})
	dib506Want422(t, err, "salesperson")

	if store.termsWrites != 0 {
		t.Fatal("refused terms must never reach the store")
	}
}

func TestUpdateTerms_PickupClearsDeliveryFacts(t *testing.T) {
	store := newDib506Store("DRAFT")
	svc := dib506Svc(store, dib506Caller(identity.TierFrontline, identity.ScopeBranch))
	_, err := svc.UpdateTerms(context.Background(), qOrg, TermsInput{
		Fulfillment: "PICKUP", DeliveryAddress: "45 Riverside", DeliveryFeeCents: 12500, RequestedDate: "2026-08-28",
	})
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	got := store.lastTerms
	if got.DeliveryAddress != "" || got.DeliveryFeeCents != 0 || got.RequestedDate != "" {
		t.Fatalf("a PICKUP quote must carry no delivery facts: %+v", got)
	}
}

func TestUpdateTerms_FrozenStatusRefused409(t *testing.T) {
	store := newDib506Store("ACCEPTED")
	svc := dib506Svc(store, dib506Caller(identity.TierFrontline, identity.ScopeBranch))
	_, err := svc.UpdateTerms(context.Background(), qOrg, TermsInput{})
	var notEditable *NotEditableError
	if !errors.As(err, &notEditable) {
		t.Fatalf("terms on an ACCEPTED quote must refuse on the matrix; got %v", err)
	}
	if store.termsWrites != 0 {
		t.Fatal("the refused write must not reach the store")
	}
}

// - line discount: bounds + the dealer switch -

func TestSetLineDiscount_BoundsAndDealerSwitch(t *testing.T) {
	store := newDib506Store("DRAFT")
	svc := dib506Svc(store, dib506Caller(identity.TierFrontline, identity.ScopeBranch))

	_, err := svc.SetLineDiscount(context.Background(), qOrg, 1, 101)
	dib506Want422(t, err, "between 0 and 100")

	// The arbiter answers above the floor, so an in-range discount passes
	// the floor decision and reaches the store.
	if _, err := svc.SetLineDiscount(context.Background(), qOrg, 1, 5); err != nil {
		t.Fatalf("an in-range discount above the floor must pass: %v", err)
	}
	store.policy.LineDiscountsEnabled = false
	_, err = svc.SetLineDiscount(context.Background(), qOrg, 1, 5)
	dib506Want422(t, err, "switched off")
	if store.discWrites != 1 {
		t.Fatal("refused discounts must never reach the store")
	}
	// Clearing a discount stays allowed even with the switch off, a dealer turning the feature
	// off must not strand existing discounts.
	if _, err := svc.SetLineDiscount(context.Background(), qOrg, 1, 0); err != nil {
		t.Fatalf("clearing a discount must pass: %v", err)
	}

	store.policy.LineDiscountsEnabled = true
	if _, err := svc.SetLineDiscount(context.Background(), qOrg, 1, 5); err != nil {
		t.Fatalf("an in-range discount must pass: %v", err)
	}
	if store.lastDisc != 5 || store.discWrites != 3 {
		t.Fatalf("want the 5%% write captured; got %d/%d", store.lastDisc, store.discWrites)
	}
}

// - the effective chips derive when un-curated (0170 posture) -

func TestQuotePolicyFor_DerivesChipsWhenUncurated(t *testing.T) {
	store := newDib506Store("DRAFT")
	svc := dib506Svc(store, dib506Caller(identity.TierFrontline, identity.ScopeBranch))
	p, err := svc.QuotePolicyFor(context.Background(), qBranch)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if len(p.EffectiveChips) != 2 || p.EffectiveChips[0] != "Pavers" {
		t.Fatalf("un-curated chips must derive from the catalogue: %+v", p.EffectiveChips)
	}
	store.policy.QuickChipSet = []string{"Steps"}
	p, _ = svc.QuotePolicyFor(context.Background(), qBranch)
	if len(p.EffectiveChips) != 1 || p.EffectiveChips[0] != "Steps" {
		t.Fatalf("a curated set must win verbatim: %+v", p.EffectiveChips)
	}
}
