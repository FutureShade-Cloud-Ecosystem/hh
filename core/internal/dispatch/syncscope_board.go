// syncscope_board.go is the dispatch board's sync trio (HH-3.5 work
// item #168): the loads, delivery_stops and will_call Stores over this
// module's own tables. The pattern is the drive-run adapters'
// (syncscope_runs.go, syncscope_stops.go, work item #167), whose
// helpers this file reuses rather than restating: the pull projection
// (the contract's DispatchStop schema over stopSelectColumns), the
// org predicate, the seq allocator and the CTE stamp.
//
// THE SHARED SEQ STREAM, the one design note this file adds:
// loads, delivery_stops and stops are three scopes over ONE table
// (delivery_stops), and the table carries ONE seq column (the frozen
// 0213 shape). Three independent counters interleaving on one column
// would break the since-cursor (a row stamped 5 after the cursor
// already passed 6 never pulls), so every write to the table, from any
// of the three scopes and from the module's own REST paths, draws from
// the stops stream's one counter. A load fact a dispatcher writes is
// visible to a driver's stops pull and the other way round, which is
// the honest behavior: the row moved. The table's ONE D30
// idempotency_key column is shared the same way: a write from any of
// the three scopes displaces the latest key, so a replayed operation
// whose key a LATER write from another scope displaced re-applies its
// declared fields, converging on the same values through fresh seqs
// (the attachments lane's documented single-key posture, now across
// three scopes; GATE-7's one-update-per-row shape and the batteries'
// constant-update posture keep the strict replay legs strict).
//
// THE WRITE WINDOWS, each the module's own vocabulary and nothing
// beyond it:
//
//	loads           the load's measurements (weight, volume, pallet
//	                positions), the SetLoadFacts surface: deliberately
//	                not lock-guardarded (the yard telling the truth
//	                about the plan happens at the forklift, inside the
//	                window). A nil value CLEARS a leg (nobody measured
//	                it), which is a different fact from zero.
//	delivery_stops  the scheduling move: a re-time with its reason
//	                (RescheduleStop's own rules: READY, off the run, a
//	                tomorrow-or-later date, a reason somebody will
//	                read). The commitment gates (commit, reopen, crew,
//	                run assignment) are capability questions the sync
//	                filter cannot answer, and they refuse here rather
//	                than being waved through: those acts stay at the
//	                desk's own door.
//	will_call       the rack tag: minted (the raw token is
//	                client-generated on the device, hashed at rest
//	                exactly as the module hashes its own, shown once on
//	                the printer that made it) and revoked. Confirmation
//	                is the public desk's typed-name door and is not
//	                reachable through sync.
//
// will_call_pickups carries no org column (0213 stamped none), so the
// org fence is the branch's, read off locations the same way
// branchInOrg reads it: a pickup row is in the org its branch is in.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// willCallSyncScope is the will_call scope's own sequence counter. The
// two board scopes over delivery_stops draw from the stops stream (the
// shared-stream note above); will_call has its table to itself.
const willCallSyncScope = "will_call"

// - loads -

// LoadsSyncStore serves the loads scope over delivery_stops: the same
// rows the stops scope serves, in their load-building role. Everything
// but the write window is the stops adapter's, embedded rather than
// restated.
type LoadsSyncStore struct {
	*StopsSyncStore
}

// NewLoadsSyncStore wires the adapter over the stops adapter's own
// read and create paths.
func NewLoadsSyncStore(inner *StopsSyncStore) *LoadsSyncStore {
	return &LoadsSyncStore{StopsSyncStore: inner}
}

// loadFactLeg is one declared measurement leg: the value is a number
// (set) or nil (clear, nobody measured it).
type loadFactLeg struct {
	value *float64
}

// loadFactNumber reads one measurement off a pushed payload: a JSON
// number, non-negative, or nil to clear. The 0107/0184 posture: nil is
// NOBODY MEASURED IT, a different fact from zero.
func loadFactNumber(v any) (*float64, error) {
	switch n := v.(type) {
	case nil:
		return nil, nil
	case float64:
		if n < 0 {
			return nil, refused(fmt.Errorf("a load measurement cannot be negative"))
		}
		return &n, nil
	case int:
		f := float64(n)
		if f < 0 {
			return nil, refused(fmt.Errorf("a load measurement cannot be negative"))
		}
		return &f, nil
	}
	return nil, fmt.Errorf("dispatch: the measurement must be a number or null: %w", sync.ErrOpRefused)
}

// loadWriteFields validates a loads update's declared legs and returns
// them in column order. The set is exactly SetLoadFacts': weight,
// volume, pallet positions.
func loadWriteFields(fields map[string]any) (weight, volume, pallets *loadFactLeg, err error) {
	for k, v := range fields {
		var leg *loadFactLeg
		switch k {
		case "id":
			continue
		case "weight_kg", "volume_m3", "pallet_positions":
			val, err := loadFactNumber(v)
			if err != nil {
				return nil, nil, nil, err
			}
			leg = &loadFactLeg{value: val}
		default:
			return nil, nil, nil, fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
		switch k {
		case "weight_kg":
			weight = leg
		case "volume_m3":
			volume = leg
		case "pallet_positions":
			pallets = leg
		}
	}
	if weight == nil && volume == nil && pallets == nil {
		return nil, nil, nil, refused(fmt.Errorf("an update that moves nothing refuses"))
	}
	// Pallet positions are whole positions on a truck.
	if pallets != nil && pallets.value != nil && *pallets.value != float64(int(*pallets.value)) {
		return nil, nil, nil, refused(fmt.Errorf("pallet positions are whole positions"))
	}
	return weight, volume, pallets, nil
}

// loadFactEqual reports whether one declared leg is already the row's
// standing fact (nil clears nil, a number equals the number).
func loadFactEqual(leg *loadFactLeg, standing *float64) bool {
	if leg == nil {
		return true
	}
	if leg.value == nil || standing == nil {
		return leg.value == nil && standing == nil
	}
	return *leg.value == *standing
}

// Write applies exactly the declared measurement legs to one load,
// under the module's own posture (facts are never lock-guardarded: the
// yard telling the truth happens at the forklift), on a FOR UPDATE
// locked pre-image, stamping a fresh seq off the shared stops stream
// and the idempotency key. The value-equal arm answers applied with
// the standing row and stamps nothing, so a superseded replay of one
// constant fact cannot inflate the feed.
func (s *LoadsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := stopScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	weight, volume, pallets, err := loadWriteFields(fields)
	if err != nil {
		return sync.RowState{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin loads sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var standingWeight, standingVolume *float64
	var standingPallets *int
	if err := tx.QueryRow(ctx,
		`SELECT weight_kg, volume_m3, pallet_positions FROM delivery_stops
		 WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&standingWeight, &standingVolume, &standingPallets); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("dispatch: lock %q for loads sync write: %w", id, err)
	}
	standingPalletsF := (*float64)(nil)
	if standingPallets != nil {
		v := float64(*standingPallets)
		standingPalletsF = &v
	}
	if loadFactEqual(weight, standingWeight) && loadFactEqual(volume, standingVolume) && loadFactEqual(pallets, standingPalletsF) {
		r, err := stopsSyncRowByID(ctx, tx, id)
		if err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: loads sync write read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: commit loads sync write: %w", err)
		}
		return stopStateOf(r)
	}
	seq, err := allocateSyncSeqTx(ctx, tx, stopsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	// The SET list is built leg by leg with a running placeholder
	// index, so undeclared legs bind nothing and the declared ones
	// bind in order ($1 id, $2 org already consumed).
	sets := []string{}
	args := []any{id, f.OrgID}
	addLeg := func(column string, v *float64, asInt bool) {
		if v == nil {
			// A cleared leg binds no placeholder (an unreferenced
			// parameter is a fragility, not a need).
			sets = append(sets, fmt.Sprintf("%s = NULL", column))
			return
		}
		args = append(args, v)
		placeholder := fmt.Sprintf("$%d", len(args))
		if asInt {
			sets = append(sets, fmt.Sprintf("%s = %s::int", column, placeholder))
			return
		}
		sets = append(sets, fmt.Sprintf("%s = %s", column, placeholder))
	}
	if weight != nil {
		addLeg("weight_kg", weight.value, false)
	}
	if volume != nil {
		addLeg("volume_m3", volume.value, false)
	}
	if pallets != nil {
		addLeg("pallet_positions", pallets.value, true)
	}
	sets = append(sets, `updated_at = now()`, fmt.Sprintf(`seq = $%d`, len(args)+1), fmt.Sprintf(`idempotency_key = $%d::uuid`, len(args)+2))
	args = append(args, seq, key)
	q := `UPDATE delivery_stops SET ` + strings.Join(sets, ", ") + `
		WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL
		RETURNING id::text`
	var rowID string
	if err := tx.QueryRow(ctx, q, args...).Scan(&rowID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("dispatch: loads sync write %q: %w", id, err)
	}
	r, err := stopsSyncRowByID(ctx, tx, rowID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: loads sync write read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit loads sync write: %w", err)
	}
	return stopStateOf(r)
}

// Delete tombstones one load and withdraws it: deleted_at set, the
// row retained, a fresh seq off the shared stream, the CANCELLED the
// module's own withdrawal writes (the pre-departure move of the stop
// matrix, READY only). A load that left the yard is a record and
// refuses the tombstone.
func (s *LoadsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return boardWithdrawStop(ctx, s.pool, f, id, key)
}

// - delivery_stops -

// DeliveryStopsSyncStore serves the delivery_stops scope over
// delivery_stops: the dispatcher's scheduling window. Everything but
// the write window is the stops adapter's, embedded.
type DeliveryStopsSyncStore struct {
	*StopsSyncStore
}

// NewDeliveryStopsSyncStore wires the adapter over the stops adapter's
// own read and create paths.
func NewDeliveryStopsSyncStore(inner *StopsSyncStore) *DeliveryStopsSyncStore {
	return &DeliveryStopsSyncStore{StopsSyncStore: inner}
}

// deliveryStopWriteFields validates a delivery_stops update's declared
// legs: the re-time pair, scheduled_date with the reason somebody will
// read (RescheduleStop's own vocabulary).
func deliveryStopWriteFields(fields map[string]any) (string, string, error) {
	date, hasDate := "", false
	reason := ""
	for k, v := range fields {
		switch k {
		case "id":
			continue
		case "scheduled_date":
			d, ok := v.(string)
			if !ok {
				return "", "", fmt.Errorf("dispatch: scheduled_date must be YYYY-MM-DD: %w", sync.ErrOpRefused)
			}
			date, hasDate = strings.TrimSpace(d), true
		case "reason":
			r, ok := v.(string)
			if !ok {
				return "", "", fmt.Errorf("dispatch: reason must be a string: %w", sync.ErrOpRefused)
			}
			reason = r
		default:
			return "", "", fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	if !hasDate {
		return "", "", refused(fmt.Errorf("an update that moves nothing refuses"))
	}
	return date, reason, nil
}

// Write applies the scheduling move to one stop: the module's own
// RescheduleStop rules, verbatim (a well-formed date that differs from
// the standing one, is not in the past, a reason under 400 characters,
// a READY stop that is not on a run), the move and the reschedule
// trail committing together on one transaction, the seq drawn off the
// shared stops stream.
func (s *DeliveryStopsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := stopScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	date, reason, err := deliveryStopWriteFields(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	actor := syncActor(ctx, s.link, f.Actor)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin delivery_stops sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, runID, branchID string
	if err := tx.QueryRow(ctx,
		`SELECT to_char(scheduled_date,'YYYY-MM-DD'), COALESCE(run_id::text,''), branch_id::text
		 FROM delivery_stops
		 WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&from, &runID, &branchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("dispatch: lock %q for delivery_stops sync write: %w", id, err)
	}
	// RescheduleStop's own guards, in its own words.
	if !validDate(date) {
		return sync.RowState{}, fmt.Errorf("dispatch: scheduled_date must be YYYY-MM-DD: %w", sync.ErrOpRefused)
	}
	if date == from {
		return sync.RowState{}, refused(fmt.Errorf("that is already the scheduled date"))
	}
	if date < time.Now().UTC().Format("2006-01-02") {
		return sync.RowState{}, refused(fmt.Errorf("%s has already passed, a delivery cannot be promised for a past date", date))
	}
	why := strings.TrimSpace(reason)
	if why == "" {
		return sync.RowState{}, refused(fmt.Errorf("say why the delivery is moving, the customer will ask"))
	}
	if len(why) > 400 {
		return sync.RowState{}, refused(fmt.Errorf("the reason is too long (max 400)"))
	}
	// The stop's standing status and run read under the lock the same
	// sentence RescheduleStop reads: not READY refuses, on-a-run
	// refuses (reschedule the whole run instead).
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM delivery_stops WHERE id = $1::uuid`, id).Scan(&status); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: delivery_stops sync status read: %w", err)
	}
	if status != StopReady {
		return sync.RowState{}, refused(ErrBackwardReschedule)
	}
	if runID != "" {
		return sync.RowState{}, refused(fmt.Errorf("this stop is on a run, take it off the run first, or reschedule the whole run"))
	}
	seq, err := allocateSyncSeqTx(ctx, tx, stopsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	var rowID string
	err = tx.QueryRow(ctx, `
		UPDATE delivery_stops SET
			scheduled_date  = $3::date,
			updated_at      = now(),
			seq             = $4,
			idempotency_key = $5::uuid
		WHERE id = $1::uuid AND org_id = $2::uuid AND scheduled_date = $6::date AND status = 'READY'
		RETURNING id::text`,
		id, f.OrgID, date, seq, key, from).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: delivery_stops sync write %q: %w", id, err)
	}
	if _, err := insertRescheduleTx(ctx, tx, Reschedule{
		BranchID: branchID, EntityType: RescheduleEntityStop, EntityID: rowID,
		FromDate: from, ToDate: date, Reason: why, StopsMoved: 1, ActorID: actor,
	}); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: delivery_stops sync trail: %w", err)
	}
	r, err := stopsSyncRowByID(ctx, tx, rowID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: delivery_stops sync write read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit delivery_stops sync write: %w", err)
	}
	return stopStateOf(r)
}

// Delete tombstones one stop and withdraws it, the loads scope's
// Delete: the pre-departure CANCELLED plus the tombstone.
func (s *DeliveryStopsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return boardWithdrawStop(ctx, s.pool, f, id, key)
}

// boardWithdrawStop is the two board scopes' shared delete: the
// tombstone (deleted_at set, the row retained) plus the CANCELLED the
// module's own withdrawal writes, READY only (a load that left the
// yard is a record), the seq off the shared stops stream, the live
// row's idempotency key kept.
func boardWithdrawStop(ctx context.Context, pool *pgxpool.Pool, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if f.Tenant || f.OrgID == "" {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin board sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM delivery_stops WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("dispatch: lock %q for board sync delete: %w", id, err)
	}
	if status != StopReady {
		return sync.RowState{}, refused(fmt.Errorf("a %s stop is a record, withdrawal is the pre-departure move", status))
	}
	if !stopCanTransition(status, StopCancelled) {
		return sync.RowState{}, refused(ErrIllegalTransition)
	}
	seq, err := allocateSyncSeqTx(ctx, tx, stopsSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE delivery_stops SET
			status     = 'CANCELLED',
			deleted_at = now(),
			updated_at = now(),
			seq        = $3
		WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL AND status = 'READY'
		RETURNING (deleted_at IS NOT NULL)`, id, f.OrgID, seq).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: board sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit board sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}

// - will_call -

// WillCallSyncStore serves the will_call scope over will_call_pickups.
type WillCallSyncStore struct {
	pool   *pgxpool.Pool
	orders OrderReader
	picks  PickCompletionReader
	link   syncLinker
}

// NewWillCallSyncStore wires the adapter: the order seam and the pick
// state seam are the module's own (the tag follows the pick), the
// linker resolves the audit actor.
func NewWillCallSyncStore(pool *pgxpool.Pool, orders OrderReader, picks PickCompletionReader, link syncLinker) *WillCallSyncStore {
	return &WillCallSyncStore{pool: pool, orders: orders, picks: picks, link: link}
}

// willCallSyncColumns is the pull projection: the module's own pickup
// shape (the staff surface's), whose token_hash leg it already omits,
// plus the sync legs. The raw token exists exactly once, on the
// printer that made it; the hash never leaves the table, and the sync
// feed carries neither.
const willCallSyncColumns = pickupCols + `, seq, (deleted_at IS NOT NULL) AS deleted`

// syncPickupRow is one scanned row of the sync projection.
type syncPickupRow struct {
	pickup  WillCall
	seq     int64
	deleted bool
}

// scanSyncPickup reads the pickup targets then the two sync legs.
func scanSyncPickup(row pgx.Row) (syncPickupRow, error) {
	var w WillCall
	var seq int64
	var deleted bool
	if err := row.Scan(&w.ID, &w.OrderID, &w.BranchID, &w.Status, &w.ExpiresAt,
		&w.SignatureRef, &w.CollectedBy, &w.ConfirmedAt, &w.CreatedAt,
		&seq, &deleted); err != nil {
		return syncPickupRow{}, err
	}
	return syncPickupRow{pickup: w, seq: seq, deleted: deleted}, nil
}

// pickupStateOf folds one scanned row into the engine's RowState.
func pickupStateOf(r syncPickupRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.pickup.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := projection(r.pickup)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// willCallScopePredicate is the WHERE predicate every will_call query
// runs under: the org fence is the branch's (will_call_pickups carries
// no org column, 0213 stamped none on it), the same read branchInOrg
// makes. A tenant filter fails closed (will_call is an org scope).
func willCallScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return `branch_id IN (SELECT id FROM locations WHERE org_id = $1::uuid)`, []any{f.OrgID}, true
}

// Pull returns the page of pickup changes past since.
func (s *WillCallSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := willCallScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM will_call_pickups
		WHERE %s AND seq IS NOT NULL AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, willCallSyncColumns, where)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("dispatch: will_call sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncPickup(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("dispatch: will_call sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.pickup.ID, Seq: r.seq})
			continue
		}
		data, err := projection(r.pickup)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.pickup.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("dispatch: will_call sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// D30 column's latest key, kept through a tombstone.
func (s *WillCallSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := willCallScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM will_call_pickups
		WHERE %s AND idempotency_key = $2::uuid
		LIMIT 1`, willCallSyncColumns, where)
	args = append(args, key)
	r, err := scanSyncPickup(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: will_call sync find applied: %w", err)
	}
	rs, err := pickupStateOf(r)
	return rs, true, err
}

// Load reads one pickup by id under the org fence.
func (s *WillCallSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.pool, f, id)
}

// willCallQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type willCallQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *WillCallSyncStore) load(ctx context.Context, db willCallQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := willCallScopePredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM will_call_pickups WHERE id = $2::uuid AND %s`,
		willCallSyncColumns, where)
	args = append(args, id)
	r, err := scanSyncPickup(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: will_call sync load: %w", err)
	}
	rs, err := pickupStateOf(r)
	return rs, true, err
}

// syncTTLHours reads one ttl leg: a JSON number or a Go-built integer.
func syncTTLHours(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// willCallCreateFields is the field set a pushed create may declare:
// the order the tag prints for, the raw client-generated token (the
// device holds it and prints it; the server stores only its hash,
// exactly the module's own mint), and the optional tag lifetime in
// hours (the module's default when absent).
func willCallCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "order_id", "token", "ttl_hours":
		default:
			return fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// Create mints one pickup row from a pushed create, the module's own
// MintWillCall rules verbatim: the order resolves through the seam, is
// a WILL_CALL order that is neither DRAFT nor finished, the material
// is picked (STAGED, the tag follows the pick), the branch sits in the
// caller's org, and one live tag per order (the 0092 partial index,
// answered with the module's own sentence). The raw token is hashed at
// rest and never stored, never echoed, never pulled.
func (s *WillCallSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := willCallScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := willCallCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	orderID, _ := fields["order_id"].(string)
	if !looksLikeUUID(orderID) {
		return sync.RowState{}, fmt.Errorf("dispatch: order_id must be a uuid: %w", sync.ErrOpRefused)
	}
	raw, _ := fields["token"].(string)
	if raw == "" {
		return sync.RowState{}, fmt.Errorf("dispatch: token is required, the device generates it and prints it once: %w", sync.ErrOpRefused)
	}
	if len(raw) < 22 || len(raw) > 128 {
		return sync.RowState{}, fmt.Errorf("dispatch: the token is the device-generated square, 32 bytes base64url: %w", sync.ErrOpRefused)
	}
	ttl := defaultPickupTTL
	if v, has := fields["ttl_hours"]; has && v != nil {
		h, ok := syncTTLHours(v)
		if !ok || h <= 0 || h > 24*90 {
			return sync.RowState{}, fmt.Errorf("dispatch: ttl_hours must be a positive number of hours: %w", sync.ErrOpRefused)
		}
		ttl = time.Duration(h) * time.Hour
	}
	if s.orders == nil {
		return sync.RowState{}, fmt.Errorf("dispatch: order seam not configured: %w", sync.ErrOpRefused)
	}
	brief, err := s.orders.Brief(ctx, orderID)
	if err != nil {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	// THE ORG FENCE RUNS BEFORE ANY STATUS SENTENCE, the stops
	// adapter's own order: a foreign order's fulfillment is not the
	// caller's fact to learn.
	if !branchInOrg(ctx, s.pool, brief.BranchID, f.OrgID) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if brief.Fulfillment != "WILL_CALL" {
		return sync.RowState{}, refused(fmt.Errorf("this is %s, not will-call, a rack tag prints only for a will-call order", fulfilmentWord(brief.Fulfillment)))
	}
	switch brief.Status {
	case "DRAFT":
		return sync.RowState{}, refused(fmt.Errorf("confirm the order before printing a pickup code"))
	case "CANCELLED", "CLOSED":
		return sync.RowState{}, refused(fmt.Errorf("%s", orderStateWord(brief.Status)))
	}
	if s.picks != nil {
		if err := requirePickedFor(ctx, s.picks, brief.ID); err != nil {
			return sync.RowState{}, err
		}
	}
	id, err := syncCreateID(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	actor := syncActor(ctx, s.link, f.Actor)
	hashed := hashPickupToken(raw)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin will_call sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSyncSeqTx(ctx, tx, willCallSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	var rowID string
	err = tx.QueryRow(ctx, `
		INSERT INTO will_call_pickups (id, order_id, branch_id, token_hash, expires_at, created_by, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, $4, $5, NULLIF($6,'')::uuid, $7, $8::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING id::text`,
		id, brief.ID, brief.BranchID, hashed, time.Now().UTC().Add(ttl), actor, seq, key).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, own, _ := s.load(ctx, tx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The one-live-tag index: the caller's own fact, answered with
		// the module's own sentence.
		return sync.RowState{}, refused(fmt.Errorf("this order already has a live pickup code, revoke it before printing another"))
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: will_call sync create insert: %w", err)
	}
	r, err := scanSyncPickup(tx.QueryRow(ctx,
		`SELECT `+willCallSyncColumns+` FROM will_call_pickups WHERE id = $1::uuid`, rowID))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: will_call sync create read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit will_call sync create: %w", err)
	}
	return pickupStateOf(r)
}

// requirePickedFor is requirePicked over the seam the adapter holds:
// the module's own gate and its own sentences (only a STAGED pick
// passes, a failed read refuses). A nil seam allows, the module's own
// unwired posture.
func requirePickedFor(ctx context.Context, picks PickCompletionReader, orderID string) error {
	state, err := picks.OrderPickState(ctx, orderID)
	if err != nil {
		return refused(fmt.Errorf("the yard could not be asked whether this order has been picked, try again in a moment"))
	}
	if !state.Exists {
		return refused(fmt.Errorf("nothing has been picked for this order yet, it is in the yard's queue, and the tag prints once the pick is staged"))
	}
	if !state.Staged {
		return refused(fmt.Errorf("this order is %s, the tag prints once the pick is staged", pickStateWord(state.Status)))
	}
	return nil
}

// willCallWriteFields is the field set a pushed update may declare: the
// revoke, and the tag's lifetime. Confirmation is the public desk's
// typed-name door; the evidence and the status are one write there.
// The lifetime leg is the runs-notes precedent restated: a row fact the
// module owns and persists per row (0092's own note anticipates
// per-square lifetimes), reachable through sync with the module's own
// clock discipline, forward only and never on a spent square.
func willCallWriteFields(fields map[string]any) (string, string, error) {
	status, expires := "", ""
	for k, v := range fields {
		switch k {
		case "id":
			continue
		case "status":
			s, ok := v.(string)
			if !ok {
				return "", "", fmt.Errorf("dispatch: status must be a string: %w", sync.ErrOpRefused)
			}
			status = s
		case "expires_at":
			e, ok := v.(string)
			if !ok {
				return "", "", fmt.Errorf("dispatch: expires_at must be a timestamp: %w", sync.ErrOpRefused)
			}
			expires = e
		default:
			return "", "", fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	if status == "" && expires == "" {
		return "", "", refused(fmt.Errorf("an update that moves nothing refuses"))
	}
	return status, expires, nil
}

// parseSyncExpiry reads one pushed expiry leg (RFC3339, the projection's
// own serialized form) and returns it in UTC.
func parseSyncExpiry(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("dispatch: expires_at must be a timestamp: %w", sync.ErrOpRefused)
	}
	return t.UTC(), nil
}

// Write applies the declared legs to one pickup: the revoke (ACTIVE to
// REVOKED, the module's own RevokePickup arm), or the lifetime's
// extension (forward only, ACTIVE only: a spent or revoked square's
// history is the record). Seq and key stamp beside the move.
func (s *WillCallSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := willCallScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	status, expires, err := willCallWriteFields(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	if status != "" && status != "REVOKED" {
		return sync.RowState{}, refused(fmt.Errorf("the desk's confirmation door is the only other move a pickup has"))
	}
	var extendTo *time.Time
	if expires != "" {
		t, err := parseSyncExpiry(expires)
		if err != nil {
			return sync.RowState{}, err
		}
		extendTo = &t
	}
	if status == "REVOKED" && extendTo != nil {
		return sync.RowState{}, refused(fmt.Errorf("a revoked square carries no lifetime to extend"))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin will_call sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var standing string
	var standingExpiry time.Time
	if err := tx.QueryRow(ctx,
		`SELECT status, expires_at FROM will_call_pickups
		 WHERE id = $1::uuid AND deleted_at IS NULL
		   AND branch_id IN (SELECT id FROM locations WHERE org_id = $2::uuid)
		 FOR UPDATE`,
		id, f.OrgID).Scan(&standing, &standingExpiry); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("dispatch: lock %q for will_call sync write: %w", id, err)
	}
	if standing == "REVOKED" && status == "REVOKED" {
		// The value-equal arm: the revoke is already the standing
		// fact, answered applied without stamping (a superseded replay
		// of one constant revoke churns nothing).
		r, err := scanSyncPickup(tx.QueryRow(ctx,
			`SELECT `+willCallSyncColumns+` FROM will_call_pickups WHERE id = $1::uuid`, id))
		if err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: will_call sync write read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: commit will_call sync write: %w", err)
		}
		return pickupStateOf(r)
	}
	if standing != "ACTIVE" {
		return sync.RowState{}, refused(fmt.Errorf("this pickup code is already %s", strings.ToLower(standing)))
	}
	if extendTo != nil {
		// The extension leg: forward only (a shorter life is a revoke,
		// not a squeeze), and never into the past.
		if !extendTo.After(standingExpiry) {
			return sync.RowState{}, refused(fmt.Errorf("a tag's life only extends, revoke it if the customer is not coming"))
		}
		seq, err := allocateSyncSeqTx(ctx, tx, willCallSyncScope)
		if err != nil {
			return sync.RowState{}, err
		}
		var rowID string
		err = tx.QueryRow(ctx, `
			UPDATE will_call_pickups SET
				expires_at      = $2,
				seq             = $3,
				idempotency_key = $4::uuid
			WHERE id = $1::uuid AND status = 'ACTIVE'
			RETURNING id::text`,
			id, *extendTo, seq, key).Scan(&rowID)
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		if err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: will_call sync extend %q: %w", id, err)
		}
		r, err := scanSyncPickup(tx.QueryRow(ctx,
			`SELECT `+willCallSyncColumns+` FROM will_call_pickups WHERE id = $1::uuid`, rowID))
		if err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: will_call sync write read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: commit will_call sync write: %w", err)
		}
		return pickupStateOf(r)
	}
	seq, err := allocateSyncSeqTx(ctx, tx, willCallSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	var rowID string
	err = tx.QueryRow(ctx, `
		UPDATE will_call_pickups SET
			status          = 'REVOKED',
			revoked_at      = now(),
			seq             = $2,
			idempotency_key = $3::uuid
		WHERE id = $1::uuid AND status = 'ACTIVE'
		RETURNING id::text`,
		id, seq, key).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: will_call sync write %q: %w", id, err)
	}
	r, err := scanSyncPickup(tx.QueryRow(ctx,
		`SELECT `+willCallSyncColumns+` FROM will_call_pickups WHERE id = $1::uuid`, rowID))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: will_call sync write read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit will_call sync write: %w", err)
	}
	return pickupStateOf(r)
}

// Delete tombstones one pickup and revokes it: the tombstone alone
// would leave an ACTIVE row the public scan still resolves (the
// module's resolver reads status, not deleted_at), so the revoke and
// the tombstone commit together, and the holder learns the code died
// (410) rather than the order never existing (404).
func (s *WillCallSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := willCallScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin will_call sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSyncSeqTx(ctx, tx, willCallSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE will_call_pickups SET
			status     = 'REVOKED',
			revoked_at = now(),
			deleted_at = now(),
			seq        = $3
		WHERE id = $1::uuid AND deleted_at IS NULL
		  AND branch_id IN (SELECT id FROM locations WHERE org_id = $2::uuid)
		RETURNING (deleted_at IS NOT NULL)`, id, f.OrgID, seq).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: will_call sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit will_call sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}
