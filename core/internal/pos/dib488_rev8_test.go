package pos

// DIB-488 rev 8, the role-gated margin glance (item 10), the JOB fields (item 11), the workspace
// margin quick-hide (0170), the curated quick-chip set (item 4) and the DIB-502 suggested add-ons
// (item 8). DB-free service tests on the service_test.go harness; exactly additive.
//
// THE GATING PROOFS mirror the portal's marshal-and-assert-absent idiom: the response struct is
// marshalled exactly as the handler marshals it (encoding/json over the omitempty tags) and the
// test asserts the margin/profit field names are BYTE-ABSENT for non-allowed roles.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func marginInput() MarginGlanceInput {
	return MarginGlanceInput{
		BranchID:     "branch-trt",
		CustomerType: "RETAIL",
		Lines: []LineInput{
			{ProductID: "prod-paver", QtyEntered: 100, UOMEntered: "SF"},
		},
	}
}

// -- item 10: the margin glance computes server-side, from the engine's numbers ------------------

func TestMarginGlance_ManagerGetsServerComputedFields(t *testing.T) {
	h := newHarness() // the harness caller carries a listed manager role
	h.caller = identity.StaffContext{UserID: "u-mgr", RoleCode: "SALES_MANAGER",
		Tier: identity.TierManager, Scope: identity.ScopeGlobal}
	h.store.branchCosts = map[string]int64{"prod-paver": 60} // WAC 60¢/base; fake engine: 10000¢ line, 100 base
	out, err := h.svc.MarginGlance(context.Background(), marginInput())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil || out.Lines[0].EstProfitCents == nil {
		t.Fatalf("an allowed caller gets per-line fields: %+v", out)
	}
	// revenue 10000¢, cost 60¢ × 100 base = 6000¢ → margin 4000 bps, est profit 4000¢.
	if *out.Lines[0].MarginBps != 4000 || *out.Lines[0].EstProfitCents != 4000 {
		t.Fatalf("margin math is the engine's revenue × the WAC cost: %+v", out.Lines[0])
	}
	if out.MarginBps == nil || *out.MarginBps != 4000 || out.EstProfitCents == nil || *out.EstProfitCents != 4000 {
		t.Fatalf("footer pair must cover the known lines: %+v", out)
	}
	if out.Partial {
		t.Fatalf("every line answered, partial must be false")
	}
}

func TestMarginGlance_RoleOffTheList_FieldsByteAbsent(t *testing.T) {
	h := newHarness()
	// A role the decided configuration keeps off the cost and margin list
	// (X3 hides it from the yard team).
	h.caller = identity.StaffContext{UserID: "u-yard", RoleCode: "YARD_TEAM",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	h.store.branchCosts = map[string]int64{"prod-paver": 60}
	out, err := h.svc.MarginGlance(context.Background(), marginInput())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	// THE MARSHAL-LAYER PROOF: marshalled exactly as the handler writes it, the payload for a
	// role off the configured list is `{}`, margin/profit BYTE-ABSENT, not zeroed, not nulled.
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("a caller off the configured list must receive {}: %s", raw)
	}
	if strings.Contains(string(raw), "margin") || strings.Contains(string(raw), "profit") {
		t.Fatalf("margin/profit must be byte-absent: %s", raw)
	}
}

func TestMarginGlance_EmptyList_NobodyGetsFields(t *testing.T) {
	h := newHarness() // manager-tier caller
	// The dealer's configuration is the switch: a cost and margin list that
	// names nobody hides the column from everyone, a manager included.
	h.svc = h.svc.WithRoleConfig(emptyCostVisibilityConfig(t))
	h.store.branchCosts = map[string]int64{"prod-paver": 60}
	out, err := h.svc.MarginGlance(context.Background(), marginInput())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("an empty configured list means nobody, a manager gets {} too: %s", raw)
	}
}

func TestMarginGlance_ConfiguredList_FrontlineGetsFields(t *testing.T) {
	h := newHarness()
	// The decided configuration names Inside Sales on the cost and margin
	// list (X3), so the counter clerk is CONSIDERED; the 0170 policy decides
	// whether the answer leaves the server, so the leg sets the dealer's
	// switch to ALL_STAFF and the frontline clerk receives margin. The spine
	// beside it: a role the list does not consider stays byte-absent under
	// the same switch, the policy alone never admits, the list still decides
	// who is considered.
	h.store.policies[""] = PosPolicy{MarginVisibility: MarginVisibilityAllStaff}
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	h.store.branchCosts = map[string]int64{"prod-paver": 60}
	out, err := h.svc.MarginGlance(context.Background(), marginInput())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if out.MarginBps == nil || out.EstProfitCents == nil {
		t.Fatalf("a considered role under ALL_STAFF receives margin, frontline included: %+v", out)
	}

	h.caller = identity.StaffContext{UserID: "u-yard", RoleCode: "YARD_TEAM",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	out, err = h.svc.MarginGlance(context.Background(), marginInput())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("ALL_STAFF must not admit a role the list does not consider: %s", raw)
	}
}

func TestMarginGlance_NoCostLine_AbsentAndFooterHonest(t *testing.T) {
	h := newHarness()
	h.caller = identity.StaffContext{UserID: "u-mgr", RoleCode: "SALES_MANAGER",
		Tier: identity.TierManager, Scope: identity.ScopeGlobal}
	h.store.branchCosts = map[string]int64{"prod-paver": 60} // prod-nocost has NO WAC row
	in := marginInput()
	in.Lines = append(in.Lines, LineInput{ProductID: "prod-nocost", QtyEntered: 5, UOMEntered: "EA"})
	out, err := h.svc.MarginGlance(context.Background(), in)
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if len(out.Lines) != 2 {
		t.Fatalf("lines answer index-aligned: %+v", out)
	}
	if out.Lines[1].MarginBps != nil || out.Lines[1].EstProfitCents != nil {
		t.Fatalf("no cost on file is ABSENT, never a fake 100%% margin: %+v", out.Lines[1])
	}
	if !out.Partial {
		t.Fatalf("an excluded line must flag the footer as partial")
	}
	if out.MarginBps == nil || *out.MarginBps != 4000 {
		t.Fatalf("the footer covers only the known lines: %+v", out)
	}
}

// -- item 11: the JOB fields ride the order, the audit Extra and the echo ------------------------

func TestSale_JobFields_RideOrderAuditAndEcho(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.JobAddress = "  128 Front St, Trenton ON  "
	in.JobReference = "  SMITH-DECK-2026  "
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	o := h.store.orders[0]
	if o.JobAddress != "128 Front St, Trenton ON" || o.JobReference != "SMITH-DECK-2026" {
		t.Fatalf("job fields not trimmed onto the order: %+v", o)
	}
	if out.JobAddress != "128 Front St, Trenton ON" || out.JobReference != "SMITH-DECK-2026" {
		t.Fatalf("result must echo the job facts: %+v", out)
	}
	var found bool
	for _, row := range h.rec.rows {
		if row.Action != audit.ActSaleCompleted {
			continue
		}
		found = true
		if row.Extra["job_address"] != "128 Front St, Trenton ON" || row.Extra["job_reference"] != "SMITH-DECK-2026" {
			t.Fatalf("audit Extra must carry the job facts: %+v", row.Extra)
		}
	}
	if !found {
		t.Fatalf("no ActSaleCompleted record")
	}
}

func TestSale_JobAddressTooLong_422(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.JobAddress = strings.Repeat("x", 201)
	_, err := h.svc.Sale(context.Background(), in)
	assertStatus(t, err, 422)
	if len(h.store.orders) != 0 {
		t.Fatalf("a refused sale must write nothing")
	}
}

func TestSale_JobReferenceTooLong_422(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.JobReference = strings.Repeat("x", 65)
	_, err := h.svc.Sale(context.Background(), in)
	assertStatus(t, err, 422)
}

// -- 0170 workspace: margin_hidden rides the whole-or-absent row and the replay ------------------

func TestWorkspace_MarginHiddenRidesTheRowAndReplay(t *testing.T) {
	h := newHarness()
	ctx := wsCtx("user-ann")
	first, err := h.svc.SaveWorkspace(ctx, WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 40, MarginHidden: true})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !first.MarginHidden {
		t.Fatalf("the save answer carries the toggle: %+v", first)
	}
	got, err := h.svc.Workspace(ctx, "branch-trt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.MarginHidden || got.Source != "user" {
		t.Fatalf("the stored row IS the workspace, toggle included: %+v", got)
	}
	// The verbatim replay answers the STORED toggle without a second write.
	again, err := h.svc.SaveWorkspace(ctx, WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 70, MarginHidden: false})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || !again.MarginHidden || h.store.userWSWrites != 1 {
		t.Fatalf("replay must answer the stored row (writes=%d): %+v", h.store.userWSWrites, again)
	}
}

// -- 0170 policy: validation arms + the derived chip set -----------------------------------------

func TestUpdatePolicy_MarginVisibilityRejected_422(t *testing.T) {
	h := newHarness()
	_, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{MarginVisibility: "EVERYONE_AND_THE_DOG"})
	assertStatus(t, err, 422)
	// Omitted normalizes to the column default rather than failing.
	p, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{})
	if err != nil {
		t.Fatalf("omitted must normalize: %v", err)
	}
	if p.MarginVisibility != MarginVisibilityManagerUp {
		t.Fatalf("omitted margin_visibility normalizes to MANAGER_UP: %+v", p)
	}
}

func TestUpdatePolicy_QuickChipSetRejected_422(t *testing.T) {
	h := newHarness()
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = "Cat"
	}
	_, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{QuickChipSet: nine})
	assertStatus(t, err, 422)
	_, err = h.svc.UpdatePolicy(context.Background(), PosPolicy{QuickChipSet: []string{"Pav,ers"}})
	assertStatus(t, err, 422)
}

func TestPolicy_QuickChipSet_DerivedFromTopSellersUntilCurated(t *testing.T) {
	h := newHarness()
	h.store.topApps = []string{"Paver", "Wall", "Bedding"}
	p, err := h.svc.Policy(context.Background(), "branch-trt")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if strings.Join(p.QuickChipSet, "|") != "Paver|Wall|Bedding" {
		t.Fatalf("an un-curated set derives from the top sellers: %+v", p.QuickChipSet)
	}
	// A curated set passes through untouched, no derivation overwrite.
	cur := h.store.policies[""]
	cur.QuickChipSet = []string{"Paver", "Aggregate"}
	h.store.policies[""] = cur
	p, err = h.svc.Policy(context.Background(), "branch-trt")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if strings.Join(p.QuickChipSet, "|") != "Paver|Aggregate" {
		t.Fatalf("a curated set wins: %+v", p.QuickChipSet)
	}
}

// -- DIB-502 (item 8): the suggested add-ons read ------------------------------------------------

func TestAddonSuggestions_PairsExcludesAndComposes(t *testing.T) {
	h := newHarness()
	h.store.productApps = map[string]string{"prod-paver": "Paver"}
	h.store.addonPairings = map[string][]string{"Paver": {"Jointing", "Bedding"}}
	lp := int64(3999)
	h.store.addonCands = []addonCandidate{
		{ProductID: "prod-paver", SKU: "DBS-X", Name: "The ticket item itself"}, // must be excluded
		{ProductID: "prod-sand", SKU: "DBS-GATOR-SAND", Name: "Polymeric Sand",
			ProductSize: "22.7 kg bag", BaseUOM: "BAG", MinimumOrder: 0, ListPriceCents: &lp, HasImage: true,
			Application: "Jointing"},
	}
	out, err := h.svc.AddonSuggestions(context.Background(), []string{"prod-paver"}, 3)
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	if len(out.Suggestions) != 1 {
		t.Fatalf("the ticket's own product must be excluded: %+v", out.Suggestions)
	}
	s := out.Suggestions[0]
	if s.ProductID != "prod-sand" || s.UOM != "BAG" || s.DefaultQty != 1 || s.SizeLine != "22.7 kg bag" {
		t.Fatalf("suggestion composition (uom fallback, qty default, size line): %+v", s)
	}
	if len(h.store.addonAsked) == 0 || strings.Join(h.store.addonAsked[0], "|") != "Jointing|Bedding" {
		t.Fatalf("candidates must be asked for the PAIRED categories in rank order: %+v", h.store.addonAsked)
	}
}

func TestAddonSuggestions_EmptyIsAnAnswerNeverAnError(t *testing.T) {
	h := newHarness()
	// Empty ticket.
	out, err := h.svc.AddonSuggestions(context.Background(), nil, 3)
	if err != nil || out.Suggestions == nil || len(out.Suggestions) != 0 {
		t.Fatalf("empty ticket → empty list, never nil/error: %+v %v", out, err)
	}
	// A ticket whose category pairs to nothing.
	h.store.productApps = map[string]string{"prod-light": "Lighting"}
	h.store.addonPairings = map[string][]string{"Paver": {"Jointing"}}
	out, err = h.svc.AddonSuggestions(context.Background(), []string{"prod-light"}, 3)
	if err != nil || len(out.Suggestions) != 0 {
		t.Fatalf("unpaired category → empty list: %+v %v", out, err)
	}
}

// -- item 9: the glance carries the discovered catalogue weight ----------------------------------

func TestStockGlance_CarriesWeightWhenOnFile(t *testing.T) {
	h := newHarness()
	h.store.stock["prod-paver"] = map[string]float64{"branch-trt": 100}
	h.store.weights = map[string]float64{"prod-paver": 3.9}
	g, err := h.svc.StockGlance(context.Background(), "prod-paver")
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if g.WeightKgPerBase != 3.9 {
		t.Fatalf("the glance carries kg-per-base: %+v", g)
	}
	// No weight on file → absent (0/omitempty), never a guess.
	h.store.stock["prod-nows"] = map[string]float64{"branch-trt": 5}
	g, err = h.svc.StockGlance(context.Background(), "prod-nows")
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if g.WeightKgPerBase != 0 {
		t.Fatalf("no weight must marshal absent: %+v", g)
	}
	raw, _ := json.Marshal(g)
	if strings.Contains(string(raw), "weight_kg_per_base") {
		t.Fatalf("weight must be byte-absent when not on file: %s", raw)
	}
}
