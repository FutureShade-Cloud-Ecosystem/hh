//go:build integration

// gap_scopes_integration_test.go: the three scopes the Phase D split
// dropped, against a real database: approvals (server_wins, org) runs
// the shared per-scope battery whole (Contract, GATE-7, the replay
// property) over the detail-note surface, with the decision legs the
// rail's own rules own proven beside it; invoices (server_wins, both
// tenancies) and payments (server_wins, tenant) are create-only
// doors by their owning modules' money invariants, so each runs the
// battery's legs that an honest money document admits: the pull
// shape, since, cursor and high-water mark, the create legs (seq
// assignment, in-order replay, dedup through the module's own
// convergence), the stale-write conflict envelope, the per-tenant
// isolation, and the refusals (an update or delete on a settled money
// row answers the operation's rejected result, never a fabricated
// apply). The operator walkthroughs are scripted step by step.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35syncgap?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/sync/...
package sync_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The gap lane's dedicated rows, the invented 3ef1 pattern like every
// fixture id: a fulfilled order the writer bills and two payable
// invoices (one the stub approves, one whose 13-cent balance triggers
// its documented decline rule). Dedicated because the shared fixture
// rows are other packages' integration surface too, and a writer that
// converges on the order key would converge on THEIR document.
const (
	gapOrderID          = "3ef10000-0000-4000-8000-0000000000f1"
	gapOrderLineID      = "3ef10000-0000-4000-8000-0000000000f2"
	gapOrderNo          = "FXORD-GAP-1"
	gapInvoicePayID     = "3ef10000-0000-4000-8000-00000000b102"
	gapInvoiceDeclineID = "3ef10000-0000-4000-8000-00000000b101"
)

// gapInvoiceWriterKey is the InvoiceWriter's own idempotency key over
// the lane's dedicated order (invoice_writer.go's order-invoice:<order>
// shape). Every writer-minted document this lane's tests raise is the
// ONE document that key converges on, so the cleanup can name the
// lane's invoices exactly and never reach into another package's
// '#INV-' rows on the shared gate.
const gapInvoiceWriterKey = "order-invoice:" + gapOrderID

// gapOrderSource adapts the order module's invoice facts onto the
// billing seam, the same six-line conversion the composition root's
// adapter carries (order's shapes and billing's are deliberately not
// imports of one another, invoice_source.go's own argument).
type gapOrderSource struct{ svc *order.Service }

func (a gapOrderSource) InvoiceableOrder(ctx context.Context, orderID string, groupIDs []string) (billing.OrderInvoiceFacts, error) {
	f, err := a.svc.InvoiceableOrder(ctx, orderID, groupIDs)
	if err != nil {
		return billing.OrderInvoiceFacts{}, err
	}
	out := billing.OrderInvoiceFacts{
		OrderID: f.OrderID, OrderNo: f.OrderNo, CustomerID: f.CustomerID,
		BranchID: f.BranchID, TermsDays: f.TermsDays, TaxReasonCode: f.TaxReasonCode,
		ClosesOrder: f.ClosesOrder, GroupIDs: f.GroupIDs,
	}
	for _, l := range f.Lines {
		out.Lines = append(out.Lines, billing.OrderInvoiceLine{
			LineNo: l.LineNo, OrderLineID: l.OrderLineID, ProductID: l.ProductID,
			Description: l.Description, QtyEntered: l.QtyEntered, UOMEntered: l.UOMEntered,
			QtyBase: l.QtyBase, UnitPriceCents: l.UnitPriceCents, PriceUOM: l.PriceUOM,
			LineTotalCents: l.LineTotalCents,
		})
	}
	return out, nil
}

func (a gapOrderSource) MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error {
	return a.svc.MarkGroupsInvoiced(ctx, groupIDs, invoiceID, at)
}

// theGapFixture wires the three scopes on one engine over the scratch
// database, exactly as the composition root wires them: the approvals
// rail's store with the staff linker, the invoices store over the
// money tail's writer (the module's own shipped flat assessor in the
// test; the composition root binds the real tax engine behind the
// same seam), and the payments store over the portal pay service on
// the deterministic stub gateway.
type theGapFixture struct {
	pool   *pgxpool.Pool
	engine *sync.Engine
	rail   *identity.Approvals
	writer *billing.InvoiceWriter
	paySvc *payments.Service
	apprSt sync.Store
	invSt  sync.Store
	paySt  sync.Store
}

func newGapFixture(t *testing.T) *theGapFixture {
	t.Helper()
	pool := openGapPool(t)
	linker := linkedUser(pool)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	writer := billing.NewInvoiceWriter(pool).
		WithOrderSource(gapOrderSource{svc: order.NewService(order.NewRepository(pool))})
	paySvc := payments.NewService(payments.NewRepository(pool), payments.NewStubGateway())
	engine := sync.NewEngine().WithAudit(intAudit{svc: audit.NewService(audit.NewRepository(pool), nil)}, nil)
	apprStore := identity.NewApprovalsSyncStore(pool, linker)
	invStore := billing.NewInvoicesSyncStore(pool, writer, identity.NewApprovalRepository(pool))
	payStore := payments.NewPaymentsSyncStore(pool, paySvc)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	must(engine.RegisterScope("approvals", apprStore))
	must(engine.RegisterScope("invoices", invStore))
	must(engine.RegisterScope("payments", payStore))
	return &theGapFixture{
		pool: pool, engine: engine, rail: rail, writer: writer, paySvc: paySvc,
		apprSt: apprStore, invSt: invStore, paySt: payStore,
	}
}

// openGapPool opens the scratch database (the pilot's polite skip
// when the gate variable is unset), builds the kit fixtures, seeds
// this lane's payable legs, and cleans this lane's rows beside the
// kit's own.
func openGapPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@127.0.0.1:55439/hh35syncgap?sslmode=disable"
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
	seed := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), q, args...); err != nil {
			t.Fatalf("gap seed: %v (statement %.60s)", err, q)
		}
	}
	// The dedicated payable invoice (FINALIZED with a balance the stub
	// approves) and the 13-cent decline-rule payable, both alpha's own.
	seed(`INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, due_date,
		subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		VALUES ($1::uuid, 'FXINV-GAP-PAY', $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30,
		10000, 1300, 11300, 11300, now()),
		       ($4::uuid, 'FXINV-GAP-DEC', $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30,
		13, 0, 13, 13, now())
		ON CONFLICT (id) DO NOTHING`,
		gapInvoicePayID, testkit.TenantAlphaID, testkit.BranchNorthAID, gapInvoiceDeclineID)
	// The dedicated fulfilled order the writer bills: one line, EA
	// throughout, fulfilled in full.
	seed(`INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, org_id, tenant_id)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'CONFIRMED', 'PICKUP', $5::uuid, $3::uuid)
		ON CONFLICT (id) DO NOTHING`,
		gapOrderID, gapOrderNo, testkit.TenantAlphaID, testkit.BranchNorthAID, testkit.OrgNorthID)
	seed(`INSERT INTO order_lines (id, order_id, line_no, product_id, description, qty_entered,
		uom_entered, qty_base, fulfilled_base, unit_price_cents, price_uom, line_total_cents, org_id, tenant_id)
		VALUES ($1::uuid, $2::uuid, 1, $3::uuid, 'Fixture gap line', 10, 'EA', 10, 10, 1000, 'EA', 10000, $4::uuid, $5::uuid)
		ON CONFLICT (id) DO NOTHING`,
		gapOrderLineID, gapOrderID, testkit.ProductID, testkit.OrgNorthID, testkit.TenantAlphaID)
	hideFixtureRowsFromFeeds(t, pool)
	t.Cleanup(func() {
		ctx := context.Background()
		exec := func(q string, args ...any) { _, _ = pool.Exec(ctx, q, args...) }
		exec(`DELETE FROM permission_approval_requests WHERE branch_id IN ($1::uuid, $2::uuid, $3::uuid)`,
			testkit.BranchNorthAID, testkit.BranchNorthBID, testkit.BranchSouthAID)
		exec(`DELETE FROM payment_intents WHERE customer_id IN ($1::uuid, $2::uuid)`,
			testkit.TenantAlphaID, testkit.TenantBetaID)
		// The allocations RESTRICT invoice deletes, so they go first
		// (every allocation this lane's documents carry). Scoped to the
		// lane's own documents, the seeded payables by id and the writer
		// document by its convergence key: a sweep of every '#INV-'
		// invoice_no would reach into other packages' leftovers on the
		// shared gate, where one foreign reference (the POS sale legs
		// leave returns that RESTRICT their invoices) fails the whole
		// statement and the swallow below hides the failure.
		exec(`DELETE FROM payment_allocations WHERE invoice_id IN
			(SELECT id FROM invoices WHERE idempotency_key = $3 OR id IN ($1::uuid, $2::uuid))`,
			gapInvoicePayID, gapInvoiceDeclineID, gapInvoiceWriterKey)
		// Scoped to this lane's tenants: a bare seq predicate would
		// sweep other packages' residue, and a SETTLED intent's ON
		// DELETE SET NULL then violates the settled-has-payment CHECK
		// and fails the whole statement.
		exec(`DELETE FROM payments WHERE seq IS NOT NULL AND customer_id IN ($1::uuid, $2::uuid)`,
			testkit.TenantAlphaID, testkit.TenantBetaID)
		exec(`DELETE FROM customer_transactions WHERE customer_id IN ($1::uuid, $2::uuid)`,
			testkit.TenantAlphaID, testkit.TenantBetaID)
		// Scoped to the lane's own documents, for the same reason: a
		// blocked sweep leaves the lane's writer document behind, and
		// the order delete below then severs that document's order link
		// through invoices.order_id's ON DELETE SET NULL, stranding a
		// keyed document with no order for the next test's push to
		// converge on. Deleting only the lane's rows cannot be blocked,
		// so the lane's world comes down whole every time.
		exec(`DELETE FROM invoices WHERE idempotency_key = $3 OR id IN ($1::uuid, $2::uuid)`,
			gapInvoicePayID, gapInvoiceDeclineID, gapInvoiceWriterKey)
		exec(`DELETE FROM order_lines WHERE id = $1::uuid`, gapOrderLineID)
		exec(`DELETE FROM orders WHERE id = $1::uuid`, gapOrderID)
		exec(`DELETE FROM scope_sequences WHERE scope IN ('approvals', 'invoices', 'payments')`)
		exec(`DELETE FROM audit_log WHERE entity_type IN ('sync.approvals', 'sync.invoices', 'sync.payments')`)
		_ = testkit.Cleanup(ctx, testkit.PgxPool(pool))
	})
	return pool
}

// gapPush pushes one outbox under a filter and returns its results.
func gapPush(t *testing.T, e *sync.Engine, scope sync.ScopeInfo, f sync.ScopeFilter, ops []apigen.SyncPushOperation) []apigen.SyncPushResult {
	t.Helper()
	resp, err := e.Push(context.Background(), scope, f, ops)
	if err != nil {
		t.Fatalf("push %s: %v", scope.Name, err)
	}
	if len(resp.Results) != len(ops) {
		t.Fatalf("push %s: %d results for %d ops", scope.Name, len(resp.Results), len(ops))
	}
	return resp.Results
}

// gapOp builds one push operation.
func gapOp(kind, key string, payload map[string]any, base *int64) apigen.SyncPushOperation {
	return apigen.SyncPushOperation{ClientId: key, Op: kind, Payload: payload, BaseSeq: base}
}

// gapNote is the approvals kit's detail note, derived from the row it
// edits: the battery builds every update of one outbox from ONE row
// snapshot, so the derived note keeps a replayed second key on the
// value-equal arm (the row already carries the note that snapshot
// produced) and the first key keeps the stamped column, which is the
// two-lock dedup the frozen schema gives a table with no event trail.
func gapNote(row map[string]any) string {
	from := fmt.Sprint(row["detail"])
	if from == "" || from == "<nil>" {
		return "fx credit note"
	}
	return from + " (revised)"
}

// TestGapApprovalsContractBattery: the shared per-scope battery over
// the rail's own table, GATE-7 and the replay property included, on
// the requester's detail-note surface (the repeatable write the
// module admits); the decision legs run in the dedicated tests below.
func TestGapApprovalsContractBattery(t *testing.T) {
	f := newGapFixture(t)
	info, ok := sync.Lookup("approvals")
	if !ok {
		t.Fatalf("approvals missing from the registry")
	}
	filter := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	store, _ := f.engine.Store("approvals")
	k := harness.Kit{
		Scope:  info,
		Filter: filter,
		Store:  store,
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"branch_id":   testkit.BranchNorthAID,
				"policy_type": "CREDIT_LIMIT_EXCEEDED",
				"detail":      "fx credit note",
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "detail": gapNote(row)}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			// Each link derives from the row the previous link left,
			// so every chain update moves the row (ascending seqs).
			out := make([]map[string]any, 0, n)
			cur := row
			for i := 0; i < n; i++ {
				payload := map[string]any{"id": row["id"], "detail": gapNote(cur)}
				out = append(out, payload)
				cur = payload
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			// The desk's decision through the rail's own service: the
			// general manager (a ruled rail approver, a different
			// person than the requester) decides, and the rail's own
			// CAS stamp draws the feed seq.
			gm, err := identity.NewApprovalRepository(f.pool).ResolveStaff(ctx, testkit.UserGeneralMgrID)
			if err != nil {
				return err
			}
			return f.rail.DecideApproval(ctx, fmt.Sprint(row["id"]), gm, true, "desk decided")
		},
	}
	harness.Contract(t, f.engine, k)
	harness.Gate7(t, f.engine, k)
	rng := rand.New(rand.NewSource(355))
	harness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestGapApprovalsDecisionLegs: the approver's act, the scope's whole
// point: a pushed decision runs the rail's authority rules, four-eyes
// and the CAS stamp, a stale replay meets the decided row and answers
// the server_wins envelope applied false, and the two system-only
// money policies refuse at both doors.
func TestGapApprovalsDecisionLegs(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	info, _ := sync.Lookup("approvals")
	staff := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}

	// The clerk files from the device (the same create the battery
	// ran, kept here so the legs read as one story).
	created := gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "fx credit note",
		}, nil),
	})
	if created[0].Status != "applied" {
		t.Fatalf("filing answered %+v", created[0])
	}
	id := createdID(t, f, "approvals", staff, *created[0].Seq)

	// Four-eyes: the requester cannot decide their own request.
	res := gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": id, "status": "APPROVED"}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "own request") {
		t.Fatalf("four-eyes: %+v err %v", res[0], res[0].Error)
	}

	// The general manager decides from the device: applied, the row's
	// decided_by is the manager's linked user.
	res = gapPush(t, f.engine, info, gm, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": id, "status": "REJECTED", "rejection_reason": "over the line"}, nil),
	})
	if res[0].Status != "applied" {
		t.Fatalf("decision answered %+v err %v", res[0], res[0].Error)
	}
	state, _, err := f.apprSt.Load(ctx, gm, id)
	if err != nil {
		t.Fatalf("load the decided row: %v", err)
	}
	if state.Data["status"] != "REJECTED" || fmt.Sprint(state.Data["decided_by"]) != testkit.UserGeneralMgrID {
		t.Fatalf("the decided row: %+v", state.Data)
	}

	// A second decision under a fresh key refuses (the rail's own
	// once-only words), never a flip of decided_by.
	res = gapPush(t, f.engine, info, gm, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": id, "status": "APPROVED"}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "already") {
		t.Fatalf("double decide: %+v err %v", res[0], res[0].Error)
	}

	// The stale replay: the desk decides a fresh filing while the
	// manager's device was offline; the device's queued decision,
	// based on the older seq, answers the server_wins envelope with
	// the server row and applied false.
	created2 := gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"branch_id": testkit.BranchNorthAID, "policy_type": "COD_CONSTRAINT", "detail": "fx cod note",
		}, nil),
	})
	id2 := createdID(t, f, "approvals", staff, *created2[0].Seq)
	stale := *created2[0].Seq
	gmCtx, err := identity.NewApprovalRepository(f.pool).ResolveStaff(ctx, testkit.UserGeneralMgrID)
	if err != nil {
		t.Fatalf("resolve gm: %v", err)
	}
	if err := f.rail.DecideApproval(ctx, id2, gmCtx, true, "desk got there first"); err != nil {
		t.Fatalf("desk decide: %v", err)
	}
	res = gapPush(t, f.engine, info, gm, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": id2, "status": "REJECTED"}, &stale),
	})
	if len(res) != 1 || res[0].Status != "conflict" {
		t.Fatalf("stale decision answered %+v", res[0])
	}
	c := res[0].Conflict
	if c.Policy != "server_wins" || c.Applied == nil || *c.Applied {
		t.Fatalf("stale decision envelope: %+v", c)
	}
	if c.ServerState["status"] != "APPROVED" {
		t.Fatalf("the client was not told to show the server row: %+v", c.ServerState)
	}

	// The system-only money policies refuse at the sync door too, with
	// the module's own words.
	for policy, want := range map[string]string{
		"JE_OVER_THRESHOLD":   "filed by the ledger",
		"FREIGHT_ABOVE_QUOTE": "filed by dispatch",
	} {
		res = gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
			gapOp("create", uuid.NewString(), map[string]any{
				"branch_id": testkit.BranchNorthAID, "policy_type": policy, "detail": "fx system note",
			}, nil),
		})
		if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, want) {
			t.Fatalf("system-only policy %s: %+v err %v", policy, res[0], res[0].Error)
		}
	}
}

// TestGapApprovalsFences: the org fence derives off the branch, and
// it holds on every leg: a south-branch filing under the north filter
// refuses, a north pull never sees the south org's requests, and a
// note edit on a foreign row answers not-found.
func TestGapApprovalsFences(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	// UX-89: a filing is held to the filer's branch grants, so the two org principal needs the south branch
	// grant a real south filer holds (the kit plants branch grants for the north principals only).
	if _, err := f.pool.Exec(ctx, `INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES ($1, $2, $3)`,
		testkit.UserTwoOrgID, testkit.BranchSouthAID, testkit.OrgSouthID); err != nil {
		t.Fatalf("plant the south branch grant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM user_branch_grants WHERE user_id = $1 AND branch_id = $2`,
			testkit.UserTwoOrgID, testkit.BranchSouthAID)
	})
	info, _ := sync.Lookup("approvals")
	north := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	south := sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubTwoOrg}

	res := gapPush(t, f.engine, info, north, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"branch_id": testkit.BranchSouthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "fx south note",
		}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "not in this organisation") {
		t.Fatalf("cross-org filing answered %+v err %v", res[0], res[0].Error)
	}

	created := gapPush(t, f.engine, info, south, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"branch_id": testkit.BranchSouthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "fx south note",
		}, nil),
	})
	if created[0].Status != "applied" {
		t.Fatalf("south filing answered %+v", created[0])
	}
	southID := createdID(t, f, "approvals", south, *created[0].Seq)

	pull, err := f.engine.Pull(ctx, info, north, 0, 200)
	if err != nil {
		t.Fatalf("north pull: %v", err)
	}
	for _, it := range pull.Items {
		if it.Id == southID {
			t.Fatalf("the north org's pull saw the south org's approval")
		}
	}
	if _, ok, _ := f.apprSt.Load(ctx, north, southID); ok {
		t.Fatalf("the north filter loaded the south org's approval")
	}
	res = gapPush(t, f.engine, info, north, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": southID, "detail": "fx foreign note"}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "row not found") {
		t.Fatalf("cross-org note answered %+v err %v", res[0], res[0].Error)
	}
	// A tenant filter fails closed: nothing pulls, nothing loads.
	tenant := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha}
	tp, err := f.engine.Pull(ctx, info, tenant, 0, 200)
	if err != nil {
		t.Fatalf("tenant pull: %v", err)
	}
	if len(tp.Items) != 0 || len(tp.Tombstones) != 0 {
		t.Fatalf("a tenant filter read org rows through the approvals scope: %+v", tp)
	}
}

// TestGapInvoicesLegs: the invoices scope's battery legs over the
// money-document invariants: the pull shape under both caller
// classes, the create legs through the writer's own convergence, the
// stale-write envelope, and the refusals an honest money document
// answers.
func TestGapInvoicesLegs(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	info, _ := sync.Lookup("invoices")
	staff := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubSalesManager}
	alpha := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha}
	beta := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantBetaID, Tenant: true, Actor: testkit.SubPortalAdminBeta}

	// The staff create: the desk's act replayed, naming the delivered
	// order, honouring the client-chosen row id.
	docID := uuid.NewString()
	res := gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{"id": docID, "order_id": gapOrderID}, nil),
	})
	if res[0].Status != "applied" || res[0].Seq == nil {
		t.Fatalf("invoice create answered %+v", res[0])
	}
	state, ok, err := f.invSt.Load(ctx, staff, docID)
	if err != nil || !ok {
		t.Fatalf("the created document did not load under the client id: ok=%v err=%v", ok, err)
	}
	if state.Data["status"] != "FINALIZED" || state.Data["balance_cents"] != float64(11300) {
		t.Fatalf("the created document: %+v", state.Data)
	}
	firstSeq := *res[0].Seq

	// The pull shape under the staff fence: the document surfaces with
	// the scope echo, the seq, the projection.
	feed, err := f.engine.Pull(ctx, info, staff, 0, 200)
	if err != nil {
		t.Fatalf("staff pull: %v", err)
	}
	if string(feed.Scope) != "invoices" || len(feed.Items) != 1 || feed.Items[0].Id != docID || feed.Items[0].Seq != firstSeq {
		t.Fatalf("staff pull shape: %+v", feed.Items)
	}
	// Since and the high-water mark: the page past the mark is empty
	// and the mark never walks backwards.
	empty, err := f.engine.Pull(ctx, info, staff, firstSeq, 200)
	if err != nil {
		t.Fatalf("empty pull: %v", err)
	}
	if len(empty.Items) != 0 || empty.LastSeq != firstSeq {
		t.Fatalf("pull since %d: %+v last %d", firstSeq, empty.Items, empty.LastSeq)
	}

	// The replay: the same outbox key pushes again and the writer's
	// own convergence answers the SAME document, one state.
	snap1 := gapSnap(t, f, info, staff)
	res = gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{"id": docID, "order_id": gapOrderID}, nil),
	})
	if res[0].Status != "applied" {
		t.Fatalf("replayed create answered %+v err %v", res[0], res[0].Error)
	}
	// A DIFFERENT key on the same order converges too: the desk's
	// double-click and the device's replay are one door.
	res = gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{"order_id": gapOrderID}, nil),
	})
	if res[0].Status != "applied" || *res[0].Seq != firstSeq {
		t.Fatalf("cross-key convergence answered %+v (want the one document's seq %d)", res[0], firstSeq)
	}
	assertGapEq(t, "invoice replay convergence", snap1, gapSnap(t, f, info, staff))

	// The cursor: a one-row page carries next_cursor and resuming at
	// it changes nothing (one document in the feed).
	page1, err := f.engine.Pull(ctx, info, staff, 0, 1)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if page1.NextCursor == nil {
		t.Fatalf("a one-row page over the feed carries no next_cursor")
	}

	// Tenant isolation, both directions: alpha pulls the document
	// (its own customer row), beta sees nothing of alpha's although
	// the two tenants share the org.
	ta, err := f.engine.Pull(ctx, info, alpha, 0, 200)
	if err != nil {
		t.Fatalf("alpha pull: %v", err)
	}
	if len(ta.Items) != 1 || ta.Items[0].Id != docID {
		t.Fatalf("alpha pull: %+v", ta.Items)
	}
	tb, err := f.engine.Pull(ctx, info, beta, 0, 200)
	if err != nil {
		t.Fatalf("beta pull: %v", err)
	}
	if len(tb.Items) != 0 || len(tb.Tombstones) != 0 {
		t.Fatalf("beta saw alpha's invoices: %+v", tb.Items)
	}
	if _, ok, _ := f.invSt.Load(ctx, beta, docID); ok {
		t.Fatalf("beta loaded alpha's invoice")
	}

	// The tenant cannot birth a document: the catalog's own
	// declaration (written by the counter and the money desk).
	res = gapPush(t, f.engine, info, alpha, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{"order_id": gapOrderID}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "raised by the desk") {
		t.Fatalf("tenant create answered %+v err %v", res[0], res[0].Error)
	}

	// The manager-tier gate: the REST door gates the same act behind
	// RequireManager and the order surface's own re-check; a frontline
	// caller's replayed create refuses with the same rule.
	frontline := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	res = gapPush(t, f.engine, info, frontline, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{"order_id": gapOrderID}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "manager-tier") {
		t.Fatalf("frontline create answered %+v err %v", res[0], res[0].Error)
	}

	// A foreign-org order answers not-found, leaking no existence: a
	// north manager naming the south org's order (the fence reads the
	// order row's own org stamp before the writer runs).
	res = gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{"order_id": testkit.OrderGammaDraftID}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "row not found") {
		t.Fatalf("cross-org order create answered %+v err %v", res[0], res[0].Error)
	}

	// Updates and deletes answer the operation's rejected result: a
	// money document has no client-declared field and no feed
	// tombstone.
	res = gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": docID, "due_date": "2001-01-01"}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "no client-declared field") {
		t.Fatalf("update answered %+v err %v", res[0], res[0].Error)
	}
	res = gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("delete", uuid.NewString(), map[string]any{"id": docID}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "not deletable") {
		t.Fatalf("delete answered %+v err %v", res[0], res[0].Error)
	}

	// The stale-write envelope: the desk's payment moves the document
	// (the payments scope's own test proves the settle), so here the
	// desk leg is a direct seq-drawing stamp, the pilot Desk hook's
	// own shape, and the device's stale update answers server_wins
	// applied false with the server row.
	var bumped int64
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ('invoices')
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`).Scan(&bumped); err != nil {
		t.Fatalf("desk stamp: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`UPDATE invoices SET holdback_cents = 600, seq = $2 WHERE id = $1::uuid`, docID, bumped); err != nil {
		t.Fatalf("desk edit: %v", err)
	}
	res = gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": docID, "due_date": "2001-01-01"}, &firstSeq),
	})
	if res[0].Status != "conflict" {
		t.Fatalf("stale update answered %+v", res[0])
	}
	c := res[0].Conflict
	if c.Policy != "server_wins" || c.Applied == nil || *c.Applied {
		t.Fatalf("stale update envelope: %+v", c)
	}
	if fmt.Sprint(c.ServerState["holdback_cents"]) != "600" {
		t.Fatalf("the envelope did not carry the server row: %+v", c.ServerState)
	}
}

// TestGapPaymentsLegs: the payments scope's battery legs: the pay
// flow through the module's own guarded choreography, the replay and
// cross-key convergence (one charge, one settlement set), the decline
// as data, the tenant fence, and the refusals.
func TestGapPaymentsLegs(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	info, _ := sync.Lookup("payments")
	alpha := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha}
	beta := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantBetaID, Tenant: true, Actor: testkit.SubPortalAdminBeta}

	// The device pays the seeded invoice with the stored method.
	payID := uuid.NewString()
	res := gapPush(t, f.engine, info, alpha, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"id":                payID,
			"invoice_ids":       []any{gapInvoicePayID},
			"expected_cents":    float64(11300),
			"payment_method_id": testkit.PayMethodAlphaID,
		}, nil),
	})
	if res[0].Status != "applied" || res[0].Seq == nil {
		t.Fatalf("pay create answered %+v err %v", res[0], res[0].Error)
	}
	state, ok, err := f.paySt.Load(ctx, alpha, payID)
	if err != nil || !ok {
		t.Fatalf("the paid row did not load under the client id: ok=%v err=%v", ok, err)
	}
	if state.Data["amount_cents"] != float64(11300) || state.Data["status"] != "RECEIVED" {
		t.Fatalf("the paid row: %+v", state.Data)
	}
	allocs, _ := state.Data["allocations"].([]any)
	if len(allocs) != 1 {
		t.Fatalf("the payment carries %d allocations, want 1: %+v", len(allocs), state.Data)
	}

	// The invoice the payment settled: its balance moved and its OWN
	// scope's feed carried the change (the cross-scope visibility the
	// settle's seq stamp exists for).
	invInfo, _ := sync.Lookup("invoices")
	invFeed, err := f.engine.Pull(ctx, invInfo, alpha, 0, 200)
	if err != nil {
		t.Fatalf("alpha invoice pull: %v", err)
	}
	var paidRow apigen.SyncEntity
	for _, it := range invFeed.Items {
		if it.Id == gapInvoicePayID {
			paidRow = it
		}
	}
	if paidRow.Id == "" || paidRow.Data["balance_cents"] != float64(0) || paidRow.Data["status"] != "PAID" {
		t.Fatalf("the settled invoice on the feed: %+v", paidRow)
	}

	// The payments feed: the row surfaces with the seq the settle
	// stamped.
	feed, err := f.engine.Pull(ctx, info, alpha, 0, 200)
	if err != nil {
		t.Fatalf("alpha payment pull: %v", err)
	}
	if len(feed.Items) != 1 || feed.Items[0].Id != payID || feed.Items[0].Seq != *res[0].Seq {
		t.Fatalf("payment pull shape: %+v", feed.Items)
	}
	paySeq := *res[0].Seq

	// The replay: the same key answers applied through the stamped
	// column, and the state is unchanged down to the seqs.
	snap1 := gapSnap(t, f, info, alpha)
	res = gapPush(t, f.engine, info, alpha, []apigen.SyncPushOperation{
		gapOp("create", "40000000-0000-4000-8000-000000000001", map[string]any{
			"id":                payID,
			"invoice_ids":       []any{gapInvoicePayID},
			"expected_cents":    float64(11300),
			"payment_method_id": testkit.PayMethodAlphaID,
		}, nil),
	})
	if res[0].Status != "applied" || *res[0].Seq != paySeq {
		t.Fatalf("same-key replay answered %+v (want the one payment's seq %d)", res[0], paySeq)
	}
	// A DIFFERENT key on the same request converges through the
	// module's server-bound intent: one charge, one settlement set.
	res = gapPush(t, f.engine, info, alpha, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"invoice_ids":       []any{gapInvoicePayID},
			"expected_cents":    float64(11300),
			"payment_method_id": testkit.PayMethodAlphaID,
		}, nil),
	})
	if res[0].Status != "applied" || *res[0].Seq != paySeq {
		t.Fatalf("cross-key convergence answered %+v (want the one payment's seq %d)", res[0], paySeq)
	}
	assertGapEq(t, "payment replay convergence", snap1, gapSnap(t, f, info, alpha))
	var n int
	// Scoped to THIS invoice: the gate database is shared, and other packages' fixtures (the POS
	// sale legs, for one) leave keyed payment rows of their own that have nothing to do with the
	// request under test. "One charge, one settlement set" is a claim about the invoice's payments.
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p
		  WHERE p.idempotency_key IS NOT NULL AND p.idempotency_key <> ''
		    AND EXISTS (SELECT 1 FROM payment_allocations a WHERE a.payment_id = p.id AND a.invoice_id = $1::uuid)`,
		gapInvoicePayID).Scan(&n); err != nil {
		t.Fatalf("count keyed payments: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d keyed payment rows against the invoice, want exactly the one", n)
	}

	// The decline is data: the 13-cent payable answers the rejected
	// result with the module's own words, and no payment row lands.
	res = gapPush(t, f.engine, info, alpha, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"invoice_ids":       []any{gapInvoiceDeclineID},
			"expected_cents":    float64(13),
			"payment_method_id": testkit.PayMethodAlphaID,
		}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "declined") {
		t.Fatalf("declined pay answered %+v err %v", res[0], res[0].Error)
	}

	// The tenant fence: beta sees nothing of alpha's payment, cannot
	// load it, and a beta pay naming alpha's invoice answers
	// not-found (the module's own IDOR discipline).
	tb, err := f.engine.Pull(ctx, info, beta, 0, 200)
	if err != nil {
		t.Fatalf("beta pull: %v", err)
	}
	if len(tb.Items) != 0 || len(tb.Tombstones) != 0 {
		t.Fatalf("beta saw alpha's payments: %+v", tb.Items)
	}
	if _, ok, _ := f.paySt.Load(ctx, beta, payID); ok {
		t.Fatalf("beta loaded alpha's payment")
	}
	// Beta's own payable (the beta invoice is DRAFT, not payable):
	// naming ALPHA's invoice is the cross-tenant leg.
	res = gapPush(t, f.engine, info, beta, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"invoice_ids":       []any{gapInvoicePayID},
			"expected_cents":    float64(11300),
			"payment_method_id": testkit.PayMethodBetaID,
		}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "row not found") {
		t.Fatalf("cross-tenant pay answered %+v err %v", res[0], res[0].Error)
	}

	// A staff filter fails closed, and updates and deletes refuse.
	staff := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	res = gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"invoice_ids":       []any{gapInvoicePayID},
			"expected_cents":    float64(11300),
			"payment_method_id": testkit.PayMethodAlphaID,
		}, nil),
	})
	if res[0].Status != "rejected" {
		t.Fatalf("staff create answered %+v", res[0])
	}
	res = gapPush(t, f.engine, info, alpha, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": payID, "status": "VOID"}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "no client-declared field") {
		t.Fatalf("update answered %+v err %v", res[0], res[0].Error)
	}
	res = gapPush(t, f.engine, info, alpha, []apigen.SyncPushOperation{
		gapOp("delete", uuid.NewString(), map[string]any{"id": payID}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "not deletable") {
		t.Fatalf("delete answered %+v err %v", res[0], res[0].Error)
	}
}

// TestGapWalkthroughs: the operator walkthroughs, scripted against
// the seeded database, each step logged so the PR body carries the
// transcript.
func TestGapWalkthroughs(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	appr, _ := sync.Lookup("approvals")
	inv, _ := sync.Lookup("invoices")
	pay, _ := sync.Lookup("payments")
	clerk := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	staff := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubSalesManager}
	alpha := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha}

	// APPROVALS: the clerk's margin override from the yard phone is the
	// quote desk's to file and the door refuses it with the rail's own
	// words (D7); the credit exception is the caller's own, the manager
	// decides it on the train, the replay changes nothing, and a decision
	// the desk already made is what the device is told to show.
	res := gapPush(t, f.engine, appr, clerk, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"branch_id": testkit.BranchNorthAID, "policy_type": "MARGIN_BELOW_MINIMUM", "detail": "fixture walkthrough note",
		}, nil),
	})
	if res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "filed by the quote desk") {
		t.Fatalf("walkthrough margin filing: %+v err %v", res[0], res[0].Error)
	}
	t.Logf("approvals 1: the clerk's pushed margin filing was refused: the quote desk files those for a quote line")
	res = gapPush(t, f.engine, appr, clerk, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "fixture walkthrough note",
		}, nil),
	})
	if res[0].Status != "applied" {
		t.Fatalf("walkthrough filing: %+v err %v", res[0], res[0].Error)
	}
	id := createdID(t, f, "approvals", clerk, *res[0].Seq)
	t.Logf("approvals 1b: the clerk filed the credit exception %s from the device, applied at seq %d", id, *res[0].Seq)
	list, err := f.engine.Pull(ctx, appr, gm, 0, 50)
	if err != nil {
		t.Fatalf("gm pull: %v", err)
	}
	t.Logf("approvals 2: the manager pulled %d pending card(s)", len(list.Items))
	res = gapPush(t, f.engine, appr, gm, []apigen.SyncPushOperation{
		gapOp("update", uuid.NewString(), map[string]any{"id": id, "status": "APPROVED"}, nil),
	})
	if res[0].Status != "applied" {
		t.Fatalf("walkthrough decide: %+v err %v", res[0], res[0].Error)
	}
	t.Logf("approvals 3: the manager approved at seq %d", *res[0].Seq)
	before := gapSnap(t, f, appr, gm)
	res = gapPush(t, f.engine, appr, gm, []apigen.SyncPushOperation{
		gapOp("update", "50000000-0000-4000-8000-000000000001", map[string]any{"id": id, "status": "APPROVED"}, nil),
	})
	if res[0].Status != "rejected" {
		t.Fatalf("walkthrough replay: %+v", res[0])
	}
	assertGapEq(t, "approvals 4", before, gapSnap(t, f, appr, gm))
	t.Logf("approvals 4: a replayed decide answered the rail's once-only refusal, the state unchanged")

	// INVOICES and PAYMENTS: the desk replays the billing of a
	// delivered order, the payer pays it from the job site, and the
	// payer's two feeds agree with the ledger's rows.
	res = gapPush(t, f.engine, inv, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{"order_id": gapOrderID}, nil),
	})
	if res[0].Status != "applied" {
		t.Fatalf("walkthrough invoice: %+v err %v", res[0], res[0].Error)
	}
	var invID string
	if err := f.pool.QueryRow(ctx,
		`SELECT id::text FROM invoices WHERE order_id = $1::uuid AND seq = $2`,
		gapOrderID, *res[0].Seq).Scan(&invID); err != nil {
		t.Fatalf("walkthrough invoice read: %v", err)
	}
	t.Logf("invoices 1: the desk's replayed billing of the delivered order landed as %s at seq %d", invID, *res[0].Seq)
	tp, err := f.engine.Pull(ctx, inv, alpha, 0, 50)
	if err != nil {
		t.Fatalf("payer pull: %v", err)
	}
	t.Logf("invoices 2: the payer pulled %d document(s), the newest balance %v",
		len(tp.Items), tp.Items[len(tp.Items)-1].Data["balance_cents"])
	res = gapPush(t, f.engine, pay, alpha, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"invoice_ids":       []any{invID},
			"expected_cents":    float64(11300),
			"payment_method_id": testkit.PayMethodAlphaID,
		}, nil),
	})
	if res[0].Status != "applied" {
		t.Fatalf("walkthrough pay: %+v err %v", res[0], res[0].Error)
	}
	t.Logf("payments 1: the payer paid from the job site, the settle landed at seq %d", *res[0].Seq)
	inc, err := f.engine.Pull(ctx, inv, alpha, *res[0].Seq-1, 50)
	if err != nil {
		t.Fatalf("payer incremental pull: %v", err)
	}
	var seen bool
	for _, it := range inc.Items {
		if it.Id == invID {
			seen = true
			if it.Data["balance_cents"] != float64(0) || it.Data["status"] != "PAID" {
				t.Fatalf("the incremental invoice pull after the pay: %+v", it.Data)
			}
		}
	}
	if !seen {
		t.Fatalf("the payer's incremental invoices pull missed the settled document")
	}
	t.Logf("invoices 3: the payer's incremental pull since the pay carried the settled document (PAID, balance 0)")
	var bal int64
	if err := f.pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&bal); err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if bal != 0 {
		t.Fatalf("the row behind the feed disagrees: balance %d", bal)
	}
	t.Logf("invoices 4: the row behind the feed agrees with the projection (balance 0)")
}

// createdID resolves the row id of the one approval the caller's feed
// gained at the given seq.
func createdID(t *testing.T, f *theGapFixture, scope string, flt sync.ScopeFilter, seq int64) string {
	t.Helper()
	info, _ := sync.Lookup(scope)
	resp, err := f.engine.Pull(context.Background(), info, flt, seq-1, 10)
	if err != nil {
		t.Fatalf("createdID pull: %v", err)
	}
	for _, it := range resp.Items {
		if it.Seq == seq {
			return it.Id
		}
	}
	t.Fatalf("no row at seq %d on the %s feed", seq, scope)
	return ""
}

// gapSnap takes one scope's snapshot under a filter (the walkthrough
// and convergence compares).
func gapSnap(t *testing.T, f *theGapFixture, info sync.ScopeInfo, flt sync.ScopeFilter) map[string]any {
	t.Helper()
	resp, err := f.engine.Pull(context.Background(), info, flt, 0, 200)
	if err != nil {
		t.Fatalf("snap pull: %v", err)
	}
	state := map[string]any{"last_seq": resp.LastSeq}
	for _, it := range resp.Items {
		state[it.Id] = fmt.Sprintf("%d|%v", it.Seq, it.Data)
	}
	for _, tb := range resp.Tombstones {
		state[tb.Id] = fmt.Sprintf("tombstone|%d", tb.Seq)
	}
	return state
}

// assertGapEq compares two snapshots.
func assertGapEq(t *testing.T, name string, want, got map[string]any) {
	t.Helper()
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Fatalf("%s: the server state changed: %v, want %v", name, got, want)
	}
}

// TestGapInvoicesFenceHoldsThroughTheBranchOrg (UX-95a): the invoices create
// fence holds an order to the caller's org through the order's BRANCH
// (orders.branch_id to locations.org_id), because no production order writer
// stamps orders.org_id. Each order below is inserted the way its writer does,
// with the column list of that writer and no org_id: the till's
// (pos/repository.go InsertOrder) and the Quote Desk's (quote/convert.go
// convertInsertSQL). Before the fix the fence read orders.org_id and refused
// every one of them.
func TestGapInvoicesFenceHoldsThroughTheBranchOrg(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	info, _ := sync.Lookup("invoices")
	north := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubSalesManager}

	const (
		tillOrderID  = "3ef10000-0000-4000-8000-0000000000f3"
		tillLineID   = "3ef10000-0000-4000-8000-0000000000f4"
		quoteOrderID = "3ef10000-0000-4000-8000-0000000000f5"
		quoteLineID  = "3ef10000-0000-4000-8000-0000000000f6"
		southOrderID = "3ef10000-0000-4000-8000-0000000000f7"
		southLineID  = "3ef10000-0000-4000-8000-0000000000f8"
		orphanOrder  = "3ef10000-0000-4000-8000-0000000000f9"
		orphanLine   = "3ef10000-0000-4000-8000-0000000000fa"
		orphanBranch = "3ef10000-0000-4000-8000-0000000000fb"
	)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("seed: %v (statement %.60s)", err, q)
		}
	}
	orders := []string{tillOrderID, quoteOrderID, southOrderID, orphanOrder}
	// The leg mints order numbers at the kit's fixture branches through the
	// real next_order_no counter, and migration 0087 pre-creates a counter
	// row for every branch: whatever the counter held before this leg, it
	// must hold after. Snapshot both branches' rows now; the cleanup
	// restores a row it found standing and removes only a row proven new.
	type gapCounterRow struct {
		lastValue int64
		updatedAt time.Time
		existed   bool
	}
	counterBefore := map[string]gapCounterRow{}
	for _, b := range []string{testkit.BranchNorthAID, testkit.BranchSouthAID} {
		row := gapCounterRow{}
		err := f.pool.QueryRow(ctx, `SELECT last_value, updated_at FROM order_no_sequences WHERE branch_id = $1::uuid`, b).
			Scan(&row.lastValue, &row.updatedAt)
		if err == nil {
			row.existed = true
		} else if err != pgx.ErrNoRows {
			t.Fatalf("snapshot the order counter at %s: %v", b, err)
		}
		counterBefore[b] = row
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range orders {
			_, _ = f.pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE order_id = $1::uuid)`, id)
			_, _ = f.pool.Exec(c, `DELETE FROM invoices WHERE order_id = $1::uuid`, id)
			_, _ = f.pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1::uuid`, id)
			_, _ = f.pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, id)
		}
		_, _ = f.pool.Exec(c, `DELETE FROM order_no_sequences WHERE branch_id = $1::uuid`, orphanBranch)
		// The walkthrough minted order numbers at the kit's fixture branches
		// through the real next_order_no counter. Migration 0087 pre-creates
		// a counter row for every branch, so the row the leg found standing
		// it restores with its prior values, and a row proven new (the kit
		// built the branch after the migration ran) it removes; deleting a
		// found row would let a later leg restart numbering at 1, and
		// leaving a new row standing would refuse the kit cleanup's branch
		// delete on the counter's foreign key.
		for _, b := range []string{testkit.BranchNorthAID, testkit.BranchSouthAID} {
			if before := counterBefore[b]; before.existed {
				_, _ = f.pool.Exec(c, `UPDATE order_no_sequences SET last_value = $2, updated_at = $3 WHERE branch_id = $1::uuid`,
					b, before.lastValue, before.updatedAt)
			} else {
				_, _ = f.pool.Exec(c, `DELETE FROM order_no_sequences WHERE branch_id = $1::uuid`, b)
			}
		}
		// The counter after the leg equals the counter before it: a row the
		// leg found standing stands again with its prior values, and a row
		// the leg planted is gone.
		for _, b := range []string{testkit.BranchNorthAID, testkit.BranchSouthAID} {
			var lastValue int64
			var updatedAt time.Time
			err := f.pool.QueryRow(c, `SELECT last_value, updated_at FROM order_no_sequences WHERE branch_id = $1::uuid`, b).
				Scan(&lastValue, &updatedAt)
			before := counterBefore[b]
			if !before.existed {
				if err == nil {
					t.Errorf("the order counter at %s survived the leg though the leg planted it: a later leg restarts numbering at 1", b)
				}
				continue
			}
			if err != nil || lastValue != before.lastValue || !updatedAt.Equal(before.updatedAt) {
				t.Errorf("the order counter at %s changed: before last_value=%d, after last_value=%d (read err %v); a later leg restarts numbering wherever this leg left the counter",
					b, before.lastValue, lastValue, err)
			}
		}
		_, _ = f.pool.Exec(c, `DELETE FROM locations WHERE id = $1::uuid`, orphanBranch)
	})
	line := func(lineID, orderID string) {
		exec(`INSERT INTO order_lines (id, order_id, line_no, product_id, description, qty_entered,
			uom_entered, qty_base, fulfilled_base, unit_price_cents, price_uom, line_total_cents)
			VALUES ($1::uuid, $2::uuid, 1, $3::uuid, 'Fixture fence line', 10, 'EA', 10, 10, 1000, 'EA', 10000)`,
			lineID, orderID, testkit.ProductID)
	}
	// The till's insert: pos/repository.go InsertOrder's column list, no org_id.
	tillOrder := func(id, branch string) {
		exec(`INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, requested_date,
			subtotal_cents, tax_cents, total_cents, created_by, metadata,
			po_job_ref, salesperson_id, job_address, job_reference)
			VALUES ($1::uuid, next_order_no($2::uuid), $3::uuid, $2::uuid, 'CONFIRMED', 'PICKUP', NULL,
			10000, 0, 10000, NULL, '{}'::jsonb, '', NULL, '', '')`,
			id, branch, testkit.TenantAlphaID)
	}
	tillOrder(tillOrderID, testkit.BranchNorthAID)
	line(tillLineID, tillOrderID)
	// The Quote Desk's insert: convert.go convertInsertSQL's column list, no org_id,
	// fulfilled so the writer can bill it.
	exec(`INSERT INTO orders (id, order_no, customer_id, branch_id, quote_id, subtotal_cents, tax_cents, total_cents,
		freight_cents, quoted_freight_cents, status, fulfillment)
		SELECT $2::uuid, next_order_no(branch_id), customer_id, branch_id, id, subtotal_cents, tax_cents, total_cents,
		delivery_fee_cents, delivery_fee_cents, 'CONFIRMED', 'PICKUP'
		FROM quotes WHERE id = $1::uuid`, testkit.QuoteAlphaID, quoteOrderID)
	line(quoteLineID, quoteOrderID)
	// Another dealer's order: the south org's branch, still no org_id.
	tillOrder(southOrderID, testkit.BranchSouthAID)
	line(southLineID, southOrderID)
	// A branch whose locations row carries no org: the order is unattributable.
	exec(`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address, org_id)
		VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', 'FXORPH', 'Fixture orphan branch', '{}', NULL)`, orphanBranch)
	tillOrder(orphanOrder, orphanBranch)
	line(orphanLine, orphanOrder)

	create := func(orderID string) apigen.SyncPushResult {
		res := gapPush(t, f.engine, info, north, []apigen.SyncPushOperation{
			gapOp("create", uuid.NewString(), map[string]any{"order_id": orderID}, nil),
		})
		return res[0]
	}
	for name, orderID := range map[string]string{"till-shaped": tillOrderID, "quote-desk-shaped": quoteOrderID} {
		r := create(orderID)
		if r.Status != "applied" || r.Seq == nil {
			t.Fatalf("%s order with no org_id was not invoiced for its own dealer: %+v err %v", name, r, r.Error)
		}
	}
	for name, orderID := range map[string]string{"another dealer's": southOrderID, "no-org branch": orphanOrder} {
		r := create(orderID)
		if r.Status != "rejected" || r.Error == nil || !strings.Contains(*r.Error, "row not found") {
			t.Fatalf("%s order answered %+v err %v (want row not found)", name, r, r.Error)
		}
		var n int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE order_id = $1::uuid`, orderID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s order raised %d invoices (err %v), want none", name, n, err)
		}
	}
}
