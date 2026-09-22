package tasks

// weight_repository.go, the 0108 persistence: the NEED_WEIGHT write, its sync stamp, and the
// order-advance ruling read.
//
// The repository rule this module established in 0073 holds here without exception: a status move and
// its task_events row commit together, and the legality check runs on a FOR UPDATE-locked pre-image
// so the recorded from→to is exactly the transition that landed.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// WeightWrite is one weight measurement being recorded and completed.
type WeightWrite struct {
	TaskID  string
	ActorID string
	Weight  string // already validated + normalised to NUMERIC(19,4) by the service
	UOM     string
	Note    string
}

// pathToDone is the legal chain from a status to DONE, as the 0073 matrix defines it. It is derived
// from allowedTransitions rather than written out, so it CANNOT drift from the matrix: if a future
// migration changes what may reach DONE, this walks the new graph.
//
// nil means DONE is unreachable from `from`, a terminal status, or one with no forward path.
func pathToDone(from string) []string {
	if from == StatusDone {
		return nil // already there; the caller reports ErrNoTransition
	}
	// Breadth-first over the matrix. The graph has six nodes; a shortest-path walk is both trivially
	// cheap and the honest way to say "the fewest legal moves that get there".
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == StatusDone {
			chain := []string{}
			for at := cur; at != from; at = prev[at] {
				chain = append([]string{at}, chain...)
			}
			return chain
		}
		for next := range allowedTransitions[cur] {
			if _, seen := prev[next]; seen {
				continue
			}
			// CANCELLED is a terminal exit, never a stepping stone: routing "to DONE" through a cancel
			// would be a data-loss bug wearing a shortest-path hat.
			//
			// BELT AND BRACES, AND KNOWN TO BE. A mutation that deleted this skip did NOT fail the
			// suite (REPORT-W3-C.md, M10), because CANCELLED currently has no outgoing edges and so
			// can never lie on a path to DONE. It is kept because the guard is about what the matrix
			// is ALLOWED to become, not about what it is today: the first migration that gives
			// CANCELLED an outgoing edge would otherwise silently route weight submissions through
			// a cancel. The honest statement is that it is unreachable now and load-bearing later.
			if next == StatusCancelled {
				continue
			}
			prev[next] = cur
			queue = append(queue, next)
		}
	}
	return nil
}

// RecordWeight stamps the measurement and walks the task to DONE in ONE transaction, writing one
// task_events row per legal hop.
//
// WHY EVERY HOP IS RECORDED. A picker types one number; the state machine still says a task goes
// QUEUED → ACCEPTED → IN_PROGRESS → DONE. Collapsing that into a single event row would make the
// audit trail disagree with the matrix the rest of the module is enforced against, and the next
// person to read task_events would find a transition that the code claims is impossible. Three rows
// with the same actor and the same timestamp are the truthful record of one act that crossed three
// states.
func (r *Repository) RecordWeight(ctx context.Context, in WeightWrite) (Task, error) {
	if r.pool == nil {
		return Task{}, errNoDB
	}
	if !looksLikeUUID(in.TaskID) {
		return Task{}, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: begin weight: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status, taskType string
	if err := tx.QueryRow(ctx,
		`SELECT status, task_type FROM tasks WHERE id = $1 FOR UPDATE`, in.TaskID).
		Scan(&status, &taskType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("tasks: lock task %q: %w", in.TaskID, err)
	}
	if taskType != TypeNeedWeight {
		return Task{}, ErrNotWeightTask
	}
	if status == StatusDone {
		return Task{}, ErrNoTransition
	}
	chain := pathToDone(status)
	if len(chain) == 0 {
		return Task{}, ErrIllegalTransition
	}

	// created_at is CLOCK_TIMESTAMP(), not the column default.
	//
	// The default is now(), which in Postgres is TRANSACTION start time, identical for all three
	// rows written here. Three audit rows with the same timestamp and random uuid primary keys cannot
	// be read back in the order they happened, so `ORDER BY created_at` returns them shuffled and the
	// trail stops being able to answer "what order did this move in". (Found by this bundle's own
	// integration test under -shuffle=on, which is exactly what an order-independence gate is for.)
	// clock_timestamp() advances between statements, so the three hops are strictly ordered.
	from := status
	for _, to := range chain {
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_events (task_id, from_status, to_status, actor_id, note, created_at)
			VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, $5, clock_timestamp())`,
			in.TaskID, from, to, in.ActorID, in.Note); err != nil {
			return Task{}, fmt.Errorf("tasks: weight event %s→%s: %w", from, to, err)
		}
		from = to
	}

	// The weight and the terminal status land in ONE statement. claimed_by/claimed_at are stamped
	// here too when the chain passed through ACCEPTED, the person who typed the weight IS the person
	// who did the work, and a DONE task with no claimant reads like nobody touched it.
	t, err := scanYardTask(tx.QueryRow(ctx, `
		UPDATE tasks
		   SET status         = 'DONE',
		       weight_entered = $2::numeric,
		       weight_uom     = $3,
		       done_at        = now(),
		       claimed_by     = COALESCE(claimed_by, NULLIF($4,'')::uuid),
		       claimed_at     = COALESCE(claimed_at, now()),
		       updated_at     = now()
		 WHERE id = $1
		RETURNING `+yardColumns,
		in.TaskID, in.Weight, in.UOM, in.ActorID))
	if err != nil {
		return Task{}, fmt.Errorf("tasks: record weight %q: %w", in.TaskID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("tasks: commit weight: %w", err)
	}
	return t, nil
}

// MarkWeightSync stamps whether the weight reached the order line (0108).
//
// A SUCCESS CLEARS THE PREVIOUS MESSAGE. A retry that finally lands must not leave last attempt's
// refusal on the row, where a reconciliation read would count it as still broken.
func (r *Repository) MarkWeightSync(ctx context.Context, taskID string, at *time.Time, errText string) error {
	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(taskID) {
		return ErrNotFound
	}
	if at != nil {
		errText = ""
	}
	if len(errText) > 400 {
		errText = errText[:400]
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE tasks SET weight_synced_at = $2, weight_sync_error = $3, updated_at = now() WHERE id = $1`,
		taskID, at, errText)
	return err
}

// AdvanceRule reads one task type's order-advance ruling (0108).
//
// AN UNKNOWN TYPE IS A DISABLED RULE, NOT AN ERROR. A task type with no row cannot advance anything,
// and turning that into an error would make a completely normal completion path fail on a deployment
// whose seed data is a version behind.
func (r *Repository) AdvanceRule(ctx context.Context, taskType string) (AdvanceRule, error) {
	if r.pool == nil {
		return AdvanceRule{}, errNoDB
	}
	rule := AdvanceRule{TaskType: taskType}
	err := r.pool.QueryRow(ctx,
		`SELECT enabled, target_status FROM task_status_advance_rules WHERE task_type = $1`, taskType).
		Scan(&rule.Enabled, &rule.TargetStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdvanceRule{TaskType: taskType}, nil
	}
	if err != nil {
		return AdvanceRule{}, fmt.Errorf("tasks: read advance rule %q: %w", taskType, err)
	}
	return rule, nil
}
