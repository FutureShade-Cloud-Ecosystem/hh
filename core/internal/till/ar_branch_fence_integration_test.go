//go:build integration

// ar_branch_fence_integration_test.go, the bounced payments queue and the credit hold list answer the
// caller's branches only. A branch scoped caller who lists them sees the bounces of its own branch;
// a global caller sees every branch. Marking a bounce is already fenced by branch (nsf_integration).
package till

import (
	"context"
	"strings"
	"testing"
)

func TestIntegration_BouncedQueueAndHoldsFollowTheCallersBranches(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchA, userID := seedBranchUser(t, pool)
	var branchB string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' AND id <> $1::uuid LIMIT 1`, branchA).Scan(&branchB); err != nil {
		t.Skipf("the seed has one branch only: %v", err)
	}
	svc := realSvc(pool)
	global := globalCtx(userID)
	parent := t

	var payA, payB, custB string
	t.Run("seedA", func(t *testing.T) {
		var cleanup func()
		payA, _, _, cleanup = seedNSFCase(t, pool, branchA, 11000)
		parent.Cleanup(cleanup)
	})
	t.Run("seedB", func(t *testing.T) {
		var cleanup func()
		payB, _, custB, cleanup = seedNSFCase(t, pool, branchB, 22000)
		parent.Cleanup(cleanup)
	})
	for _, id := range []string{payA, payB} {
		if _, err := svc.MarkNSF(global, NSFInput{PaymentID: id, Reason: "returned"}); err != nil {
			t.Fatalf("bounce %s: %v", id, err)
		}
	}

	scoped := managerCtx(userID, branchA)
	items, err := svc.ListBouncedPayments(scoped, "")
	if err != nil {
		t.Fatalf("list bounced: %v", err)
	}
	var sawA, sawB bool
	for _, b := range items {
		sawA = sawA || b.PaymentID == payA
		sawB = sawB || b.PaymentID == payB
	}
	if !sawA || sawB {
		t.Fatalf("a branch A caller must see branch A's bounce and not branch B's (A %v, B %v)", sawA, sawB)
	}
	// An exact ref search cannot reach across the wall either.
	var refB string
	_ = pool.QueryRow(ctx, `SELECT gateway_ref FROM payments WHERE id = $1::uuid`, payB).Scan(&refB)
	if items, err := svc.ListBouncedPayments(scoped, refB); err != nil || len(items) != 0 {
		t.Fatalf("a ref search for another branch's bounce must find nothing, got %d (%v)", len(items), err)
	}

	holds, err := svc.ListCreditHolds(scoped, "ACTIVE")
	if err != nil {
		t.Fatalf("list holds: %v", err)
	}
	for _, h := range holds {
		if h.CustomerID == custB {
			t.Fatalf("a hold placed by another branch's bounce must not list for a branch A caller")
		}
	}

	items, err = svc.ListBouncedPayments(global, "")
	if err != nil {
		t.Fatalf("list bounced global: %v", err)
	}
	sawA, sawB = false, false
	for _, b := range items {
		sawA = sawA || b.PaymentID == payA
		sawB = sawB || b.PaymentID == payB
	}
	if !sawA || !sawB {
		t.Fatalf("a global caller must see both branches' bounces (A %v, B %v)", sawA, sawB)
	}
	holds, err = svc.ListCreditHolds(global, "ACTIVE")
	if err != nil {
		t.Fatalf("list holds global: %v", err)
	}
	var sawHold bool
	for _, h := range holds {
		sawHold = sawHold || h.CustomerID == custB
	}
	if !sawHold {
		t.Fatalf("a global caller must see the hold branch B's bounce placed")
	}
}

// A record in another branch answers exactly as a record that does not exist: no 403 against 404 to tell
// them apart and no branch id in the body (the security review's P2-4).
func TestIntegration_AnotherBranchsPaymentRecordsAreNotFoundNotForbidden(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchA, userID := seedBranchUser(t, pool)
	var branchB string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' AND id <> $1::uuid LIMIT 1`, branchA).Scan(&branchB); err != nil {
		t.Skipf("the seed has one branch only: %v", err)
	}
	svc := realSvc(pool)
	scoped := managerCtx(userID, branchA)

	payB, invB, _, cleanup := seedNSFCase(t, pool, branchB, 22000)
	t.Cleanup(cleanup)
	missing := "00000000-0000-4000-8000-00000000dead"

	_, errMissingHist := svc.PaymentHistory(scoped, missing)
	_, errOtherHist := svc.PaymentHistory(scoped, payB)
	_, errMissingInv := svc.InvoicePayments(scoped, missing)
	_, errOtherInv := svc.InvoicePayments(scoped, invB)
	_, errMissingNSF := svc.MarkNSF(scoped, NSFInput{PaymentID: missing, Reason: "x"})
	_, errOtherNSF := svc.MarkNSF(scoped, NSFInput{PaymentID: payB, Reason: "x"})
	for name, pair := range map[string][2]error{
		"payment history":  {errMissingHist, errOtherHist},
		"invoice payments": {errMissingInv, errOtherInv},
		"mark payment NSF": {errMissingNSF, errOtherNSF},
	} {
		if pair[0] == nil || pair[1] == nil || pair[0].Error() != pair[1].Error() {
			t.Errorf("%s: a missing id answers %v, another branch's record answers %v, want the same answer", name, pair[0], pair[1])
		}
		if pair[1] != nil && strings.Contains(pair[1].Error(), branchB) {
			t.Errorf("%s: the refusal names the other branch's id: %v", name, pair[1])
		}
	}
	// The record is untouched by the refused bounce.
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1::uuid`, payB).Scan(&status)
	if status != "RECEIVED" {
		t.Errorf("the refused bounce changed the payment to %s", status)
	}
}
