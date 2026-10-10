package pricing

// floor_preview_r3_test.go, the review round 3 case for the floor preview: a change that
// names no branch is graded at each branch with that branch's own cost and that branch's
// own branch-limited rules, keeping the worst and naming the branch it happened at.
import (
	"context"
	"testing"
)

// The reviewer's served case: list 2200 in the base unit, branch costs 1518 at Trenton and
// 1540 at Kingston, a stored RETAIL tier rule at 10 percent off limited to Kingston, and the
// category floor raised from 20 to 23 percent with no branch named. Grading with no branch
// used to drop every branch-limited rule, so the preview named nothing while the save left a
// Kingston RETAIL resolve blocked (1980 at 22.22 percent against 23).
func TestPreviewFloor_NoBranchStillGradesEachBranchOwnRules(t *testing.T) {
	kingstonRule := tierRule(1000)
	kingstonRule.CustomerType = "RETAIL"
	kingstonRule.BranchID = "br-kingston"
	kingstonRule.ID, kingstonRule.IsActive = "retail-kingston", true
	st := &fakeStore{
		rules:     []Rule{*kingstonRule},
		listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 2200, PriceUOM: "SF"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2000},
	}
	s := previewSvc(st, CandidateProduct{
		ProductID: "blu60", CategoryID: "pavers", SKU: "BLU-60", Name: "Blu 60", BaseUOM: "SF", CostCents: 1540,
		BranchCosts: []CandidateBranchCost{
			{BranchID: "br-kingston", BranchCode: "KGN", BranchName: "Kingston", CostCents: 1540},
			{BranchID: "br-trenton", BranchCode: "TRT", BranchName: "Trenton", CostCents: 1518},
		},
	})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "category_policy",
		Policy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2300}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("the Kingston rule must be graded with no branch named: 1980 at 2222 bps against a 2300 floor, got %+v", out)
	}
	it := out.Items[0]
	if it.CustomerType != "RETAIL" || it.FinalCents != 1980 || it.PreviousFinalCents != 1980 ||
		it.MarginBps != 2222 || it.FloorBps != 2300 || it.AlreadyUnderFloor {
		t.Errorf("the reviewer's figures (1980 at 2222 bps against 2300, newly under): %+v", it)
	}
	if it.BranchID != "br-kingston" || it.BranchName != "Kingston" {
		t.Errorf("the item must say the worst branch is Kingston: %+v", it)
	}
	// Trenton must not have been named through the Kingston rule: at Trenton RETAIL pays the
	// 2200 list, 30.9 percent against the 23 percent floor, and no other type breaches at
	// either branch (the rule is RETAIL's alone).
	if out.Evaluated != 1 || out.NotGraded != 0 || out.NoCost != 0 || out.Truncated {
		t.Errorf("one product graded in full at two branches: %+v", out)
	}
}

// A branch-limited rule applies only at its own branch even when the change itself is a
// branch-less rule: the worst per product is kept across branches, not averaged or replaced
// by the engagement-wide picture.
func TestPreviewFloor_NoBranchKeepsTheWorstBranchForEachProduct(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}
	s := previewSvc(st, CandidateProduct{
		ProductID: "blu60", CategoryID: "pavers", SKU: "BLU-60", Name: "Blu 60", BaseUOM: "SF", CostCents: 7000,
		BranchCosts: []CandidateBranchCost{
			{BranchID: "br-cheap", BranchName: "Cheap", CostCents: 3000},
			{BranchID: "br-dear", BranchName: "Dear", CostCents: 9000},
		},
	})
	// 40 percent off list: 6000. At the cheap branch that is 5000 bps, well over the 1200
	// floor; at the dear one the cost runs past the price, -5000 bps, under. The item must
	// be the dear branch's.
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(4000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].BranchID != "br-dear" || out.Items[0].BranchName != "Dear" {
		t.Fatalf("the worst branch is the dear one, and the item says so: %+v", out.Items)
	}
	if out.Items[0].MarginBps != -5000 || out.Items[0].FinalCents != 6000 {
		t.Errorf("graded at the dear branch's own cost: %+v", out.Items[0])
	}
}

// The work cap counts the branches too: a combination is one product, customer type,
// quantity and branch grading, so a branch-less preview over many branches stops at the same
// bounded number of gradings and says it was truncated.
func TestPreviewFloor_TheWorkCapCountsBranches(t *testing.T) {
	prods := make([]CandidateProduct, 0, PreviewProductCap)
	for i := 0; i < PreviewProductCap; i++ {
		prods = append(prods, CandidateProduct{
			ProductID: pid(i), CategoryID: "pavers", SKU: "S", Name: "N", BaseUOM: "SF", CostCents: 7000,
			BranchCosts: []CandidateBranchCost{
				{BranchID: "br-a", BranchName: "A", CostCents: 7000},
				{BranchID: "br-b", BranchName: "B", CostCents: 7100},
			},
		})
	}
	st := &fakeStore{listPrice: &ListPrice{ProductID: "x", ListPriceCents: 10000, PriceUOM: "SF"}}
	s := previewSvc(st, prods...)
	h := &QuantityBreakHeader{Code: "B", Name: "N", CategoryID: "pavers", ThresholdUOM: "PLT"}
	for i := 1; i <= MaxBreakTiers; i++ {
		h.Items = append(h.Items, QuantityBreakItem{FromQty: float64(i), ValueType: BreakPctOff, DiscountBps: p64(100), PriceUOM: "SF"})
	}
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "quantity_break", Break: h})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if !out.Truncated || out.Evaluated == 0 || out.Evaluated >= PreviewProductCap {
		t.Fatalf("two branches must bite the cap earlier and say so: %+v", out)
	}
	// 5 types x 20 tiers x 2 branches per product, over no more than the cap's products.
	if got := out.Evaluated * len(previewCustomerTypes) * MaxBreakTiers * 2; got > MaxPreviewGrades {
		t.Fatalf("graded %d combinations (products x types x tiers x branches) over the %d cap", got, MaxPreviewGrades)
	}
}

// A product whose every branch carries no cost is still counted as no cost on file, and a
// branch with a cost is graded while a branch without one is not guessed at.
func TestPreviewFloor_NoBranchStillCountsNoCostAnywhere(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}
	s := previewSvc(st,
		CandidateProduct{ProductID: "nocost", CategoryID: "pavers", SKU: "A", Name: "A", BaseUOM: "SF",
			BranchCosts: []CandidateBranchCost{{BranchID: "br-a", BranchName: "A"}}},
		CandidateProduct{ProductID: "oneof", CategoryID: "pavers", SKU: "B", Name: "B", BaseUOM: "SF",
			BranchCosts: []CandidateBranchCost{{BranchID: "br-a", BranchName: "A"}, {BranchID: "br-b", BranchName: "B", CostCents: 9000}}},
	)
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(1000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if out.NoCost != 1 || out.Evaluated != 1 || out.NotGraded != 0 {
		t.Fatalf("no cost anywhere is no cost; a costed branch grades its own: %+v", out)
	}
}

func pid(i int) string {
	return "p" + string(rune('a'+i%26)) + string(rune('a'+i/26))
}
