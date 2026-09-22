package inventory

// stock_truth_test.go, the DB-free unit suite for the Wave 4 arithmetic.
//
// Three of these are MUTATION TARGETS and are written to fail on a specific, plausible edit rather
// than on "something changed":
//
//	AvailableBase          , drop the subtraction and available becomes on-hand again (the DIB-60 bug).
//	guardOverdraft         , grade on on-hand instead of available and a transfer walks off with
//	                          stock already promised to a confirmed order.
//	SuggestQty             , round to-nearest instead of up and a replenishment arrives short.
//
// Each has at least one case where the WRONG implementation produces a DIFFERENT answer, which is
// the only thing that makes a mutation test worth writing: a test whose fixture has zero reserved
// stock passes under both implementations and proves nothing.

import (
	"context"
	"math"
	"strings"
	"testing"
)

// - DIB-60: available = on_hand − reserved -

func TestAvailableBase_SubtractsReserved(t *testing.T) {
	cases := []struct {
		name             string
		onHand, reserved float64
		want             float64
	}{
		{"nothing held is all available", 100, 0, 100},
		{"a hard hold comes off the top", 100, 40, 60},
		// The case that kills the mutation: reserved is the ENTIRE balance. Under "available =
		// on_hand" this reads 100 and the branch cheerfully sells stock it has already promised.
		{"fully held reads zero, not on-hand", 100, 100, 0},
		// Negative is a legitimate answer (see AvailableBase's doc). A clamp here would hide the
		// exact condition the replenishment worklist exists to find.
		{"over-held goes negative rather than clamping", 10, 25, -15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AvailableBase(c.onHand, c.reserved); got != c.want {
				t.Fatalf("AvailableBase(%v, %v) = %v, want %v", c.onHand, c.reserved, got, c.want)
			}
		})
	}
}

func TestHoldsStock_OnlyHardSubtracts(t *testing.T) {
	if !HoldsStock(HoldHard) {
		t.Fatal("a HARD hold must subtract from available")
	}
	// A soft hold that gated a sale would be worse than no hold: the quote may never convert and the
	// counter must not be told "none left" because someone is thinking about it.
	if HoldsStock(HoldSoft) {
		t.Fatal("a SOFT hold must NOT subtract from available")
	}
	if HoldsStock(HoldNone) {
		t.Fatal("NONE must not subtract from available")
	}
}

// TestCheckAvailability_GradesAgainstAvailable is the mutation target in its WIRED form: it proves
// the subtraction is not merely defined but actually reached by the read the portal reorder preview
// and the catalogue call.
func TestCheckAvailability_GradesAgainstAvailable(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p1", "b1"

	f := newFakeStore()
	f.products[product] = true
	f.onhand[key(product, branch)] = 10
	svc := NewService(NewRepository(nil)).withStore(f)

	// 10 on hand, nothing held → 8 is fine.
	ok, reason, err := svc.CheckAvailability(ctx, product, branch, 8)
	if err != nil || !ok {
		t.Fatalf("unreserved: ok=%v reason=%q err=%v, want available", ok, reason, err)
	}

	// Hold 6 of the 10 for a confirmed order. Available is now 4, so the same request must fail.
	// Under "available = on_hand" this still passes with 10, that is the oversell.
	f.reserved[key(product, branch)] = 6
	ok, reason, err = svc.CheckAvailability(ctx, product, branch, 8)
	if err != nil {
		t.Fatalf("reserved: %v", err)
	}
	if ok {
		t.Fatal("8 units must NOT be available when 6 of 10 are hard-held, available is being read as on-hand")
	}
	if reason != ReasonOutOfStock {
		t.Fatalf("reason = %q, want %q", reason, ReasonOutOfStock)
	}

	// And exactly the available quantity still passes, the guard must not be off by the epsilon.
	if ok, _, err := svc.CheckAvailability(ctx, product, branch, 4); err != nil || !ok {
		t.Fatalf("exactly-available request refused: ok=%v err=%v", ok, err)
	}
}

// - DIB-60 policy resolution -

func TestDefaultAllocationPolicy_StatesTheShippedDefault(t *testing.T) {
	p := DefaultAllocationPolicy()
	mode, ttl := p.GradeFor(ResTypeOrder)
	if mode != HoldHard || ttl != 0 {
		t.Fatalf("ORDER default = (%s, %d), want (HARD, 0), hard-reserve on order confirm, no expiry", mode, ttl)
	}
	mode, ttl = p.GradeFor(ResTypeQuote)
	if mode != HoldSoft || ttl != 72 {
		t.Fatalf("QUOTE default = (%s, %d), want (SOFT, 72), soft-reserve on quote, 72h life", mode, ttl)
	}
}

// TRANSFER must ignore the configured policy entirely: a branch that could grade its own outbound
// transfers SOFT would be switching off the no-overdraft guard from a settings screen.
func TestGradeFor_TransferIsAlwaysHardRegardlessOfPolicy(t *testing.T) {
	p := AllocationPolicy{OrderMode: HoldNone, QuoteMode: HoldNone, QuoteTTLHours: 1}
	if mode, ttl := p.GradeFor(ResTypeTransfer); mode != HoldHard || ttl != 0 {
		t.Fatalf("TRANSFER grade = (%s, %d) under an all-NONE policy, want (HARD, 0)", mode, ttl)
	}
}

func TestGradeFor_UnknownTypeHoldsNothing(t *testing.T) {
	if mode, _ := DefaultAllocationPolicy().GradeFor("LAYAWAY"); mode != HoldNone {
		t.Fatalf("unknown reservation type graded %q, want NONE", mode)
	}
}

// - DIB-60 reserve path (through the fake store) -

func TestReserveTx_HardHoldMovesTheProjection_SoftDoesNot(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p1", "b1"
	f := newFakeStore()
	f.onhand[key(product, branch)] = 50
	svc := NewService(NewRepository(nil)).withStore(f)

	if _, err := svc.ReserveTx(ctx, nil, ReserveInput{
		ProductID: product, BranchID: branch, ResType: ResTypeOrder, QtyBase: 12,
		SourceType: "order", SourceRefID: "o1",
	}); err != nil {
		t.Fatalf("hard reserve: %v", err)
	}
	if got := f.reserved[key(product, branch)]; got != 12 {
		t.Fatalf("qty_reserved = %v after a HARD order hold, want 12", got)
	}

	if _, err := svc.ReserveTx(ctx, nil, ReserveInput{
		ProductID: product, BranchID: branch, ResType: ResTypeQuote, QtyBase: 30,
		SourceType: "quote", SourceRefID: "q1",
	}); err != nil {
		t.Fatalf("soft reserve: %v", err)
	}
	// The soft hold is RECORDED but does not move the projection, that is the whole SOFT/HARD split.
	if got := f.reserved[key(product, branch)]; got != 12 {
		t.Fatalf("qty_reserved = %v after adding a SOFT quote hold, want 12 (soft must not gate)", got)
	}
	if len(f.reservations) != 2 {
		t.Fatalf("recorded %d reservations, want 2 (the soft hold must still be visible)", len(f.reservations))
	}
}

// A branch that has switched quote allocation off must still be able to RAISE quotes, the reserve
// is skipped, not failed. Refusing here would make a settings change break the quote surface.
func TestReserveTx_PolicyNoneWritesNothingAndDoesNotError(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.onhand[key("p1", "b1")] = 5
	f.policies["b1"] = AllocationPolicy{BranchID: "b1", OrderMode: HoldHard, QuoteMode: HoldNone}
	svc := NewService(NewRepository(nil)).withStore(f)

	res, err := svc.ReserveTx(ctx, nil, ReserveInput{
		ProductID: "p1", BranchID: "b1", ResType: ResTypeQuote, QtyBase: 1,
	})
	if err != nil {
		t.Fatalf("a NONE-graded quote reserve must not error: %v", err)
	}
	if res.Applied {
		t.Fatal("policy NONE must write nothing")
	}
	if len(f.reservations) != 0 {
		t.Fatalf("policy NONE wrote %d reservation rows", len(f.reservations))
	}
	if got := f.reserved[key("p1", "b1")]; got != 0 {
		t.Fatalf("policy NONE moved the projection to %v", got)
	}

	// The same branch still HARD-holds an order, the policy is per TYPE, not a global switch.
	if _, err := svc.ReserveTx(ctx, nil, ReserveInput{
		ProductID: "p1", BranchID: "b1", ResType: ResTypeOrder, QtyBase: 2,
	}); err != nil {
		t.Fatalf("order reserve at the same branch: %v", err)
	}
	if got := f.reserved[key("p1", "b1")]; got != 2 {
		t.Fatalf("qty_reserved = %v after the ORDER hold, want 2", got)
	}
}

// A branch that configures quote_mode=HARD genuinely starts reserving against quotes, that is what
// makes the policy table meaningful rather than decorative.
func TestReserveTx_BranchCanHardenQuoteHolds(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.onhand[key("p1", "b1")] = 20
	f.policies["b1"] = AllocationPolicy{BranchID: "b1", OrderMode: HoldHard, QuoteMode: HoldHard, QuoteTTLHours: 24}
	svc := NewService(NewRepository(nil)).withStore(f)

	res, err := svc.ReserveTx(ctx, nil, ReserveInput{
		ProductID: "p1", BranchID: "b1", ResType: ResTypeQuote, QtyBase: 6,
	})
	if err != nil {
		t.Fatalf("hardened quote reserve: %v", err)
	}
	if res.Reservation.HoldMode != HoldHard {
		t.Fatalf("hold mode = %q, want HARD under the branch's configured policy", res.Reservation.HoldMode)
	}
	if got := f.reserved[key("p1", "b1")]; got != 6 {
		t.Fatalf("a HARD quote hold must move the projection; got %v", got)
	}
	if res.Reservation.ExpiresAt == "" {
		t.Fatal("a 24h TTL must produce an expiry, a hold with no expiry is a leak")
	}
}

// The overdraft guard on the reserve path itself (EnforceAvailable), which is what the transfer path
// sets. Two lines of one document must be graded against the RUNNING total, not the opening balance.
func TestReserveTx_EnforceAvailableSeesEarlierHoldsInTheSameTx(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.onhand[key("p1", "b1")] = 10
	svc := NewService(NewRepository(nil)).withStore(f)

	if _, err := svc.ReserveTx(ctx, nil, ReserveInput{
		ProductID: "p1", BranchID: "b1", ResType: ResTypeTransfer, QtyBase: 7, EnforceAvailable: true,
	}); err != nil {
		t.Fatalf("first hold: %v", err)
	}
	// 3 left. A second hold of 5 must be refused.
	if _, err := svc.ReserveTx(ctx, nil, ReserveInput{
		ProductID: "p1", BranchID: "b1", ResType: ResTypeTransfer, QtyBase: 5, EnforceAvailable: true,
	}); err == nil {
		t.Fatal("the second hold was graded against the OPENING balance, it must see the first hold")
	}
}

func TestReleaseBySourceTx_UnwindsOnlyTheHardHolds(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p1", "b1"
	f := newFakeStore()
	f.onhand[key(product, branch)] = 50
	svc := NewService(NewRepository(nil)).withStore(f)

	for _, in := range []ReserveInput{
		{ProductID: product, BranchID: branch, ResType: ResTypeOrder, QtyBase: 10, SourceType: "order", SourceRefID: "o1"},
		{ProductID: product, BranchID: branch, ResType: ResTypeQuote, QtyBase: 7, SourceType: "quote", SourceRefID: "q1"},
	} {
		if _, err := svc.ReserveTx(ctx, nil, in); err != nil {
			t.Fatalf("reserve: %v", err)
		}
	}
	if got := f.reserved[key(product, branch)]; got != 10 {
		t.Fatalf("projection = %v before release, want 10", got)
	}
	n, err := svc.ReleaseBySourceTx(ctx, nil, "order", "o1", ResConsumed)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if n != 1 {
		t.Fatalf("released %d, want 1", n)
	}
	if got := f.reserved[key(product, branch)]; got != 0 {
		t.Fatalf("projection = %v after releasing the hard hold, want 0", got)
	}
	// Releasing the SOFT hold must not push the projection negative.
	if _, err := svc.ReleaseBySourceTx(ctx, nil, "quote", "q1", ResReleased); err != nil {
		t.Fatalf("release soft: %v", err)
	}
	if got := f.reserved[key(product, branch)]; got != 0 {
		t.Fatalf("projection = %v after releasing a soft hold, want 0", got)
	}
}

func TestReleaseTx_RejectsANonTerminalStatus(t *testing.T) {
	svc := NewService(NewRepository(nil)).withStore(newFakeStore())
	if err := svc.ReleaseTx(context.Background(), nil, "res-1", ResActive); err == nil {
		t.Fatal("closing a reservation to ACTIVE must be refused")
	}
}

// - DIB-71: the no-overdraft guard -

// TestGuardOverdraft is the second mutation target. The reserved-stock case is the one that
// distinguishes "grade available" from "grade on-hand".
func TestGuardOverdraft(t *testing.T) {
	cases := []struct {
		name                  string
		onHand, reserved, qty float64
		wantErr               bool
	}{
		{"plenty spare", 100, 0, 40, false},
		{"exactly the balance", 100, 0, 100, false},
		{"more than exists", 100, 0, 101, true},
		// THE MUTATION CASE: 100 on hand but 90 hard-held for a confirmed order. A transfer for 40
		// must be refused. Grading on on-hand would wave it through and the order it was promised to
		// would ship short.
		{"on-hand is plentiful but it is spoken for", 100, 90, 40, true},
		{"exactly the AVAILABLE balance still passes", 100, 90, 10, false},
		{"nothing available at all", 0, 0, 1, true},
		// Over-held: available is negative, so nothing may leave.
		{"already over-held", 5, 20, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := guardOverdraft(c.onHand, c.reserved, c.qty, "#TR-TRT-1", 1)
			if c.wantErr && err == nil {
				t.Fatalf("guardOverdraft(onHand=%v reserved=%v qty=%v) allowed an overdraft",
					c.onHand, c.reserved, c.qty)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("guardOverdraft(onHand=%v reserved=%v qty=%v) refused a valid transfer: %v",
					c.onHand, c.reserved, c.qty, err)
			}
			if err != nil {
				// The message must name the document and the numbers, because the person reading it is
				// standing at a truck deciding what to do next.
				if !strings.Contains(err.Error(), "#TR-TRT-1") || !strings.Contains(err.Error(), "available") {
					t.Fatalf("overdraft message is not actionable: %q", err)
				}
			}
		})
	}
}

func TestGuardOverdraft_AbsorbsFloatNoiseNotRealUnits(t *testing.T) {
	// A DECIMAL(19,4) round trip can land a hair under. The epsilon must absorb that...
	if err := guardOverdraft(10.0, 0, 10.0000001, "#TR-TRT-2", 1); err != nil {
		t.Fatalf("epsilon failed to absorb float noise: %v", err)
	}
	// ...and must not absorb a real quantity. 1e-4 is the smallest representable unit.
	if err := guardOverdraft(10.0, 0, 10.0001, "#TR-TRT-2", 1); err == nil {
		t.Fatal("epsilon absorbed a real 0.0001 overdraft, it is too large")
	}
}

// - DIB-72: the replenishment arithmetic -

func TestProjectedPosition_CountsInboundStock(t *testing.T) {
	// The whole point: 5 available plus a truck of 20 and a PO of 10 is a position of 35, not 5.
	// Grading on availability alone re-orders stock that is already coming.
	if got := ProjectedPosition(5, 20, 10); got != 35 {
		t.Fatalf("ProjectedPosition(5,20,10) = %v, want 35", got)
	}
	if got := ProjectedPosition(-4, 0, 0); got != -4 {
		t.Fatalf("a negative available must survive into the position, got %v", got)
	}
}

// TestSuggestQty is the third mutation target: rounding to-nearest instead of up leaves the branch
// short after the delivery lands.
func TestSuggestQty(t *testing.T) {
	cases := []struct {
		name                                 string
		point, projected, standard, multiple float64
		want                                 float64
	}{
		{"close the gap when there is no standard qty", 100, 60, 0, 0, 40},
		{"never suggest less than the standard qty", 100, 90, 25, 0, 25},
		// THE MUTATION CASE: a gap of 65 against a 60-unit skid must round UP to 120. Rounding to
		// nearest gives 60 and the branch is 5 units short the day the truck arrives.
		{"round UP to the order multiple", 100, 35, 0, 60, 120},
		{"an exact multiple is not inflated", 120, 0, 0, 60, 120},
		{"no multiple means no rounding", 100, 35, 0, 0, 65},
		{"nothing needed", 100, 140, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SuggestQty(c.point, c.projected, c.standard, c.multiple)
			if math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("SuggestQty(point=%v projected=%v standard=%v multiple=%v) = %v, want %v",
					c.point, c.projected, c.standard, c.multiple, got, c.want)
			}
		})
	}
}

// - DIB-72: the create-PO seam default -

func TestStubPOCreator_RefusesRatherThanFabricating(t *testing.T) {
	_, _, err := StubPOCreator{}.CreateDraftPO(context.Background(), SuggestedPO{})
	if err == nil {
		t.Fatal("the unwired purchasing stub must NOT report success, a buyer would be told their PO exists")
	}
	var ce clientError
	if !asClientError(err, &ce) || ce.status != 503 {
		t.Fatalf("stub error = %v, want a 503 clientError so the surface can render the draft", err)
	}
}

// - DIB-61: the facet-count semantic -

// The count for facet F must be computed with every filter applied EXCEPT F's own, otherwise every
// unselected value in F reads zero the moment you select one, and the panel becomes unusable.
func TestBrowseWhere_DropsTheFacetsOwnConstraint(t *testing.T) {
	f := BrowseFilter{
		ColorFamily: []string{"Charcoal"},
		Texture:     []string{"Smooth"},
	}
	var argsAll []any
	all := browseWhere(f, "", &argsAll)
	if !strings.Contains(all, "color_family") || !strings.Contains(all, "texture") {
		t.Fatalf("the row predicate must apply every selected facet: %q", all)
	}

	var argsSkip []any
	skipped := browseWhere(f, "color_family", &argsSkip)
	if strings.Contains(skipped, "p.color_family = ANY") {
		t.Fatalf("the colour-family COUNT predicate still constrains colour_family: %q", skipped)
	}
	if !strings.Contains(skipped, "p.texture = ANY") {
		t.Fatalf("the colour-family COUNT predicate dropped an UNRELATED facet: %q", skipped)
	}
	if len(argsSkip) >= len(argsAll) {
		t.Fatalf("dropping a facet must drop its bind parameter: %d args vs %d", len(argsSkip), len(argsAll))
	}
}

func TestBrowseWhere_InStockOnlyGradesAvailableNotOnHand(t *testing.T) {
	var args []any
	w := browseWhere(BrowseFilter{InStockOnly: true}, "", &args)
	if !strings.Contains(w, "on_hand_base - sb.qty_reserved > 0") {
		t.Fatalf("in_stock_only must grade AVAILABLE, a fully-reserved product is not in stock: %q", w)
	}
}

func TestItoaPos(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{1, "1"}, {9, "9"}, {10, "10"}, {12, "12"}, {103, "103"}} {
		if got := itoaPos(c.n); got != c.want {
			t.Fatalf("itoaPos(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
