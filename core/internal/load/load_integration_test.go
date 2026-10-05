//go:build integration

// load_integration_test.go proves the keyed upsert helper's semantics
// against a scratch writer over a test-owned shape (the load_probe
// table, an upsert by natural code, the shape a door's owning-module
// writer carries): a name never seen creates; a replay with an
// unchanged hash writes nothing and draws no sequence; a changed field
// updates that row alone and draws exactly one new sequence; a row a
// second name would claim is refused as a conflict naming both legacy
// ids, and the batch's other items still land; a reference to a name
// not yet loaded is refused naming the kind and the key; two racing
// upserts on one name produce one row; a replayed batch id returns its
// first outcomes. The key read and the batch read close the file.
//
// Every fixture is invented; no real dealer value appears anywhere.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5620/ld1_gate?sslmode=disable' \
//	  go test -tags integration ./internal/load/... -count=1 -v
package load

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testOrgID, testSource and the derived keys are invented fixtures; the
// org id is a random-shaped uuid no org row is planted for, because the
// registry carries no foreign key (0079's crosswalk posture, carried).
const (
	testOrgID   = "5e1f0000-0000-4000-8000-1f0000000001"
	testSource  = "testsuite"
	testActorID = "5e1f0000-0000-4000-8000-1f000000000a"
	probeScope  = "load_probe"
)

// testPool opens the suite's pool off DATABASE_URL and skips politely
// when the gate variable is absent, the module legs' idiom.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// probeSeqCTE is the 0213 allocator, drawn the way the pricing module's
// syncscope draws it: one upsert on the scope's counter row, atomic in
// the caller's statement.
const probeSeqCTE = `WITH pseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('load_probe')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// probeWriter is the scratch writer: the owning module's write path for
// the test-owned load_probe shape, an upsert by the record's natural
// code on the create arm (so an attach to an existing row is exercised,
// the shape the unit and branch-map doors will carry) and an update by
// the named row id on the update arm. Both arms stamp the org, draw one
// sequence, and stamp the row's idempotency key, the 0213 stamps.
type probeWriter struct{}

func (probeWriter) Apply(ctx context.Context, tx pgx.Tx, in Write) (string, error) {
	code, _ := in.Record["code"].(string)
	payload, _ := in.Record["payload"].(string)
	if code == "" {
		return "", &Refusal{Code: "field_required", Field: "code", Detail: "the probe record names no code"}
	}
	if in.RowID == "" {
		var id string
		err := tx.QueryRow(ctx, probeSeqCTE+`
			INSERT INTO load_probe (org_id, code, payload, seq, idempotency_key)
			VALUES ($1, $2, $3, (SELECT next_value FROM pseq), NULLIF($4,'')::uuid)
			ON CONFLICT (code) DO UPDATE SET
				payload = EXCLUDED.payload,
				updated_at = now(),
				seq = EXCLUDED.seq,
				idempotency_key = EXCLUDED.idempotency_key
			RETURNING id::text`,
			in.OrgID, code, payload, in.DerivedKey).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("probe create: %w", err)
		}
		return id, nil
	}
	tag, err := tx.Exec(ctx, probeSeqCTE+`
		UPDATE load_probe SET
			payload = $3,
			updated_at = now(),
			seq = (SELECT next_value FROM pseq),
			idempotency_key = NULLIF($4,'')::uuid
		WHERE id = $2::uuid AND org_id = $1::uuid`,
		in.OrgID, in.RowID, payload, in.DerivedKey)
	if err != nil {
		return "", fmt.Errorf("probe update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", &Refusal{Code: ReasonUpdateTargetMissing, Field: "legacy_id",
			Detail: "the name's row is gone; the update refuses rather than creating a second row"}
	}
	return in.RowID, nil
}

// probeEnv is one test's fixtures: the pool, the store over it, the
// probe and bare doors, and each door's audit capture.
type probeEnv struct {
	pool       *pgxpool.Pool
	store      *Store
	door       *Door
	bareDoor   *Door
	probeAudit *countingAuditSink
	bareAudit  *countingAuditSink
}

// setup hands one test its fixtures, the two scratch shapes and a clean
// org slice of the two load tables, and undoes all of it at the end.
func setup(t *testing.T) *probeEnv {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	for _, ddl := range []string{
		`DROP TABLE IF EXISTS load_probe`,
		`DROP TABLE IF EXISTS bare_probe`,
		bareProbeTable,
		`CREATE TABLE load_probe (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id          UUID NOT NULL,
			code            TEXT NOT NULL UNIQUE,
			payload         TEXT NOT NULL,
			seq             BIGINT,
			idempotency_key UUID,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatalf("setup %q: %v", ddl, err)
		}
	}
	for _, ddl := range []string{
		`DELETE FROM load_key_registry WHERE org_id = $1::uuid`,
		`DELETE FROM load_batch WHERE org_id = $1::uuid`,
	} {
		if _, err := pool.Exec(ctx, ddl, testOrgID); err != nil {
			t.Fatalf("setup %q: %v", ddl, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM scope_sequences WHERE scope = 'load_probe'`); err != nil {
		t.Fatalf("setup scope: %v", err)
	}
	t.Cleanup(func() {
		for _, ddl := range []string{
			`DELETE FROM load_key_registry WHERE org_id = $1::uuid`,
			`DELETE FROM load_batch WHERE org_id = $1::uuid`,
		} {
			_, _ = pool.Exec(ctx, ddl, testOrgID)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM scope_sequences WHERE scope = 'load_probe'`)
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS load_probe`)
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS bare_probe`)
		pool.Close()
	})
	store := NewStore(pool)
	probeAudit := &countingAuditSink{}
	bareAudit := &countingAuditSink{}
	return &probeEnv{
		pool:       pool,
		store:      store,
		door:       NewDoor(store, "probe", probeWriter{}, probeAudit),
		bareDoor:   NewDoor(store, "bare", bareWriter{}, bareAudit),
		probeAudit: probeAudit,
		bareAudit:  bareAudit,
	}
}

// bareRowsCount counts the committed bare rows one name's row makes up:
// the race's one-row assertion read through the registry name.
func (e *probeEnv) bareRowsCount(t *testing.T, legacyID string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM bare_probe
		WHERE org_id = $1::uuid AND id IN (
			SELECT hh_id::uuid FROM load_key_registry
			WHERE org_id = $1::uuid AND source_system = $2 AND kind = 'bare' AND legacy_id = $3)`,
		testOrgID, testSource, legacyID).Scan(&n); err != nil {
		t.Fatalf("bare count: %v", err)
	}
	return n
}

// bareProbeTable is the second test-owned shape: no natural key at all,
// every create arm a fresh insert, the shape the review's race probe
// showed the false conflict on.
const bareProbeTable = `CREATE TABLE IF NOT EXISTS bare_probe (
	id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	org_id          UUID NOT NULL,
	payload         TEXT NOT NULL,
	seq             BIGINT,
	idempotency_key UUID,
	created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// bareWriter is an insert-only writer over bare_probe: no natural key
// to attach by, one sequence drawn per write through the 0213
// allocator, the stamps carried.
type bareWriter struct{}

func (bareWriter) Apply(ctx context.Context, tx pgx.Tx, in Write) (string, error) {
	payload, _ := in.Record["payload"].(string)
	if in.RowID != "" {
		tag, err := tx.Exec(ctx, probeSeqCTE+`
			UPDATE bare_probe SET
				payload = $3,
				updated_at = now(),
				seq = (SELECT next_value FROM pseq),
				idempotency_key = NULLIF($4,'')::uuid
			WHERE id = $2::uuid AND org_id = $1::uuid`,
			in.OrgID, in.RowID, payload, in.DerivedKey)
		if err != nil {
			return "", fmt.Errorf("bare update: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return "", &Refusal{Code: ReasonUpdateTargetMissing, Field: "legacy_id",
				Detail: "the name's row is gone; the update refuses rather than creating a second row"}
		}
		return in.RowID, nil
	}
	var id string
	err := tx.QueryRow(ctx, probeSeqCTE+`
		INSERT INTO bare_probe (org_id, payload, seq, idempotency_key)
		VALUES ($1, $2, (SELECT next_value FROM pseq), NULLIF($3,'')::uuid)
		RETURNING id::text`, in.OrgID, payload, in.DerivedKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("bare create: %w", err)
	}
	return id, nil
}

// countingAuditSink captures the audit rows the doors write, the suite's
// stand-in for the platform's audit write path.
type countingAuditSink struct {
	mu   sync.Mutex
	rows []BatchAuditRow
}

func (s *countingAuditSink) RecordBatchTx(_ context.Context, _ pgx.Tx, row BatchAuditRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, row)
	return nil
}

func (s *countingAuditSink) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

func (s *countingAuditSink) last() BatchAuditRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[len(s.rows)-1]
}

// TestEveryBatchLandsOneAuditRow pins the audit ruling: one audit row per
// batch, the machine principal as actor and the counts by outcome, no
// record value anywhere in the row, and a replayed batch lands no second
// row.
func TestEveryBatchLandsOneAuditRow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-audit-1", Caller: testActorID,
		Items: []Item{
			probeItem("L-1", "c-1", "one", "d1000000-0000-4000-8000-000000000001"),
			probeItem("L-2", "c-2", "two", "d1000000-0000-4000-8000-000000000002"),
		}}); err != nil {
		t.Fatalf("first submission: %v", err)
	}
	// A second batch whose one item replays unchanged: the counts differ
	// per batch, and each batch carries its own row.
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-audit-2", Caller: testActorID,
		Items: []Item{probeItem("L-1", "c-1", "one", "d1000000-0000-4000-8000-000000000001")}}); err != nil {
		t.Fatalf("replay batch: %v", err)
	}
	if n := e.probeAudit.len(); n != 2 {
		t.Fatalf("two batches landed %d audit rows, want exactly one per batch", n)
	}
	row := e.probeAudit.last()
	if row.ActorID != testActorID || row.Kind != "probe" || row.BatchID != "b-audit-2" {
		t.Fatalf("audit row carries the wrong identity: %+v", row)
	}
	if row.Counts[OutcomeUnchanged] != 1 || row.Counts[OutcomeCreated] != 0 {
		t.Fatalf("audit row counts %+v, want one unchanged", row.Counts)
	}
	// A replayed batch id returns its first outcomes and lands no second
	// audit row.
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-audit-1", Caller: testActorID,
		Items: []Item{probeItem("L-9", "c-9", "nine", "d1000000-0000-4000-8000-000000000009")}}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n := e.probeAudit.len(); n != 2 {
		t.Fatalf("the replay landed a second audit row: %d rows", n)
	}
}

// TestRacesOnOneNameNeverRefuseAFalseConflict drives the review's probe
// into the suite: two concurrent upserts on one name over an
// insert-only, sequence-drawing writer, many races, every one of them
// answering created and unchanged (or updated, when the records
// differ), never a key_conflict, and exactly one row per race. A
// changed record from the race loser lands as the row's content.
func TestRacesOnOneNameNeverRefuseAFalseConflict(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	const races = 24
	for i := 0; i < races; i++ {
		name := fmt.Sprintf("R-%02d", i)
		changed := i == races-1
		payloads := []string{"first", "second"}
		results := make([]BatchResult, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for k := 0; k < 2; k++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				payload := payloads[k]
				if changed && k == 1 {
					payload = "loser-change"
				}
				results[k], errs[k] = e.bareDoor.UpsertBatch(ctx, Batch{
					OrgID: testOrgID, SourceSystem: testSource,
					BatchID: fmt.Sprintf("bb-%02d-%d", i, k),
					Items:   []Item{probeBareItem(name, payload)},
				})
			}(k)
		}
		wg.Wait()
		for k, err := range errs {
			if err != nil {
				t.Fatalf("race %d racer %d: %v", i, k, err)
			}
			if o := outcomeOf(t, results[k], name); o.Status == OutcomeRefused {
				t.Fatalf("race %d racer %d: false conflict refused %+v", i, k, o)
			}
		}
		if changed {
			continue
		}
		if n := e.bareRowsCount(t, name); n != 1 {
			t.Fatalf("race %d left %d rows, want exactly one", i, n)
		}
		created := 0
		for k := range results {
			if outcomeOf(t, results[k], name).Status == OutcomeCreated {
				created++
			}
		}
		if created != 1 {
			t.Fatalf("race %d: %d racers created, want exactly one", i, created)
		}
	}
	if n := e.bareRowsCount(t, "R-00"); n != 1 {
		t.Fatalf("the plain races left %d rows for R-00, want one", n)
	}
	// The changed race: no refusal (asserted above), and the loser's
	// record stands as the row's content, because the loser retries into
	// the update arm against the winner's committed row.
	var payload string
	if err := e.pool.QueryRow(ctx,
		`SELECT payload FROM bare_probe WHERE org_id = $1::uuid AND id IN (
			SELECT hh_id::uuid FROM load_key_registry
			WHERE org_id = $1::uuid AND source_system = $2 AND kind = 'bare' AND legacy_id = $3)`,
		testOrgID, testSource, fmt.Sprintf("R-%02d", races-1)).Scan(&payload); err != nil {
		t.Fatalf("changed race row read: %v", err)
	}
	if payload != "loser-change" && payload != "first" {
		t.Fatalf("changed race row payload %q, want the loser's change or the winner's record", payload)
	}
}

// probeBareItem is one batch item over the bare shape.
func probeBareItem(legacyID, payload string) Item {
	return Item{LegacyID: legacyID, Record: map[string]any{"payload": payload}}
}

// probeItem is one batch item over the probe shape.
func probeItem(legacyID, code, payload, derivedKey string, requires ...Ref) Item {
	rec := map[string]any{"code": code, "payload": payload}
	return Item{LegacyID: legacyID, DerivedKey: derivedKey, Record: rec, Requires: requires}
}

// probeRow reads one probe row's payload, seq and idempotency key.
func (e *probeEnv) probeRow(t *testing.T, code string) (id, payload string, seq int64, key string) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT id::text, payload, coalesce(seq, 0), coalesce(idempotency_key::text, '')
		FROM load_probe WHERE org_id = $1::uuid AND code = $2`, testOrgID, code).
		Scan(&id, &payload, &seq, &key)
	if err != nil {
		t.Fatalf("probe row %s: %v", code, err)
	}
	return id, payload, seq, key
}

// probeRowsCount counts the probe rows for one code.
func (e *probeEnv) probeRowsCount(t *testing.T, code string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM load_probe WHERE org_id = $1::uuid AND code = $2`,
		testOrgID, code).Scan(&n); err != nil {
		t.Fatalf("probe count: %v", err)
	}
	return n
}

// probeSeqNext reads the probe scope's allocator value (zero when the
// scope has never been drawn).
func (e *probeEnv) probeSeqNext(t *testing.T) int64 {
	t.Helper()
	var next int64
	err := e.pool.QueryRow(context.Background(),
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'load_probe'), 0)`).
		Scan(&next)
	if err != nil {
		t.Fatalf("scope read: %v", err)
	}
	return next
}

// registryLegacyIDs names every legacy id recorded for one hh row.
func (e *probeEnv) registryLegacyIDs(t *testing.T, hhID string) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `
		SELECT legacy_id FROM load_key_registry
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = 'probe' AND hh_id = $3::uuid`,
		testOrgID, testSource, hhID)
	if err != nil {
		t.Fatalf("registry read: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("registry scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func outcomeOf(t *testing.T, r BatchResult, legacyID string) Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("batch carries no outcome for %s: %+v", legacyID, r.Items)
	return Outcome{}
}

// TestReferenceOnlyChangeUpdatesAndDraws pins the hash's coverage: the
// content hash covers the record plus the named references (kind and
// legacy key as named, never the resolved ids), so a re-parent lands as
// an update and draws its sequence instead of answering unchanged.
func TestReferenceOnlyChangeUpdatesAndDraws(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-refh-0",
		Items: []Item{
			probeItem("P-1", "p-1", "parent-one", "b1000000-0000-4000-8000-000000000001"),
			probeItem("P-2", "p-2", "parent-two", "b1000000-0000-4000-8000-000000000002"),
		}}); err != nil {
		t.Fatalf("parents: %v", err)
	}
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-refh-1",
		Items: []Item{probeItem("C-1", "c-1", "child", "b1000000-0000-4000-8000-000000000003",
			Ref{Kind: "probe", LegacyID: "P-1"})}}); err != nil {
		t.Fatalf("child: %v", err)
	}
	seqBefore := e.probeSeqNext(t)

	r, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-refh-2",
		Items: []Item{probeItem("C-1", "c-1", "child", "b1000000-0000-4000-8000-000000000003",
			Ref{Kind: "probe", LegacyID: "P-2"})}})
	if err != nil {
		t.Fatalf("re-parent: %v", err)
	}
	if o := outcomeOf(t, r, "C-1"); o.Status != OutcomeUpdated {
		t.Fatalf("a reference-only change answered %q, want updated", o.Status)
	}
	if drew := e.probeSeqNext(t) - seqBefore; drew != 1 {
		t.Fatalf("the re-parent drew %d sequences, want exactly one", drew)
	}
	// The replayed identical item (record and references) still answers
	// unchanged.
	r, err = e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-refh-3",
		Items: []Item{probeItem("C-1", "c-1", "child", "b1000000-0000-4000-8000-000000000003",
			Ref{Kind: "probe", LegacyID: "P-2"})}})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if o := outcomeOf(t, r, "C-1"); o.Status != OutcomeUnchanged {
		t.Fatalf("the identical replay answered %q, want unchanged", o.Status)
	}
	// A reference reordered among others is the same name set: the hash
	// holds.
	r, err = e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-refh-4",
		Items: []Item{probeItem("C-1", "c-1", "child", "b1000000-0000-4000-8000-000000000003",
			Ref{Kind: "probe", LegacyID: "P-2"}, Ref{Kind: "probe", LegacyID: "P-1"})}})
	if err != nil {
		t.Fatalf("added reference: %v", err)
	}
	if o := outcomeOf(t, r, "C-1"); o.Status != OutcomeUpdated {
		t.Fatalf("an added reference answered %q, want updated", o.Status)
	}
}

func TestUpsertCreatesANeverSeenName(t *testing.T) {
	e := setup(t)

	r, err := e.door.UpsertBatch(context.Background(), Batch{
		OrgID: testOrgID, SourceSystem: testSource, BatchID: "b-create-1", Caller: "load-lane-test",
		Items: []Item{probeItem("L-1", "c-1", "first", "a1000000-0000-4000-8000-000000000001")},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	o := outcomeOf(t, r, "L-1")
	if o.Status != OutcomeCreated {
		t.Fatalf("first sight answered %q, want created", o.Status)
	}
	if r.Counts[OutcomeCreated] != 1 {
		t.Fatalf("counts %+v, want one created", r.Counts)
	}
	_, payload, seq, key := e.probeRow(t, "c-1")
	if payload != "first" || seq != 1 {
		t.Fatalf("row payload %q seq %d, want first and the first draw", payload, seq)
	}
	if key != "a1000000-0000-4000-8000-000000000001" {
		t.Fatalf("row idempotency key %q, want the derived key", key)
	}
}

func TestUpsertReplayUnchangedWritesNothingAndDrawsNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	item := probeItem("L-1", "c-1", "same", "a1000000-0000-4000-8000-000000000001")
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-replay-1", Items: []Item{item}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	seqBefore := e.probeSeqNext(t)
	_, _, rowSeqBefore, _ := e.probeRow(t, "c-1")

	r, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-replay-2", Items: []Item{item}})
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if o := outcomeOf(t, r, "L-1"); o.Status != OutcomeUnchanged {
		t.Fatalf("unchanged replay answered %q, want unchanged", o.Status)
	}
	if after := e.probeSeqNext(t); after != seqBefore {
		t.Fatalf("unchanged replay drew a sequence: scope moved %d to %d", seqBefore, after)
	}
	if _, _, rowSeq, _ := e.probeRow(t, "c-1"); rowSeq != rowSeqBefore {
		t.Fatalf("unchanged replay moved the row's seq to %d", rowSeq)
	}
}

func TestUpsertChangeUpdatesThatRowAloneAndDrawsExactlyOneSequence(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-change-1",
		Items: []Item{
			probeItem("L-1", "c-1", "before", "a1000000-0000-4000-8000-000000000001"),
			probeItem("L-2", "c-2", "steady", "a1000000-0000-4000-8000-000000000002"),
		}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	seqBefore := e.probeSeqNext(t)
	_, _, rowOneSeqBefore, _ := e.probeRow(t, "c-1")
	_, _, rowTwoSeq, _ := e.probeRow(t, "c-2")

	r, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-change-2",
		Items: []Item{
			probeItem("L-1", "c-1", "after", "a1000000-0000-4000-8000-000000000001"),
			probeItem("L-2", "c-2", "steady", "a1000000-0000-4000-8000-000000000002"),
		}})
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if o := outcomeOf(t, r, "L-1"); o.Status != OutcomeUpdated {
		t.Fatalf("changed record answered %q, want updated", o.Status)
	}
	if o := outcomeOf(t, r, "L-2"); o.Status != OutcomeUnchanged {
		t.Fatalf("untouched record answered %q, want unchanged", o.Status)
	}
	if drew := e.probeSeqNext(t) - seqBefore; drew != 1 {
		t.Fatalf("the batch drew %d sequences, want exactly one", drew)
	}
	if _, payload, seq, _ := e.probeRow(t, "c-1"); payload != "after" || seq != seqBefore+1 || seq <= rowOneSeqBefore {
		t.Fatalf("changed row payload %q seq %d, want after and the one new draw (%d) over %d", payload, seq, seqBefore+1, rowOneSeqBefore)
	}
	if _, payload, seq, _ := e.probeRow(t, "c-2"); payload != "steady" || seq != rowTwoSeq {
		t.Fatalf("the other row moved: payload %q seq %d, want steady at %d", payload, seq, rowTwoSeq)
	}
}

func TestUpsertConflictRefusesNamingBothLegacyIDsAndTheRestLands(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-conflict-1",
		Items:   []Item{probeItem("L-1", "c-1", "held", "a1000000-0000-4000-8000-000000000001")}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}

	r, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-conflict-2",
		Items: []Item{
			// L-2's record carries the same natural code, so the writer's
			// attach arm resolves it to the row L-1 already names.
			probeItem("L-2", "c-1", "claim", "a1000000-0000-4000-8000-000000000002"),
			probeItem("L-3", "c-3", "independent", "a1000000-0000-4000-8000-000000000003"),
		}})
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	o := outcomeOf(t, r, "L-2")
	if o.Status != OutcomeRefused || o.Reason != ReasonKeyConflict {
		t.Fatalf("second claim answered %+v, want refused as %s", o, ReasonKeyConflict)
	}
	if !stringsContain(o.Detail, "L-1") || !stringsContain(o.Detail, "L-2") {
		t.Fatalf("conflict detail %q does not name both legacy ids", o.Detail)
	}
	other := outcomeOf(t, r, "L-3")
	if other.Status != OutcomeCreated {
		t.Fatalf("the batch's other item answered %+v, want created (one refusal must not stop the rest)", other)
	}
	if ids := e.registryLegacyIDs(t, other.HHID); len(ids) != 1 || ids[0] != "L-3" {
		t.Fatalf("the surviving row carries %v, want L-3 alone", ids)
	}
	if n := e.probeRowsCount(t, "c-1"); n != 1 {
		t.Fatalf("the contested code holds %d rows, want one", n)
	}
}

func TestUpsertRefusesAMissingReferenceNamingKindAndKey(t *testing.T) {
	e := setup(t)
	r, err := e.door.UpsertBatch(context.Background(), Batch{
		OrgID: testOrgID, SourceSystem: testSource, BatchID: "b-ref-1",
		Items: []Item{
			probeItem("L-1", "c-1", "child", "a1000000-0000-4000-8000-000000000001",
				Ref{Kind: "probe_parent", LegacyID: "parent-999"}),
			probeItem("L-4", "c-4", "standalone", "a1000000-0000-4000-8000-000000000004"),
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	o := outcomeOf(t, r, "L-1")
	if o.Status != OutcomeRefused || o.Reason != ReasonMissingReference {
		t.Fatalf("missing reference answered %+v, want refused as %s", o, ReasonMissingReference)
	}
	if !stringsContain(o.Detail, "probe_parent") || !stringsContain(o.Detail, "parent-999") {
		t.Fatalf("refusal detail %q does not name the kind and the key", o.Detail)
	}
	if o := outcomeOf(t, r, "L-4"); o.Status != OutcomeCreated {
		t.Fatalf("the other item answered %+v, want created", o)
	}
	if n := e.probeRowsCount(t, "c-1"); n != 0 {
		t.Fatalf("the refused item wrote %d rows, want none", n)
	}
}

func TestTwoRacingUpsertsOnOneNameProduceOneRow(t *testing.T) {
	e := setup(t)
	item := probeItem("L-race", "c-race", "raced", "a1000000-0000-4000-8000-000000000009")
	results := make([]BatchResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = e.door.UpsertBatch(context.Background(), Batch{
				OrgID: testOrgID, SourceSystem: testSource,
				BatchID: fmt.Sprintf("b-race-%d", i+1), Items: []Item{item},
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	if n := e.probeRowsCount(t, "c-race"); n != 1 {
		t.Fatalf("the race left %d rows, want exactly one", n)
	}
	created := 0
	for i := range results {
		if o := outcomeOf(t, results[i], "L-race"); o.Status == OutcomeCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d racers reported created, want exactly one: %+v, %+v", created, results[0].Items, results[1].Items)
	}
}

func TestReplayedBatchIDReturnsItsFirstOutcomes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	first, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-replay-same",
		Items: []Item{
			probeItem("L-1", "c-1", "one", "a1000000-0000-4000-8000-000000000001"),
			probeItem("L-2", "c-2", "two", "a1000000-0000-4000-8000-000000000002"),
		}})
	if err != nil {
		t.Fatalf("first submission: %v", err)
	}
	rowsBefore, seqBefore := e.probeRowsCount(t, "c-1"), e.probeSeqNext(t)

	second, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-replay-same",
		Items: []Item{
			probeItem("L-1", "c-1", "CHANGED", "a1000000-0000-4000-8000-000000000001"),
		}})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(second.Items) != len(first.Items) {
		t.Fatalf("replay answered %d outcomes, want the first submission's %d", len(second.Items), len(first.Items))
	}
	for i := range first.Items {
		if second.Items[i] != first.Items[i] {
			t.Fatalf("replay outcome %d is %+v, want the first submission's %+v", i, second.Items[i], first.Items[i])
		}
	}
	if second.Counts[OutcomeCreated] != 2 {
		t.Fatalf("replay counts %+v, want the first submission's", second.Counts)
	}
	if n := e.probeRowsCount(t, "c-1"); n != rowsBefore {
		t.Fatalf("the replay wrote rows: %d, want %d", n, rowsBefore)
	}
	if after := e.probeSeqNext(t); after != seqBefore {
		t.Fatalf("the replay drew sequences: %d, want %d", after, seqBefore)
	}
	if _, payload, _, _ := e.probeRow(t, "c-1"); payload != "one" {
		t.Fatalf("the replay overwrote the record: payload %q, want one", payload)
	}
}

// TestCrossDoorBatchReplayRefuses pins the replay's key: a batch id is
// one door's record, and replaying it against a different door refuses
// with the door mismatch instead of returning the first door's outcomes
// and loading nothing.
func TestCrossDoorBatchReplayRefuses(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-shared",
		Items:   []Item{probeItem("L-1", "c-1", "one", "c1000000-0000-4000-8000-000000000001")}}); err != nil {
		t.Fatalf("first door: %v", err)
	}

	_, err := e.bareDoor.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-shared",
		Items:   []Item{probeBareItem("O-1", "other")}})
	if !errors.Is(err, ErrBatchDoorMismatch) {
		t.Fatalf("cross door replay answered %v, want ErrBatchDoorMismatch", err)
	}
	if n := e.bareRowsCount(t, "O-1"); n != 0 {
		t.Fatalf("the cross door replay wrote %d rows, want none", n)
	}
	// The same door replaying its own batch id still replays.
	r, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-shared",
		Items:   []Item{probeItem("L-1", "c-1", "CHANGED", "c1000000-0000-4000-8000-000000000001")}})
	if err != nil {
		t.Fatalf("same door replay: %v", err)
	}
	if o := outcomeOf(t, r, "L-1"); o.Status != OutcomeCreated {
		t.Fatalf("same door replay answered %+v, want the first outcomes", o)
	}
}

func TestKeyReadResolvesNamesToIDsAndCounts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-resolve-1",
		Items: []Item{
			probeItem("L-1", "c-1", "one", "a1000000-0000-4000-8000-000000000001"),
			probeItem("L-2", "c-2", "two", "a1000000-0000-4000-8000-000000000002"),
		}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	id1, _, _, _ := e.probeRow(t, "c-1")

	r, err := e.store.ResolveKeys(ctx, testOrgID, testSource, []Ref{
		{Kind: "probe", LegacyID: "L-1"},
		{Kind: "probe", LegacyID: "L-absent"},
		{Kind: "probe_other", LegacyID: "L-1"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Items[0].HHID != id1 {
		t.Fatalf("resolve answered hh id %q, want the row %q", r.Items[0].HHID, id1)
	}
	if r.Items[1].HHID != "" || r.Items[2].HHID != "" {
		t.Fatalf("resolve invented ids for unrecorded names: %+v", r.Items[1:])
	}
	if r.Counts["found"] != 1 || r.Counts["missing"] != 2 {
		t.Fatalf("resolve counts %+v, want one found and two missing", r.Counts)
	}
}

func TestBatchReadReturnsTheRecordedBatch(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-read-1", Caller: "load-lane-test",
		Items: []Item{probeItem("L-1", "c-1", "one", "a1000000-0000-4000-8000-000000000001")}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	v, ok, err := e.store.GetBatch(ctx, testOrgID, "b-read-1")
	if err != nil || !ok {
		t.Fatalf("get batch: ok %v err %v", ok, err)
	}
	if v.BatchID != "b-read-1" || v.SourceSystem != testSource || v.Caller != "load-lane-test" {
		t.Fatalf("batch view carries the wrong identity: %+v", v)
	}
	if v.State != BatchStateRecorded {
		t.Fatalf("batch state %q, want %s", v.State, BatchStateRecorded)
	}
	if v.Counts[OutcomeCreated] != 1 || len(v.Items) != 1 {
		t.Fatalf("batch view counts %+v items %+v, want one created outcome", v.Counts, v.Items)
	}
	if _, ok, _ := e.store.GetBatch(ctx, testOrgID, "b-read-absent"); ok {
		t.Fatal("an absent batch answered found")
	}
}

func TestUpsertValidatesItsBatch(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, BatchID: "b-v-1",
		Items: []Item{probeItem("L-1", "c-1", "one", "")}}); !errors.Is(err, ErrMissingField) {
		t.Fatalf("a batch without its source system answered %v, want ErrMissingField", err)
	}
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-v-2"}); !errors.Is(err, ErrBatchBound) {
		t.Fatalf("a batch with no items answered %v, want ErrBatchBound", err)
	}
	if _, err := e.door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-v-3", Items: []Item{
			probeItem("", "c-1", "one", ""),
		}}); !errors.Is(err, ErrMissingField) {
		t.Fatalf("an item with no legacy id answered %v, want ErrMissingField", err)
	}
}

// stringsContain is a tiny helper so the refusals' key naming reads
// plainly in the assertions above.
func stringsContain(s, sub string) bool {
	for i := 0; len(sub) > 0 && i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
