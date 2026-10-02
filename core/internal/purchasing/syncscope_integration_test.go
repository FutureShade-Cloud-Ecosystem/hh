//go:build integration

// syncscope_integration_test.go: the purchase_orders sync scope against
// a real database (HH-3.5 work item #168's acceptance): the per-scope
// contract battery, GATE-7 and the replay property over the purchasing
// module's own purchasing_pos table, the module's own lifecycle
// invariants holding through sync (the receipt-driven states are never
// requestable, an empty order cannot be ordered, a received order
// closes rather than cancels), org isolation with the tenant filter
// failing closed, and the desk-created PO's visibility to a device's
// pull (the seq stamped beside the module's own writes).
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync168?sslmode=disable' \
//	  go test -tags=integration -count=1 -run TestPOSync ./internal/purchasing/
package purchasing

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	syncharness "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// poSyncFixture is the purchase_orders world: the module's own
// repository (the desk legs run its own paths), the sync store over
// the same pool, and the engine wired exactly as the composition root
// wires it.
type poSyncFixture struct {
	pool   *pgxpool.Pool
	repo   *Repository
	store  *SyncStore
	engine *sync.Engine
	info   sync.ScopeInfo
	filter sync.ScopeFilter
	vendor string
}

// poSyncVendor seeds one active vendor and returns its id.
func poSyncVendor(t *testing.T, pool *pgxpool.Pool, suffix string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
INSERT INTO vendors (vendor_no, name, is_active)
VALUES ($1::citext, $2, TRUE) RETURNING id::text`, "FXV-SYNC-"+suffix, "FX sync vendor "+suffix).Scan(&id)
	if err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
	return id
}

func newPOSyncFixture(t *testing.T) *poSyncFixture {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	f := &poSyncFixture{
		pool:   pool,
		repo:   NewRepository(pool),
		store:  NewSyncStore(pool, poVendorDir{pool: pool}, poLinkedUser(pool)),
		engine: sync.NewEngine(),
		filter: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales},
		vendor: poSyncVendor(t, pool, uniquePOSuffix()),
	}
	if err := f.engine.RegisterScope("purchase_orders", f.store); err != nil {
		t.Fatalf("register purchase_orders: %v", err)
	}
	f.info, _ = sync.Lookup("purchase_orders")
	t.Cleanup(func() {
		c := context.Background()
		// The cleanup keys on the fixture's own vendors: the po_no a
		// create mints comes from the branch's counter and carries no
		// fixture marker, but every PO this suite writes buys from a
		// vendor it seeded.
		_, _ = pool.Exec(c, `DELETE FROM purchasing_po_lines WHERE po_id IN (SELECT id FROM purchasing_pos WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no LIKE 'FXV-SYNC-%'))`)
		_, _ = pool.Exec(c, `DELETE FROM purchasing_pos WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no LIKE 'FXV-SYNC-%'))`)
		_, _ = pool.Exec(c, `DELETE FROM purchasing_pos WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no LIKE 'FXV-SYNC-%')`)
		_, _ = pool.Exec(c, `DELETE FROM vendors WHERE vendor_no LIKE 'FXV-SYNC-%'`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'purchase_orders'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	return f
}

// uniquePOSuffix is a per-fixture unique tag (the vendor_no and po_no
// markers the cleanup keys on).
func uniquePOSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// poVendorDir satisfies VendorDirectory over the vendors table, the
// same read the composition root's adapter carries.
type poVendorDir struct{ pool *pgxpool.Pool }

func (d poVendorDir) ListVendors(ctx context.Context) ([]Vendor, error) { return nil, nil }

func (d poVendorDir) Vendor(ctx context.Context, vendorID string) (Vendor, error) {
	var v Vendor
	err := d.pool.QueryRow(ctx,
		`SELECT id::text, vendor_no::text, name, is_active FROM vendors WHERE id = $1::uuid`, vendorID).
		Scan(&v.ID, &v.VendorNo, &v.Name, &v.IsActive)
	if err != nil {
		return Vendor{}, errNotFound
	}
	return v, nil
}

// poLinkedUser resolves the ERP user row for a platform subject.
func poLinkedUser(pool *pgxpool.Pool) func(ctx context.Context, platformUserID string) (string, bool) {
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

// poSyncKit builds the battery kit: creates draft against the fixture's
// vendor at the north branch, updates carry a CONSTANT expected date
// (the replay posture), the chain evolves it, and the desk corrects the
// date through the module's own UpdateExpectedAt path.
func poSyncKit(f *poSyncFixture) syncharness.Kit {
	return syncharness.Kit{
		Scope: f.info, Filter: f.filter, Store: f.store,
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"vendor_id":   f.vendor,
				"branch_id":   testkit.BranchNorthAID,
				"expected_at": poSyncDate(3),
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "expected_at": poSyncDate(14)}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			for i := 1; i <= n; i++ {
				out = append(out, map[string]any{"id": row["id"], "expected_at": poSyncDate(20 + i)})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			return f.repo.UpdateExpectedAt(ctx, fmt.Sprint(row["id"]), poSyncDate(30))
		},
	}
}

// poSyncDate is a fixture date n days out.
func poSyncDate(n int) string {
	return time.Now().UTC().AddDate(0, 0, n).Format("2006-01-02")
}

// TestPOSyncContractBattery: the per-scope battery, GATE-7 and the
// replay property.
func TestPOSyncContractBattery(t *testing.T) {
	f := newPOSyncFixture(t)
	k := poSyncKit(f)
	syncharness.Contract(t, f.engine, k)
	syncharness.Gate7(t, f.engine, k)
	rng := rand.New(rand.NewSource(1681))
	syncharness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestPOSyncModuleInvariants: the module's own lifecycle rules hold
// through sync: the receipt-driven states are never requestable, an
// empty order cannot be ordered, a finished order carries no
// corrections, and the money legs refuse.
func TestPOSyncModuleInvariants(t *testing.T) {
	f := newPOSyncFixture(t)
	ctx := context.Background()

	id := poSyncCreate(t, f, f.filter, poSyncKit(f).Create(""))

	// The receipt-driven states refuse.
	for _, st := range []string{"PARTIAL", "RECEIVED"} {
		res := poSyncPush(t, f, []poSyncOp{
			{client: poSyncKey(1), kind: "update", payload: map[string]any{"id": id, "status": st}},
		})
		if len(res) != 1 || res[0].Status != "rejected" {
			t.Fatalf("%s through sync answered %+v, want rejected", st, res[0])
		}
	}
	// An empty order cannot be ordered.
	res := poSyncPush(t, f, []poSyncOp{
		{client: poSyncKey(2), kind: "update", payload: map[string]any{"id": id, "status": "ORDERED"}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("ordering an empty PO answered %+v, want rejected", res[0])
	}
	t.Logf("invariant: ordering an empty PO refused (%s)", *res[0].Error)

	// A line lands through the module's own editor (the header seq
	// riding it, the desk-visible property), and then the order can be
	// ordered.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := f.repo.InsertPOLine(ctx, tx, id, 1, AddLineParams{
		ProductID: testkit.ProductID, Description: "FX sync line", QtyOrderedBase: 10, UnitCostCents: 500,
	}); err != nil {
		t.Fatalf("insert line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	res = poSyncPush(t, f, []poSyncOp{
		{client: poSyncKey(3), kind: "update", payload: map[string]any{"id": id, "status": "ORDERED"}},
	})
	if len(res) != 1 || res[0].Status != "applied" {
		t.Fatalf("ordering a lined PO answered %+v", res[0])
	}
	// A finished order carries no corrections.
	if _, err := f.pool.Exec(ctx,
		`UPDATE purchasing_pos SET status = 'RECEIVED' WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("receive: %v", err)
	}
	res = poSyncPush(t, f, []poSyncOp{
		{client: poSyncKey(4), kind: "update", payload: map[string]any{"id": id, "expected_at": poSyncDate(40)}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("correcting a RECEIVED order answered %+v, want rejected", res[0])
	}
	// The money legs refuse.
	res = poSyncPush(t, f, []poSyncOp{
		{client: poSyncKey(5), kind: "update", payload: map[string]any{"id": id, "fx_rate": 0.5}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("a money leg through sync answered %+v, want rejected", res[0])
	}
}

// TestPOSyncDeskCreatedVisibleAndIsolation: a PO the desk drafts
// through the module's own insert is visible to a device's pull the
// moment it lands, rows of another org never pull, and the tenant
// filter answers nothing.
func TestPOSyncDeskCreatedVisibleAndIsolation(t *testing.T) {
	f := newPOSyncFixture(t)
	ctx := context.Background()
	southVendor := poSyncVendor(t, f.pool, "S"+uniquePOSuffix())

	// The desk drafts a north PO through the module's own path.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	poNo, err := f.repo.MintPONo(ctx, tx, testkit.BranchNorthAID)
	if err != nil {
		t.Fatalf("mint po_no: %v", err)
	}
	deskID, err := f.repo.InsertPO(ctx, tx, insertPOParams{
		PONo: poNo, VendorID: f.vendor, BranchID: testkit.BranchNorthAID, Currency: "CAD", FXRate: 1.0,
	})
	if err != nil {
		t.Fatalf("insert po: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	pull, err := f.engine.Pull(ctx, f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("device pull: %v", err)
	}
	seen := false
	for _, it := range pull.Items {
		if it.Id == deskID {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the desk-drafted PO never surfaced in the device's pull")
	}
	t.Logf("desk visible: the desk-drafted %s pulled at once (org stamped from the branch by the module's own insert)", poNo)

	// A south PO through the same path never pulls north.
	stx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin south: %v", err)
	}
	southNo, err := f.repo.MintPONo(ctx, stx, testkit.BranchSouthAID)
	if err != nil {
		t.Fatalf("mint south po_no: %v", err)
	}
	southID, err := f.repo.InsertPO(ctx, stx, insertPOParams{
		PONo: southNo, VendorID: southVendor, BranchID: testkit.BranchSouthAID, Currency: "CAD", FXRate: 1.0,
	})
	if err != nil {
		t.Fatalf("insert south po: %v", err)
	}
	if err := stx.Commit(ctx); err != nil {
		t.Fatalf("commit south: %v", err)
	}
	pull, err = f.engine.Pull(ctx, f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("north pull 2: %v", err)
	}
	for _, it := range pull.Items {
		if it.Id == southID {
			t.Fatalf("the north org's pull saw the south org's PO")
		}
	}
	// The tenant filter answers nothing.
	tenant := f.filter
	tenant.TenantID, tenant.Tenant, tenant.Actor = testkit.TenantAlphaID, true, testkit.SubPortalAdminAlpha
	tenantPull, err := f.engine.Pull(ctx, f.info, tenant, 0, 200)
	if err != nil {
		t.Fatalf("tenant pull: %v", err)
	}
	if len(tenantPull.Items) != 0 || len(tenantPull.Tombstones) != 0 {
		t.Fatalf("a tenant filter read org rows through purchase_orders: %+v", tenantPull)
	}
}

// The small helpers.

type poSyncOp struct {
	client  string
	kind    string
	payload map[string]any
	base    *int64
}

func poSyncKey(n int) string { return fmt.Sprintf("34000000-0000-4000-8000-%012d", n) }

func poSyncPush(t *testing.T, f *poSyncFixture, ops []poSyncOp) []apigen.SyncPushResult {
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

// poSyncCreate pushes one create and returns the row id.
func poSyncCreate(t *testing.T, f *poSyncFixture, filter sync.ScopeFilter, payload map[string]any) string {
	t.Helper()
	res := poSyncPush(t, f, []poSyncOp{{client: poSyncKey(0), kind: "create", payload: payload}})
	if res[0].Status != "applied" || res[0].Seq == nil {
		t.Fatalf("sync create: %+v", res[0])
	}
	feed, err := f.engine.Pull(context.Background(), f.info, filter, 0, 200)
	if err != nil {
		t.Fatalf("sync create feed: %v", err)
	}
	for _, it := range feed.Items {
		if it.Seq == *res[0].Seq {
			return it.Id
		}
	}
	t.Fatalf("sync create: the new row never pulled at seq %d", *res[0].Seq)
	return ""
}
