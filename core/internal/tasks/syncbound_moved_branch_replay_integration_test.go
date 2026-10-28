//go:build integration

// syncbound_moved_branch_replay_integration_test.go, UX-103 round 4: no key creates twice for
// anyone. The pick and weights stores stamp a row with the ORDER's branch at create time while
// their create fence reads the order's CURRENT branch, so when the order moves branch along with
// the caller's reach, the round 3 gate hides the binder's own key from its binder and the replay
// re-enters Create: the order ends with two picks, the regression S2 probed at 0fa16b50. The
// ruling pairs two fixes: the ledger records WHO bound the key (sync_op_hashes.bound_by,
// migration 0449) and the binder's replay keeps the bound answer whatever its Load says; and a
// caller who is NOT the binder who cannot see the live entity is answered the seam's row not
// found before the engine can reach store.Create. A row bound before 0449 (bound_by NULL) keeps
// the bound answer for every caller. Red at 0fa16b50.
//
// Run with DATABASE_URL set to a migrated and seeded throwaway database.
package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// movingOrders is the order seam whose branch answer moves mid test: the real adapter reads the
// order fresh on every create, so an order transferred to another branch is created on the old
// branch and fenced by the new one afterwards.
type movingOrders struct{ branch *string }

func (o movingOrders) OrderBrief(_ context.Context, id string) (OrderBrief, error) {
	return OrderBrief{ID: id, BranchID: *o.branch}, nil
}

func (o movingOrders) OrderBriefInOrg(_ context.Context, _, id string) (OrderBrief, error) {
	return OrderBrief{ID: id, BranchID: *o.branch}, nil
}

// pushAsFilter pushes through the door's engine under an explicit scope filter, the actor riding
// the filter as the middleware stamps it, with the door's staff authority answering sc.
func pushAsFilter(d *pickDoor, f hhsync.ScopeFilter, sc identity.StaffContext, scope string, ops ...apigen.SyncPushOperation) []apigen.SyncPushResult {
	d.t.Helper()
	d.authority = func() (identity.StaffContext, bool, error) { return sc, true, nil }
	info, ok := hhsync.Lookup(scope)
	if !ok {
		d.t.Fatalf("scope %s missing from the registry", scope)
	}
	resp, err := d.engine.Push(context.Background(), info, f, ops)
	if err != nil {
		d.t.Fatalf("push %s: %v", scope, err)
	}
	return resp.Results
}

// useMovingOrders points the pick store's order seam (both the plain and the org held read) at
// the moving double, everything else the door wired standing.
func useMovingOrders(d *pickDoor, branch *string) {
	moving := movingOrders{branch: branch}
	d.store.orders = moving
	d.store.orgOrders = moving
}

// countForOrder counts the live task rows of one type naming an order.
func countForOrder(d *pickDoor, taskType, orderID string) int {
	d.t.Helper()
	var n int
	if err := d.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM tasks WHERE org_id = $1::uuid AND task_type = $2 AND order_id = $3::uuid AND deleted_at IS NULL`,
		d.org, taskType, orderID).Scan(&n); err != nil {
		d.t.Fatalf("count %s rows for the order: %v", taskType, err)
	}
	return n
}

// nullTheBinder makes a ledger entry a pre 0449 row: the binding stands, the binder is unknown.
func nullTheBinder(d *pickDoor, scope, key string) {
	d.t.Helper()
	if _, err := d.pool.Exec(context.Background(),
		`UPDATE sync_op_hashes SET bound_by = NULL WHERE org_id = $1::uuid AND tenant_id = '' AND scope = $2 AND client_id = $3::uuid`,
		d.org, scope, key); err != nil {
		d.t.Fatalf("null the binder: %v", err)
	}
}

// TestPickReplayMovedBranch_TheOwnerMovedBranchKeepsTheBoundAnswer: S2's probe, made permanent. A
// device creates a pick for an order under key K; the order then moves branch and the same
// device's reach moves with it (a staff transfer keeps the platform subject). The identical replay
// answers applied with no seq and the order holds ONE pick: the binder's own key keeps its bound
// answer whatever its Load says, and the create fence reading the order's current branch never
// gets a second say.
func TestPickReplayMovedBranch_TheOwnerMovedBranchKeepsTheBoundAnswer(t *testing.T) {
	d := newPickDoor(t)
	branch := d.branchA
	useMovingOrders(d, &branch)
	owner := hhsync.ScopeFilter{OrgID: d.org, Actor: uuid.NewString()}
	key := uuid.NewString()
	order := uuid.NewString()
	create := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{"order_id": order}}

	if first := pushAsFilter(d, owner, scopedTo(d.user, d.branchA), "pick_tasks", create); first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("setup: the branch A create applies: %+v (%s)", first[0], errString(first[0]))
	}
	if n := countForOrder(d, "PICK", order); n != 1 {
		t.Fatalf("setup: %d picks for the order, want 1", n)
	}

	// The order moves to branch B and the device's reach moves with it. The row is stamped with
	// branch A, so the owner's own Load misses it from branch B.
	branch = d.branchB
	again := pushAsFilter(d, owner, scopedTo(d.user, d.branchB), "pick_tasks", create)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the owner's replay after the move answers applied with no seq: got %+v (%s)", again[0], errString(again[0]))
	}
	if n := countForOrder(d, "PICK", order); n != 1 {
		t.Fatalf("the replay applies nothing twice: %d picks for the order, want 1", n)
	}

	// The binder's tampered replay is still the bound refusal.
	tampered := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{"order_id": order, "priority": "HIGH"}}
	if reused := pushAsFilter(d, owner, scopedTo(d.user, d.branchB), "pick_tasks", tampered); reused[0].Status != "rejected" || !strings.Contains(errString(reused[0]), "key_reused") {
		t.Fatalf("the owner's tampered replay is still key_reused: %+v (%s)", reused[0], errString(reused[0]))
	}
	if n := countForOrder(d, "PICK", order); n != 1 {
		t.Fatalf("the refused reuse writes nothing: %d picks for the order, want 1", n)
	}
}

// TestPickReplayMovedBranch_AColleaguesReplayOfTheOwnersKeyIsTheFencedRefusal: a caller who is not
// the binder, unable to see the live pick (the row sits on the branch the order left), is answered
// the seam's refusal row not found, never key_reused and never a create: the engine never reaches
// store.Create for a bound key whose entity exists, and the create fence reading the order's
// CURRENT branch would otherwise pass this very colleague and mint the second pick.
func TestPickReplayMovedBranch_AColleaguesReplayOfTheOwnersKeyIsTheFencedRefusal(t *testing.T) {
	d := newPickDoor(t)
	branch := d.branchA
	useMovingOrders(d, &branch)
	owner := hhsync.ScopeFilter{OrgID: d.org, Actor: uuid.NewString()}
	colleague := hhsync.ScopeFilter{OrgID: d.org, Actor: uuid.NewString()}
	key := uuid.NewString()
	order := uuid.NewString()
	create := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{"order_id": order}}
	if first := pushAsFilter(d, owner, scopedTo(d.user, d.branchA), "pick_tasks", create); first[0].Status != "applied" {
		t.Fatalf("setup: the owner's create applies: %+v (%s)", first[0], errString(first[0]))
	}

	branch = d.branchB
	same := pushAsFilter(d, colleague, scopedTo(uuid.NewString(), d.branchB), "pick_tasks", create)
	if same[0].Status != "rejected" || errString(same[0]) != hhsync.ErrRowNotFound.Error() {
		t.Fatalf("a colleague's replay of the owner's key is the fenced refusal: got %+v (%s), want rejected %q", same[0], errString(same[0]), hhsync.ErrRowNotFound.Error())
	}
	other := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{"order_id": order, "priority": "HIGH"}}
	otherRes := pushAsFilter(d, colleague, scopedTo(uuid.NewString(), d.branchB), "pick_tasks", other)
	if otherRes[0].Status != "rejected" || errString(otherRes[0]) != hhsync.ErrRowNotFound.Error() {
		t.Fatalf("a colleague's replay with another body is the fenced refusal, never key_reused: got %+v (%s)", otherRes[0], errString(otherRes[0]))
	}
	if n := countForOrder(d, "PICK", order); n != 1 {
		t.Fatalf("the colleague's replays create nothing: %d picks for the order, want 1", n)
	}

	// The owner, moved onto branch B too, still keeps the bound answer.
	again := pushAsFilter(d, owner, scopedTo(d.user, d.branchB), "pick_tasks", create)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the owner's own replay still answers applied with no seq: %+v (%s)", again[0], errString(again[0]))
	}
	if n := countForOrder(d, "PICK", order); n != 1 {
		t.Fatalf("one pick stands: %d, want 1", n)
	}
}

// TestPickReplayMovedBranch_AnOldLedgerRowWithNoBinderKeepsTheBoundAnswer: a row bound before
// migration 0449 has no binder to name. Its key keeps the bound answer for every caller, the
// owner's replay included, the gate failing toward idempotency exactly as it does for a store
// that cannot answer existence: with the binder unknowable, refusing could answer the owner's own
// replay with a refusal, and hiding could apply a second time.
func TestPickReplayMovedBranch_AnOldLedgerRowWithNoBinderKeepsTheBoundAnswer(t *testing.T) {
	d := newPickDoor(t)
	branch := d.branchA
	useMovingOrders(d, &branch)
	owner := hhsync.ScopeFilter{OrgID: d.org, Actor: uuid.NewString()}
	key := uuid.NewString()
	order := uuid.NewString()
	create := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{"order_id": order}}
	if first := pushAsFilter(d, owner, scopedTo(d.user, d.branchA), "pick_tasks", create); first[0].Status != "applied" {
		t.Fatalf("setup: the create applies: %+v (%s)", first[0], errString(first[0]))
	}
	nullTheBinder(d, "pick_tasks", key)

	branch = d.branchB
	again := pushAsFilter(d, owner, scopedTo(d.user, d.branchB), "pick_tasks", create)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the replay of a pre 0449 key answers applied with no seq, the bound answer: got %+v (%s)", again[0], errString(again[0]))
	}
	if n := countForOrder(d, "PICK", order); n != 1 {
		t.Fatalf("the replay applies nothing twice: %d picks for the order, want 1", n)
	}
	tampered := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{"order_id": order, "priority": "HIGH"}}
	if reused := pushAsFilter(d, owner, scopedTo(d.user, d.branchB), "pick_tasks", tampered); reused[0].Status != "rejected" || !strings.Contains(errString(reused[0]), "key_reused") {
		t.Fatalf("a pre 0449 key with another body is still key_reused: %+v (%s)", reused[0], errString(reused[0]))
	}
}

// TestWeightReplayMovedBranch_TheOwnerMovedBranchKeepsTheBoundAnswer: the same guarantee on the
// weights scope, whose NEED_WEIGHT rows are stamped with the order's branch at create time while
// the create fence reads the order's current one, the exact shape S2's probe takes there too.
func TestWeightReplayMovedBranch_TheOwnerMovedBranchKeepsTheBoundAnswer(t *testing.T) {
	d := newPickDoor(t)
	branch := d.branchA
	useMovingOrders(d, &branch)
	w := NewWeightSyncStore(d.pool, movingOrders{branch: &branch}, nil).WithStaffAuthority(
		func(c context.Context, sub, org string) (identity.StaffContext, bool, error) { return d.authority() })
	if err := d.engine.RegisterScope("weights", w); err != nil {
		t.Fatalf("register weights: %v", err)
	}
	owner := hhsync.ScopeFilter{OrgID: d.org, Actor: uuid.NewString()}
	key := uuid.NewString()
	order := uuid.NewString()
	create := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{"order_id": order, "order_line_no": float64(1)}}

	if first := pushAsFilter(d, owner, scopedTo(d.user, d.branchA), "weights", create); first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("setup: the branch A weight create applies: %+v (%s)", first[0], errString(first[0]))
	}
	if n := countForOrder(d, "NEED_WEIGHT", order); n != 1 {
		t.Fatalf("setup: %d weight rows for the order, want 1", n)
	}

	branch = d.branchB
	again := pushAsFilter(d, owner, scopedTo(d.user, d.branchB), "weights", create)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the owner's replay after the move answers applied with no seq: got %+v (%s)", again[0], errString(again[0]))
	}
	if n := countForOrder(d, "NEED_WEIGHT", order); n != 1 {
		t.Fatalf("the replay applies nothing twice: %d weight rows for the order, want 1", n)
	}
	tampered := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{"order_id": order, "order_line_no": float64(2)}}
	if reused := pushAsFilter(d, owner, scopedTo(d.user, d.branchB), "weights", tampered); reused[0].Status != "rejected" || !strings.Contains(errString(reused[0]), "key_reused") {
		t.Fatalf("the owner's tampered replay is still key_reused: %+v (%s)", reused[0], errString(reused[0]))
	}
}
