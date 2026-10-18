package dispatch

// repository_w3.go, the Wave 3 persistence: the pick feed reads (item 1), the MTO/POD-attach writes
// (DIB-63), the reschedule writes (DIB-382) and the scan trail (DIB-75).
//
// Kept in its OWN FILE rather than appended to repository.go so the Wave 3 surface is legible as a
// unit at the union merge and the shipped Wave 2 queries are untouched. The two rules repository.go's
// header states apply here verbatim: no authority lives in this file, and every status/date write is
// guarded on its pre-image and reports whether it actually changed a row.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// - the pick feed -

// pickFeedSelect is the board row a pick ticket is built from. Like stopSelect it joins orders and
// customers for DISPLAY ONLY; authority is delivery_stops.branch_id, as everywhere else in 0090.
const pickFeedSelect = `
SELECT s.id::text, s.branch_id::text, s.order_id::text, COALESCE(o.order_no::text,''),
       COALESCE(c.name,''), COALESCE(o.fulfillment,''), to_char(s.scheduled_date,'YYYY-MM-DD'),
       s.status, COALESCE(s.run_id::text,''), COALESCE(r.run_no,''), COALESCE(r.status,''),
       r.departs_at,
       CASE
           WHEN s.run_id IS NULL THEN 0
           ELSE COALESCE((
               SELECT COUNT(*)::int
                 FROM delivery_stops s2
                WHERE s2.run_id = s.run_id
                  AND s2.status <> 'CANCELLED'
                  AND (s2.stop_no > s.stop_no OR (s2.stop_no = s.stop_no AND s2.id >= s.id))
           ), 1)
       END,
       s.mto_class, s.time_window
  FROM delivery_stops s
  LEFT JOIN orders        o ON o.id = s.order_id
  LEFT JOIN customers     c ON c.id = o.customer_id
  LEFT JOIN delivery_runs r ON r.id = s.run_id`

// pickFeedPendingWhere is what "still awaiting picking" MEANS for a delivery, factored out of the
// list read so its count twin below is the same question rather than a second one that agrees today.
// A card whose number and whose list are computed from two hand-written predicates is a card that
// eventually says something the list does not.
const pickFeedPendingWhere = `
 WHERE s.status = 'READY' AND (s.run_id IS NULL OR r.status = 'PLANNED')`

// pickFeedCountFrom is pickFeedSelect's FROM without the display joins. The delivery_runs join stays
// because pickFeedPendingWhere reads r.status; customers and orders are display-only and a count has
// nothing to display.
const pickFeedCountFrom = `
SELECT count(*)::int
  FROM delivery_stops s
  LEFT JOIN delivery_runs r ON r.id = s.run_id`

func scanPickFeedStop(row pgx.Row) (pickFeedStop, error) {
	var p pickFeedStop
	err := row.Scan(&p.StopID, &p.BranchID, &p.OrderID, &p.OrderNo, &p.CustomerName, &p.Fulfillment,
		&p.ScheduledDate, &p.StopStatus, &p.RunID, &p.RunNo, &p.RunStatus,
		&p.RunDepartsAt, &p.LoadPosition, &p.MTOClass, &p.TimeWindow)
	return p, err
}

// PickFeedStopForOrder reads the LIVE stop for one order (not CANCELLED, not FAILED). ErrNotFound
// when the order has none, the composition root maps that onto tasks.ErrUnknownOrder, which is how
// the yard tells "no such pick" apart from "the feed is broken".
//
// The uq_delivery_stops_live_order partial index (0090) guarantees at most one such row exists, so
// there is nothing to disambiguate; ORDER BY created_at DESC is belt-and-braces against a future
// relaxation of that index rather than a live concern.
func (r *Repository) PickFeedStopForOrder(ctx context.Context, orderID string) (pickFeedStop, error) {
	pool, err := r.db()
	if err != nil {
		return pickFeedStop{}, err
	}
	if !looksLikeUUID(orderID) {
		return pickFeedStop{}, ErrNotFound
	}
	p, err := scanPickFeedStop(pool.QueryRow(ctx, pickFeedSelect+`
 WHERE s.order_id = $1 AND s.status NOT IN ('CANCELLED','FAILED')
 ORDER BY s.created_at DESC LIMIT 1`, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return pickFeedStop{}, ErrNotFound
	}
	return p, err
}

// PickFeedPendingStops reads the stops still awaiting picking: READY, and either unassigned or on a
// PLANNED run. Ordered by date then run/stop position so the queue reads the way the day runs.
//
// An empty branchID widens to every branch, matching StubPickSource's behaviour so the swap is
// behaviour-preserving at the seam. See picksource.go's header on fencing.
func (r *Repository) PickFeedPendingStops(ctx context.Context, branchID string, limit int) ([]pickFeedStop, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	if branchID != "" && !looksLikeUUID(branchID) {
		return []pickFeedStop{}, nil // an unparseable branch names no branch; it is not an error
	}
	q := pickFeedSelect + pickFeedPendingWhere
	args := []any{limit}
	if branchID != "" {
		q += ` AND s.branch_id = $2`
		args = append(args, branchID)
	}
	q += ` ORDER BY s.scheduled_date, s.stop_no, s.created_at LIMIT $1`

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pickFeedStop{}
	for rows.Next() {
		p, err := scanPickFeedStop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// - the pickup source (the second read behind the one feed) -

// pickupFeedSelect is the PICKUP row a pick ticket is built from. It reads ORDERS, not delivery_stops,
// and that is the entire point of the file's second query.
//
// WHY THIS IS ITS OWN QUERY AND NOT AN `OR` ON pickFeedSelect. A customer pickup has no delivery stop,
// no run and no truck; every column pickFeedSelect projects off `s` would be NULL or invented for it.
// Widening that query would have made every delivery row pay for an outer join it does not need, and
// it would have made the two sources' filters, one over stop status, one over order status, share a
// WHERE clause where they have nothing in common. The load board's pickup card already established
// the ruling this follows: a pickup is not derivable from delivery rows, so it gets its own read.
//
// THE ANTI-JOIN IS THE UNION'S DE-DUPLICATION, and it lives HERE rather than in Go so the two reads
// are disjoint at the database. An order that IS on a live stop belongs to the delivery source; this
// query must never also claim it, or one order would produce two tickets for one pull.
// pickupFeedWhere is the anti-join and the fulfilment filter, the half of the query that DEFINES the
// pickup population. It is a constant of its own so the list read, the count read and the D-110 stage
// split are literally the same predicate rather than three copies of it.
const pickupFeedWhere = `
 WHERE o.fulfillment IN ('PICKUP','WILL_CALL')
   AND NOT EXISTS (
         SELECT 1 FROM delivery_stops s
          WHERE s.order_id = o.id AND s.status NOT IN ('CANCELLED','FAILED'))`

const pickupFeedSelect = `
SELECT o.id::text, COALESCE(o.order_no::text,''), o.branch_id::text, COALESCE(c.name,''),
       COALESCE(o.fulfillment,''), COALESCE(to_char(o.requested_date,'YYYY-MM-DD'),''), o.status
  FROM orders    o
  LEFT JOIN customers c ON c.id = o.customer_id` + pickupFeedWhere

func scanPickFeedPickup(row pgx.Row) (pickFeedPickup, error) {
	var p pickFeedPickup
	err := row.Scan(&p.OrderID, &p.OrderNo, &p.BranchID, &p.CustomerName, &p.Fulfillment,
		&p.PromisedDate, &p.OrderStatus)
	return p, err
}

// pickupAwaitingCollection is the order-status window a pickup ticket exists in.
//
// DRAFT is excluded because nothing is committed yet, MintWillCall refuses a draft for the same
// reason. Everything from FULFILLED on is excluded because the material has left the yard: the
// collection is the thing that ends the ticket, exactly as delivery ends a stop's. PARTIALLY_FULFILLED
// is IN: some of the order is still on the ground and somebody still has to pull it.
const pickupAwaitingCollection = ` AND o.status IN ('CONFIRMED','PICKING','READY','PARTIALLY_FULFILLED')`

// PickFeedPickupForOrder reads the pickup row for one order, or ErrNotFound when the order is not a
// pickup awaiting collection. The single-order twin of PickFeedStopForOrder.
func (r *Repository) PickFeedPickupForOrder(ctx context.Context, orderID string) (pickFeedPickup, error) {
	pool, err := r.db()
	if err != nil {
		return pickFeedPickup{}, err
	}
	if !looksLikeUUID(orderID) {
		return pickFeedPickup{}, ErrNotFound
	}
	p, err := scanPickFeedPickup(pool.QueryRow(ctx,
		pickupFeedSelect+pickupAwaitingCollection+` AND o.id = $1 LIMIT 1`, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return pickFeedPickup{}, ErrNotFound
	}
	return p, err
}

// PickFeedPendingPickups reads the pickup-bound orders still awaiting collection at a branch, oldest
// promised date first, the same shape and the same ordering discipline as PickFeedPendingStops, so
// the feed can merge the two sorted lists without re-sorting either.
//
// An order with NO promised date sorts LAST, not first. NULLS LAST is deliberate: a missing date is an
// absent urgency signal, and sorting it to the top of a picker's queue would let a blank field
// outrank a customer who is actually waiting.
//
// An empty branchID widens to every branch, matching PickFeedPendingStops. See picksource.go's header
// on fencing, this read is not branch-fenced either, and is reachable only from the internal feed.
func (r *Repository) PickFeedPendingPickups(ctx context.Context, branchID string, limit int) ([]pickFeedPickup, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	if branchID != "" && !looksLikeUUID(branchID) {
		return []pickFeedPickup{}, nil // an unparseable branch names no branch; it is not an error
	}
	q := pickupFeedSelect + pickupAwaitingCollection
	args := []any{limit}
	if branchID != "" {
		q += ` AND o.branch_id = $2`
		args = append(args, branchID)
	}
	q += ` ORDER BY o.requested_date ASC NULLS LAST, o.created_at LIMIT $1`

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pickFeedPickup{}
	for rows.Next() {
		p, err := scanPickFeedPickup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// - DIB-63 · MTO classification -

// SetStopMTO writes a stop's made-to-order classification and note. Guarded on the stop still being
// pre-departure: the service refuses first with a sentence, and `AND status IN (…)` here is what makes
// the refusal atomic against a driver pressing "en route" in the same second.
func (r *Repository) SetStopMTO(ctx context.Context, stopID, class, note string, weightKg *float64) (bool, error) {
	pool, err := r.db()
	if err != nil {
		return false, err
	}
	if !looksLikeUUID(stopID) {
		return false, ErrNotFound
	}
	tag, err := pool.Exec(ctx, `
UPDATE delivery_stops
   SET mto_class = $2, mto_note = $3, weight_kg = $4, updated_at = now()
 WHERE id = $1 AND status IN ('READY','LOADED')`, stopID, class, note, weightKg)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// StopMTO reads the MTO trio for one stop (the manifest and the stop drawer both need it, and it is
// not on the frozen Stop projection Wave 2 shipped).
func (r *Repository) StopMTO(ctx context.Context, stopID string) (class, note string, weightKg *float64, err error) {
	pool, err := r.db()
	if err != nil {
		return "", "", nil, err
	}
	if !looksLikeUUID(stopID) {
		return "", "", nil, ErrNotFound
	}
	err = pool.QueryRow(ctx,
		`SELECT mto_class, mto_note, weight_kg::float8 FROM delivery_stops WHERE id = $1`, stopID).
		Scan(&class, &note, &weightKg)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, ErrNotFound
	}
	return class, note, weightKg, err
}

// ManifestStopMTO reads the MTO trio for every stop on a run, keyed by stop id. One query, because
// the manifest renders N stops and N+1 reads to print a sheet is how a print button starts timing out.
func (r *Repository) ManifestStopMTO(ctx context.Context, runID string) (map[string]StopMTO, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	if !looksLikeUUID(runID) {
		return nil, ErrNotFound
	}
	rows, err := pool.Query(ctx,
		`SELECT id::text, mto_class, mto_note, weight_kg::float8 FROM delivery_stops WHERE run_id = $1`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]StopMTO{}
	for rows.Next() {
		var id string
		var m StopMTO
		if err := rows.Scan(&id, &m.Class, &m.Note, &m.WeightKg); err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, rows.Err()
}

// - DIB-63 · POD photo attachment -

// AttachPODPhotos folds every not-yet-attached photo on a stop into the sealed POD record, stamping
// the attestation ref it evidences. Returns how many were attached.
//
// IDEMPOTENT BY CONSTRUCTION: `WHERE pod_attached_at IS NULL` means a re-run attaches only what is
// new, so a driver who uploads a photo after the signature (the dead-signal retry the module was
// built to tolerate) gets it attached by the next call rather than either double-stamping the old
// ones or being refused.
func (r *Repository) AttachPODPhotos(ctx context.Context, stopID, sigRef string, at time.Time) (int, error) {
	pool, err := r.db()
	if err != nil {
		return 0, err
	}
	if !looksLikeUUID(stopID) {
		return 0, ErrNotFound
	}
	if strings.TrimSpace(sigRef) == "" {
		// The 0107 CHECK would refuse this anyway; refusing here names the bug instead of the constraint.
		return 0, fmt.Errorf("dispatch: cannot attach photos to a blank attestation ref")
	}
	tag, err := pool.Exec(ctx, fmt.Sprintf(syncSeqCTE, podSyncScope)+`
UPDATE delivery_pod_photos
   SET pod_attached_at = $3, pod_signature_ref = $2, seq = (SELECT next_value FROM seq)
 WHERE stop_id = $1 AND pod_attached_at IS NULL`, stopID, sigRef, at)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// - DIB-382 · reschedule -

// insertRescheduleTx appends one row to the append-only reschedule trail (0107) ON THE CALLER'S
// TRANSACTION. It is unexported precisely so no future caller can write a trail row outside the move
// it describes, see MoveStopDate's comment on why the two commit together.
func insertRescheduleTx(ctx context.Context, tx pgx.Tx, in Reschedule) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
INSERT INTO dispatch_reschedules (branch_id, entity_type, entity_id, from_date, to_date, reason,
                                  stops_moved, actor_id)
VALUES ($1, $2, $3, $4::date, $5::date, $6, $7, NULLIF($8,'')::uuid)
RETURNING id::text`,
		in.BranchID, in.EntityType, in.EntityID, in.FromDate, in.ToDate, in.Reason,
		in.StopsMoved, in.ActorID).Scan(&id)
	return id, err
}

// MoveStopDate rewrites one stop's delivery date AND writes the trail row that explains it, in ONE
// transaction.
//
// THE MOVE AND ITS REASON COMMIT TOGETHER. A date that changed with no recorded reason is exactly the
// hole DIB-382 exists to close, and two statements outside a transaction is how that hole reappears
// the first time the second one fails. The UPDATE is guarded on the pre-image date AND on the stop
// still being READY, so a stop that left the yard between the service's check and this write keeps
// its date and gets no trail row either.
//
// ok == false means the guard bit: nothing moved and nothing was recorded.
func (r *Repository) MoveStopDate(ctx context.Context, stopID, fromDate, toDate string, trail Reschedule) (rescheduleID string, ok bool, err error) {
	pool, err := r.db()
	if err != nil {
		return "", false, err
	}
	if !looksLikeUUID(stopID) {
		return "", false, ErrNotFound
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The stops stream's seq rides the move (the shared delivery_stops
	// feed, syncscope.go's design note), so a desk reschedule is
	// visible to a device's pull the moment it lands.
	tag, err := tx.Exec(ctx, fmt.Sprintf(syncSeqCTE, stopsSyncScope)+`
UPDATE delivery_stops
   SET scheduled_date = $3::date, updated_at = now(), seq = (SELECT next_value FROM seq)
 WHERE id = $1 AND scheduled_date = $2::date AND status = 'READY'`, stopID, fromDate, toDate)
	if err != nil {
		return "", false, err
	}
	if tag.RowsAffected() != 1 {
		return "", false, nil
	}
	trail.StopsMoved = 1
	id, err := insertRescheduleTx(ctx, tx, trail)
	if err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// MoveRunDate rewrites a PLANNED run's date, every READY stop on it, and the trail row, in ONE
// transaction.
//
// THE RUN AND ITS STOPS MUST MOVE TOGETHER. AssignStop refuses a stop whose date disagrees with its
// run's (0090's service guard), so a half-applied move would leave a run nobody can add a stop to and
// stops nobody can explain. Returns how many stops moved with it.
func (r *Repository) MoveRunDate(ctx context.Context, runID, fromDate, toDate string, trail Reschedule) (rescheduleID string, moved int, ok bool, err error) {
	pool, err := r.db()
	if err != nil {
		return "", 0, false, err
	}
	if !looksLikeUUID(runID) {
		return "", 0, false, ErrNotFound
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", 0, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
UPDATE delivery_runs SET scheduled_date = $3::date, updated_at = now()
 WHERE id = $1 AND scheduled_date = $2::date AND status = 'PLANNED'`, runID, fromDate, toDate)
	if err != nil {
		return "", 0, false, err
	}
	if tag.RowsAffected() != 1 {
		return "", 0, false, nil // someone dispatched or already moved it; the caller reports a conflict
	}
	stopTag, err := tx.Exec(ctx, `
UPDATE delivery_stops SET scheduled_date = $2::date, updated_at = now()
 WHERE run_id = $1 AND status = 'READY'`, runID, toDate)
	if err != nil {
		return "", 0, false, err
	}
	trail.StopsMoved = int(stopTag.RowsAffected())
	id, err := insertRescheduleTx(ctx, tx, trail)
	if err != nil {
		return "", 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", 0, false, err
	}
	return id, trail.StopsMoved, true, nil
}

// ListReschedules reads one entity's reschedule trail, newest first.
func (r *Repository) ListReschedules(ctx context.Context, entityType, entityID string) ([]Reschedule, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	if !looksLikeUUID(entityID) {
		return nil, ErrNotFound
	}
	rows, err := pool.Query(ctx, `
SELECT id::text, branch_id::text, entity_type, entity_id::text,
       to_char(from_date,'YYYY-MM-DD'), to_char(to_date,'YYYY-MM-DD'), reason, stops_moved,
       COALESCE(actor_id::text,''), created_at
  FROM dispatch_reschedules
 WHERE entity_type = $1 AND entity_id = $2
 ORDER BY created_at DESC`, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reschedule{}
	for rows.Next() {
		var x Reschedule
		if err := rows.Scan(&x.ID, &x.BranchID, &x.EntityType, &x.EntityID, &x.FromDate, &x.ToDate,
			&x.Reason, &x.StopsMoved, &x.ActorID, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// - DIB-75 · the scan trail -

const scanCols = `id::text, branch_id::text, order_id::text, COALESCE(stop_id::text,''),
                  load_ref::text, checkpoint, COALESCE(scanned_by::text,''), scanned_at, note,
                  pod_signature_ref`

func scanCheckpointRow(row pgx.Row) (ScanCheckpoint, error) {
	var c ScanCheckpoint
	err := row.Scan(&c.ID, &c.BranchID, &c.OrderID, &c.StopID, &c.LoadRef, &c.Checkpoint,
		&c.ScannedBy, &c.ScannedAt, &c.Note, &c.PODSignatureRef)
	return c, err
}

// UpsertScanCheckpoint records one checkpoint. A re-scan of the SAME load at the SAME checkpoint
// updates the existing row rather than appending a second, a trail that shows PICKED three times is
// a trail nobody trusts (0108's UNIQUE key is what makes this a single statement rather than a
// read-then-write race).
//
// The re-scan deliberately refreshes scanned_at/scanned_by: the last person to physically see the
// load at that stage is the useful answer, not the first.
func (r *Repository) UpsertScanCheckpoint(ctx context.Context, in ScanCheckpoint) (ScanCheckpoint, error) {
	pool, err := r.db()
	if err != nil {
		return ScanCheckpoint{}, err
	}
	row := pool.QueryRow(ctx, `
INSERT INTO load_scan_checkpoints (branch_id, order_id, stop_id, load_ref, checkpoint, scanned_by,
                                   scanned_at, note)
VALUES ($1, $2, NULLIF($3,'')::uuid, $4, $5, NULLIF($6,'')::uuid, $7, $8)
ON CONFLICT (order_id, load_ref, checkpoint) DO UPDATE
   SET stop_id    = COALESCE(EXCLUDED.stop_id, load_scan_checkpoints.stop_id),
       scanned_by = EXCLUDED.scanned_by,
       scanned_at = EXCLUDED.scanned_at,
       note       = CASE WHEN EXCLUDED.note <> '' THEN EXCLUDED.note ELSE load_scan_checkpoints.note END
RETURNING `+scanCols,
		in.BranchID, in.OrderID, in.StopID, in.LoadRef, in.Checkpoint, in.ScannedBy, in.ScannedAt, in.Note)
	return scanCheckpointRow(row)
}

// ListScanTrail reads every checkpoint for one order, oldest first, the order they happened.
func (r *Repository) ListScanTrail(ctx context.Context, orderID string) ([]ScanCheckpoint, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	if !looksLikeUUID(orderID) {
		return nil, ErrNotFound
	}
	rows, err := pool.Query(ctx,
		`SELECT `+scanCols+` FROM load_scan_checkpoints WHERE order_id = $1 ORDER BY scanned_at, load_ref`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScanCheckpoint{}
	for rows.Next() {
		c, err := scanCheckpointRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TieScanTrailToPOD stamps the attestation ref onto the DELIVERED checkpoints of one stop's order.
// Best-effort at the call site (see service_w3.go): the delivery is already evidenced on the stop, and
// a trail that could not be closed is a gap to see, not a delivery to roll back.
func (r *Repository) TieScanTrailToPOD(ctx context.Context, orderID, sigRef string) (int, error) {
	pool, err := r.db()
	if err != nil {
		return 0, err
	}
	if !looksLikeUUID(orderID) || strings.TrimSpace(sigRef) == "" {
		return 0, nil
	}
	tag, err := pool.Exec(ctx, `
UPDATE load_scan_checkpoints SET pod_signature_ref = $2
 WHERE order_id = $1 AND checkpoint = 'DELIVERED' AND pod_signature_ref = ''`, orderID, sigRef)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// StopBranchAndOrder resolves the (branch, order) pair a scan needs from a stop id, without dragging
// the whole stop projection through a scan write.
func (r *Repository) StopBranchAndOrder(ctx context.Context, stopID string) (branchID, orderID string, err error) {
	pool, err := r.db()
	if err != nil {
		return "", "", err
	}
	if !looksLikeUUID(stopID) {
		return "", "", ErrNotFound
	}
	err = pool.QueryRow(ctx,
		`SELECT branch_id::text, order_id::text FROM delivery_stops WHERE id = $1`, stopID).Scan(&branchID, &orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return branchID, orderID, err
}

// OrderBranchForScan resolves an order's branch for a checkpoint recorded before any stop exists
// (RECEIVED / YARD). ErrNotFound for an unknown order, a scan against nothing is refused, not stored.
func (r *Repository) OrderBranchForScan(ctx context.Context, orderID string) (string, error) {
	pool, err := r.db()
	if err != nil {
		return "", err
	}
	if !looksLikeUUID(orderID) {
		return "", ErrNotFound
	}
	var branchID string
	err = pool.QueryRow(ctx, `SELECT branch_id::text FROM orders WHERE id = $1`, orderID).Scan(&branchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return branchID, err
}

// - the counts behind the page, and the population behind the D-110 card -

// CountPickFeedPendingStops is PickFeedPendingStops' twin: the same population, counted rather than
// read. It shares the WHERE constant with the list, which is the only way a "showing 1-20 of 47" can
// be trusted, a total computed from a second, hand-written predicate is a total that drifts.
func (r *Repository) CountPickFeedPendingStops(ctx context.Context, branchID string) (int, error) {
	pool, err := r.db()
	if err != nil {
		return 0, err
	}
	if branchID != "" && !looksLikeUUID(branchID) {
		return 0, nil // an unparseable branch names no branch; it is not an error
	}
	q := pickFeedCountFrom + pickFeedPendingWhere
	args := []any{}
	if branchID != "" {
		q += ` AND s.branch_id = $1`
		args = append(args, branchID)
	}
	var n int
	err = pool.QueryRow(ctx, q, args...).Scan(&n)
	return n, err
}

// CountPickFeedPendingPickups is PickFeedPendingPickups' twin, on the same terms.
func (r *Repository) CountPickFeedPendingPickups(ctx context.Context, branchID string) (int, error) {
	pool, err := r.db()
	if err != nil {
		return 0, err
	}
	if branchID != "" && !looksLikeUUID(branchID) {
		return 0, nil
	}
	q := `SELECT count(*)::int FROM orders o` + pickupFeedWhere + pickupAwaitingCollection
	args := []any{}
	if branchID != "" {
		q += ` AND o.branch_id = $1`
		args = append(args, branchID)
	}
	var n int
	err = pool.QueryRow(ctx, q, args...).Scan(&n)
	return n, err
}

// PendingPickupOrderIDs is the ORDER IDS of the open-pickup population, the same population
// PickFeedPendingPickups lists, reduced to the one column the D-110 card needs.
//
// WHY IDS AND NOT A COUNT WITH A JOIN. The card must split that population by how far the YARD has
// got, and how far the yard has got is a fact in the tasks module's own table. dispatch does not read
// tasks' tables, the whole PickCompletionReader seam (module.go) exists to reverse exactly that
// dependency, so the split is done by asking the seam about these ids, not by joining across a
// module boundary in SQL. The cost is one extra round trip; the alternative is a cross-module table
// read that the architecture rules forbid outright and that no test would catch.
//
// Unbounded on purpose: this is a COUNT's input, and a branch's open pickups is a small set by
// construction (an order leaves it the moment it is collected). It is capped anyway, so a
// pathological branch degrades to an undercount rather than to an unbounded scan, and the cap is
// reported, never silently applied.
func (r *Repository) PendingPickupOrderIDs(ctx context.Context, branchID string, cap int) ([]string, bool, error) {
	pool, err := r.db()
	if err != nil {
		return nil, false, err
	}
	if branchID != "" && !looksLikeUUID(branchID) {
		return []string{}, false, nil
	}
	if cap <= 0 {
		cap = 500
	}
	q := `SELECT o.id::text FROM orders o` + pickupFeedWhere + pickupAwaitingCollection
	args := []any{cap + 1}
	if branchID != "" {
		q += ` AND o.branch_id = $2`
		args = append(args, branchID)
	}
	q += ` ORDER BY o.requested_date ASC NULLS LAST, o.created_at LIMIT $1`

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// One row over the cap is how we know we hit it. The caller says so on screen rather than
	// quietly showing a number that is smaller than the truth.
	if len(out) > cap {
		return out[:cap], true, nil
	}
	return out, false, nil
}
