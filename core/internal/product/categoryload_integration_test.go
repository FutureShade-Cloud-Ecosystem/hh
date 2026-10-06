//go:build integration

// categoryload_integration_test.go proves the load doors' category
// writer (HH-LOAD-1 LD-4) through the frame: the tree whose parent is
// named by legacy key, the stamps a loaded row carries (org, seq,
// idempotency key), the replay arms (unchanged draws nothing, a changed
// name updates and draws exactly one), a cycle refused with nothing of
// the write standing, and the attach to a row that already carries the
// code. The writer never opens a connection and never commits: the
// transaction it runs on is the frame's.
//
// Fixtures are invented: the fxcat codes, the org and actor ids. The
// fixture rows leave with the test.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5625/ld4doors?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run TestCategoryLoad
package product

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The category door's fixture constants, invented.
const (
	fxLoadOrgID   = "4442a800-0000-4000-8000-00000000d001"
	fxLoadActorID = "4442a800-0000-4000-8000-00000000d00a"
	fxLoadSource  = "testsuite"
)

// loadAuditSpy captures the audit rows the frame writes, the suite's
// stand in for the platform's audit path.
type loadAuditSpy struct {
	mu   sync.Mutex
	rows int
}

func (s *loadAuditSpy) RecordBatchTx(_ context.Context, _ pgx.Tx, _ load.BatchAuditRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows++
	return nil
}

// categoryLoadEnv is one test's fixtures: the pool, the door, the spy.
type categoryLoadEnv struct {
	pool  *pgxpool.Pool
	door  *load.Door
	spy   *loadAuditSpy
	store *load.Store
}

func categoryLoadSetup(t *testing.T) *categoryLoadEnv {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM load_key_registry WHERE org_id = $1::uuid`, []any{fxLoadOrgID}},
		{`DELETE FROM load_batch WHERE org_id = $1::uuid`, []any{fxLoadOrgID}},
		{`DELETE FROM product_categories WHERE code LIKE 'fxcat-%'`, nil},
		{`DELETE FROM scope_sequences WHERE scope = 'product_categories'`, nil},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("category fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid`, fxLoadOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid`, fxLoadOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM product_categories WHERE code LIKE 'fxcat-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_sequences WHERE scope = 'product_categories'`)
		pool.Close()
	})
	spy := &loadAuditSpy{}
	store := load.NewStore(pool)
	return &categoryLoadEnv{
		pool:  pool,
		door:  load.NewDoor(store, "category", CategoryLoadWriter{}, spy),
		spy:   spy,
		store: store,
	}
}

// catItem is one category batch item; the derived key is a canonical
// fixture uuid keyed off the legacy id.
func catItem(legacyID, code, name string, requires ...load.Ref) load.Item {
	suffix := map[string]string{
		"CAT-P": "000000000001",
		"CAT-C": "000000000002",
		"CAT-X": "000000000003",
		"CAT-Y": "000000000004",
	}[legacyID]
	return load.Item{
		LegacyID:   legacyID,
		DerivedKey: "d1000000-0000-4000-8000-" + suffix,
		Record:     map[string]any{"code": code, "name": name},
		Requires:   requires,
	}
}

// catRow reads one fixture category row's id, parent, org, seq and key.
func (e *categoryLoadEnv) catRow(t *testing.T, code string) (id, parent string, org string, seq int64, key string) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT id::text, coalesce(parent_id::text, ''), coalesce(org_id::text, ''),
		       coalesce(seq, 0), coalesce(idempotency_key::text, '')
		FROM product_categories WHERE code = $1`, code).
		Scan(&id, &parent, &org, &seq, &key)
	if err != nil {
		t.Fatalf("category row %s: %v", code, err)
	}
	return id, parent, org, seq, key
}

func outcome(t *testing.T, r load.BatchResult, legacyID string) load.Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("batch carries no outcome for %s: %+v", legacyID, r.Items)
	return load.Outcome{}
}

func TestCategoryLoadBuildsTheTreeByLegacyKey(t *testing.T) {
	e := categoryLoadSetup(t)
	ctx := context.Background()
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-1", Caller: fxLoadActorID,
		Items: []load.Item{
			catItem("CAT-P", "fxcat-p1", "Fixture Parent"),
			catItem("CAT-C", "fxcat-c1", "Fixture Child", load.Ref{Kind: "category", LegacyID: "CAT-P"}),
		}})
	if err != nil {
		t.Fatalf("category batch: %v", err)
	}
	if o := outcome(t, r, "CAT-P"); o.Status != load.OutcomeCreated {
		t.Fatalf("the parent answered %+v, want created", o)
	}
	if o := outcome(t, r, "CAT-C"); o.Status != load.OutcomeCreated {
		t.Fatalf("the child answered %+v, want created", o)
	}
	pid, _, org, seq, key := e.catRow(t, "fxcat-p1")
	_, parent, corg, cseq, ckey := e.catRow(t, "fxcat-c1")
	if parent != pid {
		t.Fatalf("the child's parent is %q, want the parent row %q named by legacy key", parent, pid)
	}
	if org != fxLoadOrgID || corg != fxLoadOrgID {
		t.Fatalf("loaded rows carry org %q and %q, want the machine principal's org", org, corg)
	}
	if seq != 1 || cseq != 2 {
		t.Fatalf("the draws stand at %d and %d, want one and two: the allocator drew once per write", seq, cseq)
	}
	if key == "" || ckey == "" {
		t.Fatalf("the rows carry no idempotency keys: %q, %q", key, ckey)
	}
}

func TestCategoryLoadReplayUnchangedDrawsNothingAndUpdateDrawsOne(t *testing.T) {
	e := categoryLoadSetup(t)
	ctx := context.Background()
	batch := func(batchID, childName string) load.Batch {
		return load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
			BatchID: batchID, Caller: fxLoadActorID,
			Items: []load.Item{
				catItem("CAT-P", "fxcat-p1", "Fixture Parent"),
				catItem("CAT-C", "fxcat-c1", childName, load.Ref{Kind: "category", LegacyID: "CAT-P"}),
			}}
	}
	if _, err := e.door.UpsertBatch(ctx, batch("b-cat-r1", "Fixture Child")); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	var seqBefore int64
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'product_categories'), 0)`).Scan(&seqBefore); err != nil {
		t.Fatalf("scope read: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, batch("b-cat-r2", "Fixture Child"))
	if err != nil {
		t.Fatalf("replay batch: %v", err)
	}
	if o := outcome(t, r, "CAT-C"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the identical replay answered %q, want unchanged", o.Status)
	}
	var seqAfter int64
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'product_categories'), 0)`).Scan(&seqAfter); err != nil {
		t.Fatalf("scope read: %v", err)
	}
	if seqAfter != seqBefore {
		t.Fatalf("the unchanged replay drew sequences: %d over %d", seqAfter, seqBefore)
	}
	r, err = e.door.UpsertBatch(ctx, batch("b-cat-r3", "Fixture Child Renamed"))
	if err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if o := outcome(t, r, "CAT-C"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed name answered %q, want updated", o.Status)
	}
	var drew int64
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'product_categories'), 0)`).Scan(&drew); err != nil {
		t.Fatalf("scope read: %v", err)
	}
	if drew != seqBefore+1 {
		t.Fatalf("the update drew %d over %d, want exactly one new draw", drew, seqBefore)
	}
	var name string
	if err := e.pool.QueryRow(ctx,
		`SELECT name FROM product_categories WHERE code = 'fxcat-c1'`).Scan(&name); err != nil {
		t.Fatalf("child read: %v", err)
	}
	if name != "Fixture Child Renamed" {
		t.Fatalf("the child's name is %q, want the update's", name)
	}
}

func TestCategoryLoadRefusesACycleAndNothingOfTheWriteStands(t *testing.T) {
	e := categoryLoadSetup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-cy1", Caller: fxLoadActorID,
		Items: []load.Item{
			catItem("CAT-P", "fxcat-p1", "Fixture Parent"),
			catItem("CAT-C", "fxcat-c1", "Fixture Child", load.Ref{Kind: "category", LegacyID: "CAT-P"}),
		}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	// The parent re names the child as its own parent: the tree would
	// cycle, and the item refuses whole.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-cy2", Caller: fxLoadActorID,
		Items: []load.Item{
			catItem("CAT-P", "fxcat-p1", "Fixture Parent", load.Ref{Kind: "category", LegacyID: "CAT-C"}),
		}})
	if err != nil {
		t.Fatalf("cycle batch: %v", err)
	}
	o := outcome(t, r, "CAT-P")
	if o.Status != load.OutcomeRefused || o.Reason != "category_cycle" {
		t.Fatalf("the cycle answered %+v, want refused as category_cycle", o)
	}
	// Nothing of the refused write stands: the parent still has no parent.
	_, parent, _, _, _ := e.catRow(t, "fxcat-p1")
	if parent != "" {
		t.Fatalf("the refused cycle left the parent pointing at %q; the write must roll back whole", parent)
	}
}

func TestCategoryLoadAttachesARowThatAlreadyCarriesTheCode(t *testing.T) {
	e := categoryLoadSetup(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO product_categories (code, name) VALUES ('fxcat-p2', 'Planted By Staff')`); err != nil {
		t.Fatalf("plant category: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-a1", Caller: fxLoadActorID,
		Items: []load.Item{catItem("CAT-X", "fxcat-p2", "Planted By Staff")}})
	if err != nil {
		t.Fatalf("attach batch: %v", err)
	}
	if o := outcome(t, r, "CAT-X"); o.Status != load.OutcomeCreated {
		t.Fatalf("the attach answered %+v, want created", o)
	}
	var n int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM product_categories WHERE code = 'fxcat-p2'`).Scan(&n); err != nil {
		t.Fatalf("attach count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d rows carry the code, want the one attached row", n)
	}
	_, _, org, seq, _ := e.catRow(t, "fxcat-p2")
	if org != fxLoadOrgID || seq == 0 {
		t.Fatalf("the attached row carries org %q seq %d, want the load stamps", org, seq)
	}
	// A second legacy key naming the same code is a conflict naming both
	// legacy ids: nothing merges silently.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-a2", Caller: fxLoadActorID,
		Items: []load.Item{catItem("CAT-Y", "fxcat-p2", "Planted By Staff")}})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	o := outcome(t, r, "CAT-Y")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonKeyConflict {
		t.Fatalf("the second claim answered %+v, want refused as %s", o, load.ReasonKeyConflict)
	}
}

// A changed code on a recorded legacy key updates that row in place and
// draws exactly one sequence; the row count and the id do not move.
func TestCategoryLoadRenamedCodeUpdatesTheRecordedRowInPlace(t *testing.T) {
	e := categoryLoadSetup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-rn1", Caller: fxLoadActorID,
		Items: []load.Item{catItem("CAT-X", "fxcat-r1", "Fixture Renamed")}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	id1, _, _, seq1, _ := e.catRow(t, "fxcat-r1")
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-rn2", Caller: fxLoadActorID,
		Items: []load.Item{catItem("CAT-X", "fxcat-r2", "Fixture Renamed")}})
	if err != nil {
		t.Fatalf("rename batch: %v", err)
	}
	if o := outcome(t, r, "CAT-X"); o.Status != load.OutcomeUpdated || o.HHID != id1 {
		t.Fatalf("the code change answered %+v, want updated on the recorded row %s", o, id1)
	}
	id2, _, _, seq2, _ := e.catRow(t, "fxcat-r2")
	if id2 != id1 || seq2 != seq1+1 {
		t.Fatalf("the renamed row is %s seq %d, want the same row %s with one new draw after %d", id2, seq2, id1, seq1)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM product_categories WHERE code IN ('fxcat-r1','fxcat-r2')`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d rows carry the old or new code, want the one renamed row", n)
	}
}

// A new code that another row already carries is a conflict naming
// both legacy keys, and nothing of the write stands.
func TestCategoryLoadRenameOntoAnotherRowsCodeRefusesNamingBothKeys(t *testing.T) {
	e := categoryLoadSetup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-rc1", Caller: fxLoadActorID,
		Items: []load.Item{
			catItem("CAT-X", "fxcat-r1", "Fixture One"),
			catItem("CAT-Y", "fxcat-r3", "Fixture Three"),
		}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-rc2", Caller: fxLoadActorID,
		Items: []load.Item{catItem("CAT-Y", "fxcat-r1", "Fixture Three")}})
	if err != nil {
		t.Fatalf("collision batch: %v", err)
	}
	o := outcome(t, r, "CAT-Y")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonKeyConflict ||
		!strings.Contains(o.Detail, "CAT-Y") || !strings.Contains(o.Detail, "CAT-X") {
		t.Fatalf("the collision answered %+v, want a key conflict naming CAT-Y and CAT-X", o)
	}
}

// The parent walk fails closed at its bound: a chain deeper than the
// bound is refused, naming the field.
func TestCategoryLoadRefusesAChainOverTheDepthBound(t *testing.T) {
	e := categoryLoadSetup(t)
	ctx := context.Background()
	items := make([]load.Item, 0, 70)
	for i := 0; i < 70; i++ {
		it := load.Item{LegacyID: fmt.Sprintf("CAT-D%d", i),
			Record: map[string]any{"code": fmt.Sprintf("fxcat-d%d", i), "name": "Fixture Deep"}}
		if i > 0 {
			it.Requires = []load.Ref{{Kind: "category", LegacyID: fmt.Sprintf("CAT-D%d", i-1)}}
		}
		items = append(items, it)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-deep", Caller: fxLoadActorID, Items: items})
	if err != nil {
		t.Fatalf("deep batch: %v", err)
	}
	first := -1
	for i, o := range r.Items {
		if o.Status == load.OutcomeRefused {
			first = i
			break
		}
	}
	if first < 60 || first > 70 {
		t.Fatalf("the first refusal is at %d, want one just past the bound of %d", first, categoryCycleDepth)
	}
	o := r.Items[first]
	if o.Reason != "category_depth" || o.Field != "category" {
		t.Fatalf("the over bound item answered %+v, want refused as category_depth on the field category", o)
	}
}

// A row owned by another org is refused, never re-stamped.
func TestCategoryLoadRefusesARowOwnedByAnotherOrg(t *testing.T) {
	e := categoryLoadSetup(t)
	ctx := context.Background()
	const otherOrg = "4442a800-0000-4000-8000-00000000d0ff"
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO product_categories (code, name, org_id) VALUES ('fxcat-o1', 'Other Org Row', $1::uuid)`, otherOrg); err != nil {
		t.Fatalf("plant other org row: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
		BatchID: "b-cat-og1", Caller: fxLoadActorID,
		Items: []load.Item{catItem("CAT-X", "fxcat-o1", "Renamed By Load")}})
	if err != nil {
		t.Fatalf("org batch: %v", err)
	}
	o := outcome(t, r, "CAT-X")
	if o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" ||
		!strings.Contains(o.Detail, "category") || !strings.Contains(o.Detail, "CAT-X") {
		t.Fatalf("the other org's row answered %+v, want refused as org_mismatch naming the kind and key", o)
	}
	_, _, org, _, _ := e.catRow(t, "fxcat-o1")
	var name string
	if err := e.pool.QueryRow(ctx, `SELECT name FROM product_categories WHERE code = 'fxcat-o1'`).Scan(&name); err != nil {
		t.Fatalf("name read: %v", err)
	}
	if org != otherOrg || name != "Other Org Row" {
		t.Fatalf("the row now carries org %q name %q, want it untouched", org, name)
	}
}
