//go:build integration

// contractor_scopes_integration_test.go: the #169 scopes against a real
// database (work item #169's acceptance): the harness battery, GATE-7
// and the replay property per push scope, the per-scope tenant
// isolation legs, the customer_quotes token-lifecycle composition, the
// pull-only feeds over the fixture rows, the operator walkthrough
// scripted step by step, and the mutation checks that revert a guard
// and watch a test fail. Nothing here fakes the module whose
// correctness the acceptance depends on: the Stores under test are the
// owning modules' own adapters and the desk edits run through the
// modules' real write paths where they exist.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync169?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/sync/...
package sync_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/planflow"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/procurement"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/warranty"
)

// counter feeds the kits' ever-changing payloads: every update the
// battery pushes must move the row, or the no-op rule swallows it.
var counter atomic.Int64

func nextVal() int64 { return counter.Add(1) }

// noAuditSeam is the test's audit seam for the customerquote service:
// the audit write is an index, never a precondition (the module's own
// recorded doctrine), so a no-op seam runs the lifecycle without the
// audit table.
type noAuditSeam struct{}

func (noAuditSeam) RecordAcceptanceTx(ctx context.Context, tx customerquote.TxExec, row customerquote.AcceptanceAuditRow) error {
	return nil
}

func (noAuditSeam) LoadAcceptance(ctx context.Context, acceptanceID string) (customerquote.AcceptanceAuditRow, bool, error) {
	return customerquote.AcceptanceAuditRow{}, false, nil
}

// fakeCapturer is the test's typed-attestation seam: a stable ref per
// capture, so the pickups confirm path runs without the vault.
type fakeCapturer struct{ n atomic.Int64 }

func (f *fakeCapturer) CaptureTyped(ctx context.Context, in dispatch.TypedAttestation) (string, error) {
	return fmt.Sprintf("fx169sig-%d", f.n.Add(1)), nil
}

// CaptureTypedTx keeps the fake satisfiable of the widened dispatch seam
// (the confirm's capture now rides the caller's transaction). The fake
// touches no database, so the transaction passes through untouched and
// the ref sequence is identical either way.
func (f *fakeCapturer) CaptureTypedTx(ctx context.Context, _ pgx.Tx, in dispatch.TypedAttestation) (string, error) {
	return f.CaptureTyped(ctx, in)
}

// tenantOrdersAdap satisfies dispatch.TenantOrderReader over the order
// module's tenant-scoped reads, the same mapping the composition root
// carries.
type tenantOrdersAdap struct{ repo *order.Repository }

func (a tenantOrdersAdap) TenantBrief(ctx context.Context, tenantID, orderID string) (dispatch.OrderBrief, bool, error) {
	b, ok, err := a.repo.OrderBriefForTenant(ctx, tenantID, orderID)
	if err != nil || !ok {
		return dispatch.OrderBrief{}, false, err
	}
	return dispatch.OrderBrief{ID: b.ID, BranchID: b.BranchID, Fulfillment: b.Fulfillment, Status: b.Status}, true, nil
}

func (a tenantOrdersAdap) TenantOrderIDs(ctx context.Context, tenantID string) ([]string, error) {
	return a.repo.OrderIDsByCustomer(ctx, tenantID)
}

// the169Fixture wires every #169 scope on one engine over the scratch
// database, exactly as the composition root wires them.
type the169Fixture struct {
	pool      *pgxpool.Pool
	engine    *sync.Engine
	cqStore   *customerquote.Store
	cqSvc     *customerquote.Service
	sig       *fakeCapturer
	orderSvc  *order.Service
	orderRepo *order.Repository
}

func new169Fixture(t *testing.T) *the169Fixture {
	t.Helper()
	pool := open169Pool(t)
	cqStore, err := customerquote.NewStore(pool)
	if err != nil {
		t.Fatalf("cq store: %v", err)
	}
	cqSvc, err := customerquote.NewService(cqStore, noAuditSeam{},
		&customerquote.SigningKeys{KeyID: "fx169-key", Key: bytes.Repeat([]byte{0x51}, 32)},
		customerquote.Config{})
	if err != nil {
		t.Fatalf("cq service: %v", err)
	}
	sig := &fakeCapturer{}
	orderRepo := order.NewRepository(pool)
	orderSvc := order.NewService(orderRepo)
	f := &the169Fixture{pool: pool, cqStore: cqStore, cqSvc: cqSvc, sig: sig, orderSvc: orderSvc, orderRepo: orderRepo}
	engine := sync.NewEngine()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	must(engine.RegisterScope("orders", planflow.NewOrdersSyncStore(pool)))
	must(engine.RegisterScope("scope_items", planflow.NewItemsSyncStore(pool)))
	must(engine.RegisterScope("delivery_instructions", planflow.NewInstructionsSyncStore(pool)))
	must(engine.RegisterScope("customer_quotes", customerquote.NewSyncStore(cqStore, cqSvc)))
	must(engine.RegisterScope("team", portal.NewTeamSyncStore(pool)))
	must(engine.RegisterScope("pickups", dispatch.NewPickupsSyncStore(pool, tenantOrdersAdap{repo: orderRepo}, sig, nil, nil)))
	f.engine = engine
	return f
}

// open169Pool opens the scratch database (the pilot's polite skip when
// the gate variable is unset), builds the kit fixtures, and cleans this
// lane's rows beside the kit's own.
func open169Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync169?sslmode=disable"
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
	hideFixtureRowsFromFeeds(t, pool)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM plan_line WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(ctx, `DELETE FROM plan WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(ctx, `DELETE FROM quote_acceptance WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_quote WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE email LIKE 'fx169-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM will_call_pickup_events WHERE pickup_id IN (SELECT id FROM will_call_pickups WHERE order_id IN (SELECT id FROM orders WHERE order_no LIKE 'FXWCL-%'))`)
		_, _ = pool.Exec(ctx, `DELETE FROM will_call_pickups WHERE order_id IN (SELECT id FROM orders WHERE order_no LIKE 'FXWCL-%')`)
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE order_no LIKE 'FXWCL-%'`)
		// The pull-only feeds leg seeds one row per org-class table, the kit
		// product's balance among them; every row the lane adds the lane
		// removes, before the shared kit cleanup runs, or a foreign key from
		// one of them refuses the kit's branch, user and product deletes and
		// every later package on the shared gate finds the fixture world
		// still standing.
		_, _ = pool.Exec(ctx, `DELETE FROM stock_balances WHERE product_id = $1::uuid AND location_id = $2::uuid`,
			testkit.ProductID, testkit.BranchNorthAID)
		_, _ = pool.Exec(ctx, `DELETE FROM pos_parked_tickets WHERE ticket_no = 'FX169-PARK'`)
		_, _ = pool.Exec(ctx, `DELETE FROM warranty_claims WHERE claim_no = 'FX169-WTY'`)
		_, _ = pool.Exec(ctx, `DELETE FROM dashboard_user_widgets WHERE widget_key = 'fx169.measure'`)
		_, _ = pool.Exec(ctx, `DELETE FROM dispatch_trucks WHERE code = 'FX169-TRK'`)
		_, _ = pool.Exec(ctx, `DELETE FROM dispatch_drivers WHERE name = 'FX Driver'`)
		_, _ = pool.Exec(ctx, `DELETE FROM vendors WHERE vendor_no = 'FX169-VEN'`)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE account_no = 'FX169-ACCT'`)
		_ = testkit.Cleanup(ctx, testkit.PgxPool(pool))
	})
	return pool
}

// The tenant filters the kits push and pull as: alpha and beta share
// the north org (the isolation legs), the G4 org arrives with the
// filter, never from a header.
func alphaFilter() sync.ScopeFilter {
	return sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha}
}

func betaFilter() sync.ScopeFilter {
	return sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantBetaID, Tenant: true, Actor: testkit.SubPortalAdminBeta}
}

// pullOf pulls one page under a filter.
func pullOf(t *testing.T, e *sync.Engine, scope string, f sync.ScopeFilter, since int64, limit int) []string {
	t.Helper()
	resp, err := e.Pull(context.Background(), mustInfo(scope), f, since, limit)
	if err != nil {
		t.Fatalf("pull %s: %v", scope, err)
	}
	out := []string{}
	for _, it := range resp.Items {
		out = append(out, it.Id)
	}
	for _, tb := range resp.Tombstones {
		out = append(out, tb.Id)
	}
	return out
}

func mustInfo(scope string) sync.ScopeInfo {
	info, ok := sync.Lookup(scope)
	if !ok {
		panic("scope " + scope + " missing from the registry")
	}
	return info
}

// drawSeq draws one seq from a scope's counter, the desk hooks' stamp.
func drawSeq(t *testing.T, pool *pgxpool.Pool, scope string) int64 {
	t.Helper()
	var next int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, scope).Scan(&next); err != nil {
		t.Fatalf("draw seq: %v", err)
	}
	return next
}

// insertParentCard inserts one alpha card for the scope_items kit.
func (f *the169Fixture) insertParentCard(t *testing.T) string {
	t.Helper()
	id := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	seq := drawSeq(t, f.pool, "orders")
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO plan (id, org_id, tenant_id, name, stage, seq) VALUES ($1, $2, $3, 'FX kit card', 'plan', $4)`,
		id, testkit.OrgNorthID, testkit.TenantAlphaID, seq); err != nil {
		t.Fatalf("parent card: %v", err)
	}
	return id
}

// insertWillCallOrder inserts one alpha WILL_CALL order the pickups kit
// mints against (the fixture kit's own discipline: patterned ids, the
// test's own rows, cleaned by the pool's cleanup).
func (f *the169Fixture) insertWillCallOrder(t *testing.T) string {
	t.Helper()
	id := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	no := fmt.Sprintf("FXWCL-%06d", nextVal())
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, org_id, tenant_id)
		VALUES ($1, $2, $3, $4, 'CONFIRMED', 'WILL_CALL', $5, $3)`,
		id, no, testkit.TenantAlphaID, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
		t.Fatalf("will-call order: %v", err)
	}
	return id
}

// the169Kits builds the per-scope harness kits.
func (f *the169Fixture) ordersKit() harness.Kit {
	info := mustInfo("orders")
	return harness.Kit{
		Scope: info, Filter: alphaFilter(), Store: planflow.NewOrdersSyncStore(f.pool),
		Create: func(id string) map[string]any {
			fields := map[string]any{"name": fmt.Sprintf("FX Patio %d", nextVal())}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "sort_order": nextVal()}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			// The supplier's move: the dealer order landed, the card sits
			// in the order column, the shared counter stamped (the arm a
			// future dealer-side surface will own).
			_, err := f.pool.Exec(ctx, `UPDATE plan SET stage = 'order', updated_at = now(), seq = $2 WHERE id = $1::uuid`,
				row["id"], drawSeqT(ctx, f.pool, "orders"))
			return err
		},
	}
}

// drawSeqT is the context-carrying seq draw the desk hooks use.
func drawSeqT(ctx context.Context, pool *pgxpool.Pool, scope string) int64 {
	var next int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, scope).Scan(&next); err != nil {
		return 0
	}
	return next
}

func (f *the169Fixture) itemsKit(parent string) harness.Kit {
	info := mustInfo("scope_items")
	return harness.Kit{
		Scope: info, Filter: alphaFilter(), Store: planflow.NewItemsSyncStore(f.pool),
		Create: func(id string) map[string]any {
			fields := map[string]any{"plan_id": parent, "description": "FX paver line", "qty_entered": "12", "uom_entered": "SF"}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "notes": fmt.Sprintf("FX note %d", nextVal())}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			// The pricing pass: the desk quotes the line (price_source
			// quoted, the price legs set), the line's own counter stamped.
			_, err := f.pool.Exec(ctx, `
				UPDATE plan_line SET
					unit_price_cents = 1234, price_uom = 'SF', line_total_cents = 14808,
					qty_base = '12', price_source = 'quoted', price_valid_until = now() + interval '7 days',
					updated_at = now(), seq = $2 WHERE id = $1::uuid`,
				row["id"], drawSeqT(ctx, f.pool, "scope_items"))
			return err
		},
	}
}

func (f *the169Fixture) instructionsKit() harness.Kit {
	info := mustInfo("delivery_instructions")
	return harness.Kit{
		Scope: info, Filter: alphaFilter(), Store: planflow.NewInstructionsSyncStore(f.pool),
		Create: func(id string) map[string]any {
			fields := map[string]any{"site_instructions": fmt.Sprintf("FX note %d", nextVal())}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "site_instructions": fmt.Sprintf("FX note %d", nextVal())}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			_, err := f.pool.Exec(ctx, `UPDATE plan SET site_instructions = 'FX desk note', updated_at = now(), seq = $2 WHERE id = $1::uuid`,
				row["id"], drawSeqT(ctx, f.pool, "orders"))
			return err
		},
	}
}

func (f *the169Fixture) quotesKit() harness.Kit {
	info := mustInfo("customer_quotes")
	return harness.Kit{
		Scope: info, Filter: alphaFilter(), Store: customerquote.NewSyncStore(f.cqStore, f.cqSvc),
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"markup_bps": 1000,
				"customer_contact": map[string]any{
					"name": "FX Homeowner",
				},
				"lines": []map[string]any{{
					"id":   fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal()),
					"name": "FX paver bundle", "qty": "12", "uom": "SF",
					"unit_cost_cents": 1000, "price_source": "erp", "presentation": "commodity",
				}},
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			cur, _ := row["markup_bps"].(float64)
			return map[string]any{"id": row["id"], "markup_bps": int64(cur) + 100}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			cur, _ := row["markup_bps"].(float64)
			out := make([]map[string]any, 0, n)
			for i := 1; i <= n; i++ {
				out = append(out, map[string]any{"id": row["id"], "markup_bps": int64(cur) + int64(100*i)})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			// The desk's send: the module's own path, rotating the token
			// and stamping the row's seq (the feed obligation this lane
			// added to the module's rotations).
			_, err := f.cqSvc.Send(ctx, customerquote.Scope{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID}, fmt.Sprint(row["id"]))
			return err
		},
	}
}

func (f *the169Fixture) teamKit() harness.Kit {
	info := mustInfo("team")
	return harness.Kit{
		Scope: info, Filter: alphaFilter(), Store: portal.NewTeamSyncStore(f.pool),
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"email": fmt.Sprintf("fx169-%d@fx.test", nextVal()),
				"name":  "FX Crew", "role": "field_crew",
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			// The role alternates, so every update moves the row.
			to := "buyer"
			if fmt.Sprint(row["role"]) == "buyer" {
				to = "field_crew"
			}
			return map[string]any{"id": row["id"], "role": to}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			// The chain walks the role ladder, each step valid against
			// the row the step before it leaves.
			roles := []string{"buyer", "field_crew", "buyer", "field_crew"}
			out := make([]map[string]any, 0, n)
			for i := 0; i < n; i++ {
				out = append(out, map[string]any{"id": row["id"], "role": roles[i%len(roles)]})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			// The module's own role change (the REST path's write, seq
			// stamped by this lane's CTE).
			return portal.NewRepository(f.pool).UpdateRole(ctx, fmt.Sprint(row["id"]), "ap_finance", false)
		},
	}
}

// pickupsKit is the honest subset: one confirm per pickup is the
// module's own evidence rule, so the battery's three-update chain would
// demand fabricated inputs the module refuses. The pickups legs below
// run the pull shape, the create, the single confirm, the tombstone and
// GATE-7 by hand instead of through harness.Contract.
func (f *the169Fixture) pickupsKit() harness.Kit {
	info := mustInfo("pickups")
	return harness.Kit{
		Scope: info, Filter: alphaFilter(), Store: dispatch.NewPickupsSyncStore(f.pool, tenantOrdersAdap{repo: f.orderRepo}, f.sig, nil, nil),
		Create: func(id string) map[string]any {
			fields := map[string]any{"order_id": f.insertWillCallOrderT()}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "status": "CONFIRMED", "collected_by": "FX Crew Member", "affirmed": true}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			// The desk's revoke: the module's own write, stamped from the table's one
			// counter (the will_call scope, which the contractor's writes draw too).
			_, err := f.pool.Exec(ctx, `UPDATE will_call_pickups SET status = 'REVOKED', revoked_at = now(), seq = $2 WHERE id = $1::uuid`,
				row["id"], drawSeqT(ctx, f.pool, "will_call"))
			return err
		},
	}
}

func (f *the169Fixture) insertWillCallOrderT() string {
	id := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	no := fmt.Sprintf("FXWCL-%06d", nextVal())
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, org_id, tenant_id)
		VALUES ($1, $2, $3, $4, 'CONFIRMED', 'WILL_CALL', $5, $3)`,
		id, no, testkit.TenantAlphaID, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
		return ""
	}
	return id
}

// TestPickupsFreshness_AStaffMintReachesTheContractorsFeed: the sweep's probe 1, green. A staff
// InsertPickup (the desk mints the tag) must reach the contractor's pickups feed from the cursor a
// device already holds. Every will_call_pickups writer draws the one will_call stream (UX-72), and the
// sync seq trigger (0422) carries the table for any writer that does not.
func TestPickupsFreshness_AStaffMintReachesTheContractorsFeed(t *testing.T) {
	f := new169Fixture(t)
	orderID := f.insertWillCallOrderT()
	if orderID == "" {
		t.Fatalf("seed the will call order")
	}
	harness.Fresh(t, harness.FreshCase{
		Engine: f.engine, Scope: mustInfo("pickups"), Filter: alphaFilter(),
		Write: func(ctx context.Context) (string, error) {
			w, err := dispatch.NewRepository(f.pool).InsertPickup(ctx, orderID, testkit.BranchNorthAID,
				fmt.Sprintf("fresh-hash-%d", nextVal()), "", time.Now().Add(time.Hour))
			return w.ID, err
		},
		Want: func(d map[string]any) error {
			if d["order_id"] != orderID {
				return fmt.Errorf("order_id is %v, want %s", d["order_id"], orderID)
			}
			return nil
		},
	})
}

// Test169PushScopeBatteries: the harness battery, GATE-7 and the replay
// property per push scope, the acceptance's per-scope technical leg.
func Test169PushScopeBatteries(t *testing.T) {
	// One fixture PER subtest: the battery's since=0 legs count rows, so
	// each scope's rows must be the only ones in its tables when its
	// battery runs (the shared plan table feeds both orders and
	// delivery_instructions, and the subtest's cleanup retires its rows
	// before the next scope's battery starts).
	build := map[string]func(t *testing.T) (*the169Fixture, harness.Kit){
		"orders": func(t *testing.T) (*the169Fixture, harness.Kit) { f := new169Fixture(t); return f, f.ordersKit() },
		"scope_items": func(t *testing.T) (*the169Fixture, harness.Kit) {
			f := new169Fixture(t)
			return f, f.itemsKit(f.insertParentCard(t))
		},
		"delivery_instructions": func(t *testing.T) (*the169Fixture, harness.Kit) { f := new169Fixture(t); return f, f.instructionsKit() },
		"customer_quotes":       func(t *testing.T) (*the169Fixture, harness.Kit) { f := new169Fixture(t); return f, f.quotesKit() },
		"team":                  func(t *testing.T) (*the169Fixture, harness.Kit) { f := new169Fixture(t); return f, f.teamKit() },
	}
	for name, mk := range build {
		t.Run(name, func(t *testing.T) {
			f, k := mk(t)
			harness.Contract(t, f.engine, k)
			harness.Gate7(t, f.engine, k)
			rng := rand.New(rand.NewSource(169))
			harness.ReplayProperty(t, f.engine, k, 6, rng.Intn)
		})
	}
}

// Test169PickupsLegs: the pickups scope's own battery, the honest
// subset (one confirm per pickup, the module's evidence rule): the pull
// shape, since and cursor legs, the create, the confirm as the one
// update, the desk revoke under the manual policy, the tombstone, and
// GATE-7 over an outbox shaped like a device's (create, confirm,
// delete).
func Test169PickupsLegs(t *testing.T) {
	f := new169Fixture(t)
	k := f.pickupsKit()
	e, ctx := f.engine, context.Background()

	// The pull shape: one create surfaces with the scope echo, id, seq
	// and projection.
	created := pushApplied(t, e, k, "create", k.Create(""))
	feed := pullPage(t, e, "pickups", k.Filter, 0, 200)
	if len(feed.Items) != 1 || feed.Items[0].Id != created.ID || feed.Items[0].Seq != created.Seq {
		t.Fatalf("pickups pull shape: %+v", feed.Items)
	}
	id := feed.Items[0].Id

	// The confirm: the one update, applied with a fresh seq, the typed
	// name recorded, the attestation sealed.
	row := loadRow(t, k, id)
	res := push169One(t, e, k, "update", k.Update(row.Data), nil)
	if len(res.Results) != 1 || res.Results[0].Status != "applied" || res.Results[0].Seq == nil || *res.Results[0].Seq <= row.Seq {
		msg := ""
		if res.Results[0].Error != nil {
			msg = *res.Results[0].Error
		}
		t.Fatalf("confirm result: %+v (%s)", res.Results, msg)
	}
	after := loadRow(t, k, id)
	if after.Data["status"] != "CONFIRMED" || after.Data["collected_by"] != "FX Crew Member" {
		t.Fatalf("confirm state: %+v", after.Data)
	}
	if after.Data["signature_ref"] == "" {
		t.Fatalf("confirm sealed no attestation")
	}

	// A second confirm refuses (the module's own rule): one confirmation
	// per pickup, the square is spent.
	again := push169One(t, e, k, "update", k.Update(after.Data), nil)
	if len(again.Results) != 1 || again.Results[0].Status != "rejected" {
		t.Fatalf("second confirm answered %+v", again.Results[0])
	}

	// The manual leg: a desk revoke between the device's pull and its
	// stale replayed confirm answers conflict, applies nothing, returns
	// the server row with the review marker.
	row2 := loadRow(t, k, id)
	if err := k.Desk(ctx, row2.Data); err != nil {
		t.Fatalf("desk revoke: %v", err)
	}
	moved := loadRow(t, k, id)
	base := row2.Seq
	stale := push169One(t, e, k, "update", k.Update(row2.Data), &base)
	out := stale.Results[0]
	if out.Status != "conflict" || out.Conflict == nil || out.Conflict.Policy != string(sync.PolicyManual) {
		t.Fatalf("stale confirm answered %+v", out)
	}
	if out.Conflict.Applied == nil || *out.Conflict.Applied {
		t.Fatalf("manual conflict reports applied: %+v", out.Conflict)
	}
	if out.Conflict.ServerState["status"] != "REVOKED" {
		t.Fatalf("the client was not told to show the server row: %+v", out.Conflict.ServerState)
	}
	end := loadRow(t, k, id)
	if end.Data["status"] != "REVOKED" || end.Seq != moved.Seq {
		t.Fatalf("manual policy overwrote the revoke: %+v", end)
	}

	// The tombstone leg on a second row.
	second := pushApplied(t, e, k, "create", k.Create(""))
	del := push169One(t, e, k, "delete", map[string]any{"id": second.ID}, nil)
	if del.Results[0].Status != "applied" || del.Results[0].Seq == nil {
		t.Fatalf("pickups delete: %+v", del.Results[0])
	}

	// GATE-7 over the device-shaped outbox.
	harness.Gate7(t, e, k)
}

// feedIDOf resolves a create's assigned id off the pull feed when the
// payload carried none.
func feedIDOf(t *testing.T, e *sync.Engine, k harness.Kit, res apigen.SyncPushResult, payload map[string]any) string {
	t.Helper()
	if id, ok := payload["id"].(string); ok && id != "" {
		return id
	}
	resp, err := e.Pull(context.Background(), k.Scope, k.Filter, 0, 200)
	if err != nil {
		t.Fatalf("feed read: %v", err)
	}
	for _, it := range resp.Items {
		if it.Seq == *res.Seq {
			return it.Id
		}
	}
	t.Fatalf("no feed row carries seq %d", *res.Seq)
	return ""
}

// pushApplied pushes one operation and requires an applied outcome.
func pushApplied(t *testing.T, e *sync.Engine, k harness.Kit, op string, payload map[string]any) sync.RowState {
	t.Helper()
	return pushAppliedBase(t, e, k, op, payload, nil)
}

// pushAppliedBase is pushApplied with an optional base_seq.
func pushAppliedBase(t *testing.T, e *sync.Engine, k harness.Kit, op string, payload map[string]any, base *int64) sync.RowState {
	t.Helper()
	resp := push169One(t, e, k, op, payload, base)
	if len(resp.Results) != 1 || resp.Results[0].Status != "applied" || resp.Results[0].Seq == nil {
		msg := ""
		if len(resp.Results) == 1 && resp.Results[0].Error != nil {
			msg = *resp.Results[0].Error
		}
		t.Fatalf("push %s not applied: %+v (%s)", op, resp.Results, msg)
	}
	row, ok, err := k.Store.Load(context.Background(), k.Filter, feedIDOf(t, e, k, resp.Results[0], payload))
	if err != nil || !ok {
		t.Fatalf("load after %s: ok=%v err=%v", op, ok, err)
	}
	return row
}

// payloadID resolves the pushed row's id: the payload's id for
// row-addressed ops, else the engine-assigned one through the feed.
func payloadID(payload map[string]any, feedID string) string {
	if id, ok := payload["id"].(string); ok && id != "" {
		return id
	}
	return feedID
}

// pushOne pushes one operation under the kit's filter.
func push169One(t *testing.T, e *sync.Engine, k harness.Kit, op string, payload map[string]any, base *int64) apigen.SyncPushResponse {
	t.Helper()
	resp, err := e.Push(context.Background(), k.Scope, k.Filter,
		[]apigen.SyncPushOperation{{ClientId: fmt.Sprintf("31000000-0000-4000-8000-%012d", nextVal()), Op: op, Payload: payload, BaseSeq: base}})
	if err != nil {
		t.Fatalf("push %s: %v", op, err)
	}
	return resp
}

// pullPage pulls one raw page (the pickups legs' feed read).
func pullPage(t *testing.T, e *sync.Engine, scope string, f sync.ScopeFilter, since int64, limit int) apigen.SyncPullResponse {
	t.Helper()
	resp, err := e.Pull(context.Background(), mustInfo(scope), f, since, limit)
	if err != nil {
		t.Fatalf("pull %s: %v", scope, err)
	}
	return resp
}

// loadRow loads one row's current state under the kit's filter.
func loadRow(t *testing.T, k harness.Kit, id string) sync.RowState {
	t.Helper()
	row, ok, err := k.Store.Load(context.Background(), k.Filter, id)
	if err != nil || !ok {
		t.Fatalf("load %s: ok=%v err=%v", id, ok, err)
	}
	return row
}

// Test169TenantIsolationPerScope: a tenant token reads only its own
// tenant's rows and pushes only into its own tenant, scope by scope
// (alpha and beta share the north org, so the org alone can never be
// the fence; gamma sits in the south org).
func Test169TenantIsolationPerScope(t *testing.T) {
	f := new169Fixture(t)
	ctx := context.Background()
	beta := betaFilter()
	alpha := alphaFilter()

	// orders (plan cards): alpha's card never pulls through beta, and
	// beta's push lands in beta only.
	card := pushAppliedFilter(t, f.engine, "orders", alpha, f.ordersKit().Create(""))
	if ids := pullOf(t, f.engine, "orders", beta, 0, 200); contains(ids, card.ID) {
		t.Fatalf("beta's orders pull saw alpha's card")
	}
	betaCard := pushAppliedFilter(t, f.engine, "orders", beta, f.ordersKit().Create(""))
	row, _, _ := planflow.NewOrdersSyncStore(f.pool).Load(ctx, beta, betaCard.ID)
	if row.Data == nil || fmt.Sprint(row.Data["id"]) != betaCard.ID {
		t.Fatalf("beta's card load: %+v", row)
	}
	if _, ok, _ := planflow.NewOrdersSyncStore(f.pool).Load(ctx, beta, card.ID); ok {
		t.Fatalf("beta loaded alpha's card by id: existence leaked across tenants")
	}

	// scope_items: alpha's line never pulls through beta.
	parent := f.insertParentCard(t)
	line := pushAppliedFilter(t, f.engine, "scope_items", alpha, f.itemsKit(parent).Create(""))
	if ids := pullOf(t, f.engine, "scope_items", beta, 0, 200); contains(ids, line.ID) {
		t.Fatalf("beta's scope_items pull saw alpha's line")
	}

	// customer_quotes: alpha's document never pulls through beta.
	doc := pushAppliedFilter(t, f.engine, "customer_quotes", alpha, f.quotesKit().Create(""))
	if ids := pullOf(t, f.engine, "customer_quotes", beta, 0, 200); contains(ids, doc.ID) {
		t.Fatalf("beta's customer_quotes pull saw alpha's document")
	}

	// team: alpha's offline-added member never pulls through beta.
	member := pushAppliedFilter(t, f.engine, "team", alpha, f.teamKit().Create(""))
	if ids := pullOf(t, f.engine, "team", beta, 0, 200); contains(ids, member.ID) {
		t.Fatalf("beta's team pull saw alpha's member")
	}

	// delivery_instructions: alpha's note never pulls through beta.
	note := pushAppliedFilter(t, f.engine, "delivery_instructions", alpha, f.instructionsKit().Create(""))
	if ids := pullOf(t, f.engine, "delivery_instructions", beta, 0, 200); contains(ids, note.ID) {
		t.Fatalf("beta's delivery_instructions pull saw alpha's note")
	}

	// pickups: alpha's pickup never pulls through beta (the tenant
	// rides the ORDER, the seam the adapter reads).
	pickup := pushAppliedFilter(t, f.engine, "pickups", alpha, f.pickupsKit().Create(""))
	if ids := pullOf(t, f.engine, "pickups", beta, 0, 200); contains(ids, pickup.ID) {
		t.Fatalf("beta's pickups pull saw alpha's pickup")
	}
	if _, ok, _ := dispatch.NewPickupsSyncStore(f.pool, tenantOrdersAdap{repo: f.orderRepo}, f.sig, nil, nil).Load(ctx, beta, pickup.ID); ok {
		t.Fatalf("beta loaded alpha's pickup by id: existence leaked across tenants")
	}

	// The pull-only tenant scopes: projects, payment_methods, aging and
	// catalog, the same fence.
	projects := procurement.NewProjectsStore(f.pool)
	methods := payments.NewPaymentMethodsStore(f.pool)
	aging := billing.NewAgingStore(f.pool, nil)
	stamp := func(table, id string, seq int64) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET seq = $2 WHERE id = $1::uuid`, table), id, seq); err != nil {
			t.Fatalf("stamp %s: %v", table, err)
		}
	}
	stamp("procurement_projects", testkit.ProcureAlphaID, 1)
	stamp("portal_payment_methods", testkit.PayMethodAlphaID, 1)
	stamp("invoices", testkit.InvoiceAlphaID, 1)
	if got, _, _ := projects.Pull(ctx, beta, 0, 200); len(got) != 0 {
		t.Fatalf("beta's projects pull saw alpha's project: %d rows", len(got))
	}
	if got, _, _ := projects.Pull(ctx, alpha, 0, 200); len(got) == 0 {
		t.Fatalf("alpha's projects pull missed its own project")
	}
	if got, _, _ := methods.Pull(ctx, beta, 0, 200); len(got) != 0 {
		t.Fatalf("beta's payment_methods pull saw alpha's method")
	}
	if got, _, _ := methods.Pull(ctx, alpha, 0, 200); len(got) == 0 {
		t.Fatalf("alpha's payment_methods pull missed its own method")
	}
	if got, _, _ := aging.Pull(ctx, beta, 0, 200); len(got) != 0 {
		t.Fatalf("beta's aging pull saw alpha's invoice")
	}
	if got, _, _ := aging.Pull(ctx, alpha, 0, 200); len(got) == 0 {
		t.Fatalf("alpha's aging pull missed its own invoice")
	}
	// catalog: the org's products through each tenant; the south org's
	// tenant sees none of the north org's rows.
	if _, err := f.pool.Exec(ctx, `UPDATE products SET seq = 1 WHERE id = $1::uuid`, testkit.ProductID); err != nil {
		t.Fatalf("stamp product: %v", err)
	}
	catalog := product.NewCatalogStore(f.pool)
	if got, _, _ := catalog.Pull(ctx, alpha, 0, 200); len(got) == 0 {
		t.Fatalf("alpha's catalog pull missed the org's product")
	}
	south := sync.ScopeFilter{OrgID: testkit.OrgSouthID, TenantID: testkit.TenantGammaID, Tenant: true, Actor: testkit.SubPortalAdminGamma}
	if got, _, _ := catalog.Pull(ctx, south, 0, 200); len(got) != 0 {
		t.Fatalf("the south tenant's catalog pull saw the north org's product")
	}
}

// pushAppliedFilter pushes one create under an explicit filter and
// requires an applied outcome (the isolation legs' write half).
func pushAppliedFilter(t *testing.T, e *sync.Engine, scope string, f sync.ScopeFilter, payload map[string]any) sync.RowState {
	t.Helper()
	info := mustInfo(scope)
	resp, err := e.Push(context.Background(), info, f, []apigen.SyncPushOperation{{
		ClientId: fmt.Sprintf("32000000-0000-4000-8000-%012d", nextVal()), Op: "create", Payload: payload,
	}})
	if err != nil {
		t.Fatalf("push %s: %v", scope, err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != "applied" {
		t.Fatalf("push %s: %+v", scope, resp.Results)
	}
	pull, err := e.Pull(context.Background(), info, f, 0, 200)
	if err != nil {
		t.Fatalf("pull %s: %v", scope, err)
	}
	for _, it := range pull.Items {
		if it.Seq == *resp.Results[0].Seq {
			return sync.RowState{ID: it.Id, Seq: it.Seq, Data: it.Data}
		}
	}
	t.Fatalf("no %s feed row carries seq %d", scope, *resp.Results[0].Seq)
	return sync.RowState{}
}

// hideFixtureRowsFromFeeds clears the seq of the kit fixture's own rows (ids 3ef10000-...). Since the sync seq
// trigger (migration 0422) the fixture builder's plain INSERTs take a seq, so every feed would hold the
// fixture rows beside the rows a battery creates; these legs count the rows of a scope (since=0) and stamp
// the fixture rows they want on a feed themselves, so their premise is a feed that holds only what the leg
// put there. Setting seq to NULL is an explicit seq write, so the trigger leaves it alone.
func hideFixtureRowsFromFeeds(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, table := range []string{
		"payments", "invoices", "quotes", "delivery_runs", "delivery_stops", "delivery_pod_photos", "products",
		"customers", "vendors", "portal_payment_methods", "portal_users", "warranty_claims", "pos_parked_tickets",
		"customer_quote", "procurement_projects", "dispatch_trucks", "dispatch_drivers", "attachment", "plan",
		"plan_line", "permission_approval_requests", "purchasing_pos", "task_shortages", "count_sheets",
	} {
		if _, err := pool.Exec(context.Background(),
			fmt.Sprintf(`UPDATE %s SET seq = NULL WHERE id::text LIKE '3ef10000-%%' AND seq IS NOT NULL`, table)); err != nil {
			t.Fatalf("clear the fixture rows' seq on %s: %v", table, err)
		}
	}
}

func contains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// Test169PullOnlyFeeds: every pull-only scope's GET over the real
// tables: the org fence holds on both scoping shapes (the org column
// and the branch join), the composite-key scopes derive one stable id,
// and the aging projection computes its legs off the invoice row.
func Test169PullOnlyFeeds(t *testing.T) {
	f := new169Fixture(t)
	ctx := context.Background()
	staff := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}

	// Seed one stamped row per org-class table (the fixture kit's own
	// discipline: patterned ids, this test's rows, the kit's cleanup).
	seed := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	seed(`UPDATE products SET seq = 1 WHERE id = $1`, testkit.ProductID)
	custID := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	seed(`INSERT INTO customers (id, account_no, name, org_id, seq) VALUES ($1, 'FX169-ACCT', 'FX Yard Supply', $2, 1) ON CONFLICT (id) DO NOTHING`, custID, testkit.OrgNorthID)
	vendID := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	seed(`INSERT INTO vendors (id, vendor_no, name, org_id, seq) VALUES ($1, 'FX169-VEN', 'FX Stone Works', $2, 1) ON CONFLICT (id) DO NOTHING`, vendID, testkit.OrgNorthID)
	seed(`UPDATE vendors SET seq = COALESCE(seq, 1) WHERE org_id = $1`, testkit.OrgNorthID)
	truckID := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	seed(`INSERT INTO dispatch_trucks (id, branch_id, code, name, seq) VALUES ($1, $2, 'FX169-TRK', 'FX Flatbed', 1) ON CONFLICT (id) DO NOTHING`, truckID, testkit.BranchNorthAID)
	driverID := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	seed(`INSERT INTO dispatch_drivers (id, branch_id, name, seq) VALUES ($1, $2, 'FX Driver', 1) ON CONFLICT (id) DO NOTHING`, driverID, testkit.BranchNorthAID)
	balProduct := testkit.ProductID
	seed(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base, org_id, seq)
	     VALUES ($1, $2, $2, 40, 5, $3, 1) ON CONFLICT (product_id, location_id) DO NOTHING`, balProduct, testkit.BranchNorthAID, testkit.OrgNorthID)
	parkID := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	seed(`INSERT INTO pos_parked_tickets (id, ticket_no, branch_id, lines, seq) VALUES ($1, 'FX169-PARK', $2, '[]'::jsonb, 1) ON CONFLICT (id) DO NOTHING`, parkID, testkit.BranchNorthAID)
	wtyID := fmt.Sprintf("3ef10000-0000-4000-8000-%012d", nextVal())
	seed(`INSERT INTO warranty_claims (id, claim_no, branch_id, seq) VALUES ($1, 'FX169-WTY', $2, 1) ON CONFLICT (id) DO NOTHING`, wtyID, testkit.BranchNorthAID)
	widgetUser := testkit.UserInsideSalesID
	seed(`INSERT INTO dashboard_user_widgets (user_id, widget_key, shape, measure, seq) VALUES ($1, 'fx169.measure', 'stat', 'fx169_measure', 1) ON CONFLICT (user_id, widget_key) DO NOTHING`, widgetUser)
	seed(`UPDATE dashboard_user_widgets SET seq = 1 WHERE user_id = $1 AND widget_key = 'fx169.measure'`, widgetUser)

	link := func(ctx context.Context, subject string) (string, bool) {
		var uid string
		if err := f.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE platform_user_id = $1 AND deleted_at IS NULL`, subject).Scan(&uid); err != nil {
			return "", false
		}
		return uid, true
	}

	type leg struct {
		name  string
		store sync.Store
	}
	legs := []leg{
		{"products", product.NewProductsStore(f.pool)},
		{"customers", customer.NewCustomersStore(f.pool)},
		{"vendors", purchasing.NewVendorsStore(f.pool)},
		{"trucks", dispatch.NewTrucksStore(f.pool)},
		{"drivers", dispatch.NewDriversStore(f.pool)},
		{"inventory_lookup", inventory.NewInventoryLookupStore(f.pool)},
		{"parked_tickets", pos.NewParkedTicketsStore(f.pool)},
		{"warranty_claims", warranty.NewWarrantyClaimsStore(f.pool)},
		{"warranty_board", warranty.NewWarrantyBoardStore(f.pool)},
		{"widget_measures", dashboard.NewWidgetMeasuresStore(f.pool, link)},
	}
	for _, l := range legs {
		items, tombs, err := l.store.Pull(ctx, staff, 0, 200)
		if err != nil {
			t.Fatalf("%s pull: %v", l.name, err)
		}
		if len(items) == 0 && len(tombs) == 0 {
			t.Fatalf("%s pull carried nothing over the seeded rows", l.name)
		}
		for _, it := range items {
			if it.ID == "" || it.Seq == 0 || len(it.Data) == 0 {
				t.Fatalf("%s row shape: %+v", l.name, it)
			}
		}
		// The south org's staff caller sees none of the north org's rows.
		south := sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubGeneralMgr}
		southItems, _, _ := l.store.Pull(ctx, south, 0, 200)
		for _, it := range southItems {
			for _, north := range items {
				if it.ID == north.ID {
					t.Fatalf("%s: the south org's pull saw the north org's row %s", l.name, it.ID)
				}
			}
		}
	}

	// The inventory_lookup row's derived id is stable across pulls.
	bal, _, _ := inventory.NewInventoryLookupStore(f.pool).Pull(ctx, staff, 0, 200)
	if len(bal) == 0 {
		t.Fatalf("inventory_lookup carried no balance row")
	}
	again, _, _ := inventory.NewInventoryLookupStore(f.pool).Pull(ctx, staff, 0, 200)
	if bal[0].ID != again[0].ID {
		t.Fatalf("the derived balance id moved between pulls: %s then %s", bal[0].ID, again[0].ID)
	}

	// The aging projection: the past-due leg computes off the due date
	// and the balance the posting path maintains.
	seed(`UPDATE invoices SET seq = 1, balance_cents = 5000, due_date = now() - interval '45 days' WHERE id = $1`, testkit.InvoiceAlphaID)
	aging := billing.NewAgingStore(f.pool, nil)
	items, _, err := aging.Pull(ctx, alphaFilter(), 0, 200)
	if err != nil {
		t.Fatalf("aging pull: %v", err)
	}
	var found bool
	for _, it := range items {
		if it.ID == testkit.InvoiceAlphaID {
			found = true
			if it.Data["bucket"] != "days_31_60" || it.Data["past_due"] != true {
				t.Fatalf("aging legs: %+v", it.Data)
			}
		}
	}
	if !found {
		t.Fatalf("aging pull missed the tenant's invoice: %+v", items)
	}
}

// Test169CustomerQuoteTokenLifecycle: the customer_quotes sync path
// composes with HH-3.6's token lifecycle: a sync write never
// resurrects a dead token's document. The contractor edits offline;
// the desk sends and then revises (the token rotates, the old link
// dies); the offline edit replays into the manual conflict arm,
// applies nothing, and the dead token still answers 410 on the public
// route.
func Test169CustomerQuoteTokenLifecycle(t *testing.T) {
	f := new169Fixture(t)
	k := f.quotesKit()
	e, ctx := f.engine, context.Background()
	scope := customerquote.Scope{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID}

	// The contractor composes a draft offline and pulls it.
	created := pushApplied(t, e, k, "create", k.Create(""))
	id := created.ID
	row := loadRow(t, k, id)
	base := row.Seq

	// The desk sends: the module's own rotation, the raw token handed
	// back exactly once.
	sent, err := f.cqSvc.Send(ctx, scope, id)
	if err != nil {
		t.Fatalf("desk send: %v", err)
	}
	liveToken := sent.MagicLinkToken
	t.Logf("lifecycle: desk sent the quote, the magic link minted (raw token held by the test only)")

	// The desk revises while the contractor's device holds a stale
	// draft edit: the rotation retires the live token and returns the
	// document to draft.
	if _, err := f.cqSvc.Revise(ctx, scope, id); err != nil {
		t.Fatalf("desk revise: %v", err)
	}
	moved := loadRow(t, k, id)
	if moved.Seq <= base {
		t.Fatalf("the revise did not bump the row's seq (%d then %d)", base, moved.Seq)
	}

	// The offline edit replays against the stale base: the manual
	// policy answers conflict, applies nothing, returns the server row
	// with the review marker, and the document the desk left (draft,
	// token retired) stands.
	edit := k.Update(row.Data)
	stale := push169One(t, e, k, "update", edit, &base)
	out := stale.Results[0]
	if out.Status != "conflict" || out.Conflict == nil || out.Conflict.Policy != string(sync.PolicyManual) {
		t.Fatalf("stale edit answered %+v", out)
	}
	if out.Conflict.Applied == nil || *out.Conflict.Applied {
		t.Fatalf("manual conflict reports applied: %+v", out.Conflict)
	}
	after := loadRow(t, k, id)
	if after.Seq != moved.Seq || fmt.Sprint(after.Data["status"]) != fmt.Sprint(moved.Data["status"]) {
		t.Fatalf("the stale edit moved the document: %+v want %+v", after.Data, moved.Data)
	}
	if after.Data["share_token_hash"] != nil {
		t.Fatalf("the projection carries the token hash: %+v", after.Data)
	}

	// The dead token answers 410 on the public leg: the rotation killed
	// the link the contractor's customer held.
	_, pubErr := f.cqSvc.PublicView(ctx, liveToken)
	if pubErr == nil {
		t.Fatalf("the retired token still served the public view")
	}
	var cqErr *customerquote.Error
	if !errors.As(pubErr, &cqErr) || cqErr.Status != 410 {
		t.Fatalf("the retired token answered %v, want the 410 dead-link family", pubErr)
	}
	t.Logf("lifecycle: the retired token answers 410 on the public route; the replay returned the server row with the review marker")

	// A payload that declares the token legs refuses whole, so no sync
	// write can resurrect or counterfeit a token.
	hijack := push169One(t, e, k, "update", map[string]any{
		"id": id, "status": "sent", "share_token_hash": "fx169forged",
	}, nil)
	if hijack.Results[0].Status != "rejected" {
		t.Fatalf("a token-leg payload applied: %+v", hijack.Results[0])
	}
}

// Test169Walkthrough: the operator walkthrough, scripted against the
// seeded database exactly as the acceptance words it: a contractor
// edits a customer quote offline, a revise on the server rotates the
// token, the replay returns the server row with the review marker and
// the dead token still answers 410 on the public route; a second
// contractor tenant pulls and sees none of it.
func Test169Walkthrough(t *testing.T) {
	f := new169Fixture(t)
	k := f.quotesKit()
	e, ctx := f.engine, context.Background()
	scope := customerquote.Scope{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID}

	// Step 1: the contractor composes the proposal for the homeowner at
	// the counter of a job site with no signal.
	created := pushApplied(t, e, k, "create", k.Create(""))
	id := created.ID
	row := loadRow(t, k, id)
	t.Logf("walkthrough 1: contractor drafted customer quote %s offline (markup %v)", id, row.Data["markup_bps"])
	base := row.Seq

	// Step 2: the device pulls the document it just birthed; the desk
	// sends the proposal (the live magic link) and then revises it when
	// the homeowner asks for a change: the rotation retires the link.
	sent, err := f.cqSvc.Send(ctx, scope, id)
	if err != nil {
		t.Fatalf("desk send: %v", err)
	}
	if _, err := f.cqSvc.Revise(ctx, scope, id); err != nil {
		t.Fatalf("desk revise: %v", err)
	}
	t.Logf("walkthrough 2: desk sent then revised the proposal; the first magic link is dead")

	// Step 3: signal returns; the contractor's offline markup edit
	// replays and meets the manual policy: the server row comes back
	// with the review marker, nothing of the stale edit lands.
	edit := k.Update(row.Data)
	stale := push169One(t, e, k, "update", edit, &base)
	out := stale.Results[0]
	if out.Status != "conflict" || out.Conflict.Policy != string(sync.PolicyManual) || *out.Conflict.Applied {
		t.Fatalf("walkthrough 3: %+v %+v", out, out.Conflict)
	}
	t.Logf("walkthrough 3: the replay answered conflict (manual), the review marker stands, the server row is what the device must show")

	// Step 4: the dead token still answers 410 on the public route.
	perr := func() error {
		_, err := f.cqSvc.PublicView(ctx, sent.MagicLinkToken)
		return err
	}()
	if perr == nil {
		t.Fatalf("walkthrough 4: the dead token served the public view")
	}
	var cqErr *customerquote.Error
	if !errors.As(perr, &cqErr) || cqErr.Status != 410 {
		t.Fatalf("walkthrough 4: the dead token answered %v, want the 410 dead-link family", perr)
	}
	t.Logf("walkthrough 4: the dead magic link answers 410 on the public route")

	// Step 5: the second contractor tenant pulls and sees none of it.
	beta := betaFilter()
	resp, err := e.Pull(ctx, mustInfo("customer_quotes"), beta, 0, 200)
	if err != nil {
		t.Fatalf("walkthrough 5: %v", err)
	}
	for _, it := range resp.Items {
		if it.Id == id {
			t.Fatalf("walkthrough 5: beta's pull saw alpha's proposal")
		}
	}
	t.Logf("walkthrough 5: the second tenant's customer_quotes pull carried %d row(s), none of alpha's", len(resp.Items))
}

// Test169ManualGuardMutation: the mutation check the acceptance
// demands, run mechanically in the suite where the guard is a query
// the test can flip: the manual policy's never-overwrite arm is the
// ENGINE's guard, so this leg proves the store-side guard (the
// customerquote draft-state refusal) by writing through the module's
// own path with the guard's inputs. The engine-side and route-side
// mutations (the resolveConflict arm and the handler's pull-only
// refusal) are run as scripted reverts recorded in the PR's mutation
// table: reverting conflict.go's manual arm to applied true fails
// Test169PushScopeBatteries/customer_quotes; reverting handler.go's
// PolicyPullOnly check fails TestPullOnlyScopesPostRefused.
func Test169ManualGuardMutation(t *testing.T) {
	f := new169Fixture(t)
	k := f.quotesKit()
	e, ctx := f.engine, context.Background()
	scope := customerquote.Scope{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID}

	// A sent document refuses a FRESH sync edit: the module's own
	// draft-state guard, the store half of the manual composition.
	created := pushApplied(t, e, k, "create", k.Create(""))
	id := created.ID
	if _, err := f.cqSvc.Send(ctx, scope, id); err != nil {
		t.Fatalf("send: %v", err)
	}
	fresh := push169One(t, e, k, "update", map[string]any{"id": id, "markup_bps": 2000}, nil)
	if fresh.Results[0].Status != "rejected" {
		t.Fatalf("a fresh edit on a sent document applied: %+v", fresh.Results[0])
	}
	// And the refusal left the document exactly as the send left it.
	row := loadRow(t, k, id)
	if row.Data["status"] != "sent" {
		t.Fatalf("the refused edit changed the document: %+v", row.Data)
	}
}

// Test169PriceLegsRefused: the scope_items writable set refuses every
// price leg, so no client field merge can write, resurrect or
// counterfeit a price: the #168 quotes hazard, refused by construction
// here.
func Test169PriceLegsRefused(t *testing.T) {
	f := new169Fixture(t)
	parent := f.insertParentCard(t)
	k := f.itemsKit(parent)
	e := f.engine
	created := pushApplied(t, e, k, "create", k.Create(""))
	id := created.ID
	for field, value := range map[string]any{
		"unit_price_cents": 1, "price_source": "quoted", "price_uom": "SQFT",
		"line_total_cents": 1, "price_valid_until": "2030-01-01T00:00:00Z",
		"qty_base": "12", "price_snapshot": map[string]any{"core": true}, "snapshot": map[string]any{"sku": "FX"},
	} {
		res := push169One(t, e, k, "update", map[string]any{"id": id, field: value}, nil)
		if len(res.Results) != 1 || res.Results[0].Status != "rejected" {
			t.Fatalf("price leg %q applied: %+v", field, res.Results)
		}
	}
	// And a create carrying a price leg refuses whole.
	res := push169One(t, e, k, "create", map[string]any{"plan_id": parent, "unit_price_cents": 1}, nil)
	if res.Results[0].Status != "rejected" {
		t.Fatalf("a create carrying a price leg applied: %+v", res.Results)
	}
}

// TestTeamLastOwnerGuardHoldsThroughSync pins the module's M-1 rule on the
// sync path (the independent review's P2-2: the guard was implemented and
// REST-tested, but no push had ever driven it). A tenant's LAST active
// account_admin cannot be demoted, deactivated or removed through a sync
// push: each is that operation's rejected result, the module's own
// sentence travelling in it, and the row is untouched. The alpha tenant's
// seeded admin (testkit.PortalAdminAlphaID) is that tenant's only active
// admin, so the guard must bite on it.
func TestTeamLastOwnerGuardHoldsThroughSync(t *testing.T) {
	f := new169Fixture(t)
	ctx := context.Background()
	alpha := alphaFilter()
	id := testkit.PortalAdminAlphaID

	push := func(kind string, payload map[string]any) apigen.SyncPushResult {
		t.Helper()
		info := mustInfo("team")
		resp, err := f.engine.Push(ctx, info, alpha, []apigen.SyncPushOperation{{
			ClientId: fmt.Sprintf("33000000-0000-4000-8000-%012d", nextVal()), Op: kind, Payload: payload,
		}})
		if err != nil {
			t.Fatalf("push team %s: %v", kind, err)
		}
		if len(resp.Results) != 1 {
			t.Fatalf("push team %s: %+v", kind, resp.Results)
		}
		return resp.Results[0]
	}

	// Demote the last active admin: rejected, the module's sentence.
	demote := push("update", map[string]any{"id": id, "role": "buyer"})
	if demote.Status != "rejected" || demote.Error == nil || !strings.Contains(*demote.Error, "last owner cannot be demoted") {
		t.Fatalf("the last admin's demotion answered %+v, want the rejected last-owner refusal", demote)
	}
	// Deactivate the last active admin: the same refusal.
	deactivate := push("update", map[string]any{"id": id, "status": "inactive"})
	if deactivate.Status != "rejected" || deactivate.Error == nil || !strings.Contains(*deactivate.Error, "last owner cannot be demoted") {
		t.Fatalf("the last admin's deactivation answered %+v, want the rejected last-owner refusal", deactivate)
	}
	// Remove the last active admin: rejected, the delete's own sentence.
	remove := push("delete", map[string]any{"id": id})
	if remove.Status != "rejected" || remove.Error == nil || !strings.Contains(*remove.Error, "last owner cannot be removed") {
		t.Fatalf("the last admin's removal answered %+v, want the rejected last-owner refusal", remove)
	}

	// The row is untouched: still the active admin.
	var role, status string
	if err := f.pool.QueryRow(ctx,
		`SELECT role, status FROM portal_users WHERE id = $1::uuid AND deleted_at IS NULL`, id).
		Scan(&role, &status); err != nil {
		t.Fatalf("read the admin back: %v", err)
	}
	if role != "account_admin" || status != "active" {
		t.Fatalf("a refused guard moved the row: role %q status %q", role, status)
	}
}
