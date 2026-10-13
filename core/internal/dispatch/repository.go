package dispatch

// repository.go, the pgx layer over the 0090-0093 tables.
//
// TWO RULES THIS FILE KEEPS.
//
//  1. NO AUTHORITY LIVES HERE. Every method takes ids the service has already fenced. The repository
//     never reads a StaffContext and never decides who may see what, a fence spread across a service
//     AND its queries is a fence with two chances to be forgotten.
//
//  2. STATUS WRITES ARE GUARDED, ALWAYS. Every status UPDATE carries `AND status = $from` and reports
//     whether it actually changed a row. The service checks legality against the matrix first for a
//     good error message; this is what makes the check ATOMIC, two dispatchers tapping "en route" on
//     the same stop cannot both succeed, and the loser gets a conflict rather than a silent overwrite.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is dispatch's persistence layer. A nil pool is tolerated at construction (the scaffold
// posture the rest of the tree uses) and refused at call time.
type Repository struct{ pool *pgxpool.Pool }

// NewRepository wraps a pool (may be nil).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var errNoDB = errors.New("dispatch: no database")

func (r *Repository) db() (*pgxpool.Pool, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	return r.pool, nil
}

// - fleet -

const truckCols = `id::text, branch_id::text, code::text, name, truck_type, capacity_kg,
       capacity_m3, pallet_positions, is_active`

func scanTruck(row pgx.Row) (Truck, error) {
	var t Truck
	err := row.Scan(&t.ID, &t.BranchID, &t.Code, &t.Name, &t.TruckType, &t.CapacityKg,
		&t.CapacityM3, &t.PalletPositions, &t.IsActive)
	return t, err
}

// ListTrucks returns a branch's trucks, active first then by code.
func (r *Repository) ListTrucks(ctx context.Context, branchID string, includeInactive bool) ([]Truck, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + truckCols + ` FROM dispatch_trucks WHERE branch_id = $1`
	if !includeInactive {
		q += ` AND is_active`
	}
	q += ` ORDER BY is_active DESC, code`
	rows, err := pool.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("list trucks: %w", err)
	}
	defer rows.Close()
	out := []Truck{}
	for rows.Next() {
		t, err := scanTruck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTruck reads one truck. pgx.ErrNoRows maps to ErrNotFound here (not at the service) because a
// malformed uuid and a missing row must be indistinguishable to the caller.
func (r *Repository) GetTruck(ctx context.Context, id string) (Truck, error) {
	pool, err := r.db()
	if err != nil {
		return Truck{}, err
	}
	if !looksLikeUUID(id) {
		return Truck{}, ErrNotFound
	}
	t, err := scanTruck(pool.QueryRow(ctx, `SELECT `+truckCols+` FROM dispatch_trucks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Truck{}, ErrNotFound
	}
	return t, err
}

// InsertTruck creates a truck. The (branch_id, code) unique violation surfaces as a client error the
// service maps to 409, "TRK-1 already exists at this branch" is a fact the dispatcher can act on.
func (r *Repository) InsertTruck(ctx context.Context, t Truck) (string, error) {
	pool, err := r.db()
	if err != nil {
		return "", err
	}
	var id string
	err = pool.QueryRow(ctx, `
INSERT INTO dispatch_trucks (branch_id, code, name, truck_type, capacity_kg, capacity_m3,
                             pallet_positions, is_active)
VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE) RETURNING id::text`,
		t.BranchID, t.Code, t.Name, t.TruckType, t.CapacityKg, t.CapacityM3, t.PalletPositions).Scan(&id)
	return id, err
}

const driverCols = `id::text, branch_id::text, COALESCE(user_id::text,''), name, phone, licence_class, is_active`

func scanDriver(row pgx.Row) (Driver, error) {
	var d Driver
	err := row.Scan(&d.ID, &d.BranchID, &d.UserID, &d.Name, &d.Phone, &d.LicenceClass, &d.IsActive)
	return d, err
}

// ListDrivers returns a branch's drivers.
func (r *Repository) ListDrivers(ctx context.Context, branchID string, includeInactive bool) ([]Driver, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + driverCols + ` FROM dispatch_drivers WHERE branch_id = $1`
	if !includeInactive {
		q += ` AND is_active`
	}
	q += ` ORDER BY is_active DESC, name`
	rows, err := pool.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("list drivers: %w", err)
	}
	defer rows.Close()
	out := []Driver{}
	for rows.Next() {
		d, err := scanDriver(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDriver reads one driver.
func (r *Repository) GetDriver(ctx context.Context, id string) (Driver, error) {
	pool, err := r.db()
	if err != nil {
		return Driver{}, err
	}
	if !looksLikeUUID(id) {
		return Driver{}, ErrNotFound
	}
	d, err := scanDriver(pool.QueryRow(ctx, `SELECT `+driverCols+` FROM dispatch_drivers WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Driver{}, ErrNotFound
	}
	return d, err
}

// DriverForUser resolves the driver record linked to a staff login, the driver PWA's "my runs" read.
// A staff user with no driver row is ErrNotFound, which the surface renders as "you are not set up as
// a driver at this branch" rather than an empty run list that looks like a day off.
func (r *Repository) DriverForUser(ctx context.Context, userID string) (Driver, error) {
	pool, err := r.db()
	if err != nil {
		return Driver{}, err
	}
	if !looksLikeUUID(userID) {
		return Driver{}, ErrNotFound
	}
	d, err := scanDriver(pool.QueryRow(ctx,
		`SELECT `+driverCols+` FROM dispatch_drivers WHERE user_id = $1 AND is_active ORDER BY created_at LIMIT 1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Driver{}, ErrNotFound
	}
	return d, err
}

// InsertDriver creates a driver.
func (r *Repository) InsertDriver(ctx context.Context, d Driver) (string, error) {
	pool, err := r.db()
	if err != nil {
		return "", err
	}
	var id string
	err = pool.QueryRow(ctx, `
INSERT INTO dispatch_drivers (branch_id, user_id, name, phone, licence_class, is_active)
VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5, TRUE) RETURNING id::text`,
		d.BranchID, d.UserID, d.Name, d.Phone, d.LicenceClass).Scan(&id)
	return id, err
}

// - runs -

// runSelect resolves the truck code and driver name in the same read the board renders from: the
// board shows "TRK-1 · Wade Prentice", and making the client fetch two registries to turn two UUIDs
// into two words is how a board ends up rendering UUIDs.
const runSelectColumns = `
SELECT r.id::text, r.run_no, r.branch_id::text, to_char(r.scheduled_date,'YYYY-MM-DD'),
       COALESCE(r.truck_id::text,''), COALESCE(r.driver_id::text,''),
       COALESCE(t.code::text,''), COALESCE(d.name,''),
       r.status, r.notes, r.dispatched_at, r.completed_at,
       (SELECT count(*) FROM delivery_stops s WHERE s.run_id = r.id)::int,
       r.created_at, r.commit_state, COALESCE(to_char(r.reopened_at,'YYYY-MM-DD"T"HH24:MI:SSOF:00'),''),
       r.reopen_reason`

// runSelectFrom is the FROM half; the sync adapter (syncscope_runs.go)
// composes it with the same columns plus the sync legs, so one column
// list serves the board and the device.
const runSelectFrom = `
  FROM delivery_runs r
  LEFT JOIN dispatch_trucks  t ON t.id = r.truck_id
  LEFT JOIN dispatch_drivers d ON d.id = r.driver_id`

const runSelect = runSelectColumns + runSelectFrom

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.RunNo, &r.BranchID, &r.ScheduledDate, &r.TruckID, &r.DriverID,
		&r.TruckCode, &r.DriverName, &r.Status, &r.Notes, &r.DispatchedAt, &r.CompletedAt,
		&r.StopCount, &r.CreatedAt, &r.CommitState, &r.ReopenedAt, &r.ReopenReason)
	return r, err
}

// InsertRun opens a run, minting its number through next_run_no (the ONLY mint, 0090's header says
// why three writers inventing three formats is not hypothetical here).
func (r *Repository) InsertRun(ctx context.Context, in Run, createdBy string) (Run, error) {
	pool, err := r.db()
	if err != nil {
		return Run{}, err
	}
	var id string
	// The sync legs (0213) stamp beside the row: the org from the
	// branch, the seq drawn from the runs scope's allocator inside the
	// same statement, so a run the desk opens is visible to a device's
	// pull the moment it lands.
	err = pool.QueryRow(ctx, fmt.Sprintf(syncSeqCTE, runsSyncScope)+`
INSERT INTO delivery_runs (run_no, branch_id, scheduled_date, truck_id, driver_id, notes, created_by, org_id, seq)
SELECT next_run_no($1), $1, $2::date, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid, $5, NULLIF($6,'')::uuid,
       (SELECT org_id FROM locations WHERE id = $1::uuid), (SELECT next_value FROM seq)
RETURNING id::text`,
		in.BranchID, in.ScheduledDate, in.TruckID, in.DriverID, in.Notes, createdBy).Scan(&id)
	if err != nil {
		return Run{}, err
	}
	return r.GetRun(ctx, id)
}

// GetRun reads one run by id.
func (r *Repository) GetRun(ctx context.Context, id string) (Run, error) {
	pool, err := r.db()
	if err != nil {
		return Run{}, err
	}
	if !looksLikeUUID(id) {
		return Run{}, ErrNotFound
	}
	run, err := scanRun(pool.QueryRow(ctx, runSelect+` WHERE r.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return run, err
}

// GetRunByNo reads one run by its printed number ("#RUN-TRT-3"). Used by the number-lookup endpoint,
// whose 404-not-403 posture is the reason this exists as a separate read.
func (r *Repository) GetRunByNo(ctx context.Context, runNo string) (Run, error) {
	pool, err := r.db()
	if err != nil {
		return Run{}, err
	}
	run, err := scanRun(pool.QueryRow(ctx, runSelect+` WHERE r.run_no = $1`, runNo))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return run, err
}

// ListRuns returns a branch's runs for a date (empty date = every date, newest first).
func (r *Repository) ListRuns(ctx context.Context, branchID, date string) ([]Run, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	q := runSelect + ` WHERE r.branch_id = $1 AND ($2 = '' OR r.scheduled_date = $2::date)
                      ORDER BY r.scheduled_date DESC, r.run_no`
	rows, err := pool.Query(ctx, q, branchID, date)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
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

// ListRunsForDriver returns one driver's runs on a date, the driver PWA's day.
func (r *Repository) ListRunsForDriver(ctx context.Context, driverID, date string) ([]Run, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, runSelect+
		` WHERE r.driver_id = $1 AND ($2 = '' OR r.scheduled_date = $2::date)
          ORDER BY r.scheduled_date DESC, r.run_no`, driverID, date)
	if err != nil {
		return nil, fmt.Errorf("list driver runs: %w", err)
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

// AssignRunCrew sets (or clears) a run's truck/driver. Only a PLANNED run may be re-crewed: once the
// truck has left, changing who drove it rewrites history.
func (r *Repository) AssignRunCrew(ctx context.Context, runID, truckID, driverID string) (bool, error) {
	pool, err := r.db()
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, runsSyncScope)+`
UPDATE delivery_runs
   SET truck_id = NULLIF($2,'')::uuid, driver_id = NULLIF($3,'')::uuid, updated_at = now(),
       seq = (SELECT next_value FROM seq)
 WHERE id = $1 AND status = 'PLANNED'`, runID, truckID, driverID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// UpdateRunStatus is the guarded run transition: it lands ONLY when the row is still in `from`.
// updated=false with err=nil means someone else moved it first, a conflict, not a failure.
func (r *Repository) UpdateRunStatus(ctx context.Context, runID, from, to string, at time.Time) (updated bool, err error) {
	pool, err := r.db()
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, runsSyncScope)+`
UPDATE delivery_runs
   SET status = $3,
       dispatched_at = CASE WHEN $3 = 'DISPATCHED' THEN $4 ELSE dispatched_at END,
       completed_at  = CASE WHEN $3 IN ('COMPLETED','CANCELLED') THEN $4 ELSE completed_at END,
       updated_at = now(),
       seq = (SELECT next_value FROM seq)
 WHERE id = $1 AND status = $2`, runID, from, to, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// OpenStopCount counts a run's stops that are not yet terminal, the run-completion guard's input.
func (r *Repository) OpenStopCount(ctx context.Context, runID string) (int, error) {
	pool, err := r.db()
	if err != nil {
		return 0, err
	}
	var n int
	err = pool.QueryRow(ctx,
		`SELECT count(*)::int FROM delivery_stops
          WHERE run_id = $1 AND status NOT IN ('DELIVERED','FAILED','CANCELLED')`, runID).Scan(&n)
	return n, err
}

// - stops -

// stopSelect joins orders and customers for DISPLAY ONLY, the order number and the customer name the
// board, the manifest and the driver's stop card all show. AUTHORITY never comes from this read: the
// branch a stop is fenced by is delivery_stops.branch_id, copied at creation through the OrderReader
// seam (module.go). The join is here rather than behind the seam because order.Repository.OrderBrief
// is a FROZEN four-field authority seam that deliberately carries no display fields, and widening
// another module's frozen seam for a label is not a trade this module gets to make.
const stopSelectColumns = `
SELECT s.id::text, s.branch_id::text, s.order_id::text, COALESCE(o.order_no::text,''),
       COALESCE(c.name,''), COALESCE(s.run_id::text,''), COALESCE(r.run_no,''),
       s.stop_no, to_char(s.scheduled_date,'YYYY-MM-DD'), s.status, s.time_window,
       s.contact_name, s.contact_phone, s.address_line, s.city, s.postal_code,
       s.latitude, s.longitude, s.instructions,
       s.pod_signature_ref, s.pod_received_by, s.pod_at, s.failure_reason,
       s.loaded_at, s.en_route_at, s.delivered_at, s.handoff_at,
       (SELECT count(*) FROM delivery_pod_photos p WHERE p.stop_id = s.id)::int,
       s.created_at,
       -- 0184: the load-building facts. Every measurement stays NULLable end to end, the Go side
       -- reads them as pointers precisely so "nobody measured it" survives the trip.
       s.weight_kg, s.volume_m3, s.pallet_positions, s.value_cents, s.freight_cents,
       s.freight_is_override, s.load_seq, s.load_count,
       COALESCE(to_char(s.requested_date,'YYYY-MM-DD'),''), s.commit_state, s.committed_at,
       s.reopened_at, s.reopen_reason`

// stopSelectFrom is the FROM half, split for the same reason
// runSelectFrom exists: the sync adapter (syncscope_stops.go) composes
// the identical columns plus the sync legs over it.
const stopSelectFrom = `
  FROM delivery_stops s
  LEFT JOIN orders       o ON o.id = s.order_id
  LEFT JOIN customers    c ON c.id = o.customer_id
  LEFT JOIN delivery_runs r ON r.id = s.run_id`

const stopSelect = stopSelectColumns + stopSelectFrom

func scanStop(row pgx.Row) (Stop, error) {
	var s Stop
	err := row.Scan(&s.ID, &s.BranchID, &s.OrderID, &s.OrderNo, &s.CustomerName, &s.RunID, &s.RunNo,
		&s.StopNo, &s.ScheduledDate, &s.Status, &s.TimeWindow, &s.ContactName, &s.ContactPhone,
		&s.AddressLine, &s.City, &s.PostalCode, &s.Latitude, &s.Longitude, &s.Instructions,
		&s.PODSignedRef, &s.PODReceivedBy, &s.PODAt, &s.FailureReason,
		&s.LoadedAt, &s.EnRouteAt, &s.DeliveredAt, &s.HandoffAt, &s.PhotoCount, &s.CreatedAt,
		&s.WeightKg, &s.VolumeM3, &s.PalletPositions, &s.ValueCents, &s.FreightCents,
		&s.FreightIsOverride, &s.LoadSeq, &s.LoadCount,
		&s.RequestedDate, &s.CommitState, &s.CommittedAt, &s.ReopenedAt, &s.ReopenReason)
	return s, err
}

// InsertStop schedules one order. branchID is the ORDER's branch (the service copies it through the
// seam), never the caller's.
func (r *Repository) InsertStop(ctx context.Context, in Stop, createdBy string, now time.Time) (Stop, error) {
	pool, err := r.db()
	if err != nil {
		return Stop{}, err
	}
	var id string
	// The sync legs stamp beside the row exactly as InsertRun's do: the
	// org from the branch, the seq from the stops scope's allocator,
	// one statement.
	err = pool.QueryRow(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
INSERT INTO delivery_stops (branch_id, order_id, scheduled_date, time_window, contact_name,
                            contact_phone, address_line, city, postal_code, latitude, longitude,
                            instructions, created_by,
                            weight_kg, volume_m3, pallet_positions, value_cents,
                            requested_date, commit_state, committed_at, committed_by, org_id, seq)
SELECT $1, $2, $3::date, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13,'')::uuid,
        $14, $15, $16, $17, NULLIF($18,'')::date, $19,
        -- A COMMITTED row carries its stamp or the 0184 CHECK refuses it. The stamp is the clock and
        -- the caller the SERVICE resolved, never anything the request supplied. The service refuses
        -- the whole write for a caller without the commit capability (0185, item D).
        CASE WHEN $19 = 'COMMITTED' THEN $20::timestamptz ELSE NULL END,
        CASE WHEN $19 = 'COMMITTED' THEN NULLIF($13,'')::uuid ELSE NULL END,
        (SELECT org_id FROM locations WHERE id = $1::uuid), (SELECT next_value FROM seq)
RETURNING id::text`,
		in.BranchID, in.OrderID, in.ScheduledDate, in.TimeWindow, in.ContactName, in.ContactPhone,
		in.AddressLine, in.City, in.PostalCode, in.Latitude, in.Longitude, in.Instructions, createdBy,
		in.WeightKg, in.VolumeM3, in.PalletPositions, in.ValueCents,
		in.RequestedDate, in.CommitState, now).Scan(&id)
	if err != nil {
		return Stop{}, err
	}
	return r.GetStop(ctx, id)
}

// GetStop reads one stop.
func (r *Repository) GetStop(ctx context.Context, id string) (Stop, error) {
	pool, err := r.db()
	if err != nil {
		return Stop{}, err
	}
	if !looksLikeUUID(id) {
		return Stop{}, ErrNotFound
	}
	s, err := scanStop(pool.QueryRow(ctx, stopSelect+` WHERE s.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Stop{}, ErrNotFound
	}
	return s, err
}

// ListStops returns a branch's stops for a date, run order then stop order then creation order.
func (r *Repository) ListStops(ctx context.Context, branchID, date string) ([]Stop, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, stopSelect+`
 WHERE s.branch_id = $1 AND ($2 = '' OR s.scheduled_date = $2::date)
 ORDER BY r.run_no NULLS FIRST, s.stop_no, s.created_at`, branchID, date)
	if err != nil {
		return nil, fmt.Errorf("list stops: %w", err)
	}
	defer rows.Close()
	out := []Stop{}
	for rows.Next() {
		s, err := scanStop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListStopsForRun returns one run's stops in stop order, the manifest's body and the driver's list.
func (r *Repository) ListStopsForRun(ctx context.Context, runID string) ([]Stop, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, stopSelect+` WHERE s.run_id = $1 ORDER BY s.stop_no, s.created_at`, runID)
	if err != nil {
		return nil, fmt.Errorf("list run stops: %w", err)
	}
	defer rows.Close()
	out := []Stop{}
	for rows.Next() {
		s, err := scanStop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// LiveStopsForOrder returns the order's non-terminal stops, the will-call / re-schedule guard's input.
func (r *Repository) LiveStopsForOrder(ctx context.Context, orderID string) ([]Stop, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, stopSelect+
		` WHERE s.order_id = $1 AND s.status NOT IN ('CANCELLED','FAILED') ORDER BY s.created_at`, orderID)
	if err != nil {
		return nil, fmt.Errorf("live stops: %w", err)
	}
	defer rows.Close()
	out := []Stop{}
	for rows.Next() {
		s, err := scanStop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AssignStop puts a stop on a run (or takes it off, with an empty runID). Only a stop that has not
// yet been loaded may be re-assigned, a stop physically on a truck does not move to another truck by
// someone dragging a card.
func (r *Repository) AssignStop(ctx context.Context, stopID, runID string, stopNo int) (bool, error) {
	pool, err := r.db()
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
UPDATE delivery_stops
   SET run_id = NULLIF($2,'')::uuid, stop_no = $3, updated_at = now(),
       seq = (SELECT next_value FROM seq)
 WHERE id = $1 AND status = 'READY'`, stopID, runID, stopNo)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// NextStopNo returns the next position on a run (1-based) so an appended stop lands at the end.
func (r *Repository) NextStopNo(ctx context.Context, runID string) (int, error) {
	pool, err := r.db()
	if err != nil {
		return 0, err
	}
	var n int
	err = pool.QueryRow(ctx,
		`SELECT COALESCE(max(stop_no),0)::int + 1 FROM delivery_stops WHERE run_id = $1`, runID).Scan(&n)
	return n, err
}

// UpdateStopStatus is the guarded stop transition. The per-status timestamp is stamped in the same
// statement, so a stop can never carry a status without the time it reached it.
func (r *Repository) UpdateStopStatus(ctx context.Context, stopID, from, to, failureReason string, at time.Time) (updated bool, err error) {
	pool, err := r.db()
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
UPDATE delivery_stops
   SET status = $3,
       loaded_at    = CASE WHEN $3 = 'LOADED'    THEN $5 ELSE loaded_at END,
       en_route_at  = CASE WHEN $3 = 'EN_ROUTE'  THEN $5 ELSE en_route_at END,
       delivered_at = CASE WHEN $3 = 'DELIVERED' THEN $5 ELSE delivered_at END,
       failure_reason = CASE WHEN $3 = 'FAILED' THEN $4 ELSE failure_reason END,
       updated_at = now(),
       seq = (SELECT next_value FROM seq)
 WHERE id = $1 AND status = $2`, stopID, from, to, failureReason, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetPOD records the attestation on a stop AND flips it to DELIVERED in ONE statement.
//
// WHY ONE STATEMENT. The 0090 CHECK refuses a DELIVERED row without a signature ref. Writing the ref
// first and the status second would leave a window where a crash produces a stop with proof and no
// delivery; writing status first is simply refused by the CHECK. Doing both together means the
// constraint and the write agree by construction, and a partial POD is not a state that exists.
func (r *Repository) SetPOD(ctx context.Context, stopID, from, sigRef, receivedBy, podBy, idemKey string, at time.Time) (updated bool, err error) {
	pool, err := r.db()
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
UPDATE delivery_stops
   SET pod_signature_ref = $3, pod_received_by = $4, pod_at = $6, pod_by = NULLIF($5,'')::uuid,
       status = 'DELIVERED', delivered_at = $6, updated_at = now(),
       idempotency_key = COALESCE(NULLIF($7,'')::uuid, idempotency_key),
       seq = (SELECT next_value FROM seq)
 WHERE id = $1 AND status = $2`, stopID, from, sigRef, receivedBy, podBy, at, idemKey)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MarkHandoff stamps the moment this stop's delivery advanced its order toward invoicing. It is
// idempotent (`AND handoff_at IS NULL`): a retried handoff never restamps, so the column keeps
// meaning "when billing first heard about this".
func (r *Repository) MarkHandoff(ctx context.Context, stopID string, at time.Time) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		fmt.Sprintf(syncSeqCTE, stopsSyncScope)+
			`UPDATE delivery_stops SET handoff_at = $2, updated_at = now(), seq = (SELECT next_value FROM seq)
			 WHERE id = $1 AND handoff_at IS NULL`,
		stopID, at)
	return err
}

// - POD photos -

// InsertPhoto stores one POD photo. byte_size is passed explicitly and 0091's CHECK verifies it
// against octet_length, a declared size that disagrees with the bytes is refused by the database
// rather than trusted, which is the same posture the feedback uploader takes with Plane's presigned
// policy.
func (r *Repository) InsertPhoto(ctx context.Context, stopID, branchID, contentType, caption, by string, data []byte) (string, error) {
	pool, err := r.db()
	if err != nil {
		return "", err
	}
	var id string
	// The pod scope's seq stamps beside the row, so a photo the desk
	// uploads is visible to a device's pull the moment it lands.
	err = pool.QueryRow(ctx, fmt.Sprintf(syncSeqCTE, podSyncScope)+`
INSERT INTO delivery_pod_photos (stop_id, branch_id, content_type, byte_size, image_bytes, caption, captured_by, seq)
SELECT $1, $2, $3, $4, $5, $6, NULLIF($7,'')::uuid, (SELECT next_value FROM seq) RETURNING id::text`,
		stopID, branchID, contentType, len(data), data, caption, by).Scan(&id)
	return id, err
}

// ListPhotoMeta returns a stop's photo metadata WITHOUT the bytes.
func (r *Repository) ListPhotoMeta(ctx context.Context, stopID string) ([]PhotoMeta, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
SELECT id::text, stop_id::text, content_type, byte_size, caption, captured_at
  FROM delivery_pod_photos WHERE stop_id = $1 ORDER BY captured_at`, stopID)
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	defer rows.Close()
	out := []PhotoMeta{}
	for rows.Next() {
		var p PhotoMeta
		if err := rows.Scan(&p.ID, &p.StopID, &p.ContentType, &p.ByteSize, &p.Caption, &p.CapturedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPhoto reads one photo WITH its bytes and its own branch (so the fence is checked on the photo
// row itself, not on the stop it is assumed to belong to).
func (r *Repository) GetPhoto(ctx context.Context, photoID string) (Photo, string, error) {
	pool, err := r.db()
	if err != nil {
		return Photo{}, "", err
	}
	if !looksLikeUUID(photoID) {
		return Photo{}, "", ErrNotFound
	}
	var p Photo
	var branchID string
	err = pool.QueryRow(ctx, `
SELECT id::text, stop_id::text, content_type, byte_size, caption, captured_at, image_bytes, branch_id::text
  FROM delivery_pod_photos WHERE id = $1`, photoID).
		Scan(&p.ID, &p.StopID, &p.ContentType, &p.ByteSize, &p.Caption, &p.CapturedAt, &p.Bytes, &branchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Photo{}, "", ErrNotFound
	}
	return p, branchID, err
}

// - depot -

// Depot returns a branch's yard coordinate. ok=false means the branch has none, the caller then
// omits the distance estimate rather than substituting a guess.
func (r *Repository) Depot(ctx context.Context, branchID string) (Point, bool, error) {
	pool, err := r.db()
	if err != nil {
		return Point{}, false, err
	}
	var p Point
	err = pool.QueryRow(ctx,
		`SELECT latitude, longitude FROM dispatch_branch_depots WHERE branch_id = $1`, branchID).
		Scan(&p.Lat, &p.Lng)
	if errors.Is(err, pgx.ErrNoRows) {
		return Point{}, false, nil
	}
	if err != nil {
		return Point{}, false, err
	}
	return p, true, nil
}

// BranchName resolves a branch's display name for the manifest header and the public will-call page.
func (r *Repository) BranchName(ctx context.Context, branchID string) (string, error) {
	pool, err := r.db()
	if err != nil {
		return "", err
	}
	var name string
	err = pool.QueryRow(ctx, `SELECT name FROM locations WHERE id = $1`, branchID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// - order display -

// OrderDisplay is the read-only label set the will-call public page shows. See stopSelect's comment:
// display only, never authority.
type OrderDisplay struct {
	OrderNo      string
	CustomerName string
	BranchID     string
	LineCount    int
}

// OrderDisplayFor reads one order's labels.
func (r *Repository) OrderDisplayFor(ctx context.Context, orderID string) (OrderDisplay, error) {
	pool, err := r.db()
	if err != nil {
		return OrderDisplay{}, err
	}
	if !looksLikeUUID(orderID) {
		return OrderDisplay{}, ErrNotFound
	}
	var d OrderDisplay
	err = pool.QueryRow(ctx, `
SELECT o.order_no::text, COALESCE(c.name,''), o.branch_id::text,
       (SELECT count(*) FROM order_lines l WHERE l.order_id = o.id)::int
  FROM orders o LEFT JOIN customers c ON c.id = o.customer_id
 WHERE o.id = $1`, orderID).Scan(&d.OrderNo, &d.CustomerName, &d.BranchID, &d.LineCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrderDisplay{}, ErrNotFound
	}
	return d, err
}

// - will-call -

const pickupCols = `id::text, order_id::text, branch_id::text, status, expires_at,
                    signature_ref, collected_by, confirmed_at, created_at`

func scanPickup(row pgx.Row) (WillCall, error) {
	var w WillCall
	err := row.Scan(&w.ID, &w.OrderID, &w.BranchID, &w.Status, &w.ExpiresAt,
		&w.SignatureRef, &w.CollectedBy, &w.ConfirmedAt, &w.CreatedAt)
	return w, err
}

// InsertPickup mints a token row. The unique partial index (one ACTIVE per order) is what makes a
// duplicate mint a conflict rather than two live QR squares on two tags in the same yard. The
// will_call scope's seq stamps beside the row (0213), so a tag the desk prints is visible to a
// device's pull the moment it lands.
func (r *Repository) InsertPickup(ctx context.Context, orderID, branchID, tokenHash, createdBy string, expires time.Time) (WillCall, error) {
	pool, err := r.db()
	if err != nil {
		return WillCall{}, err
	}
	var id string
	err = pool.QueryRow(ctx, fmt.Sprintf(syncSeqCTE, willCallSyncScope)+`
INSERT INTO will_call_pickups (order_id, branch_id, token_hash, expires_at, created_by, seq)
VALUES ($1, $2, $3, $4, NULLIF($5,'')::uuid, (SELECT next_value FROM seq)) RETURNING id::text`,
		orderID, branchID, tokenHash, expires, createdBy).Scan(&id)
	if err != nil {
		return WillCall{}, err
	}
	w, err := scanPickup(pool.QueryRow(ctx, `SELECT `+pickupCols+` FROM will_call_pickups WHERE id = $1`, id))
	return w, err
}

// PickupByTokenHash resolves a scanned token.
func (r *Repository) PickupByTokenHash(ctx context.Context, hash string) (WillCall, error) {
	pool, err := r.db()
	if err != nil {
		return WillCall{}, err
	}
	w, err := scanPickup(pool.QueryRow(ctx, `SELECT `+pickupCols+` FROM will_call_pickups WHERE token_hash = $1`, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return WillCall{}, ErrTokenNotFound
	}
	return w, err
}

// ListPickups returns an order's pickup tokens, newest first (staff view).
func (r *Repository) ListPickups(ctx context.Context, orderID string) ([]WillCall, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	if !looksLikeUUID(orderID) {
		return nil, ErrNotFound
	}
	rows, err := pool.Query(ctx, `SELECT `+pickupCols+
		` FROM will_call_pickups WHERE order_id = $1 ORDER BY created_at DESC`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list pickups: %w", err)
	}
	defer rows.Close()
	out := []WillCall{}
	for rows.Next() {
		w, err := scanPickup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ConfirmPickup records the collection. Guarded on ACTIVE, so a second scan of the same square cannot
// produce a second confirmation (and the caller learns the token is spent, 410). The will_call
// scope's seq rides the confirmation so the device that minted the tag learns it was used.
func (r *Repository) ConfirmPickup(ctx context.Context, id, sigRef, collectedBy string, at time.Time) (bool, error) {
	pool, err := r.db()
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, willCallSyncScope)+`
UPDATE will_call_pickups
   SET status = 'CONFIRMED', signature_ref = $2, collected_by = $3, confirmed_at = $4,
       seq = (SELECT next_value FROM seq)
 WHERE id = $1 AND status = 'ACTIVE'`, id, sigRef, collectedBy, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RevokePickup kills a live token (a reprinted tag, a cancelled order). The will_call scope's seq
// rides the revoke for the same reason ConfirmPickup's does.
func (r *Repository) RevokePickup(ctx context.Context, id string, at time.Time) (bool, error) {
	pool, err := r.db()
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, willCallSyncScope)+
		`UPDATE will_call_pickups SET status = 'REVOKED', revoked_at = $2, seq = (SELECT next_value FROM seq)
		 WHERE id = $1 AND status = 'ACTIVE'`, id, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// LogPickupEvent records a scan or a confirm against a token. Best-effort by contract: the caller
// never fails a customer's pickup because an analytics row would not write.
func (r *Repository) LogPickupEvent(ctx context.Context, pickupID, event string) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO will_call_pickup_events (pickup_id, event) VALUES ($1, $2)`, pickupID, event)
	return err
}

// looksLikeUUID reports whether s is a canonical 36-char 8-4-4-4-12 hex uuid. Mirrors
// order.looksLikeUUID: a malformed id must be NOT FOUND, never a 500 carrying a driver error, and
// never distinguishable from an id that simply does not exist.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}
