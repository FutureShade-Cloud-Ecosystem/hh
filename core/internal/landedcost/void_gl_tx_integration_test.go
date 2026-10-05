//go:build integration

// void_gl_tx_integration_test.go, the legs for Void's reversing journal riding the void's own transaction.
//
// THE DEFECT CLASS. Void holds a pool connection (its WithinTx transaction, with the header row and the
// allocation rows locked) and, on the unfixed tree, posts the reversing journal through the GL seam's pool
// variant, whose InsertJournal opens a second WHOLE transaction on the same pool. With the pool at four
// connections and four voids running together, each caller holds one connection and waits for a fifth that
// never comes: connection starvation by second acquisition. Postgres sees nothing wrong (the sessions sit
// idle in transaction, no lock is waited on for the starvation itself), so the process stalls until each
// action's timeout. Money stays correct (everything rolls back); availability does not.
//
// A SECOND DEFECT in the same ordering: the journal commits in its own transaction BEFORE the void's commit,
// so a failure after the post (the status flip, the commit itself) leaves a POSTED journal for a doc that
// stayed un-VOIDED. The fix posts the journal through the void's transaction, so the two commit or roll back
// together.
//
// These legs use the REAL gl.Service and gl.Repository, built on the SAME pool as landedcost, so the
// starvation is not hidden by a fake. The inventory seam is the package's own DB-backed txDurableWAC (the
// real seam's shape: the WAC row is read and written through the caller's transaction). Helper connections
// (blocker, monitor, setup) come from other pools and never eat a scenario connection.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'VoidGL' ./internal/landedcost/
package landedcost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

const (
	// voidPoolSize is the scenario pool's connection cap and the number of concurrent voids derives from it.
	voidPoolSize = 4
	// voidStallDeadline bounds the whole leg so a stall fails in seconds rather than hanging the suite.
	voidStallDeadline = 10 * time.Second
	// voidOverlapWindow is how long the leg waits for every caller to be parked on the blocker's lock before
	// it releases the blocker anyway.
	voidOverlapWindow = 3 * time.Second

	voidScenarioApp = "lc_void_gl_scenario"
)

// realVoidGL adapts gl.Service onto the landedcost GLPoster seam, the same shape the cmd/core wiring's
// landedGLPoster provides. PostJournalTx is the tx-aware variant the fixed Void routes the reversing journal
// through; on the unfixed tree it is an unused extra method.
type realVoidGL struct{ svc *gl.Service }

func voidGLExtLines(in []GLLine) []gl.ExternalLine {
	lines := make([]gl.ExternalLine, len(in))
	for i, l := range in {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return lines
}

func (a realVoidGL) PostJournal(ctx context.Context, in GLJournal) (string, error) {
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: voidGLExtLines(in.Lines),
	})
}

func (a realVoidGL) PostJournalTx(ctx context.Context, tx pgx.Tx, in GLJournal) (string, error) {
	return a.svc.PostExternalTx(ctx, tx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: voidGLExtLines(in.Lines),
	})
}

// voidGLURL is the legs' database url; a leg never runs against a default database.
func voidGLURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	return url
}

// voidGLProduct inserts a dedicated product for allocation rows to reference (product_id is a products FK)
// and returns its id. Fixed ids, idempotent, so the suite can run repeatedly against one database.
func voidGLProduct(t *testing.T, pool *pgxpool.Pool, n int) string {
	t.Helper()
	id := fmt.Sprintf("d12c0000-0000-4000-9000-0000000000%02d", n)
	ctx := context.Background()
	var uom string
	if err := pool.QueryRow(ctx, `SELECT code FROM uoms ORDER BY code LIMIT 1`).Scan(&uom); err != nil {
		t.Fatalf("voidGLProduct: resolve uom: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO products (id, sku, name, base_uom, price_uom, weight_per_unit_kg, is_active)
		 VALUES ($1, $2, $3, $4, $4, 20.0, TRUE) ON CONFLICT (id) DO NOTHING`,
		id, fmt.Sprintf("LC-VOID-GL-%d", n), fmt.Sprintf("Void GL leg product %d", n), uom); err != nil {
		t.Fatalf("voidGLProduct: %v", err)
	}
	return id
}

// voidGLDoc creates one POSTED landed cost over its own single product: a DB-backed WAC row (txDurableWAC, the
// package's real-seam stand-in), a received line, a Create, and an Apply that posts its capitalization journal
// through the real gl.Service. Setup rides the helper pool and never touches the scenario pool.
func voidGLDoc(t *testing.T, helper *pgxpool.Pool, glSvc *gl.Service, n int) (docID, product string) {
	t.Helper()
	product = voidGLProduct(t, helper, n)
	capitalizer := &txDurableWAC{
		lines:  []ReceivedLine{{ProductID: product, QtyBase: 200, ValueCents: 100000, WeightKg: 1}},
		onHand: map[string]float64{product: 200},
	}
	capitalizer.ensureWACTable(t, helper, product, 130)
	svc := NewService(NewRepository(helper)).WithCapitalizer(capitalizer).WithGLPoster(realVoidGL{svc: glSvc}).withCaller(globalCaller)
	id, err := svc.Create(context.Background(), CreateInput{
		POID: landedPO, BranchID: trentonBranch, Currency: "CAD", FreightCents: 2700, AllocationBasis: "value",
	})
	if err != nil {
		t.Fatalf("create doc %d: %v", n, err)
	}
	if err := svc.Apply(context.Background(), id); err != nil {
		t.Fatalf("apply doc %d: %v", n, err)
	}
	key := "landedcost.void:" + id
	t.Cleanup(func() { voidGLDropJournal(helper, key) })
	key = "landedcost.apply:" + id
	t.Cleanup(func() { voidGLDropJournal(helper, key) })
	return id, product
}

// voidGLDropJournal removes a leg's journal by key, best effort (a journal that is still referenced by the
// doc's gl_journal_id stays).
func voidGLDropJournal(pool *pgxpool.Pool, key string) {
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, key)
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE idempotency_key = $1`, key)
}

// voidGLJournalCount counts journals under one idempotency key.
func voidGLJournalCount(t *testing.T, pool *pgxpool.Pool, key string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count journals: %v", err)
	}
	return n
}

// voidGLScenario builds the scenario pool at voidPoolSize and a landedcost service whose GL seam is the REAL
// gl service over that same pool (the pool the starvation is about). The capitalizer is a fresh DB-backed
// seam covering every leg product; at void time it only reads its maps and writes through the caller's tx.
func voidGLScenario(t *testing.T, url string, products []string) (*Service, *pgxpool.Pool) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.MaxConns = int32(voidPoolSize)
	cfg.ConnConfig.RuntimeParams["application_name"] = voidScenarioApp
	scn, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("scenario pool: %v", err)
	}
	t.Cleanup(scn.Close)
	onHand := make(map[string]float64, len(products))
	for _, p := range products {
		onHand[p] = 200
	}
	glSvc := gl.NewService(gl.NewRepository(scn))
	svc := NewService(NewRepository(scn)).
		WithCapitalizer(&txDurableWAC{onHand: onHand}).
		WithGLPoster(realVoidGL{svc: glSvc}).
		withCaller(globalCaller)
	return svc, scn
}

// voidGLBlock holds every doc's header row FOR UPDATE on its own single-connection pool, the row every Void
// locks after its transaction opens. The returned func releases it (idempotent).
func voidGLBlock(t *testing.T, url string, docIDs []string) func() {
	t.Helper()
	bcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	bcfg.MaxConns = 1
	bpool, err := pgxpool.NewWithConfig(context.Background(), bcfg)
	if err != nil {
		t.Fatalf("blocker pool: %v", err)
	}
	t.Cleanup(bpool.Close)
	btx, err := bpool.Begin(context.Background())
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = btx.Rollback(context.Background())
		}
	}
	t.Cleanup(release)
	if _, err := btx.Exec(context.Background(), `SELECT 1 FROM landed_costs WHERE id = ANY($1::uuid[]) FOR UPDATE`, docIDs); err != nil {
		t.Fatalf("blocker lock: %v", err)
	}
	return release
}

// voidGLParked waits until voidPoolSize scenario sessions are parked on the blocker's lock, or the overlap
// window ends.
func voidGLParked(t *testing.T, monitor *pgxpool.Pool) int {
	t.Helper()
	parked := 0
	for until := time.Now().Add(voidOverlapWindow); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		if err := monitor.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			 WHERE application_name = $1 AND wait_event_type = 'Lock'`, voidScenarioApp).Scan(&parked); err != nil {
			t.Fatalf("monitor: %v", err)
		}
		if parked >= voidPoolSize {
			break
		}
	}
	return parked
}

// voidGLVerdict turns per-caller results into the leg's failure: a deadline hit while the leg's own deadline
// expired is the STALL banner; any other error prints itself, never the banner.
func voidGLVerdict(t *testing.T, ctx context.Context, parked int, labels []string, errs []error, elapsed []time.Duration) {
	t.Helper()
	var stalled, failed []string
	for i := range errs {
		line := fmt.Sprintf("%s: %v after %s", labels[i], errs[i], elapsed[i].Round(time.Millisecond))
		switch {
		case errs[i] == nil:
		case errors.Is(errs[i], context.DeadlineExceeded) && ctx.Err() != nil:
			stalled = append(stalled, line)
		default:
			failed = append(failed, line)
		}
	}
	if len(failed) > 0 {
		t.Fatalf("a caller failed for a reason other than a stall:\n%s", strings.Join(failed, "\n"))
	}
	if len(stalled) > 0 {
		t.Fatalf("STALL: %d of %d callers hit the %s deadline at pool size %d "+
			"(connection starvation: each Void holds a connection and waits for a second from the same pool; "+
			"%d callers parked on the lock when it was released)\n%s",
			len(stalled), len(errs), voidStallDeadline, voidPoolSize, parked, strings.Join(stalled, "\n"))
	}
}

// LEG 1 · POOL 4. Four concurrent Voids of distinct POSTED landed costs, the real gl seam, overlap forced:
// the blocker holds every doc's header row until all four callers are parked inside their transactions, then
// releases. On the unfixed tree each caller then asks the same pool for a second connection for the reversing
// journal and all four starve; on the fixed tree the journal rides each void's own transaction and all four
// finish with every doc VOIDED and exactly one reversing journal each.
func TestIntegration_VoidGL_PoolFourConcurrentVoidsDoNotStarve(t *testing.T) {
	url := voidGLURL(t)
	helper := intPool(t)
	seedPO(t, helper)
	hgl := gl.NewService(gl.NewRepository(helper)) // setup-only gl over the helper pool

	docIDs := make([]string, voidPoolSize)
	products := make([]string, voidPoolSize)
	for i := 0; i < voidPoolSize; i++ {
		docIDs[i], products[i] = voidGLDoc(t, helper, hgl, i+1)
	}
	svc, _ := voidGLScenario(t, url, products)
	release := voidGLBlock(t, url, docIDs)

	ctx, cancel := context.WithTimeout(context.Background(), voidStallDeadline)
	defer cancel()
	start := time.Now()
	labels := make([]string, voidPoolSize)
	errs := make([]error, voidPoolSize)
	elapsed := make([]time.Duration, voidPoolSize)
	var wg sync.WaitGroup
	for i := 0; i < voidPoolSize; i++ {
		labels[i] = fmt.Sprintf("void doc %d", i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t0 := time.Now()
			errs[i] = svc.Void(ctx, docIDs[i])
			elapsed[i] = time.Since(t0)
		}(i)
	}
	parked := voidGLParked(t, helper)
	release()
	wg.Wait()
	voidGLVerdict(t, ctx, parked, labels, errs, elapsed)
	t.Logf("all %d voids finished in %s with %d parked on the lock", voidPoolSize, time.Since(start).Round(time.Millisecond), parked)

	// Every doc VOIDED with exactly one reversing journal each, read through the helper pool.
	mon := NewService(NewRepository(helper)).withCaller(globalCaller)
	for i, id := range docIDs {
		got, err := mon.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("read doc %d: %v", i, err)
		}
		if got.Status != StatusVoided || got.GLJournalID == "" {
			t.Fatalf("doc %d = status %s journal %q, want VOIDED with a journal", i, got.Status, got.GLJournalID)
		}
		if n := voidGLJournalCount(t, helper, "landedcost.void:"+id); n != 1 {
			t.Fatalf("doc %d: %d reversing journals, want 1", i, n)
		}
	}
}

// LEG 2 · ATOMICITY. A forced failure of the void's COMMIT (a deferred constraint trigger scoped to this one
// doc, firing exactly when the VOIDED flip tries to commit) must leave NO journal under landedcost.void:<id>
// and the doc not VOIDED; a retry after the refusal is lifted then completes with exactly one journal. On the
// unfixed tree the journal committed in its own transaction and outlives the rolled-back void: an ORPHAN
// JOURNAL for a doc whose reversal never happened.
func TestIntegration_VoidGL_JournalRollsBackWithTheVoid(t *testing.T) {
	url := voidGLURL(t)
	helper := intPool(t)
	seedPO(t, helper)
	hgl := gl.NewService(gl.NewRepository(helper))

	docID, product := voidGLDoc(t, helper, hgl, voidPoolSize+1) // a product of its own
	svc, _ := voidGLScenario(t, url, []string{product})
	key := "landedcost.void:" + docID

	fn := "lc_voidgl_refuse_" + strings.ReplaceAll(docID[:8], "-", "")
	mustExec := func(sql string) {
		t.Helper()
		if _, err := helper.Exec(context.Background(), sql); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	mustExec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $f$
		BEGIN RAISE EXCEPTION 'leg: refuse the void commit'; END $f$`, fn))
	mustExec(fmt.Sprintf(`CREATE CONSTRAINT TRIGGER %s AFTER UPDATE ON landed_costs
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
		WHEN (NEW.id = '%s' AND NEW.status = 'VOIDED') EXECUTE FUNCTION %s()`, fn, docID, fn))
	armed := true
	disarm := func() {
		if armed {
			armed = false
			_, _ = helper.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON landed_costs`, fn))
			_, _ = helper.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
		}
	}
	t.Cleanup(disarm)

	if err := svc.Void(context.Background(), docID); err == nil {
		t.Fatal("Void must fail when the void's commit is refused")
	} else if !strings.Contains(err.Error(), "refuse the void commit") {
		t.Fatalf("Void failed for a different reason than the forced commit refusal: %v", err)
	}
	mon := NewService(NewRepository(helper)).withCaller(globalCaller)
	got, err := mon.Get(context.Background(), docID)
	if err != nil {
		t.Fatalf("read doc: %v", err)
	}
	if got.Status == StatusVoided {
		t.Fatalf("after a refused commit the doc is VOIDED, want %s", got.Status)
	}
	if n := voidGLJournalCount(t, helper, key); n != 0 {
		t.Fatalf("ORPHAN JOURNAL: %d journal(s) remain under %s after the void's commit was refused; "+
			"the journal committed in its own transaction and outlived the void", n, key)
	}

	// The success path: the same doc voids once the refusal is lifted, with exactly one journal.
	disarm()
	if err := svc.Void(context.Background(), docID); err != nil {
		t.Fatalf("void after the refusal is lifted: %v", err)
	}
	got, err = mon.Get(context.Background(), docID)
	if err != nil {
		t.Fatalf("read doc after retry: %v", err)
	}
	if got.Status != StatusVoided || got.GLJournalID == "" {
		t.Fatalf("after retry: status %s journal %q, want VOIDED with a journal", got.Status, got.GLJournalID)
	}
	if n := voidGLJournalCount(t, helper, key); n != 1 {
		t.Fatalf("%d journals under %s, want exactly 1", n, key)
	}
	var viaKey string
	if err := helper.QueryRow(context.Background(), `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&viaKey); err != nil {
		t.Fatalf("journal by key: %v", err)
	}
	if viaKey != got.GLJournalID {
		t.Fatalf("doc points at journal %s but the key holds %s", got.GLJournalID, viaKey)
	}
}
