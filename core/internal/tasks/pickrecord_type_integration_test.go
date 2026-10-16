//go:build integration

// pickrecord_type_integration_test.go: the task type guard at the SQL row, where the review's
// section 4 finding lives. RecordPickLocked locks the tasks row by id and used to check the status
// only, so a NEED_WEIGHT row took a pick record: the apply callback (the order module's ApplyPicks)
// ran, the order line backordered and the order repriced, for material the yard never picked. The
// database is the only place that fault can be proved closed, because the fake never had it in its
// SQL form.
//
// Run with DATABASE_URL set to a throwaway migrated database; the legs skip politely without one.

package tasks

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRecordPickLocked_RefusesATaskThatIsNotAPick seeds a NEED_WEIGHT row beside a PICK control, in
// its own org, and proves: the not-a-pick refusal for the NEED_WEIGHT id, the order callback never
// called, no task_pick_lines row, and the control PICK task still recording.
func TestRecordPickLocked_RefusesATaskThatIsNotAPick(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("pick record type leg: DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("database unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	repo := NewRepository(pool)
	ctx := context.Background()

	org := uuid.NewString()
	if _, err := pool.Exec(ctx,
		`INSERT INTO org (id, slug, name) VALUES ($1, $2, 'pick type guard leg')`, org, "yptg-"+org); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM tasks WHERE org_id = $1`, org)
		_, _ = pool.Exec(bg, `DELETE FROM org WHERE id = $1`, org)
	})

	order := uuid.NewString()
	weightRow := uuid.NewString()
	pickRow := uuid.NewString()
	for _, row := range []struct {
		id  string
		typ string
	}{{weightRow, TypeNeedWeight}, {pickRow, TypePick}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO tasks (id, task_type, order_id, priority, status, source, org_id, seq)
			VALUES ($1, $2, $3::uuid, 'NORMAL', 'QUEUED', 'MANUAL', $4::uuid, 1)`,
			row.id, row.typ, order, org); err != nil {
			t.Fatalf("seed %s task: %v", row.typ, err)
		}
	}

	// applySpy answers whether the record body reached the order module.
	applyRan := false
	apply := func(context.Context, Task) ([]PickedLineInput, error) {
		applyRan = true
		return []PickedLineInput{{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"}}, nil
	}

	_, err = repo.RecordPickLocked(ctx, weightRow, "u-1", apply)
	if err == nil {
		t.Fatal("a NEED_WEIGHT row must not take a pick record")
	}
	if !errors.Is(err, ErrNotAPick) {
		t.Fatalf("want the not-a-pick domain refusal, got %v", err)
	}
	if applyRan {
		t.Fatal("the order module must never be told: the apply callback ran")
	}
	var lines int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM task_pick_lines WHERE task_id = $1`, weightRow).Scan(&lines); err != nil {
		t.Fatalf("count pick lines: %v", err)
	}
	if lines != 0 {
		t.Fatalf("nothing may be recorded against the NEED_WEIGHT row, found %d line(s)", lines)
	}

	got, err := repo.RecordPickLocked(ctx, pickRow, "u-1", apply)
	if err != nil {
		t.Fatalf("the PICK control row still records: %v", err)
	}
	if len(got) != 1 || got[0].OrderLineNo != 1 {
		t.Fatalf("the control's recorded line is wrong: %+v", got)
	}
}
