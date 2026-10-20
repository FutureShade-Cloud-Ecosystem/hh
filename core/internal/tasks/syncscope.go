// syncscope.go is the pick_tasks sync adapter: the tasks module's
// implementation of the sync core's Store seam (core/internal/sync, work
// item #165's pilot scope). It lives here because the tasks module owns
// the tasks and task_events tables, and the fence rules hold on the sync
// surface exactly as everywhere else: the sync core drives protocol
// mechanics and touches no table, this adapter touches only tables this
// module owns, and the two meet at the Store interface.
//
// The projection is the contract's Task schema (core/api/openapi.yaml):
// the same tasks-row shape the module's own REST surface serves, so a
// device's cached row and the desk's screen read one shape. The scope
// covers PICK tasks (the yard-pick frontend's pick tickets); every query
// below carries the caller's org predicate (stamped by the middleware
// from X-Org under the staff bearer) plus the task_type predicate, and no
// query runs without them.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// refused wraps one of the module's invariant refusals in the Store
// seam's classified marker (sync.ErrOpRefused), so the engine reports
// the refusal as the operation's rejected result with the module's own
// words, never as an operational fault that fails the whole push.
func refused(err error) error {
	return fmt.Errorf("%w: %w", sync.ErrOpRefused, err)
}

// idemNotePrefix heads the task_events note that records one applied sync
// operation's idempotency key: the module's append-only transition audit
// is the pilot's full applied-key history, so an outbox holding several
// operations on one row replays without re-applying the earlier ones (the
// row's single idempotency_key column records only the latest, the D30
// shape; the events carry the set). An idem row is not a transition: its
// to_status carries the row's status when the operation applied (the
// column is NOT NULL), which is what makes the row honest to read back.
const idemNotePrefix = "sync idem "

// idemNote builds the note that records one key.
func idemNote(key string) string { return idemNotePrefix + key }

// SyncStore is the pick_tasks Store. It needs the order-validation seam
// (a task is born against an order, the module rule: validate through the
// order module's read seam, never a JOIN) and the staff identity linker
// (the audit rows' actor is the linked ERP user, exactly as the REST
// surface resolves it); both arrive by constructor, both optional in
// tests.
type SyncStore struct {
	pool   *pgxpool.Pool
	orders OrderReader
	link   identity.UserIDLinker
	// recorder and staff are the pick record door's seams (syncpick.go).
	recorder PickRecorder
	staff    identity.SyncStaffResolver
	// tickets is the REST door's own ticket feed (the Service's PickSource), so a pulled row carries
	// the ticket exactly as GET /yard/picks/{id} builds it; nil pulls rows without one.
	tickets PickSource
	// claimerRoles labels a row's claimer by role on the rows a device reads (claimer_role.go).
	claimerRoles ClaimerRoleReader
	log          *slog.Logger
}

// NewSyncStore wires the adapter; nil seams are legal only where the
// caller knows creates and actor attribution will not run (tests),
// production wires both.
func NewSyncStore(pool *pgxpool.Pool, orders OrderReader, link identity.UserIDLinker) *SyncStore {
	return &SyncStore{pool: pool, orders: orders, link: link}
}

// pickScopePredicate is the WHERE predicate every adapter query runs
// under: the org stamp (never absent) plus the PICK type. The filter
// arrives from the middleware-stamped scope; a tenant filter is refused
// earlier at the route (pick_tasks is an org scope), and the adapter
// fails closed on one anyway: it answers nothing rather than read past
// its scope.
func pickScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return "org_id = $1::uuid AND task_type = 'PICK'", []any{f.OrgID}, true
}

// syncColumns is the pull projection: the module's full Task columns plus
// the sync legs (seq, tombstone flag) the engine's page shape needs. It is
// literally yardColumns plus a suffix, the same provably-additive
// extension yardColumns itself makes over taskColumns.
const syncColumns = yardColumns + `, seq, (deleted_at IS NOT NULL) AS deleted`

// syncRow is one scanned row of the sync projection: the Task projection
// plus its sync legs.
type syncRow struct {
	task    Task
	seq     int64
	deleted bool
	// picked is what the yard recorded pulling on the task (task_pick_lines), attached for the
	// reads a device or a conflict envelope sees; empty until a pick is recorded.
	picked []PickedLine
}

// scanSyncRow reads the 30-target row. The 28 Task targets re-declare
// scanYardTask's (pgx cannot split one row across scanners; the module
// paid the same cost extending taskColumns to yardColumns, and the guard
// against drift is that both scanners read the same shared column
// constants).
func scanSyncRow(row rowScanner) (syncRow, error) {
	var t Task
	var lineNo *int
	var claimedAt, pushedAt, deadlineAt, doneAt, stagedAt, weightSyncedAt *time.Time
	var seq int64
	var deleted bool
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
		&seq, &deleted,
	); err != nil {
		return syncRow{}, err
	}
	t.OrderLineNo = lineNo
	t.ClaimedAt = claimedAt
	t.PushedAt = pushedAt
	t.AcceptDeadlineAt = deadlineAt
	t.DoneAt = doneAt
	t.StagedAt = stagedAt
	t.WeightSyncedAt = weightSyncedAt
	return syncRow{task: t, seq: seq, deleted: deleted}, nil
}

// taskData projects one Task into the wire shape the contract's Task
// schema pins, the same json tags the module's own surface serves (a
// marshal round trip, so omitempty and the DECIMAL-as-string legs land
// exactly as the REST surface writes them).
func taskData(t Task, picked []PickedLine) (map[string]any, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("tasks: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("tasks: sync projection decode: %w", err)
	}
	if len(picked) > 0 {
		rawPicked, err := json.Marshal(picked)
		if err != nil {
			return nil, fmt.Errorf("tasks: sync projection picked: %w", err)
		}
		var lines []any
		if err := json.Unmarshal(rawPicked, &lines); err != nil {
			return nil, fmt.Errorf("tasks: sync projection picked decode: %w", err)
		}
		data["picked"] = lines
	}
	return data, nil
}

// rowStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func rowStateOf(r syncRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.task.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := taskData(r.task, r.picked)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// Pull returns the page of PICK-task changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// predicate.
func (s *SyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := pickScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM tasks
		WHERE %s AND seq IS NOT NULL AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, syncColumns, where)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("tasks: sync pull: %w", err)
	}
	defer rows.Close()
	page := make([]syncRow, 0, limit)
	for rows.Next() {
		r, err := scanSyncRow(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("tasks: sync pull scan: %w", err)
		}
		page = append(page, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("tasks: sync pull: %w", err)
	}
	rows.Close()
	// The page's recorded pick lines ride in one query, after the page query's connection is back.
	ptrs := make([]*syncRow, len(page))
	for i := range page {
		ptrs[i] = &page[i]
	}
	if err := s.attachPicked(ctx, s.pool, ptrs...); err != nil {
		return nil, nil, err
	}
	reach := s.ticketReach(ctx, f)
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	datas := make([]map[string]any, 0, len(page))
	for _, r := range page {
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.task.ID, Seq: r.seq})
			continue
		}
		data, err := taskData(r.task, r.picked)
		if err != nil {
			return nil, nil, err
		}
		datas = append(datas, data)
		// A transient source fault fails the page: the engine answers 503 and the device retries this
		// cursor, where a ticket-less row would be cached until the task next moves (its seq is the
		// only thing that re-sends it).
		if err := s.attachTicketData(ctx, reach, r.task.BranchID, r.task.OrderID, data); err != nil {
			s.logger().Error("tasks: sync pull ticket read failed", "task", r.task.ID, "order", r.task.OrderID, "err", err)
			return nil, nil, fmt.Errorf("tasks: sync pull ticket: %w", err)
		}
		items = append(items, sync.Entity{ID: r.task.ID, Seq: r.seq, Data: data})
	}
	// The page's claimers are labelled by role in one read (UX-75); a fault leaves the label off.
	s.attachClaimerRoles(ctx, datas)
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// row's idempotency_key column (the D30 shape, the latest key) or the
// applied-key history the adapter records in task_events (every key this
// row ever applied, so multi-operation outboxes replay clean).
func (s *SyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := pickScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	// No table alias: the projection's shortage-count subqueries
	// reference tasks.id unqualified, which an alias would hide.
	q := fmt.Sprintf(`SELECT %s FROM tasks
		WHERE %s
		  AND (idempotency_key = $2::uuid
		       OR EXISTS (SELECT 1 FROM task_events e WHERE e.task_id = tasks.id AND e.note = $3))
		LIMIT 1`, syncColumns, where)
	args = append(args, key, idemNote(key))
	r, err := scanSyncRow(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("tasks: sync find applied: %w", err)
	}
	if err := s.attachPicked(ctx, s.pool, &r); err != nil {
		return sync.RowState{}, false, err
	}
	// No ticket here: only the row's seq leaves an applied replay (see DecorateServerState).
	rs, err := rowStateOf(r)
	return rs, true, err
}

// Load reads one row by id under the org predicate; a row outside the
// caller's org is the same as an absent one (existence never leaks).
func (s *SyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	// No ticket here: Load serves the update and delete paths and the replay, which use the seq and the
	// task fields. The ticket rides where the row leaves, the conflict envelope (DecorateServerState).
	return s.load(ctx, s.pool, f, id)
}

// DecorateServerState attaches the ticket to the row a conflict envelope shows the device, under the
// caller's branch reach and with a source fault leaving the envelope without it (an envelope is a
// reply to a write, not a cursor feed, so it degrades and logs). It is the engine's one call for it.
func (s *SyncStore) DecorateServerState(ctx context.Context, f sync.ScopeFilter, row *sync.RowState) {
	s.attachTicket(ctx, s.ticketReach(ctx, f), row)
	if !row.Deleted && row.Data != nil {
		s.attachClaimerRoles(ctx, []map[string]any{row.Data})
	}
}

// pickQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type pickQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *SyncStore) load(ctx context.Context, db pickQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := pickScopePredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE id = $2::uuid AND %s`, syncColumns, where)
	args = append(args, id)
	r, err := scanSyncRow(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("tasks: sync load: %w", err)
	}
	if err := s.attachPicked(ctx, db, &r); err != nil {
		return sync.RowState{}, false, err
	}
	rs, err := rowStateOf(r)
	return rs, true, err
}

// writableFields is the field set a pushed update may declare on a pick
// task: the status (through the module's state machine, enforced on a
// locked pre-image exactly as the REST transitions enforce it) and the
// yard-facing stamps the device writes. The id is the row address the
// engine consumed. Any other declared field refuses the operation whole:
// applying a payload silently half-way would be the dishonest outcome.
var writableFields = map[string]bool{
	"id":             true,
	"status":         true,
	"picked":         true,
	"photo_url":      true,
	"stage_location": true,
	"stage_ref":      true,
}

// checkWritable refuses a payload that declares anything outside the
// writable set.
func checkWritable(fields map[string]any) error {
	for k := range fields {
		if !writableFields[k] {
			return fmt.Errorf("tasks: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// checkWritableCreate validates a create payload's declared fields: the
// create admits the row-shaping legs (id, order_id, priority) and nothing
// else; a create cannot birth a task mid-flight, the birth row is QUEUED.
func checkWritableCreate(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "order_id", "priority":
		default:
			return fmt.Errorf("tasks: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// actorERP resolves the audit actor from the filter's caller identity:
// the linked ERP user when the linker resolves one, else the empty string
// (the module's system-actor convention, the same NULL the REST surface
// writes for a system act).
func (s *SyncStore) actorERP(ctx context.Context, f sync.ScopeFilter) string {
	if s.link == nil || f.Actor == "" {
		return ""
	}
	if uid, ok := s.link(ctx, f.Actor); ok {
		return uid
	}
	return ""
}

// Create inserts one PICK task from a pushed create: the order validates
// through the module's own read seam (never a JOIN), the row stamps the
// caller's org, a freshly allocated seq, and the idempotency key, and the
// birth and idem task_events rows land in the same transaction. The id is
// the payload's id when the client generated one, else the server assigns
// it; a client-chosen id that already exists (under another key, the
// dedup already ran) refuses the create whole.
func (s *SyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := pickScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkWritableCreate(fields); err != nil {
		return sync.RowState{}, err
	}
	orderID, _ := fields["order_id"].(string)
	if !looksLikeUUID(orderID) {
		return sync.RowState{}, fmt.Errorf("tasks: order_id must be a uuid: %w", sync.ErrOpRefused)
	}
	if s.orders == nil {
		return sync.RowState{}, fmt.Errorf("tasks: order reader not configured: %w", sync.ErrOpRefused)
	}
	ob, err := s.orders.OrderBrief(ctx, orderID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: %w: %v", sync.ErrOpRefused, err)
	}
	priority, _ := fields["priority"].(string)
	if priority == "" {
		priority = PriorityNormal
	}
	if !validPriority(priority) {
		return sync.RowState{}, fmt.Errorf("tasks: unknown priority %q: %w", priority, sync.ErrOpRefused)
	}
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("tasks: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	actor := s.actorERP(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocatePickSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncRow(tx.QueryRow(ctx, `
		INSERT INTO tasks (id, task_type, order_id, branch_id, priority, status, created_by, source, source_message_id, org_id, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), 'PICK', $2::uuid, NULLIF($3,'')::uuid, $4, 'QUEUED', NULLIF($5,'')::uuid, 'MANUAL', NULL, $6::uuid, $7, $8::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+syncColumns,
		id, ob.ID, ob.BranchID, priority, actor, f.OrgID, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		// The ON CONFLICT DO NOTHING arm answered no row: the
		// client-chosen id is taken. Which refusal answers depends on
		// WHERE it is taken: inside the caller's org the collision is
		// the caller's own fact (the row is visible to it), outside it
		// the answer is not-found so existence never leaks across the
		// scope boundary.
		if _, own, _ := s.load(ctx, tx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: sync create insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, 'QUEUED', NULLIF($2,'')::uuid, 'created (sync)')`,
		r.task.ID, actor); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: sync create event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, $2, NULLIF($3,'')::uuid, $4)`,
		r.task.ID, r.task.Status, actor, idemNote(key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit sync create: %w", err)
	}
	return rowStateOf(r)
}

// Write applies exactly the declared fields to one row under the module's
// own invariants: the status move runs the same legality matrix the REST
// transitions run, enforced on a FOR UPDATE-locked pre-image so the
// recorded from-to is the transition that landed, and the row draws a
// fresh seq beside the write (the pull-feed re-appearance every tasks
// write owes). The transition and idem task_events rows land in the same
// transaction.
func (s *SyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := pickScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkWritable(fields); err != nil {
		return sync.RowState{}, err
	}
	if _, record := fields["picked"]; record {
		return s.writePicked(ctx, f, id, key, fields)
	}
	status, hasStatus := fields["status"].(string)
	if hasStatus && !validStatus(status) {
		return sync.RowState{}, fmt.Errorf("tasks: unknown status %q: %w", status, sync.ErrOpRefused)
	}
	photoURL, _ := fields["photo_url"].(string)
	stageLocation, _ := fields["stage_location"].(string)
	stageRef, _ := fields["stage_ref"].(string)
	actor := s.actorERP(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, claimedBy string
	if err := tx.QueryRow(ctx,
		`SELECT status, COALESCE(claimed_by::text, '') FROM tasks WHERE id = $1::uuid AND org_id = $2::uuid AND task_type = 'PICK' AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&from, &claimedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("tasks: lock %q for sync write: %w", id, err)
	}
	// Stage is the holder's act (UX-76): the move to STAGED and the stage legs are compared with the claim
	// on the locked row. The claim itself, the start and the other moves keep their own rules.
	if (hasStatus && status == StatusStaged) || stageLocation != "" || stageRef != "" {
		if notHolder(claimedBy, actor) {
			return sync.RowState{}, refused(ErrNotHolder)
		}
	}
	to := from
	if hasStatus {
		if from == status {
			return sync.RowState{}, refused(ErrNoTransition)
		}
		if !canTransition(from, status) {
			return sync.RowState{}, refused(ErrIllegalTransition)
		}
		to = status
	}
	seq, err := allocatePickSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncRow(tx.QueryRow(ctx, `
		UPDATE tasks SET
			status          = $3,
			claimed_by      = CASE WHEN $3 = 'ACCEPTED' THEN NULLIF($4,'')::uuid ELSE claimed_by END,
			claimed_at      = CASE WHEN $3 = 'ACCEPTED' THEN now() ELSE claimed_at END,
			done_at         = CASE WHEN $3 = 'DONE' THEN now() ELSE done_at END,
			photo_url       = CASE WHEN $5 <> '' THEN $5 ELSE photo_url END,
			stage_location  = CASE WHEN $6 <> '' THEN $6 ELSE stage_location END,
			stage_ref       = CASE WHEN $7 <> '' THEN $7 ELSE stage_ref END,
			updated_at      = now(),
			seq             = $8,
			idempotency_key = $9::uuid
		WHERE id = $1::uuid AND org_id = $2::uuid
		RETURNING `+syncColumns,
		id, f.OrgID, to, actor, photoURL, stageLocation, stageRef, seq, key))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: sync write %q: %w", id, err)
	}
	if hasStatus {
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
			VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, 'sync')`,
			id, from, to, actor); err != nil {
			return sync.RowState{}, fmt.Errorf("tasks: sync transition event: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, $2, NULLIF($3,'')::uuid, $4)`,
		id, to, actor, idemNote(key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit sync write: %w", err)
	}
	return rowStateOf(r)
}

// Delete tombstones one row: deleted_at set, the row retained, a fresh
// seq, the idempotency key stamped, and the idem event recorded, all in
// one transaction.
func (s *SyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := pickScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	actor := s.actorERP(ctx, f)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocatePickSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncRow(tx.QueryRow(ctx, `
		UPDATE tasks SET
			deleted_at      = now(),
			updated_at      = now(),
			seq             = $3,
			idempotency_key = $4::uuid
		WHERE id = $1::uuid AND org_id = $2::uuid AND task_type = 'PICK' AND deleted_at IS NULL
		RETURNING `+syncColumns,
		id, f.OrgID, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: sync delete %q: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, $2, NULLIF($3,'')::uuid, $4)`,
		id, r.task.Status, actor, idemNote(key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit sync delete: %w", err)
	}
	rs, err := rowStateOf(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}

// WithPickSource wires the ticket feed the pull attaches. It is the same PickSource the REST door
// reads, so there is one builder of a ticket and the two doors cannot drift.
func (s *SyncStore) WithPickSource(p PickSource) *SyncStore {
	s.tickets = p
	return s
}

// ticketReach resolves the caller's staff authority once and returns the branch test the ticket
// attach applies to every row of a read: the same authority the record door and the REST doors
// fence on (CanAccessBranch; GLOBAL reaches all). An authority that cannot be resolved, a caller with
// no active staff standing, or an unwired resolver returns nil, and nil attaches nothing: the row
// still ships with its task fields, the ticket (customer, order number, lines) does not.
func (s *SyncStore) ticketReach(ctx context.Context, f sync.ScopeFilter) func(branch string) bool {
	if s.tickets == nil || s.staff == nil {
		return nil
	}
	sc, has, err := s.staff(ctx, f.Actor, f.OrgID)
	if err != nil || !has || (sc.Status != "" && sc.Status != identity.StatusActive) {
		return nil
	}
	return sc.CanAccessBranch
}

// attachTicket sets the ticket on a read row's projection (see attachTicketData).
func (s *SyncStore) attachTicket(ctx context.Context, reach func(string) bool, rs *sync.RowState) {
	if rs.Deleted || rs.Data == nil {
		return
	}
	orderID, _ := rs.Data["order_id"].(string)
	branch, _ := rs.Data["branch_id"].(string)
	// A one row read (a conflict envelope, a replay) degrades to the row without its ticket and logs,
	// as the REST decorate does; only the cursor feed has to fail so a device retries.
	if err := s.attachTicketData(ctx, reach, branch, orderID, rs.Data); err != nil {
		s.logger().Warn("tasks: sync row ticket read failed", "task", rs.ID, "order", orderID, "err", err)
	}
}

func (s *SyncStore) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// attachTicketData sets data["ticket"] to the order's pick ticket, the leg the device's pick screen
// composes from, and only when the caller's authority reaches the row's branch (reach, from
// ticketReach; nil attaches nothing). The pull is org scoped and the sync filter carries no branch, so
// this is the fence the REST enumeration doors have ("branch out of scope"). A source that does not
// know the order (ErrUnknownOrder) leaves the row without a ticket; any other fault is returned, and
// the Pull fails its page on it. The sync core's filter carries no role, so the cost
// gate fails closed here: the one money figure of a ticket (still owed) is held out for every caller,
// as blankTicketMoney does for a role the rule refuses; the hold mark and its words stay. It runs only
// on the pool reads (Pull, Load, FindApplied), never inside a write transaction, so it never asks the
// pool for a second connection while holding one.
func (s *SyncStore) attachTicketData(ctx context.Context, reach func(string) bool, branch, orderID string, data map[string]any) error {
	if s.tickets == nil || reach == nil || orderID == "" || data == nil || !reach(branch) {
		return nil
	}
	t, err := s.tickets.PickTicket(ctx, orderID)
	if errors.Is(err, ErrUnknownOrder) {
		return nil
	}
	if err != nil {
		return err
	}
	t.StillOwedCents = 0
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	data["ticket"] = v
	return nil
}
