//go:build integration

// keybody_d30_integration_test.go: the fourth guarantee on a scope whose
// owning store answers FindApplied from the row's single D30 idempotency
// column (customer_quotes), which holds only the LATEST key. The tasks pilot
// keeps a full applied-key history and hides the defect these legs pin.
package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func cleanLedger(t *testing.T, f *the169Fixture) {
	t.Helper()
	cleanOwnLedgerRows(t, f.pool)
}

// cleanOwnLedgerRows removes the North and South ledger rows the leg writes,
// and only those: rows created from the database's clock at registration on,
// which it returns for the leg's other cleanups.
// Rows other packages' legs left in the shared gate database are not this
// leg's to delete (the package guard counts them before and after).
func cleanOwnLedgerRows(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var since time.Time
	if err := pool.QueryRow(context.Background(), `SELECT clock_timestamp()`).Scan(&since); err != nil {
		t.Fatalf("read the database clock: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM sync_op_hashes WHERE org_id IN ($1::uuid, $2::uuid) AND created_at >= $3`,
			testkit.OrgNorthID, testkit.OrgSouthID, since); err != nil {
			t.Errorf("clean the leg's ledger rows: %v", err)
		}
	})
	return since
}

func d30Push(t *testing.T, f *the169Fixture, op apigen.SyncPushOperation) apigen.SyncPushResult {
	t.Helper()
	k := f.quotesKit()
	resp, err := f.engine.Push(context.Background(), k.Scope, k.Filter, []apigen.SyncPushOperation{op})
	if err != nil || len(resp.Results) != 1 {
		t.Fatalf("push: %+v err %v", resp, err)
	}
	return resp.Results[0]
}

func d30Markup(t *testing.T, f *the169Fixture, id string) float64 {
	t.Helper()
	m, _ := loadRow(t, f.quotesKit(), id).Data["markup_bps"].(float64)
	return m
}

func quoteCount(t *testing.T, f *the169Fixture) int {
	t.Helper()
	k := f.quotesKit()
	return len(pullOf(t, f.engine, "customer_quotes", k.Filter, 0, 500))
}

// A document moved on by two updates: the module's column holds the second
// key only. The first key, reused with a different body, must be refused;
// the same body must replay without applying again.
func TestKeyBodyD30ColumnScopeRefusesMovedOnKey(t *testing.T) {
	f := new169Fixture(t)
	cleanLedger(t, f)
	f.engine.WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	k := f.quotesKit()

	createOp := apigen.SyncPushOperation{ClientId: newKey(950), Op: "create", Payload: k.Create("")}
	created := d30Push(t, f, createOp)
	if created.Status != "applied" {
		t.Fatalf("create: %+v", created)
	}
	id := pullOf(t, f.engine, "customer_quotes", k.Filter, 0, 500)[0]
	up1 := apigen.SyncPushOperation{ClientId: newKey(951), Op: "update", Payload: map[string]any{"id": id, "markup_bps": 1100}}
	up2 := apigen.SyncPushOperation{ClientId: newKey(952), Op: "update", Payload: map[string]any{"id": id, "markup_bps": 1200}}
	for _, op := range []apigen.SyncPushOperation{up1, up2} {
		if r := d30Push(t, f, op); r.Status != "applied" {
			t.Fatalf("setup update: %+v", r)
		}
	}
	var held int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM sync_op_hashes WHERE client_id IN ($1::uuid, $2::uuid, $3::uuid)`,
		createOp.ClientId, up1.ClientId, up2.ClientId).Scan(&held); err != nil || held != 3 {
		t.Fatalf("ledger rows %d err %v, want 3", held, err)
	}
	row := loadRow(t, k, id)

	// Different body under the first update key.
	evil := up1
	evil.Payload = map[string]any{"id": id, "markup_bps": 9900}
	if r := d30Push(t, f, evil); r.Status != "rejected" || r.Error == nil || len(*r.Error) < 10 || (*r.Error)[:10] != "key_reused" {
		t.Fatalf("reused update key answered %+v, want key_reused", r)
	}
	if m := d30Markup(t, f, id); m != 1200 {
		t.Fatalf("a refused reuse moved the row: markup %v, want 1200", m)
	}

	// Different body under the create key makes no second document.
	other := createOp
	other.Payload = k.Create("")
	if r := d30Push(t, f, other); r.Status != "rejected" || r.Error == nil || (*r.Error)[:10] != "key_reused" {
		t.Fatalf("reused create key answered %+v, want key_reused", r)
	}
	if n := quoteCount(t, f); n != 1 {
		t.Fatalf("%d documents, want the one", n)
	}

	// The same bodies replay as applied at the row's current seq, no write.
	for name, op := range map[string]apigen.SyncPushOperation{"create": createOp, "update": up1} {
		r := d30Push(t, f, op)
		if r.Status != "applied" || r.Seq == nil || *r.Seq != row.Seq {
			t.Errorf("%s replay answered %+v, want applied at seq %d", name, r, row.Seq)
		}
	}
	after := loadRow(t, k, id)
	if after.Seq != row.Seq || d30Markup(t, f, id) != 1200 {
		t.Fatalf("a replay re-applied: before seq %d after %+v", row.Seq, after)
	}
}
