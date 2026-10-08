// syncscope_runs.go is the runs Store (server_wins, Colton's ruling:
// written at end-of-run close, matching the ERP rule that dispatch
// closes a run carrying a cancelled stop). The rows are delivery_runs,
// the projection is the contract's DispatchRun schema, and every query
// runs under the caller's org predicate stamped by the middleware from
// X-Org (an org scope; a tenant filter fails closed, answering nothing
// rather than reading past its fence).
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// RunsSyncStore serves the runs scope over delivery_runs.
type RunsSyncStore struct {
	pool *pgxpool.Pool
	link syncLinker
	role SyncRoleResolver
}

// NewRunsSyncStore wires the adapter; a nil linker is legal only where
// actor attribution will not run (tests), production wires the staff
// linker the composition root already holds.
func NewRunsSyncStore(pool *pgxpool.Pool, link syncLinker) *RunsSyncStore {
	return &RunsSyncStore{pool: pool, link: link}
}

// WithDriverRole wires the driver fence's role seam (identity's
// resolver, bridged to the role code). Production wires it; the
// composition test in cmd/core pins that it did.
func (s *RunsSyncStore) WithDriverRole(role SyncRoleResolver) *RunsSyncStore {
	s.role = role
	return s
}

// DriverRoleWired answers whether the driver fence's role seam is
// wired, the leg the composition test pins so an unwired boot cannot
// ship the fence's desk half silently.
func (s *RunsSyncStore) DriverRoleWired() bool { return s.role != nil }

// runsSyncQuery is the board's own run columns plus the two sync legs
// (the per-scope seq, the tombstone flag) over the board's own joins,
// so the device's cached run and the board's run are one shape read
// one way.
const runsSyncQuery = runSelectColumns + `,
       r.seq, (r.deleted_at IS NOT NULL) AS deleted` + runSelectFrom

// syncRunRow is one scanned row of the runs sync projection.
type syncRunRow struct {
	run     Run
	seq     int64
	deleted bool
}

// scanSyncRun reads the runSelect targets in their declared order then
// the two sync legs (pgx cannot split one row across scanners; the
// shared column constants are the guard against drift, the same
// argument yardColumns makes over taskColumns).
func scanSyncRun(row pgx.Row) (syncRunRow, error) {
	var r Run
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.RunNo, &r.BranchID, &r.ScheduledDate, &r.TruckID, &r.DriverID,
		&r.TruckCode, &r.DriverName, &r.Status, &r.Notes, &r.DispatchedAt, &r.CompletedAt,
		&r.StopCount, &r.CreatedAt, &r.CommitState, &r.ReopenedAt, &r.ReopenReason,
		&seq, &deleted); err != nil {
		return syncRunRow{}, err
	}
	return syncRunRow{run: r, seq: seq, deleted: deleted}, nil
}

// runsSyncRowByID reads the full sync projection for one run id on the
// given executor (a transaction or the pool), the read-back arm every
// write path uses: RETURNING cannot produce the joined display names
// runSelect carries, so the write lands first and the row is read back
// inside the same transaction, the visibility a commit gives.
func runsSyncRowByID(ctx context.Context, q runner, id string) (syncRunRow, error) {
	return scanSyncRun(q.QueryRow(ctx, runsSyncQuery+` WHERE r.id = $1::uuid`, id))
}

// runStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func runStateOf(r syncRunRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.run.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := projection(r.run)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// runScopePredicate is the WHERE predicate every runs adapter query
// runs under. The org stamp is never absent (the middleware resolves
// it); a tenant filter is refused earlier at the route (runs is an org
// scope) and the adapter fails closed on one anyway. A driver caller
// carries the driver arm beside the org: their runs alone, or nothing
// at all when their login links to no driver record (the empty feed is
// MyRuns' "you are not set up as a driver" said to a sync pull).
func runScopePredicate(f sync.ScopeFilter, d driverFence) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	switch {
	case !d.driver:
		return "r.org_id = $1::uuid", []any{f.OrgID}, true
	case d.driverID == "":
		return "FALSE", nil, true
	default:
		return "r.org_id = $1::uuid AND r.driver_id = $2::uuid", []any{f.OrgID, d.driverID}, true
	}
}

// Pull returns the page of run changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// predicate and the driver arm.
func (s *RunsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	d, err := resolveDriverFence(ctx, s.pool, s.link, s.role, f)
	if err != nil {
		return nil, nil, err
	}
	where, args, ok := runScopePredicate(f, d)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := runsSyncQuery + `
		WHERE ` + where + ` AND r.seq IS NOT NULL AND r.seq > ` + syncPH(len(args)+1) + `
		ORDER BY r.seq ASC
		LIMIT ` + syncPH(len(args)+2)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("dispatch: runs sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncRun(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("dispatch: runs sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.run.ID, Seq: r.seq})
			continue
		}
		data, err := projection(r.run)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.run.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("dispatch: runs sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied, under
// the org predicate. The column holds the latest LIVE key (a tombstone
// keeps it); the delete leg dedups through the engine's
// already-tombstoned arm, the design note in syncscope.go's header.
func (s *RunsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	d, err := resolveDriverFence(ctx, s.pool, s.link, s.role, f)
	if err != nil {
		return sync.RowState{}, false, err
	}
	where, args, ok := runScopePredicate(f, d)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := runsSyncQuery + `
		WHERE ` + where + ` AND r.idempotency_key = ` + syncPH(len(args)+1) + `::uuid
		LIMIT 1`
	args = append(args, key)
	r, err := scanSyncRun(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: runs sync find applied: %w", err)
	}
	rs, err := runStateOf(r)
	return rs, true, err
}

// Load reads one run by id under the org predicate; a row outside the
// caller's org is the same as an absent one (existence never leaks).
func (s *RunsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.pool, f, id)
}

// runsQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type runsQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *RunsSyncStore) load(ctx context.Context, db runsQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	d, err := resolveDriverFence(ctx, s.pool, s.link, s.role, f)
	if err != nil {
		return sync.RowState{}, false, err
	}
	where, args, ok := runScopePredicate(f, d)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := runsSyncQuery + `
		WHERE r.id = ` + syncPH(len(args)+1) + `::uuid AND ` + where
	args = append(args, id)
	r, err := scanSyncRun(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: runs sync load: %w", err)
	}
	rs, err := runStateOf(r)
	return rs, true, err
}

// runCreateFields is the field set a pushed create may declare on a
// run: the row's own shape (id, branch, date, crew, notes) and nothing
// else. A create cannot birth a run mid-flight: the birth row is
// PLANNED and its number is minted by next_run_no, the only mint.
func runCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "branch_id", "scheduled_date", "truck_id", "driver_id", "notes":
		default:
			return fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// runWriteFields is the field set a pushed update may declare on a
// run: the status (through the module's matrix and its completion
// precondition) and the notes the end-of-run summary writes. The id is
// the row address the engine consumed. Any other declared field
// refuses the operation whole.
func runWriteFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "status", "notes":
		default:
			return fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// branchInOrg answers whether a branch location sits in the caller's
// org, the create fence for every row birthed through sync (the filter
// is the org authority, the branch row ties it to a yard). The
// locations read is the one this module has always made (BranchName,
// the depot read); it is reference data, not a module's book.
func branchInOrg(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, branchID, orgID string) bool {
	var ok bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1::uuid AND org_id = $2::uuid)`,
		branchID, orgID).Scan(&ok); err != nil {
		return false
	}
	return ok
}

// Create inserts one run from a pushed create: the branch must sit in
// the caller's org, the crew (when declared) at that branch, the row
// stamps the caller's org, a freshly allocated seq, the idempotency
// key, and a run number minted by next_run_no. The id is the payload's
// id when the client generated one; a taken id refuses the create
// whole (inside the caller's org as the caller's own fact, outside it
// as not-found so existence never leaks).
func (s *RunsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	d, err := resolveDriverFence(ctx, s.pool, s.link, s.role, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := runScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := runCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	branchID, _ := fields["branch_id"].(string)
	if !looksLikeUUID(branchID) {
		return sync.RowState{}, fmt.Errorf("dispatch: branch_id must be a uuid: %w", sync.ErrOpRefused)
	}
	if !branchInOrg(ctx, s.pool, branchID, f.OrgID) {
		return sync.RowState{}, fmt.Errorf("dispatch: the branch is not in this organisation: %w", sync.ErrOpRefused)
	}
	date := strings.TrimSpace(fmt.Sprint(fields["scheduled_date"]))
	if date == "" || !validDate(date) {
		return sync.RowState{}, fmt.Errorf("dispatch: scheduled_date must be YYYY-MM-DD: %w", sync.ErrOpRefused)
	}
	truckID, _ := fields["truck_id"].(string)
	driverID, _ := fields["driver_id"].(string)
	if truckID != "" && !looksLikeUUID(truckID) {
		return sync.RowState{}, fmt.Errorf("dispatch: truck_id must be a uuid: %w", sync.ErrOpRefused)
	}
	if driverID != "" && !looksLikeUUID(driverID) {
		return sync.RowState{}, fmt.Errorf("dispatch: driver_id must be a uuid: %w", sync.ErrOpRefused)
	}
	if err := s.crewInBranch(ctx, branchID, truckID, driverID); err != nil {
		return sync.RowState{}, err
	}
	notes, _ := fields["notes"].(string)
	id, err := syncCreateID(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	actor := syncActor(ctx, s.link, f.Actor)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin runs sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSyncSeqTx(ctx, tx, runsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	var rowID string
	err = tx.QueryRow(ctx, `
		INSERT INTO delivery_runs (id, run_no, branch_id, scheduled_date, truck_id, driver_id, notes, created_by, org_id, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), next_run_no($2::uuid), $2::uuid, $3::date,
		        NULLIF($4,'')::uuid, NULLIF($5,'')::uuid, $6, NULLIF($7,'')::uuid, $8::uuid, $9, $10::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING id::text`,
		id, branchID, date, truckID, driverID, notes, actor, f.OrgID, seq, key).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The ON CONFLICT DO NOTHING arm answered no row: the
		// client-chosen id is taken. Which refusal answers depends on
		// WHERE it is taken: inside the caller's org the collision is
		// the caller's own fact, outside it the answer is not-found so
		// existence never leaks across the scope boundary.
		if _, own, _ := s.load(ctx, tx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: runs sync create insert: %w", err)
	}
	r, err := runsSyncRowByID(ctx, tx, rowID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: runs sync create read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit runs sync create: %w", err)
	}
	return runStateOf(r)
}

// crewInBranch answers whether a declared truck and driver both belong
// to the branch the run is opened at, the same rule assertCrewInBranch
// states for the REST create.
func (s *RunsSyncStore) crewInBranch(ctx context.Context, branchID, truckID, driverID string) error {
	for _, leg := range []struct {
		query string
		id    string
		what  string
	}{
		{`SELECT EXISTS(SELECT 1 FROM dispatch_trucks WHERE id = $1::uuid AND branch_id = $2::uuid)`, truckID, "truck"},
		{`SELECT EXISTS(SELECT 1 FROM dispatch_drivers WHERE id = $1::uuid AND branch_id = $2::uuid)`, driverID, "driver"},
	} {
		if leg.id == "" {
			continue
		}
		var ok bool
		if err := s.pool.QueryRow(ctx, leg.query, leg.id, branchID).Scan(&ok); err != nil || !ok {
			return refused(fmt.Errorf("the %s is not at this branch", leg.what))
		}
	}
	return nil
}

// Write applies exactly the declared fields to one run under the
// module's own rules, on a FOR UPDATE-locked pre-image so the recorded
// transition is the one that landed: the status move runs the same
// matrix the REST transitions run, DISPATCHED needs its crew (0090's
// own CHECK), COMPLETED needs every stop settled (the precondition
// that applies to everybody, the settled set carrying CANCELLED, which
// is the ERP rule the runs ruling cites: the close over a cancelled
// stop is the write this scope exists to carry), and CANCELLED falls
// its READY stops back to unassigned exactly as CancelRun does. The
// row draws a fresh seq beside the write and stamps the idempotency
// key on the live row.
//
// THE COMPLETION AUTHORITY RULE DOES NOT KEY OFF A ROLE HERE. The REST
// route's narrower driver rule (everyStopHasAnAnswer, a driver may
// close only runs whose stops all recorded an outcome) keys on the
// caller's role at the moment of the tap; a replayed outbox carries
// the queued write, not the role, and the ruling that created this
// scope states the write it carries: the end-of-run close over a
// cancelled stop, the settled-set precondition every closer shares.
// The narrower REST rule keeps its own door; this one is the replay's.
func (s *RunsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	d, err := resolveDriverFence(ctx, s.pool, s.link, s.role, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := runScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := runWriteFields(fields); err != nil {
		return sync.RowState{}, err
	}
	status, hasStatus := fields["status"].(string)
	if hasStatus && !validRunStatus(status) {
		return sync.RowState{}, fmt.Errorf("dispatch: unknown run status %q: %w", status, sync.ErrOpRefused)
	}
	notes, hasNotes := fields["notes"].(string)
	if !hasStatus && !hasNotes {
		return sync.RowState{}, refused(fmt.Errorf("an update that moves nothing refuses"))
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin runs sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, truckID, driverID, fromNotes string
	arm, armArg := d.leg(runDriverCol, 3)
	if err := tx.QueryRow(ctx,
		`SELECT status, COALESCE(truck_id::text,''), COALESCE(driver_id::text,''), notes FROM delivery_runs
		 WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL`+arm+` FOR UPDATE`,
		id, f.OrgID, armArg).Scan(&from, &truckID, &driverID, &fromNotes); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("dispatch: lock run %q for sync write: %w", id, err)
	}
	if hasStatus && from != status {
		if !runCanTransition(from, status) {
			return sync.RowState{}, refused(ErrIllegalTransition)
		}
		switch status {
		case RunDispatched:
			if truckID == "" || driverID == "" {
				return sync.RowState{}, refused(ErrCrewRequired)
			}
		case RunCompleted:
			var open int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM delivery_stops
				 WHERE run_id = $1::uuid AND status NOT IN ('DELIVERED','FAILED','CANCELLED')`, id).Scan(&open); err != nil {
				return sync.RowState{}, fmt.Errorf("dispatch: open stop count for sync close: %w", err)
			}
			if open > 0 {
				return sync.RowState{}, refused(ErrRunHasOpenStops)
			}
		case RunCancelled:
			// CancelRun's own rule: a withdrawn run's READY stops fall
			// back to the unassigned column, they are not cancelled.
			// The stops draw their own scope's seq beside the move (the
			// unassignment is a stop change a device's pull must see,
			// same as the REST path's stamped AssignStop).
			if _, err := tx.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
				UPDATE delivery_stops SET run_id = NULL, stop_no = 0, updated_at = now(),
				       seq = (SELECT next_value FROM seq)
				 WHERE run_id = $1::uuid AND status = 'READY'`, id); err != nil {
				return sync.RowState{}, fmt.Errorf("dispatch: unassign stops on sync cancel: %w", err)
			}
		}
	}
	if (!hasStatus || from == status) && (!hasNotes || strings.TrimSpace(notes) == fromNotes) {
		// The declared write is already the row's standing fact: the
		// client's outcome is true, and a second seq for the same fact
		// would be noise in every device's feed. Answer applied with
		// the row as it stands, stamping nothing (a superseded replay
		// of one constant note meets this arm, never a fresh write).
		r, err := runsSyncRowByID(ctx, tx, id)
		if err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: runs sync write read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: commit runs sync write: %w", err)
		}
		return runStateOf(r)
	}
	to := from
	if hasStatus {
		to = status
	}
	at := time.Now().UTC()
	seq, err := allocateSyncSeqTx(ctx, tx, runsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	// The guarded UPDATE carries the driver arm the locked pre-image
	// already enforced, so the move cannot land on a row the fence took
	// away between the two statements.
	setArm, setArg := d.leg(runDriverCol, 10)
	var updated bool
	err = tx.QueryRow(ctx, `
		UPDATE delivery_runs SET
			status          = $3,
			dispatched_at   = CASE WHEN $3 = 'DISPATCHED' THEN $4 ELSE dispatched_at END,
			completed_at    = CASE WHEN $3 IN ('COMPLETED','CANCELLED') THEN $4 ELSE completed_at END,
			notes           = CASE WHEN $5::bool THEN $6 ELSE notes END,
			updated_at      = now(),
			seq             = $7,
			idempotency_key = $8::uuid
		WHERE id = $1::uuid AND org_id = $2::uuid AND status = $9`+setArm+`
		RETURNING (deleted_at IS NOT NULL)`,
		id, f.OrgID, to, at, hasNotes, notes, seq, key, from, setArg).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: runs sync write %q: %w", id, err)
	}
	r, err := runsSyncRowByID(ctx, tx, id)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: runs sync write read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit runs sync write: %w", err)
	}
	return runStateOf(r)
}

// Delete tombstones one run: deleted_at set, the row retained, a fresh
// seq. The idempotency key is deliberately NOT stamped over the row's:
// the tombstone keeps the last live operation's key so that key still
// dedups after the delete, and the delete itself is idempotent through
// the engine's already-tombstoned arm (syncscope.go's header).
func (s *RunsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	d, err := resolveDriverFence(ctx, s.pool, s.link, s.role, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := runScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin runs sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSyncSeqTx(ctx, tx, runsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	delArm, delArg := d.leg(runDriverCol, 4)
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE delivery_runs SET
			deleted_at = now(),
			updated_at = now(),
			seq        = $3
		WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL`+delArm+`
		RETURNING (deleted_at IS NOT NULL)`, id, f.OrgID, seq, delArg).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: runs sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit runs sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}

// validRunStatus pins the run status vocabulary (0090's CHECK set).
func validRunStatus(s string) bool {
	switch s {
	case RunPlanned, RunDispatched, RunCompleted, RunCancelled:
		return true
	}
	return false
}
