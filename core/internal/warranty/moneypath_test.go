package warranty

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// moneypath_test.go, ERP:warranty-p2 acceptance: EACH outcome posts its OWN named money path.
//
// The assertion in every case is three-part and deliberately so: the named PATH, the STATUS, and the artefact
// (the journal's account legs, or the seam request). Asserting only "a journal posted" is what let P1 collapse
// four money consequences into two without any test noticing.

// - VENDOR_CREDIT → VENDOR_RECOVERY (GL) -

func TestMoneyPath_VendorCredit_PostsVendorRecoveryAndAccrues(t *testing.T) {
	svc, fs, gl := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 4_000_00})
	if err != nil {
		t.Fatal(err)
	}
	if res.MoneyPath != PathVendorRecovery || res.PathSeam != SeamGL || res.PathStatus != PathPosted {
		t.Fatalf("vendor credit must take the VENDOR_RECOVERY/GL path and POST; got %q/%q/%q",
			res.MoneyPath, res.PathSeam, res.PathStatus)
	}
	if len(gl.posted) != 1 {
		t.Fatalf("want exactly one journal; got %d", len(gl.posted))
	}
	// The AR-side recovery entry: DR clearing, CR warranty recovery, from the CONFIG, not a constant.
	if got := legs(gl.posted[0]); got != "DR1100:400000 CR4900:400000" {
		t.Errorf("vendor recovery journal legs wrong: %s", got)
	}
	// And it ACCRUES: this is the money the WTY-RECOVERY board chases.
	if res.AccruedCents != 4_000_00 {
		t.Errorf("vendor credit must accrue the recovery; got %d", res.AccruedCents)
	}
	p, ok := fs.posting(id)
	if !ok {
		t.Fatal("no posting row recorded")
	}
	if !p.AccruesRecovery || p.AmountCents != 4_000_00 || p.GLJournalID == "" {
		t.Errorf("posting row wrong: %+v", p)
	}
}

// - CUSTOMER_ACCOMMODATION → CUSTOMER_CREDIT (BILLING) -

func TestMoneyPath_CustomerAccommodation_IssuesCreditMemoThroughBillingSeam(t *testing.T) {
	svc, fs, gl := newSvc()
	credit := &fakeCredit{res: CustomerCreditResult{CreditMemoID: "cm-9", CreditMemoNo: "CM-2026-118"}}
	svc.WithCustomerCredit(credit)
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, CustomerID: "cust-7"})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeCustomerAccommodation, RealizedCents: 1_250_00})
	if err != nil {
		t.Fatal(err)
	}
	if res.MoneyPath != PathCustomerCredit || res.PathSeam != SeamBilling || res.PathStatus != PathPosted {
		t.Fatalf("accommodation must take the CUSTOMER_CREDIT/BILLING path and POST; got %q/%q/%q",
			res.MoneyPath, res.PathSeam, res.PathStatus)
	}
	if res.ExternalRef != "CM-2026-118" {
		t.Errorf("the credit-memo document must be recorded on the settlement; got %q", res.ExternalRef)
	}
	// Billing owns the journal for a credit memo. Warranty must NOT also post one, that is the double-book.
	if len(gl.posted) != 0 {
		t.Errorf("warranty must not post its own journal when billing issued the credit memo; got %d", len(gl.posted))
	}
	if len(credit.reqs) != 1 {
		t.Fatalf("want one billing call; got %d", len(credit.reqs))
	}
	req := credit.reqs[0]
	if req.AmountCents != 1_250_00 || req.CustomerID != "cust-7" || req.IdempotencyKey != "warranty-credit:"+id {
		t.Errorf("billing request wrong: %+v", req)
	}
	// A credit going OUT is not money owed TO us: it must never appear on the recovery board.
	if res.AccruedCents != 0 {
		t.Errorf("a customer credit must not accrue vendor recovery; got %d", res.AccruedCents)
	}
	if p, _ := fs.posting(id); p.AccruesRecovery {
		t.Error("the customer-credit posting must not be marked as accruing recovery")
	}
}

func TestMoneyPath_CustomerAccommodation_UnwiredBillingFallsBackToExpenseJournal(t *testing.T) {
	svc, fs, gl := newSvc() // billing seam deliberately UNBOUND
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeCustomerAccommodation, RealizedCents: 900_00})
	if err != nil {
		t.Fatal(err)
	}
	// FALLBACK_GL, deliberately NOT POSTED: the books are right, the document the customer expects does not
	// exist yet, and conflating those two would hide a missing credit memo behind a green tick.
	if res.PathStatus != PathFallbackGL {
		t.Fatalf("an unbound billing seam must record FALLBACK_GL; got %q", res.PathStatus)
	}
	if res.ExternalRef != "" {
		t.Error("a fallback journal is not a credit-memo document, external_ref must stay empty")
	}
	if len(gl.posted) != 1 || legs(gl.posted[0]) != "DR5900:90000 CR1100:90000" {
		t.Errorf("fallback must post the warranty-expense pair; got %d journals %v", len(gl.posted), gl.posted)
	}
	if p, _ := fs.posting(id); p.Status != PathFallbackGL || p.GLJournalID == "" {
		t.Errorf("the FALLBACK_GL posting must carry its journal; got %+v", p)
	}
}

func TestMoneyPath_CustomerAccommodation_BoundBillingRefusal_DoesNotDoubleBook(t *testing.T) {
	svc, _, gl := newSvc()
	svc.WithCustomerCredit(&fakeCredit{err: errors.New("billing: customer on hold")})
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeCustomerAccommodation, RealizedCents: 500_00})
	if err != nil {
		t.Fatal("a refused money path must never un-book a recorded settlement")
	}
	if res.PathStatus != PathFailed {
		t.Fatalf("a BOUND seam that refused is FAILED, not a fallback; got %q", res.PathStatus)
	}
	// The critical arm: billing may have partially created the memo. Posting the expense ourselves on top of
	// that is exactly how a double-book happens.
	if len(gl.posted) != 0 {
		t.Errorf("a bound-seam refusal must not trigger the GL fallback; got %d journals", len(gl.posted))
	}
	if !strings.Contains(res.GLNote, "customer on hold") {
		t.Errorf("the refusal reason must survive to the caller; got %q", res.GLNote)
	}
}

// - REPLACEMENT_STOCK → INVENTORY_REPLACEMENT (INVENTORY) -

func TestMoneyPath_Replacement_MovesCostThroughInventorySeam(t *testing.T) {
	svc, fs, gl := newSvc()
	inv := &fakeInvCost{res: ReplacementCostResult{MovementRef: "MOVE-4412"}}
	svc.WithInventoryCost(inv)
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{
		BranchID: testBranch,
		Lines:    []ClaimLineInput{{ProductID: "prod-1", ProductText: "Blu 60 Slab", Qty: "42", UOMCode: "EA"}},
	})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeReplacementStock, RealizedCents: 2_100_00})
	if err != nil {
		t.Fatal(err)
	}
	if res.MoneyPath != PathInventoryReplacement || res.PathSeam != SeamInventory || res.PathStatus != PathPosted {
		t.Fatalf("replacement must take the INVENTORY_REPLACEMENT/INVENTORY path and POST; got %q/%q/%q",
			res.MoneyPath, res.PathSeam, res.PathStatus)
	}
	if res.ExternalRef != "MOVE-4412" {
		t.Errorf("the inventory movement ref must be recorded; got %q", res.ExternalRef)
	}
	if len(gl.posted) != 0 {
		t.Error("warranty must never post a cash journal for a replacement, inventory owns the cost movement")
	}
	if len(inv.reqs) != 1 {
		t.Fatalf("want one inventory call; got %d", len(inv.reqs))
	}
	req := inv.reqs[0]
	if len(req.Lines) != 1 || req.Lines[0].ProductID != "prod-1" || req.Lines[0].Qty != "42" {
		t.Errorf("the claim's product lines must ride the replacement request; got %+v", req.Lines)
	}
	if req.IdempotencyKey != "warranty-replace:"+id {
		t.Errorf("replacement idempotency key wrong: %q", req.IdempotencyKey)
	}
	if p, _ := fs.posting(id); p.ExternalRef != "MOVE-4412" || p.AccruesRecovery {
		t.Errorf("replacement posting wrong: %+v", p)
	}
}

func TestMoneyPath_Replacement_UnwiredInventoryHasNoFallbackAndPreservesTheRequest(t *testing.T) {
	svc, fs, gl := newSvc() // inventory seam deliberately UNBOUND
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{
		BranchID: testBranch,
		Lines:    []ClaimLineInput{{ProductText: "grey pavers", Qty: "12"}},
	})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeReplacementStock, RealizedCents: 800_00})
	if err != nil {
		t.Fatal(err)
	}
	if res.PathStatus != PathSeamUnwired {
		t.Fatalf("an unbound inventory seam must record SEAM_UNWIRED; got %q", res.PathStatus)
	}
	// NO fallback, on purpose: fabricating a cost movement for stock that may never have left the yard would be
	// a lie in the inventory valuation.
	if len(gl.posted) != 0 {
		t.Errorf("the replacement path must have NO GL fallback; got %d journals", len(gl.posted))
	}
	p, ok := fs.posting(id)
	if !ok {
		t.Fatal("no posting row recorded")
	}
	// The preserved request is what makes the union's job "bind one adapter and replay" rather than "reconstruct
	// what should have happened from a log line".
	if p.Request["seam"] != "inventory.MoveReplacementCost" {
		t.Errorf("the unwired request must be preserved for replay; got %+v", p.Request)
	}
	if lines, _ := p.Request["lines"].([]map[string]any); len(lines) != 1 {
		t.Errorf("the preserved request must carry the claim lines; got %+v", p.Request["lines"])
	}
}

// - WRITE_OFF (denied) → EXPENSE_WRITE_OFF (GL) -

func TestMoneyPath_WriteOff_PostsWarrantyExpense(t *testing.T) {
	svc, fs, gl := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeWriteOff, RealizedCents: 675_00})
	if err != nil {
		t.Fatal(err)
	}
	if res.MoneyPath != PathExpenseWriteOff || res.PathSeam != SeamGL || res.PathStatus != PathPosted {
		t.Fatalf("a denied claim must take the EXPENSE_WRITE_OFF/GL path and POST; got %q/%q/%q",
			res.MoneyPath, res.PathSeam, res.PathStatus)
	}
	if len(gl.posted) != 1 || legs(gl.posted[0]) != "DR5900:67500 CR1100:67500" {
		t.Errorf("write-off journal legs wrong: %d journals %v", len(gl.posted), gl.posted)
	}
	// A write-off recovers nothing. It must not put money on the recovery board.
	if res.AccruedCents != 0 {
		t.Errorf("a write-off must not accrue recovery; got %d", res.AccruedCents)
	}
	if p, _ := fs.posting(id); p.AccruesRecovery {
		t.Error("the write-off posting must not be marked as accruing recovery")
	}
}

// - the four paths are DISTINCT (the regression P1 could not see) -

func TestMoneyPath_EachOutcomeTakesADistinctNamedPath(t *testing.T) {
	want := map[string]string{
		OutcomeVendorCredit:          PathVendorRecovery,
		OutcomeCustomerAccommodation: PathCustomerCredit,
		OutcomeReplacementStock:      PathInventoryReplacement,
		OutcomeWriteOff:              PathExpenseWriteOff,
	}
	seen := map[string]string{}
	for outcome, path := range want {
		svc, _, _ := newSvc()
		ctx := ctxWith(staff("u1"))
		id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
		res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: outcome, RealizedCents: 100_00})
		if err != nil {
			t.Fatalf("%s: %v", outcome, err)
		}
		if res.MoneyPath != path {
			t.Errorf("%s must take %s; got %s", outcome, path, res.MoneyPath)
		}
		if prev, dup := seen[res.MoneyPath]; dup {
			t.Errorf("%s and %s collapsed onto the same money path %s", prev, outcome, res.MoneyPath)
		}
		seen[res.MoneyPath] = outcome
	}
	if len(seen) != 4 {
		t.Fatalf("want four distinct money paths; got %d", len(seen))
	}
}

// - configuration, failures, and the manual override -

func TestMoneyPath_UnconfiguredOutcome_PostsNothingAndSaysSo(t *testing.T) {
	svc, fs, gl := newSvc()
	delete(fs.paths, OutcomeVendorCredit) // the accountant deactivated the row mid-flight
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 300_00})
	if err != nil {
		t.Fatal("a configuration gap must not un-book a settlement the manager was told succeeded")
	}
	if len(gl.posted) != 0 {
		t.Error("an unconfigured outcome must NOT default to a journal, inventing a money path is worse than declining")
	}
	if !strings.Contains(res.GLNote, "no money path is configured") {
		t.Errorf("the note must say why nothing moved; got %q", res.GLNote)
	}
	// The settlement itself still stands: amounts recorded, claim settled.
	if fs.claims[id].RealizedCents != 300_00 || fs.claims[id].Outcome != OutcomeVendorCredit {
		t.Error("the settlement must remain recorded")
	}
}

func TestMoneyPath_GLFailure_RecordsFailedAndDoesNotAccrue(t *testing.T) {
	svc, fs, gl := newSvc()
	gl.fail = true
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 2_000_00})
	if err != nil {
		t.Fatal("a GL failure must never un-book a settlement")
	}
	if res.PathStatus != PathFailed || res.GLPosted {
		t.Fatalf("a failed post must be FAILED and not gl_posted; got %q/%v", res.PathStatus, res.GLPosted)
	}
	// Accruing off a failed post would put money on the recovery board that the ledger has never heard of.
	if res.AccruedCents != 0 {
		t.Errorf("a failed recovery post must not accrue; got %d", res.AccruedCents)
	}
	if p, _ := fs.posting(id); p.AccruesRecovery {
		t.Error("the posting row must not claim an accrual the ledger does not have")
	}
}

func TestMoneyPath_GLReturnsBlankJournalID_IsFailedNotPosted(t *testing.T) {
	svc, _, gl := newSvc()
	gl.blankID = true
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeWriteOff, RealizedCents: 100_00})
	if err != nil {
		t.Fatal(err)
	}
	// The 0129 CHECK requires a POSTED row to show its work; a blank id is not work shown, so reporting POSTED
	// here would write a row the database refuses, and, worse, tell a manager the money moved.
	if res.PathStatus != PathFailed {
		t.Fatalf("a provider that returns no journal id must be FAILED; got %q", res.PathStatus)
	}
}

func TestMoneyPath_ManualOverride_RunsNoPathAndAccruesNothing(t *testing.T) {
	svc, fs, gl := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})

	res, err := svc.RecordSettlement(ctx, id, SettlementInput{
		Outcome: OutcomeVendorCredit, RealizedCents: 5_000_00,
		ManualOverride: true, OverrideReason: "Bookkeeper applied Techo's credit note CN-8812 in March",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.PathStatus != PathManual {
		t.Fatalf("P1's manual settlement must remain available as an explicit override; got %q", res.PathStatus)
	}
	if len(gl.posted) != 0 {
		t.Error("a manual override must post nothing, the money was booked by hand")
	}
	// And it must NOT accrue: money the bookkeeper already collected would sit on the recovery worklist forever.
	if res.AccruedCents != 0 {
		t.Errorf("a manual override must not accrue; got %d", res.AccruedCents)
	}
	p, _ := fs.posting(id)
	if p.AccruesRecovery || !strings.Contains(p.Note, "CN-8812") {
		t.Errorf("the override reason must be recorded on the posting; got %+v", p)
	}
}

func TestMoneyPath_ZeroAmount_RecordsNoEffectForEveryOutcome(t *testing.T) {
	for _, outcome := range []string{OutcomeVendorCredit, OutcomeCustomerAccommodation, OutcomeReplacementStock, OutcomeWriteOff} {
		svc, _, gl := newSvc()
		svc.WithCustomerCredit(&fakeCredit{res: CustomerCreditResult{CreditMemoNo: "CM-1"}})
		svc.WithInventoryCost(&fakeInvCost{res: ReplacementCostResult{MovementRef: "MOVE-1"}})
		ctx := ctxWith(staff("u1"))
		id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
		res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: outcome, RealizedCents: 0})
		if err != nil {
			t.Fatalf("%s: %v", outcome, err)
		}
		if res.PathStatus != PathNoEffect {
			t.Errorf("%s at $0 must be NO_EFFECT; got %q", outcome, res.PathStatus)
		}
		if len(gl.posted) != 0 {
			t.Errorf("%s at $0 must post nothing", outcome)
		}
		if res.MoneyPath == "" {
			t.Errorf("%s at $0 must still NAME its path", outcome)
		}
	}
}

func TestMoneyPath_PostingsAreBranchScoped(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeWriteOff, RealizedCents: 100}); err != nil {
		t.Fatal(err)
	}
	// A staffer who cannot reach the claim's branch cannot read what its money did either.
	outsider := ctxWith(staffAt("u2", otherBranch))
	if _, err := svc.SettlementPostings(outsider, id); err == nil {
		t.Fatal("money-path reads must obey the same branch fence as the claim")
	}
}

// compile-time assertions that the two W5-B seam doubles satisfy the ports the union will bind.
var (
	_ CustomerCreditIssuer = (*fakeCredit)(nil)
	_ InventoryCostMover   = (*fakeInvCost)(nil)
	_ SLANotifier          = (*fakeNotifier)(nil)
	_ context.Context      = context.Background()
)
