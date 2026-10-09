//go:build integration

// productload_lockorder_integration_test.go proves the product writer's
// lock order (HH-LOAD-1 LD-5): the products scope row first, the product
// row second, in the landed set check as in the write statement. A batch
// holds the scope row to its end, so a check that took the product row
// first could wait on a batch holding the scope row and wanting that same
// product, a Postgres deadlock between two batches that load the same
// names (seen on the pool 4 leg as one run in several hundred).
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5680/integration_gate?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run TestProductLoadLandedCheckTakesTheScopeRowBeforeTheProductRow
package product

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

func TestProductLoadLandedCheckTakesTheScopeRowBeforeTheProductRow(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-lo-seed", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-P1", "FXPR-9101", "Fixture Order One", nil)}})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	productID := r.Items[0].HHID

	// Another batch stands holding the scope row.
	holder, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("holder begin: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM scope_sequences WHERE scope = 'products' FOR UPDATE`); err != nil {
		t.Fatalf("holder scope lock: %v", err)
	}

	checker, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("checker begin: %v", err)
	}
	defer func() { _ = checker.Rollback(ctx) }()
	done := make(chan error, 1)
	go func() {
		_, err := landedUnitSetRefusal(ctx, checker, productID, fxProdOrgID, "EA", "EA", "PR-P1")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the landed set check finished (%v) while another batch held the scope row: it must wait there, before it takes the product row", err)
	case <-time.After(500 * time.Millisecond):
	}

	// While it waits it must hold no lock on the product row.
	probe, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("probe begin: %v", err)
	}
	defer func() { _ = probe.Rollback(ctx) }()
	var got string
	if err := probe.QueryRow(ctx, `SELECT id::text FROM products WHERE id = $1::uuid FOR NO KEY UPDATE NOWAIT`, productID).Scan(&got); err != nil {
		t.Fatalf("the waiting check already holds the product row: %v", err)
	}
	_ = probe.Rollback(ctx)

	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("holder commit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the check after the scope row freed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the check never finished after the scope row freed")
	}
}
