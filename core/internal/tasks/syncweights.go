// syncweights.go is the weights sync adapter (HH-3.5 #166): the tasks
// module's Store implementation for the weights scope, the yard-pick feed
// of NEED_WEIGHT tasks (0073/0108, DIB-324). It is the second Store over
// the tasks table beside the pick_tasks pilot: the two feeds partition the
// table exactly (task_type is one value per row), each row's seq only
// ever comes from its own scope's counter (allocateTaskSeqTx), and the
// fence holds unchanged, the module's own tables under an org predicate
// the middleware stamped.
//
// The projection is the contract's Task schema, the same shape the pick
// scope and the module's REST surface serve, so the weight legs
// (weight_entered, weight_uom, weight_synced_at, weight_sync_error) ride
// the row exactly as the counter reads them. The policy is server_wins
// (the confirmed table): a stale replay never overwrites the row the desk
// moved, the server row is what the client is told to show.
//
// A weight landing through sync is the yard's answer to a NEED_WEIGHT
// request typed offline. It runs the module's own one-tap rule (weight.go:
// one tap in the yard is three legal moves): the row walks to DONE
// through the SAME legality matrix, one task_events row per hop, the
// weight and the terminal status land in one statement. The order-line
// push the online SubmitWeight drives stays where the composition root
// wires it today: unwired, the recorded weight leaves the 0108-indexed
// gap (weight_synced_at NULL) for reconciliation, the module's documented
// degradation, never a silent drop.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// weightScopePredicate is the WHERE predicate every weights-scope query
// runs under: the caller's org stamp plus the NEED_WEIGHT type, the
// partition that keeps this feed and the pick feed disjoint on one table.
// A tenant filter or an empty org fails closed and answers nothing.
func weightScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return "org_id = $1::uuid AND task_type = 'NEED_WEIGHT'", []any{f.OrgID}, true
}

// WeightSyncStore is the weights Store. Its seams are the pick adapter's:
// the order-validation read seam (a weight task is born against an order
// line) and the staff identity linker for attribution; nil seams are legal
// only where the caller knows creates and attribution will not run.
type WeightSyncStore struct {
	pool   *pgxpool.Pool
	orders OrderReader
	link   identity.UserIDLinker
	// orgOrders is the org held order read (UX-81c), taken from the order seam when it implements it, which the
	// composition root's adapter does. staff and branchOrg are the caller's branch reach, the pick door's seams.
	orgOrders OrgOrderReader
	staff     identity.SyncStaffResolver
	branchOrg BranchOrgReader
}

// NewWeightSyncStore wires the adapter.
func NewWeightSyncStore(pool *pgxpool.Pool, orders OrderReader, link identity.UserIDLinker) *WeightSyncStore {
	s := &WeightSyncStore{pool: pool, orders: orders, link: link}
	s.orgOrders, _ = orders.(OrgOrderReader)
	return s
}

// WithStaffAuthority wires the branch fence's authority seam, the pick door's (SyncStore.WithStaffAuthority).
func (s *WeightSyncStore) WithStaffAuthority(r identity.SyncStaffResolver) *WeightSyncStore {
	s.staff = r
	return s
}

// WithBranchOrgReader wires the branch to org seam the caller's reach is bounded by (UX-81c).
func (s *WeightSyncStore) WithBranchOrgReader(r BranchOrgReader) *WeightSyncStore {
	s.branchOrg = r
	return s
}

// OrgScoped reports whether the create's order read is held to the caller's org and its reach is wired, the leg the
// composition root's boot check pins (UX-81c), as SyncStore.OrgScoped does for the pick door.
func (s *WeightSyncStore) OrgScoped() bool {
	return s.orgOrders != nil && s.staff != nil && s.branchOrg != nil
}

// orderBrief reads the order for a create under the caller's org when the seam can: a foreign order is the same
// ErrUnknownOrder as a missing one, and an empty org reads nothing.
func (s *WeightSyncStore) orderBrief(ctx context.Context, orgID, orderID string) (OrderBrief, error) {
	if s.orgOrders != nil {
		if orgID == "" {
			return OrderBrief{}, ErrUnknownOrder
		}
		return s.orgOrders.OrderBriefInOrg(ctx, orgID, orderID)
	}
	return s.orders.OrderBrief(ctx, orderID)
}

// heldToReach answers ErrRowNotFound for a task on a branch the caller does not reach, exactly the answer a missing
// task gets (UX-81c round 2): the weights scope held a task to the caller's org only, so a scoped caller of one branch
// wrote to, and deleted, another branch's weight task. An unwired authority fences nothing, as the create; an
// unreadable one is the plain retryable error.
func (s *WeightSyncStore) heldToReach(ctx context.Context, f sync.ScopeFilter, branch string) error {
	if s.staff == nil {
		return nil
	}
	reach, err := s.callerReaches(ctx, f, branch)
	if err != nil {
		return err
	}
	if !reach {
		return sync.ErrRowNotFound
	}
	return nil
}

// callerReaches is the pick door's rule over the same seams, never a second copy of it (SyncStore.callerReaches).
func (s *WeightSyncStore) callerReaches(ctx context.Context, f sync.ScopeFilter, branch string) (bool, error) {
	return (&SyncStore{staff: s.staff, branchOrg: s.branchOrg}).callerReaches(ctx, f, branch)
}

// Pull returns the page of NEED_WEIGHT task changes past since, live rows
// and tombstones in seq order, at most limit rows total, under the org
// predicate and the caller's branch reach.
func (s *WeightSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := weightScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	// The branch reach clause is the pick scope's pull rule (SyncStore.pullReach), part of the query so the page
	// fills with the rows the caller reaches and `seq > since` stays exact (UX-81c round 3). GLOBAL adds no clause,
	// and a task with no branch is no scoped caller's. An unwired authority fences nothing, as the create.
	reachSQL := ""
	var branches []string
	if s.staff != nil {
		var reach bool
		var err error
		branches, reach, err = (&SyncStore{staff: s.staff}).pullReach(ctx, f)
		if err != nil {
			return nil, nil, err
		}
		if !reach {
			return []sync.Entity{}, []sync.Tombstone{}, nil
		}
		if branches != nil {
			reachSQL = " AND branch_id = ANY($4::uuid[])"
		}
	}
	q := fmt.Sprintf(`SELECT %s FROM tasks
		WHERE %s AND seq IS NOT NULL AND seq > $2%s
		ORDER BY seq ASC
		LIMIT $3`, syncColumns, where, reachSQL)
	args = append(args, since, limit)
	if branches != nil {
		args = append(args, branches)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("tasks: weight sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncRow(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("tasks: weight sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.task.ID, Seq: r.seq})
			continue
		}
		data, err := taskData(r.task, nil)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.task.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("tasks: weight sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the NEED_WEIGHT row it
// applied: the row's idempotency_key column or the applied-key history
// the adapter records in task_events (the pilot's mechanism, the events
// table IS this row's own log). The branch reach is part of the query, as
// the weights pull's is: the column carries no unique index (0213's rule),
// so a key can name rows on two branches and an unordered LIMIT 1 must not
// hand back the out of reach one (UX-103).
func (s *WeightSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := weightScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	// The reach clause is the pick scope's pull rule (SyncStore.pullReach), in the query so LIMIT 1 picks
	// only among the rows the caller reaches: a task with no branch is no scoped caller's, a GLOBAL caller
	// adds no clause (the org predicate bounds it), an unwired authority fences nothing, and an absent or
	// inactive one answers not applied.
	reachSQL := ""
	var branches []string
	if s.staff != nil {
		var reach bool
		var err error
		branches, reach, err = (&SyncStore{staff: s.staff}).pullReach(ctx, f)
		if err != nil {
			return sync.RowState{}, false, err
		}
		if !reach {
			return sync.RowState{}, false, nil
		}
		if branches != nil {
			reachSQL = " AND branch_id = ANY($4::uuid[])"
		}
	}
	q := fmt.Sprintf(`SELECT %s FROM tasks
		WHERE %s
		  AND (idempotency_key = $2::uuid
		       OR EXISTS (SELECT 1 FROM task_events e WHERE e.task_id = tasks.id AND e.note = $3))%s
		LIMIT 1`, syncColumns, where, reachSQL)
	args = append(args, key, idemNote(key))
	if branches != nil {
		args = append(args, branches)
	}
	r, err := scanSyncRow(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("tasks: weight sync find applied: %w", err)
	}
	rs, err := rowStateOf(r)
	return rs, true, err
}

// Load reads one NEED_WEIGHT row by id under the org predicate; a row
// outside the caller's org, or on a branch outside the caller's reach, is the
// same as an absent one.
func (s *WeightSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.pool, f, id)
}

// weightQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type weightQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *WeightSyncStore) load(ctx context.Context, db weightQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := weightScopePredicate(f)
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
		return sync.RowState{}, false, fmt.Errorf("tasks: weight sync load: %w", err)
	}
	if err := s.heldToReach(ctx, f, r.task.BranchID); err != nil {
		if errors.Is(err, sync.ErrRowNotFound) {
			return sync.RowState{}, false, nil
		}
		return sync.RowState{}, false, err
	}
	rs, err := rowStateOf(r)
	return rs, true, err
}

// weightWritableCreate validates a create payload's declared fields: the
// birth row names an order line and may carry the unit the yard answers
// in; a create cannot birth a task mid-flight, the birth row is QUEUED.
func weightWritableCreate(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "order_id", "order_line_no", "weight_uom":
		default:
			return fmt.Errorf("tasks: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// weightWritable is the field set a pushed update may declare: the weight
// legs (which close the task through the module's own one-tap rule), a
// status move through the legality matrix, and the photo stamp.
var weightWritable = map[string]bool{
	"id": true, "status": true, "weight_entered": true, "weight_uom": true, "photo_url": true,
}

// WeightSyncStore's actor resolution is the pick adapter's.
func (s *WeightSyncStore) actorERP(ctx context.Context, f sync.ScopeFilter) string {
	if s.link == nil || f.Actor == "" {
		return ""
	}
	if uid, ok := s.link(ctx, f.Actor); ok {
		return uid
	}
	return ""
}

// Create inserts one NEED_WEIGHT task from a pushed create: the order
// validates through the module's own read seam (never a JOIN), the row
// stamps the caller's org, a freshly allocated weights seq and the
// idempotency key, and the birth and idem task_events rows land in the
// same transaction. Priority defaults HIGH, exactly as RequestWeight
// rules: somebody is waiting on this number.
func (s *WeightSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := weightScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := weightWritableCreate(fields); err != nil {
		return sync.RowState{}, err
	}
	orderID, _ := fields["order_id"].(string)
	if !looksLikeUUID(orderID) {
		return sync.RowState{}, fmt.Errorf("tasks: order_id must be a uuid: %w", sync.ErrOpRefused)
	}
	lineNo, ok := numericLeg(fields["order_line_no"])
	if !ok || lineNo <= 0 {
		return sync.RowState{}, fmt.Errorf("tasks: order_line_no must be positive, a weight request names ONE line: %w", sync.ErrOpRefused)
	}
	if s.orders == nil {
		return sync.RowState{}, fmt.Errorf("tasks: order reader not configured: %w", sync.ErrOpRefused)
	}
	// The order is read under the caller's org (UX-81c): another org's order is an unknown order here.
	ob, err := s.orderBrief(ctx, f.OrgID, orderID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: %w: %v", sync.ErrOpRefused, err)
	}
	// A weight request is created only on a branch the caller reaches, by the pick door's rule. An order out of
	// reach is refused with exactly the words an unknown order gets. An unreadable authority is no verdict on the
	// order: the plain error fails the push, which the device retries.
	if s.staff != nil {
		reach, err := s.callerReaches(ctx, f, ob.BranchID)
		if err != nil {
			return sync.RowState{}, err
		}
		if !reach {
			return sync.RowState{}, fmt.Errorf("tasks: %w: %v", sync.ErrOpRefused, ErrUnknownOrder)
		}
	}
	// Priority is HIGH, always, exactly as RequestWeight rules: somebody
	// is waiting on this number. The birth row is not negotiable through
	// sync, so the field is not in the writable create set either.
	priority := PriorityHigh
	uom, _ := fields["weight_uom"].(string)
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("tasks: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	actor := s.actorERP(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin weight sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateScopeSeqTx(ctx, tx, weightsSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncRow(tx.QueryRow(ctx, `
		INSERT INTO tasks (id, task_type, order_id, order_line_no, branch_id, priority, status,
		                   weight_uom, created_by, source, source_message_id, org_id, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), 'NEED_WEIGHT', $2::uuid, $3,
		        NULLIF($4,'')::uuid, $5, 'QUEUED', NULLIF($6,'')::text, NULLIF($7,'')::uuid,
		        'MANUAL', NULL, $8::uuid, $9, $10::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+syncColumns,
		id, ob.ID, lineNo, ob.BranchID, priority, uom, actor, f.OrgID, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		// The client-chosen id is taken; WHERE it is taken decides the
		// refusal, existence never leaking across the scope boundary.
		if _, own, _ := s.load(ctx, tx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: weight sync create insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, 'QUEUED', NULLIF($2,'')::uuid, 'created (sync)')`,
		r.task.ID, actor); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: weight sync create event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, $2, NULLIF($3,'')::uuid, $4)`,
		r.task.ID, r.task.Status, actor, idemNote(key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: weight sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit weight sync create: %w", err)
	}
	return rowStateOf(r)
}

// numericLeg reads one payload leg as an integer, the loose JSON number
// shape included (encoding/json decodes numbers as float64).
func numericLeg(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// Write applies one pushed update under the module's own rules. Declaring
// the weight closes the task through the one-tap walk (RecordWeight's own
// semantics: pathToDone, one event per hop, weight and terminal status in
// one statement); declaring a status runs the legality matrix on a locked
// pre-image exactly as the pick adapter does. Declaring both a weight and
// a non-DONE status refuses whole: a weight write closes the task, and a
// payload applying half-way would be the dishonest outcome.
func (s *WeightSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := weightScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	for k := range fields {
		if !weightWritable[k] {
			return sync.RowState{}, fmt.Errorf("tasks: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	status, hasStatus := fields["status"].(string)
	if hasStatus && !validStatus(status) {
		return sync.RowState{}, fmt.Errorf("tasks: unknown status %q: %w", status, sync.ErrOpRefused)
	}
	weight, hasWeight := fields["weight_entered"].(string)
	uom, _ := fields["weight_uom"].(string)
	if hasWeight {
		if hasStatus && status != StatusDone {
			return sync.RowState{}, fmt.Errorf("tasks: a weight write closes the task, declare the weight or a status move, not both: %w", sync.ErrOpRefused)
		}
		normalised, err := parsePositiveQty(weight)
		if err != nil {
			return sync.RowState{}, fmt.Errorf("tasks: weight_entered: %v: %w", err, sync.ErrOpRefused)
		}
		if !weightIsPlausible(normalised) {
			return sync.RowState{}, fmt.Errorf("tasks: weight_entered is not a plausible scale reading: %w", sync.ErrOpRefused)
		}
		weight = normalised
	}
	photoURL, _ := fields["photo_url"].(string)
	actor := s.actorERP(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin weight sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, fromUOM, branch string
	if err := tx.QueryRow(ctx,
		`SELECT status, COALESCE(weight_uom, ''), COALESCE(branch_id::text, '') FROM tasks
		 WHERE id = $1::uuid AND org_id = $2::uuid AND task_type = 'NEED_WEIGHT' AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&from, &fromUOM, &branch); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("tasks: lock %q for weight sync write: %w", id, err)
	}
	if err := s.heldToReach(ctx, f, branch); err != nil {
		return sync.RowState{}, err
	}

	var r syncRow
	if hasWeight {
		// The one-tap walk: DONE through the fewest legal hops, one event
		// per hop (clock_timestamp, the order-readability rule), the
		// weight and the terminal status in one statement.
		uom = firstNonEmpty(uom, fromUOM)
		if uom == "" {
			return sync.RowState{}, fmt.Errorf("tasks: say what unit the weight is in: %w", sync.ErrOpRefused)
		}
		if from == StatusDone {
			return sync.RowState{}, refused(ErrNoTransition)
		}
		chain := pathToDone(from)
		if len(chain) == 0 {
			return sync.RowState{}, refused(ErrIllegalTransition)
		}
		hop := from
		for _, to := range chain {
			if _, err := tx.Exec(ctx, `
				INSERT INTO task_events (task_id, from_status, to_status, actor_id, note, created_at)
				VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, 'sync', clock_timestamp())`,
				id, hop, to, actor); err != nil {
				return sync.RowState{}, fmt.Errorf("tasks: weight sync event %s to %s: %w", hop, to, err)
			}
			hop = to
		}
		seq, err := allocateScopeSeqTx(ctx, tx, weightsSeqScope)
		if err != nil {
			return sync.RowState{}, err
		}
		r, err = scanSyncRow(tx.QueryRow(ctx, `
			UPDATE tasks SET
				status         = 'DONE',
				weight_entered = $3::numeric,
				weight_uom     = $4,
				done_at        = now(),
				claimed_by     = COALESCE(claimed_by, NULLIF($5,'')::uuid),
				claimed_at     = COALESCE(claimed_at, now()),
				photo_url      = CASE WHEN $6 <> '' THEN $6 ELSE photo_url END,
				updated_at     = now(),
				seq            = $7,
				idempotency_key = $8::uuid
			WHERE id = $1::uuid AND org_id = $2::uuid
			RETURNING `+syncColumns,
			id, f.OrgID, weight, uom, actor, photoURL, seq, key))
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		if err != nil {
			return sync.RowState{}, fmt.Errorf("tasks: weight sync write %q: %w", id, err)
		}
	} else {
		// A status move (or a stamp-only update): the pilot's own path,
		// the legality matrix on the locked pre-image.
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
		seq, err := allocateScopeSeqTx(ctx, tx, weightsSeqScope)
		if err != nil {
			return sync.RowState{}, err
		}
		r, err = scanSyncRow(tx.QueryRow(ctx, `
			UPDATE tasks SET
				status          = $3,
				claimed_by      = CASE WHEN $3 = 'ACCEPTED' THEN NULLIF($4,'')::uuid ELSE claimed_by END,
				claimed_at      = CASE WHEN $3 = 'ACCEPTED' THEN now() ELSE claimed_at END,
				done_at         = CASE WHEN $3 = 'DONE' THEN now() ELSE done_at END,
				photo_url       = CASE WHEN $5 <> '' THEN $5 ELSE photo_url END,
				weight_uom      = CASE WHEN $6 <> '' THEN $6 ELSE weight_uom END,
				updated_at      = now(),
				seq             = $7,
				idempotency_key = $8::uuid
			WHERE id = $1::uuid AND org_id = $2::uuid
			RETURNING `+syncColumns,
			id, f.OrgID, to, actor, photoURL, uom, seq, key))
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		if err != nil {
			return sync.RowState{}, fmt.Errorf("tasks: weight sync write %q: %w", id, err)
		}
		if hasStatus {
			if _, err := tx.Exec(ctx, `
				INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
				VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, 'sync')`,
				id, from, to, actor); err != nil {
				return sync.RowState{}, fmt.Errorf("tasks: weight sync transition event: %w", err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, $2, NULLIF($3,'')::uuid, $4)`,
		id, r.task.Status, actor, idemNote(key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: weight sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit weight sync write: %w", err)
	}
	return rowStateOf(r)
}

// Delete tombstones one NEED_WEIGHT row: deleted_at set, the row retained,
// a fresh weights seq, the idempotency key stamped, the idem event
// recorded, one transaction.
func (s *WeightSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := weightScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	actor := s.actorERP(ctx, f)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin weight sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var branch string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(branch_id::text, '') FROM tasks
		 WHERE id = $1::uuid AND org_id = $2::uuid AND task_type = 'NEED_WEIGHT' AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&branch); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("tasks: lock %q for weight sync delete: %w", id, err)
	}
	if err := s.heldToReach(ctx, f, branch); err != nil {
		return sync.RowState{}, err
	}
	seq, err := allocateScopeSeqTx(ctx, tx, weightsSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncRow(tx.QueryRow(ctx, `
		UPDATE tasks SET
			deleted_at      = now(),
			updated_at      = now(),
			seq             = $3,
			idempotency_key = $4::uuid
		WHERE id = $1::uuid AND org_id = $2::uuid AND task_type = 'NEED_WEIGHT' AND deleted_at IS NULL
		RETURNING `+syncColumns,
		id, f.OrgID, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: weight sync delete %q: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, $2, NULLIF($3,'')::uuid, $4)`,
		id, r.task.Status, actor, idemNote(key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: weight sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit weight sync delete: %w", err)
	}
	rs, err := rowStateOf(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}

// firstNonEmpty is the two-argument helper the uom resolution reads.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
