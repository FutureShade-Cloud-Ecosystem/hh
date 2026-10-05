//go:build integration

package pos

// return_requests_pool_integration_test.go, the return-request decision
// surface on the small-pool rig (the trenton world, the exact 10.00 anchor
// and the ring helpers are returns_trenton_integration_test.go's):
// pool-size concurrent decisions over an explicitly built four-connection
// pool. ApproveReturnRequest and RejectReturnRequest each open ONE decision
// transaction and hold it; when the work inside that transaction asks the
// SHARED POOL for a second connection, pool-size concurrent decisions each
// hold one connection and wait for a connection the pool can never give:
// connection starvation by second acquisition, no row lock need be
// involved, Postgres sees only idle sessions no lock is waited on. On a
// core where the return rail's transactional body and the rail-key probe
// ride the caller's transaction, every decision runs to commit on the one
// connection it opened, in milliseconds.
//
// The overlap is FORCED, not hoped for: a blocker transaction on a SEPARATE
// helper pool holds FOR UPDATE every request row until pg_stat_activity
// shows every contender session waiting on those locks, which is the
// deterministic proof each contender is inside its decision transaction
// holding exactly one scenario connection; only then does the blocker
// commit and the second acquisitions collide. The scenario pool stamps its
// sessions with an application_name, so the stall fingerprint (every
// contender session idle in transaction, no wait event, transaction open)
// is what turns the shared bound into the STALL verdict; any other failure
// prints the contenders' own errors and never the banner.
//
// Run (own throwaway container, another unused port, removed afterwards):
//
//	docker run --name <container> -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=<database> \
//	  -p <port>:5432 -d postgres:16-alpine
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:<port>/<database>?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:<port>/<database>?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:<port>/<database>?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_ReturnRequests -p 1 -v

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rreqPoolSize is the scenario pool's connection budget AND the contender
// count, one literal on purpose: the stall needs every connection held
// while each holder asks the pool for one more.
const rreqPoolSize = 4

// rreqStallBound bounds the concurrent phase after the blocker lets go. On
// the starved tree the contenders burn the whole bound waiting for a second
// connection; a healthy tree finishes in milliseconds.
const rreqStallBound = 10 * time.Second

// rreqScenarioPool builds the contenders' pool explicitly: DATABASE_URL
// decides the server (the leg skips without one, it never falls back to a
// default port or database), MaxConns pins the budget the contender count
// derives from, and the application_name marks exactly these sessions so
// pg_stat_activity can fingerprint the stall. The leg's OTHER connections
// (the blocker, the fingerprints) come from rreqHelperPool, never from
// here: a helper on the scenario pool would eat one of the four connections
// the contenders need.
func rreqScenarioPool(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if !set || url == "" {
		t.Skip("the pool-size decision legs need DATABASE_URL at a throwaway container")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.MaxConns = int32(rreqPoolSize)
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("scenario pool not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// rreqHelperPool is the leg's own second pool over the same DSN: the
// blocker transaction and the pg_stat_activity reads ride here, never on
// the scenario pool, so no helper ever eats one of the contender
// connections.
func rreqHelperPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if !set || url == "" {
		t.Skip("the pool-size decision legs need DATABASE_URL at a throwaway container")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("helper pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("helper pool not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// rreqStaffCtx stamps the decision caller the way the crash-window leg
// does: a real seeded user id (decided_by is FK-bound to users) carrying
// the global staff authority, which reaches every branch by the same rule
// the guards apply.
func rreqStaffCtx(t *testing.T, helper *pgxpool.Pool) context.Context {
	t.Helper()
	var userID string
	if err := helper.QueryRow(context.Background(), `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	return identity.ContextWithStaff(identity.ContextWithStaffUserID(context.Background(), userID), identity.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
}

// rreqPlantRequest creates one PENDING portal request on one sale through
// the real service seam: a request is intent only, no money, no stock. The
// request wants a slice of the sale's one line, so the approval rail has
// room to run when it is finally allowed to.
func rreqPlantRequest(t *testing.T, svc *Service, helper *pgxpool.Pool, sctx context.Context, orderID string) string {
	t.Helper()
	var lineID, custID string
	if err := helper.QueryRow(context.Background(),
		`SELECT ol.id::text, o.customer_id::text
		   FROM order_lines ol JOIN orders o ON o.id = ol.order_id
		  WHERE o.id::text = $1 ORDER BY ol.line_no LIMIT 1`, orderID).Scan(&lineID, &custID); err != nil {
		t.Fatalf("read the sale's line and customer: %v", err)
	}
	rr, err := svc.CreateReturnRequest(sctx, CreateReturnRequestInput{
		CustomerID: custID, OrderID: orderID, ReasonCode: ReasonDamaged, Note: "pool-size decision leg",
		Lines: []ReturnRequestLineInput{{OrderLineID: lineID, QtyBase: 10}},
	})
	if err != nil {
		t.Fatalf("create request on %s: %v", orderID, err)
	}
	return rr.ID
}

// rreqBarrierWait polls pg_stat_activity until want contender sessions sit
// waiting on a lock: with the blocker holding every request row, that is
// the deterministic proof all contenders are inside their decision
// transactions, one connection held each, before any second acquisition.
// ok false means the overlap never formed within the poll's bound.
func rreqBarrierWait(helper *pgxpool.Pool, appName string, want int) bool {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		if err := helper.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE application_name = $1 AND wait_event_type = 'Lock'`, appName).Scan(&n); err != nil {
			return false
		}
		if n >= want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// rreqStallFingerprint reads the stall's Postgres-side evidence: every
// contender session idle in transaction, its transaction open, waiting on
// NO lock. That is connection starvation by second acquisition: the
// sessions hold their decision transactions and wait client-side on the
// pool (Postgres files them under ClientRead, waiting for the next
// statement that never comes), where the forced overlap had them waiting
// under Lock on the blocker's row locks. ok false means the bound was hit
// some other way and the caller reports the contenders' own errors instead.
func rreqStallFingerprint(helper *pgxpool.Pool, appName string, want int) (ok bool, evidence string, seen int) {
	rows, err := helper.Query(context.Background(),
		`SELECT state, COALESCE(wait_event_type,''), xact_start IS NOT NULL
		   FROM pg_stat_activity WHERE application_name = $1`, appName)
	if err != nil {
		return false, fmt.Sprintf("pg_stat_activity read failed: %v", err), 0
	}
	defer rows.Close()
	total, starved := 0, 0
	for rows.Next() {
		var state, waitEvent string
		var inTx bool
		if err := rows.Scan(&state, &waitEvent, &inTx); err != nil {
			return false, fmt.Sprintf("pg_stat_activity scan failed: %v", err), total
		}
		total++
		if state == "idle in transaction" && waitEvent != "Lock" && inTx {
			starved++
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Sprintf("pg_stat_activity walk failed: %v", err), total
	}
	return total == want && starved == want,
		fmt.Sprintf("%d of %d contender sessions idle in transaction waiting on no lock", starved, want),
		total
}

// rreqOutcome is one contender's answer: how long it ran, what came back.
type rreqOutcome struct {
	elapsed time.Duration
	err     error
}

func (o rreqOutcome) report() string {
	if o.err != nil {
		return fmt.Sprintf("ran %v, failed: %v", o.elapsed.Round(time.Millisecond), o.err)
	}
	return fmt.Sprintf("ran %v, committed", o.elapsed.Round(time.Millisecond))
}

// rreqForceAndRun plants the blocker over every request row, fires one
// contender per request, forces the overlap, releases the blocker and
// bounds the concurrent phase. parent is the callers' stamped context, the
// one the decision calls run under (the cancellable contender context
// derives from it, so a cancel never drops the staff stamp). On the
// starved tree it prints the STALL banner with per-caller timings and
// fails the leg; a different failure reports the contenders' own errors
// and never claims the stall. On a healthy tree every contender answers
// inside the bound and the leg's assertions run.
func rreqForceAndRun(t *testing.T, scenario, helper *pgxpool.Pool, appName string, parent context.Context, requestIDs []string, decide func(ctx context.Context, requestID string) error) []rreqOutcome {
	t.Helper()

	// The blocker holds FOR UPDATE on every request row from a SEPARATE
	// helper connection: every contender opens its decision transaction and
	// parks on its own row, one scenario connection held each, before any
	// second acquisition can happen. The rollback guard makes every exit
	// path below release the blocker's connection BEFORE the test
	// framework's pool cleanups run: a Fatal with the blocker tx still open
	// would deadlock pool.Close on its checked-out connection.
	blockerCtx, blockerCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer blockerCancel()
	blocker, err := helper.Begin(blockerCtx)
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	blockerDone := false
	defer func() {
		if !blockerDone {
			_ = blocker.Rollback(context.Background())
		}
	}()
	if _, err := blocker.Exec(blockerCtx,
		`SELECT id FROM return_requests WHERE id::text = ANY($1) FOR UPDATE`, requestIDs); err != nil {
		t.Fatalf("blocker lock: %v", err)
	}

	// Pool-size contenders, one request each, on the callers' stamped
	// context.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	outcomes := make([]rreqOutcome, len(requestIDs))
	var wg sync.WaitGroup
	for i, id := range requestIDs {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			start := time.Now()
			err := decide(ctx, id)
			outcomes[i] = rreqOutcome{elapsed: time.Since(start), err: err}
		}(i, id)
	}

	if !rreqBarrierWait(helper, appName, len(requestIDs)) {
		cancel()
		wg.Wait()
		diagnosis := make([]string, len(outcomes))
		for i, o := range outcomes {
			diagnosis[i] = fmt.Sprintf("contender %d (request %s): %s", i, requestIDs[i], o.report())
		}
		t.Fatalf("could not force the overlap: no contender reached the request-row locks within the barrier bound, the contenders' own answers: %s",
			strings.Join(diagnosis, "; "))
	}
	if got := scenario.Stat().TotalConns(); got != int32(rreqPoolSize) {
		t.Fatalf("the forced overlap must park every scenario connection, the pool holds %d", got)
	}

	if err := blocker.Commit(blockerCtx); err != nil {
		cancel()
		wg.Wait()
		t.Fatalf("blocker commit: %v", err)
	}
	blockerDone = true
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return outcomes
	case <-time.After(rreqStallBound):
		stalled, evidence, seen := rreqStallFingerprint(helper, appName, len(requestIDs))
		cancel()
		<-done
		report := make([]string, len(outcomes))
		for i, o := range outcomes {
			report[i] = fmt.Sprintf("contender %d (request %s): %s", i, requestIDs[i], o.report())
		}
		if !stalled {
			t.Fatalf("the concurrent phase hit the %v bound without the stall fingerprint (%d contender sessions seen, %s), the contenders' own failures: %s",
				rreqStallBound, seen, evidence, strings.Join(report, "; "))
		}
		t.Logf("STALL: %d pool-size concurrent decisions each hold their decision transaction and wait for a second connection the pool of %d can never give", len(requestIDs), rreqPoolSize)
		t.Logf("STALL: fingerprint: %s", evidence)
		for _, line := range report {
			t.Logf("STALL: %s", line)
		}
		t.Fatalf("STALL: connection starvation by second acquisition, pool of %d, %d concurrent decisions: %s", rreqPoolSize, len(requestIDs), evidence)
	}
	return outcomes
}

// Pool-size concurrent ApproveReturnRequest on DISTINCT pending requests
// (real store, real return rail, real ACCOUNT_CREDIT rail): each decision
// opens its transaction, takes the request row's FOR UPDATE and then calls
// the return rail, which opens its OWN transaction on the same pool while
// the first is held. Every holder wants a second connection, the pool of
// four has none, the phase stalls to the bound. On the fixed core the
// rail's transactional body rides the decision transaction, every request
// lands APPROVED with exactly one committed return each.
func TestIntegration_ReturnRequests_ApprovePoolSizeConcurrent(t *testing.T) {
	scenario := rreqScenarioPool(t, "rreq_approve_contenders")
	helper := rreqHelperPool(t)
	s := newTrentonStackOnPool(t, scenario)
	sctx := rreqStaffCtx(t, helper)

	requestIDs := make([]string, rreqPoolSize)
	for i := range requestIDs {
		sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{30}, TenderCash)
		requestIDs[i] = rreqPlantRequest(t, s.svc, helper, sctx, sale.OrderID)
	}

	outcomes := rreqForceAndRun(t, scenario, helper, "rreq_approve_contenders", sctx, requestIDs,
		func(ctx context.Context, requestID string) error {
			_, err := s.svc.ApproveReturnRequest(ctx, requestID)
			return err
		})

	// Every request APPROVED with its return linked and exactly one
	// committed return per reserved key: the rail ran once per request, on
	// the decision transaction, no second refund anywhere.
	keys := make([]string, len(requestIDs))
	for i, id := range requestIDs {
		if err := outcomes[i].err; err != nil {
			t.Fatalf("approve %d (%s) must commit when the rail rides the decision transaction: %v", i, id, err)
		}
		keys[i] = reservedRequestKeyPrefix + id
		var status, returnID string
		if err := helper.QueryRow(context.Background(),
			`SELECT status, COALESCE(return_id::text,'') FROM return_requests WHERE id::text = $1`, id).
			Scan(&status, &returnID); err != nil {
			t.Fatalf("read request %s: %v", id, err)
		}
		if status != RequestApproved || returnID == "" {
			t.Fatalf("request %s must land APPROVED with its return linked, got %s / %q", id, status, returnID)
		}
	}
	var total, distinct int
	if err := helper.QueryRow(context.Background(),
		`SELECT count(*), count(DISTINCT id) FROM pos_returns WHERE idempotency_key = ANY($1)`, keys).
		Scan(&total, &distinct); err != nil {
		t.Fatalf("read the committed returns: %v", err)
	}
	if total != rreqPoolSize || distinct != rreqPoolSize {
		t.Fatalf("each approval commits exactly one return, got %d rows over %d distinct keys", total, rreqPoolSize)
	}
	// The money trail: one credit memo per sale, the ACCOUNT_CREDIT rail ran
	// once per approval and no further.
	for _, id := range requestIDs {
		var memos int
		if err := helper.QueryRow(context.Background(),
			`SELECT count(*) FROM customer_transactions ct
			   JOIN return_requests rq ON rq.order_id = ct.source_ref_id
			  WHERE rq.id::text = $1 AND ct.source_type = 'pos_return'`, id).Scan(&memos); err != nil {
			t.Fatalf("read the credit memos of %s: %v", id, err)
		}
		if memos != 1 {
			t.Fatalf("approval of %s must book exactly one credit memo, got %d", id, memos)
		}
	}
}

// Pool-size concurrent RejectReturnRequest on DISTINCT pending requests:
// each decision opens its transaction, takes the request row's FOR UPDATE
// and then probes the rail's reserved idempotency key, which on the starved
// tree reads through the POOL while the decision transaction holds its
// connection. Every holder wants a second connection, the pool of four has
// none, the phase stalls to the bound. On the fixed core the probe rides
// the decision transaction, every request lands REJECTED with its reason.
func TestIntegration_ReturnRequests_RejectPoolSizeConcurrent(t *testing.T) {
	scenario := rreqScenarioPool(t, "rreq_reject_contenders")
	helper := rreqHelperPool(t)
	s := newTrentonStackOnPool(t, scenario)
	sctx := rreqStaffCtx(t, helper)

	requestIDs := make([]string, rreqPoolSize)
	for i := range requestIDs {
		sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{30}, TenderCash)
		requestIDs[i] = rreqPlantRequest(t, s.svc, helper, sctx, sale.OrderID)
	}

	outcomes := rreqForceAndRun(t, scenario, helper, "rreq_reject_contenders", sctx, requestIDs,
		func(ctx context.Context, requestID string) error {
			_, err := s.svc.RejectReturnRequest(ctx, requestID, "pool-size decision leg: refused at the desk")
			return err
		})

	// Every request REJECTED with its reason recorded, and not one return
	// row or credit memo under any reserved key: a rejection moves nothing.
	keys := make([]string, len(requestIDs))
	for i, id := range requestIDs {
		if err := outcomes[i].err; err != nil {
			t.Fatalf("reject %d (%s) must commit when the probe rides the decision transaction: %v", i, id, err)
		}
		keys[i] = reservedRequestKeyPrefix + id
		var status, reason string
		if err := helper.QueryRow(context.Background(),
			`SELECT status, decision_reason FROM return_requests WHERE id::text = $1`, id).
			Scan(&status, &reason); err != nil {
			t.Fatalf("read request %s: %v", id, err)
		}
		if status != RequestRejected || reason == "" {
			t.Fatalf("request %s must land REJECTED with its reason, got %s / %q", id, status, reason)
		}
	}
	var returns int
	if err := helper.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_returns WHERE idempotency_key = ANY($1)`, keys).Scan(&returns); err != nil {
		t.Fatalf("read the returns: %v", err)
	}
	if returns != 0 {
		t.Fatalf("a rejected request moves no money, got %d return rows under the reserved keys", returns)
	}
}

// Approve then reject racing on ONE request: the request row's FOR UPDATE
// serializes the decisions, so exactly one lands, the loser refuses with
// the 409, and the money matches the decision that landed, never both.
// The race holds on the starved tree and on the fixed core alike: the fix
// changes what rides the decision transaction, never the serialization.
func TestIntegration_ReturnRequests_ApproveRejectRace(t *testing.T) {
	scenario := rreqScenarioPool(t, "rreq_race_contenders")
	helper := rreqHelperPool(t)
	s := newTrentonStackOnPool(t, scenario)
	sctx := rreqStaffCtx(t, helper)

	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{30}, TenderCash)
	requestID := rreqPlantRequest(t, s.svc, helper, sctx, sale.OrderID)

	// Both decision callers run under the same stamped context, the race
	// decides who lands.
	ctx, cancel := context.WithTimeout(sctx, 30*time.Second)
	defer cancel()
	type verdict struct {
		err     error
		elapsed time.Duration
	}
	var approveV, rejectV verdict
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		start := time.Now()
		_, approveV.err = s.svc.ApproveReturnRequest(ctx, requestID)
		approveV.elapsed = time.Since(start)
	}()
	go func() {
		defer wg.Done()
		start := time.Now()
		_, rejectV.err = s.svc.RejectReturnRequest(ctx, requestID, "pool-size race leg: refused at the desk")
		rejectV.elapsed = time.Since(start)
	}()
	wg.Wait()

	decisions := 0
	if approveV.err == nil {
		decisions++
	} else if st, ok := ClientStatus(approveV.err); !ok || st != 409 {
		t.Fatalf("a losing approve must refuse with the 409, got %v", approveV.err)
	}
	if rejectV.err == nil {
		decisions++
	} else if st, ok := ClientStatus(rejectV.err); !ok || st != 409 {
		t.Fatalf("a losing reject must refuse with the 409, got %v", rejectV.err)
	}
	if decisions != 1 {
		t.Fatalf("exactly one decision must land on a raced request, approve err: %v (%v), reject err: %v (%v)",
			approveV.err, approveV.elapsed, rejectV.err, rejectV.elapsed)
	}

	var status, reason, returnID string
	if err := helper.QueryRow(context.Background(),
		`SELECT status, decision_reason, COALESCE(return_id::text,'') FROM return_requests WHERE id::text = $1`, requestID).
		Scan(&status, &reason, &returnID); err != nil {
		t.Fatalf("read request %s: %v", requestID, err)
	}
	var returns int
	if err := helper.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_returns WHERE idempotency_key = $1`, reservedRequestKeyPrefix+requestID).
		Scan(&returns); err != nil {
		t.Fatalf("read the returns: %v", err)
	}
	switch status {
	case RequestApproved:
		if returnID == "" || returns != 1 {
			t.Fatalf("an approved request carries its one return, got return %q and %d return rows", returnID, returns)
		}
	case RequestRejected:
		if reason == "" || returns != 0 {
			t.Fatalf("a rejected request carries its reason and no money, got reason %q and %d return rows", reason, returns)
		}
	default:
		t.Fatalf("the raced request must land decided, got %s", status)
	}
}
