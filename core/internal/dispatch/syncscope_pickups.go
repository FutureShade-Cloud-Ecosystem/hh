// syncscope_pickups.go is the pickups Store (HH-3.5 #169, manual
// policy): the contractor's will-call pickups over this module's own
// will_call_pickups table. The contractor reads a pickup on their order
// and confirms the collection offline ("confirm a will-call pickup,
// which cancels the pending auto-collect rather than adding an
// additional delivered event", the catalog's pro-field job); the manual
// policy means a stale confirm never overwrites whatever the desk did
// while the phone was offline (a revoke, an earlier confirm): the
// replay answers conflict, applies nothing, and returns the server row
// with the review marker.
//
// Tenancy rides the ORDER: will_call_pickups carries no tenant column
// in the frozen schema, so the tenant's rows are the pickups of the
// orders the tenant owns, read through the owning module's seam
// (TenantOrderReader below), never a join across the fence. A confirm
// seals its typed-name attestation through the same SignatureCapturer
// seam the public confirm path uses, so the evidence rules (a blank
// name is not a signature, the affirmative is a separate act) are the
// provider's, not this adapter's.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// pickupsLinker names the staff identity seam the adapter takes (the
// linked ERP user for a platform subject), aliased locally so the
// constructor reads in this file's vocabulary. The tenant caller's
// actor is the platform subject itself; the link resolves only on the
// composition root's staff paths, which this adapter never drives.
type pickupsLinker = identity.UserIDLinker

// allocatePickupsSeqTx draws the next pickups seq inside the caller's
// transaction. The table has one counter (willCallSyncScope), so the
// contractor's writes draw from it like every staff write.
func allocatePickupsSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	return allocateSyncSeqTx(ctx, tx, willCallSyncScope)
}

// TenantOrderReader is the tenant half of the order seam: the order
// module's own tenant-scoped read (a cross-tenant id resolves to the
// same not-found an absent one does) plus the tenant's order id set
// the feed predicate needs. Satisfied by an adapter over the order
// module's repository at the composition root.
type TenantOrderReader interface {
	// TenantBrief reads one order of the named tenant: ok false when
	// the order is absent or another tenant's.
	TenantBrief(ctx context.Context, tenantID, orderID string) (brief OrderBrief, ok bool, err error)
	// TenantOrderIDs returns every order id of the named tenant, live
	// and tombstoned, for the feed predicate.
	TenantOrderIDs(ctx context.Context, tenantID string) ([]string, error)
}

// PickupsSyncStore is the pickups Store over will_call_pickups.
type PickupsSyncStore struct {
	pool     *pgxpool.Pool
	orders   TenantOrderReader
	sig      SignatureCapturer
	pickHold PickHoldGate
	link     pickupsLinker
}

// NewPickupsSyncStore wires the adapter: the tenant order seam, the
// signature capturer and the pick hold gate are mandatory for creates and
// confirms; nil sig refuses confirms exactly as the public path's
// ErrNoSignatureProvider does.
func NewPickupsSyncStore(pool *pgxpool.Pool, orders TenantOrderReader, sig SignatureCapturer, pickHold PickHoldGate, link pickupsLinker) *PickupsSyncStore {
	return &PickupsSyncStore{pool: pool, orders: orders, sig: sig, pickHold: pickHold, link: link}
}

// PickHoldGateWired reports whether the sync confirm carries the hold (the
// composition root's boot assertion's fact, the Service door's own
// PickHoldGateWired posture).
func (s *PickupsSyncStore) PickHoldGateWired() bool { return s.pickHold != nil }

// pickupsColumns is the pull projection: the module's own pickup legs
// plus the sync legs. The token hash is never in the projection: it is
// the credential's storage form and no device renders it.
const pickupsColumns = `id::text, order_id::text, branch_id::text, status, expires_at,
	COALESCE(signature_ref,''), COALESCE(collected_by,''), confirmed_at, revoked_at, created_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// pickupSyncRow is one scanned row plus its sync legs.
type pickupSyncRow struct {
	pickup  WillCall
	revoked *time.Time
	seq     int64
	deleted bool
}

// scanPickupSync reads the 12-target row.
func scanPickupSync(row interface{ Scan(dest ...any) error }) (pickupSyncRow, error) {
	var r pickupSyncRow
	if err := row.Scan(&r.pickup.ID, &r.pickup.OrderID, &r.pickup.BranchID, &r.pickup.Status,
		&r.pickup.ExpiresAt, &r.pickup.SignatureRef, &r.pickup.CollectedBy, &r.pickup.ConfirmedAt,
		&r.revoked, &r.pickup.CreatedAt, &r.seq, &r.deleted); err != nil {
		return pickupSyncRow{}, err
	}
	return r, nil
}

// pickupData projects one row through the module's own json tags.
func pickupData(w WillCall, revoked *time.Time) (map[string]any, error) {
	raw, err := json.Marshal(struct {
		WillCall
		RevokedAt *time.Time `json:"revoked_at,omitempty"`
	}{w, revoked})
	if err != nil {
		return nil, fmt.Errorf("dispatch: pickups projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("dispatch: pickups projection decode: %w", err)
	}
	return data, nil
}

// pickupRowStateOf folds one scanned row into the engine's RowState.
func pickupRowStateOf(r pickupSyncRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.pickup.ID, Seq: r.seq, Deleted: r.deleted}
	if r.deleted {
		return rs, nil
	}
	data, err := pickupData(r.pickup, r.revoked)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Data = data
	return rs, nil
}

// Pull returns the page of pickup changes past since for the tenant's
// orders, in seq order.
func (s *PickupsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	if !f.Tenant || f.TenantID == "" || s.orders == nil {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	ids, err := s.orders.TenantOrderIDs(ctx, f.TenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("dispatch: pickups pull order set: %w", err)
	}
	if len(ids) == 0 {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM will_call_pickups
		WHERE order_id = ANY($1::uuid[]) AND seq IS NOT NULL AND seq > $2
		ORDER BY seq ASC LIMIT $3`, pickupsColumns), ids, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("dispatch: pickups pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanPickupSync(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("dispatch: pickups pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.pickup.ID, Seq: r.seq})
			continue
		}
		rs, err := pickupRowStateOf(r)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.pickup.ID, Seq: r.seq, Data: rs.Data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("dispatch: pickups pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// row's single D30 column, scoped to the tenant's orders.
func (s *PickupsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	ids, ok := s.tenantIDs(ctx, f)
	if !ok || len(ids) == 0 {
		return sync.RowState{}, false, nil
	}
	r, err := scanPickupSync(s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM will_call_pickups
		WHERE order_id = ANY($1::uuid[]) AND idempotency_key = $2::uuid LIMIT 1`,
		pickupsColumns), ids, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: pickups find applied: %w", err)
	}
	rs, err := pickupRowStateOf(r)
	return rs, true, err
}

// Load reads one pickup by id; a pickup on another tenant's order is
// the same as an absent one.
func (s *PickupsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	ids, ok := s.tenantIDs(ctx, f)
	return s.load(ctx, s.pool, ids, ok, id)
}

// pickupsQuerier is the read surface a pool and a transaction share, so
// one loader body serves the public Load (the pool) and the callers
// that already hold a transaction (their own connection).
type pickupsQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier and an already resolved tenant order id
// set (ids, ok as tenantIDs answers). The id set comes from the order
// seam, which reads through the shared pool, so a caller that holds a
// transaction resolves it BEFORE the transaction opens and passes it in:
// neither the row read nor the set resolution then asks the pool for a
// second connection (connection starvation by second acquisition when
// pool-size callers do this together).
func (s *PickupsSyncStore) load(ctx context.Context, db pickupsQuerier, ids []string, ok bool, id string) (sync.RowState, bool, error) {
	if !ok || !looksLikeUUID(id) || len(ids) == 0 {
		return sync.RowState{}, false, nil
	}
	r, err := scanPickupSync(db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM will_call_pickups
		WHERE id = $2::uuid AND order_id = ANY($1::uuid[])`,
		pickupsColumns), ids, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: pickups load: %w", err)
	}
	rs, err := pickupRowStateOf(r)
	return rs, true, err
}

// tenantIDs resolves the feed's order id set; ok false fails closed.
func (s *PickupsSyncStore) tenantIDs(ctx context.Context, f sync.ScopeFilter) ([]string, bool) {
	if !f.Tenant || f.TenantID == "" || s.orders == nil {
		return nil, false
	}
	ids, err := s.orders.TenantOrderIDs(ctx, f.TenantID)
	if err != nil {
		return nil, false
	}
	return ids, true
}

// pickupsCreateWritable is the create set: the order the pickup is
// requested for, and the row id the client may generate (D30).
var pickupsCreateWritable = map[string]bool{"id": true, "order_id": true}

// pickupsWritable is the update set: the confirm legs. Only a confirm
// is expressible: a status leg must name CONFIRMED, the typed name is
// the collector's own words, and the affirmative is a separate required
// act exactly as the public confirm path rules it.
var pickupsWritable = map[string]bool{"id": true, "status": true, "collected_by": true, "affirmed": true}

// Create records the tenant's request for a will-call collection on
// their own order: the module's own mint rules run (will-call
// fulfillment only, the order not draft, cancelled or closed), the
// token is minted server-side and its raw form discarded (the tenant
// path never prints a tag; the desk can mint a printable one beside
// it), and the row stamps a fresh pickups seq and the idempotency key.
func (s *PickupsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	// The tenant's order id set resolves here, before the transaction
	// opens: the conflict arm reads with it from inside the transaction
	// and must not ask the order seam (a pool read) for it there.
	tenantOrderIDs, idsOK := s.tenantIDs(ctx, f)
	if !idsOK {
		return sync.RowState{}, sync.ErrOpRefused
	}
	for k := range fields {
		if !pickupsCreateWritable[k] {
			return sync.RowState{}, fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	orderID, _ := fields["order_id"].(string)
	if !looksLikeUUID(orderID) {
		return sync.RowState{}, fmt.Errorf("dispatch: order_id must be a uuid: %w", sync.ErrOpRefused)
	}
	brief, ok, err := s.orders.TenantBrief(ctx, f.TenantID, orderID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pickup order read: %w", err)
	}
	if !ok {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if brief.Fulfillment != "WILL_CALL" {
		return sync.RowState{}, fmt.Errorf("dispatch: this order is not will-call, a pickup confirms only a will-call order: %w", sync.ErrOpRefused)
	}
	switch brief.Status {
	case "DRAFT":
		return sync.RowState{}, fmt.Errorf("dispatch: confirm the order before requesting a pickup: %w", sync.ErrOpRefused)
	case "CANCELLED", "CLOSED":
		return sync.RowState{}, fmt.Errorf("dispatch: the order is %s: %w", brief.Status, sync.ErrOpRefused)
	}
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("dispatch: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	_, tokenHash, err := newPickupToken()
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pickup token mint: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin pickups create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocatePickupsSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanPickupSync(tx.QueryRow(ctx, `
		INSERT INTO will_call_pickups (id, order_id, branch_id, token_hash, expires_at, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, $4,
		        now() + $5::interval, $6, $7::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+pickupsColumns,
		id, orderID, brief.BranchID, tokenHash, defaultPickupTTL.String(), seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, own, _ := s.load(ctx, tx, tenantOrderIDs, idsOK, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The unique partial index (one ACTIVE pickup per order) or the
		// idempotency index fired: an active pickup already exists for
		// the order, which is the caller's own fact.
		return sync.RowState{}, sync.ErrRowExists
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pickups create insert: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO will_call_pickup_events (pickup_id, event) VALUES ($1, 'VIEW')`, r.pickup.ID); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pickups create event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit pickups create: %w", err)
	}
	return pickupRowStateOf(r)
}

// Write applies a confirm to one pickup: the typed name must be
// present, the affirmative leg must be true (the provider's two
// refusals, restated here so the operation refuses before the seal),
// the attestation seals through the same SignatureCapturer the public
// path uses, and the row moves ACTIVE to CONFIRMED with a fresh seq
// and the idempotency key.
func (s *PickupsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	// The tenant's order id set resolves here, before the transaction
	// opens: the order seam reads through the shared pool, so asking it
	// again from inside the transaction would take a second connection
	// while the first is held (connection starvation when pool-size
	// confirms do this together). The set is the one the refusal check
	// already used. One difference from reading it again inside the
	// transaction: an order the tenant created in between is not in the
	// set, and a failed lookup always refuses (see the pull request).
	tenantOrderIDs, idsOK := s.tenantIDs(ctx, f)
	if !idsOK {
		return sync.RowState{}, sync.ErrOpRefused
	}
	for k := range fields {
		if !pickupsWritable[k] {
			return sync.RowState{}, fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	status, hasStatus := fields["status"].(string)
	if !hasStatus || status != "CONFIRMED" {
		return sync.RowState{}, fmt.Errorf("dispatch: the only write through sync is a confirm: %w", sync.ErrOpRefused)
	}
	name, _ := fields["collected_by"].(string)
	if len(name) > maxCollectorName {
		return sync.RowState{}, fmt.Errorf("dispatch: that name is too long: %w", sync.ErrOpRefused)
	}
	if name == "" {
		return sync.RowState{}, fmt.Errorf("dispatch: type your full name to confirm this pickup: %w", sync.ErrOpRefused)
	}
	affirmed, ok := fields["affirmed"].(bool)
	if !ok || !affirmed {
		return sync.RowState{}, fmt.Errorf("dispatch: please confirm you are collecting this order before signing: %w", sync.ErrOpRefused)
	}
	if s.sig == nil {
		return sync.RowState{}, fmt.Errorf("dispatch: %v: %w", ErrNoSignatureProvider, sync.ErrOpRefused)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin pickups write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	before, err := scanPickupSync(tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM will_call_pickups
		WHERE id = $1::uuid AND order_id = ANY($2::uuid[]) AND deleted_at IS NULL FOR UPDATE`,
		pickupsColumns), id, tenantOrderIDs))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: lock pickup %q for sync write: %w", id, err)
	}
	if before.pickup.Status != "ACTIVE" {
		return sync.RowState{}, fmt.Errorf("dispatch: the pickup is %s, it cannot be confirmed again: %w", before.pickup.Status, sync.ErrOpRefused)
	}
	// Item 9 Opus P1-2: the offline confirm carries the pick hold inside the
	// confirm's own transaction (the row lock is held above, so the read and
	// the seal see one state): a held order's goods do not leave through the
	// contractor's phone either, whatever live token predates the reopened
	// balance. The read rides the transaction (HeldFromPickingTx), never a
	// second pool connection while this one is held.
	if err := refuseHeldFromPickingSyncTx(ctx, s.pickHold, tx, before.pickup.OrderID); err != nil {
		return sync.RowState{}, err
	}
	// The attestation rides THIS transaction (CaptureTypedTx): its signature row and its audit row
	// commit or roll back with the confirm, and the capture never asks the shared pool for a second
	// connection while the transaction holds this one. The pool capture here was both the
	// connection starvation at pool size and the orphan rows a rolled back confirm left behind.
	ref, err := s.sig.CaptureTypedTx(ctx, tx, TypedAttestation{
		Purpose: SignaturePurposePOD, RefType: RefTypeWillCall, RefID: id,
		TypedName: name, Affirmed: true,
	})
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pickup attestation: %w", err)
	}
	seq, err := allocatePickupsSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanPickupSync(tx.QueryRow(ctx, `
		UPDATE will_call_pickups SET
			status = 'CONFIRMED', signature_ref = $2, collected_by = $3, confirmed_at = now(),
			seq = $4, idempotency_key = $5::uuid
		WHERE id = $1::uuid AND status = 'ACTIVE'
		RETURNING `+pickupsColumns,
		id, ref, name, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, fmt.Errorf("dispatch: the pickup is no longer active: %w", sync.ErrOpRefused)
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pickups write %q: %w", id, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO will_call_pickup_events (pickup_id, event) VALUES ($1, 'CONFIRM')`, id); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pickups confirm event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit pickups write: %w", err)
	}
	return pickupRowStateOf(r)
}

// Delete tombstones and revokes one pickup: the tombstone is the sync
// fact, REVOKED the business one (a revoked square is spent, 410 on the
// public leg), both with one fresh seq.
func (s *PickupsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	_ = key
	ids, ok := s.tenantIDs(ctx, f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, sync.ErrOpRefused
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin pickups delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocatePickupsSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanPickupSync(tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE will_call_pickups SET
			deleted_at = now(), revoked_at = COALESCE(revoked_at, now()),
			status = CASE WHEN status = 'ACTIVE' THEN 'REVOKED' ELSE status END,
			seq = $3
		WHERE id = $1::uuid AND order_id = ANY($2::uuid[]) AND deleted_at IS NULL
		RETURNING %s`, pickupsColumns), id, ids, seq))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pickups delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit pickups delete: %w", err)
	}
	rs, err := pickupRowStateOf(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}
