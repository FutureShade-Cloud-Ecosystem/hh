package warranty

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// recovery_test.go, DIB-221 acceptance: the WTY-RECOVERY arithmetic.
//
// The one equation the client's ~$40k depends on is outstanding = accrued − received. It is asserted here at
// three levels: the pure board math (BuildRecoveryBoard), the reconciliation RULE that stops an over-receipt,
// and the end-to-end settle → chase → receive walk.

var boardNow = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

func daysAgo(n int) string { return boardNow.AddDate(0, 0, -n).Format(time.RFC3339) }

// - the equation -

func TestRecoveryBoard_OutstandingIsAccruedMinusReceived(t *testing.T) {
	rows := []RecoveryRow{
		// Accrued 4000.00, received 1500.00 → 2500.00 still owed.
		{ClaimID: "c1", ClaimNo: "WTY-1", VendorID: "v1", VendorName: "Techo-Bloc",
			AccruedCents: 4_000_00, ReceivedCents: 1_500_00, OutstandingCents: 2_500_00, AccruedAt: daysAgo(10)},
		// Nothing received: the whole accrual is outstanding.
		{ClaimID: "c2", ClaimNo: "WTY-2", VendorID: "v1", VendorName: "Techo-Bloc",
			AccruedCents: 1_200_00, ReceivedCents: 0, OutstandingCents: 1_200_00, AccruedAt: daysAgo(45)},
		// Fully received: reconciled, and contributes NOTHING to any aging bucket.
		{ClaimID: "c3", ClaimNo: "WTY-3", VendorID: "v2", VendorName: "Oaks",
			AccruedCents: 900_00, ReceivedCents: 900_00, OutstandingCents: 0, AccruedAt: daysAgo(120)},
	}
	b := BuildRecoveryBoard(rows, boardNow)

	if b.TotalAccruedCents != 6_100_00 {
		t.Errorf("accrued total wrong: %d", b.TotalAccruedCents)
	}
	if b.TotalReceivedCents != 2_400_00 {
		t.Errorf("received total wrong: %d", b.TotalReceivedCents)
	}
	if b.TotalOutstandingCents != 3_700_00 {
		t.Errorf("outstanding must be accrued − received (6100.00 − 2400.00 = 3700.00); got %d", b.TotalOutstandingCents)
	}
	if b.TotalAccruedCents-b.TotalReceivedCents != b.TotalOutstandingCents {
		t.Error("the board's own totals must satisfy the equation")
	}
	if b.OpenClaimCount != 2 || b.ReconciledClaimCount != 1 {
		t.Errorf("open/reconciled split wrong: %d/%d", b.OpenClaimCount, b.ReconciledClaimCount)
	}
}

func TestRecoveryBoard_AgingBucketsMeasureFromTheAccrualNotTheClaim(t *testing.T) {
	rows := []RecoveryRow{
		{ClaimID: "c1", ClaimNo: "WTY-1", VendorID: "v1", AccruedCents: 100_00, OutstandingCents: 100_00, AccruedAt: daysAgo(5)},
		{ClaimID: "c2", ClaimNo: "WTY-2", VendorID: "v1", AccruedCents: 200_00, OutstandingCents: 200_00, AccruedAt: daysAgo(45)},
		{ClaimID: "c3", ClaimNo: "WTY-3", VendorID: "v1", AccruedCents: 400_00, OutstandingCents: 400_00, AccruedAt: daysAgo(75)},
		{ClaimID: "c4", ClaimNo: "WTY-4", VendorID: "v1", AccruedCents: 800_00, OutstandingCents: 800_00, AccruedAt: daysAgo(200)},
	}
	b := BuildRecoveryBoard(rows, boardNow)
	want := map[string]int64{"0-30": 100_00, "31-60": 200_00, "61-90": 400_00, "90+": 800_00}
	for bucket, cents := range want {
		if b.Buckets[bucket] != cents {
			t.Errorf("bucket %s: want %d got %d", bucket, cents, b.Buckets[bucket])
		}
	}
}

func TestRecoveryBoard_ReconciledClaimsContributeNoAging(t *testing.T) {
	// A 200-day-old accrual that came back in full is not "90+ money owed", it is not owed at all. Counting it
	// would make the worst bucket, the one a manager acts on, meaningless.
	rows := []RecoveryRow{
		{ClaimID: "c1", ClaimNo: "WTY-1", VendorID: "v1", AccruedCents: 5_000_00, ReceivedCents: 5_000_00,
			OutstandingCents: 0, AccruedAt: daysAgo(200)},
	}
	b := BuildRecoveryBoard(rows, boardNow)
	if b.Buckets["90+"] != 0 {
		t.Errorf("a fully-received accrual must age nothing; got %d in 90+", b.Buckets["90+"])
	}
	if len(b.Vendors) != 1 || b.Vendors[0].OldestDays != 0 {
		t.Errorf("a reconciled vendor has no oldest OUTSTANDING money; got %+v", b.Vendors)
	}
}

func TestRecoveryBoard_PerVendorRollupAndWorklistOrder(t *testing.T) {
	rows := []RecoveryRow{
		{ClaimID: "a", ClaimNo: "WTY-1", VendorID: "v1", VendorName: "Techo-Bloc", AccruedCents: 1_000_00, OutstandingCents: 1_000_00, AccruedAt: daysAgo(10)},
		{ClaimID: "b", ClaimNo: "WTY-2", VendorID: "v1", VendorName: "Techo-Bloc", AccruedCents: 3_000_00, OutstandingCents: 3_000_00, AccruedAt: daysAgo(95)},
		{ClaimID: "c", ClaimNo: "WTY-3", VendorID: "v2", VendorName: "Oaks", AccruedCents: 2_000_00, OutstandingCents: 2_000_00, AccruedAt: daysAgo(3)},
	}
	b := BuildRecoveryBoard(rows, boardNow)
	if len(b.Vendors) != 2 {
		t.Fatalf("want two vendors; got %d", len(b.Vendors))
	}
	// Biggest outstanding first, a rep works down this list.
	if b.Vendors[0].VendorName != "Techo-Bloc" || b.Vendors[0].OutstandingCents != 4_000_00 {
		t.Errorf("vendor roll-up/order wrong: %+v", b.Vendors[0])
	}
	if b.Vendors[0].ClaimCount != 2 || b.Vendors[0].OldestDays != 95 {
		t.Errorf("vendor claim count / oldest wrong: %+v", b.Vendors[0])
	}
	// Within a vendor the worklist is oldest-first.
	if b.Vendors[0].Claims[0].ClaimNo != "WTY-2" {
		t.Errorf("a vendor's claims must be oldest-first; got %s", b.Vendors[0].Claims[0].ClaimNo)
	}
}

func TestRecoveryBoard_UnattributedVendorIsGroupedNeverDropped(t *testing.T) {
	// A claim settled before triage identified the vendor still owes us money. Dropping it would understate the
	// total the business is chasing.
	rows := []RecoveryRow{{ClaimID: "c1", ClaimNo: "WTY-9", AccruedCents: 750_00, OutstandingCents: 750_00, AccruedAt: daysAgo(2)}}
	b := BuildRecoveryBoard(rows, boardNow)
	if b.TotalOutstandingCents != 750_00 || len(b.Vendors) != 1 {
		t.Fatalf("unattributed money must still appear; got %+v", b)
	}
	if !strings.Contains(b.Vendors[0].VendorName, "not yet identified") {
		t.Errorf("the unattributed group must say so; got %q", b.Vendors[0].VendorName)
	}
}

// - the reconciliation rule -

// accruedClaim settles a claim to the vendor-recovery path so it has something to chase.
func accruedClaim(t *testing.T, svc *Service, ctx context.Context, amount int64) string {
	t.Helper()
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: amount}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	return id
}

func TestRecoveryAction_ReceivedReconcilesAgainstTheClaim(t *testing.T) {
	svc, fs, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := accruedClaim(t, svc, ctx, 4_000_00)

	// The chase: contact, promise, then a partial receipt.
	mustAction(t, svc, ctx, id, RecoveryActionInput{Action: ActionContactMade, Note: "called Sara at Techo"})
	mustAction(t, svc, ctx, id, RecoveryActionInput{Action: ActionPromised, AmountCents: 4_000_00, PromisedDate: "2026-08-01"})
	mustAction(t, svc, ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 1_500_00, Reference: "CN-8812"})

	board, err := svc.RecoveryBoardView(ctx, testBranch, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if board.TotalAccruedCents != 4_000_00 || board.TotalReceivedCents != 1_500_00 || board.TotalOutstandingCents != 2_500_00 {
		t.Fatalf("outstanding must be accrued − received; got accrued=%d received=%d outstanding=%d",
			board.TotalAccruedCents, board.TotalReceivedCents, board.TotalOutstandingCents)
	}
	if board.TotalPromisedCents != 4_000_00 {
		t.Errorf("a promise is visible but is not money received; got promised=%d", board.TotalPromisedCents)
	}
	// The chase is mirrored onto the claim timeline, the one place a person looks for "what happened here".
	mirrored := map[string]bool{}
	for _, e := range fs.events {
		if e.ClaimID == id && e.EventType == "NOTE" && e.Detail["recovery_action"] != nil {
			mirrored[e.Detail["recovery_action"].(string)] = true
		}
	}
	for _, want := range []string{ActionContactMade, ActionPromised, ActionReceived} {
		if !mirrored[want] {
			t.Errorf("chase action %s must appear on the claim timeline; got %v", want, mirrored)
		}
	}
}

func TestRecoveryAction_OverReceiptIsRefusedWithTheNumbers(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := accruedClaim(t, svc, ctx, 1_000_00)
	mustAction(t, svc, ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 600_00})

	// 600 already in; 500 more would exceed the 1000 accrual. An over-receipt is either a keying error or a
	// credit that belongs to another claim, and accepting it produces a NEGATIVE outstanding that nets against
	// another vendor's real balance in the roll-up.
	_, err := svc.LogRecoveryAction(ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 500_00})
	if err == nil {
		t.Fatal("an over-receipt must be refused")
	}
	if st := statusOf(err); st != 422 {
		t.Errorf("want 422; got %d (%v)", st, err)
	}
	// The person keying it is holding a credit note and needs to know whether they are on the wrong claim.
	for _, want := range []string{"100000", "60000", "40000"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must show the numbers (accrued/received/outstanding); missing %s in %q", want, err.Error())
		}
	}
	// Exact-to-the-cent still works.
	mustAction(t, svc, ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 400_00})
	board, _ := svc.RecoveryBoardView(ctx, testBranch, "", false)
	if board.TotalOutstandingCents != 0 {
		t.Errorf("a fully-received claim must reconcile to zero; got %d", board.TotalOutstandingCents)
	}
}

func TestRecoveryAction_NonMoneyActionCannotCarryAnAmount(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := accruedClaim(t, svc, ctx, 500_00)
	// A CONTACT_MADE row with an amount would silently do nothing to the balance and look like a lost receipt.
	if _, err := svc.LogRecoveryAction(ctx, id, RecoveryActionInput{Action: ActionContactMade, AmountCents: 100_00}); err == nil {
		t.Fatal("a non-money action carrying an amount must be refused")
	}
	if _, err := svc.LogRecoveryAction(ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 0}); err == nil {
		t.Fatal("a RECEIVED action with no amount must be refused")
	}
}

func TestRecoveryAction_ClaimWithNoAccrualCannotBeChased(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeWriteOff, RealizedCents: 300_00}); err != nil {
		t.Fatal(err)
	}
	// A write-off recovers nothing, so there is nothing to chase and no balance to reconcile against.
	_, err := svc.LogRecoveryAction(ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 100})
	if err == nil {
		t.Fatal("a claim with no recovery accrual must not accept a receipt")
	}
	if !strings.Contains(err.Error(), "no recovery accrual") {
		t.Errorf("the refusal must say why; got %q", err.Error())
	}
}

func TestRecoveryBoard_IsBranchScoped(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	accruedClaim(t, svc, ctx, 2_000_00)

	// A staffer scoped to another branch sees none of it, even asking for "everything".
	outsider := ctxWith(staffAt("u2", otherBranch))
	board, err := svc.RecoveryBoardView(outsider, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if board.TotalOutstandingCents != 0 || len(board.Vendors) != 0 {
		t.Fatalf("outstanding vendor money must obey the branch fence; got %+v", board)
	}
	// And an explicit request for a branch they cannot reach is refused outright.
	if _, err := svc.RecoveryBoardView(outsider, testBranch, "", true); err == nil {
		t.Fatal("an explicit cross-branch recovery read must be forbidden")
	}
}

func TestRecoveryBoard_OpenOnlyHidesReconciledClaims(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := accruedClaim(t, svc, ctx, 800_00)
	mustAction(t, svc, ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 800_00})

	open, _ := svc.RecoveryBoardView(ctx, testBranch, "", true)
	if len(open.Vendors) != 0 {
		t.Errorf("a reconciled claim belongs in history, not on the worklist; got %+v", open.Vendors)
	}
	all, _ := svc.RecoveryBoardView(ctx, testBranch, "", false)
	if all.ReconciledClaimCount != 1 || all.TotalReceivedCents != 800_00 {
		t.Errorf("the full view must still show the reconciled claim; got %+v", all)
	}
}

// mustAction logs a chase action or fails the test.
func mustAction(t *testing.T, svc *Service, ctx context.Context, claimID string, in RecoveryActionInput) RecoveryAction {
	t.Helper()
	a, err := svc.LogRecoveryAction(ctx, claimID, in)
	if err != nil {
		t.Fatalf("log %s: %v", in.Action, err)
	}
	return a
}

// statusOf extracts the HTTP status a clientError carries (0 for anything else).
func statusOf(err error) int {
	var ce clientError
	if errors.As(err, &ce) {
		return ce.status
	}
	return 0
}
