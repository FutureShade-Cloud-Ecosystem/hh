//go:build integration

// org_stamp_integration_test.go (UX-95): a task written with no caller org (the REST weight request) takes
// the branch's org, and a caller org still wins when given.
package tasks

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// plantUX95Order plants an org, a branch carrying it and one order there, and removes the order's tasks and the
// order at test end.
func plantUX95Order(t *testing.T) (pool *pgxpool.Pool, orgID, branchID, ord string) {
	t.Helper()
	pool = yardPool(t)
	ctx := context.Background()
	orgID, branchID = testkit.PlantOrgBranch(t, pool)
	var cust string
	if err := pool.QueryRow(ctx, `INSERT INTO customers (account_no, name) VALUES ('UX95T-' || gen_random_uuid()::text, 'ux95 task') RETURNING id::text`).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('#UX95-' || gen_random_uuid()::text, $1::uuid, $2::uuid, 'CONFIRMED', 'PICKUP', 100, 100) RETURNING id::text`, cust, branchID).Scan(&ord); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM task_events WHERE task_id IN (SELECT id FROM tasks WHERE order_id = $1::uuid)`, ord)
		_, _ = pool.Exec(c, `DELETE FROM tasks WHERE order_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, cust)
	})
	return pool, orgID, branchID, ord
}

func TestIntegration_UX95_TaskWithNoCallerOrgTakesTheBranchOrg(t *testing.T) {
	pool, orgID, branchID, ord := plantUX95Order(t)
	ctx := context.Background()
	line := 1
	tk, err := NewRepository(pool).CreateTask(ctx, NewTaskInput{
		OrderID: ord, OrderLineNo: &line, BranchID: branchID, TaskType: TypeNeedWeight,
		Priority: PriorityHigh, Source: SourceManual,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var got *string
	if err := pool.QueryRow(ctx, `SELECT org_id::text FROM tasks WHERE id = $1::uuid`, tk.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != orgID {
		t.Fatalf("tasks.org_id = %v, want the branch org %q", got, orgID)
	}
}

// A task written by the REST door (no caller org) reaches the device pull of its scope: the weights pull for a
// NEED_WEIGHT task and the picks pull for a PICK task, both under the branch's org, and not another org's.
func TestIntegration_UX95_ATaskFromAnyDoorReachesTheDevicePull(t *testing.T) {
	pool, orgID, branchID, ord := plantUX95Order(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	line := 1
	weight, err := repo.CreateTask(ctx, NewTaskInput{OrderID: ord, OrderLineNo: &line, BranchID: branchID,
		TaskType: TypeNeedWeight, Priority: PriorityHigh, Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	pick, err := repo.CreateTask(ctx, NewTaskInput{OrderID: ord, BranchID: branchID,
		TaskType: TypePick, Priority: PriorityNormal, Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	has := func(es []sync.Entity, id string) bool {
		for _, e := range es {
			if e.ID == id {
				return true
			}
		}
		return false
	}
	own := sync.ScopeFilter{OrgID: orgID}
	other := sync.ScopeFilter{OrgID: "00000000-0000-4000-8000-0000000000ee"}
	wes, _, err := NewWeightSyncStore(pool, nil, nil).Pull(ctx, own, 0, 500)
	if err != nil || !has(wes, weight.ID) {
		t.Fatalf("weights pull under the branch org lacks the REST weight task %s (err %v)", weight.ID, err)
	}
	if wes, _, _ := NewWeightSyncStore(pool, nil, nil).Pull(ctx, other, 0, 500); has(wes, weight.ID) {
		t.Fatal("weights pull under another org carries the task")
	}
	pes, _, err := NewSyncStore(pool, nil, nil).Pull(ctx, own, 0, 500)
	if err != nil || !has(pes, pick.ID) {
		t.Fatalf("picks pull under the branch org lacks the pick task %s (err %v)", pick.ID, err)
	}
}
