//go:build integration

// syncscope_integration_test.go: the quotes sync scope against a real
// database (HH-3.5 work item #168's acceptance): the per-scope contract
// battery, GATE-7 and the replay property over the quote module's own
// table, the D14/D15 document-semantics proof (a merged field never
// resurrects an expired document's price), org isolation with the
// tenant filter failing closed, the module's own invariants holding
// through sync, the audit rows, and the operator walkthrough (a quote
// edited offline at a counter with no signal, a price change published
// from the desk, the replay merging the quote's fields while the
// expired price stays refused, the quote desk reading one consistent
// state after).
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync168?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/quote/
//
// The database is a throwaway scratch; never a shared proof database.
// Nothing here fakes the module whose correctness the acceptance
// depends on: the Store under test is the quote SyncStore, the desk
// legs run the module's own repository paths, and every fixture value
// is invented.
package quote

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	syncharness "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// syncFixture is the quotes world: the module's own repository (the
// desk legs run its own paths), the sync store over it, and the engine
// wired exactly as the composition root wires it.
type syncFixture struct {
	pool   *pgxpool.Pool
	repo   *Repository
	store  *SyncStore
	engine *sync.Engine
	info   sync.ScopeInfo
	filter sync.ScopeFilter
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE org_id IN ($1::uuid, $2::uuid)`,
			testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(c, `DELETE FROM price_change_log WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXSYNC-%')`)
		_, _ = pool.Exec(c, `DELETE FROM pricing_list_prices WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXSYNC-%')`)
		_, _ = pool.Exec(c, `DELETE FROM price_batches WHERE batch_no LIKE 'FXBATCH-QW-%'`)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE sku LIKE 'FXSYNC-%'`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'price_matrix'`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'quotes'`)
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_type = 'sync.quotes'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	repo := NewRepository(pool)
	store := NewSyncStore(pool, linkedSyncUser(pool))
	engine := sync.NewEngine().WithAudit(quoteSyncAudit{svc: audit.NewService(audit.NewRepository(pool), nil)}, nil)
	if err := engine.RegisterScope("quotes", store); err != nil {
		t.Fatalf("register quotes: %v", err)
	}
	info, ok := sync.Lookup("quotes")
	if !ok {
		t.Fatalf("quotes missing from the registry")
	}
	return &syncFixture{
		pool: pool, repo: repo, store: store, engine: engine, info: info,
		filter: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales},
	}
}

// linkedSyncUser resolves the ERP user row for a platform subject, the
// same shape the composition root's linker carries.
func linkedSyncUser(pool *pgxpool.Pool) func(ctx context.Context, platformUserID string) (string, bool) {
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

// quoteSyncAudit maps the engine's entries onto the audit module's rows
// for the scope and op legs only. It records no actor (actor_id empty,
// no actor_subject): the actor mapping is cmd/core's syncAuditSeam
// alone, proven by the composition test in cmd/core
// (sync_audit_wire_test.go).
type quoteSyncAudit struct{ svc *audit.Service }

func (a quoteSyncAudit) RecordSync(ctx context.Context, e sync.AuditEntry) error {
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

// syncDate parses the projection's valid_until (RFC3339 from the json
// round trip) or returns today when the document never expires.
func syncDate(v any) time.Time {
	s, _ := v.(string)
	if d, err := time.Parse(time.RFC3339, s); err == nil {
		return d
	}
	return time.Now().UTC()
}

// syncKit builds the battery kit: creates are born against the north
// branch for the fixture's alpha customer, updates move the validity a
// week out (each write a changing fact, the ascending-seq leg's
// requirement), and the desk edits the expiry through the module's own
// guarded path.
func syncKit(f *syncFixture) syncharness.Kit {
	return syncharness.Kit{
		Scope:  f.info,
		Filter: f.filter,
		Store:  f.store,
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"customer_id": testkit.TenantAlphaID,
				"branch_id":   testkit.BranchNorthAID,
				"valid_until": time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02"),
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		// The attachments lane's kit posture: a CONSTANT Update (a
		// replay meets the value-equal arm and moves nothing) and an
		// evolving Chain, pushed once and never replayed.
		Update: func(row map[string]any) map[string]any {
			// Midnight-normalized: the column is a DATE, and the
			// battery's field compare reads the projection's own
			// serialized form back.
			return map[string]any{
				"id":          row["id"],
				"valid_until": time.Now().UTC().AddDate(0, 0, 120).Truncate(24 * time.Hour).Format(time.RFC3339),
			}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			base := syncDate(row["valid_until"])
			out := make([]map[string]any, 0, n)
			for i := 1; i <= n; i++ {
				out = append(out, map[string]any{
					"id":          row["id"],
					"valid_until": base.AddDate(0, 0, i).UTC().Format(time.RFC3339),
				})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			to := syncDate(row["valid_until"]).AddDate(0, 0, 14).Format("2006-01-02")
			_, _, _, err := f.repo.UpdateValidUntilGuarded(ctx, fmt.Sprint(row["id"]), to,
				[]string{"DRAFT", "SENT"}, testkit.UserInsideSalesID, quoteDateHooks{})
			return err
		},
	}
}

// TestQuotesContractBattery: the per-scope battery over the real
// table, GATE-7 and the replay property included.
func TestQuotesContractBattery(t *testing.T) {
	f := newSyncFixture(t)
	k := syncKit(f)
	syncharness.Contract(t, f.engine, k)
	syncharness.Gate7(t, f.engine, k)
	rng := rand.New(rand.NewSource(168))
	syncharness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestQuotesExpiredDocumentStaysExpired is the D14/D15 proof the pack
// demands: the document's validity is the server's, and no pushed
// write, fresh or merged, resurrects an expired document's price.
func TestQuotesExpiredDocumentStaysExpired(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	// A live document the device holds.
	id := syncCreate(t, f, syncKit(f).Create(""))
	live := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
	past := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	// The document's validity lapses server-side (D14: expiry is
	// document-level and computed, no status is written).
	if _, err := f.pool.Exec(ctx,
		`UPDATE quotes SET valid_until = $2::date WHERE id = $1::uuid`, id, past); err != nil {
		t.Fatalf("lapse: %v", err)
	}

	// A fresh pushed extension refuses: the module's own expiry guard,
	// verbatim, inside the adapter's Write.
	res := syncPush(t, f, []syncOp{
		{client: syncKey(2), kind: "update", payload: map[string]any{"id": id, "valid_until": live}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("fresh extension on an expired document answered %+v, want rejected", res[0])
	}
	t.Logf("d14: fresh extension refused (%s)", *res[0].Error)

	// The MERGED write refuses too: the desk moves the row (a fresh seq
	// from the scope's own allocator, the same upsert every module write
	// path runs), the device pushes its stale edit, and the engine's
	// last_write_wins_by_field resolution hands the adapter the merged
	// fields, whose expiry guard refuses them whole. A merged field
	// never resurrects an expired document's price.
	var deskSeq int64
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ('quotes')
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`).Scan(&deskSeq); err != nil {
		t.Fatalf("desk seq: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`UPDATE quotes SET updated_at = now(), seq = $2 WHERE id = $1::uuid`, id, deskSeq); err != nil {
		t.Fatalf("desk move: %v", err)
	}
	base := deskSeq - 1
	res = syncPush(t, f, []syncOp{
		{client: syncKey(3), kind: "update", base: &base,
			payload: map[string]any{"id": id, "valid_until": live}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("merged extension on an expired document answered %+v, want rejected", res[0])
	}
	t.Logf("d14: merged extension refused (%s)", *res[0].Error)

	// The document is exactly what it was: still expired, still DRAFT,
	// its prices still refused at the guard.
	row, ok, err := f.store.Load(ctx, f.filter, id)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got := syncDate(row.Data["valid_until"]).Format("2006-01-02"); got != past {
		t.Fatalf("the expired document's validity moved: %s, want %s", got, past)
	}
	if row.Data["status"] != "DRAFT" {
		t.Fatalf("status: %v", row.Data["status"])
	}
}

// TestQuotesModuleInvariantsHold: the adapter enforces the module's
// own rules through sync: the frozen matrix (a SENT quote's expiry is
// editable, an ACCEPTED one's is not), the unwritable field refusal,
// and the withdrawal refusal on a seen document.
func TestQuotesModuleInvariantsHold(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	id := syncCreate(t, f, syncKit(f).Create(""))

	// An unwritable field refuses whole.
	res := syncPush(t, f, []syncOp{
		{client: syncKey(2), kind: "update", payload: map[string]any{"id": id, "total_cents": 999999}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("money field write answered %+v, want rejected", res[0])
	}
	t.Logf("invariant: a money leg refused through sync (%s)", *res[0].Error)

	// An ACCEPTED quote is frozen (R2): the matrix refuses the edit.
	if _, err := f.pool.Exec(ctx,
		`UPDATE quotes SET status = 'ACCEPTED' WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("accept: %v", err)
	}
	res = syncPush(t, f, []syncOp{
		{client: syncKey(3), kind: "update",
			payload: map[string]any{"id": id, "valid_until": time.Now().UTC().AddDate(0, 0, 40).Format("2006-01-02")}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("frozen-matrix write answered %+v, want rejected", res[0])
	}
	// The withdrawal refuses on a seen document.
	res = syncPush(t, f, []syncOp{
		{client: syncKey(4), kind: "delete", payload: map[string]any{"id": id}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("withdrawal of an ACCEPTED quote answered %+v, want rejected", res[0])
	}
}

// TestQuotesOrgIsolation: rows of another org never pull through the
// north filter, and a tenant filter answers nothing (the adapter fails
// closed; the route's 403 is the route test's leg).
func TestQuotesOrgIsolation(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	northID := syncCreate(t, f, syncKit(f).Create(""))
	// A south quote through the module's own insert, org stamped from
	// the branch.
	southID, _, err := f.repo.CreateQuote(ctx, QuoteCreate{
		CustomerID: testkit.TenantGammaID, BranchID: testkit.BranchSouthAID, CreatedBy: "",
	})
	if err != nil {
		t.Fatalf("south create: %v", err)
	}
	pull, err := f.engine.Pull(ctx, f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("north pull: %v", err)
	}
	for _, it := range pull.Items {
		if it.Id == southID {
			t.Fatalf("the north org's pull saw the south org's quote")
		}
	}
	found := false
	for _, it := range pull.Items {
		if it.Id == northID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the north org's pull missed its own quote")
	}
	// The south quote IS visible through the south filter (the module
	// path stamped its org from the branch).
	southFilter := sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubInsideSales}
	southPull, err := f.engine.Pull(ctx, f.info, southFilter, 0, 200)
	if err != nil {
		t.Fatalf("south pull: %v", err)
	}
	seen := false
	for _, it := range southPull.Items {
		if it.Id == southID {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the south org's pull missed the desk-created quote")
	}
	// The tenant filter answers nothing.
	tenantFilter := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha}
	tenantPull, err := f.engine.Pull(ctx, f.info, tenantFilter, 0, 200)
	if err != nil {
		t.Fatalf("tenant pull: %v", err)
	}
	if len(tenantPull.Items) != 0 || len(tenantPull.Tombstones) != 0 {
		t.Fatalf("a tenant filter read org rows through the quotes scope: %+v", tenantPull)
	}
}

// TestInternalWalkthrough is the pack's operator walkthrough, scripted
// against the seeded database: a quote edited offline at a counter
// with no signal, a price change published from the desk, the replay
// merging the quote's fields while the expired price stays refused,
// and the quote desk reading one consistent state after.
func TestInternalWalkthrough(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	// Step 1: the desk opens a quote for the alpha customer through the
	// module's own path (org and seq stamped beside the row).
	deskID, _, err := f.repo.CreateQuote(ctx, QuoteCreate{
		CustomerID: testkit.TenantAlphaID, BranchID: testkit.BranchNorthAID,
		ValidUntil: time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02"),
		CreatedBy:  testkit.UserInsideSalesID,
	})
	if err != nil {
		t.Fatalf("desk create: %v", err)
	}
	t.Logf("walkthrough 1: the desk opened quote %s, valid thirty days", deskID)

	// Step 2: the counter device pulls the quote list.
	pull, err := f.engine.Pull(ctx, f.info, f.filter, 0, 50)
	if err != nil {
		t.Fatalf("device pull: %v", err)
	}
	var device apigen.SyncEntity
	for _, it := range pull.Items {
		if it.Id == deskID {
			device = it
		}
	}
	if device.Id == "" {
		t.Fatalf("the device never saw the desk's quote: %+v", pull.Items)
	}
	baseSeq := device.Seq
	t.Logf("walkthrough 2: the device pulled the quote at seq %d", baseSeq)

	// Step 3: no signal. The counter edits the quote's validity (the
	// one header field the module PATCHes), holding the outbox.
	newValidity := time.Now().UTC().AddDate(0, 0, 60).Format("2006-01-02")
	outbox := []syncOp{
		{client: syncKey(11), kind: "update", base: &baseSeq,
			payload: map[string]any{"id": deskID, "valid_until": newValidity}},
	}

	// Step 4: while the counter is dark, the desk publishes a price
	// change through the pricing module's own batch apply (the one
	// list-price write path), and moves the quote itself (its own
	// guarded expiry edit, a fresh seq the device has not seen).
	// The walkthrough's own product (org-stamped, fixture-cleaned), so
	// the desk's publish touches nothing another suite seeded.
	var walkProduct string
	if err := f.pool.QueryRow(ctx, `
INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, org_id)
VALUES ('31f10000-0000-4000-8000-000000000099', 'FXSYNC-QW', 'FX sync walkthrough product', 'EA', 'EA', TRUE, $1::uuid)
ON CONFLICT (id) DO UPDATE SET sku = EXCLUDED.sku RETURNING id::text`,
		testkit.OrgNorthID).Scan(&walkProduct); err != nil {
		t.Fatalf("seed walkthrough product: %v", err)
	}
	batchNo := fmt.Sprintf("FXBATCH-QW-%d", time.Now().UnixNano())
	var batchID string
	if err := f.pool.QueryRow(ctx, `
INSERT INTO price_batches (batch_no, status, effective_date, source_ref)
VALUES ($1, 'APPROVED', now(), 'fx sync walkthrough') RETURNING id::text`, batchNo).Scan(&batchID); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
INSERT INTO price_change_lines (batch_id, product_id, after_retail_cents)
VALUES ($1::uuid, $2::uuid, 4321)`, batchID, walkProduct); err != nil {
		t.Fatalf("seed batch line: %v", err)
	}
	pricingSvc := pricing.NewService(pricing.NewRepository(f.pool))
	if _, applied, err := pricingSvc.ApplyBatch(ctx, batchID, testkit.UserInsideSalesID, time.Now().UTC()); err != nil || applied != 1 {
		t.Fatalf("desk price publish: applied=%d err=%v", applied, err)
	}
	t.Logf("walkthrough 4: the desk published a price change (1 anchor applied through the pricing module's own path)")
	deskMove := time.Now().UTC().AddDate(0, 0, 45).Format("2006-01-02")
	if _, _, _, err := f.repo.UpdateValidUntilGuarded(ctx, deskID, deskMove,
		[]string{"DRAFT", "SENT"}, testkit.UserInsideSalesID, quoteDateHooks{}); err != nil {
		t.Fatalf("desk quote edit: %v", err)
	}
	t.Logf("walkthrough 4: the desk re-timed the quote to %s while the counter was dark", deskMove)

	// Step 5: signal returns. The outbox replays: the stale edit merges
	// per field (the device's validity lands, every undeclared field
	// stays the server's, applied true with the merge envelope).
	res := syncPush(t, f, outbox)
	if len(res) != 1 || res[0].Status != "applied" || res[0].Conflict == nil {
		t.Fatalf("walkthrough 5: the replay answered %+v", res[0])
	}
	c := res[0].Conflict
	if c.Policy != string(sync.PolicyLWWByField) || c.Applied == nil || !*c.Applied {
		t.Fatalf("walkthrough 5: merge envelope %+v", c)
	}
	if got := c.ServerState["valid_until"]; fmt.Sprint(got)[:10] != newValidity {
		t.Fatalf("walkthrough 5: merged validity %v, want %s", got, newValidity)
	}
	row, ok, err := f.store.Load(ctx, f.filter, deskID)
	if err != nil || !ok {
		t.Fatalf("walkthrough 5: load: ok=%v err=%v", ok, err)
	}
	if fmt.Sprint(row.Data["valid_until"])[:10] != newValidity {
		t.Fatalf("walkthrough 5: the row's validity %v, want the merged %s", row.Data["valid_until"], newValidity)
	}
	t.Logf("walkthrough 5: the replay merged the validity to %s, the server's every other field standing", newValidity)

	// Step 6: the expired price stays refused. A second document whose
	// validity lapsed server-side refuses the counter's edit, fresh or
	// merged, exactly as the module's own guard refuses it.
	expiredID, _, err := f.repo.CreateQuote(ctx, QuoteCreate{
		CustomerID: testkit.TenantAlphaID, BranchID: testkit.BranchNorthAID,
		ValidUntil: time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02"),
		CreatedBy:  testkit.UserInsideSalesID,
	})
	if err != nil {
		t.Fatalf("expired create: %v", err)
	}
	res = syncPush(t, f, []syncOp{
		{client: syncKey(12), kind: "update",
			payload: map[string]any{"id": expiredID, "valid_until": newValidity}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("walkthrough 6: the expired document's edit answered %+v, want rejected", res[0])
	}
	t.Logf("walkthrough 6: the expired document refused the edit (%s), its prices stay refused", *res[0].Error)

	// Step 7: the quote desk reads one consistent state: one pull of
	// the quotes scope shows the merged document, one pull of the price
	// matrix scope shows the published anchor, and the two agree with
	// the rows.
	quotes, err := f.engine.Pull(ctx, f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("desk quotes pull: %v", err)
	}
	var merged apigen.SyncEntity
	for _, it := range quotes.Items {
		if it.Id == deskID {
			merged = it
		}
	}
	if merged.Id == "" || fmt.Sprint(merged.Data["valid_until"])[:10] != newValidity {
		t.Fatalf("walkthrough 7: the desk's read of the quote: %+v", merged)
	}
	matrixStore := pricing.NewSyncStore(f.pool, linkedSyncUser(f.pool))
	matrixEngine := sync.NewEngine()
	if err := matrixEngine.RegisterScope("price_matrix", matrixStore); err != nil {
		t.Fatalf("register price_matrix: %v", err)
	}
	matrixInfo, _ := sync.Lookup("price_matrix")
	matrix, err := matrixEngine.Pull(ctx, matrixInfo, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("desk matrix pull: %v", err)
	}
	var anchor apigen.SyncEntity
	for _, it := range matrix.Items {
		if it.Id == walkProduct {
			anchor = it
		}
	}
	if anchor.Id == "" || anchor.Data["list_price_cents"] != float64(4321) {
		t.Fatalf("walkthrough 7: the desk's read of the anchor: %+v", anchor)
	}
	t.Logf("walkthrough 7: the desk reads one consistent state: quote %s at %s, anchor at %v cents (seq %d)",
		merged.Data["quote_no"], newValidity, anchor.Data["list_price_cents"], anchor.Seq)

	// Step 8: a duplicate replay of the counter's outbox changes
	// nothing (GATE-7's property at the walkthrough's scale).
	before := walkthroughSnap(t, f)
	again := syncPush(t, f, outbox)
	if len(again) != 1 || again[0].Status != "applied" {
		t.Fatalf("walkthrough 8: the duplicate replay answered %+v", again[0])
	}
	after := walkthroughSnap(t, f)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("walkthrough 8: the duplicate replay changed the state: %v then %v", before, after)
	}
	t.Logf("walkthrough 8: a duplicate replay applied through dedup, the state unchanged")
}

// The small helpers the walkthrough and proofs share.

type syncOp struct {
	client  string
	kind    string
	payload map[string]any
	base    *int64
}

func syncKey(n int) string { return fmt.Sprintf("31000000-0000-4000-8000-%012d", n) }

// syncCreate pushes one create and returns the row id (the create
// result carries the seq; the id reads off the feed).
func syncCreate(t *testing.T, f *syncFixture, payload map[string]any) string {
	t.Helper()
	res := syncPush(t, f, []syncOp{{client: syncKey(0), kind: "create", payload: payload}})
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
	t.Fatalf("sync create: the new row never pulled at seq %d", *res[0].Seq)
	return ""
}

// syncPush pushes one outbox under the fixture's filter.
func syncPush(t *testing.T, f *syncFixture, ops []syncOp) []apigen.SyncPushResult {
	t.Helper()
	out := make([]apigen.SyncPushOperation, 0, len(ops))
	for _, o := range ops {
		out = append(out, apigen.SyncPushOperation{ClientId: o.client, Op: o.kind, Payload: o.payload, BaseSeq: o.base})
	}
	resp, err := f.engine.Push(context.Background(), f.info, f.filter, out)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(resp.Results) != len(ops) {
		t.Fatalf("push answered %d results for %d ops", len(resp.Results), len(ops))
	}
	return resp.Results
}

// walkthroughSnap takes the quotes scope's snapshot (the state compare).
func walkthroughSnap(t *testing.T, f *syncFixture) map[string]any {
	t.Helper()
	resp, err := f.engine.Pull(context.Background(), f.info, f.filter, 0, 200)
	if err != nil {
		t.Fatalf("snap pull: %v", err)
	}
	state := map[string]any{"last_seq": resp.LastSeq}
	for _, it := range resp.Items {
		state[it.Id] = fmt.Sprintf("%d|%v|%v", it.Seq, it.Data["status"], it.Data["valid_until"])
	}
	for _, tb := range resp.Tombstones {
		state[tb.Id] = fmt.Sprintf("tombstone|%d", tb.Seq)
	}
	return state
}
