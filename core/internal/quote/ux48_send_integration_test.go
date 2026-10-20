//go:build integration

// ux48_send_integration_test.go, UX-48: a quote is Sent only when it reached the customer, or the
// dealer says they sent it themselves. Minting a link (Copy link, Preview, Print) is not a send.
package quote

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIntegration_UX48_MintingALinkLeavesTheQuoteADraftWithNoFollowUps(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)

	svc := shareSvc(t, pool, now)
	minted, err := svc.MintShareLink(qCtxAs(intStaff(intTrenton)), quoteID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if minted.Token == "" {
		t.Fatal("the mint returned no link")
	}
	if got := quoteStatus(t, pool, quoteID); got != "DRAFT" {
		t.Fatalf("after a mint the quote reads %s, want DRAFT: a link nobody was sent is not a send", got)
	}
	if n := planCount(t, quoteID); n != 0 {
		t.Fatalf("after a mint the quote has %d follow-up plan(s), want 0: reminders about a quote the customer never saw", n)
	}
}

func TestIntegration_UX48_ASmsSendTheCoreMadeReadsSentWithAPlan(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	cust, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)

	staff := shareSvc(t, pool, now)
	q := &fakeQueue{consent: map[string]bool{cust: true}}
	auto := NewAutomationService(NewFollowUpRepository(pool)).WithStaff(staff).WithQueue(q).
		WithBaseURL("http://localhost:5173").WithClock(func() time.Time { return now })

	if _, err := auto.ShareBySMS(qCtxAs(intStaff(intTrenton)), quoteID, 0); err != nil {
		t.Fatalf("sms share: %v", err)
	}
	if len(q.shares) != 1 {
		t.Fatalf("the text queue holds %d share notice(s), want 1", len(q.shares))
	}
	if got := quoteStatus(t, pool, quoteID); got != "SENT" {
		t.Fatalf("after a text the core queued the quote reads %s, want SENT", got)
	}
	if n := planCount(t, quoteID); n != 1 {
		t.Fatalf("after a text the quote has %d follow-up plan(s), want 1", n)
	}
}

func TestIntegration_UX48_ATextTheCustomerRefusesLeavesTheQuoteADraft(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	cust, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)

	staff := shareSvc(t, pool, now)
	q := &fakeQueue{consent: map[string]bool{cust: false}}
	auto := NewAutomationService(NewFollowUpRepository(pool)).WithStaff(staff).WithQueue(q).
		WithBaseURL("http://localhost:5173").WithClock(func() time.Time { return now })

	if _, err := auto.ShareBySMS(qCtxAs(intStaff(intTrenton)), quoteID, 0); err == nil {
		t.Fatal("a text to a customer who has not agreed was accepted")
	}
	if got := quoteStatus(t, pool, quoteID); got != "DRAFT" {
		t.Fatalf("the refused text left the quote %s, want DRAFT", got)
	}
	if n := planCount(t, quoteID); n != 0 {
		t.Fatalf("the refused text started %d follow-up plan(s), want 0", n)
	}
}

func TestIntegration_UX48_ISentItMyselfReadsSentAsTheDealersWord(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)

	svc := shareSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	if _, err := svc.MintShareLink(ctx, quoteID, 0); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if got := quoteStatus(t, pool, quoteID); got != "DRAFT" {
		t.Fatalf("after the copy the quote reads %s, want DRAFT", got)
	}
	if err := svc.MarkSentByDealer(ctx, quoteID); err != nil {
		t.Fatalf("I sent it myself: %v", err)
	}
	if got := quoteStatus(t, pool, quoteID); got != "SENT" {
		t.Fatalf("after I sent it myself the quote reads %s, want SENT", got)
	}
	if n := planCount(t, quoteID); n != 1 {
		t.Fatalf("after I sent it myself the quote has %d follow-up plan(s), want 1", n)
	}
	hist, err := svc.QuoteEditHistory(ctx, quoteID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	found := false
	for _, c := range hist.Changes {
		if c.Field == "sent_how" && c.After == "DEALER_WORD" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the history does not record that the dealer said they sent it: %+v", hist.Changes)
	}
	// Pressing it twice changes nothing and starts nothing more.
	if err := svc.MarkSentByDealer(ctx, quoteID); err != nil {
		t.Fatalf("second press: %v", err)
	}
	if n := planCount(t, quoteID); n != 1 {
		t.Fatalf("a second press left %d plan(s), want 1", n)
	}
}

func TestIntegration_UX48_AnEmptyQuoteCannotHaveALinkOrBeMarkedSent(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)
	if _, err := pool.Exec(context.Background(), `DELETE FROM quote_lines WHERE quote_id = $1`, quoteID); err != nil {
		t.Fatal(err)
	}
	svc := shareSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	if _, err := svc.MintShareLink(ctx, quoteID, 0); err == nil {
		t.Fatal("a link was minted for a quote with no lines")
	}
	if err := svc.MarkSentByDealer(ctx, quoteID); err == nil {
		t.Fatal("a quote with no lines was marked sent")
	}
}

// mintAndSend is what a test that needs a quote the customer can act on uses: a link, then the dealer's
// word that it went out (a mint alone no longer sends, UX-48). A quote past SENT keeps its status.
func mintAndSend(svc *StaffService, ctx context.Context, quoteID string, ttl time.Duration) (MintedShareLink, error) {
	m, err := svc.MintShareLink(ctx, quoteID, ttl)
	if err != nil {
		return m, err
	}
	var ne *NotEditableError
	if err := svc.MarkSentByDealer(ctx, quoteID); err != nil && !errors.As(err, &ne) {
		return m, err
	}
	return m, nil
}

// A text to a quote that is not a draft (the dealer's multi accept switch lets an Accepted or Converted quote be shared
// again) queues once, answers success and leaves the status alone; a retry does not queue a second text.
func TestIntegration_UX48_ATextToANonDraftQuoteQueuesOnceAndLeavesTheStatus(t *testing.T) {
	for _, status := range []string{"ACCEPTED", "CONVERTED", "SENT"} {
		t.Run(status, func(t *testing.T) {
			pool := intPool(t)
			setMultiAccept(t, pool, true)
			now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			cust, quoteID, _, _ := seedQuote(t, pool, status, nil)
			cleanupShares(t, pool, quoteID)
			cleanupPlans(t, quoteID)

			staff := shareSvc(t, pool, now)
			q := &fakeQueue{consent: map[string]bool{cust: true}}
			auto := NewAutomationService(NewFollowUpRepository(pool)).WithStaff(staff).WithQueue(q).
				WithBaseURL("http://localhost:5173").WithClock(func() time.Time { return now })
			ctx := qCtxAs(intStaff(intTrenton))

			if _, err := auto.ShareBySMS(ctx, quoteID, 0); err != nil {
				t.Fatalf("a text to a %s quote answered failure after queueing it: %v", status, err)
			}
			if len(q.shares) != 1 {
				t.Fatalf("the text queue holds %d share notice(s), want 1", len(q.shares))
			}
			if got := quoteStatus(t, pool, quoteID); got != status {
				t.Fatalf("the text moved the quote from %s to %s", status, got)
			}
		})
	}
}

func TestIntegration_UX48_ARetryOfATextToANonDraftQuoteIsRefusedBeforeItQueuesNothingMore(t *testing.T) {
	pool := intPool(t)
	setMultiAccept(t, pool, true)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)
	staff := shareSvc(t, pool, now)
	q := &fakeQueue{consent: map[string]bool{cust: true}}
	auto := NewAutomationService(NewFollowUpRepository(pool)).WithStaff(staff).WithQueue(q).
		WithBaseURL("http://localhost:5173").WithClock(func() time.Time { return now })
	ctx := qCtxAs(intStaff(intTrenton))
	if _, err := auto.ShareBySMS(ctx, quoteID, 0); err != nil {
		t.Fatalf("first text: %v", err)
	}
	// The dealer switches multi accept off; the quote can no longer be shared, and the second press queues nothing.
	setMultiAccept(t, pool, false)
	if _, err := auto.ShareBySMS(ctx, quoteID, 0); err == nil {
		t.Fatal("with multi accept off a text to an Accepted quote was accepted")
	}
	if len(q.shares) != 1 {
		t.Fatalf("after the retry the text queue holds %d notice(s), want 1", len(q.shares))
	}
}
