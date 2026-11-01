//go:build integration

// integration_w6c_replay_test.go, row 0.40 (from PR 758's delta): a warranty settlement replayed
// with a DIFFERENT amount.
//
// The reader's worry: the credit key ("warranty-credit:<claimID>") is bound to the first memo, so a
// replay with another amount would draw billing's key conflict (409), and warranty would then record
// "billing refused the credit memo, settlement recorded, no credit issued" although the first memo
// exists. The finding, by design: that note can never be written for a replay, because a replay
// never reaches the seam. RecordSettlement refuses a second settlement of a claim with a 409 before
// any money path runs (the atomic ClaimSettlementWinner write is the guard), so the first posting row
// stays the only one, still POSTED, still naming the first memo. The seam's own refusal is pinned
// below so the two 409s are each proven where they live.
package warranty

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

func TestIntegrationW6C_SettlementReplayedWithAnotherAmountIs409AndKeepsTheFirstMemo(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	glSvc := gl.NewService(gl.NewRepository(pool))
	writer := billing.NewInvoiceWriter(pool).
		WithCreditMemoPoster(realCreditMemoPoster{svc: glSvc, rules: gl.NewRepository(pool)})
	svc := w5bSvc(pool, staffCaller(demoOwner), &now).WithCustomerCredit(realCreditIssuer{w: writer})

	cust := w6cCustomer(t, pool)
	id := w5bClaim(t, svc, pool, ctx, ClaimInput{
		BranchID: trentonBranch, CustomerID: cust, Description: "W6C replay amount",
	})

	first, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeCustomerAccommodation, RealizedCents: 400_00})
	if err != nil {
		t.Fatalf("first settle: %v", err)
	}
	if first.PathStatus != PathPosted || first.ExternalRef == "" {
		t.Fatalf("first settle: status %q ref %q, want POSTED with a memo number", first.PathStatus, first.ExternalRef)
	}

	_, err = svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeCustomerAccommodation, RealizedCents: 900_00})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != http.StatusConflict {
		t.Fatalf("a settlement replayed with another amount must be a 409, got %v", err)
	}

	// The first memo is still the only one, at the first amount.
	var memos int
	var sub int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(min(subtotal_cents), 0) FROM invoices WHERE idempotency_key = $1`,
		"warranty-credit:"+id).Scan(&memos, &sub); err != nil {
		t.Fatalf("count memos: %v", err)
	}
	if memos != 1 || sub != -400_00 {
		t.Fatalf("after the replay: %d memo(s) at subtotal %d, want 1 at -40000", memos, sub)
	}

	// And warranty never wrote a "no credit issued" posting: one posting row, still POSTED, still the first memo.
	posts, err := svc.SettlementPostings(ctx, id)
	if err != nil {
		t.Fatalf("postings: %v", err)
	}
	if len(posts) != 1 || posts[0].Status != PathPosted || posts[0].ExternalRef != first.ExternalRef {
		t.Fatalf("postings after the replay = %+v, want exactly the first POSTED row naming %s", posts, first.ExternalRef)
	}
	if posts[0].AmountCents != 400_00 {
		t.Fatalf("the posting amount moved to %d", posts[0].AmountCents)
	}

	// The seam's own guard, where the other 409 lives: the same key with a different amount is refused
	// by billing and the first memo stands.
	issuer := realCreditIssuer{w: writer}
	_, serr := issuer.IssueCustomerCredit(ctx, CustomerCreditRequest{
		CustomerID: cust, BranchID: trentonBranch, AmountCents: 900_00,
		Memo: "different amount", IdempotencyKey: fmt.Sprintf("warranty-credit:%s", id),
	})
	if serr == nil {
		t.Fatal("billing must refuse the same key with a different amount")
	}
}
