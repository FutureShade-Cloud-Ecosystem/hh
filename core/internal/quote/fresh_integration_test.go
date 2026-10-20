//go:build integration

// fresh_integration_test.go: the sync freshness probe (harness.Fresh) over the
// quote builder's terms write. A device that has already synced the quotes
// feed must see a terms change and the money refold behind it on the next pull
// from its own cursor. Each test drives the REAL repository write path
// (UpdateTermsGuarded) against the REAL quotes scope store.
package quote

import (
	"context"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// freshQuoteCase creates one quote the device has synced and returns the case.
func freshQuoteCase(t *testing.T) (*syncFixture, harness.FreshCase, string) {
	t.Helper()
	f := newSyncFixture(t)
	id := syncCreate(t, f, syncKit(f).Create(""))
	return f, harness.FreshCase{Engine: f.engine, Scope: f.info, Filter: f.filter}, id
}

// TestIntegration_Fresh_QuoteTermsRefoldReachesTheFeed: a delivery fee in the
// terms strip moves the quote's tax and total (builder_repository.go terms
// write and money refold).
func TestIntegration_Fresh_QuoteTermsRefoldReachesTheFeed(t *testing.T) {
	f, c, id := freshQuoteCase(t)
	c.Write = func(ctx context.Context) (string, error) {
		_, err := f.repo.UpdateTermsGuarded(ctx, id, TermsInput{Fulfillment: "PICKUP", DeliveryFeeCents: 10000},
			[]string{"DRAFT", "SENT"}, testkit.UserInsideSalesID, 1300)
		return id, err
	}
	c.Want = func(d map[string]any) error {
		// 10000 delivery + 1300 flat HST on it, no lines.
		if got, _ := d["total_cents"].(float64); got != 11300 {
			return fmt.Errorf("total_cents is %v, want 11300", d["total_cents"])
		}
		return nil
	}
	harness.Fresh(t, c)
}

// TestIntegration_Fresh_QuoteTermsOwnerChangeReachesTheNewOwnersFeed: a terms
// write that names another salesperson changes the quote's owner and moves no
// served money. The quote desk's read ladder is part of the pull, so the row
// must reach the NEW owner's device, which is the fence change case: a filter
// the quote did not belong to before the write.
func TestIntegration_Fresh_QuoteTermsOwnerChangeReachesTheNewOwnersFeed(t *testing.T) {
	f, c, id := freshQuoteCase(t)
	c.Filter.Actor = testkit.SubSalesManager
	c.Write = func(ctx context.Context) (string, error) {
		_, err := f.repo.UpdateTermsGuarded(ctx, id, TermsInput{Fulfillment: "PICKUP", SalespersonID: testkit.UserSalesMgrID},
			[]string{"DRAFT", "SENT"}, testkit.UserInsideSalesID, 1300)
		return id, err
	}
	c.Want = func(d map[string]any) error {
		if d["id"] != id {
			return fmt.Errorf("id is %v, want %s", d["id"], id)
		}
		return nil
	}
	harness.Fresh(t, c)
}
