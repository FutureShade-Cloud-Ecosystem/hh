//go:build integration

// receivables_r3_integration_test.go: the round 3 legs of the open
// receivables door (HH-LOAD-1 LD-11, the review's findings), each
// written red first. Every fixture is invented.
package load_test

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// ld11RecordingPoster records every journal the repost job asks for.
type ld11RecordingPoster struct{ posted []billing.CreditMemoJournal }

func (p *ld11RecordingPoster) PostCreditMemoJournal(_ context.Context, in billing.CreditMemoJournal) (string, error) {
	p.posted = append(p.posted, in)
	return "journal-fixture", nil
}

// P1-1: the nightly credit memo repost job never journals a loaded
// credit memo, and still journals one HH raised itself.
func TestReceivablesNightlyRepostNeverSweepsALoadedCreditMemo(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-r3-repost",
		ld11Item("RP-1", "CUST-ONE", "BR-A", "RP-DOC-1", "2032-06-01", "2032-07-01", -4321, "", ""))); err != nil {
		t.Fatalf("load: %v", err)
	}
	poster := &ld11RecordingPoster{}
	res, err := billing.NewInvoiceWriter(e.pool).WithCreditMemoPoster(poster).RepostFailedCreditMemoJournals(ctx, 0)
	if err != nil {
		t.Fatalf("repost: %v", err)
	}
	for _, m := range poster.posted {
		if m.ARCents == 4321 {
			t.Fatalf("the nightly repost job asked the GL to post a journal for the loaded credit memo (result %+v)", res)
		}
	}
}

var _ = load.ReceivablesKind
