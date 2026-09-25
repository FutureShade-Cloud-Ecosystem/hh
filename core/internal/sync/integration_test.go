//go:build integration

// integration_test.go: the pick_tasks pilot against a real database, the
// contract battery over the tasks module's own tables (work item #165's
// acceptance), the operator walkthrough scripted step by step, and the
// org isolation and audit legs. Nothing here fakes the module whose
// correctness the acceptance depends on: the Store under test is the
// tasks SyncStore, the desk edits run through the real tasks service,
// and the fixtures are the kit's synthetic rows.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync165?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/sync/...
//
// The database is a throwaway scratch (created empty, the frozen
// migration set applied by the core's migrate role); never a shared
// proof database.
package sync_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// intOrderReader adapts the order module's read seam for the adapter,
// the same mapping the composition root's adapter carries.
type intOrderReader struct{ repo *order.Repository }

func (a intOrderReader) OrderBrief(ctx context.Context, orderID string) (tasks.OrderBrief, error) {
	b, err := a.repo.OrderBrief(ctx, orderID)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			return tasks.OrderBrief{}, tasks.ErrUnknownOrder
		}
		return tasks.OrderBrief{}, err
	}
	return tasks.OrderBrief{ID: b.ID, BranchID: b.BranchID, Fulfillment: b.Fulfillment, Status: b.Status}, nil
}

// intPool opens the scratch database, politely skipping when the gate
// variable is unset, and builds the kit fixtures.
func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync165?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := testkit.Build(context.Background(), testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM tasks WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_sequences WHERE scope = 'pick_tasks'`)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_type = 'sync.pick_tasks'`)
		_ = testkit.Cleanup(ctx, testkit.PgxPool(pool))
	})
	return pool
}

// pilotFixture is the pilot wired exactly as the composition root wires
// it: the tasks SyncStore over the pool, the engine with the real audit
// seam, and the desk hook through the real tasks service.
type pilotFixture struct {
	pool   *pgxpool.Pool
	store  *tasks.SyncStore
	svc    *tasks.Service
	engine *sync.Engine
	info   sync.ScopeInfo
	filter sync.ScopeFilter
}

func newPilot(t *testing.T) *pilotFixture {
	t.Helper()
	pool := intPool(t)
	reader := intOrderReader{repo: order.NewRepository(pool)}
	linker := linkedUser(pool)
	store := tasks.NewSyncStore(pool, reader, linker)
	svc := tasks.NewService(tasks.NewRepository(pool)).WithOrderReader(reader)
	engine := sync.NewEngine().WithAudit(intAudit{svc: audit.NewService(audit.NewRepository(pool), nil)}, nil)
	if err := engine.RegisterScope("pick_tasks", store); err != nil {
		t.Fatalf("register pick_tasks: %v", err)
	}
	info, ok := sync.Lookup("pick_tasks")
	if !ok {
		t.Fatalf("pick_tasks missing from the registry")
	}
	return &pilotFixture{
		pool: pool, store: store, svc: svc, engine: engine, info: info,
		filter: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales},
	}
}

// linkedUser resolves the ERP user id for a platform subject over the
// users table (the fixture rows carry the platform links).
func linkedUser(pool *pgxpool.Pool) func(ctx context.Context, platformUserID string) (string, bool) {
	return func(ctx context.Context, platformUserID string) (string, bool) {
		var id string
		err := pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE platform_user_id = $1 AND deleted_at IS NULL`,
			platformUserID).Scan(&id)
		if err != nil {
			return "", false
		}
		return id, true
	}
}

// intAudit maps the engine's entries onto the audit module, the same
// mapping cmd/core's syncAuditSeam carries.
type intAudit struct{ svc *audit.Service }

func (a intAudit) RecordSync(ctx context.Context, e sync.AuditEntry) error {
	return a.svc.Record(ctx, audit.SensitiveAccess{
		Action:     e.Action,
		EntityType: "sync." + e.Scope,
		EntityID:   e.EntityID,
		ActorID:    "",
		Extra: map[string]any{
			"scope": e.Scope, "op": e.Op, "client_id": e.ClientID, "status": e.Outcome,
		},
	})
}

// The kit the battery runs with: creates are born against the fixture's
// alpha picking order in the north org; updates walk the pick state
// machine one legal step; the desk moves the row through the real
// service's own transition path.
func pilotKit(f *pilotFixture) harness.Kit {
	return harness.Kit{
		Scope:  f.info,
		Filter: f.filter,
		Store:  f.store,
		Create: func(id string) map[string]any {
			fields := map[string]any{"order_id": testkit.OrderAlphaPickID}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			to, ok := pickNextStatus(fmt.Sprint(row["status"]))
			if !ok {
				// Terminal or unknown: a stamp-only update the module accepts.
				return map[string]any{"id": row["id"], "stage_location": "FX Bay"}
			}
			return map[string]any{"id": row["id"], "status": to}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			status := fmt.Sprint(row["status"])
			for i := 0; i < n; i++ {
				to, ok := pickNextStatus(status)
				if !ok {
					// Terminal: the rest of the chain carries stamp-only
					// updates, which the module accepts repeatedly.
					out = append(out, map[string]any{"id": row["id"], "stage_location": fmt.Sprintf("FX Bay %d", i)})
					continue
				}
				status = to
				out = append(out, map[string]any{"id": row["id"], "status": to})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			id := fmt.Sprint(row["id"])
			var from string
			if err := f.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id = $1::uuid`, id).Scan(&from); err != nil {
				return err
			}
			to, ok := pickNextStatus(from)
			if !ok {
				// Terminal: the desk stamps a field and draws a seq from
				// the scope's own allocator, the same upsert every tasks
				// write path runs.
				var seq int64
				if err := f.pool.QueryRow(ctx,
					`INSERT INTO scope_sequences (scope) VALUES ('pick_tasks')
					 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
					 RETURNING next_value`).Scan(&seq); err != nil {
					return err
				}
				_, err := f.pool.Exec(ctx,
					`UPDATE tasks SET stage_location = 'FX Desk', updated_at = now(), seq = $2 WHERE id = $1::uuid`, id, seq)
				return err
			}
			actor := testkit.UserInsideSalesID
			switch to {
			case tasks.StatusAccepted:
				_, err := f.svc.Claim(ctx, id, actor)
				return err
			case tasks.StatusInProgress:
				_, err := f.svc.Accept(ctx, id, actor)
				return err
			case tasks.StatusStaged:
				_, err := f.store.Write(ctx, f.filter, id, "00000000-0000-4000-8000-0000000000de", map[string]any{"id": id, "status": to})
				return err
			case tasks.StatusDone:
				_, err := f.svc.Done(ctx, id, actor, "")
				return err
			}
			return nil
		},
	}
}

// pickNextStatus is the pick state machine's forward walk, the same
// matrix the module enforces (QUEUED to ACCEPTED to IN_PROGRESS to
// STAGED to DONE).
func pickNextStatus(from string) (string, bool) {
	next := map[string]string{
		"QUEUED": tasks.StatusAccepted, "PUSHED": tasks.StatusAccepted,
		"ACCEPTED": tasks.StatusInProgress, "IN_PROGRESS": tasks.StatusStaged,
		"STAGED": tasks.StatusDone,
	}
	to, ok := next[from]
	return to, ok
}

// TestPilotContractBattery: the per-scope battery over the real tables,
// GATE-7 and the replay property included.
func TestPilotContractBattery(t *testing.T) {
	f := newPilot(t)
	k := pilotKit(f)
	harness.Contract(t, f.engine, k)
	harness.Gate7(t, f.engine, k)
	// The scaffold's rng is the standard library's, seeded fixed so a
	// failure reproduces.
	rng := rand.New(rand.NewSource(165))
	harness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestPilotWalkthrough: the operator walkthrough, scripted against the
// seeded database exactly as the acceptance words it: a yard
// supervisor's pick list pulled to a device, a task completed offline,
// the outbox replayed, a second replay changing nothing, and a
// hand-edited server row being what the client is told to show. Each
// step logs its outcome so the PR body carries the transcript.
func TestPilotWalkthrough(t *testing.T) {
	f := newPilot(t)
	ctx := context.Background()

	// Step 1: the desk queues a pick task against the alpha picking
	// order, through the module's own REST service path.
	task, err := f.svc.CreateFromOrder(ctx, tasks.CreateInput{
		OrderID:   testkit.OrderAlphaPickID,
		Source:    tasks.SourceManual,
		CreatedBy: testkit.UserInsideSalesID,
		OrgID:     testkit.OrgNorthID,
	})
	if err != nil {
		t.Fatalf("desk create: %v", err)
	}
	t.Logf("walkthrough 1: desk queued pick task %s on order %s, status %s", task.ID, testkit.OrderAlphaPickID, task.Status)
	if task.Status != tasks.StatusQueued {
		t.Fatalf("desk create left the task at %s", task.Status)
	}

	// Step 2: the device pulls the pick list.
	pull, err := f.engine.Pull(ctx, f.info, f.filter, 0, 50)
	if err != nil {
		t.Fatalf("device pull: %v", err)
	}
	if len(pull.Items) != 1 || pull.Items[0].Id != task.ID {
		t.Fatalf("device pull: %+v", pull.Items)
	}
	deviceRow := pull.Items[0]
	t.Logf("walkthrough 2: device pulled %d pick task(s), row id %s seq %d status %v",
		len(pull.Items), deviceRow.Id, deviceRow.Seq, deviceRow.Data["status"])
	baseSeq := deviceRow.Seq

	// Step 3: the device works offline: claim, start, finish, as one
	// outbox replayed in order when signal returns.
	outbox := []syncOp{
		{client: newKey(1), kind: "update", payload: map[string]any{"id": task.ID, "status": tasks.StatusAccepted}},
		{client: newKey(2), kind: "update", payload: map[string]any{"id": task.ID, "status": tasks.StatusInProgress}},
		{client: newKey(3), kind: "update", payload: map[string]any{"id": task.ID, "status": tasks.StatusDone, "photo_url": "fx://done/proof"}},
	}
	pushed := pushOps(t, f, outbox)
	for i, res := range pushed {
		if res.Status != "applied" {
			t.Fatalf("walkthrough 3: op %d (%s) answered %+v err %s", i, outbox[i].kind, res, *res.Error)
		}
		t.Logf("walkthrough 3: op %d (%s to %v) applied, seq %d", i, outbox[i].kind, outbox[i].payload["status"], *res.Seq)
	}
	row, ok, err := f.store.Load(ctx, f.filter, task.ID)
	if err != nil || !ok || row.Data["status"] != tasks.StatusDone {
		t.Fatalf("walkthrough 3: final state %+v ok=%v err=%v", row.Data, ok, err)
	}

	// Step 4: a second replay of the same outbox changes nothing.
	before := snap(t, f)
	pushed2 := pushOps(t, f, outbox)
	for i, res := range pushed2 {
		if res.Status != "applied" {
			t.Fatalf("walkthrough 4: replay op %d answered %+v", i, res)
		}
	}
	assertEq(t, "walkthrough 4", before, snap(t, f))
	t.Logf("walkthrough 4: second replay applied %d ops through dedup, server state unchanged (high-water mark stands at %v)", len(pushed2), before["last_seq"])

	// Step 5: a hand-edited server row is what the client is told to
	// show. A second task is queued, pulled by the device, then moved at
	// the desk (the service's own claim path, seq bumping); the device's
	// stale completion meets the moved row and server_wins answers with
	// the server row.
	task2, err := f.svc.CreateFromOrder(ctx, tasks.CreateInput{
		OrderID:   testkit.OrderAlphaReadyID,
		Source:    tasks.SourceManual,
		CreatedBy: testkit.UserInsideSalesID,
		OrgID:     testkit.OrgNorthID,
	})
	if err != nil {
		t.Fatalf("desk create 2: %v", err)
	}
	pull2, err := f.engine.Pull(ctx, f.info, f.filter, baseSeq, 50)
	if err != nil {
		t.Fatalf("device pull 2: %v", err)
	}
	var device2 apigen.SyncEntity
	for _, it := range pull2.Items {
		if it.Id == task2.ID {
			device2 = it
		}
	}
	if device2.Id == "" {
		t.Fatalf("device never saw the second task: %+v", pull2.Items)
	}
	stale := device2.Seq
	claimed, err := f.svc.Claim(ctx, task2.ID, testkit.UserInsideSalesID)
	if err != nil {
		t.Fatalf("desk claim: %v", err)
	}
	t.Logf("walkthrough 5: desk claimed task %s while the device was offline, status %s", claimed.ID, claimed.Status)
	res := pushOps(t, f, []syncOp{
		{client: newKey(4), kind: "update", payload: map[string]any{"id": task2.ID, "status": tasks.StatusDone}, base: &stale},
	})
	if len(res) != 1 || res[0].Status != "conflict" {
		t.Fatalf("walkthrough 5: stale completion answered %+v", res[0])
	}
	c := res[0].Conflict
	if c.Policy != string(sync.PolicyServerWins) || c.Applied == nil || *c.Applied {
		t.Fatalf("walkthrough 5: envelope %+v", c)
	}
	if c.ServerState["status"] != tasks.StatusAccepted {
		t.Fatalf("walkthrough 5: the client was not told to show the server row: %+v", c.ServerState)
	}
	t.Logf("walkthrough 5: stale completion answered conflict (server_wins), the client is told to show the server row (status %v)",
		c.ServerState["status"])
}

// TestPilotOrgIsolationAndTenantClosed: rows of another org never pull
// through the north org's filter, and a tenant filter answers nothing
// through the pilot scope (the adapter fails closed; the route's 403 is
// the unit suite's leg).
func TestPilotOrgIsolationAndTenantClosed(t *testing.T) {
	f := newPilot(t)
	ctx := context.Background()
	north, err := f.svc.CreateFromOrder(ctx, tasks.CreateInput{
		OrderID: testkit.OrderAlphaPickID, Source: tasks.SourceManual,
		CreatedBy: testkit.UserInsideSalesID, OrgID: testkit.OrgNorthID,
	})
	if err != nil {
		t.Fatalf("north create: %v", err)
	}
	south, err := f.svc.CreateFromOrder(ctx, tasks.CreateInput{
		OrderID: testkit.OrderGammaDraftID, Source: tasks.SourceManual,
		CreatedBy: testkit.UserInsideSalesID, OrgID: testkit.OrgSouthID,
	})
	if err != nil {
		t.Fatalf("south create: %v", err)
	}
	pull, err := f.engine.Pull(ctx, f.info, f.filter, 0, 50)
	if err != nil {
		t.Fatalf("north pull: %v", err)
	}
	for _, it := range pull.Items {
		if it.Id == south.ID {
			t.Fatalf("the north org's pull saw the south org's task")
		}
	}
	found := false
	for _, it := range pull.Items {
		if it.Id == north.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the north org's pull missed its own task")
	}
	southFilter := sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubInsideSales}
	southPull, err := f.engine.Pull(ctx, f.info, southFilter, 0, 50)
	if err != nil {
		t.Fatalf("south pull: %v", err)
	}
	if len(southPull.Items) != 1 || southPull.Items[0].Id != south.ID {
		t.Fatalf("the south org's pull: %+v", southPull.Items)
	}
	// The tenant filter through the pilot scope answers nothing.
	tenantFilter := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha}
	tenantPull, err := f.engine.Pull(ctx, f.info, tenantFilter, 0, 50)
	if err != nil {
		t.Fatalf("tenant pull: %v", err)
	}
	if len(tenantPull.Items) != 0 || len(tenantPull.Tombstones) != 0 {
		t.Fatalf("a tenant filter read org rows through the pilot scope: %+v", tenantPull)
	}
}

// TestPilotModuleInvariantsHold: the adapter enforces the module's own
// state machine through sync: an illegal jump is that operation's
// rejected result, and a create against an unknown order refuses.
func TestPilotModuleInvariantsHold(t *testing.T) {
	f := newPilot(t)
	ctx := context.Background()
	task, err := f.svc.CreateFromOrder(ctx, tasks.CreateInput{
		OrderID: testkit.OrderAlphaPickID, Source: tasks.SourceManual,
		CreatedBy: testkit.UserInsideSalesID, OrgID: testkit.OrgNorthID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res := pushOps(t, f, []syncOp{
		{client: newKey(1), kind: "update", payload: map[string]any{"id": task.ID, "status": tasks.StatusDone}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("illegal jump answered %+v", res[0])
	}
	t.Logf("module invariant: QUEUED to DONE through sync answered rejected (%s)", *res[0].Error)
	res = pushOps(t, f, []syncOp{
		{client: newKey(2), kind: "create", payload: map[string]any{"order_id": "3ef10000-0000-4000-8000-0f0000000002"}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("unknown order create answered %+v", res[0])
	}
	// A payload outside the writable set refuses whole.
	res = pushOps(t, f, []syncOp{
		{client: newKey(3), kind: "update", payload: map[string]any{"id": task.ID, "priority": "URGENT"}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("unwritable field answered %+v", res[0])
	}
}

// TestPilotAuditRows: every push operation writes its audit row through
// the real audit service.
func TestPilotAuditRows(t *testing.T) {
	f := newPilot(t)
	ctx := context.Background()
	pushOps(t, f, []syncOp{
		{client: newKey(1), kind: "create", payload: map[string]any{"order_id": testkit.OrderAlphaPickID}},
	})
	var n int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE entity_type = 'sync.pick_tasks' AND action = 'SYNC_PUSH'`).Scan(&n); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if n < 1 {
		t.Fatalf("no SYNC_PUSH audit rows landed")
	}
	t.Logf("audit: %d SYNC_PUSH rows on the pilot scope", n)
}

// The small helpers the walkthrough uses, named for readability.

type syncOp struct {
	client  string
	kind    string
	payload map[string]any
	base    *int64
}

func newKey(n int) string { return fmt.Sprintf("30000000-0000-4000-8000-%012d", n) }

func pushOps(t *testing.T, f *pilotFixture, ops []syncOp) []apigen.SyncPushResult {
	t.Helper()
	out := make([]apigen.SyncPushOperation, 0, len(ops))
	for _, o := range ops {
		out = append(out, apigen.SyncPushOperation{ClientId: o.client, Op: o.kind, Payload: o.payload, BaseSeq: o.base})
	}
	resp := f.engine.Push(context.Background(), f.info, f.filter, out)
	return resp.Results
}

// snap takes the scope's snapshot (the walkthrough's state compare).
func snap(t *testing.T, f *pilotFixture) map[string]any {
	t.Helper()
	resp, err := f.engine.Pull(context.Background(), f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("snap pull: %v", err)
	}
	state := map[string]any{"last_seq": resp.LastSeq}
	for _, it := range resp.Items {
		state[it.Id] = fmt.Sprintf("%d|%v", it.Seq, it.Data["status"])
	}
	for _, tb := range resp.Tombstones {
		state[tb.Id] = fmt.Sprintf("tombstone|%d", tb.Seq)
	}
	return state
}

// assertEq compares two snapshots.
func assertEq(t *testing.T, name string, want, got map[string]any) {
	t.Helper()
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Fatalf("%s: server state changed on replay: %v, want %v", name, got, want)
	}
}
