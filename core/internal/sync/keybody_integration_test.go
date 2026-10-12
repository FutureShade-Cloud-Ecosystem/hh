//go:build integration

// keybody_integration_test.go: the fourth guarantee against a real
// database. The ledger table is the engine's own (sync_op_hashes, 0387);
// the pilot scope is the tasks module's real Store, so the dedup the body
// check sits inside is the production one.
package sync_test

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// The Postgres ledger: first write wins, and a slot is private to its org,
// its tenant (staff and tenant callers never share one) and its scope.
func TestKeyLedgerPostgresSlots(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
	})
	led := sync.NewPgKeyLedger(pool)
	north := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "fxstaff-any"}
	south := sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: "fxstaff-any"}
	tenant := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: "7e000000-0000-4000-8000-000000000001", Tenant: true, Actor: "fxportal-alpha"}
	k := newKey(900)

	if _, ok, err := led.BodyHash(ctx, north, "pick_tasks", k); err != nil || ok {
		t.Fatalf("empty ledger answered ok=%v err=%v", ok, err)
	}
	if err := led.Remember(ctx, north, "pick_tasks", k, hashA); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if err := led.Remember(ctx, north, "pick_tasks", k, hashB); err != nil {
		t.Fatalf("second remember must be a quiet no-op: %v", err)
	}
	if h, ok, _ := led.BodyHash(ctx, north, "pick_tasks", k); !ok || h != hashA {
		t.Fatalf("first write must win: got %q ok=%v", h, ok)
	}
	for name, c := range map[string]struct {
		f     sync.ScopeFilter
		scope string
	}{
		"other org":   {south, "pick_tasks"},
		"tenant":      {tenant, "pick_tasks"},
		"other scope": {north, "shortages"},
	} {
		if _, ok, err := led.BodyHash(ctx, c.f, c.scope, k); err != nil || ok {
			t.Errorf("%s shares the slot: ok=%v err=%v", name, ok, err)
		}
	}
}

// End to end on the pilot: the real Store dedups, the ledger binds the
// body, and the reuse is refused, audited and never applied.
func TestKeyBodyPilotEndToEnd(t *testing.T) {
	f := newPilot(t)
	ctx := context.Background()
	f.engine.WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_type = 'sync.pick_tasks'`)
	})

	create := syncOp{client: newKey(901), kind: "create", payload: map[string]any{"order_id": testkit.OrderAlphaPickID}}
	first := pushOps(t, f, []syncOp{create})[0]
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("create: %+v", first)
	}
	id := rowIDFor(t, f)
	var stored int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM sync_op_hashes WHERE client_id = $1::uuid AND scope = 'pick_tasks'`, create.client).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("hash rows for the applied key: %d err %v", stored, err)
	}
	before := snap(t, f)

	replay := pushOps(t, f, []syncOp{create})[0]
	if replay.Status != "applied" || replay.Seq == nil || *replay.Seq != *first.Seq {
		t.Fatalf("same body replay: %+v, want applied at seq %d", replay, *first.Seq)
	}

	// The same key now carrying an update of that row: a different body.
	reused := pushOps(t, f, []syncOp{{client: create.client, kind: "update", payload: map[string]any{"id": id, "stage_location": "FX Bay"}}})[0]
	if reused.Status != "rejected" || reused.Error == nil || !strings.HasPrefix(*reused.Error, "key_reused") || reused.Seq != nil || reused.Conflict != nil {
		t.Fatalf("reuse: %+v", reused)
	}
	assertEq(t, "reuse", before, snap(t, f))

	var n int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE entity_type = 'sync.pick_tasks' AND action = 'SYNC_KEY_REUSED'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("SYNC_KEY_REUSED audit rows: %d err %v", n, err)
	}

	// A legacy key (applied, no stored hash) replays plainly whatever the body.
	if _, err := f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, create.client); err != nil {
		t.Fatalf("simulate a pre ledger key: %v", err)
	}
	legacy := pushOps(t, f, []syncOp{{client: create.client, kind: "update", payload: map[string]any{"id": id, "stage_location": "FX Bay"}}})[0]
	if legacy.Status != "applied" {
		t.Fatalf("legacy key: %+v, want a plain applied replay", legacy)
	}
	assertEq(t, "legacy replay", before, snap(t, f))
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM sync_op_hashes WHERE client_id = $1::uuid`, create.client).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a legacy replay stored a hash: %d err %v", n, err)
	}
}

// rowIDFor reads the one pick task the pilot's scope holds for the filter.
func rowIDFor(t *testing.T, f *pilotFixture) string {
	t.Helper()
	resp, err := f.engine.Pull(context.Background(), f.info, f.filter, 0, 50)
	if err != nil || len(resp.Items) != 1 {
		t.Fatalf("pull: %d items err %v", len(resp.Items), err)
	}
	return resp.Items[0].Id
}
