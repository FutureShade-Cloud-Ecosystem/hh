package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// yard_repository.go, persistence for the Wave 2 yard flow (0095). It follows the 0073 rules without
// exception: every status move locks its row FOR UPDATE, checks legality against the SHARED
// canTransition matrix on that locked pre-image, and writes its task_events row inside the same
// transaction. Nothing here can move a task without leaving the history edge behind.

// StagedNotice is the fact "this order is picked and parked, ready for the customer", handed to the
// StageOutbox seam INSIDE the stage transaction. It is a value type this module owns: tasks knows
// nothing about SMS, phone numbers or providers, only that somebody out-of-band may want to say this.
type StagedNotice struct {
	TaskID        string
	OrderID       string
	BranchID      string
	StageLocation string
	StageRef      string
}

// StageOutbox is the durable out-of-band notification seam. It is handed the SAME pgx transaction as
// the stage write, so the notice and the stage commit together or neither happens, a customer must
// never be told their order is ready by a transaction that then rolled back, and an enqueue failure
// must take the stage down with it rather than leaving a silent gap.
//
// Un-wired (nil) it is simply skipped: staging still works, nothing is queued. Satisfied at the
// composition root by an adapter over the SMS notification outbox (0097); tasks imports nothing.
type StageOutbox interface {
	EnqueueStagedTx(ctx context.Context, tx pgx.Tx, n StagedNotice) error
}

// WithStageOutbox injects the out-of-band notice seam (see StageOutbox).
func (r *Repository) WithStageOutbox(o StageOutbox) *Repository { r.stageOutbox = o; return r }

// yardColumns extends the canonical task projection with the 0095 stage stamp plus the two shortage
// counts. The counts are scalar sub-selects rather than denormalised columns: a counter column would
// need maintaining in every path that touches task_shortages, and the first path that forgot would
// leave the yard board quietly lying about open shortages. Sub-selects are legal in both SELECT and
// RETURNING, so Create/Get/List/Transition/Stage all scan the identical shape.
//
// W3 (Swarm C) extends it once more with the 0108 weight-sync stamp. Same discipline: appended to the
// SAME constant so Create/Get/List/Transition/Stage/RecordWeight still all scan one identical shape,
// and a NEED_WEIGHT task's "did the order take it" answer travels with the task rather than needing a
// second read on every surface that shows one.
const yardColumns = taskColumns + `,
	staged_at, COALESCE(stage_location, ''), COALESCE(stage_ref, ''),
	(SELECT COUNT(*) FROM task_shortages ts WHERE ts.task_id = tasks.id AND ts.resolved_at IS NULL),
	(SELECT COUNT(*) FROM task_shortages ts WHERE ts.task_id = tasks.id),
	weight_synced_at, COALESCE(weight_sync_error, '')`

// scanYardTask reads the extended projection. It delegates the 0073 columns to the shared scanner's
// column order by re-declaring the same targets, because pgx has no way to split one row across two
// scanners, the cost of that is this function, and the guard against drift is that both constants
// are built from the same taskColumns base.
func scanYardTask(row rowScanner) (Task, error) {
	var t Task
	var lineNo *int
	var claimedAt, pushedAt, deadlineAt, doneAt, stagedAt, weightSyncedAt *time.Time
	if err := row.Scan(
		&t.ID, &t.TaskType, &t.OrderID, &lineNo,
		&t.BranchID, &t.Priority, &t.Status,
		&t.CreatedBy, &t.AssignedTo, &t.ClaimedBy,
		&claimedAt, &pushedAt, &deadlineAt, &doneAt,
		&t.PhotoURL, &t.WeightEntered, &t.WeightUOM,
		&t.Source, &t.SourceMessageID, &t.CreatedAt, &t.UpdatedAt,
		&stagedAt, &t.StageLocation, &t.StageRef,
		&t.OpenShortages, &t.TotalShortages,
		&weightSyncedAt, &t.WeightSyncError,
	); err != nil {
		return Task{}, err
	}
	t.OrderLineNo = lineNo
	t.ClaimedAt = claimedAt
	t.PushedAt = pushedAt
	t.AcceptDeadlineAt = deadlineAt
	t.DoneAt = doneAt
	t.StagedAt = stagedAt
	t.WeightSyncedAt = weightSyncedAt
	return t, nil
}

// Stage applies IN_PROGRESS → STAGED atomically: lock the row, enforce the state machine on the
// locked pre-image, write status + the stage stamp, append the task_events row, and hand the
// out-of-band notice to the StageOutbox seam, all in ONE transaction (the transactional-outbox
// pattern this codebase already uses in pos/inventory/portal).
func (r *Repository) Stage(ctx context.Context, in StageInput) (Task, error) {
	if r.pool == nil {
		return Task{}, errNoDB
	}
	if !looksLikeUUID(in.TaskID) {
		return Task{}, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: begin stage: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, taskType, claimedBy string
	if err := tx.QueryRow(ctx, `SELECT status, task_type, COALESCE(claimed_by::text, '') FROM tasks WHERE id = $1 FOR UPDATE`, in.TaskID).Scan(&from, &taskType, &claimedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("tasks: lock %q for stage: %w", in.TaskID, err)
	}
	// The holder's act: compared on the locked row, in this transaction (UX-76).
	if notHolder(claimedBy, in.ActorID) {
		return Task{}, ErrNotHolder
	}
	if from == StatusStaged {
		return Task{}, ErrNoTransition
	}
	if !canTransition(from, StatusStaged) {
		return Task{}, ErrIllegalTransition
	}

	seq, err := allocateTaskSeqTx(ctx, tx, taskType)
	if err != nil {
		return Task{}, err
	}
	t, err := scanYardTask(tx.QueryRow(ctx, `
		UPDATE tasks SET
			status         = 'STAGED',
			staged_at      = now(),
			stage_location = $2,
			stage_ref      = $3,
			updated_at     = now(),
			seq            = $4
		WHERE id = $1
		RETURNING `+yardColumns,
		in.TaskID, in.Location, in.Ref, seq))
	if err != nil {
		return Task{}, fmt.Errorf("tasks: stage %q: %w", in.TaskID, err)
	}

	note := in.Note
	if note == "" {
		note = "staged at " + in.Location + " (" + in.Ref + ")"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, $2, 'STAGED', NULLIF($3,'')::uuid, $4)`,
		in.TaskID, from, in.ActorID, note); err != nil {
		return Task{}, fmt.Errorf("tasks: insert stage event: %w", err)
	}

	// The out-of-band notice rides the SAME transaction (see StageOutbox). Un-wired = skipped.
	if r.stageOutbox != nil {
		if err := r.stageOutbox.EnqueueStagedTx(ctx, tx, StagedNotice{
			TaskID:        t.ID,
			OrderID:       t.OrderID,
			BranchID:      t.BranchID,
			StageLocation: t.StageLocation,
			StageRef:      t.StageRef,
		}); err != nil {
			return Task{}, fmt.Errorf("tasks: enqueue staged notice: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("tasks: commit stage: %w", err)
	}
	return t, nil
}

// shortageColumns is the canonical task_shortages projection.
const shortageColumns = `id::text, task_id::text, order_line_no, COALESCE(product_id::text, ''),
	sku, description, qty_short::text, uom, note,
	COALESCE(reported_by::text, ''), reported_at,
	resolved_at, COALESCE(resolved_by::text, ''), resolution`

func scanShortage(row rowScanner) (Shortage, error) {
	var s Shortage
	var lineNo *int
	var resolvedAt *time.Time
	if err := row.Scan(
		&s.ID, &s.TaskID, &lineNo, &s.ProductID,
		&s.SKU, &s.Description, &s.QtyShort, &s.UOM, &s.Note,
		&s.ReportedBy, &s.ReportedAt,
		&resolvedAt, &s.ResolvedBy, &s.Resolution,
	); err != nil {
		return Shortage{}, err
	}
	s.OrderLineNo = lineNo
	s.ResolvedAt = resolvedAt
	return s, nil
}

// InsertShortage appends one shortage and returns it alongside the (re-read) task, in one tx. The
// task is re-read INSIDE the transaction so its shortage counts already include this row, the caller
// hands that task to the EVENT post, and a "1 open shortage" board that says 0 for a second would be
// a bug people would chase for an afternoon.
func (r *Repository) InsertShortage(ctx context.Context, in ShortageInput) (Shortage, Task, error) {
	if r.pool == nil {
		return Shortage{}, Task{}, errNoDB
	}
	if !looksLikeUUID(in.TaskID) {
		return Shortage{}, Task{}, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: begin shortage: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the task first: it both proves the task exists (FK would say so, but with a 23503 the
	// caller cannot tell "no such task" from a real failure) and serialises concurrent reports.
	var status, taskType string
	if err := tx.QueryRow(ctx, `SELECT status, task_type FROM tasks WHERE id = $1 FOR UPDATE`, in.TaskID).Scan(&status, &taskType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Shortage{}, Task{}, ErrNotFound
		}
		return Shortage{}, Task{}, fmt.Errorf("tasks: lock %q for shortage: %w", in.TaskID, err)
	}

	// The shortage row draws a shortages-scope seq (0213: the column exists
	// for exactly this feed) and the parent task draws its own scope's seq
	// beside it, because the task's projection carries the shortage counts
	// and a device holding the task row must see the counts move.
	shortageSeq, err := allocateScopeSeqTx(ctx, tx, shortagesSeqScope)
	if err != nil {
		return Shortage{}, Task{}, err
	}
	sh, err := scanShortage(tx.QueryRow(ctx, `
		INSERT INTO task_shortages
			(task_id, order_line_no, product_id, sku, description, qty_short, uom, note, reported_by, seq)
		VALUES ($1::uuid, $2, NULLIF($3,'')::uuid, $4, $5, $6::numeric, $7, $8, NULLIF($9,'')::uuid, $10)
		RETURNING `+shortageColumns,
		in.TaskID, in.OrderLineNo, in.ProductID, in.SKU, in.Description,
		in.QtyShort, in.UOM, in.Note, in.ReportedBy, shortageSeq))
	if err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: insert shortage: %w", err)
	}
	taskSeq, err := allocateTaskSeqTx(ctx, tx, taskType)
	if err != nil {
		return Shortage{}, Task{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET seq = $2 WHERE id = $1`, in.TaskID, taskSeq); err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: bump task after shortage: %w", err)
	}

	t, err := scanYardTask(tx.QueryRow(ctx, `SELECT `+yardColumns+` FROM tasks WHERE id = $1`, in.TaskID))
	if err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: reread task after shortage: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: commit shortage: %w", err)
	}
	return sh, t, nil
}

// ListShortages reads a task's shortages oldest-first.
func (r *Repository) ListShortages(ctx context.Context, taskID string, openOnly bool) ([]Shortage, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(taskID) {
		return nil, ErrNotFound
	}
	q := `SELECT ` + shortageColumns + ` FROM task_shortages WHERE task_id = $1`
	if openOnly {
		q += ` AND resolved_at IS NULL`
	}
	q += ` ORDER BY reported_at, id`
	rows, err := r.pool.Query(ctx, q, taskID)
	if err != nil {
		return nil, fmt.Errorf("tasks: list shortages: %w", err)
	}
	defer rows.Close()
	out := []Shortage{}
	for rows.Next() {
		s, err := scanShortage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ResolveShortage stamps the resolution on an OPEN shortage belonging to the named task. The UPDATE
// carries `resolved_at IS NULL` in its WHERE clause, so two people clearing the same shortage at once
// cannot both win, the loser gets ErrShortageResolved and the first resolver's name stands.
func (r *Repository) ResolveShortage(ctx context.Context, in ResolveShortageInput) (Shortage, Task, error) {
	if r.pool == nil {
		return Shortage{}, Task{}, errNoDB
	}
	if !looksLikeUUID(in.TaskID) || !looksLikeUUID(in.ShortageID) {
		return Shortage{}, Task{}, ErrShortageNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: begin resolve: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The resolve draws a fresh shortages seq (the desk's correction is the
	// write a stale device replay must meet) and bumps the parent task's
	// own seq beside it, the same two-draw shape InsertShortage pays,
	// because the task's projection carries the shortage counts.
	shortageSeq, err := allocateScopeSeqTx(ctx, tx, shortagesSeqScope)
	if err != nil {
		return Shortage{}, Task{}, err
	}
	sh, err := scanShortage(tx.QueryRow(ctx, `
		UPDATE task_shortages SET
			resolved_at = now(),
			resolved_by = NULLIF($3,'')::uuid,
			resolution  = $4,
			seq         = $5
		WHERE id = $1::uuid AND task_id = $2::uuid AND resolved_at IS NULL
		RETURNING `+shortageColumns,
		in.ShortageID, in.TaskID, in.ActorID, in.Resolution, shortageSeq))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either it is not this task's shortage / does not exist, or it is already closed. One
			// extra read tells them apart, so the caller gets an honest 404 vs 409.
			var closed bool
			probe := tx.QueryRow(ctx,
				`SELECT resolved_at IS NOT NULL FROM task_shortages WHERE id = $1::uuid AND task_id = $2::uuid`,
				in.ShortageID, in.TaskID)
			if perr := probe.Scan(&closed); perr == nil && closed {
				return Shortage{}, Task{}, ErrShortageResolved
			}
			return Shortage{}, Task{}, ErrShortageNotFound
		}
		return Shortage{}, Task{}, fmt.Errorf("tasks: resolve shortage: %w", err)
	}
	var taskType string
	if err := tx.QueryRow(ctx, `SELECT task_type FROM tasks WHERE id = $1 FOR UPDATE`, in.TaskID).Scan(&taskType); err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: lock %q after resolve: %w", in.TaskID, err)
	}
	taskSeq, err := allocateTaskSeqTx(ctx, tx, taskType)
	if err != nil {
		return Shortage{}, Task{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET seq = $2 WHERE id = $1`, in.TaskID, taskSeq); err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: bump task after resolve: %w", err)
	}

	t, err := scanYardTask(tx.QueryRow(ctx, `SELECT `+yardColumns+` FROM tasks WHERE id = $1`, in.TaskID))
	if err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: reread task after resolve: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Shortage{}, Task{}, fmt.Errorf("tasks: commit resolve: %w", err)
	}
	return sh, t, nil
}

// OrderPickStatus answers ONE question about one order: how far has its pick got?
//
// WHY THE LATEST TASK AND NOT AN AGGREGATE. A cancelled pick followed by a re-queued one is a normal
// day in a yard, and "has this order been picked" is a question about the pick that is live now, not
// about every pick that ever existed. CANCELLED tasks are excluded outright for the same reason: a
// pick somebody cancelled is not a state anybody is waiting on.
//
// It returns ("", nil) when the order has no live pick at all, an ABSENCE, not an error. The caller
// distinguishes "nobody has started" from "the read failed", and those two deserve different
// sentences.
func (r *Repository) OrderPickStatus(ctx context.Context, orderID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", nil
	}
	var status string
	err := r.pool.QueryRow(ctx, `
SELECT status FROM tasks
 WHERE order_id = $1 AND task_type = 'PICK' AND status <> 'CANCELLED'
 ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("tasks: order pick status: %w", err)
	}
	return status, nil
}

// OrderPickStatuses is OrderPickStatus for many orders in one round trip.
//
// WHY IT EXISTS. The load board's customer-pickups card has to split the open-pickup population by
// how far the yard has got with each one. Asking one order at a time would be N round trips to paint
// one number, and the alternative, dispatch reaching into this module's tasks table in SQL, is the
// cross-module table read the architecture forbids and the PickCompletionReader seam exists to
// prevent. So the seam gets a batched shape and the boundary stays where it is.
//
// SAME RULE AS THE SINGULAR, restated because it is the part that is easy to get wrong in a batch:
// the LATEST live task per order, CANCELLED excluded. DISTINCT ON does that in one pass; a GROUP BY
// max(created_at) would need a second join to get back to the status and would tie differently.
//
// An order with no live pick is ABSENT from the map, never present-with-an-empty-string: the caller
// distinguishes "nobody has started" from "read failed", and a zero value that means both is how
// those two get confused.
func (r *Repository) OrderPickStatuses(ctx context.Context, orderIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if r.pool == nil {
		return nil, errNoDB
	}
	ids := make([]string, 0, len(orderIDs))
	for _, id := range orderIDs {
		if looksLikeUUID(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
SELECT DISTINCT ON (order_id) order_id::text, status
  FROM tasks
 WHERE order_id = ANY($1::uuid[]) AND task_type = 'PICK' AND status <> 'CANCELLED'
 ORDER BY order_id, created_at DESC`, ids)
	if err != nil {
		return nil, fmt.Errorf("tasks: order pick statuses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, fmt.Errorf("tasks: order pick statuses: %w", err)
		}
		out[id] = status
	}
	return out, rows.Err()
}

// StalePickTasks lists PICK tasks still QUEUED since before cutoff, oldest first. This is the nightly
// sweep's feed and nothing else reads it; it is deliberately a plain read with no side effect, so the
// sweep can never mutate what it is only supposed to report on.
func (r *Repository) StalePickTasks(ctx context.Context, cutoff time.Time, limit int) ([]Task, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `SELECT `+yardColumns+` FROM tasks
		WHERE task_type = 'PICK' AND status = 'QUEUED' AND created_at < $1
		ORDER BY created_at LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("tasks: stale picks: %w", err)
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanYardTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// - 0187 · what was actually pulled, per line -

const pickedLineColumns = `id::text, task_id::text, order_line_no, COALESCE(product_id::text,''),
	sku, description, qty_ordered::text, qty_picked::text, uom, COALESCE(picked_by::text,''), picked_at,
	short_reason, short_note`

func scanPickedLine(row pgx.Row) (PickedLine, error) {
	var l PickedLine
	err := row.Scan(&l.ID, &l.TaskID, &l.OrderLineNo, &l.ProductID, &l.SKU, &l.Description,
		&l.QtyOrdered, &l.QtyPicked, &l.UOM, &l.PickedBy, &l.PickedAt, &l.ShortReason, &l.ShortNote)
	return l, err
}

// UpsertPickedLines records what the yard pulled, in ONE transaction with the task locked.
//
// UPSERT AND NOT INSERT, because this is a measurement and a picker corrects themselves. 0187's
// UNIQUE (task_id, order_line_no) makes "one live number per line" a property of the schema rather
// than of whoever wrote the last query; the correction HISTORY lives in task_events, which is where
// this module already keeps "what happened and who did it".
//
// THE TASK IS LOCKED FIRST, exactly as InsertShortage does and for the same two reasons: it proves
// the task exists (a 23503 cannot tell "no such task" from a real failure) and it serialises two
// pickers working the same ticket, which in a yard is a Tuesday rather than an edge case.
func (r *Repository) UpsertPickedLines(ctx context.Context, taskID, actorID string, lines []PickedLineInput) ([]PickedLine, error) {
	return r.RecordPickLocked(ctx, taskID, actorID, func(context.Context, Task) ([]PickedLineInput, error) { return lines, nil })
}

// ErrRecordPoolTooSmall is the refusal for a pool the record cannot work over. A record holds the task
// row's transaction while the order module takes its own connection, and a sync record also holds the
// key ledger's lock connection, so three connections are the least a pool can hold for one record.
var ErrRecordPoolTooSmall = errors.New("tasks: the pick record needs a pool of at least 3 connections (pool_max_conns)")

// RequireRecordPool is the boot check beside the key ledger's: it returns ErrRecordPoolTooSmall for a
// pool the pick record cannot work over.
func RequireRecordPool(pool *pgxpool.Pool) error {
	if conns := pool.Config().MaxConns; conns < 3 {
		return fmt.Errorf("%w, got %d", ErrRecordPoolTooSmall, conns)
	}
	return nil
}

// recordSlotsFor is how many records may hold the task row's transaction at once on a pool of n
// connections. The key ledger lets at most n/2 pushes hold a lock connection; every record in flight
// holds one more; the order module's transaction needs one beyond both. So that a pool full of ledger
// holders and open records still has a connection for an order call to finish on (a record waiting
// for a connection that only a finishing record could free would wait forever), records in flight
// are capped at n - n/2 - 1.
func recordSlotsFor(n int32) int { return int(n) - int(n)/2 - 1 }

// RecordPickLocked records a pick in ONE transaction that holds the task row's lock across the order
// call. begin, SELECT the task FOR UPDATE, refuse a CANCELLED or DONE one, run apply (the order call,
// which hands back the lines to store), upsert them and draw the task's fresh seq, commit. Every status
// move takes the same row FOR UPDATE, so a cancel or a complete that arrives during the order call waits
// and lands after the record, which keeps its lines; the order module never locks a tasks row, so the
// lock order (task, then order) cannot invert.
//
// UPSERT AND NOT INSERT, because this is a measurement and a picker corrects themselves. 0187's
// UNIQUE (task_id, order_line_no) makes "one live number per line" a property of the schema rather
// than of whoever wrote the last query; the correction HISTORY lives in task_events.
//
// The task row also draws a fresh seq here, as InsertShortage does: the row re-appears in a device's
// pull with the recorded lines, and a queued sync record based on the older seq meets server wins
// instead of applying blind over this one.
//
// The cost is a second pooled connection while the order call runs, which recordSlotsFor bounds.
func (r *Repository) RecordPickLocked(ctx context.Context, taskID, actorID string, apply func(context.Context, Task) ([]PickedLineInput, error)) ([]PickedLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(taskID) {
		return nil, ErrNotFound
	}
	if r.recordSlots == nil {
		return nil, ErrRecordPoolTooSmall
	}
	select {
	case r.recordSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-r.recordSlots }()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("tasks: begin pick lines: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	task, err := scanYardTask(tx.QueryRow(ctx, `SELECT `+yardColumns+` FROM tasks WHERE id = $1 FOR UPDATE`, taskID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("tasks: lock %q for pick lines: %w", taskID, err)
	}
	// A pick record is a yard-pick measurement: only a PICK task carries one. A NEED_WEIGHT /
	// DISPATCH / OTHER task has no ticket and no order line to update, but RecordPickLocked running
	// against it would still hand the order module a backorder (FROM-PRINCIPAL row 2122 item 5 /
	// REVIEW-YS-opus section 4: $8.02 off DEMO-PLM-O14). Refuse BEFORE pickClosed so a CANCELLED /
	// DONE non-PICK row cannot sneak through either leg; the closed-pick path's caller message would
	// be misleading on a row that was never a pick to begin with.
	if task.TaskType != TypePick {
		return nil, ErrNotPickTask
	}
	if err := pickClosed(task.Status); err != nil {
		return nil, err
	}
	// Save pick is the holder's act: compared on the locked row, before the order is told anything (UX-76).
	if notHolder(task.ClaimedBy, actorID) {
		return nil, ErrNotHolder
	}
	lines, err := apply(ctx, task)
	if err != nil {
		return nil, err
	}
	seq, err := allocateTaskSeqTx(ctx, tx, task.TaskType)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET seq = $2 WHERE id = $1`, taskID, seq); err != nil {
		return nil, fmt.Errorf("tasks: bump task after pick lines: %w", err)
	}

	for _, l := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_pick_lines
				(task_id, order_line_no, product_id, sku, description, qty_ordered, qty_picked, uom, picked_by,
				 short_reason, short_note)
			VALUES ($1::uuid, $2, NULLIF($3,'')::uuid, $4, $5, $6::numeric, $7::numeric, $8, NULLIF($9,'')::uuid, $10, $11)
			ON CONFLICT (task_id, order_line_no) DO UPDATE SET
				product_id  = EXCLUDED.product_id,
				sku         = EXCLUDED.sku,
				description = EXCLUDED.description,
				qty_ordered = EXCLUDED.qty_ordered,
				qty_picked  = EXCLUDED.qty_picked,
				uom         = EXCLUDED.uom,
				picked_by   = EXCLUDED.picked_by,
				short_reason = EXCLUDED.short_reason,
				short_note   = EXCLUDED.short_note,
				updated_at  = now()`,
			taskID, l.OrderLineNo, l.ProductID, l.SKU, l.Description,
			l.QtyOrdered, l.QtyPicked, l.UOM, actorID, l.ShortReason, l.ShortNote); err != nil {
			return nil, fmt.Errorf("tasks: record pick line %d: %w", l.OrderLineNo, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tasks: commit pick lines: %w", err)
	}
	return r.ListPickedLines(ctx, taskID)
}

// ListPickedLines reads a task's recorded pick, in ticket order.
func (r *Repository) ListPickedLines(ctx context.Context, taskID string) ([]PickedLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(taskID) {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+pickedLineColumns+` FROM task_pick_lines WHERE task_id = $1 ORDER BY order_line_no`, taskID)
	if err != nil {
		return nil, fmt.Errorf("tasks: list pick lines: %w", err)
	}
	defer rows.Close()
	out := []PickedLine{}
	for rows.Next() {
		l, err := scanPickedLine(rows)
		if err != nil {
			return nil, fmt.Errorf("tasks: scan pick line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// HolderLocked runs apply with the task row locked (FOR UPDATE) and the actor checked against the claim on
// that locked row: ErrNoActor with no actor, ErrNotHolder when someone else holds the pick. The skid count's
// guard (UX-76b): the order call inside apply may need a pooled connection, so it takes a record slot as
// RecordPickLocked does, and the lock is held across the call so a claim and a count cannot cross.
func (r *Repository) HolderLocked(ctx context.Context, taskID, actorID string, apply func(context.Context) error) error {
	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(taskID) {
		return ErrNotFound
	}
	if r.recordSlots == nil {
		return ErrRecordPoolTooSmall
	}
	select {
	case r.recordSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-r.recordSlots }()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tasks: begin holder lock: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var claimedBy string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(claimed_by::text, '') FROM tasks WHERE id = $1 FOR UPDATE`, taskID).Scan(&claimedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("tasks: lock %q for holder check: %w", taskID, err)
	}
	if actorID == "" {
		return ErrNoActor
	}
	if notHolder(claimedBy, actorID) {
		return ErrNotHolder
	}
	if err := apply(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tasks: commit holder lock: %w", err)
	}
	return nil
}
