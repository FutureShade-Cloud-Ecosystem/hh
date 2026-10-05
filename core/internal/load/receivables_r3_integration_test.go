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

// P1-2: a replay is graded against what that batch recorded, not
// against the registry's current stamp.
func TestReceivablesReplayIsGradedAgainstWhatThatBatchRecorded(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	a := ld11Item("RV-A", "CUST-ONE", "BR-A", "RV-DOC-A", "2032-06-01", "2032-07-01", 1000, "", "")
	b := ld11Item("RV-B", "CUST-ONE", "BR-A", "RV-DOC-B", "2032-06-02", "2032-07-02", 2000, "", "")
	first, err := e.door.UpsertReceivables(ctx, ld11Batch("rv-r1", a))
	if err != nil {
		t.Fatalf("rv-r1: %v", err)
	}
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("rv-r2", b)); err != nil {
		t.Fatalf("rv-r2: %v", err)
	}
	// Shape one: the replay carries an item another batch recorded,
	// with its amount changed.
	bChanged := ld11Item("RV-B", "CUST-ONE", "BR-A", "RV-DOC-B", "2032-06-02", "2032-07-02", 2500, "", "")
	_, err = e.door.UpsertReceivables(ctx, ld11Batch("rv-r1", a, bChanged))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "RV-B" {
		t.Fatalf("shape one answered %+v, want %s naming RV-B", ref.Refusing, load.ReasonReplayChanged)
	}
	// Shape two: another batch restamps A unchanged, then the replay of
	// rv-r1 carries A with a changed amount.
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("rv-r3", a)); err != nil {
		t.Fatalf("rv-r3: %v", err)
	}
	aChanged := ld11Item("RV-A", "CUST-ONE", "BR-A", "RV-DOC-A", "2032-06-01", "2032-07-01", 1500, "", "")
	_, err = e.door.UpsertReceivables(ctx, ld11Batch("rv-r1", aChanged))
	ref = ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "RV-A" {
		t.Fatalf("shape two answered %+v, want %s naming RV-A", ref.Refusing, load.ReasonReplayChanged)
	}
	// The faithful replay still answers the first outcomes and writes nothing.
	rowsBefore := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID)
	again, err := e.door.UpsertReceivables(ctx, ld11Batch("rv-r1", a))
	if err != nil {
		t.Fatalf("faithful replay: %v", err)
	}
	if len(again.Items) != 1 || again.Items[0] != first.Items[0] {
		t.Fatalf("the faithful replay answered %+v, want the first outcomes %+v", again.Items, first.Items)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != rowsBefore {
		t.Fatalf("the faithful replay wrote rows: %d, want %d", n, rowsBefore)
	}
	if inv := e.invoices(t); inv["RV-DOC-A"].Balance != 1000 || inv["RV-DOC-B"].Balance != 2000 {
		t.Fatalf("the refused replays moved money: %+v", inv)
	}
}
