//go:build integration

// preview_integration_test.go, the staff preview of the customer's page writes nothing.
//
// The ruling: Preview never sends. A read of the customer's view by quote id must leave the quote's
// status, its share links, its change history and its follow up schedule exactly as they were, and
// must offer no way to accept.
package quote

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type previewFootprint struct {
	Status string
	Links  int
	Views  int
	Audit  int
	Plans  int
	Events int
}

func TestIntegration_CustomerViewPreview_WritesNothing(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM quote_followup_events WHERE quote_id = $1`, quoteID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM quote_followup_plans WHERE quote_id = $1`, quoteID)
	})
	svc := shareSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	footprint := func() previewFootprint {
		t.Helper()
		var f previewFootprint
		c := context.Background()
		if err := pool.QueryRow(c, `SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&f.Status); err != nil {
			t.Fatalf("read status: %v", err)
		}
		for sql, dst := range map[string]*int{
			`SELECT count(*) FROM quote_share_links WHERE quote_id=$1`:                                                                 &f.Links,
			`SELECT count(*) FROM quote_share_link_events WHERE share_link_id IN (SELECT id FROM quote_share_links WHERE quote_id=$1)`: &f.Views,
			`SELECT count(*) FROM quote_followup_plans WHERE quote_id=$1`:                                                              &f.Plans,
			`SELECT count(*) FROM quote_followup_events WHERE quote_id=$1`:                                                             &f.Events,
			`SELECT count(*) FROM audit_log WHERE entity_type='quote' AND entity_id=$1`:                                                &f.Audit,
		} {
			if err := pool.QueryRow(c, sql, quoteID).Scan(dst); err != nil {
				t.Fatalf("count %q: %v", sql, err)
			}
		}
		return f
	}
	hist, err := svc.QuoteEditHistory(ctx, quoteID)
	if err != nil {
		t.Fatalf("history before: %v", err)
	}

	before := footprint()
	view, err := svc.CustomerViewPreview(ctx, quoteID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	after := footprint()

	if !reflect.DeepEqual(before, after) {
		t.Errorf("a preview must write nothing.\n before: %+v\n after:  %+v", before, after)
	}
	if after.Status != "DRAFT" {
		t.Errorf("a preview must leave a DRAFT quote DRAFT; status is %q", after.Status)
	}
	histAfter, err := svc.QuoteEditHistory(ctx, quoteID)
	if err != nil {
		t.Fatalf("history after: %v", err)
	}
	if len(histAfter.Changes) != len(hist.Changes) {
		t.Errorf("a preview must add no change history: %d before, %d after", len(hist.Changes), len(histAfter.Changes))
	}
	if view.Acceptable {
		t.Error("a preview must never be acceptable")
	}
	if !view.Preview {
		t.Error("a preview must say it is a preview")
	}
	if view.QuoteNo == "" || view.CustomerName == "" {
		t.Errorf("the preview must carry the customer's page: %+v", view)
	}
}

func TestIntegration_CustomerViewPreview_UnknownAndForbidden(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	svc := shareSvc(t, pool, now)

	if _, err := svc.CustomerViewPreview(qCtxAs(intStaff(intTrenton)), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown quote must be ErrNotFound, got %v", err)
	}
	other := intStaff("00000000-0000-0000-0000-0000000000aa")
	if _, err := svc.CustomerViewPreview(qCtxAs(other), quoteID); err == nil {
		t.Error("a caller outside the quote's branch must be refused")
	}
}

// The preview and the link holder's read come from one code path: for a sent quote they differ only in
// the two preview fields (and the preview never offers the accept).
func TestIntegration_CustomerViewPreview_MatchesThePublicRead(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	cleanupShares(t, pool, quoteID)
	svc := shareSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	minted, err := svc.MintShareLink(ctx, quoteID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	pub, err := svc.PublicQuoteByToken(context.Background(), minted.Token)
	if err != nil {
		t.Fatalf("public read: %v", err)
	}
	prev, err := svc.CustomerViewPreview(ctx, quoteID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !pub.Acceptable {
		t.Fatal("fixture: a live SENT link should be acceptable")
	}
	if prev.Acceptable || !prev.Preview {
		t.Errorf("preview must be unacceptable and flagged: %+v", prev)
	}
	prev.Acceptable, prev.Preview = pub.Acceptable, pub.Preview
	if !reflect.DeepEqual(prev, pub) {
		t.Errorf("preview drifted from the public read:\n preview: %+v\n public:  %+v", prev, pub)
	}
}
