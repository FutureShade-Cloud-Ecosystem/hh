package pricing

// floor_preview_r4_test.go, the review round 4 cases for the floor preview: a change that
// NAMES a branch with no cost row counts its products as no cost instead of grading them at
// cost 0, the branch-less check says which yard lacks cost, and the work cap grades the
// dearest branches first and says how many it left out.
import (
	"context"
	"fmt"
	"testing"
)

// The reviewer's round 4 case: a RETAIL Pavers rule at 90 percent off LIMITED to a branch
// that carries no cost row. The named-branch site used to carry the branch with whatever
// cost it had, cost 0 included, so a margin graded at cost 0 read as a clean 100 percent
// and the panel's "no cost on file" notice was lost: the change must count those products
// as no cost, exactly as the branch-less read always has.
func TestPreviewFloor_ANamedBranchWithoutCostCountsNoCost(t *testing.T) {
	var prods []CandidateProduct
	for i := 0; i < 9; i++ {
		prods = append(prods, CandidateProduct{ProductID: fmt.Sprintf("n%d", i), CategoryID: "pavers", SKU: "S", Name: "N", BaseUOM: "SF"})
	}
	s := previewSvc(&fakeStore{listPrice: &ListPrice{ProductID: "n0", ListPriceCents: 10000, PriceUOM: "SF"}}, prods...)
	r := tierRule(9000)
	r.CustomerType = "RETAIL"
	r.BranchID = "br-fixture-north"
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: r})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if out.NoCost != 9 || out.Evaluated != 0 || out.NotGraded != 0 || len(out.Items) != 0 {
		t.Fatalf("nine products with no cost at the named branch are no cost, never a confident zero: %+v", out)
	}
}

// The branch-less counterpart stays as round 3 left it: a product with no cost anywhere is
// counted whole, and a branch carrying a cost is still graded at its own.
func TestPreviewFloor_NoBranchCasesStayGreen(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}
	s := previewSvc(st,
		CandidateProduct{ProductID: "nocost", CategoryID: "pavers", SKU: "A", Name: "A", BaseUOM: "SF"},
		CandidateProduct{ProductID: "costed", CategoryID: "pavers", SKU: "B", Name: "B", BaseUOM: "SF", CostCents: 7000},
	)
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(1000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if out.NoCost != 1 || out.Evaluated != 1 || out.NotGraded != 0 {
		t.Fatalf("no cost anywhere counts the product; a costed one grades: %+v", out)
	}
}

// A branch-less check skips a branch with no cost row and grades the branches that carry
// one; the answer must still say which yard lacked cost and for how many products, without
// turning the product into a no-cost count (it was graded) or a gap that blocks the save.
func TestPreviewFloor_NoBranchSaysWhichBranchLacksCost(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}
	kingstonOnly := func(id string) CandidateProduct {
		return CandidateProduct{ProductID: id, CategoryID: "pavers", SKU: "S", Name: "N", BaseUOM: "SF",
			BranchCosts: []CandidateBranchCost{
				{BranchID: "br-kingston", BranchCode: "KGN", BranchName: "Kingston"},
				{BranchID: "br-trenton", BranchCode: "TRT", BranchName: "Trenton", CostCents: 7000},
			}}
	}
	s := previewSvc(st, kingstonOnly("one"), kingstonOnly("two"),
		CandidateProduct{ProductID: "all", CategoryID: "pavers", SKU: "A", Name: "A", BaseUOM: "SF",
			BranchCosts: []CandidateBranchCost{{BranchID: "br-kingston", BranchName: "Kingston"}, {BranchID: "br-trenton", BranchName: "Trenton"}}})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(1000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if out.Evaluated != 2 || out.NoCost != 1 {
		t.Fatalf("two products graded at Trenton's cost, one counted as no cost anywhere: %+v", out)
	}
	want := []FloorNoCostBranch{{BranchID: "br-kingston", BranchCode: "KGN", BranchName: "Kingston", Products: 2}}
	if len(out.NoCostBranches) != len(want) {
		t.Fatalf("Kingston alone lacks cost on file: %+v", out.NoCostBranches)
	}
	if out.NoCostBranches[0] != want[0] {
		t.Fatalf("the branch is named with its code, its name and the product count: %+v", out.NoCostBranches[0])
	}
}

// The reviewer's round 4 bounds case: a 20-tier break over 41 costed branches is 5 x 20 x 41
// combinations per product, past the 4000 cap. The check must grade the branches that fit,
// dearest cost first, so the worst yard is always among them, and say it was truncated with
// the one branch grading it left out.
func TestPreviewFloor_TheCapGradesTheDearestBranchesFirst(t *testing.T) {
	branches := make([]CandidateBranchCost, 0, 41)
	for i := 0; i < 40; i++ {
		branches = append(branches, CandidateBranchCost{BranchID: fmt.Sprintf("br-cheap-%02d", i), BranchName: "Cheap", CostCents: 3000})
	}
	// The dearest yard sits last in the seam's answer, so an unsorted cut would drop it.
	branches = append(branches, CandidateBranchCost{BranchID: "br-dear", BranchCode: "DEA", BranchName: "Dear", CostCents: 7000})
	st := &fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}
	s := previewSvc(st, CandidateProduct{ProductID: "blu60", CategoryID: "pavers", SKU: "BLU-60", Name: "Blu 60", BaseUOM: "SF",
		CostCents: 7000, BranchCosts: branches})
	h := &QuantityBreakHeader{Code: "B", Name: "N", CategoryID: "pavers", ThresholdUOM: "PLT"}
	for i := 1; i <= MaxBreakTiers; i++ {
		h.Items = append(h.Items, QuantityBreakItem{FromQty: float64(i), ValueType: BreakPctOff, DiscountBps: p64(4000), PriceUOM: "SF"})
	}
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "quantity_break", Break: h})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if !out.Truncated || out.NotGradedBranches != 1 {
		t.Fatalf("41 branches over the cap leave one branch grading out, said: %+v", out)
	}
	if out.Evaluated != 1 {
		t.Fatalf("the one product is graded at the 40 branches that fit: %+v", out)
	}
	// 40 percent off prices 6000: at the cheap yards that is 5000 bps, over the 1200 floor; at
	// the dear one -1666 bps, the only breach, so the item proves the dearest branch was kept.
	if len(out.Items) != 1 || out.Items[0].BranchID != "br-dear" || out.Items[0].BranchCode != "DEA" {
		t.Fatalf("the dearest branch must be among the graded ones and named: %+v", out.Items)
	}
	if out.Items[0].MarginBps != -1666 || out.Items[0].FinalCents != 6000 || out.Items[0].AlreadyUnderFloor {
		t.Fatalf("graded at the dear branch's own cost (6000 at -1666 bps, newly under): %+v", out.Items[0])
	}
}
