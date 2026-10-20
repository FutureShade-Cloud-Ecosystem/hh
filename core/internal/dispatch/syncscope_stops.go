// syncscope_stops.go is the stops Store (server_wins): the staff-side
// delivery_stops rows the drive-run device carries. The projection is
// the contract's DispatchStop schema, the same shape the board and the
// driver's stop card read, and the D17 contractor stop view (HH-3.6's)
// is untouched: it is a different surface over different claims, and
// the payload proof in this lane's tests pins the sync payload to the
// contract's declared fields, the two money legs absent by type.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// StopsSyncStore serves the stops scope over delivery_stops.
type StopsSyncStore struct {
	pool   *pgxpool.Pool
	orders OrderReader
	link   syncLinker
	role   SyncRoleResolver
	lines  OrderLineReader
}

// NewStopsSyncStore wires the adapter. The order seam is the module's
// own OrderReader (a stop is born against an order, the branch comes
// from the order through the seam, never from the payload); a nil seam
// is legal only where creates will not run (tests).
func NewStopsSyncStore(pool *pgxpool.Pool, orders OrderReader, link syncLinker) *StopsSyncStore {
	return &StopsSyncStore{pool: pool, orders: orders, link: link}
}

// WithDriverRole wires the driver fence's role seam; the loads and
// delivery_stops board adapters embed this store and ride the same
// fence. The composition test in cmd/core pins the wiring.
func (s *StopsSyncStore) WithDriverRole(role SyncRoleResolver) *StopsSyncStore {
	s.role = role
	return s
}

// WithOrderLines wires the seam that answers "what comes off the truck at
// this stop" for the driver arm of the stops scope. Only the drive-run stops
// scope wires it; the board scopes that embed this store's reads do not, so
// a desk caller's stop never carries a load list.
func (s *StopsSyncStore) WithOrderLines(r OrderLineReader) *StopsSyncStore {
	s.lines = r
	return s
}

// DriverRoleWired answers whether the driver fence's role seam is
// wired, the leg the composition test pins.
func (s *StopsSyncStore) DriverRoleWired() bool { return s.role != nil }

// resolveDriverFenceFor resolves the driver fence for one filter over
// this adapter's own seams: the board scopes that embed this store
// resolve the same arm before they address a row, so their writes hold
// to the calling driver exactly as this store's reads do.
//
// Every caller resolves it BEFORE a transaction opens (Create, Write,
// Delete) or with none held (Pull, FindApplied, Load) and passes the
// answer down: the loader runs inside an open transaction and must not
// ask the pool for a second connection (the pool-cycle rule the create
// census keeps).
func (s *StopsSyncStore) resolveDriverFenceFor(ctx context.Context, f sync.ScopeFilter) (driverFence, error) {
	return resolveDriverFence(ctx, s.pool, s.link, s.role, f)
}

// stopsSyncQuery is the board's own stop columns plus the two sync legs
// over the board's own joins.
const stopsSyncQuery = stopSelectColumns + `,
       s.seq, (s.deleted_at IS NOT NULL) AS deleted` + stopSelectFrom

// syncStopRow is one scanned row of the stops sync projection.
type syncStopRow struct {
	stop    Stop
	seq     int64
	deleted bool
}

// scanSyncStop reads the stopSelect targets in their declared order
// then the two sync legs.
func scanSyncStop(row pgx.Row) (syncStopRow, error) {
	var st Stop
	var seq int64
	var deleted bool
	if err := row.Scan(&st.ID, &st.BranchID, &st.OrderID, &st.OrderNo, &st.CustomerName, &st.RunID, &st.RunNo,
		&st.StopNo, &st.ScheduledDate, &st.Status, &st.TimeWindow, &st.ContactName, &st.ContactPhone,
		&st.AddressLine, &st.City, &st.PostalCode, &st.Latitude, &st.Longitude, &st.Instructions,
		&st.GateCode, &st.PODSignedRef, &st.PODReceivedBy, &st.PODAt, &st.FailureReason,
		&st.LoadedAt, &st.EnRouteAt, &st.DeliveredAt, &st.HandoffAt, &st.PhotoCount, &st.CreatedAt,
		&st.WeightKg, &st.VolumeM3, &st.PalletPositions, &st.ValueCents, &st.FreightCents,
		&st.FreightIsOverride, &st.LoadSeq, &st.LoadCount,
		&st.RequestedDate, &st.CommitState, &st.CommittedAt, &st.ReopenedAt, &st.ReopenReason,
		&seq, &deleted); err != nil {
		return syncStopRow{}, err
	}
	return syncStopRow{stop: st, seq: seq, deleted: deleted}, nil
}

// driverLoadList answers "what comes off the truck at this stop" as the
// wire list the contract's DispatchDriverLine declares, or nil when the
// caller is not a linked driver, the seam is not wired, or the read fails
// or answers nothing (the device then shows "not recorded", never an empty
// truck: the same posture Service.driverStopLines holds). byOrder caches
// one read per order across a page.
//
// NO MONEY CAN ARRIVE HERE: DriverLine has no money member and the yard's
// bin (PickFeedLine.Location) is dropped by the conversion.
func (s *StopsSyncStore) driverLoadList(ctx context.Context, d driverFence, orderID string, byOrder map[string][]any) []any {
	if s.lines == nil || !d.driver || d.driverID == "" || orderID == "" {
		return nil
	}
	if cached, seen := byOrder[orderID]; seen {
		return cached
	}
	var out []any
	if raw, err := s.lines.OrderPickLines(ctx, orderID); err == nil && len(raw) > 0 {
		lines := make([]DriverLine, 0, len(raw))
		for _, l := range raw {
			lines = append(lines, DriverLine{LineNo: l.LineNo, SKU: l.SKU, Description: l.Description, Qty: l.QtyOrdered, UOM: l.UOM})
		}
		if b, err := json.Marshal(lines); err == nil {
			_ = json.Unmarshal(b, &out)
		}
	}
	byOrder[orderID] = out
	return out
}

// stateOf is stopStateOf plus the driver arm's load list. Callers hold no
// transaction (the line read goes through another module's pool).
func (s *StopsSyncStore) stateOf(ctx context.Context, d driverFence, r syncStopRow) (sync.RowState, error) {
	rs, err := stopStateOf(r)
	if err != nil || r.deleted {
		return rs, err
	}
	if lines := s.driverLoadList(ctx, d, r.stop.OrderID, map[string][]any{}); lines != nil {
		rs.Data["lines"] = lines
	}
	return rs, nil
}

// stopsSyncRowByID reads the full sync projection for one stop id on
// the given executor, the read-back arm of the write paths.
func stopsSyncRowByID(ctx context.Context, q runner, id string) (syncStopRow, error) {
	return scanSyncStop(q.QueryRow(ctx, stopsSyncQuery+` WHERE s.id = $1::uuid`, id))
}

// stopStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func stopStateOf(r syncStopRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.stop.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := projection(r.stop)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// stopScopePredicate is the WHERE predicate every stops adapter query
// runs under; a tenant filter fails closed (stops is an org scope). A
// driver caller carries the driver arm beside the org through the
// delivery_runs join stopSelectFrom already carries: only the stops of
// the runs assigned to them, so an unassigned stop (no run yet) is not
// theirs either, and nothing at all when their login links to no
// driver record.
func stopScopePredicate(f sync.ScopeFilter, d driverFence) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	switch {
	case !d.driver:
		return "s.org_id = $1::uuid", []any{f.OrgID}, true
	case d.driverID == "":
		return "FALSE", nil, true
	default:
		return "s.org_id = $1::uuid AND r.driver_id = $2::uuid", []any{f.OrgID, d.driverID}, true
	}
}

// Pull returns the page of stop changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// predicate.
func (s *StopsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	d, err := s.resolveDriverFenceFor(ctx, f)
	if err != nil {
		return nil, nil, err
	}
	where, args, ok := stopScopePredicate(f, d)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := stopsSyncQuery + `
		WHERE ` + where + ` AND s.seq IS NOT NULL AND s.seq > ` + syncPH(len(args)+1) + `
		ORDER BY s.seq ASC
		LIMIT ` + syncPH(len(args)+2)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("dispatch: stops sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	byOrder := map[string][]any{}
	for rows.Next() {
		r, err := scanSyncStop(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("dispatch: stops sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.stop.ID, Seq: r.seq})
			continue
		}
		data, err := projection(r.stop)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.stop.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("dispatch: stops sync pull: %w", err)
	}
	// The load lists are read after the page's cursor closes: the order
	// module's read takes its own connection, and a held cursor must not
	// wait on a second acquisition from the same pool.
	rows.Close()
	for i := range items {
		if lines := s.driverLoadList(ctx, d, fmt.Sprint(items[i].Data["order_id"]), byOrder); lines != nil {
			items[i].Data["lines"] = lines
		}
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied, the
// column holding the latest live key and a tombstone keeping it (the
// design note in syncscope.go's header).
func (s *StopsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	d, err := s.resolveDriverFenceFor(ctx, f)
	if err != nil {
		return sync.RowState{}, false, err
	}
	where, args, ok := stopScopePredicate(f, d)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := stopsSyncQuery + `
		WHERE ` + where + ` AND s.idempotency_key = ` + syncPH(len(args)+1) + `::uuid
		LIMIT 1`
	args = append(args, key)
	r, err := scanSyncStop(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: stops sync find applied: %w", err)
	}
	rs, err := s.stateOf(ctx, d, r)
	return rs, true, err
}

// Load reads one stop by id under the org predicate.
func (s *StopsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	d, err := s.resolveDriverFenceFor(ctx, f)
	if err != nil {
		return sync.RowState{}, false, err
	}
	rs, found, err := s.load(ctx, s.pool, f, d, id)
	if err != nil || !found {
		return rs, found, err
	}
	// The driver arm's load list, read outside any transaction.
	if lines := s.driverLoadList(ctx, d, fmt.Sprint(rs.Data["order_id"]), map[string][]any{}); lines != nil {
		rs.Data["lines"] = lines
	}
	return rs, true, nil
}

// stopsQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type stopsQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *StopsSyncStore) load(ctx context.Context, db stopsQuerier, f sync.ScopeFilter, d driverFence, id string) (sync.RowState, bool, error) {
	where, args, ok := stopScopePredicate(f, d)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := stopsSyncQuery + `
		WHERE s.id = ` + syncPH(len(args)+1) + `::uuid AND ` + where
	args = append(args, id)
	r, err := scanSyncStop(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: stops sync load: %w", err)
	}
	rs, err := stopStateOf(r)
	return rs, true, err
}

// stopCreateFields is the field set a pushed create may declare on a
// stop: the order it delivers for, the date, the optional address legs
// the device copies off the paperwork, and the run it joins. The
// branch is never in the set: it is copied from the order through the
// seam exactly as Schedule copies it.
func stopCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "order_id", "scheduled_date", "run_id",
			"time_window", "contact_name", "contact_phone",
			"address_line", "city", "postal_code", "instructions", "gate_code":
		default:
			return fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// stopWriteFields is the field set a pushed update may declare on a
// stop: the status move and the failure reason that rides a FAILED
// move. DELIVERED is not reachable through sync (it is reachable only
// through the POD door, where the evidence and the status are one
// write); a payload that asks for it is refused with the reason naming
// the door, the same sentence AdvanceStop answers.
func stopWriteFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "status", "failure_reason":
		default:
			return fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// Create inserts one stop from a pushed create: the order resolves
// through the module's own seam (the branch comes from it), the order
// must be schedulable (the same status rule Schedule states), no live
// stop may already stand for the order, and the optional run must sit
// at the same branch on the same date. A run a stop may join through
// sync is an open run of the day (PLANNED, or DISPATCHED for the
// over-the-radio hand-off to a truck already out); a run whose day is
// filed (COMPLETED, CANCELLED) refuses new stops. The row stamps the
// caller's org, a freshly allocated seq and the idempotency key.
func (s *StopsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	d, err := s.resolveDriverFenceFor(ctx, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := stopScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := stopCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	// A held driver's writes land on their own runs alone: a stop that
	// names no run belongs to nobody (the driver could never read it
	// back), and a colleague's run is refused below as absent.
	if heldRunID, _ := fields["run_id"].(string); d.driver && heldRunID == "" {
		return sync.RowState{}, refused(fmt.Errorf("a driver schedules stops onto their own run"))
	}
	orderID, _ := fields["order_id"].(string)
	if !looksLikeUUID(orderID) {
		return sync.RowState{}, fmt.Errorf("dispatch: order_id must be a uuid: %w", sync.ErrOpRefused)
	}
	if s.orders == nil {
		return sync.RowState{}, fmt.Errorf("dispatch: order seam not configured: %w", sync.ErrOpRefused)
	}
	brief, err := s.orders.Brief(ctx, orderID)
	if err != nil {
		// Schedule's own posture: a missing or garbage order id never
		// distinguishes itself, and neither does a foreign one below,
		// so all three answer the same not-found.
		return sync.RowState{}, sync.ErrRowNotFound
	}
	// THE ORG FENCE RUNS BEFORE ANY STATUS SENTENCE: a foreign order's
	// status is not the caller's fact to learn, and the refusal must
	// never distinguish a foreign order from a missing one.
	if !branchInOrg(ctx, s.pool, brief.BranchID, f.OrgID) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	switch brief.Status {
	case "DRAFT":
		return sync.RowState{}, refused(fmt.Errorf("confirm the order before scheduling a delivery for it"))
	case "CANCELLED", "CLOSED":
		return sync.RowState{}, refused(fmt.Errorf("order is %s and cannot be scheduled", brief.Status))
	}
	date := strings.TrimSpace(fmt.Sprint(fields["scheduled_date"]))
	if date == "" || !validDate(date) {
		return sync.RowState{}, fmt.Errorf("dispatch: scheduled_date must be YYYY-MM-DD: %w", sync.ErrOpRefused)
	}
	runID, _ := fields["run_id"].(string)
	var runBranch, runDate, runStatus, runDriver string
	if runID != "" {
		if !looksLikeUUID(runID) {
			return sync.RowState{}, fmt.Errorf("dispatch: run_id must be a uuid: %w", sync.ErrOpRefused)
		}
		if err := s.pool.QueryRow(ctx,
			`SELECT branch_id::text, to_char(scheduled_date,'YYYY-MM-DD'), status, COALESCE(driver_id::text,'')
			 FROM delivery_runs WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL`,
			runID, f.OrgID).Scan(&runBranch, &runDate, &runStatus, &runDriver); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return sync.RowState{}, sync.ErrRowNotFound
			}
			return sync.RowState{}, fmt.Errorf("dispatch: stops sync create run read: %w", err)
		}
		// The fence: a colleague's run, or any run to a driver login
		// with no driver record, is the same as an absent one.
		if d.driver && (d.driverID == "" || runDriver != d.driverID) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		if runBranch != brief.BranchID {
			return sync.RowState{}, refused(fmt.Errorf("that run belongs to another branch"))
		}
		if runDate != date {
			return sync.RowState{}, refused(fmt.Errorf("the stop is scheduled for %s and the run is %s", date, runDate))
		}
		switch runStatus {
		case RunPlanned, RunDispatched:
		default:
			return sync.RowState{}, refused(fmt.Errorf("stops cannot be added to a %s run", runStatus))
		}
	}
	id, err := syncCreateID(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	actor := syncActor(ctx, s.link, f.Actor)
	str := func(k string) string { v, _ := fields[k].(string); return v }

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin stops sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// One live stop per order (the 0090 partial index's rule), refused
	// here with the module's own sentence rather than trusted to the
	// constraint's name.
	var live string
	if err := tx.QueryRow(ctx,
		`SELECT to_char(scheduled_date,'YYYY-MM-DD') FROM delivery_stops
		 WHERE order_id = $1::uuid AND status NOT IN ('CANCELLED','FAILED') LIMIT 1`,
		brief.ID).Scan(&live); err == nil {
		return sync.RowState{}, refused(fmt.Errorf("this order already has a delivery scheduled for %s", live))
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, fmt.Errorf("dispatch: stops sync create live read: %w", err)
	}
	seq, err := allocateSyncSeqTx(ctx, tx, stopsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	stopNo := 0
	if runID != "" {
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(max(stop_no),0)::int + 1 FROM delivery_stops WHERE run_id = $1::uuid`,
			runID).Scan(&stopNo); err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: stops sync create stop no: %w", err)
		}
	}
	var rowID string
	err = tx.QueryRow(ctx, `
		INSERT INTO delivery_stops (id, branch_id, order_id, run_id, stop_no, scheduled_date,
		                            time_window, contact_name, contact_phone, address_line, city,
		                            postal_code, instructions, gate_code, created_by, org_id, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, NULLIF($4,'')::uuid,
		        $5, $6::date, $7, $8, $9, $10, $11, $12, $13, $18, NULLIF($14,'')::uuid, $15::uuid, $16, $17::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING id::text`,
		id, brief.BranchID, brief.ID, runID, stopNo, date,
		str("time_window"), str("contact_name"), str("contact_phone"),
		str("address_line"), str("city"), str("postal_code"), str("instructions"),
		actor, f.OrgID, seq, key, str("gate_code")).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, own, _ := s.load(ctx, tx, f, d, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// The live-stop index lost the race the pre-check narrows:
			// one live delivery per order is the caller's own fact,
			// answered as a refusal, never as an outage.
			return sync.RowState{}, refused(fmt.Errorf("this order already has a live delivery stop"))
		}
		return sync.RowState{}, fmt.Errorf("dispatch: stops sync create insert: %w", err)
	}
	r, err := stopsSyncRowByID(ctx, tx, rowID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: stops sync create read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit stops sync create: %w", err)
	}
	return s.stateOf(ctx, d, r)
}

// Write applies exactly the declared fields to one stop under the
// module's own rules, on a FOR UPDATE-locked pre-image: the status move
// runs the same matrix AdvanceStop runs with the same per-move gates
// (LOADED needs a run, EN_ROUTE needs a dispatched run, FAILED needs a
// reason, CANCELLED is the pre-departure withdrawal, DELIVERED is the
// pod door's alone), the per-status timestamps stamp in the same
// statement, and the row draws a fresh seq beside the write.
func (s *StopsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	d, err := s.resolveDriverFenceFor(ctx, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := stopScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := stopWriteFields(fields); err != nil {
		return sync.RowState{}, err
	}
	status, hasStatus := fields["status"].(string)
	if hasStatus && !validStopStatus(status) {
		return sync.RowState{}, fmt.Errorf("dispatch: unknown stop status %q: %w", status, sync.ErrOpRefused)
	}
	reason, hasReason := fields["failure_reason"].(string)
	if !hasStatus && !hasReason {
		return sync.RowState{}, refused(fmt.Errorf("an update that moves nothing refuses"))
	}
	// A failed stop without a reason is a state 0090 refuses to hold:
	// the correction may change the sentence, never erase it.
	if hasReason && strings.TrimSpace(reason) == "" {
		return sync.RowState{}, refused(fmt.Errorf("a failed stop needs a reason"))
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin stops sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, runID, fromReason string
	preArm, preArg := d.leg(stopDriverCol, 3)
	if err := tx.QueryRow(ctx,
		`SELECT status, COALESCE(run_id::text,''), failure_reason FROM delivery_stops
		 WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL`+preArm+` FOR UPDATE`,
		id, f.OrgID, preArg).Scan(&from, &runID, &fromReason); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("dispatch: lock stop %q for sync write: %w", id, err)
	}
	if !hasStatus && hasReason && from != StopFailed {
		// A reason correction is the one non-status write the scope
		// admits, and only on a failed stop: anywhere else it would be
		// an applied write that changed nothing.
		return sync.RowState{}, refused(fmt.Errorf("a %s stop carries no failure reason to correct", from))
	}
	if hasStatus && from != status {
		if status == StopDelivered {
			return sync.RowState{}, refused(ErrPODRequired)
		}
		if !stopCanTransition(from, status) {
			return sync.RowState{}, refused(ErrIllegalTransition)
		}
		switch status {
		case StopLoaded:
			if runID == "" {
				return sync.RowState{}, refused(ErrStopUnassigned)
			}
		case StopEnRoute:
			var runStatus string
			if runID == "" {
				return sync.RowState{}, refused(ErrStopUnassigned)
			}
			if err := tx.QueryRow(ctx,
				`SELECT status FROM delivery_runs WHERE id = $1::uuid`, runID).Scan(&runStatus); err != nil {
				return sync.RowState{}, refused(ErrStopUnassigned)
			}
			if runStatus != RunDispatched {
				return sync.RowState{}, refused(ErrRunNotDispatched)
			}
		case StopFailed:
			if strings.TrimSpace(reason) == "" {
				return sync.RowState{}, refused(fmt.Errorf("a failed stop needs a reason"))
			}
		}
	}
	if (!hasStatus || from == status) && (!hasReason || strings.TrimSpace(reason) == fromReason) {
		// The declared write is already the row's standing fact (a
		// superseded replay of one constant reason correction meets
		// this arm): answer applied with the row as it stands,
		// stamping nothing.
		r, err := stopsSyncRowByID(ctx, tx, id)
		if err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: stops sync write read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: commit stops sync write: %w", err)
		}
		return s.stateOf(ctx, d, r)
	}
	to := from
	if hasStatus {
		to = status
	}
	at := time.Now().UTC()
	seq, err := allocateSyncSeqTx(ctx, tx, stopsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	var updated bool
	setArm, setArg := d.leg(stopDriverCol, 9)
	err = tx.QueryRow(ctx, `
		UPDATE delivery_stops SET
			status          = $3,
			loaded_at       = CASE WHEN $3 = 'LOADED'    THEN $4 ELSE loaded_at END,
			en_route_at     = CASE WHEN $3 = 'EN_ROUTE'  THEN $4 ELSE en_route_at END,
			delivered_at    = CASE WHEN $3 = 'DELIVERED' THEN $4 ELSE delivered_at END,
			failure_reason  = CASE WHEN $3 = 'FAILED'    THEN $5 ELSE failure_reason END,
			updated_at      = now(),
			seq             = $6,
			idempotency_key = $7::uuid
		WHERE id = $1::uuid AND org_id = $2::uuid AND status = $8`+setArm+`
		RETURNING (deleted_at IS NOT NULL)`,
		id, f.OrgID, to, at, reason, seq, key, from, setArg).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: stops sync write %q: %w", id, err)
	}
	r, err := stopsSyncRowByID(ctx, tx, id)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: stops sync write read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit stops sync write: %w", err)
	}
	return s.stateOf(ctx, d, r)
}

// Delete tombstones one stop: deleted_at set, the row retained, a
// fresh seq, the live row's idempotency key kept (syncscope.go's
// header).
func (s *StopsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	d, err := s.resolveDriverFenceFor(ctx, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := stopScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin stops sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSyncSeqTx(ctx, tx, stopsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	delArm, delArg := d.leg(stopDriverCol, 4)
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE delivery_stops SET
			deleted_at = now(),
			updated_at = now(),
			seq        = $3
		WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL`+delArm+`
		RETURNING (deleted_at IS NOT NULL)`, id, f.OrgID, seq, delArg).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: stops sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit stops sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}

// validStopStatus pins the stop status vocabulary (0090's CHECK set).
func validStopStatus(s string) bool {
	switch s {
	case StopReady, StopLoaded, StopEnRoute, StopDelivered, StopFailed, StopCancelled:
		return true
	}
	return false
}
