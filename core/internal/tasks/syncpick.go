// syncpick.go is the pick record door of the pick_tasks sync scope (Field CP-2): the sync twin of
// PUT /api/v1/yard/picks/{id}/lines. A yard device that recorded what it pulled while offline
// replays it as one pick_tasks update, {id, picked: [line, ...]}, and the door gives the SAME effect
// and the SAME guards as the direct door by running the same service body: Service.RecordPick (the
// line rules, the order module's ApplyPicks through the yard order seam with its above ordered
// refusal, the absolute quantities, the picked lines upsert, the order rep's short pick notice)
// after the branch fence the direct door's taskBranchFence applies.
//
// What the door adds is only what the sync core owes a write: the task row draws a fresh pick seq (the
// row re-appears in the device's pull), carries the idempotency key, and the idem task_events note
// records it, so FindApplied answers a replay and RecordPick never runs twice for one key. The
// record, then the stamp, are two steps, not one transaction: a crash between them replays the
// record, which sets absolute quantities and converges (the direct door's own stated retry rule).
//
// The skid count is NOT here. It moves the order's skid deposit, which is money, and stays a direct
// door.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// maxPickedLines is the direct door's own bound on one record (RecordPick refuses more).
const maxPickedLines = 500

// PickRecorder is the direct door's service body, the narrow seam the record door runs: the tasks
// Service satisfies it.
type PickRecorder interface {
	RecordPick(ctx context.Context, taskID, actorID string, lines []PickedLineInput) ([]PickedLine, error)
}

// WithPickRecorder wires the record door's service body.
func (s *SyncStore) WithPickRecorder(r PickRecorder) *SyncStore {
	s.recorder = r
	return s
}

// WithStaffAuthority wires the branch fence's authority seam: the caller's staff context in the org,
// read again here because the sync core's ScopeFilter carries the subject and the org and no role.
func (s *SyncStore) WithStaffAuthority(r identity.SyncStaffResolver) *SyncStore {
	s.staff = r
	return s
}

// PickDoorWired reports whether both seams of the record door are wired, the leg the composition
// test and the boot fail fast pin. An unwired door refuses; it never records unfenced.
func (s *SyncStore) PickDoorWired() bool { return s.recorder != nil && s.staff != nil }

// pickedLineLegs are the legs one line may carry: the direct door's body keys, one for one. The
// client's local row id, task id and picked_at stamp are not on the wire.
var pickedLineLegs = map[string]bool{
	"order_line_no": true, "product_id": true, "sku": true, "description": true,
	"qty_ordered": true, "qty_picked": true, "uom": true, "short_reason": true, "short_note": true,
}

func refusedf(format string, a ...any) error {
	return fmt.Errorf("tasks: "+format+": %w", append(a, sync.ErrOpRefused)...)
}

// parsePickedLines reads the payload's picked array into the direct door's input. Shape only: the
// line rules (quantities, reasons, duplicates, bounds on a line's text) are RecordPick's own, so one
// body decides them for both doors.
func parsePickedLines(raw any) ([]PickedLineInput, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, refusedf("picked must be an array of lines")
	}
	if len(arr) == 0 {
		return nil, refusedf("say what was picked, an empty record is not the same as a pick of nothing")
	}
	if len(arr) > maxPickedLines {
		return nil, refusedf("that is more lines than any one ticket carries (max %d)", maxPickedLines)
	}
	out := make([]PickedLineInput, 0, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, refusedf("picked line %d must be an object", i+1)
		}
		for k := range m {
			if !pickedLineLegs[k] {
				return nil, refusedf("picked line %d: field %q is not writable through sync", i+1, k)
			}
		}
		var l PickedLineInput
		if raw, has := m["order_line_no"]; has {
			n, ok := raw.(float64)
			if !ok || n != math.Trunc(n) || n < math.MinInt32 || n > math.MaxInt32 {
				return nil, refusedf("picked line %d: order_line_no must be an integer", i+1)
			}
			l.OrderLineNo = int(n)
		}
		for k, dst := range map[string]*string{
			"product_id": &l.ProductID, "sku": &l.SKU, "description": &l.Description,
			"qty_ordered": &l.QtyOrdered, "qty_picked": &l.QtyPicked, "uom": &l.UOM,
			"short_reason": &l.ShortReason, "short_note": &l.ShortNote,
		} {
			raw, has := m[k]
			if !has {
				continue
			}
			v, ok := raw.(string)
			if !ok {
				return nil, refusedf("picked line %d: %s must be a string", i+1, k)
			}
			*dst = v
		}
		out = append(out, l)
	}
	return out, nil
}

// checkPickedOnly refuses a record op that carries anything beside the row address and the lines:
// a record is its own operation, and a payload that also moved the status would apply two effects
// under one idempotency key.
func checkPickedOnly(fields map[string]any) error {
	for k := range fields {
		if k != "id" && k != "picked" {
			return refusedf("field %q cannot travel with picked, a pick record is its own operation", k)
		}
	}
	return nil
}

// classifyRecordErr sorts the record body's errors: the direct door's client faults are the op's
// refusal with the module's words, an unknown task is the not found, anything else is an
// operational fault the engine fails the push on.
func classifyRecordErr(err error) error {
	var bad httpx.BadRequestError
	switch {
	case errors.Is(err, ErrNotFound):
		return sync.ErrRowNotFound
	case errors.Is(err, ErrAboveOrdered), errors.Is(err, ErrOrderRefused), errors.Is(err, ErrPickClosed), errors.Is(err, ErrNotPickTask), errors.As(err, &bad):
		return refused(err)
	}
	return err
}

// writePicked applies one record op. Order: the payload's shape, the seams, the row under the org and
// PICK predicate, the branch fence, the lines, the record, the stamp.
func (s *SyncStore) writePicked(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if err := checkPickedOnly(fields); err != nil {
		return sync.RowState{}, err
	}
	if !s.PickDoorWired() {
		return sync.RowState{}, refusedf("the pick record door is not configured")
	}
	cur, ok, err := s.load(ctx, s.pool, f, id)
	if err != nil {
		return sync.RowState{}, err
	}
	if !ok || cur.Deleted {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	sc, has, err := s.staff(ctx, f.Actor, f.OrgID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: pick record door authority: %w", err)
	}
	if !has || (sc.Status != "" && sc.Status != identity.StatusActive) {
		return sync.RowState{}, refusedf("no active staff authority in this org")
	}
	// The direct door's taskBranchFence answers 401 to a staff login with no linked ERP user: a pick
	// record moves an order's quantities and money and must name who did it.
	if sc.UserID == "" {
		return sync.RowState{}, refusedf("yard: unauthenticated")
	}
	if branch, _ := cur.Data["branch_id"].(string); branch != "" && !sc.CanAccessBranch(branch) {
		return sync.RowState{}, refusedf("yard: branch out of scope")
	}
	// A closed pick takes no record: refuse before the lines are parsed or the record body is called
	// (RecordPick and the store check it again, from the row as it stands).
	if status, _ := cur.Data["status"].(string); pickClosed(status) != nil {
		return sync.RowState{}, refused(pickClosed(status))
	}
	lines, err := parsePickedLines(fields["picked"])
	if err != nil {
		return sync.RowState{}, err
	}
	actor := sc.UserID
	if _, err := s.recorder.RecordPick(ctx, id, actor, lines); err != nil {
		return sync.RowState{}, classifyRecordErr(err)
	}
	return s.stampRecord(ctx, f, id, key, actor)
}

// stampRecord is the sync half of the write: a fresh pick seq, the idempotency key and the idem
// event, in one transaction, then the row read back with its lines.
func (s *SyncStore) stampRecord(ctx context.Context, f sync.ScopeFilter, id, key, actor string) (sync.RowState, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin sync record stamp: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Task row first, then the counter: the order UpsertPickedLines and InsertShortage take, so a direct
	// record and a replayed one on the same task queue behind each other instead of deadlocking.
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT true FROM tasks WHERE id = $1::uuid AND org_id = $2::uuid AND task_type = 'PICK' AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("tasks: lock task for sync record stamp %q: %w", id, err)
	}
	seq, err := allocatePickSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncRow(tx.QueryRow(ctx, `
		UPDATE tasks SET
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
		return sync.RowState{}, fmt.Errorf("tasks: sync record stamp %q: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, $2, NULLIF($3,'')::uuid, $4)`,
		id, r.task.Status, actor, idemNote(key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: sync record idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit sync record stamp: %w", err)
	}
	if err := s.attachPicked(ctx, s.pool, &r); err != nil {
		return sync.RowState{}, err
	}
	return rowStateOf(r)
}

// pickedQuerier is the multi row read surface a pool and a transaction share.
type pickedQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// attachPicked reads the recorded lines of the given rows (one query for the page) and sets them
// on the sync projection, so a device's pulled row carries what the replay landed. A row with no
// lines carries none.
func (s *SyncStore) attachPicked(ctx context.Context, db pickedQuerier, rows ...*syncRow) error {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if !r.deleted {
			ids = append(ids, r.task.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	res, err := db.Query(ctx,
		`SELECT `+pickedLineColumns+` FROM task_pick_lines WHERE task_id = ANY($1::uuid[]) ORDER BY task_id, order_line_no`, ids)
	if err != nil {
		return fmt.Errorf("tasks: sync picked lines: %w", err)
	}
	defer res.Close()
	byTask := make(map[string][]PickedLine, len(ids))
	for res.Next() {
		l, err := scanPickedLine(res)
		if err != nil {
			return fmt.Errorf("tasks: sync picked lines scan: %w", err)
		}
		byTask[l.TaskID] = append(byTask[l.TaskID], l)
	}
	if err := res.Err(); err != nil {
		return fmt.Errorf("tasks: sync picked lines: %w", err)
	}
	for _, r := range rows {
		r.picked = byTask[r.task.ID]
	}
	return nil
}
