package pos

// returns_fence_test.go, the branch wall ahead of every rail (X2, fixing
// HH-218). Today's hole the plan records: a card reversal reached the
// processor before the till's branch check, and the account credit rail had
// no check at all. These tests hold the order the rule names: the wall's
// 403 comes back and NO rail has been touched, whatever the refund method,
// and the return lookup answers the same wall before it reads another
// branch's returnable lines.

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// identityBranchScoped is a caller whose resolved grants reach exactly one
// branch, the shape every branch-scoped demo role resolves to.
func identityBranchScoped(userID, roleCode, branchID string) identity.StaffContext {
	return identity.StaffContext{UserID: userID, RoleCode: roleCode,
		Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Branches: map[string]bool{branchID: true}}
}

// kingstonClerk is a caller whose resolved grants reach another branch, not
// the branch the seeded sale lives at.
func kingstonClerk() (h *harness) {
	h = newHarness()
	seedReturnable(h)
	h.caller = identityBranchScoped("u-other", "INSIDE_SALES", "branch-kgt")
	return h
}

// TestReturn_Card_WallBeforeTheProcessor: the reversal must never reach the
// card rail for a sale the caller cannot reach.
func TestReturn_Card_WallBeforeTheProcessor(t *testing.T) {
	h := kingstonClerk()
	oc := h.store.retCtx["order-1"]
	oc.SaleTender = TenderCard
	oc.GatewayRef = "rp_orig_capture"
	h.store.retCtx["order-1"] = oc

	in := returnInput()
	in.RefundMethod = RefundCard
	in.TillSessionID = ""
	_, err := h.svc.Return(context.Background(), in)
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("want the wall's 403, got %v", err)
	}
	if h.refunder.called {
		t.Fatal("the processor must not see a reversal the wall refused (HH-218)")
	}
	if h.tender.refundCalled {
		t.Fatal("no till refund row may move behind a refused reversal")
	}
	if len(h.store.returnRows) != 0 {
		t.Fatal("a walled return must persist nothing")
	}
}

// TestReturn_Cash_WallBeforeTheDrawer: no drawer payout for another
// branch's sale.
func TestReturn_Cash_WallBeforeTheDrawer(t *testing.T) {
	h := kingstonClerk()
	_, err := h.svc.Return(context.Background(), returnInput())
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("want the wall's 403, got %v", err)
	}
	if h.tender.refundCalled {
		t.Fatal("the drawer must not pay out behind the wall")
	}
}

// TestReturn_AccountCredit_WallBeforeTheLedger: the account credit rail had
// no branch check at all; the wall stands in front of it now.
func TestReturn_AccountCredit_WallBeforeTheLedger(t *testing.T) {
	h := kingstonClerk()
	oc := h.store.retCtx["order-1"]
	oc.SaleTender = TenderAccount
	h.store.retCtx["order-1"] = oc

	in := returnInput()
	in.RefundMethod = RefundAccountCredit
	in.TillSessionID = ""
	_, err := h.svc.Return(context.Background(), in)
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("want the wall's 403, got %v", err)
	}
	if len(h.store.custTxns) != 0 {
		t.Fatal("no credit memo may move behind the wall")
	}
}

// TestReturn_WallBeforeIdempotentInsert: the wall stands before the return
// row is even drafted, so the refused attempt leaves no row and no replay
// slot behind.
func TestReturn_WallBeforeIdempotentInsert(t *testing.T) {
	h := kingstonClerk()
	_, err := h.svc.Return(context.Background(), returnInput())
	if err == nil {
		t.Fatal("want the wall's refusal")
	}
	if len(h.store.returnRows) != 0 || len(h.store.returnLineRows) != 0 {
		t.Fatal("a walled return must not draft its row")
	}
	cached, found, err := h.store.FindReturnByKey(context.Background(), "ret-key-1")
	if err != nil || found {
		t.Fatalf("the refused key must not be reservable: found=%v err=%v", found, err)
	}
	_ = cached
}

// TestLookupReturn_WallBeforeTheLines: the pre-return read refuses another
// branch's sale before its lines are read. The fake store serves the
// returnable lines from the same seeded map for any order id, so the
// assertion is the refusal itself; a real store would have read the lines
// only behind the wall.
func TestLookupReturn_WallBeforeTheLines(t *testing.T) {
	h := kingstonClerk()
	_, err := h.svc.LookupReturn(context.Background(), "order-1", "")
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("want the wall's 403 on the lookup, got %v", err)
	}
}

// A caller granted the sale's branch reads and returns as before: the wall
// is a wall, not a gate that squeezes the home branch.
func TestReturn_HomeBranch_Unfenced(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	h.caller = identityBranchScoped("u-trt", "INSIDE_SALES", "branch-trt")
	out, err := h.svc.Return(context.Background(), returnInput())
	if err != nil {
		t.Fatalf("home-branch cash return: %v", err)
	}
	if out.TotalCents != 4520 {
		t.Fatalf("home-branch return money wrong: %+v", out)
	}
}

// The replay sits BEHIND the wall (the lead's P2-2 ruling): a foreign
// clerk holding a committed return's idempotency key refuses exactly like
// the first attempt did, and never reads the return's number, sale,
// invoice, payment id or money.
func TestReturn_ReplayOfAForeignSale_RefusesLikeTheFirstAttempt(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	h.caller = identityBranchScoped("u-trt", "INSIDE_SALES", "branch-trt")
	first, err := h.svc.Return(context.Background(), returnInput())
	if err != nil {
		t.Fatalf("first return: %v", err)
	}
	if first.ReturnID == "" {
		t.Fatalf("the first return committed: %+v", first)
	}

	h.caller = identityBranchScoped("u-kgt", "INSIDE_SALES", "branch-kgt")
	replay, err := h.svc.Return(context.Background(), returnInput())
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("the foreign replay must hit the wall, got %v (answer %+v)", err, replay)
	}
	if replay.ReturnID != "" || replay.ReturnNo != "" || replay.TotalCents != 0 {
		t.Fatalf("a walled replay must carry none of the committed return: %+v", replay)
	}
}

// The reviewer's case C (the lead's P2-2R ruling): a foreign clerk holding
// a committed return's key and naming a sale of their OWN that they DO
// reach. The wall judges the REPLAYED return's sale: the key buys nothing,
// and the refusal is the wall's words, never another branch's return.
func TestReturn_ReplayOfAForeignSale_KeyWithAnOwnedSaleInTheBody_Refuses(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	// The foreign clerk's own Kingston sale, reachable to them.
	h.store.retCtx["order-kgt"] = returnOrderCtx{
		OrderID: "order-kgt", OrderNo: "#SO-KGT", BranchID: "branch-kgt", CustomerID: "cust-abc",
		InvoiceID: "inv-kgt", InvoiceStatus: "PAID", SaleTender: TenderCash,
	}
	h.store.retLines["line-kgt"] = returnableLine{
		OrderLineID: "line-kgt", ProductID: "prod-blu", QtySold: 4,
		UnitPriceCents: 1000, LineTotalCents: 4000, TaxCents: 520,
	}
	h.caller = identityBranchScoped("u-trt", "INSIDE_SALES", "branch-trt")
	first, err := h.svc.Return(context.Background(), returnInput())
	if err != nil {
		t.Fatalf("first return: %v", err)
	}
	if first.OrderID != "order-1" {
		t.Fatalf("the first return is the Trenton sale's: %+v", first)
	}

	// The Kingston clerk, the TRENTON key, their OWN Kingston sale in the
	// body.
	h.caller = identityBranchScoped("u-kgt", "INSIDE_SALES", "branch-kgt")
	in := returnInput()
	in.OrderID = "order-kgt"
	in.TillSessionID = "till-kgt"
	replay, err := h.svc.Return(context.Background(), in)
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("the wall must judge the replayed return's sale, got %v (answer %+v)", err, replay)
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("the refusal must be the wall's words, got %v", err)
	}
	if replay.ReturnID != "" || replay.ReturnNo != "" || replay.TotalCents != 0 {
		t.Fatalf("a walled replay must carry none of the committed return: %+v", replay)
	}
}

// Round 11: the typed sale reference is compared without its hash, case or stray spaces, so the
// stored #SO-TRT-21 and #INV-17 are found by what the clerk reads off a slip.
func TestNormalizeSaleRef(t *testing.T) {
	cases := map[string]string{
		"#SO-TRT-21":  "SO-TRT-21",
		"SO-TRT-21":   "SO-TRT-21",
		" so-trt-21 ": "SO-TRT-21",
		"#inv-17":     "INV-17",
		"21":          "21",
		"":            "",
		"   ":         "",
		"##SO-TRT-21": "#SO-TRT-21", // only the one leading hash is the stored prefix
	}
	for in, want := range cases {
		if got := normalizeSaleRef(in); got != want {
			t.Errorf("normalizeSaleRef(%q) = %q, want %q", in, got, want)
		}
	}
}
