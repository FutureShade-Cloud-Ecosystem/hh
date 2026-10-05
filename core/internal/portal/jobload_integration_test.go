//go:build integration

// jobload_integration_test.go proves the load doors' job writer
// (HH-LOAD-1 LD-6) through the frame: jobs as projects rows, the record
// only (name, job address, customer by key), the stamps a loaded job
// carries (the tenant its customer is, the sync sequence, the idempotency
// key), the org riding through the customer, the replay arms (unchanged
// draws nothing, a change updates one row and draws exactly one), the
// missing and cross org customer references, and the pool 4 leg with its
// bite. The writer never opens a connection and never commits.
//
// Fixtures are invented. The fixture rows leave with the test.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5660/ld6a?sslmode=disable' \
//	  go test -tags integration ./internal/portal/... -count=1 -run TestJobLoad
package portal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	fxJobOrgID      = "4442a800-0000-4000-8000-00000000f001"
	fxJobOtherOrgID = "4442a800-0000-4000-8000-00000000f002"
	fxJobActorID    = "4442a800-0000-4000-8000-00000000f00a"
	fxJobSource     = "testsuite"
)

type jobAuditSpy struct{}

func (jobAuditSpy) RecordBatchTx(_ context.Context, _ pgx.Tx, _ load.BatchAuditRow) error { return nil }

type jobLoadEnv struct {
	pool                *pgxpool.Pool
	door                *load.Door
	custID, otherCustID string
}

func cleanJobLoad(ctx context.Context, pool *pgxpool.Pool) {
	for _, org := range []string{fxJobOrgID, fxJobOtherOrgID} {
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid`, org)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid`, org)
	}
	// By the fixture account prefix and by the fixture orgs' stamp, so a
	// row a broken reload left under either survives no run; the delete
	// cascades the projects.
	_, _ = pool.Exec(ctx, `
		DELETE FROM customers
		WHERE account_no::text LIKE 'fxjob-%' OR org_id IN ($1::uuid, $2::uuid)`, fxJobOrgID, fxJobOtherOrgID)
}

func jobLoadSetup(t *testing.T) *jobLoadEnv {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	cleanJobLoad(ctx, pool)
	t.Cleanup(func() { cleanJobLoad(context.Background(), pool) })
	e := &jobLoadEnv{pool: pool}
	for _, c := range []struct {
		acct, org string
		dst       *string
	}{{"fxjob-a", fxJobOrgID, &e.custID}, {"fxjob-o", fxJobOtherOrgID, &e.otherCustID}} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO customers (account_no, name, org_id, seq) VALUES ($1, 'Fixture Customer', $2::uuid, 1)
			RETURNING id::text`, c.acct, c.org).Scan(c.dst); err != nil {
			t.Fatalf("plant customer: %v", err)
		}
	}
	// The customer door ran before this one in a real load: plant the two
	// names it would have recorded (the other org's customer is named in
	// this org's registry on purpose).
	for legacy, hh := range map[string]string{"CUST-A": e.custID, "CUST-O": e.otherCustID} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
			VALUES ($1::uuid, $2, $3, $4, $5::uuid, repeat('a', 64))`,
			fxJobOrgID, fxJobSource, load.KindCustomer, legacy, hh); err != nil {
			t.Fatalf("plant name: %v", err)
		}
	}
	e.door = load.NewDoor(load.NewStore(pool), load.KindJob, JobLoadWriter{Customers: customer.OrgReader{}}, jobAuditSpy{})
	return e
}

func jobItem(legacyID, name, address string, requires ...load.Ref) load.Item {
	suffix := map[string]string{"JOB-1": "000000000001", "JOB-2": "000000000002"}[legacyID]
	return load.Item{LegacyID: legacyID, DerivedKey: "d7000000-0000-4000-8000-" + suffix,
		Record:   map[string]any{"name": name, "job_address": address},
		Requires: requires}
}

func (e *jobLoadEnv) submit(t *testing.T, door *load.Door, batchID string, items ...load.Item) load.BatchResult {
	t.Helper()
	r, err := door.UpsertBatch(context.Background(), load.Batch{OrgID: fxJobOrgID, SourceSystem: fxJobSource,
		BatchID: batchID, Caller: fxJobActorID, Items: items})
	if err != nil {
		t.Fatalf("batch %s: %v", batchID, err)
	}
	return r
}

func jobOutcome(t *testing.T, r load.BatchResult, legacyID string) load.Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("no outcome for %s: %+v", legacyID, r.Items)
	return load.Outcome{}
}

func (e *jobLoadEnv) scopeNext(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'projects'), 0)`).Scan(&n); err != nil {
		t.Fatalf("scope read: %v", err)
	}
	return n
}

func (e *jobLoadEnv) projectRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM projects WHERE customer_id IN ($1::uuid, $2::uuid)`, e.custID, e.otherCustID).Scan(&n); err != nil {
		t.Fatalf("project count: %v", err)
	}
	return n
}

func TestJobLoadCreatesProjectsAsRecordsUnderTheirCustomer(t *testing.T) {
	e := jobLoadSetup(t)
	before := e.scopeNext(t)
	r := e.submit(t, e.door, "b-j-1", jobItem("JOB-1", "Fixture Job", "12 Fixture Lane, Fixtureville",
		load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"}))
	o := jobOutcome(t, r, "JOB-1")
	if o.Status != load.OutcomeCreated || o.HHID == "" {
		t.Fatalf("the job answered %+v, want created", o)
	}
	var cust, tenant, name, addr, key string
	var seq int64
	if err := e.pool.QueryRow(context.Background(), `
		SELECT customer_id::text, coalesce(tenant_id::text, ''), name, job_address,
		       coalesce(idempotency_key::text, ''), coalesce(seq, 0) FROM projects WHERE id = $1::uuid`, o.HHID).
		Scan(&cust, &tenant, &name, &addr, &key, &seq); err != nil {
		t.Fatalf("project row: %v", err)
	}
	if cust != e.custID || tenant != e.custID || name != "Fixture Job" || addr != "12 Fixture Lane, Fixtureville" {
		t.Fatalf("project row %q %q %q %q: want the record under its customer, the tenant the customer is", cust, tenant, name, addr)
	}
	if seq <= before || key != "d7000000-0000-4000-8000-000000000001" {
		t.Fatalf("project seq %d (scope was %d) key %q: want the load's stamps", seq, before, key)
	}
	// Jobs are records only: nothing of the order to cash spine moved.
	var orders int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM orders WHERE project_id = $1::uuid`, o.HHID).Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("orders on the loaded job: %d %v, want none", orders, err)
	}
}

func TestJobLoadReplayUnchangedDrawsNothingAndUpdateDrawsOne(t *testing.T) {
	e := jobLoadSetup(t)
	ref := load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"}
	e.submit(t, e.door, "b-j-r1", jobItem("JOB-1", "Fixture Job", "1 Fixture Lane", ref))
	base := e.scopeNext(t)
	r := e.submit(t, e.door, "b-j-r2", jobItem("JOB-1", "Fixture Job", "1 Fixture Lane", ref))
	if jobOutcome(t, r, "JOB-1").Status != load.OutcomeUnchanged || e.scopeNext(t) != base {
		t.Fatalf("the identical replay was not unchanged and draw free")
	}
	r = e.submit(t, e.door, "b-j-r3", jobItem("JOB-1", "Fixture Job", "2 Fixture Lane", ref))
	if jobOutcome(t, r, "JOB-1").Status != load.OutcomeUpdated {
		t.Fatalf("the changed address was not updated")
	}
	if got := e.scopeNext(t); got != base+1 {
		t.Fatalf("the update drew %d over %d, want exactly one", got, base)
	}
	if e.projectRows(t) != 1 {
		t.Fatalf("%d project rows, want the one updated in place", e.projectRows(t))
	}
}

func TestJobLoadRefusesMissingAndCrossOrgCustomers(t *testing.T) {
	e := jobLoadSetup(t)
	r := e.submit(t, e.door, "b-j-m1",
		jobItem("JOB-1", "Fixture Job", "x", load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-NOPE"}),
		jobItem("JOB-2", "Fixture Job Two", "y", load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-O"}))
	o := jobOutcome(t, r, "JOB-1")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonMissingReference ||
		!strings.Contains(o.Detail, "customer") || !strings.Contains(o.Detail, "CUST-NOPE") {
		t.Fatalf("a missing customer answered %+v, want refused naming kind and key", o)
	}
	if o := jobOutcome(t, r, "JOB-2"); o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" ||
		!strings.Contains(o.Detail, "customer") || !strings.Contains(o.Detail, "CUST-O") {
		t.Fatalf("another org's customer answered %+v, want refused org_mismatch naming kind and key", o)
	}
	if e.projectRows(t) != 0 {
		t.Fatalf("a refused job left a project row")
	}
	// No customer reference at all, and no name.
	r = e.submit(t, e.door, "b-j-m2", jobItem("JOB-1", "Fixture Job", "x"),
		load.Item{LegacyID: "JOB-2", Record: map[string]any{"job_address": "x"},
			Requires: []load.Ref{{Kind: load.KindCustomer, LegacyID: "CUST-A"}}})
	if o := jobOutcome(t, r, "JOB-1"); o.Status != load.OutcomeRefused || o.Field != load.KindCustomer {
		t.Fatalf("a job with no customer answered %+v", o)
	}
	if o := jobOutcome(t, r, "JOB-2"); o.Status != load.OutcomeRefused || o.Field != "name" {
		t.Fatalf("a job with no name answered %+v", o)
	}
}

func poolFourJobs(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := intPool(t).Config().Copy()
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool four: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestJobLoadPoolFourThreeContendersEveryCallOnTheBatchTransaction(t *testing.T) {
	e := jobLoadSetup(t)
	pool := poolFourJobs(t)
	ctx := context.Background()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	door := load.NewDoor(load.NewStore(pool), load.KindJob, JobLoadWriter{Customers: customer.OrgReader{}}, jobAuditSpy{})
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			_, errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxJobOrgID, SourceSystem: fxJobSource,
				BatchID: fmt.Sprintf("b-j-pool-%d", k), Caller: fxJobActorID,
				Items: []load.Item{jobItem("JOB-1", "Fixture Job", "1 Fixture Lane",
					load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"})}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err != nil {
			t.Fatalf("contender %d: %v (a batch needed a connection beyond the transaction it holds)", k, err)
		}
	}
	if n := e.projectRows(t); n != 1 {
		t.Fatalf("%d project rows after three contenders, want one", n)
	}
}

type strayJobPoolWriter struct {
	pool  *pgxpool.Pool
	inner load.Writer
}

func (w strayJobPoolWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	if _, err := w.pool.Exec(ctx, `SELECT 1`); err != nil {
		return "", fmt.Errorf("stray pool use: %w", err)
	}
	return w.inner.Apply(ctx, tx, in)
}

func TestJobLoadPoolFourBitesAWriterThatUsesThePool(t *testing.T) {
	jobLoadSetup(t)
	pool := poolFourJobs(t)
	held, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	door := load.NewDoor(load.NewStore(pool), load.KindJob, strayJobPoolWriter{pool: pool, inner: JobLoadWriter{Customers: customer.OrgReader{}}}, jobAuditSpy{})
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxJobOrgID, SourceSystem: fxJobSource,
				BatchID: fmt.Sprintf("b-j-stray-%d", k), Caller: fxJobActorID,
				Items: []load.Item{jobItem("JOB-1", "Fixture Job", "x", load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"})}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err == nil {
			t.Fatalf("contender %d finished: the pool 4 leg missed a writer that uses the pool", k)
		}
	}
}

func TestJobLoadRefusalSaysTheCustomerRowIsGone(t *testing.T) {
	e := jobLoadSetup(t)
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, e.custID); err != nil {
		t.Fatalf("delete customer: %v", err)
	}
	r := e.submit(t, e.door, "b-j-g1", jobItem("JOB-1", "Fixture Job", "x", load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"}))
	o := jobOutcome(t, r, "JOB-1")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonMissingReference ||
		strings.Contains(o.Detail, "another org") || !strings.Contains(o.Detail, "CUST-A") {
		t.Fatalf("a job under a gone customer answered %+v, want missing_reference naming the key, not another org", o)
	}
}

// waitForJobLockWait polls until some session in this database waits on a
// lock, the batch standing behind the outside transaction.
func waitForJobLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatalf("lock wait poll: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no session ever waited on a lock: the batch never reached the held row")
}

func TestJobLoadDeadlockInTheWriterAnswersTheContentionSentinel(t *testing.T) {
	e := jobLoadSetup(t)
	ctx := context.Background()
	// A second customer of the org, whose row the outside transaction holds.
	var heldID string
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, org_id, seq) VALUES ('fxjob-b', 'Fixture Held', $1::uuid, 2)
		RETURNING id::text`, fxJobOrgID).Scan(&heldID); err != nil {
		t.Fatalf("plant held customer: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
		VALUES ($1::uuid, $2, $3, 'CUST-B', $4::uuid, repeat('a', 64))`,
		fxJobOrgID, fxJobSource, load.KindCustomer, heldID); err != nil {
		t.Fatalf("plant name: %v", err)
	}
	outside, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("outside begin: %v", err)
	}
	defer func() { _ = outside.Rollback(ctx) }()
	// A long deadlock timeout on the outside session, so the batch's own
	// detection (the default, a second) fires first and the batch is the
	// victim.
	if _, err := outside.Exec(ctx, `SET LOCAL deadlock_timeout = '30s'`); err != nil {
		t.Fatalf("outside timeout: %v", err)
	}
	if _, err := outside.Exec(ctx, `SELECT 1 FROM customers WHERE id = $1::uuid FOR UPDATE`, heldID); err != nil {
		t.Fatalf("outside lock: %v", err)
	}
	// Item one inserts a job and holds its registry name to the batch's end;
	// item two inserts a job under the held customer and waits. The outside
	// then asks for the registry name the batch holds.
	batch := load.Batch{OrgID: fxJobOrgID, SourceSystem: fxJobSource, BatchID: "b-j-dl", Caller: fxJobActorID,
		Items: []load.Item{
			jobItem("JOB-1", "Fixture Job One", "1 Fixture Lane", load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"}),
			jobItem("JOB-2", "Fixture Job Two", "2 Fixture Lane", load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-B"}),
		}}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := e.door.UpsertBatch(cctx, batch)
		done <- err
	}()
	waitForJobLockWait(t, e.pool)
	_, outsideErr := outside.Exec(ctx, `
		INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
		VALUES ($1::uuid, $2, 'job', 'JOB-1', gen_random_uuid(), repeat('c', 64))`, fxJobOrgID, fxJobSource)
	_ = outsideErr
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked batch answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)
	var batches, names, rows int
	if err := e.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-j-dl'),
		       (SELECT count(*) FROM load_key_registry WHERE org_id = $1::uuid AND kind = 'job'),
		       (SELECT count(*) FROM projects WHERE name LIKE 'Fixture Job %')`, fxJobOrgID).Scan(&batches, &names, &rows); err != nil {
		t.Fatalf("remainder read: %v", err)
	}
	if batches != 0 || names != 0 || rows != 0 {
		t.Fatalf("the victim left %d batch rows, %d names, %d job rows: a contended batch rolls back whole", batches, names, rows)
	}
	r, err := e.door.UpsertBatch(ctx, batch)
	if err != nil {
		t.Fatalf("resubmission: %v", err)
	}
	for _, o := range r.Items {
		if o.Status != load.OutcomeCreated {
			t.Fatalf("the resubmission answered %+v, want created", o)
		}
	}
}
