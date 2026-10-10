// syncscope_pod.go is the pod Store (manual: never overwrites, the
// server row and the review marker returned). The rows are
// delivery_pod_photos, the proof-of-delivery records the driver
// captures at a stop; the projection is the contract's
// DispatchPhotoMeta schema, a POD record WITHOUT its bytes, because
// the bytes come only from the per-photo route (0091's own seam rule)
// and a sync payload that carried them would put evidence in every
// queue and cache the board never asked to hold.
//
// A pushed create carries the photo's bytes base64 under
// image_bytes_b64: the row cannot exist without them (image_bytes is
// NOT NULL and the 0091 CHECK binds byte_size to the real length), the
// frozen schema keeps the bytes in Postgres rather than an object
// store, and the push payload is the contract's free-form object. The
// pull projection never carries them, and no update path can change
// them: a captured proof is a fact, not a field.
//
// The org fence rides the owning stop (delivery_pod_photos carries no
// org column, 0213 stamped it with the sync legs only): a photo is in
// scope exactly when the stop it proves sits in the caller's org, the
// module's own tables answering the scope question with no join past
// the fence.
package dispatch

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// PODSyncStore serves the pod scope over delivery_pod_photos.
type PODSyncStore struct {
	pool *pgxpool.Pool
	link syncLinker
	role SyncRoleResolver
}

// NewPODSyncStore wires the adapter; a nil linker is legal only where
// actor attribution will not run (tests).
func NewPODSyncStore(pool *pgxpool.Pool, link syncLinker) *PODSyncStore {
	return &PODSyncStore{pool: pool, link: link}
}

// WithDriverRole wires the driver fence's role seam; the composition
// test in cmd/core pins that it did.
func (s *PODSyncStore) WithDriverRole(role SyncRoleResolver) *PODSyncStore {
	s.role = role
	return s
}

// DriverRoleWired answers whether the driver fence's role seam is
// wired, the leg the composition test pins.
func (s *PODSyncStore) DriverRoleWired() bool { return s.role != nil }

// driverFence resolves the caller's driver arm over the pool. Every
// caller resolves it BEFORE a transaction opens (Create, Write, Delete)
// or with none held (Pull, FindApplied, Load) and passes the answer
// down: the loader runs inside an open transaction and must not ask
// the pool for a second connection (the pool-cycle rule the create
// census keeps).
func (s *PODSyncStore) driverFence(ctx context.Context, f sync.ScopeFilter) (driverFence, error) {
	return resolveDriverFence(ctx, s.pool, s.link, s.role, f)
}

// podSyncQuery is the photo metadata projection plus the two sync
// legs, fenced on the owning stop's org stamp.
const podSyncQuery = `
SELECT p.id::text, p.stop_id::text, p.content_type, p.byte_size, p.caption, p.captured_at,
       p.seq, (p.deleted_at IS NOT NULL) AS deleted
  FROM delivery_pod_photos p`

// podScopePredicate is the WHERE predicate every pod adapter query
// runs under: the owning stop must sit in the caller's org. A tenant
// filter fails closed (pod is an org scope). A driver caller carries
// the driver arm inside the same EXISTS: the stop must sit on a run
// assigned to them, so a colleague's proof is the same as absent, and
// an unlinked driver login reads nothing at all.
func podScopePredicate(f sync.ScopeFilter, d driverFence) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	switch {
	case !d.driver:
		return `EXISTS (SELECT 1 FROM delivery_stops s
		          WHERE s.id = p.stop_id AND s.org_id = $1::uuid)`, []any{f.OrgID}, true
	case d.driverID == "":
		return "FALSE", nil, true
	default:
		return `EXISTS (SELECT 1 FROM delivery_stops s
		          JOIN delivery_runs r ON r.id = s.run_id
		          WHERE s.id = p.stop_id AND s.org_id = $1::uuid AND r.driver_id = $2::uuid)`,
			[]any{f.OrgID, d.driverID}, true
	}
}

// syncPodRow is one scanned row of the pod sync projection.
type syncPodRow struct {
	meta    PhotoMeta
	seq     int64
	deleted bool
}

// scanSyncPod reads the projection's targets in order.
func scanSyncPod(row pgx.Row) (syncPodRow, error) {
	var m PhotoMeta
	var seq int64
	var deleted bool
	if err := row.Scan(&m.ID, &m.StopID, &m.ContentType, &m.ByteSize, &m.Caption, &m.CapturedAt,
		&seq, &deleted); err != nil {
		return syncPodRow{}, err
	}
	return syncPodRow{meta: m, seq: seq, deleted: deleted}, nil
}

// podStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func podStateOf(r syncPodRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.meta.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := projection(r.meta)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// Pull returns the page of POD-record changes past since, live rows
// and tombstones in seq order, at most limit rows total, under the org
// predicate.
func (s *PODSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	d, err := s.driverFence(ctx, f)
	if err != nil {
		return nil, nil, err
	}
	where, args, ok := podScopePredicate(f, d)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := podSyncQuery + `
		WHERE ` + where + ` AND p.seq IS NOT NULL AND p.seq > ` + syncPH(len(args)+1) + `
		ORDER BY p.seq ASC
		LIMIT ` + syncPH(len(args)+2)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("dispatch: pod sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncPod(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("dispatch: pod sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.meta.ID, Seq: r.seq})
			continue
		}
		data, err := projection(r.meta)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.meta.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("dispatch: pod sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied, the
// column holding the latest live key and a tombstone keeping it (the
// design note in syncscope.go's header).
func (s *PODSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	d, err := s.driverFence(ctx, f)
	if err != nil {
		return sync.RowState{}, false, err
	}
	where, args, ok := podScopePredicate(f, d)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := podSyncQuery + `
		WHERE ` + where + ` AND p.idempotency_key = ` + syncPH(len(args)+1) + `::uuid
		LIMIT 1`
	args = append(args, key)
	r, err := scanSyncPod(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: pod sync find applied: %w", err)
	}
	rs, err := podStateOf(r)
	return rs, true, err
}

// Load reads one POD record by id under the org predicate.
func (s *PODSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	d, err := s.driverFence(ctx, f)
	if err != nil {
		return sync.RowState{}, false, err
	}
	return s.load(ctx, s.pool, f, d, id)
}

// podQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type podQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *PODSyncStore) load(ctx context.Context, db podQuerier, f sync.ScopeFilter, d driverFence, id string) (sync.RowState, bool, error) {
	where, args, ok := podScopePredicate(f, d)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := podSyncQuery + `
		WHERE p.id = ` + syncPH(len(args)+1) + `::uuid AND ` + where
	args = append(args, id)
	r, err := scanSyncPod(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: pod sync load: %w", err)
	}
	rs, err := podStateOf(r)
	return rs, true, err
}

// podCreateFields is the field set a pushed create may declare: the
// stop it proves, the photo's content type, its bytes base64, and the
// optional caption. The captured_at time and the branch are the
// server's to stamp, never the payload's to claim.
func podCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "stop_id", "content_type", "image_bytes_b64", "caption":
		default:
			return fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// decodePhotoBytes validates and decodes the create's base64 bytes:
// the content type must be one of 0091's three, the bytes non-empty
// and within the module's own bound (MaxPhotoBytes, the bound the
// database CHECK backstops), the declared size never trusted over the
// real length.
func decodePhotoBytes(contentType, encoded string) ([]byte, error) {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if !podContentTypes[contentType] {
		return nil, fmt.Errorf("dispatch: a proof photo must be a jpeg, png or webp: %w", sync.ErrOpRefused)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("dispatch: image_bytes_b64 is not valid base64: %w", sync.ErrOpRefused)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("dispatch: the photo is empty: %w", sync.ErrOpRefused)
	}
	if len(data) > MaxPhotoBytes {
		return nil, fmt.Errorf("dispatch: the photo is too large (max %d MB): %w", MaxPhotoBytes>>20, sync.ErrOpRefused)
	}
	return data, nil
}

// Create inserts one POD record from a pushed create: the stop must
// sit in the caller's org and be a stop with something to photograph
// (EN_ROUTE, DELIVERED or FAILED, AddPhoto's own rule; a photo before
// the truck left is not proof of delivery), the bytes validate and
// ride in as the row's image_bytes with byte_size stamped from the
// real length, and the row stamps the stop's branch, the caller's
// linked user as the capturer, a freshly allocated seq and the
// idempotency key.
func (s *PODSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	d, err := s.driverFence(ctx, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := podScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := podCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	stopID, _ := fields["stop_id"].(string)
	if !looksLikeUUID(stopID) {
		return sync.RowState{}, fmt.Errorf("dispatch: stop_id must be a uuid: %w", sync.ErrOpRefused)
	}
	var branchID, stopStatus string
	readArm, readArg := d.leg(stopDriverCol, 3)
	err = s.pool.QueryRow(ctx,
		`SELECT branch_id::text, status FROM delivery_stops
		 WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL`+readArm,
		stopID, f.OrgID, readArg).Scan(&branchID, &stopStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pod sync create stop read: %w", err)
	}
	switch stopStatus {
	case StopEnRoute, StopDelivered, StopFailed:
	default:
		return sync.RowState{}, refused(fmt.Errorf("a %s stop has nothing to photograph yet", stopStatus))
	}
	contentType, _ := fields["content_type"].(string)
	encoded, _ := fields["image_bytes_b64"].(string)
	data, err := decodePhotoBytes(contentType, encoded)
	if err != nil {
		return sync.RowState{}, err
	}
	caption, _ := fields["caption"].(string)
	id, err := syncCreateID(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	actor := syncActor(ctx, s.link, f.Actor)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin pod sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSyncSeqTx(ctx, tx, podSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	var rowID string
	err = tx.QueryRow(ctx, `
		INSERT INTO delivery_pod_photos (id, stop_id, branch_id, content_type, byte_size, image_bytes, caption, captured_by, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, $4, $5, $6, $7, NULLIF($8,'')::uuid, $9, $10::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING id::text`,
		id, stopID, branchID, strings.ToLower(strings.TrimSpace(contentType)), len(data), data,
		strings.TrimSpace(caption), actor, seq, key).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, own, _ := s.load(ctx, tx, f, d, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pod sync create insert: %w", err)
	}
	r, err := scanSyncPod(tx.QueryRow(ctx, podSyncQuery+` WHERE p.id = $1::uuid`, rowID))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pod sync create read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit pod sync create: %w", err)
	}
	return podStateOf(r)
}

// podWriteFields is the field set a pushed update may declare on a
// POD record: the caption alone. The photo's bytes, its stop and its
// capture facts are not editable through any path, sync included: an
// attestation with a name attached must not imply more than it
// captured, which is the standing rule that made this scope manual.
func podWriteFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "caption":
		default:
			return fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// Write applies exactly the declared caption to one POD record, on a
// FOR UPDATE-locked pre-image, drawing a fresh seq and stamping the
// idempotency key. The manual conflict policy above this store decides
// what a stale write does; this method only ever runs for a write the
// engine judged fresh.
func (s *PODSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	d, err := s.driverFence(ctx, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := podScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := podWriteFields(fields); err != nil {
		return sync.RowState{}, err
	}
	caption, hasCaption := fields["caption"].(string)
	if !hasCaption {
		return sync.RowState{}, refused(fmt.Errorf("an update that moves nothing refuses"))
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin pod sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var fromCaption string
	preArm, preArg := d.leg(podDriverCol, 3)
	err = tx.QueryRow(ctx,
		`SELECT p.caption FROM delivery_pod_photos p
		 WHERE p.id = $1::uuid AND p.deleted_at IS NULL
		   AND EXISTS (SELECT 1 FROM delivery_stops s WHERE s.id = p.stop_id AND s.org_id = $2::uuid)`+preArm+`
		 FOR UPDATE OF p`,
		id, f.OrgID, preArg).Scan(&fromCaption)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: lock pod %q for sync write: %w", id, err)
	}
	if hasCaption && strings.TrimSpace(caption) == fromCaption {
		// The declared caption is already the record's caption (a
		// superseded replay of one constant re-caption meets this
		// arm): answer applied with the record as it stands, stamping
		// nothing, so a replay cannot move a captured proof's feed
		// position.
		r, err := scanSyncPod(tx.QueryRow(ctx, podSyncQuery+` WHERE p.id = $1::uuid`, id))
		if err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: pod sync write read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("dispatch: commit pod sync write: %w", err)
		}
		return podStateOf(r)
	}
	seq, err := allocateSyncSeqTx(ctx, tx, podSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	// The org fence rides the UPDATE's own WHERE (the owning stop's org
	// stamp), so the row is fenced atomically, not check-then-write; the
	// driver arm rides with it through the stop's run.
	setArm, setArg := d.leg(podDriverCol, 7)
	r, err := scanSyncPod(tx.QueryRow(ctx, `
		UPDATE delivery_pod_photos p SET
			caption          = CASE WHEN $2::bool THEN $3 ELSE p.caption END,
			seq              = $4,
			idempotency_key  = $5::uuid
		WHERE p.id = $1::uuid AND p.deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM delivery_stops s WHERE s.id = p.stop_id AND s.org_id = $6::uuid)`+setArm+`
		RETURNING p.id::text, p.stop_id::text, p.content_type, p.byte_size, p.caption, p.captured_at,
		          p.seq, (p.deleted_at IS NOT NULL)`,
		id, hasCaption, strings.TrimSpace(caption), seq, key, f.OrgID, setArg))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pod sync write %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit pod sync write: %w", err)
	}
	return podStateOf(r)
}

// Delete tombstones one POD record: deleted_at set, the row retained
// (the bytes stay under the table's own fence), a fresh seq, the live
// row's idempotency key kept (syncscope.go's header).
func (s *PODSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	d, err := s.driverFence(ctx, f)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, _, ok := podScopePredicate(f, d); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: begin pod sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSyncSeqTx(ctx, tx, podSyncScope)
	if err != nil {
		return sync.RowState{}, err
	}
	delArm, delArg := d.leg(podDriverCol, 4)
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE delivery_pod_photos p SET
			deleted_at = now(),
			seq        = $3
		WHERE p.id = $1::uuid AND p.deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM delivery_stops s WHERE s.id = p.stop_id AND s.org_id = $2::uuid)`+delArm+`
		RETURNING (p.deleted_at IS NOT NULL)`, id, f.OrgID, seq, delArg).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: pod sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: commit pod sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}
