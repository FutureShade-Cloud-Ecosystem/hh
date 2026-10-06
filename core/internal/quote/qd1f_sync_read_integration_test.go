//go:build integration

// qd1f_sync_read_integration_test.go is the quotes sync door's read rung at
// the store seam (P2-C, round 4): Load and FindApplied feed a conflict
// envelope and a replayed answer, so the projection of a quote the caller
// could not list through REST must not leave them, while the id and seq the
// engine's stale check needs still do. The by-request legs sit in cmd/core
// (qd1f_quote_sync_door_test.go); this leg injects configurations.
package quote

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestQD1F_SyncLoadAndFindAppliedCarryNoProjectionOfAnUnreadableQuote(t *testing.T) {
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
	read := func(name, sub string, wantData bool) {
		t.Helper()
		rs, found, err := f.store.Load(ctx, as(sub), quoteID)
		if err != nil || !found || rs.Seq != 1 {
			t.Fatalf("%s: load answered %+v found %v err %v, want the row's id and seq", name, rs, found, err)
		}
		if (rs.Data != nil) != wantData {
			t.Errorf("%s: load projection present %v, want %v", name, rs.Data != nil, wantData)
		}
		rs, found, err = f.store.FindApplied(ctx, as(sub), key)
		if err != nil || !found {
			t.Fatalf("%s: find applied answered %+v found %v err %v", name, rs, found, err)
		}
		if (rs.Data != nil) != wantData {
			t.Errorf("%s: find applied projection present %v, want %v", name, rs.Data != nil, wantData)
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
