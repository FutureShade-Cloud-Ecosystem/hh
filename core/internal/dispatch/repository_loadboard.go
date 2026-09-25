package dispatch

// repository_loadboard.go, pgx persistence for the load board (0184).
//
// Three groups, each with one rule worth stating:
//
//   POLICY   branch-over-NULL (0043/0173). A missing dealer row is an ERROR, not a silent default:
//            the seed is part of the migration, and a service quietly running on hard-coded values
//            because a row vanished is a policy nobody can audit.
//   COMMIT   the REOPEN is written with the caller and the reason on it, on the load and (in bulk) on
//            the run. Clearing a run's marker clears all three, because "reopened by nobody, for no
//            reason" is worse than no history at all.
//   FREIGHT  written in ONE transaction over every load of an order. A split that lands on two of
//            three rows is an invoice that does not add up, and the surface would have no way to
//            know which third is missing.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// - policy -

// ReadPolicy resolves the effective dispatch policy: the branch row when one exists, else the dealer
// NULL row (the 0043 idiom, 0184 seeds the NULL row).
func (r *Repository) ReadPolicy(ctx context.Context, branchID string) (Policy, error) {
	pool, err := r.db()
	if err != nil {
		return Policy{}, err
	}
	const q = `
SELECT COALESCE(branch_id::text,''), commit_prompt_hours, reopen_policy, load_value_visibility,
       commit_horizon_weeks, working_view_days, freight_split_mode, driver_session_hours
  FROM dispatch_policy
 WHERE ($1 <> '' AND branch_id::text = $1) OR branch_id IS NULL
 ORDER BY branch_id::text NULLS LAST
 LIMIT 1`
	var p Policy
	err = pool.QueryRow(ctx, q, branchID).Scan(&p.BranchID, &p.CommitPromptHours, &p.ReopenPolicy,
		&p.LoadValueVisibility, &p.CommitHorizonWeeks, &p.WorkingViewDays, &p.FreightSplitMode,
		&p.DriverSessionHours)
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, fmt.Errorf("dispatch: dispatch_policy default row missing (0184 seed)")
	}
	return p, err
}

// UpsertPolicy writes the dealer NULL row (BranchID "") or a branch override row.
func (r *Repository) UpsertPolicy(ctx context.Context, p Policy) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	if p.BranchID == "" {
		_, err = pool.Exec(ctx, `
UPDATE dispatch_policy SET commit_prompt_hours = $1, reopen_policy = $2, load_value_visibility = $3,
       commit_horizon_weeks = $4, working_view_days = $5, freight_split_mode = $6,
       driver_session_hours = $7, updated_at = now()
 WHERE branch_id IS NULL`,
			p.CommitPromptHours, p.ReopenPolicy, p.LoadValueVisibility, p.CommitHorizonWeeks,
			p.WorkingViewDays, p.FreightSplitMode, p.DriverSessionHours)
		return err
	}
	_, err = pool.Exec(ctx, `
INSERT INTO dispatch_policy (branch_id, commit_prompt_hours, reopen_policy, load_value_visibility,
                             commit_horizon_weeks, working_view_days, freight_split_mode,
                             driver_session_hours)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (branch_id) DO UPDATE SET
    commit_prompt_hours = EXCLUDED.commit_prompt_hours,
    reopen_policy = EXCLUDED.reopen_policy,
    load_value_visibility = EXCLUDED.load_value_visibility,
    commit_horizon_weeks = EXCLUDED.commit_horizon_weeks,
    working_view_days = EXCLUDED.working_view_days,
    freight_split_mode = EXCLUDED.freight_split_mode,
    driver_session_hours = EXCLUDED.driver_session_hours,
    updated_at = now()`,
		p.BranchID, p.CommitPromptHours, p.ReopenPolicy, p.LoadValueVisibility, p.CommitHorizonWeeks,
		p.WorkingViewDays, p.FreightSplitMode, p.DriverSessionHours)
	return err
}

// - the reopen -

// ReopenStop returns ONE committed load to REQUESTED, with who, when and why on it. The commit stamps
// are deliberately left alone: "committed on the 2nd, reopened on the 3rd" is the history somebody
// reads when a customer asks why the truck changed.
func (r *Repository) ReopenStop(ctx context.Context, stopID, reason, actorID string, at time.Time) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
UPDATE delivery_stops
   SET commit_state = 'REQUESTED', reopened_by = NULLIF($2,'')::uuid, reopened_at = $3,
       reopen_reason = $4, updated_at = now(), seq = (SELECT next_value FROM seq)
 WHERE id = $1`, stopID, actorID, at, reason)
	return err
}

// ReopenRun is the BULK form: the run's marker and every live load on it, in ONE transaction. A
// reopen that lands on the run but not on its loads would leave a truck that says it is open above
// loads that still refuse to move.
func (r *Repository) ReopenRun(ctx context.Context, runID, reason, actorID string, at time.Time) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
UPDATE delivery_runs SET commit_state = 'REOPENED', reopened_by = NULLIF($2,'')::uuid,
       reopened_at = $3, reopen_reason = $4, updated_at = now()
 WHERE id = $1`, runID, actorID, at, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
UPDATE delivery_stops
   SET commit_state = 'REQUESTED', reopened_by = NULLIF($2,'')::uuid, reopened_at = $3,
       reopen_reason = $4, updated_at = now(), seq = (SELECT next_value FROM seq)
 WHERE run_id = $1 AND status NOT IN ('CANCELLED','FAILED')`, runID, actorID, at, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClearRunReopen takes the marker off. All three fields go together, 0185's CHECK refuses a REOPENED
// run with no evidence, and "reopened by nobody, for no reason" is worse than no history at all.
func (r *Repository) ClearRunReopen(ctx context.Context, runID string) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
UPDATE delivery_runs SET commit_state = 'AUTO', reopened_by = NULL, reopened_at = NULL,
       reopen_reason = '', updated_at = now()
 WHERE id = $1`, runID)
	return err
}

// UnassignedInWindow is THE WORKING VIEW read: every live load between two dates that is not yet on a
// truck. It is the staging lane, and the staging lane is the dispatcher's work queue, which is why
// it reaches a fortnight rather than a day (0185 §1).
func (r *Repository) UnassignedInWindow(ctx context.Context, branchID, from, to string) ([]Stop, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, stopSelect+
		` WHERE s.branch_id = $1 AND s.run_id IS NULL
            AND s.scheduled_date BETWEEN $2::date AND $3::date
            AND s.status NOT IN ('CANCELLED','FAILED')
          ORDER BY s.scheduled_date, s.stop_no, s.created_at`, branchID, from, to)
	if err != nil {
		return nil, fmt.Errorf("working view: %w", err)
	}
	defer rows.Close()
	out := []Stop{}
	for rows.Next() {
		st, err := scanStop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// - requested → committed -

// CommitStopDate promises a date. requested_date is BACK-FILLED from the current scheduled_date the
// first time, so the ask survives the promise even when the stop was born committed.
func (r *Repository) CommitStopDate(ctx context.Context, stopID, toDate, actorID string, at time.Time) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
UPDATE delivery_stops
   SET requested_date = COALESCE(requested_date, scheduled_date),
       scheduled_date = COALESCE(NULLIF($2,'')::date, scheduled_date),
       commit_state = 'COMMITTED', committed_at = $3, committed_by = NULLIF($4,'')::uuid,
       updated_at = now(), seq = (SELECT next_value FROM seq)
 WHERE id = $1`, stopID, toDate, at, actorID)
	return err
}

// - the split -

// LoadsForOrder reads every live load of one order in load order, the set a freight split divides.
//
// The statement itself moved to loadsForOrderOn (repository_freightraise.go) when the approval gate
// needed the SAME read on a caller's transaction. Two copies of a select that decides how freight is
// divided is two selects that can come to disagree about which loads count; this is the pool-shaped
// door onto the one body.
func (r *Repository) LoadsForOrder(ctx context.Context, orderID string) ([]Stop, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	return r.loadsForOrderOn(ctx, pool, orderID)
}

// WriteFreightSplit stores one split ATOMICALLY across every load of the order. The amounts arrive
// already computed and already validated by the service, this function does no arithmetic, which is
// the point: there is exactly one place in the system that divides freight, and it is not here and
// it is not the browser.
// The per-row UPDATE moved to writeFreightSplitOn (repository_freightraise.go) so the approval
// gate's apply can run the identical statements inside the transaction that stamps the decision.
// This entry point keeps its own transaction, because a caller that has none still needs one.
func (r *Repository) WriteFreightSplit(ctx context.Context, stopIDs []string, amounts []int64, override bool, loadCount int) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := r.writeFreightSplitOn(ctx, tx, stopIDs, amounts, override, loadCount); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InsertSplitLoad opens the Nth load of an order. It copies the parent load's delivery facts (the
// address does not change because the material came on two trucks) and takes its own measurements.
func (r *Repository) InsertSplitLoad(ctx context.Context, parent Stop, seq, count int, createdBy string) (Stop, error) {
	pool, err := r.db()
	if err != nil {
		return Stop{}, err
	}
	id, err := r.insertSplitLoadOn(ctx, pool, parent.ID, createdBy, seq, count)
	if err != nil {
		return Stop{}, err
	}
	return r.GetStop(ctx, id)
}

// - the gap read -

// StopsForGapScan reads the (id, order, order_no) triples of a day's live loads, the minimum a gap
// scan needs, so the scan never drags a board's worth of addresses through a second query.
func (r *Repository) StopsForGapScan(ctx context.Context, branchID, date string) ([][3]string, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
SELECT s.id::text, s.order_id::text, COALESCE(o.order_no::text,'')
  FROM delivery_stops s LEFT JOIN orders o ON o.id = s.order_id
 WHERE s.branch_id = $1 AND s.scheduled_date = $2::date
   AND s.status IN ('READY','LOADED')
 ORDER BY s.stop_no`, branchID, date)
	if err != nil {
		return nil, fmt.Errorf("gap scan: %w", err)
	}
	defer rows.Close()
	out := [][3]string{}
	for rows.Next() {
		var t [3]string
		if err := rows.Scan(&t[0], &t[1], &t[2]); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// - the calendar -

// CalendarDay is one square of the week/month view: the question the client said the calendar
// answers is "how many trucks are full", so the row carries the count of runs and the count of runs
// whose gauge is at or over the amber line, nothing else.
type CalendarDay struct {
	Date       string `json:"date"`
	Runs       int    `json:"runs"`
	Stops      int    `json:"stops"`
	Unassigned int    `json:"unassigned"`
	Full       int    `json:"full"`
	Requested  int    `json:"requested"`
}

// CalendarRows reads the per-day counts over a date range in ONE query. The "full" count is filled
// in by the service, which is the only layer that knows the gauge bands.
func (r *Repository) CalendarRows(ctx context.Context, branchID, from, to string) ([]CalendarDay, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
SELECT to_char(d::date,'YYYY-MM-DD'),
       (SELECT count(*) FROM delivery_runs r
         WHERE r.branch_id = $1 AND r.scheduled_date = d::date AND r.status <> 'CANCELLED')::int,
       (SELECT count(*) FROM delivery_stops s
         WHERE s.branch_id = $1 AND s.scheduled_date = d::date
           AND s.status NOT IN ('CANCELLED','FAILED'))::int,
       (SELECT count(*) FROM delivery_stops s
         WHERE s.branch_id = $1 AND s.scheduled_date = d::date AND s.run_id IS NULL
           AND s.status NOT IN ('CANCELLED','FAILED'))::int,
       (SELECT count(*) FROM delivery_stops s
         WHERE s.branch_id = $1 AND s.scheduled_date = d::date AND s.commit_state = 'REQUESTED'
           AND s.status NOT IN ('CANCELLED','FAILED'))::int
  FROM generate_series($2::date, $3::date, interval '1 day') AS d
 ORDER BY 1`, branchID, from, to)
	if err != nil {
		return nil, fmt.Errorf("calendar: %w", err)
	}
	defer rows.Close()
	out := []CalendarDay{}
	for rows.Next() {
		var c CalendarDay
		if err := rows.Scan(&c.Date, &c.Runs, &c.Stops, &c.Unassigned, &c.Requested); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// - the load's own measurements -

// UpdateLoadFacts writes what somebody measured.
//
// A NIL FIELD IS "LEAVE IT ALONE" AND AN ENTRY IN Clear IS "NOBODY MEASURED IT AFTER ALL". The two
// have to be different requests: an empty weight box on a form must not silently erase the number
// the yard put on the load at 06:10, and there still has to be a way to take back a mistake.
func (r *Repository) UpdateLoadFacts(ctx context.Context, stopID string, in LoadFactsInput) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	clear := map[string]bool{}
	for _, c := range in.Clear {
		clear[c] = true
	}
	// The shared stops stream's seq rides the facts (0213, the board
	// scopes' design note in syncscope_board.go): what a load weighs is
	// a load fact, and a device's pull must see it the moment the yard
	// records it.
	_, err = pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
UPDATE delivery_stops SET
    weight_kg        = CASE WHEN $5 THEN NULL WHEN $2::numeric IS NULL THEN weight_kg        ELSE $2 END,
    volume_m3        = CASE WHEN $6 THEN NULL WHEN $3::numeric IS NULL THEN volume_m3        ELSE $3 END,
    pallet_positions = CASE WHEN $7 THEN NULL WHEN $4::int     IS NULL THEN pallet_positions ELSE $4 END,
    updated_at = now(),
    seq = (SELECT next_value FROM seq)
 WHERE id = $1`, stopID, in.WeightKg, in.VolumeM3, in.PalletPositions,
		clear["weight_kg"], clear["volume_m3"], clear["pallet_positions"])
	return err
}

// - the calendar's range reads -

// ListRunsRange and ListStopsRange are the week/month reads. They are deliberately two plain range
// queries rather than one join: the calendar folds them with the SAME BuildGauge the day board uses,
// so "full" means the same thing on both screens (and there is one 85% line in the system).

func (r *Repository) ListRunsRange(ctx context.Context, branchID, from, to string) ([]Run, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, runSelect+
		` WHERE r.branch_id = $1 AND r.scheduled_date BETWEEN $2::date AND $3::date
          ORDER BY r.scheduled_date, r.run_no`, branchID, from, to)
	if err != nil {
		return nil, fmt.Errorf("runs range: %w", err)
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (r *Repository) ListStopsRange(ctx context.Context, branchID, from, to string) ([]Stop, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, stopSelect+
		` WHERE s.branch_id = $1 AND s.scheduled_date BETWEEN $2::date AND $3::date
          ORDER BY s.scheduled_date, s.stop_no`, branchID, from, to)
	if err != nil {
		return nil, fmt.Errorf("stops range: %w", err)
	}
	defer rows.Close()
	out := []Stop{}
	for rows.Next() {
		st, err := scanStop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// - the pickup card -
//
// CountActivePickups LIVED HERE AND HAS BEEN REMOVED (D-110, session A). It counted live rack-tag
// CODES, `will_call_pickups WHERE status='ACTIVE' AND expires_at > now()`, under a comment
// asserting it was "the customer pickups card". After D-110 that sentence was false in both
// directions: a pickup nobody has pulled yet has no code (the mint gate refuses one until the pick is
// staged), so the pickups most in need of attention were invisible to it; and a printed code that had
// not expired kept counting after the customer drove away.
//
// It is deleted rather than left dormant. A read whose only remaining consumer is a comment is a read
// somebody wires back, and this one carried its own wrong explanation with it. The card is now
// counted by Service.PendingPickupCounts over PendingPickupOrderIDs (repository_w3.go), from the same
// predicate the pick queue itself lists from. A future will-call desk that genuinely wants "how many
// live codes are out" should ask that question in those words, with a name that says so.
