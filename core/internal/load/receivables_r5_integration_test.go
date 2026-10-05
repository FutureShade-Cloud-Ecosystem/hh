//go:build integration

// receivables_r5_integration_test.go: the round 5 legs of the open
// receivables door (HH-LOAD-1 LD-11, the second review's findings), each
// written red first. Every fixture is invented.
package load_test

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// P1: a replay whose item count differs from what the batch recorded is
// refused with the replay-changed answer before any outcome is built; a
// reordered replay of the same items is still faithful.
func TestReceivablesReplayWithADifferentItemCountRefuses(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	a := ld11Item("RC-A", "CUST-ONE", "BR-A", "RC-DOC-A", "2020-06-01", "2020-07-01", 1000, "", "")
	b := ld11Item("RC-B", "CUST-ONE", "BR-A", "RC-DOC-B", "2020-06-02", "2020-07-02", 2000, "", "")
	c := ld11Item("RC-C", "CUST-ONE", "BR-A", "RC-DOC-C", "2020-06-03", "2020-07-03", 3000, "", "")
	first, err := e.door.UpsertReceivables(ctx, ld11Batch("rc-1", a, b))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	rows := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID)
	audits := e.audit.len()

	// Subset: A alone of A and B.
	res, err := e.door.UpsertReceivables(ctx, ld11Batch("rc-1", a))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "RC-B" {
		t.Fatalf("the subset replay answered %+v, want %s naming the dropped item RC-B", ref.Refusing, load.ReasonReplayChanged)
	}
	if len(res.Items) != 0 {
		t.Fatalf("the subset replay built %d outcomes before refusing", len(res.Items))
	}
	// Superset: A, B and a third item the batch never recorded.
	_, err = e.door.UpsertReceivables(ctx, ld11Batch("rc-1", a, b, c))
	ref = ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "RC-C" {
		t.Fatalf("the superset replay answered %+v, want %s naming RC-C", ref.Refusing, load.ReasonReplayChanged)
	}
	// Reordered: still faithful, the first outcomes come back.
	again, err := e.door.UpsertReceivables(ctx, ld11Batch("rc-1", b, a))
	if err != nil {
		t.Fatalf("the reordered replay refused: %v", err)
	}
	if len(again.Items) != 2 {
		t.Fatalf("the reordered replay answered %d outcomes, want two", len(again.Items))
	}
	for _, o := range again.Items {
		if o.LegacyID == "" || o.Status == "" {
			t.Fatalf("an empty outcome came back: %+v", o)
		}
	}
	byName := map[string]load.Outcome{}
	for _, o := range first.Items {
		byName[o.LegacyID] = o
	}
	for _, o := range again.Items {
		if byName[o.LegacyID] != o {
			t.Fatalf("outcome %+v is not the first one %+v", o, byName[o.LegacyID])
		}
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != rows {
		t.Fatalf("the replays wrote rows: %d, want %d", n, rows)
	}
	if e.audit.len() != audits {
		t.Fatalf("the replays wrote audit rows")
	}
}

var _ = time.Now
