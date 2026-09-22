//go:build integration

// dib482_cadence_integration_test.go, DIB-482 / M3-G3 half A: 'Create link' starts the cadence.
//
// THE GAP. quote_followup_plans was empty on a database that had been used, and it was empty for a
// structural reason rather than a data one: the ONLY code path that ever called EnsurePlan was
// AutomationService.ShareBySMS. The staff panel's "Create link" button, the ordinary way a quote is
// sent, the one that mints a token and moves DRAFT -> SENT, started nothing at all. The Follow-ups
// panel at the foot of that same screen promised otherwise.
package quote

import (
	"context"
	"testing"
	"time"
)

func cleanupPlans(t *testing.T, quoteID string) {
	t.Helper()
	pool := intPool(t)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM quote_followup_events WHERE quote_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_followup_plans WHERE quote_id = $1`, quoteID)
	})
}

func planCount(t *testing.T, quoteID string) int {
	t.Helper()
	var n int
	if err := intPool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM quote_followup_plans WHERE quote_id = $1`, quoteID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestIntegration_DIB482_MintingAShareLinkStartsTheCadence is the ticket, stated as a fact about the
// database: before the mint the quote has no plan, after it the quote has one.
//
// This is the mutation anchor. Remove the startCadenceOnShare call from MintShareLink and this test
// dies on the second assertion with "still 0 plans".
func TestIntegration_DIB482_MintingAShareLinkStartsTheCadence(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)

	if n := planCount(t, quoteID); n != 0 {
		t.Fatalf("the quote already has %d plan(s) before anything was shared, fixture is not clean", n)
	}

	svc := shareSvc(t, pool, now)
	if _, err := svc.MintShareLink(qCtxAs(intStaff(intTrenton)), quoteID, 0); err != nil {
		t.Fatalf("mint: %v", err)
	}

	if n := planCount(t, quoteID); n != 1 {
		t.Fatalf("after 'Create link' the quote has %d follow-up plan(s), want 1, the ordinary way "+
			"of sending a quote still starts no cadence", n)
	}
}

// TestIntegration_DIB482_TheCadenceStartsOnTheHouseTERMS pins WHAT was started, not merely that
// something was. A plan on the wrong channel or the wrong offsets is a plan that will text a customer
// who was emailed, or chase them on a schedule nobody agreed.
func TestIntegration_DIB482_TheCadenceStartsOnTheHouseTerms(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)

	svc := shareSvc(t, pool, now)
	if _, err := svc.MintShareLink(qCtxAs(intStaff(intTrenton)), quoteID, 0); err != nil {
		t.Fatalf("mint: %v", err)
	}

	plan, err := NewFollowUpRepository(pool).Plan(context.Background(), quoteID)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if plan.Channel != ChannelBoth {
		t.Errorf("channel = %q, want %q, the link path must agree with the SMS path", plan.Channel, ChannelBoth)
	}
	if len(plan.OffsetsDays) != len(DefaultOffsets) {
		t.Fatalf("offsets = %v, want %v", plan.OffsetsDays, DefaultOffsets)
	}
	for i, d := range DefaultOffsets {
		if plan.OffsetsDays[i] != d {
			t.Fatalf("offsets = %v, want %v", plan.OffsetsDays, DefaultOffsets)
		}
	}
	if !plan.Enabled {
		t.Error("the plan starts disabled, a cadence nobody asked to pause")
	}
	// The clock starts at the share, which is the moment the salesperson thinks in.
	if !plan.StartedAt.Equal(now) {
		t.Errorf("started_at = %s, want the share instant %s", plan.StartedAt, now)
	}
}

// TestIntegration_DIB482_ASecondShareDoesNotRestartTheClock is the property that makes it safe to
// call EnsurePlan on a path an operator can press twice. Re-sharing on day 6 must not reset the
// cadence to day 0: the day-3 message has already gone, and a restart is how a customer receives the
// same three messages twice.
func TestIntegration_DIB482_ASecondShareDoesNotRestartTheClock(t *testing.T) {
	pool := intPool(t)
	first := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)

	if _, err := shareSvc(t, pool, first).MintShareLink(qCtxAs(intStaff(intTrenton)), quoteID, 0); err != nil {
		t.Fatalf("first mint: %v", err)
	}
	// Six days later the salesperson mints a second link for the same quote.
	later := first.AddDate(0, 0, 6)
	if _, err := shareSvc(t, pool, later).MintShareLink(qCtxAs(intStaff(intTrenton)), quoteID, 0); err != nil {
		t.Fatalf("second mint: %v", err)
	}

	if n := planCount(t, quoteID); n != 1 {
		t.Fatalf("two shares produced %d plans, one quote gets one cadence", n)
	}
	plan, err := NewFollowUpRepository(pool).Plan(context.Background(), quoteID)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.StartedAt.Equal(first) {
		t.Fatalf("started_at moved to %s, the second share restarted the clock, and this customer "+
			"will now be chased twice on the same quote", plan.StartedAt)
	}
}

// TestIntegration_DIB482_ARefusedMintStartsNoCadence. The cadence is started AFTER the link exists,
// so a mint the guards refuse must leave no plan behind, a reminder about a document the customer
// never received is worse than no reminder.
func TestIntegration_DIB482_ARefusedMintStartsNoCadence(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	// ACCEPTED is not shareable, MintShareLink refuses it before any token is made.
	_, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupShares(t, pool, quoteID)
	cleanupPlans(t, quoteID)

	svc := shareSvc(t, pool, now)
	if _, err := svc.MintShareLink(qCtxAs(intStaff(intTrenton)), quoteID, 0); err == nil {
		t.Fatal("want the mint refused on an ACCEPTED quote")
	}
	if n := planCount(t, quoteID); n != 0 {
		t.Fatalf("a refused mint left %d follow-up plan(s) behind", n)
	}
}
