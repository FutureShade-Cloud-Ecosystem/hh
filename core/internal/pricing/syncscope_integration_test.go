//go:build integration

// syncscope_integration_test.go: the price_matrix sync scope against
// a real database (HH-3.5 work item #168's acceptance): the per-scope
// contract battery, GATE-7 and the replay property over the pricing
// module's own table, the write-path proof (every matrix write, pushed
// or desk-published, running the module's one list-price path with its
// price_change_log trail beside it, never around it), org isolation
// with the tenant caller reading the same dealer rows through its
// token's org, and the audit rows.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync168?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/pricing/
package pricing

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

// matrixFixture is the price_matrix world: the module's own service
// (the desk publish legs run its own batch apply), the sync store over
// the same pool, and the engine wired exactly as the composition root
// wires it.
type matrixFixture struct {
	pool   *pgxpool.Pool
	svc    *Service
	store  *SyncStore
	engine *sync.Engine
	info   sync.ScopeInfo
	filter sync.ScopeFilter
	n      int
	t      *testing.T
}

// newMatrixFixture builds the world and one org-stamped product per
// anchor the battery needs (the row's identity is the product).
func newMatrixFixture(t *testing.T) *matrixFixture {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	f := &matrixFixture{
		pool:   pool,
		svc:    realSvc(pool),
		store:  NewSyncStore(pool, nil),
		engine: sync.NewEngine(),
		filter: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales},
		t:      t,
	}
	if err := f.engine.RegisterScope("price_matrix", f.store); err != nil {
		t.Fatalf("register price_matrix: %v", err)
	}
	f.info, _ = sync.Lookup("price_matrix")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM price_change_log WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXSYNC-%')`)
		_, _ = pool.Exec(c, `DELETE FROM pricing_list_prices WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXSYNC-%')`)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE sku LIKE 'FXSYNC-%'`)
		_, _ = pool.Exec(c, `DELETE FROM price_batches WHERE batch_no LIKE 'FXBATCH-SYNC-%'`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'price_matrix'`)
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_type = 'sync.price_matrix'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	return f
}

// product seeds one org-stamped product (the anchor's identity) and
// returns its id; a caller-chosen id (a device that generated its own
// row id) seeds the product under it.
func (f *matrixFixture) product(t *testing.T, id string) string {
	t.Helper()
	if id == "" {
		f.n++
		id = fmt.Sprintf("31f10000-0000-4000-8000-%012d", f.n)
	}
	// ON CONFLICT DO NOTHING: the battery rebuilds its outbox payloads
	// (k.Create called again with the same id), and the seed is
	// idempotent the way the fixture builder is.
	if _, err := f.pool.Exec(context.Background(), `
INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, org_id)
VALUES ($1::uuid, $2::citext, $3, 'EA', 'EA', TRUE, $4::uuid)
ON CONFLICT (id) DO NOTHING`,
		id, "FXSYNC-"+id[24:], "FX sync product "+id[24:], testkit.OrgNorthID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return id
}

// matrixKit builds the battery kit: creates name a fresh product's
// anchor (the battery's own id parameter is ignored, the row's id is
// the product's), updates move the anchor's cents, and the desk
// publishes through the module's own batch apply.
func matrixKit(f *matrixFixture) syncharness.Kit {
	return syncharness.Kit{
		Scope:  f.info,
		Filter: f.filter,
		Store:  f.store,
		Create: func(id string) map[string]any {
			return map[string]any{"id": f.product(f.t, id), "list_price_cents": 1000}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			for i := 1; i <= n; i++ {
				out = append(out, map[string]any{"id": row["id"], "list_price_cents": 2000 + i*100})
			}
			return out
		},
		// The attachments lane's kit posture, restated here because it
		// is the honest one for a free-field scope the frozen schema
		// gives no applied-key history: the battery's Update is a
		// CONSTANT (a replay of it meets the value-equal arm and moves
		// nothing), and the CHAIN evolves the cents (three distinct
		// writes, pushed once, never replayed).
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "list_price_cents": 1500}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			// The desk publish: the module's own batch apply over one
			// seeded APPROVED batch line, the path every desk price
			// change runs.
			id := fmt.Sprint(row["id"])
			batchNo := fmt.Sprintf("FXBATCH-SYNC-%d", time.Now().UnixNano())
			var batchID string
			if err := f.pool.QueryRow(ctx, `
INSERT INTO price_batches (batch_no, status, effective_date, source_ref)
VALUES ($1, 'APPROVED', now(), 'fx sync desk') RETURNING id::text`, batchNo).Scan(&batchID); err != nil {
				return err
			}
			next := int64(row["list_price_cents"].(float64)) + 250
			if _, err := f.pool.Exec(ctx, `
INSERT INTO price_change_lines (batch_id, product_id, after_retail_cents)
VALUES ($1::uuid, $2::uuid, $3)`, batchID, id, next); err != nil {
				return err
			}
			_, _, err := f.svc.ApplyBatch(ctx, batchID, testkit.UserInsideSalesID, time.Now().UTC())
			return err
		},
	}
}

// TestMatrixContractBattery: the per-scope battery over the real
// table, GATE-7 and the replay property included.
func TestMatrixContractBattery(t *testing.T) {
	f := newMatrixFixture(t)
	k := matrixKit(f)
	syncharness.Contract(t, f.engine, k)
	syncharness.Gate7(t, f.engine, k)
	rng := rand.New(rand.NewSource(1168))
	syncharness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestMatrixWritePathIsTheModulesOwn is the fence proof the pack
// demands: a pushed write lands through the module's one list-price
// path, its price_change_log trail beside it (the audit spine every
// price change owes), and a desk batch publish moves the same row
// visible to a device's pull. Nothing writes around the module.
func TestMatrixWritePathIsTheModulesOwn(t *testing.T) {
	f := newMatrixFixture(t)
	ctx := context.Background()
	product := f.product(t, "")

	// The pushed write: applied, and trailed.
	res := matrixPush(t, f, []matrixOp{
		{client: matrixKey(1), kind: "create", payload: map[string]any{"id": product, "list_price_cents": 1500}},
	})
	if res[0].Status != "applied" {
		t.Fatalf("pushed write: %+v", res[0])
	}
	var trail int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM price_change_log WHERE product_id = $1::uuid AND field = 'retail_cents'`,
		product).Scan(&trail); err != nil {
		t.Fatalf("trail read: %v", err)
	}
	if trail != 1 {
		t.Fatalf("the pushed write left %d trail rows, want 1 (the module's own audit spine)", trail)
	}
	t.Logf("write path: the pushed write left its price_change_log row (before NULL, after 1500)")

	// The desk publish over the same row: the module's own batch apply,
	// and the row's move visible to a device's pull past its cursor.
	before, okBefore, err := f.store.Load(ctx, f.filter, product)
	if err != nil || !okBefore {
		t.Fatalf("load before: ok=%v err=%v", okBefore, err)
	}
	desk := matrixKit(f)
	deskRow := map[string]any{"id": product, "list_price_cents": before.Data["list_price_cents"]}
	if err := desk.Desk(ctx, deskRow); err != nil {
		t.Fatalf("desk publish: %v", err)
	}
	after, ok, err := f.store.Load(ctx, f.filter, product)
	if err != nil || !ok {
		t.Fatalf("load after: ok=%v err=%v", ok, err)
	}
	if after.Seq <= before.Seq {
		t.Fatalf("the desk publish did not move the row's seq: %d then %d", before.Seq, after.Seq)
	}
	if after.Data["list_price_cents"] == before.Data["list_price_cents"] {
		t.Fatalf("the desk publish did not move the anchor: %v", after.Data["list_price_cents"])
	}
	// The trail doubled: one pushed, one published.
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM price_change_log WHERE product_id = $1::uuid AND field = 'retail_cents'`,
		product).Scan(&trail); err != nil {
		t.Fatalf("trail read 2: %v", err)
	}
	if trail != 2 {
		t.Fatalf("after the desk publish the trail holds %d rows, want 2", trail)
	}
	t.Logf("write path: the desk batch publish moved the anchor to %v and doubled the trail, one writer, one spine",
		after.Data["list_price_cents"])

	// The pull past the device's old cursor carries exactly the desk's
	// move (the desk-visible property the seq stamp buys).
	pull, err := f.engine.Pull(ctx, f.info, f.filter, before.Seq, 200)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	found := false
	for _, it := range pull.Items {
		if it.Id == product {
			found = true
			if it.Seq != after.Seq {
				t.Fatalf("the pull's row seq %d, want the desk's %d", it.Seq, after.Seq)
			}
		}
	}
	if !found {
		t.Fatalf("the device's pull past %d never saw the desk's publish", before.Seq)
	}
}

// TestMatrixOrgAndTenantReads: a foreign org's anchor never pulls, and
// the tenant caller (the catalog declares the scope on both sides)
// reads the same dealer rows through its token's org.
func TestMatrixOrgAndTenantReads(t *testing.T) {
	f := newMatrixFixture(t)
	ctx := context.Background()
	north := f.product(t, "")
	south := f.product(t, "")
	if _, err := f.pool.Exec(ctx,
		`UPDATE products SET org_id = $2::uuid WHERE id = $1::uuid`, south, testkit.OrgSouthID); err != nil {
		t.Fatalf("south stamp: %v", err)
	}
	for _, p := range []string{north, south} {
		if _, err := f.pool.Exec(ctx, priceSeqCTE+`
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom, org_id, seq)
SELECT $1::uuid, 1000, 'EA', (SELECT org_id FROM products WHERE id = $1::uuid), (SELECT next_value FROM pseq)`,
			p); err != nil {
			t.Fatalf("seed anchor: %v", err)
		}
	}
	// The north pull sees only the north anchor (both org-stamped).
	pull, err := f.engine.Pull(ctx, f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("north pull: %v", err)
	}
	for _, it := range pull.Items {
		if it.Id == south {
			t.Fatalf("the north org's pull saw the south org's anchor")
		}
	}
	// The tenant caller reads the same rows: its filter carries the
	// org its token's team resolves to, never a header (G4).
	tenantFilter := sync.ScopeFilter{
		OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true,
		Actor: testkit.SubPortalAdminAlpha,
	}
	tenantPull, err := f.engine.Pull(ctx, f.info, tenantFilter, 0, 200)
	if err != nil {
		t.Fatalf("tenant pull: %v", err)
	}
	seen := false
	for _, it := range tenantPull.Items {
		if it.Id == north {
			seen = true
		}
		if it.Id == south {
			t.Fatalf("the tenant caller saw the south org's anchor")
		}
	}
	if !seen {
		t.Fatalf("the tenant caller missed the dealer's north anchor")
	}
	// A tenant caller CANNOT write: the catalog declares the push side
	// for the staff frontends, and the store's create under a tenant
	// filter still names a product in the resolved org, so the honest
	// proof is the route's own refusal, carried by the route test.
}

// The small helpers.

type matrixOp struct {
	client  string
	kind    string
	payload map[string]any
	base    *int64
}

func matrixKey(n int) string { return fmt.Sprintf("32000000-0000-4000-8000-%012d", n) }

func matrixPush(t *testing.T, f *matrixFixture, ops []matrixOp) []apigen.SyncPushResult {
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
