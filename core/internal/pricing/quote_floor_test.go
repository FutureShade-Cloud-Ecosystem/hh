package pricing

// quote_floor_test.go, the quote module's floor seam (plan 3.3 and 3.5): one
// evaluation owned by pricing, the same gate and floor source the resolver
// uses, and the pricing log row the decision writes (a BYPASS row for a
// cleared below floor price, the single call site the UPDATE row for a below
// rule above floor price rides at, which QD-2 completes).
import (
	"context"
	"testing"
)

func TestEvaluateLineFloor_AnswersFromTheGateAndFloorSource(t *testing.T) {
	// A category policy floor of 2000 bps (not the 1200 default) proves the
	// floor source is the category policy, exactly as the resolver reads it.
	st := &fakeStore{
		listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginFloorBps: 2000},
	}
	s := newSvc(st, "pavers")

	// At the rule price: the decision is not asked, but the answer must still
	// report the figures. A candidate at the rule price is not below it.
	ans, err := s.EvaluateLineFloor(context.Background(), FloorQuestion{
		CustomerType: "CONTRACTOR", ProductID: "blu60", QtyEntered: 2, UOMEntered: "EA",
		RuleUnitCents: 10000, CandidateUnitCents: 10000, PriceUOM: "EA", CostCents: 4000,
	})
	if err != nil {
		t.Fatalf("EvaluateLineFloor: %v", err)
	}
	if ans.BelowRule || ans.BelowFloor {
		t.Fatalf("a candidate at the rule price is neither below rule nor below floor, got %+v", ans)
	}
	if ans.FloorBps != 2000 {
		t.Errorf("the floor must come from the category policy (2000), got %d", ans.FloorBps)
	}

	// Below the rule price, above the floor: 10 percent off 10000 is 9000,
	// margin over the 4000 cost is 5555 bps, above the 2000 floor.
	ans, err = s.EvaluateLineFloor(context.Background(), FloorQuestion{
		CustomerType: "CONTRACTOR", ProductID: "blu60", QtyEntered: 2, UOMEntered: "EA",
		RuleUnitCents: 10000, CandidateUnitCents: 9000, PriceUOM: "EA", CostCents: 4000,
	})
	if err != nil {
		t.Fatalf("EvaluateLineFloor: %v", err)
	}
	if !ans.BelowRule || ans.BelowFloor {
		t.Fatalf("10 percent off must be below rule and above floor, got %+v", ans)
	}
	if ans.MarginBps != 5555 {
		t.Errorf("margin at the candidate must be 5555 bps, got %d", ans.MarginBps)
	}

	// Below the floor: 60 percent off 10000 is 4000, margin 0.
	ans, err = s.EvaluateLineFloor(context.Background(), FloorQuestion{
		CustomerType: "CONTRACTOR", ProductID: "blu60", QtyEntered: 2, UOMEntered: "EA",
		RuleUnitCents: 10000, CandidateUnitCents: 4000, PriceUOM: "EA", CostCents: 4000,
	})
	if err != nil {
		t.Fatalf("EvaluateLineFloor: %v", err)
	}
	if !ans.BelowFloor {
		t.Fatalf("a 60 percent discount at this cost must be below the floor, got %+v", ans)
	}
}

// The floor source is the RESOLVED category's own policy row (P1-3, the PR
// 117 review's finding): the seam once read CategoryPolicy with an empty id,
// which the real repository answers nil (repository.go), so every decision
// fell back to the 1200 default and never saw the category's floor or its
// enforcement switch. This store answers as the real one does: a policy for
// the resolved category alone, nothing for any other id the seam may ask
// with. The candidate sits at 3333 bps over the cost: above the 1200
// default, below the category's 2000.
func TestEvaluateLineFloor_ReadsTheResolvedCategorysPolicyRow(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		catPolicies: map[string]CategoryPolicy{
			"pavers": {CategoryID: "pavers", MarginFloorBps: 2000},
		},
	}
	s := newSvc(st, "pavers")
	ans, err := s.EvaluateLineFloor(context.Background(), FloorQuestion{
		CustomerType: "CONTRACTOR", ProductID: "blu60", QtyEntered: 1, UOMEntered: "EA",
		RuleUnitCents: 10000, CandidateUnitCents: 4700, PriceUOM: "EA", CostCents: 4000,
	})
	if err != nil {
		t.Fatalf("EvaluateLineFloor: %v", err)
	}
	if ans.FloorBps != 2000 {
		t.Errorf("the floor must come from the resolved category's own policy row (2000), got %d", ans.FloorBps)
	}
	if !ans.BelowFloor {
		t.Errorf("a 1489 bps margin is below the category's 2000 floor, the gate must say so, got %+v", ans)
	}
	if ans.CategoryID != "pavers" {
		t.Errorf("the answer must name the resolved category, got %q", ans.CategoryID)
	}
}

// Enforcement disabled is the resolver's own bypass: the gate does not run,
// so the floor question answers not below.
func TestEvaluateLineFloor_EnforcementDisabledAnswersNotBelow(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginFloorBps: 2000, EnforcementDisabled: true},
	}
	s := newSvc(st, "pavers")
	ans, err := s.EvaluateLineFloor(context.Background(), FloorQuestion{
		CustomerType: "CONTRACTOR", ProductID: "blu60", QtyEntered: 1, UOMEntered: "EA",
		RuleUnitCents: 10000, CandidateUnitCents: 2000, PriceUOM: "EA", CostCents: 4000,
	})
	if err != nil {
		t.Fatalf("EvaluateLineFloor: %v", err)
	}
	if ans.BelowFloor {
		t.Fatalf("with enforcement disabled the gate does not run, got %+v", ans)
	}
}

// The BYPASS row for a cleared below floor price: the honoured override's
// shape (EntityType margin_enforcement, the reason naming the authority, the
// quote as the drill through target).
func TestRecordFloorDecision_WritesBypassRow(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
	}
	s := newSvc(st, "pavers")
	s.RecordFloorDecision(context.Background(), FloorRecord{
		UserID: "u1", QuoteID: "q1", LineNo: 2, CategoryID: "pavers",
		RuleUnitCents: 10000, NetUnitCents: 4000, DiscPct: 60,
		MarginBps: 0, FloorBps: 2000,
		Action: "BYPASS", BypassReason: BypassManagerScope,
	})
	if len(st.audits) != 1 {
		t.Fatalf("the clearing must write one row, got %d", len(st.audits))
	}
	e := st.audits[0]
	if e.Action != "BYPASS" || e.BypassReason != BypassManagerScope {
		t.Errorf("the row must be a BYPASS naming the authority, got %+v", e)
	}
	if e.RefEntityType != RefQuote || e.RefEntityID != "q1" {
		t.Errorf("the row must drill through to the quote, got %+v", e)
	}
	if e.UserID != "u1" {
		t.Errorf("the row must name who, got %q", e.UserID)
	}

	// The UPDATE row call site (QD-2 completes it): the same seam, one row.
	s.RecordFloorDecision(context.Background(), FloorRecord{
		UserID: "u1", QuoteID: "q1", LineNo: 2, CategoryID: "pavers",
		RuleUnitCents: 10000, NetUnitCents: 9000, DiscPct: 10,
		MarginBps: 5555, FloorBps: 2000,
		Action: "UPDATE",
	})
	if len(st.audits) != 2 || st.audits[1].Action != "UPDATE" {
		t.Fatalf("the UPDATE row must ride the same single call site, got %+v", st.audits)
	}
}

// The transactional record on a store that cannot ride a transaction (a
// DB-free fake) refuses rather than pretending the row landed: this path's
// callers return the error, a silent drop here would be a committed write
// with no log row, the exact loss the transactional path exists to close.
func TestRecordFloorDecisionTx_RefusesWithoutATxStore(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
	}
	s := newSvc(st, "pavers")
	err := s.RecordFloorDecisionTx(context.Background(), nil, FloorRecord{
		UserID: "u1", QuoteID: "q1", ProductID: "blu60",
		RuleUnitCents: 10000, NetUnitCents: 9000, DiscPct: 10,
		MarginBps: 5555, FloorBps: 2000, Action: "UPDATE",
	})
	if err == nil || err != errNoTxAudit {
		t.Fatalf("a store with no transactional append must refuse, got %v", err)
	}
	if len(st.audits) != 0 {
		t.Fatalf("nothing may reach the pool append on this path, got %+v", st.audits)
	}

	// The drop-don't-fail discipline on the CHECK-guarded columns is the
	// record's own: an unknown action writes nothing and errors nowhere.
	if err := s.RecordFloorDecisionTx(context.Background(), nil, FloorRecord{Action: "WEIRD"}); err != nil {
		t.Fatalf("an unknown action drops its row and reports nothing, got %v", err)
	}
}
