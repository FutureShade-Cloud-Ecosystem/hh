//go:build integration

// qd1f_sync_read_integration_test.go is the quotes sync door's read rung at
// the store seam (P2-C, round 4; UX-108): Load and FindApplied feed a
// conflict envelope and a replayed answer, so a quote the caller could not
// list through REST must not leave them at all. The door answers not found
// for such a row, the answer an absent row gives: a masked found would tell
// the engine that an unreadable row is visible. The by-request legs sit in
// cmd/core (qd1f_quote_sync_door_test.go); this leg injects configurations.
package quote

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestQD1F_SyncLoadAndFindAppliedAnswerNotFoundForAnUnreadableQuote(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	managerID, ok := linkedSyncUser(f.pool)(ctx, testkit.SubSalesManager)
	if !ok {
		t.Fatal("no linked user for the Sales manager")
	}
	const key = "3ef10000-0d1f-4000-8000-0000000000e1"
	var quoteID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, org_id, status, subtotal_cents, tax_cents,
		                    total_cents, salesperson_id, created_by, seq, idempotency_key)
		VALUES ('#Q-QD1F-' || nextval('quote_no_seq'), $1::uuid, $2::uuid, $3::uuid, 'SENT', 2000, 0, 2000,
		        $4::uuid, $4::uuid, 1, $5::uuid)
		RETURNING id::text`,
		testkit.TenantAlphaID, testkit.BranchNorthAID, testkit.OrgNorthID, managerID, key).Scan(&quoteID); err != nil {
		t.Fatalf("plant quote: %v", err)
	}
	as := func(sub string) sync.ScopeFilter { return sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: sub} }
	read := func(name, sub string, wantFound bool) {
		t.Helper()
		rs, found, err := f.store.Load(ctx, as(sub), quoteID)
		if err != nil {
			t.Fatalf("%s: load failed: %v", name, err)
		}
		if found != wantFound {
			t.Errorf("%s: load answers found %v, want %v (%+v)", name, found, wantFound, rs)
		}
		if wantFound {
			if rs.Data == nil {
				t.Errorf("%s: the reader's load carries no projection", name)
			}
		} else if rs.ID != "" || rs.Seq != 0 || rs.Data != nil || rs.Deleted {
			t.Errorf("%s: the unreadable load answer is not the absent row's answer: %+v", name, rs)
		}
		rs, found, err = f.store.FindApplied(ctx, as(sub), key)
		if err != nil {
			t.Fatalf("%s: find applied failed: %v", name, err)
		}
		if found != wantFound {
			t.Errorf("%s: find applied answers found %v, want %v (%+v)", name, found, wantFound, rs)
		}
		if wantFound {
			if rs.Seq != 1 {
				t.Errorf("%s: the reader's key answers with the row's seq: got %v want 1", name, rs.Seq)
			}
		} else if rs.ID != "" || rs.Seq != 0 || rs.Data != nil || rs.Deleted {
			t.Errorf("%s: the unreadable find applied answer is not the absent key's answer: %+v", name, rs)
		}
	}

	// The decided default: Manage reads the branch, Work reads its own.
	qd1cPublish(t, qd1Cfg)
	read("Sales manager (Manage) reads the branch", testkit.SubSalesManager, true)
	read("Inside Sales (Work) reads another seller's quote", testkit.SubInsideSales, false)
	read("the General Manager (Read) reads what it reaches", testkit.SubGeneralMgr, true)
	// The Inside Sales role with no Quote Desk level reads nothing.
	qd1cPublish(t, qd1cCfgWithCell(t, "inside_sales", roleconfig.LevelNone))
	read("Inside Sales with no level", testkit.SubInsideSales, false)
	// Nothing published: no level for anyone, the door fails closed.
	qd1cPublish(t, nil)
	read("nothing published, the Sales manager", testkit.SubSalesManager, false)
}
