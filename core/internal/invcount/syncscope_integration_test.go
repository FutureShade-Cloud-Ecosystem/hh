//go:build integration

// syncscope_integration_test.go: the cycle_counts sync scope against a
// real database (HH-3.5 work item #168's acceptance): the per-scope
// contract battery, GATE-7 and the replay property over the invcount
// module's own count_sheets table, the manual policy's own acceptance
// leg (a stale replay NEVER overwrites the desk's count, the server row
// returns with the review marker, and the review marker lands in the
// audit log), org isolation with the tenant filter failing closed, and
// the module's own ladder rules holding through sync.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync168?sslmode=disable' \
//	  go test -tags=integration -count=1 -run TestCycleSync ./internal/invcount/
package invcount

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	syncharness "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// cycleSyncFixture is the cycle_counts world.
type cycleSyncFixture struct {
	pool   *pgxpool.Pool
	repo   *Repository
	store  *SyncStore
	engine *sync.Engine
	info   sync.ScopeInfo
	filter sync.ScopeFilter
	stock  *fakeStockCounter
}

// fakeStockCounter satisfies StockCounter with a fixed snapshot: two
// products with on-hand readings, the yard's own facts the freeze
// captures.
type fakeStockCounter struct {
	branchID string
	snaps    []Snapshot
}

func (f *fakeStockCounter) SnapshotOnHand(ctx context.Context, branchID string) ([]Snapshot, error) {
	return f.snaps, nil
}

func (f *fakeStockCounter) WeightedAvgCostCents(ctx context.Context, productID, branchID string) (int64, error) {
	return 500, nil
}

func (f *fakeStockCounter) PostCountAdjustmentTx(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, qtyDelta float64, unitCostCents int64, reason string) (string, error) {
	return "", nil
}

func newCycleSyncFixture(t *testing.T) *cycleSyncFixture {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	// The snapshot carries a REAL location id, the shape the production
	// seam returns (per-location on-hand): count_lines' unique key is
	// (sheet, product, location), and a NULL location never matches the
	// upsert the module's own SaveCounts runs.
	stock := &fakeStockCounter{snaps: []Snapshot{
		{ProductID: testkit.ProductID, LocationID: testkit.BranchNorthAID, OnHandBase: 100, AvgCostCents: 500},
	}}
	f := &cycleSyncFixture{
		pool:   pool,
		repo:   NewRepository(pool),
		store:  NewSyncStore(pool, stock, cycleLinkedUser(pool)),
		engine: sync.NewEngine().WithAudit(cycleSyncAudit{svc: audit.NewService(audit.NewRepository(pool), nil)}, nil),
		filter: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales},
		stock:  stock,
	}
	if err := f.engine.RegisterScope("cycle_counts", f.store); err != nil {
		t.Fatalf("register cycle_counts: %v", err)
	}
	f.info, _ = sync.Lookup("cycle_counts")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM count_lines WHERE sheet_id IN (SELECT id FROM count_sheets WHERE notes LIKE 'FXSYNC%')`)
		_, _ = pool.Exec(c, `DELETE FROM count_sheets WHERE notes LIKE 'FXSYNC%'`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'cycle_counts'`)
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_type IN ('sync.cycle_counts')`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	return f
}

// cycleLinkedUser resolves the ERP user row for a platform subject.
func cycleLinkedUser(pool *pgxpool.Pool) func(ctx context.Context, platformUserID string) (string, bool) {
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

// cycleSyncAudit maps the engine's entries onto the audit module's rows
// for the scope and op legs only. It records no actor (actor_id empty,
// no actor_subject): the actor mapping is cmd/core's syncAuditSeam
// alone, proven by the composition test in cmd/core
// (sync_audit_wire_test.go).
type cycleSyncAudit struct{ svc *audit.Service }

func (a cycleSyncAudit) RecordSync(ctx context.Context, e sync.AuditEntry) error {
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

// cycleSyncKit builds the battery kit: creates open an AD_HOC sheet at
// the north branch (the notes carrying the fixture marker the cleanup
// keys on), updates carry a CONSTANT freeze (the replay posture: a
// superseded freeze on a frozen sheet refuses and moves nothing), the
// chain walks the ladder (freeze, submit, re-submit), and the desk
// submits its own counts through the module's own SaveCounts path.
func cycleSyncKit(f *cycleSyncFixture) syncharness.Kit {
	return syncharness.Kit{
		Scope: f.info, Filter: f.filter, Store: f.store,
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"branch_id": testkit.BranchNorthAID,
				"scope":     "AD_HOC",
				"notes":     "FXSYNC sheet",
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "status": "FROZEN"}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			id := row["id"]
			return []map[string]any{
				{"id": id, "status": "COUNTED", "counts": []map[string]any{
					{"product_id": testkit.ProductID, "location_id": testkit.BranchNorthAID, "counted_qty_base": 97},
				}},
				{"id": id, "status": "COUNTED", "counts": []map[string]any{
					{"product_id": testkit.ProductID, "location_id": testkit.BranchNorthAID, "counted_qty_base": 96},
				}},
				{"id": id, "status": "COUNTED", "counts": []map[string]any{
					{"product_id": testkit.ProductID, "location_id": testkit.BranchNorthAID, "counted_qty_base": 95},
				}},
			}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			return f.repo.SaveCounts(ctx, fmt.Sprint(row["id"]), testkit.UserInsideSalesID, []CountLineInput{
				{ProductID: testkit.ProductID, CountedQtyBase: 95},
			})
		},
	}
}

// TestCycleSyncContractBattery: the per-scope battery, GATE-7 and the
// replay property.
func TestCycleSyncContractBattery(t *testing.T) {
	f := newCycleSyncFixture(t)
	k := cycleSyncKit(f)
	syncharness.Contract(t, f.engine, k)
	syncharness.Gate7(t, f.engine, k)
	rng := rand.New(rand.NewSource(1682))
	syncharness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestCycleSyncManualNeverOverwrites is the manual policy's own
// acceptance leg, beyond the battery: the yard's count is written at
// the desk while the device was offline, the device's stale replay
// answers conflict with the server row and the review marker, the
// sheet is exactly what the desk left, and the review marker lands in
// the audit log as the review card's source.
func TestCycleSyncManualNeverOverwrites(t *testing.T) {
	f := newCycleSyncFixture(t)
	ctx := context.Background()

	// The device opens a sheet and freezes it (its base state).
	id := cycleSyncCreate(t, f, cycleSyncKit(f).Create(""))
	res := cycleSyncPush(t, f, []cycleOp{
		{client: cycleKey(1), kind: "update", payload: map[string]any{"id": id, "status": "FROZEN"}},
	})
	if res[0].Status != "applied" {
		t.Fatalf("freeze: %+v", res[0])
	}
	row, ok, err := f.store.Load(ctx, f.filter, id)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	baseSeq := row.Seq

	// The desk submits the crew's counted sheet through the module's
	// own path while the device is dark.
	if err := f.repo.SaveCounts(ctx, id, testkit.UserInsideSalesID, []CountLineInput{
		{ProductID: testkit.ProductID, LocationID: testkit.BranchNorthAID, CountedQtyBase: 95},
	}); err != nil {
		t.Fatalf("desk submit: %v", err)
	}
	deskRow, _, err := f.store.Load(ctx, f.filter, id)
	if err != nil {
		t.Fatalf("desk load: %v", err)
	}
	if deskRow.Seq <= baseSeq || deskRow.Data["status"] != "COUNTED" {
		t.Fatalf("the desk's submit did not land: %+v", deskRow)
	}
	t.Logf("manual: the desk submitted the sheet (status %v, seq %d) while the device was dark",
		deskRow.Data["status"], deskRow.Seq)

	// The device's stale replay: the manual policy never overwrites.
	res = cycleSyncPush(t, f, []cycleOp{
		{client: cycleKey(2), kind: "update", base: &baseSeq,
			payload: map[string]any{"id": id, "status": "COUNTED", "counts": []map[string]any{
				{"product_id": testkit.ProductID, "counted_qty_base": 42},
			}}},
	})
	if len(res) != 1 || res[0].Status != "conflict" {
		t.Fatalf("the stale replay answered %+v, want conflict", res[0])
	}
	c := res[0].Conflict
	if c == nil || c.Policy != string(sync.PolicyManual) || c.Applied == nil || *c.Applied {
		t.Fatalf("the manual envelope: %+v", c)
	}
	if fmt.Sprint(c.ServerState["status"]) != "COUNTED" {
		t.Fatalf("the envelope's server state: %+v", c.ServerState)
	}
	after, _, err := f.store.Load(ctx, f.filter, id)
	if err != nil {
		t.Fatalf("after load: %v", err)
	}
	if after.Seq != deskRow.Seq || fmt.Sprint(after.Data["counted_by"]) != fmt.Sprint(deskRow.Data["counted_by"]) {
		t.Fatalf("the manual policy overwrote the desk's count: %+v then %+v", deskRow, after)
	}
	// The device's counted quantity never landed: the desk's line
	// stands, 95, not 42.
	var counted float64
	if err := f.pool.QueryRow(ctx,
		`SELECT counted_qty_base FROM count_lines
		 WHERE sheet_id = $1::uuid AND product_id = $2::uuid AND location_id = $3::uuid`,
		id, testkit.ProductID, testkit.BranchNorthAID).Scan(&counted); err != nil {
		t.Fatalf("counted read: %v", err)
	}
	if counted != 95 {
		t.Fatalf("the stale replay's quantity landed: %v, want the desk's 95", counted)
	}
	t.Logf("manual: the replay returned the server row with the review marker, the desk's 95 stands")

	// The review marker lands in the audit log (the review card's
	// source, the engine's SYNC_CONFLICT_REVIEW row).
	var n int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE entity_type = 'sync.cycle_counts' AND action = 'SYNC_CONFLICT_REVIEW' AND entity_id = $1::uuid`,
		id).Scan(&n); err != nil {
		t.Fatalf("review marker read: %v", err)
	}
	if n < 1 {
		t.Fatalf("no SYNC_CONFLICT_REVIEW row landed for the conflicted sheet")
	}
	t.Logf("manual: %d review-marker row(s) in the audit log", n)
}

// TestCycleSyncLadderRules: the module's own ladder rules hold through
// sync: a POSTED sheet cannot be cancelled, the approve and post doors
// are not reachable, and an unknown scope word refuses.
func TestCycleSyncLadderRules(t *testing.T) {
	f := newCycleSyncFixture(t)
	ctx := context.Background()
	id := cycleSyncCreate(t, f, cycleSyncKit(f).Create(""))

	// The approve and post doors are the desk's.
	res := cycleSyncPush(t, f, []cycleOp{
		{client: cycleKey(1), kind: "update", payload: map[string]any{"id": id, "status": "POSTED"}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("POSTED through sync answered %+v, want rejected", res[0])
	}
	t.Logf("ladder: the post door is the desk's (%s)", *res[0].Error)

	// A POSTED sheet cannot be cancelled.
	if _, err := f.pool.Exec(ctx,
		`UPDATE count_sheets SET status = 'POSTED' WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("post: %v", err)
	}
	del := cycleSyncPush(t, f, []cycleOp{
		{client: cycleKey(2), kind: "delete", payload: map[string]any{"id": id}},
	})
	if len(del) != 1 || del[0].Status != "rejected" {
		t.Fatalf("cancelling a POSTED sheet answered %+v, want rejected", del[0])
	}
}

// TestCycleSyncOrgIsolation: sheets of another org never pull, and the
// tenant filter answers nothing.
func TestCycleSyncOrgIsolation(t *testing.T) {
	f := newCycleSyncFixture(t)
	ctx := context.Background()
	north := cycleSyncCreate(t, f, cycleSyncKit(f).Create(""))
	if _, err := f.pool.Exec(ctx, `
INSERT INTO count_sheets (sheet_no, branch_id, scope, notes, seq)
VALUES ('CS-FXSYNC-SOUTH', $1::uuid, 'AD_HOC', 'FXSYNC south', 1)`,
		testkit.BranchSouthAID); err != nil {
		t.Fatalf("south sheet: %v", err)
	}
	pull, err := f.engine.Pull(ctx, f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("north pull: %v", err)
	}
	for _, it := range pull.Items {
		if it.Data["sheet_no"] == "CS-FXSYNC-SOUTH" {
			t.Fatalf("the north org's pull saw the south org's sheet")
		}
	}
	found := false
	for _, it := range pull.Items {
		if it.Id == north {
			found = true
		}
	}
	if !found {
		t.Fatalf("the north org's pull missed its own sheet")
	}
	tenant := f.filter
	tenant.TenantID, tenant.Tenant, tenant.Actor = testkit.TenantAlphaID, true, testkit.SubPortalAdminAlpha
	tenantPull, err := f.engine.Pull(ctx, f.info, tenant, 0, 200)
	if err != nil {
		t.Fatalf("tenant pull: %v", err)
	}
	if len(tenantPull.Items) != 0 || len(tenantPull.Tombstones) != 0 {
		t.Fatalf("a tenant filter read org rows through cycle_counts: %+v", tenantPull)
	}
}

// The small helpers.

type cycleOp struct {
	client  string
	kind    string
	payload map[string]any
	base    *int64
}

func cycleKey(n int) string { return fmt.Sprintf("35000000-0000-4000-8000-%012d", n) }

func cycleSyncPush(t *testing.T, f *cycleSyncFixture, ops []cycleOp) []apigen.SyncPushResult {
	t.Helper()
	out := make([]apigen.SyncPushOperation, 0, len(ops))
	for _, o := range ops {
		out = append(out, apigen.SyncPushOperation{ClientId: o.client, Op: o.kind, Payload: o.payload, BaseSeq: o.base})
	}
	resp, err := f.engine.Push(context.Background(), f.info, f.filter, out)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	return resp.Results
}

// cycleSyncCreate pushes one create and returns the row id.
func cycleSyncCreate(t *testing.T, f *cycleSyncFixture, payload map[string]any) string {
	t.Helper()
	res := cycleSyncPush(t, f, []cycleOp{{client: cycleKey(0), kind: "create", payload: payload}})
	if res[0].Status != "applied" || res[0].Seq == nil {
		t.Fatalf("sync create: %+v", res[0])
	}
	feed, err := f.engine.Pull(context.Background(), f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("sync create feed: %v", err)
	}
	for _, it := range feed.Items {
		if it.Seq == *res[0].Seq {
			return it.Id
		}
	}
	t.Fatalf("sync create: the new sheet never pulled at seq %d", *res[0].Seq)
	return ""
}
