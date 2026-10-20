package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

var errNoDB = errors.New("tasks: database not configured")

// Repository owns all tasks + task_events persistence (pgx). Every status move is transactional: the
// tasks status write and its task_events audit row commit together (the order.SetStatus / outbox
// precedent), so a task can never move without its history edge, and the legality check runs on a
// FOR UPDATE-locked pre-image, so the recorded from→to is exactly the transition that landed.
type Repository struct {
	pool        *pgxpool.Pool // nil in scaffold/tests
	stageOutbox StageOutbox   // W2: out-of-band staged-notice seam (yard_repository.go); nil = skipped
	// recordSlots bounds the pick records holding the task row's transaction across the order call
	// (RecordPickLocked); nil on a pool too small for one (ErrRecordPoolTooSmall).
	recordSlots chan struct{}
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	r := &Repository{pool: pool}
	if pool != nil && pool.Config().MaxConns >= 3 {
		r.recordSlots = make(chan struct{}, recordSlotsFor(pool.Config().MaxConns))
	}
	return r
}

// taskColumns is the 0073 base tasks SELECT/RETURNING list. Nullable uuid/text/numeric columns are
// COALESCE'd to "" and the nullable ints/timestamps are read into pointers.
//
// It is no longer scanned directly: yardColumns (yard_repository.go) extends it with the 0095 stage
// stamp + shortage counts, and every read here uses THAT, so Create/Get/List/Transition/Stage all
// return one identical Task shape. Keeping the base constant separate is what makes the extension
// provably additive, yardColumns is literally taskColumns plus a suffix.
const taskColumns = `id::text, task_type, order_id::text, order_line_no,
	COALESCE(branch_id::text, ''), priority, status,
	COALESCE(created_by::text, ''), COALESCE(assigned_to::text, ''), COALESCE(claimed_by::text, ''),
	claimed_at, pushed_at, accept_deadline_at, done_at,
	COALESCE(photo_url, ''), COALESCE(weight_entered::text, ''), COALESCE(weight_uom, ''),
	source, COALESCE(source_message_id::text, ''), created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

// CreateTask inserts one task (status QUEUED) and its birth task_events row (from_status NULL →
// to_status QUEUED) in a single tx, returning the persisted task. order_id is required; branch_id /
// created_by / source_message_id are nullable (empty string → SQL NULL). The row draws a pick_tasks
// seq and carries the caller's org stamp, so the write is sync-visible from the moment it lands
// (context 4.5: the core owns this write path, unlike the ERP-carried rows 0213 left seq-NULL).
func (r *Repository) CreateTask(ctx context.Context, in NewTaskInput) (Task, error) {
	if r.pool == nil {
		return Task{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: begin create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	seq, err := allocateTaskSeqTx(ctx, tx, in.TaskType)
	if err != nil {
		return Task{}, err
	}
	t, err := scanYardTask(tx.QueryRow(ctx, `
		INSERT INTO tasks (task_type, order_id, order_line_no, branch_id, priority, status, created_by, source, source_message_id, org_id, seq)
		VALUES ($1, $2::uuid, $3, NULLIF($4,'')::uuid, $5, 'QUEUED', NULLIF($6,'')::uuid, $7, NULLIF($8,'')::uuid, NULLIF($9,'')::uuid, $10)
		RETURNING `+yardColumns,
		in.TaskType, in.OrderID, in.OrderLineNo, in.BranchID, in.Priority, in.CreatedBy, in.Source, in.SourceMessageID, in.OrgID, seq))
	if err != nil {
		return Task{}, fmt.Errorf("tasks: insert task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, 'QUEUED', NULLIF($2,'')::uuid, 'created')`,
		t.ID, in.CreatedBy); err != nil {
		return Task{}, fmt.Errorf("tasks: insert create event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("tasks: commit create: %w", err)
	}
	return t, nil
}

// GetTask loads one task by id. A malformed (non-UUID) id short-circuits to ErrNotFound before the
// query (tasks.id is a uuid column, a non-UUID would raise SQLSTATE 22P02 → HTTP 500), mirroring the
// order module's boundary guard.
func (r *Repository) GetTask(ctx context.Context, id string) (Task, error) {
	if r.pool == nil {
		return Task{}, errNoDB
	}
	if !looksLikeUUID(id) {
		return Task{}, ErrNotFound
	}
	t, err := scanYardTask(r.pool.QueryRow(ctx, `SELECT `+yardColumns+` FROM tasks WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("tasks: get %q: %w", id, err)
	}
	return t, nil
}

// ListTasks returns one page of the picking queue, filtered by optional status + branch_id. This is
// the query projection that IS the queue (no separate queue table): open tasks surface by ordering
// QUEUED/PUSHED first, then priority, then oldest.
//
// Cursor pagination (HH-3.2 B5): the offset family is retired. The resume
// predicate is a row-value comparison over the queue's stable-ordering key
// (open-first flag inverted so every leg ascends, priority rank, created_at,
// id tie-breaker), guarded by an IS NULL leg so it evaluates only with a key
// present; the ORDER BY gains the id tie-breaker the key needs. No count
// query: the cursor envelope carries no total.
func (r *Repository) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	where := "1=1"
	var args []any
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if f.BranchID != "" {
		args = append(args, f.BranchID)
		where += fmt.Sprintf(" AND branch_id = $%d::uuid", len(args))
	}

	var cActive, cPriority, cCreated, cID any
	if f.AfterCursor {
		cActive, cPriority, cCreated, cID = f.CursorActive, f.CursorPriority, f.CursorCreatedAt, f.CursorID
	}
	args = append(args, cActive, cPriority, cCreated, cID, f.Limit)
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE %s
		   AND ($%d::int IS NULL OR
		        ((status NOT IN ('QUEUED','PUSHED'))::int,
		         CASE priority WHEN 'URGENT' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'NORMAL' THEN 2 ELSE 3 END,
		         created_at, id)
		        > ($%d::int, $%d::int, $%d::timestamptz, $%d::uuid))
		ORDER BY (status IN ('QUEUED','PUSHED')) DESC,
		         CASE priority WHEN 'URGENT' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'NORMAL' THEN 2 ELSE 3 END,
		         created_at, id
		LIMIT $%d`,
		yardColumns, where,
		len(args)-4, len(args)-4, len(args)-3, len(args)-2, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		t, err := scanYardTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Transition applies one status move atomically: it locks the target task row (FOR UPDATE), reads the
// true immediately-prior status, enforces the state machine on that locked pre-image, writes the new
// status (plus the transition's field effects, claimed_by/at on ACCEPTED, done_at/photo_url on DONE),
// and appends the task_events row, all in one tx. A missing/malformed id → ErrNotFound; a no-op
// (To == current) → ErrNoTransition; an illegal jump → ErrIllegalTransition. Locking the row makes the
// recorded from→to the exact transition that landed, never a stale pre-image (the order.UpdateStatus
// FOR UPDATE precedent).
func (r *Repository) Transition(ctx context.Context, in TransitionInput) (Task, error) {
	if r.pool == nil {
		return Task{}, errNoDB
	}
	if !looksLikeUUID(in.TaskID) {
		return Task{}, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: begin transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, taskType, claimedBy string
	if err := tx.QueryRow(ctx, `SELECT status, task_type, COALESCE(claimed_by::text, '') FROM tasks WHERE id = $1 FOR UPDATE`, in.TaskID).Scan(&from, &taskType, &claimedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("tasks: lock %q: %w", in.TaskID, err)
	}
	if from == in.To {
		return Task{}, ErrNoTransition
	}
	if !canTransition(from, in.To) {
		return Task{}, ErrIllegalTransition
	}
	// The holder rule (UX-76b), on the locked row: a write with no actor is refused so no pick is claimed and
	// left with no holder; start and done are the holder's; a cancel is the holder's, or an explicit act of
	// a manager above the yard hands, with its reason, audited on this transition's event.
	note := in.Note
	switch in.To {
	case StatusAccepted:
		if in.ActorID == "" {
			return Task{}, ErrNoActor
		}
	case StatusInProgress, StatusDone:
		if in.ActorID == "" {
			return Task{}, ErrNoActor
		}
		if notHolder(claimedBy, in.ActorID) {
			return Task{}, ErrNotHolder
		}
	case StatusCancelled:
		if in.ActorID == "" {
			return Task{}, ErrNoActor
		}
		if notHolder(claimedBy, in.ActorID) {
			if in.OverrideRole == "" {
				return Task{}, ErrCancelNotAllowed
			}
			if strings.TrimSpace(in.Note) == "" {
				return Task{}, httpx.BadRequestf("say why you are cancelling a pick someone else holds")
			}
			note = "cancelled over the holder by " + in.OverrideRole + ": " + strings.TrimSpace(in.Note)
		}
	}

	seq, err := allocateTaskSeqTx(ctx, tx, taskType)
	if err != nil {
		return Task{}, err
	}
	t, err := scanYardTask(tx.QueryRow(ctx, `
		UPDATE tasks SET
			status     = $2,
			claimed_by = CASE WHEN $2 = 'ACCEPTED' THEN NULLIF($3,'')::uuid ELSE claimed_by END,
			claimed_at = CASE WHEN $2 = 'ACCEPTED' THEN now() ELSE claimed_at END,
			done_at    = CASE WHEN $2 = 'DONE' THEN now() ELSE done_at END,
			photo_url  = CASE WHEN $2 = 'DONE' AND $4 <> '' THEN $4 ELSE photo_url END,
			updated_at = now(),
			seq        = $5
		WHERE id = $1
		RETURNING `+yardColumns,
		in.TaskID, in.To, in.ActorID, in.PhotoURL, seq))
	if err != nil {
		return Task{}, fmt.Errorf("tasks: update %q: %w", in.TaskID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, $5)`,
		in.TaskID, from, in.To, in.ActorID, note); err != nil {
		return Task{}, fmt.Errorf("tasks: insert transition event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("tasks: commit transition: %w", err)
	}
	return t, nil
}

// looksLikeUUID reports whether s is a canonical 36-char 8-4-4-4-12 hex UUID (the shape of a tasks.id),
// so a malformed id is rejected before it reaches a Postgres uuid column (which would otherwise raise
// SQLSTATE 22P02 → HTTP 500). Mirrors the order module's validator.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// pickTasksSeqScope names the scope_sequences counter row every PICK-tasks
// row write draws from: the manifest's pick_tasks scope, one of the two
// sync scopes whose projection is the tasks table (context 4.5; the 0213
// convention: one counter row per scope, the order_no_sequences precedent
// of 0087). weightsSeqScope is the other: the weights scope projects the
// NEED_WEIGHT rows of the same table.
const (
	pickTasksSeqScope = "pick_tasks"
	weightsSeqScope   = "weights"
)

// allocatePickSeqTx draws the next pick_tasks seq on the caller's business
// transaction, exactly the recipe the 0213 comment records and the
// customerquote module already follows: the scope_sequences upsert
// returning next_value, gaps allowed, never reused. Every write path that
// changes a tasks row draws one, so a row a device has pulled reappears in
// the pull feed the moment the desk moves it: that re-appearance is what
// the server_wins conflict story reads (a stale client write meets a row
// whose seq moved).
func allocatePickSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	return allocateScopeSeqTx(ctx, tx, pickTasksSeqScope)
}

// allocateTaskSeqTx draws the next seq for one tasks row from the counter
// its task type's sync scope owns. The two tasks-table feeds partition rows
// exactly (a row is PICK or NEED_WEIGHT, never both), so each row's seq
// only ever comes from its own scope's counter and each feed stays
// monotonic on the column it orders by. NEED_WEIGHT rows draw the weights
// counter (the weights scope, HH-3.5 #166); every other type keeps the
// pick_tasks counter unchanged, and because the pick_tasks feed filters
// task_type = 'PICK', a NEED_WEIGHT row's seq was never pick-feed-visible
// and the counter it draws from is not observable there.
func allocateTaskSeqTx(ctx context.Context, tx pgx.Tx, taskType string) (int64, error) {
	scope := pickTasksSeqScope
	if taskType == TypeNeedWeight {
		scope = weightsSeqScope
	}
	return allocateScopeSeqTx(ctx, tx, scope)
}

// allocateScopeSeqTx is the shared upsert behind the two named allocators:
// the scope_sequences draw returning next_value, gaps allowed, never
// reused, the 0213 convention one counter row per scope.
func allocateScopeSeqTx(ctx context.Context, tx pgx.Tx, scope string) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, scope).Scan(&next); err != nil {
		return 0, fmt.Errorf("tasks: seq allocate for scope %s: %w", scope, err)
	}
	return next, nil
}
