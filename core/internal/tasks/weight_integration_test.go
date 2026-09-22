//go:build integration

// weight_integration_test.go, the DIB-324 / DIB-325 persistence against a real, migrated Postgres.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run W3Weight ./internal/tasks/
//
// What only a database can prove, and is therefore proved here:
//   - the 0108 columns exist and the weight write + its THREE task_events rows commit together;
//   - a NEED_WEIGHT task actually satisfies 0073's task_type CHECK on the real write path;
//   - the seeded advance rules are OFF, in the database, for every task type, the ruling's default
//     is a row, and a Go constant asserting it would prove nothing;
//   - the enabled-needs-a-target CHECK refuses a half-configured rule.
//
// FIXTURES RESTORE WHAT THEY TOUCH: each test allocates its own synthetic order id (tasks.order_id
// carries no FK by design, 0073) and deletes every row it made. task_status_advance_rules is READ,
// never written, except in the one test that flips a rule and restores it inside the same test.

package tasks

import (
	"context"
	"errors"
	"testing"
)

func newWeightFixture(t *testing.T) (*Service, *Repository, string) {
	t.Helper()
	pool := yardPool(t)
	ctx := context.Background()

	var orderID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&orderID); err != nil {
		t.Fatalf("mint order id: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM task_events WHERE task_id IN (SELECT id FROM tasks WHERE order_id = $1::uuid)`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM tasks WHERE order_id = $1::uuid`, orderID)
	})
	repo := NewRepository(pool)
	svc := NewService(repo).WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
		orderID: {ID: orderID, BranchID: trentonBranch, Fulfillment: "PICKUP", Status: "CONFIRMED"},
	}})
	return svc, repo, orderID
}

func TestW3Weight_TheWriteAndItsThreeAuditHopsCommitTogether(t *testing.T) {
	svc, repo, orderID := newWeightFixture(t)
	lines := &fakeLines{uom: "T"}
	svc = svc.WithOrderLineWriter(lines)

	task, err := svc.RequestWeight(context.Background(), WeightRequestInput{
		OrderID: orderID, OrderLineNo: 2, UOM: "T", RequestedBy: "",
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if task.TaskType != TypeNeedWeight {
		t.Fatalf("0073's task_type CHECK admitted %q, want NEED_WEIGHT", task.TaskType)
	}

	res, err := svc.SubmitWeight(context.Background(), WeightInput{
		TaskID: task.ID, Weight: "2.8700", UOM: "T",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if !res.OrderSynced {
		t.Fatalf("order not synced: %q", res.OrderError)
	}

	got, err := repo.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != StatusDone || got.WeightEntered != "2.8700" || got.WeightUOM != "T" {
		t.Fatalf("task = %+v", got)
	}
	if got.WeightSyncedAt == nil {
		t.Fatal("0108's weight_synced_at must be stamped on a successful sync")
	}

	// One tap in the yard, three legal hops in the trail. This is the assertion that keeps the audit
	// from disagreeing with the matrix the rest of the module is enforced against.
	pool := yardPool(t)
	rows, err := pool.Query(context.Background(),
		`SELECT from_status, to_status FROM task_events WHERE task_id = $1 ORDER BY created_at, id`, task.ID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer rows.Close()
	var hops [][2]string
	for rows.Next() {
		var from *string
		var to string
		if err := rows.Scan(&from, &to); err != nil {
			t.Fatalf("scan: %v", err)
		}
		f := ""
		if from != nil {
			f = *from
		}
		hops = append(hops, [2]string{f, to})
	}
	// birth (NULL→QUEUED) plus the three walked hops.
	if len(hops) != 4 {
		t.Fatalf("task_events rows = %d (%v), want 4", len(hops), hops)
	}
	want := [][2]string{
		{"", StatusQueued},
		{StatusQueued, StatusAccepted},
		{StatusAccepted, StatusInProgress},
		{StatusInProgress, StatusDone},
	}
	for i := range want {
		if hops[i] != want[i] {
			t.Fatalf("hops = %v, want %v", hops, want)
		}
	}
}

func TestW3Weight_ARefusedOrderWriteLeavesTheQueryableGap(t *testing.T) {
	svc, repo, orderID := newWeightFixture(t)
	svc = svc.WithOrderLineWriter(&fakeLines{
		uom:    "T",
		setErr: errors.New("order: this order's lines can no longer be edited (status FULFILLED)"),
	})
	task, err := svc.RequestWeight(context.Background(), WeightRequestInput{OrderID: orderID, OrderLineNo: 1, UOM: "T"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := svc.SubmitWeight(context.Background(), WeightInput{TaskID: task.ID, Weight: "3.1", UOM: "T"}); err != nil {
		t.Fatalf("the measurement must survive the order's refusal: %v", err)
	}
	got, err := repo.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.WeightEntered != "3.1000" {
		t.Fatalf("weight = %q, want it kept", got.WeightEntered)
	}
	if got.WeightSyncedAt != nil {
		t.Fatal("a refused write must leave weight_synced_at NULL, 0108 indexes exactly that")
	}
	if got.WeightSyncError == "" {
		t.Fatal("the order module's refusal must be readable on the task")
	}
	// And the row is findable by the reconciliation read the index exists for.
	pool := yardPool(t)
	var n int
	if err := pool.QueryRow(context.Background(), `
SELECT count(*)::int FROM tasks
 WHERE id = $1 AND task_type = 'NEED_WEIGHT'
   AND weight_entered IS NOT NULL AND weight_synced_at IS NULL`, task.ID).Scan(&n); err != nil {
		t.Fatalf("gap query: %v", err)
	}
	if n != 1 {
		t.Fatal("the gap must be listable, that is the whole reason it is a timestamp and not a swallow")
	}
}

func TestW3Advance_EverySeededRuleIsOff(t *testing.T) {
	// The ruling's default is a ROW. Asserting it in Go would prove nothing about the deployment.
	_, repo, _ := newWeightFixture(t)
	for _, taskType := range []string{TypePick, TypeDispatch, TypeNeedWeight, TypeOther} {
		rule, err := repo.AdvanceRule(context.Background(), taskType)
		if err != nil {
			t.Fatalf("read rule %s: %v", taskType, err)
		}
		if rule.Fires() {
			t.Fatalf("%s advances orders by default, that is a product ruling nobody made", taskType)
		}
	}
}

func TestW3Advance_AnUnknownTaskTypeIsADisabledRuleNotAnError(t *testing.T) {
	_, repo, _ := newWeightFixture(t)
	rule, err := repo.AdvanceRule(context.Background(), "SOMETHING_NEW")
	if err != nil {
		t.Fatalf("an unknown type must not be an error: %v", err)
	}
	if rule.Fires() {
		t.Fatal("a type with no row cannot advance anything")
	}
}

func TestW3Advance_TheSchemaRefusesASwitchWiredToNothing(t *testing.T) {
	pool := yardPool(t)
	if _, err := pool.Exec(context.Background(),
		`UPDATE task_status_advance_rules SET enabled = TRUE, target_status = '' WHERE task_type = 'OTHER'`); err == nil {
		// If it somehow succeeded, put it back before failing, a fixture restores what it touches.
		_, _ = pool.Exec(context.Background(),
			`UPDATE task_status_advance_rules SET enabled = FALSE, target_status = '' WHERE task_type = 'OTHER'`)
		t.Fatal("0108's CHECK must refuse an enabled rule with no target")
	}
}
