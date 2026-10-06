//go:build integration

// postbill_gl_tx_integration_test.go, the legs for PostBill's GL seams riding the bill's own transaction.
//
// THE DEFECT CLASS. PostBill holds a pool connection (the bill transaction, from Begin to Commit) and, while it
// holds it, calls the GL seams: one to four ResolveRule reads (each a fresh pool query) and PostJournal, whose
// InsertJournal opens a second WHOLE transaction on the same pool. With the pool at four connections and four
// bills posting together, each caller holds one connection and waits for a fifth that never comes. Postgres sees
// nothing wrong (the sessions sit idle in transaction, no lock is waited on for the starvation itself), so the
// process stalls until each action's timeout. Money stays correct (everything rolls back); availability does not.
//
// A SECOND DEFECT in the same ordering: the journal commits in its own transaction BEFORE the bill's commit, so
// a failure after the post (SetBillPosted, the commit itself) leaves a POSTED journal for a bill that stayed
// DRAFT. The fix posts the journal through the bill's transaction, so the two commit or roll back together.
//
// Unlike postbill_pool_integration_test.go (fake GL seams, isolating the purchasing reads), these legs use the
// REAL gl.Service and gl.Repository, built on the SAME pool as purchasing, so the starvation is not hidden by a
// fake. Helper connections (blocker, monitor, setup) come from other pools and never eat a scenario connection.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'PostBillGL' ./internal/purchasing/
package purchasing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

const postBillGLApp = "postbill_gl_tx_scenario"

// glTxURL is the leg's database url; a leg never runs against a default database.
func glTxURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	return url
}

// glTxBills receives a PO in full and enters n DRAFT bills of ten units each against it.
func glTxBills(t *testing.T, h *harness, tag string, n int) (poID string, billIDs []string) {
	t.Helper()
	product := h.newProduct(t, tag)
	po := h.orderedPO(t, product, float64(n*10), 2500)
	lineID := po.Lines[0].ID
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: float64(n * 10)}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	for i := 0; i < n; i++ {
		b, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
			BillNo: billNo(fmt.Sprintf("%s%d", tag, i)), POID: po.ID,
			Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 10, UnitCostCents: 2500}},
		})
		if err != nil {
			t.Fatalf("create bill %d: %v", i, err)
		}
		billIDs = append(billIDs, b.ID)
	}
	t.Cleanup(func() {
		for _, id := range billIDs {
			glTxDropJournal(h, "purchasing.bill:"+id)
		}
	})
	return po.ID, billIDs
}

// glTxDropJournal removes a leg's journal by key, best effort (a journal that is still referenced stays).
func glTxDropJournal(h *harness, key string) {
	ctx := context.Background()
	_, _ = h.pool.Exec(ctx, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, key)
	_, _ = h.pool.Exec(ctx, `DELETE FROM gl_journals WHERE idempotency_key = $1`, key)
}

// glTxScenario builds the scenario pool at poolSize and a purchasing service whose GL seams are the REAL gl
// service over that same pool (the pool the starvation is about).
func glTxScenario(t *testing.T, h *harness, url string) (*Service, *gl.Service) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.MaxConns = int32(poolSize)
	cfg.ConnConfig.RuntimeParams["application_name"] = postBillGLApp
	scn, err := pgxpool.NewWithConfig(h.ctx, cfg)
	if err != nil {
		t.Fatalf("scenario pool: %v", err)
	}
	t.Cleanup(scn.Close)
	glSvc := gl.NewService(gl.NewRepository(scn))
	svc := NewService(NewRepository(scn)).
		WithGLPoster(itGL{svc: glSvc}).
		WithRuleResolver(itRules{svc: glSvc}).
		withCaller(ctxCaller(h.user))
	return svc, glSvc
}

// glTxBlock holds the PO's lines FOR UPDATE on its own single-connection pool, the row every PostBill locks
// after its transaction opens. The returned func releases it (idempotent).
func glTxBlock(t *testing.T, h *harness, url, poID string) func() {
	t.Helper()
	bcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	bcfg.MaxConns = 1
	bpool, err := pgxpool.NewWithConfig(h.ctx, bcfg)
	if err != nil {
		t.Fatalf("blocker pool: %v", err)
	}
	t.Cleanup(bpool.Close)
	btx, err := bpool.Begin(h.ctx)
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
	if _, err := btx.Exec(h.ctx, `SELECT 1 FROM purchasing_po_lines WHERE po_id = $1 FOR UPDATE`, poID); err != nil {
		t.Fatalf("blocker lock: %v", err)
	}
	return release
}

// glTxParked waits until poolSize scenario sessions are parked on the blocker's lock, or the overlap window ends.
func glTxParked(t *testing.T, h *harness) int {
	t.Helper()
	parked := 0
	for until := time.Now().Add(overlapWindow); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		if err := h.pool.QueryRow(h.ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE application_name = $1 AND wait_event_type = 'Lock'`, postBillGLApp).Scan(&parked); err != nil {
			t.Fatalf("monitor: %v", err)
		}
		if parked >= poolSize {
			break
		}
	}
	return parked
}

// glTxVerdict turns per-caller results into the leg's failure: a deadline hit while the leg's own deadline
// expired is the STALL banner; any other error prints itself, never the banner.
func glTxVerdict(t *testing.T, ctx context.Context, parked int, labels []string, errs []error, elapsed []time.Duration) {
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
			"(connection starvation: each PostBill holds a connection and waits for a second from the same pool; "+
			"%d callers parked on the lock when it was released)\n%s",
			len(stalled), len(errs), stallDeadline, poolSize, parked, strings.Join(stalled, "\n"))
	}
}

// LEG 1 · POOL 4. Four concurrent PostBills on distinct bills, the real gl seams, overlap forced.
func TestIntegration_PostBillGL_PoolFourConcurrentPostsDoNotStarve(t *testing.T) {
	url := glTxURL(t)
	h := newHarness(t)
	poID, billIDs := glTxBills(t, h, "gltxpool", poolSize)
	svc, _ := glTxScenario(t, h, url)
	release := glTxBlock(t, h, url, poID)

	ctx, cancel := context.WithTimeout(h.ctx, stallDeadline)
	defer cancel()
	start := time.Now()
	labels := make([]string, poolSize)
	errs := make([]error, poolSize)
	elapsed := make([]time.Duration, poolSize)
	var wg sync.WaitGroup
	for i := 0; i < poolSize; i++ {
		labels[i] = fmt.Sprintf("post bill %d", i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t0 := time.Now()
			_, errs[i] = svc.PostBill(ctx, billIDs[i], PostBillRequest{})
			elapsed[i] = time.Since(t0)
		}(i)
	}
	parked := glTxParked(t, h)
	release()
	wg.Wait()
	glTxVerdict(t, ctx, parked, labels, errs, elapsed)
	t.Logf("all %d posts finished in %s with %d parked on the lock", poolSize, time.Since(start).Round(time.Millisecond), parked)

	for i, id := range billIDs {
		b, err := h.svc.GetBill(h.ctx, id)
		if err != nil {
			t.Fatalf("read bill %d: %v", i, err)
		}
		if b.Status != BillPosted || b.GLJournalID == "" {
			t.Fatalf("bill %d = status %s journal %q, want POSTED with a journal", i, b.Status, b.GLJournalID)
		}
	}
}

// glTxJournalCount counts journals under one idempotency key.
func glTxJournalCount(t *testing.T, h *harness, key string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count journals: %v", err)
	}
	return n
}

// LEG 2 · ATOMICITY. A failure after the journal post (here the bill's COMMIT, forced by a deferred constraint
// trigger scoped to this one bill) must leave NO journal for the bill and the bill DRAFT; then the success path
// leaves exactly one journal with the legs the bill computed.
func TestIntegration_PostBillGL_JournalRollsBackWithTheBill(t *testing.T) {
	glTxURL(t)
	h := newHarness(t)
	_, billIDs := glTxBills(t, h, "gltxatom", 1)
	billID := billIDs[0]
	key := "purchasing.bill:" + billID

	fn := "pb_gltx_refuse_" + strings.ReplaceAll(billID[:8], "-", "")
	mustExec := func(sql string) {
		t.Helper()
		if _, err := h.pool.Exec(h.ctx, sql); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	mustExec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $f$
		BEGIN RAISE EXCEPTION 'leg: refuse the bill commit'; END $f$`, fn))
	mustExec(fmt.Sprintf(`CREATE CONSTRAINT TRIGGER %s AFTER UPDATE ON purchasing_bills
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
		WHEN (NEW.id = '%s' AND NEW.status = 'POSTED') EXECUTE FUNCTION %s()`, fn, billID, fn))
	armed := true
	disarm := func() {
		if armed {
			armed = false
			_, _ = h.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON purchasing_bills`, fn))
			_, _ = h.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
		}
	}
	t.Cleanup(disarm)

	if _, err := h.svc.PostBill(h.ctx, billID, PostBillRequest{}); err == nil {
		t.Fatal("PostBill must fail when the bill's commit is refused")
	} else if !strings.Contains(err.Error(), "refuse the bill commit") {
		t.Fatalf("PostBill failed for a different reason than the forced commit refusal: %v", err)
	}
	b, err := h.svc.GetBill(h.ctx, billID)
	if err != nil {
		t.Fatalf("read bill: %v", err)
	}
	if b.Status != BillDraft {
		t.Fatalf("after a refused commit the bill is %s, want DRAFT", b.Status)
	}
	if n := glTxJournalCount(t, h, key); n != 0 {
		t.Fatalf("ORPHAN JOURNAL: %d journal(s) remain under %s after the bill's commit was refused; "+
			"the journal committed in its own transaction and outlived the bill", n, key)
	}

	// The success path: the same bill posts once the refusal is lifted, with exactly one journal.
	disarm()
	posted, err := h.svc.PostBill(h.ctx, billID, PostBillRequest{})
	if err != nil {
		t.Fatalf("post after the refusal is lifted: %v", err)
	}
	if posted.Status != BillPosted || posted.GLJournalID == "" {
		t.Fatalf("posted bill = status %s journal %q", posted.Status, posted.GLJournalID)
	}
	if n := glTxJournalCount(t, h, key); n != 1 {
		t.Fatalf("%d journals under %s, want exactly 1", n, key)
	}
	legs := h.journalLegs(t, posted.GLJournalID)
	if len(legs) != 2 || legs["2150"] != [2]int64{25000, 0} || legs["2100"] != [2]int64{0, 25000} {
		t.Fatalf("journal legs = %v, want DR 2150 25000 and CR 2100 25000", legs)
	}
	var viaKey string
	if err := h.pool.QueryRow(h.ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&viaKey); err != nil {
		t.Fatalf("journal by key: %v", err)
	}
	if viaKey != posted.GLJournalID {
		t.Fatalf("bill points at journal %s but the key holds %s", posted.GLJournalID, viaKey)
	}
}

// oneShotOutbox is a notify.OutboxStore that hands its dispatcher exactly one event and records the
// settlement. It holds no pool connection, so the leg exercises the GL consumer's own posting path (the
// handler RegisterPostingHandlers installs) and not the claim transaction, which is a separate site.
type oneShotOutbox struct {
	ev  notify.Event
	mu  sync.Mutex
	got *notify.Settlement
}

func (o *oneShotOutbox) ProcessBatch(ctx context.Context, _ int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	s := settle(ctx, o.ev)
	o.mu.Lock()
	o.got = &s
	o.mu.Unlock()
	return 1, nil
}

// LEG 3 · LOCK ORDER. Concurrent PostBills against concurrent outbox journal posting (the gl consumer's real
// path: posting rule read, then InsertJournal) on the same pool. PostBill now holds the fiscal year's counter
// row (next_entry_no) until its own commit, so a poster queued behind it waits for the commit. The lock order
// is bill row, PO lines, counter row for PostBill and counter row alone for a poster, so no cycle exists; the
// leg proves it by finishing every caller inside the bound with every journal present.
func TestIntegration_PostBillGL_LockOrderAgainstOutboxPosting(t *testing.T) {
	url := glTxURL(t)
	h := newHarness(t)
	poID, billIDs := glTxBills(t, h, "gltxlock", poolSize)
	svc, glSvc := glTxScenario(t, h, url)

	const posters = poolSize
	sessions := make([]string, posters)
	outboxes := make([]*oneShotOutbox, posters)
	dispatchers := make([]*notify.Dispatcher, posters)
	for i := range sessions {
		if err := h.pool.QueryRow(h.ctx, `SELECT gen_random_uuid()::text`).Scan(&sessions[i]); err != nil {
			t.Fatalf("uuid: %v", err)
		}
		sid := sessions[i]
		t.Cleanup(func() { glTxDropJournal(h, "till.session.completed:"+sid) })
		outboxes[i] = &oneShotOutbox{ev: notify.Event{
			EventID: fmt.Sprintf("leg-evt-%d", i), Topic: "till.session.completed",
			Payload: []byte(fmt.Sprintf(`{"session_id":%q,"branch_id":%q,"over_short_cents":%d}`, sid, branchTRT, -500-i)),
		}}
		dispatchers[i] = notify.NewDispatcher(outboxes[i], nil)
		glSvc.RegisterPostingHandlers(dispatchers[i])
	}
	release := glTxBlock(t, h, url, poID)

	ctx, cancel := context.WithTimeout(h.ctx, stallDeadline)
	defer cancel()
	start := time.Now()
	total := poolSize + posters
	labels := make([]string, total)
	errs := make([]error, total)
	elapsed := make([]time.Duration, total)
	var wg sync.WaitGroup
	for i := 0; i < poolSize; i++ {
		labels[i] = fmt.Sprintf("post bill %d", i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t0 := time.Now()
			_, errs[i] = svc.PostBill(ctx, billIDs[i], PostBillRequest{})
			elapsed[i] = time.Since(t0)
		}(i)
	}
	for j := 0; j < posters; j++ {
		k := poolSize + j
		labels[k] = fmt.Sprintf("outbox post %d", j)
		wg.Add(1)
		go func(j, k int) {
			defer wg.Done()
			t0 := time.Now()
			dispatchers[j].DrainOnce(ctx)
			elapsed[k] = time.Since(t0)
			outboxes[j].mu.Lock()
			s := outboxes[j].got
			outboxes[j].mu.Unlock()
			switch {
			case s == nil:
				errs[k] = errors.New("the dispatcher settled nothing")
			case fmt.Sprint(s.Status) != "PUBLISHED":
				if ctx.Err() != nil {
					errs[k] = fmt.Errorf("%s: %w", s.LastError, context.DeadlineExceeded)
				} else {
					errs[k] = fmt.Errorf("settled %v: %s", s.Status, s.LastError)
				}
			}
		}(j, k)
	}
	parked := glTxParked(t, h)
	release()
	wg.Wait()
	glTxVerdict(t, ctx, parked, labels, errs, elapsed)
	t.Logf("%d posts and %d outbox postings finished in %s", poolSize, posters, time.Since(start).Round(time.Millisecond))

	for i, id := range billIDs {
		if n := glTxJournalCount(t, h, "purchasing.bill:"+id); n != 1 {
			t.Fatalf("bill %d: %d journals, want 1", i, n)
		}
	}
	for j, sid := range sessions {
		if n := glTxJournalCount(t, h, "till.session.completed:"+sid); n != 1 {
			t.Fatalf("outbox posting %d: %d journals, want 1", j, n)
		}
	}
}
