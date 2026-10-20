//go:build integration

package main

// tasks_org_scope_integration_test.go: the pick creates and the pick ticket read held to the caller's ORG (UX-81
// round 4, FROM-PRINCIPAL item 103). The order brief read and the pick ticket read used to take an order id alone,
// so a GLOBAL caller of one org created a pick on another org's order (the REST create answered 201, the sync create
// applied) and pulled that org's ticket. Both reads now carry the caller's org in their WHERE, and a foreign order
// answers exactly as an unknown one. The legs use the test kit's two orgs (north and south) and the real order and
// dispatch adapters the composition root wires.
//
// Gated on HH_CORE_TEST_DATABASE_URL (the migrated scratch database with the kit baseline).

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// orgScopeWorld is the two orgs' orders the legs read: north's kit order and a south pickup order planted for the
// run (the kit's south order is a draft, which has no pick ticket).
type orgScopeWorld struct {
	pool       *pgxpool.Pool
	northOrder string // the kit's confirmed pickup order in org north, branch north A
	southOrder string // a confirmed pickup order planted in org south, branch south A
}

func newOrgScopeWorld(t *testing.T) *orgScopeWorld {
	t.Helper()
	pool := wiringPool(t)
	ctx := context.Background()
	// The kit's two orgs: built for the run and cleaned after it (the cleanup registered first runs last, after
	// this world's own rows are gone).
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("build the kit's two orgs: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(pool)) })
	w := &orgScopeWorld{pool: pool, northOrder: uuid.NewString(), southOrder: uuid.NewString()}
	// Both orders are inserted exactly the way production inserts an order: NO org_id (no writer stamps it; UX-95
	// owns that). The branch decides the dealer: north's order sits at north A, south's at south A, whose locations
	// rows carry the org (UX-81 round 6).
	for _, o := range []struct{ id, branch string }{{w.northOrder, testkit.BranchNorthAID}, {w.southOrder, testkit.BranchSouthAID}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment)
			VALUES ($1::uuid, 'FXORD-ORGSCOPE-'||substr($1::text,1,8), $2::uuid, $3::uuid, 'CONFIRMED', 'PICKUP')`,
			o.id, testkit.TenantGammaID, o.branch); err != nil {
			t.Fatalf("plant an order the production way: %v", err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM task_events WHERE task_id IN (SELECT id FROM tasks WHERE order_id = ANY($1::uuid[]))`, []string{w.northOrder, w.southOrder})
		_, _ = pool.Exec(bg, `DELETE FROM tasks WHERE order_id = ANY($1::uuid[])`, []string{w.northOrder, w.southOrder})
		_, _ = pool.Exec(bg, `DELETE FROM orders WHERE id = ANY($1::uuid[])`, []string{w.northOrder, w.southOrder})
	})
	return w
}

func (w *orgScopeWorld) globalOf(org string) identity.StaffContext {
	return identity.StaffContext{UserID: uuid.NewString(), RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive}
}

func (w *orgScopeWorld) orders() orderReaderAdapter {
	return orderReaderAdapter{repo: order.NewRepository(w.pool)}
}

func (w *orgScopeWorld) syncStore(sc identity.StaffContext) *tasks.SyncStore {
	link := func(context.Context, string) (string, bool) { return sc.UserID, true }
	staff := func(context.Context, string, string) (identity.StaffContext, bool, error) { return sc, true, nil }
	return tasks.NewSyncStore(w.pool, w.orders(), link).WithStaffAuthority(staff).
		WithBranchOrgReader(branchOrgAdapter{repo: branches.NewRepository(w.pool)}).
		WithPickSource(newDispatchPickSource(w.pool, order.NewRepository(w.pool), slog.Default()))
}

func (w *orgScopeWorld) taskCount(t *testing.T, org string) int {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE org_id = $1::uuid AND order_id = ANY($2::uuid[])`,
		org, []string{w.northOrder, w.southOrder}).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return n
}

// A GLOBAL caller of org north naming org south's order: the REST create's service path.
func TestTasksOrgScope_RESTCreateOnAnotherOrgsOrderIsAnUnknownOrder(t *testing.T) {
	w := newOrgScopeWorld(t)
	svc := tasks.NewService(tasks.NewRepository(w.pool)).WithOrderReader(w.orders())
	global := w.globalOf(testkit.OrgNorthID)
	create := func(org, orderID string) (tasks.Task, error) {
		return svc.CreateFromOrder(context.Background(), tasks.CreateInput{OrderID: orderID, OrgID: org, CreatedBy: global.UserID,
			Reach: func(branch string) (bool, error) { return global.CanAccessBranch(branch), nil }})
	}
	_, unknownErr := create(testkit.OrgNorthID, uuid.NewString())
	if unknownErr == nil {
		t.Fatal("an unknown order must be refused")
	}
	_, err := create(testkit.OrgNorthID, w.southOrder)
	if err == nil || err.Error() != unknownErr.Error() {
		t.Errorf("a GLOBAL caller of org north naming org south's order must get exactly the unknown order answer (%v), got %v", unknownErr, err)
	}
	if n := w.taskCount(t, testkit.OrgNorthID); n != 0 {
		t.Errorf("a refused create writes nothing in the caller's org: %d tasks", n)
	}
	if n := w.taskCount(t, testkit.OrgSouthID); n != 0 {
		t.Errorf("a refused create writes nothing in the order's org either: %d tasks", n)
	}
	if _, err := create(testkit.OrgNorthID, w.northOrder); err != nil {
		t.Errorf("the caller's own org's order still creates: %v", err)
	}
}

// The same caller through the sync create.
func TestTasksOrgScope_SyncCreateOnAnotherOrgsOrderIsAnUnknownOrder(t *testing.T) {
	w := newOrgScopeWorld(t)
	store := w.syncStore(w.globalOf(testkit.OrgNorthID))
	f := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "org-scope-actor"}
	create := func(orderID string) error {
		_, err := store.Create(context.Background(), f, uuid.NewString(), map[string]any{"order_id": orderID})
		return err
	}
	unknownErr := create(uuid.NewString())
	if unknownErr == nil {
		t.Fatal("an unknown order must be refused")
	}
	err := create(w.southOrder)
	if err == nil || err.Error() != unknownErr.Error() {
		t.Errorf("a GLOBAL caller of org north naming org south's order must get exactly the unknown order answer (%v), got %v", unknownErr, err)
	}
	if n := w.taskCount(t, testkit.OrgNorthID) + w.taskCount(t, testkit.OrgSouthID); n != 0 {
		t.Errorf("a refused create writes no row: %d tasks", n)
	}
	if err := create(w.northOrder); err != nil {
		t.Errorf("the caller's own org's order still creates: %v", err)
	}
}

// The ticket read: a task of org north that points at org south's order (a row the old create allowed) must not
// carry south's ticket in north's pull; the dispatch read itself answers a foreign order as unknown.
func TestTasksOrgScope_TheTicketReadIsHeldToTheCallersOrg(t *testing.T) {
	w := newOrgScopeWorld(t)
	ctx := context.Background()
	src := newDispatchPickSource(w.pool, order.NewRepository(w.pool), slog.Default())
	if _, err := src.PickTicketInOrg(ctx, testkit.OrgNorthID, w.southOrder); err == nil || !strings.Contains(err.Error(), "unknown order") {
		t.Errorf("the dispatch ticket read in org north must answer org south's order as unknown, got %v", err)
	}
	if _, err := src.PickTicketInOrg(ctx, testkit.OrgSouthID, w.southOrder); err != nil {
		t.Errorf("the order's own org still reads its ticket: %v", err)
	}

	// Two org north tasks (a row the old create allowed: south's order; and north's own order), both pulled by
	// a GLOBAL caller of org north through the pick store as composed.
	insert := func(orderID string) string {
		var id string
		if err := w.pool.QueryRow(ctx, `
			INSERT INTO tasks (task_type, order_id, branch_id, priority, status, source, org_id, seq)
			VALUES ('PICK', $1::uuid, $2::uuid, 'NORMAL', 'QUEUED', 'MANUAL', $3::uuid, (SELECT COALESCE(max(seq),0)+1 FROM tasks))
			RETURNING id::text`, orderID, testkit.BranchNorthAID, testkit.OrgNorthID).Scan(&id); err != nil {
			t.Fatalf("plant a north task on order %s: %v", orderID, err)
		}
		return id
	}
	foreign, own := insert(w.southOrder), insert(w.northOrder)
	store := w.syncStore(w.globalOf(testkit.OrgNorthID))
	items, _, err := store.Pull(ctx, hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "org-scope-actor"}, 0, 500)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	var sawForeign, sawOwn, foreignTicket, ownTicket bool
	for _, it := range items {
		_, has := it.Data["ticket"]
		switch it.ID {
		case foreign:
			sawForeign, foreignTicket = true, has
		case own:
			sawOwn, ownTicket = true, has
		}
	}
	if !sawForeign || !sawOwn {
		t.Fatalf("the caller's org's rows are in its pull: foreign %v own %v", sawForeign, sawOwn)
	}
	if foreignTicket {
		t.Errorf("org south's ticket must not ride org north's pull")
	}
	if !ownTicket {
		t.Errorf("the caller's own order still carries its ticket")
	}
}

// (2) A GLOBAL caller's reach is every branch of ITS org, never every branch in the database: another org's task is
// invisible to the pull, Load and FindApplied whatever the caller's scope.
func TestTasksOrgScope_AGlobalCallersReachStopsAtItsOrg(t *testing.T) {
	w := newOrgScopeWorld(t)
	ctx := context.Background()
	var southTask string
	key := uuid.NewString()
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO tasks (task_type, order_id, branch_id, priority, status, source, org_id, seq, idempotency_key)
		VALUES ('PICK', $1::uuid, $2::uuid, 'NORMAL', 'QUEUED', 'MANUAL', $3::uuid, (SELECT COALESCE(max(seq),0)+1 FROM tasks), $4::uuid)
		RETURNING id::text`, w.southOrder, testkit.BranchSouthAID, testkit.OrgSouthID, key).Scan(&southTask); err != nil {
		t.Fatalf("plant a south task: %v", err)
	}
	store := w.syncStore(w.globalOf(testkit.OrgNorthID))
	north := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "org-scope-actor"}
	items, _, err := store.Pull(ctx, north, 0, 500)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	for _, it := range items {
		if it.ID == southTask {
			t.Errorf("a GLOBAL caller of org north pulled org south's pick")
		}
	}
	if _, ok, err := store.Load(ctx, north, southTask); err != nil || ok {
		t.Errorf("a GLOBAL caller of org north loaded org south's pick: ok=%v err=%v", ok, err)
	}
	if _, found, err := store.FindApplied(ctx, north, key); err != nil || found {
		t.Errorf("a GLOBAL caller of org north resolved org south's key: found=%v err=%v", found, err)
	}
}
