//go:build integration

package billing

// credit_memo_body_integration_test.go, the credit memo's one body check (wave 3 P036 fix round).
//
// A key belongs to the credit that first used it. A second request on that key with a different
// branch or a different reversed invoice is a key conflict, whether it arrives after the winner
// (the fast path) or races it (the fast path or the unique violation fallback).
//
// Run: DATABASE_URL=… go test -tags integration ./internal/billing -run TestIntegration_CreditMemoBody -count=1

import (
	"context"
	"errors"
	"testing"
)

type memoBodyAsk struct {
	branch  string
	reverse string
}

// memoBodyRace sends two asks on one key at once and wants one winner and one key conflict.
func memoBodyRace(t *testing.T, w *InvoiceWriter, custID, keyBase string, rounds int, a, b memoBodyAsk) {
	t.Helper()
	ctx := context.Background()
	for round := 0; round < rounds; round++ {
		key := keyBase + "-" + string(rune('a'+round))
		asks := []memoBodyAsk{a, b}
		errs := make([]error, 2)
		start := make(chan struct{})
		done := make(chan struct{}, 2)
		for i := range asks {
			go func(i int) {
				<-start
				_, errs[i] = w.IssueCreditMemo(ctx, CreditMemoInput{
					CustomerID: custID, BranchID: asks[i].branch, ReversesInvoiceID: asks[i].reverse,
					AmountCents: 100, Memo: "Body race", IdempotencyKey: key, SourceKind: SourceOrder,
				})
				done <- struct{}{}
			}(i)
		}
		close(start)
		<-done
		<-done
		wins, conflicts := 0, 0
		for _, err := range errs {
			var c *creditMemoKeyConflict
			switch {
			case err == nil:
				wins++
			case errors.As(err, &c):
				conflicts++
			default:
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if wins != 1 || conflicts != 1 {
			t.Fatalf("round %d: %d answered and %d conflicted, want one winner and one 409", round, wins, conflicts)
		}
	}
}

func memoBodyRig(t *testing.T) (*InvoiceWriter, string, string, string) {
	t.Helper()
	pool := intPool(t)
	tag := ns(t)
	custID := seedCustomer(t, pool, tag)
	invA := seedCappedInvoice(t, pool, custID, "#INV-"+tag+"-BA")
	invB := seedCappedInvoice(t, pool, custID, "#INV-"+tag+"-BB")
	return NewInvoiceWriter(pool), custID, invA, invB
}

func TestIntegration_CreditMemoBody_ARacingLoserOnADifferentBranchIsAConflict(t *testing.T) {
	w, custID, inv, _ := memoBodyRig(t)
	memoBodyRace(t, w, custID, "mbb-"+ns(t), 8,
		memoBodyAsk{trentonBranch, inv}, memoBodyAsk{kingstonBranch, inv})
}

func TestIntegration_CreditMemoBody_ARacingLoserOnADifferentInvoiceIsAConflict(t *testing.T) {
	w, custID, invA, invB := memoBodyRig(t)
	memoBodyRace(t, w, custID, "mbi-"+ns(t), 8,
		memoBodyAsk{trentonBranch, invA}, memoBodyAsk{trentonBranch, invB})
}

// After the winner has committed, the same two bodies meet the fast path alone.
func TestIntegration_CreditMemoBody_ALateAskWithADifferentBranchOrInvoiceIsAConflict(t *testing.T) {
	w, custID, invA, invB := memoBodyRig(t)
	ctx := context.Background()
	key := "mbl-" + ns(t)
	in := CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: invA,
		AmountCents: 5_000, Memo: "Late", IdempotencyKey: key, SourceKind: SourceOrder}
	first, err := w.IssueCreditMemo(ctx, in)
	if err != nil {
		t.Fatalf("first use of the key: %v", err)
	}
	again, err := w.IssueCreditMemo(ctx, in)
	if err != nil || !again.Reused || again.InvoiceID != first.InvoiceID {
		t.Fatalf("the same body twice: %+v, %v, want the first memo reused", again, err)
	}
	for name, mut := range map[string]func(*CreditMemoInput){
		"a different branch":  func(i *CreditMemoInput) { i.BranchID = kingstonBranch },
		"a different invoice": func(i *CreditMemoInput) { i.ReversesInvoiceID = invB },
	} {
		other := in
		mut(&other)
		var c *creditMemoKeyConflict
		if _, err := w.IssueCreditMemo(ctx, other); !errors.As(err, &c) {
			t.Fatalf("%s answered %v, want the key conflict", name, err)
		}
	}
}
