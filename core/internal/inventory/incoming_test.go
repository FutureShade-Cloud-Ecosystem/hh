package inventory

// incoming_test.go, DIB-460's transfer half and DIB-461's two actions, without a database.
//
// The fakes here RECORD what they were handed (the DIB-431b pattern), because every property this
// ticket is held to is a property of the ARGUMENTS a seam receives:
//
//	the branch reach the in-transit read is given          → the fence
//	the SuggestedPO the POCreator seam is given            → "reused, not forked"
//	the NudgePost the messaging seam is given              → the NOTABLE tier, the channel, the key
//
// None of those is visible in a return value, which is exactly why they are the things that rot.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// - fakes -

// fakeIncoming implements the base store plus BOTH DIB-460/461 optional read seams.
type fakeIncoming struct {
	*fakeStore

	transfers []InTransitLine
	gotReach  BranchReach
	queried   bool

	line       ShortfallRow
	lineFound  bool
	gotLineFor [2]string
	// gotOnOrder is the DIB-470 on-order leg the service handed this read. Recorded, not ignored:
	// "Order now" must grade with the same number the worklist grades with.
	gotOnOrder float64

	product ProductLookup
	found   bool
	rows    []BranchStock
}

func (f *fakeIncoming) InTransitLinesForProduct(_ context.Context, _ string, reach BranchReach) ([]InTransitLine, error) {
	f.gotReach, f.queried = reach, true
	return f.transfers, nil
}

func (f *fakeIncoming) ReplenishLineFor(_ context.Context, productID, branchID string, onOrderBase float64) (ShortfallRow, bool, error) {
	f.gotLineFor = [2]string{productID, branchID}
	f.gotOnOrder = onOrderBase
	// The real read grades with the on-order it is handed, so the fake does too, a double that
	// ignored the argument would let the service stop passing it without a test noticing.
	line := f.line
	if line.ProductID != "" || f.lineFound {
		line.OnOrderBase = onOrderBase
		line.Derive()
	}
	return line, f.lineFound, nil
}

func (f *fakeIncoming) ResolveProductRef(_ context.Context, _ string) (ProductLookup, bool, error) {
	return f.product, f.found, nil
}

func (f *fakeIncoming) ProductStockByBranch(_ context.Context, _ string, _ BranchReach) ([]BranchStock, error) {
	return f.rows, nil
}

// BranchStock is the four-bucket position the nudge quotes, the same method name *Repository has,
// which is what makes the seam a seam rather than a second read.
func (f *fakeIncoming) BranchStock(_ context.Context, _, _ string) ([]BranchStock, error) {
	return f.rows, nil
}

func newIncomingSvc(f *fakeIncoming) *Service {
	f.fakeStore = newFakeStore()
	return NewService(nil).withStore(f)
}

// recordingPOCreator records the draft it was handed instead of creating anything.
type recordingPOCreator struct {
	got    SuggestedPO
	called bool
	err    error
}

func (c *recordingPOCreator) CreateDraftPO(_ context.Context, in SuggestedPO) (string, string, error) {
	c.got, c.called = in, true
	if c.err != nil {
		return "", "", c.err
	}
	return "po-1", "#PO-TRT-121", nil
}

// recordingNudger records the post it was handed. inserted mimics messaging's idempotent insert.
type recordingNudger struct {
	got      NudgePost
	calls    int
	inserted bool
	err      error
}

func (n *recordingNudger) PostProcurementNudge(_ context.Context, post NudgePost) (bool, error) {
	n.got, n.calls = post, n.calls+1
	return n.inserted, n.err
}

// - DIB-460 · the in-transit read -

func TestInTransit_TheReachIsHandedToTheReadSeam(t *testing.T) {
	f := &fakeIncoming{}
	svc := newIncomingSvc(f)

	if _, err := svc.InTransitForProduct(context.Background(), BranchReach{BranchIDs: []string{"b-trt"}}, "p-1"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !f.queried {
		t.Fatal("the read seam was never reached")
	}
	if f.gotReach.Global || len(f.gotReach.BranchIDs) != 1 || f.gotReach.BranchIDs[0] != "b-trt" {
		t.Fatalf("reach = %#v, want exactly the caller's grant", f.gotReach)
	}
}

// TestInTransit_NoGrantsQueriesNothingAndReturnsNothing, the inversion case. A caller with no
// grants must not be handed every truck in the company.
func TestInTransit_NoGrantsQueriesNothingAndReturnsNothing(t *testing.T) {
	f := &fakeIncoming{transfers: []InTransitLine{{TransferNo: "#TR-KGN-3", QtyBase: 15}}}
	svc := newIncomingSvc(f)

	rows, err := svc.InTransitForProduct(context.Background(), BranchReach{}, "p-1")
	if err != nil {
		t.Fatalf("a caller with no grants gets an ANSWER: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d transfer lines for a caller with no branch grants", len(rows))
	}
	if f.queried {
		t.Fatal("a query ran for a caller who can reach no branch")
	}
}

func TestInTransit_ProductIDIsRequired(t *testing.T) {
	f := &fakeIncoming{}
	svc := newIncomingSvc(f)
	if _, err := svc.InTransitForProduct(context.Background(), BranchReach{Global: true}, " "); err == nil {
		t.Fatal("a read with no product must be refused, not answered with every open transfer")
	}
}

// - DIB-461(a) · Order now -

func stockedLine() ShortfallRow {
	return ShortfallRow{
		ProductID: "p-1", SKU: "DBS-TB-BLU60", ProductName: "Techo-Bloc Blu 60 Slab",
		BranchID: "b-trt", BranchCode: "TRT", UOM: "PC",
		OnHandBase: 40, ReservedBase: 0, InTransitBase: 0, OnOrderBase: 0,
		ReorderPointBase: 100, ReorderQtyBase: 60, OrderMultipleBase: 0,
		PreferredVendorID: "v-techo", PreferredVendorName: "Techo Bloc", AvgCostCents: 1900,
		AvailableBase: 40, ProjectedBase: 40,
		SuggestedQtyBase: SuggestQty(100, 40, 60, 0),
	}
}

// TestReorderPO_HandsTheSameDraftShapeTheShortfallPathDoes, "reused, not forked", asserted on the
// argument the shared seam receives.
func TestReorderPO_HandsTheSameDraftShapeTheShortfallPathDoes(t *testing.T) {
	f := &fakeIncoming{line: stockedLine(), lineFound: true}
	creator := &recordingPOCreator{}
	svc := newIncomingSvc(f).WithPOCreator(creator)

	res, err := svc.CreateReorderPO(context.Background(), BranchReach{Global: true}, ReorderRequest{ProductID: "p-1", BranchID: "b-trt"})
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if !creator.called {
		t.Fatal("the POCreator seam was never called, the create-PO path was not reused")
	}
	got := creator.got
	if got.BranchID != "b-trt" || got.VendorID != "v-techo" {
		t.Fatalf("draft = %+v, want the branch asked for and the line's PREFERRED vendor", got)
	}
	if len(got.Lines) != 1 || got.Lines[0].ProductID != "p-1" {
		t.Fatalf("draft lines = %+v, want exactly the product asked for", got.Lines)
	}
	// THE NUMBER. It must be what SuggestQty computes, the same arithmetic the shortfall worklist
	// suggests, or the product page and the worklist tell a buyer to order different amounts.
	want := SuggestQty(100, 40, 60, 0)
	if got.Lines[0].QtyBase != want {
		t.Fatalf("qty = %v, want %v (SuggestQty, the worklist's own suggestion)", got.Lines[0].QtyBase, want)
	}
	if got.Lines[0].UnitCostCents != 1900 {
		t.Fatalf("unit cost = %d, want the branch weighted average", got.Lines[0].UnitCostCents)
	}
	if res.PONo != "#PO-TRT-121" || res.POID != "po-1" {
		t.Fatalf("result = %+v, want the created PO's id and number for the deep link", res)
	}
	if res.VendorName != "Techo Bloc" || res.UOM != "PC" {
		t.Fatalf("result = %+v, want the vendor named and the quantity's unit carried", res)
	}
}

// TestReorderPO_RoundsUpToTheOrderMultipleLikeTheWorklist, the multiple is part of the shared
// arithmetic, so a skid-based product must not be ordered in half skids from this screen either.
func TestReorderPO_RoundsUpToTheOrderMultipleLikeTheWorklist(t *testing.T) {
	line := stockedLine()
	line.OrderMultipleBase = 40
	line.SuggestedQtyBase = SuggestQty(line.ReorderPointBase, line.ProjectedBase, line.ReorderQtyBase, 40)
	f := &fakeIncoming{line: line, lineFound: true}
	creator := &recordingPOCreator{}
	svc := newIncomingSvc(f).WithPOCreator(creator)

	if _, err := svc.CreateReorderPO(context.Background(), BranchReach{Global: true}, ReorderRequest{ProductID: "p-1", BranchID: "b-trt"}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if got := creator.got.Lines[0].QtyBase; got != 80 {
		t.Fatalf("qty = %v, want 80 (60 needed, rounded UP to two 40-unit skids)", got)
	}
}

// TestReorderPO_ATypedQuantityIsUsedVerbatim, the suggestion is a default, not a rule.
func TestReorderPO_ATypedQuantityIsUsedVerbatim(t *testing.T) {
	f := &fakeIncoming{line: stockedLine(), lineFound: true}
	creator := &recordingPOCreator{}
	svc := newIncomingSvc(f).WithPOCreator(creator)

	res, err := svc.CreateReorderPO(context.Background(), BranchReach{Global: true}, ReorderRequest{ProductID: "p-1", BranchID: "b-trt", QtyBase: 15})
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if got := creator.got.Lines[0].QtyBase; got != 15 {
		t.Fatalf("qty = %v, want the 15 the buyer typed", got)
	}
	if res.Suggested != SuggestQty(100, 40, 60, 0) {
		t.Fatalf("suggested = %v, the suggestion must still be reported so the buyer can see they overrode it", res.Suggested)
	}
}

// TestReorderPO_RefusesWithoutAReplenishmentLineOrAVendor, the two refusals, each naming its fix.
func TestReorderPO_RefusesWithoutAReplenishmentLineOrAVendor(t *testing.T) {
	t.Run("no managed line", func(t *testing.T) {
		f := &fakeIncoming{lineFound: false}
		creator := &recordingPOCreator{}
		svc := newIncomingSvc(f).WithPOCreator(creator)
		_, err := svc.CreateReorderPO(context.Background(), BranchReach{Global: true}, ReorderRequest{ProductID: "p-1", BranchID: "b-trt"})
		if err == nil {
			t.Fatal("a product with no replenishment policy has no vendor to buy from")
		}
		if !strings.Contains(err.Error(), "preferred vendor") {
			t.Errorf("the refusal must name the fix; got %q", err.Error())
		}
		if creator.called {
			t.Fatal("a purchase order was drafted with no vendor")
		}
	})
	t.Run("line without a vendor", func(t *testing.T) {
		line := stockedLine()
		line.PreferredVendorID = ""
		f := &fakeIncoming{line: line, lineFound: true}
		creator := &recordingPOCreator{}
		svc := newIncomingSvc(f).WithPOCreator(creator)
		if _, err := svc.CreateReorderPO(context.Background(), BranchReach{Global: true}, ReorderRequest{ProductID: "p-1", BranchID: "b-trt"}); err == nil {
			t.Fatal("a line with no preferred vendor must be refused")
		}
		if creator.called {
			t.Fatal("a purchase order was drafted with no vendor")
		}
	})
}

// TestReorderPO_UnwiredPurchasingReturnsTheDraftWithTheRefusal, the StubPOCreator posture, kept:
// the surface can still show the buyer exactly what would have been ordered.
func TestReorderPO_UnwiredPurchasingReturnsTheDraftWithTheRefusal(t *testing.T) {
	f := &fakeIncoming{line: stockedLine(), lineFound: true}
	svc := newIncomingSvc(f) // no POCreator → StubPOCreator

	res, err := svc.CreateReorderPO(context.Background(), BranchReach{Global: true}, ReorderRequest{ProductID: "p-1", BranchID: "b-trt"})
	if err == nil {
		t.Fatal("an unwired purchasing seam must refuse rather than fabricate a PO number")
	}
	if res.POID != "" || res.PONo != "" {
		t.Fatalf("result = %+v, want NO purchase order id or number, a buyer must not be told an order exists", res)
	}
	if len(res.Draft.Lines) != 1 {
		t.Fatalf("the draft must ride along with the refusal; got %+v", res.Draft)
	}
}

// - DIB-461(b) · the nudge -

func nudgeSvc(n *recordingNudger) (*Service, *fakeIncoming) {
	f := &fakeIncoming{
		product: ProductLookup{ProductID: "p-1", SKU: "DBS-TB-BLU60", Name: "Techo-Bloc Blu 60 Slab", UOM: "PC"},
		found:   true,
		rows:    []BranchStock{{BranchID: "b-trt", BranchCode: "TRT", OnHandBase: 8, AvailableBase: 8}},
	}
	return newIncomingSvc(f).WithProcurementNudger(n), f
}

// TestNudge_PostsNOTABLE_NeverCRITICAL is the tier test.
//
// CRITICAL is the DIB-217 forced-ack path: it lands in #escalations and BLOCKS a till until somebody
// acknowledges it. A low-stock courtesy posted at that tier would stop a sale.
func TestNudge_PostsNOTABLE_NeverCRITICAL(t *testing.T) {
	n := &recordingNudger{inserted: true}
	svc, _ := nudgeSvc(n)

	res, err := svc.NudgeProcurement(context.Background(), BranchReach{Global: true}, "p-1", "b-trt")
	if err != nil {
		t.Fatalf("nudge: %v", err)
	}
	if n.calls != 1 {
		t.Fatalf("the messaging seam was called %d times, want exactly 1", n.calls)
	}
	if n.got.Class != NudgeClassNotable {
		t.Fatalf("class = %q, want NOTABLE, CRITICAL is the forced-ack tier and would block a till", n.got.Class)
	}
	if n.got.Class == "CRITICAL" || n.got.Class == "ROUTINE" {
		t.Fatalf("class = %q", n.got.Class)
	}
	if res.Class != NudgeClassNotable {
		t.Fatalf("the surface was told class %q", res.Class)
	}
	if !res.Posted || res.Note != NudgePostedNote {
		t.Fatalf("result = %+v, want a posted nudge and the posted sentence", res)
	}
}

// TestNudge_GoesToThePurchasingRelevantChannel, the routing decision is the module's, and named.
func TestNudge_GoesToThePurchasingRelevantChannel(t *testing.T) {
	n := &recordingNudger{inserted: true}
	svc, _ := nudgeSvc(n)

	if _, err := svc.NudgeProcurement(context.Background(), BranchReach{Global: true}, "p-1", "b-trt"); err != nil {
		t.Fatalf("nudge: %v", err)
	}
	if n.got.ChannelSlug != ProcurementChannelSlug {
		t.Fatalf("channel = %q, want %q", n.got.ChannelSlug, ProcurementChannelSlug)
	}
	// A branch yard channel would be invisible to procurement sitting at the other branch, the
	// exact failure the ticket describes.
	if strings.HasPrefix(n.got.ChannelSlug, "yard-") {
		t.Fatalf("a nudge posted to %q is invisible to procurement at the other branch", n.got.ChannelSlug)
	}
}

// TestNudge_NamesTheProductTheBranchAndThePosition, the body is the whole value of the feature: a
// nudge without numbers starts a conversation, a nudge with them starts a decision.
func TestNudge_NamesTheProductTheBranchAndThePosition(t *testing.T) {
	n := &recordingNudger{inserted: true}
	svc, _ := nudgeSvc(n)

	res, err := svc.NudgeProcurement(context.Background(), BranchReach{Global: true}, "p-1", "b-trt")
	if err != nil {
		t.Fatalf("nudge: %v", err)
	}
	for _, want := range []string{"Techo-Bloc Blu 60 Slab", "DBS-TB-BLU60", "TRT", "8 PC"} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("the message does not say %q: %s", want, res.Body)
		}
	}
	if res.Body != n.got.Body {
		t.Error("the body reported to the surface is not the body that was posted")
	}
}

// TestNudgeBody_QuantitiesCarryTheirUnitAndAreNotPrintedAsDecimals.
func TestNudgeBody_QuantitiesCarryTheirUnitAndAreNotPrintedAsDecimals(t *testing.T) {
	p := ProductLookup{Name: "Blu 60", SKU: "SKU-1", UOM: "PC"}
	body := NudgeBody(p, "TRT", BranchStock{OnHandBase: 120, AvailableBase: 96, InTransitBase: 15})
	for _, want := range []string{"96 PC available", "120 PC on hand", "15 PC in transit"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
	// It must NOT claim anything about what is on order: the only figure this module can compute is
	// the DIB-72 leg, which counts receiving documents rather than W4 purchase orders and is
	// therefore structurally zero. Asserting "nothing on order" would contradict the On-order
	// section the same person is reading (PARK P5).
	if strings.Contains(body, "on order") {
		t.Errorf("the nudge claims something about on-order stock it cannot see: %s", body)
	}
	if strings.Contains(body, ".0000") {
		t.Errorf("a DECIMAL(19,4) leaked into a sentence a person reads: %s", body)
	}
}

// TestNudgeEventKey_IsOnePerProductPerBranchPerDay, the idempotency contract, as a value.
func TestNudgeEventKey_IsOnePerProductPerBranchPerDay(t *testing.T) {
	morning := time.Date(2026, 8, 12, 7, 30, 0, 0, time.UTC)
	evening := time.Date(2026, 8, 12, 19, 45, 0, 0, time.UTC)
	tomorrow := time.Date(2026, 8, 13, 7, 30, 0, 0, time.UTC)

	if NudgeEventKey("p-1", "b-trt", morning) != NudgeEventKey("p-1", "b-trt", evening) {
		t.Error("two nudges on the same day produced different keys, the second would post again")
	}
	if NudgeEventKey("p-1", "b-trt", morning) == NudgeEventKey("p-1", "b-trt", tomorrow) {
		t.Error("tomorrow's nudge shares today's key, a product still short tomorrow is new information")
	}
	if NudgeEventKey("p-1", "b-trt", morning) == NudgeEventKey("p-1", "b-kgn", morning) {
		t.Error("two branches share one key, nudging Trenton would silence Kingston")
	}
	if NudgeEventKey("p-1", "b-trt", morning) == NudgeEventKey("p-2", "b-trt", morning) {
		t.Error("two products share one key")
	}
}

// TestNudge_ASecondNudgeTodayIsPolitelyRefused, inserted=false is an ANSWER, not an error, and the
// person is told what they wanted to know: procurement has already been told.
func TestNudge_ASecondNudgeTodayIsPolitelyRefused(t *testing.T) {
	n := &recordingNudger{inserted: false} // messaging: the event key already exists today
	svc, _ := nudgeSvc(n)

	res, err := svc.NudgeProcurement(context.Background(), BranchReach{Global: true}, "p-1", "b-trt")
	if err != nil {
		t.Fatalf("a repeat nudge is not an error: %v", err)
	}
	if res.Posted {
		t.Fatal("a repeat nudge reported itself as posted, the channel would show one message and the screen two")
	}
	if res.Note != NudgeAlreadyNote {
		t.Fatalf("note = %q, want the already-nudged sentence", res.Note)
	}
}

// TestNudge_RefusedForABranchTheCallerCannotSee, the write must not defeat the read fence.
func TestNudge_RefusedForABranchTheCallerCannotSee(t *testing.T) {
	n := &recordingNudger{inserted: true}
	svc, _ := nudgeSvc(n)

	_, err := svc.NudgeProcurement(context.Background(), BranchReach{BranchIDs: []string{"b-kgn"}}, "p-1", "b-trt")
	if err == nil {
		t.Fatal("nudging about an ungranted branch must be refused")
	}
	if n.calls != 0 {
		t.Fatal("a message carrying another branch's numbers was posted")
	}
}

// TestNudge_UnwiredMessagingRefusesLoudly, the StubProcurementNudger posture. A silent success here
// would leave somebody waiting for a reply to a message that was never sent.
func TestNudge_UnwiredMessagingRefusesLoudly(t *testing.T) {
	f := &fakeIncoming{
		product: ProductLookup{ProductID: "p-1", SKU: "S", Name: "N", UOM: "PC"},
		found:   true,
		rows:    []BranchStock{{BranchID: "b-trt", BranchCode: "TRT"}},
	}
	svc := newIncomingSvc(f) // no nudger → StubProcurementNudger

	res, err := svc.NudgeProcurement(context.Background(), BranchReach{Global: true}, "p-1", "b-trt")
	if err == nil {
		t.Fatal("an unwired messaging seam must refuse rather than report a nudge nobody received")
	}
	if res.Posted {
		t.Fatal("posted=true with no messaging behind it")
	}
	if res.Body == "" {
		t.Error("the body must ride along with the refusal so an operator can see what was lost")
	}
}

func TestNudge_RefusesWithoutAProductOrBranch(t *testing.T) {
	n := &recordingNudger{inserted: true}
	svc, _ := nudgeSvc(n)
	if _, err := svc.NudgeProcurement(context.Background(), BranchReach{Global: true}, "", "b-trt"); err == nil {
		t.Error("a nudge with no product is a 400")
	}
	if _, err := svc.NudgeProcurement(context.Background(), BranchReach{Global: true}, "p-1", ""); err == nil {
		t.Error("a nudge with no branch is a 400, the message has to say which yard")
	}
	if n.calls != 0 {
		t.Fatal("an incomplete nudge reached the channel")
	}
}

// - the branch a control acts on (found by the Chromium walkthrough) -

// TestResolveBranch_AnEmptyBranchMeansTheCallerOwnYard.
//
// THE DEFECT THIS PINS. The quick view's four-bucket grid is deliberately CROSS-BRANCH, so a
// Trenton-only clerk sees Kingston's row on the card, and the control, which picks the yard with
// the least available, was aimed at a branch that clerk cannot act on. The fence refused it, which
// is correct and useless: a button that always fails is a broken feature, not a safe one. The reach
// is the authority on "my yard", so the reach is what resolves an omitted branch.
func TestResolveBranch_AnEmptyBranchMeansTheCallerOwnYard(t *testing.T) {
	got, err := resolveBranch(BranchReach{BranchIDs: []string{"b-trt"}}, "")
	if err != nil || got != "b-trt" {
		t.Fatalf("resolveBranch = %q, %v, one grant is one answer", got, err)
	}
}

func TestResolveBranch_RefusesToGuessForAMultiBranchCaller(t *testing.T) {
	if _, err := resolveBranch(BranchReach{Global: true}, ""); err == nil {
		t.Error("a GLOBAL caller has no home yard, picking one for them must be refused")
	}
	if _, err := resolveBranch(BranchReach{BranchIDs: []string{"b-trt", "b-kgn"}}, ""); err == nil {
		t.Error("two grants and no branch: picking one would depend on map iteration order")
	}
	if _, err := resolveBranch(BranchReach{}, ""); err == nil {
		t.Error("no grants means no yard to act on")
	}
}

func TestResolveBranch_AnExplicitBranchMustBeInsideTheReach(t *testing.T) {
	got, err := resolveBranch(BranchReach{BranchIDs: []string{"b-trt", "b-kgn"}}, "b-kgn")
	if err != nil || got != "b-kgn" {
		t.Fatalf("a granted branch must be accepted: %q %v", got, err)
	}
	if _, err := resolveBranch(BranchReach{BranchIDs: []string{"b-trt"}}, "b-kgn"); err == nil {
		t.Error("a branch outside the reach was accepted")
	}
	if got, err := resolveBranch(BranchReach{Global: true}, "b-kgn"); err != nil || got != "b-kgn" {
		t.Errorf("a GLOBAL caller may name any branch: %q %v", got, err)
	}
}
