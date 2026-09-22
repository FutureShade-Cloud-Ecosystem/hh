package bistrack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// repository.go, DIB-53 phase 0 persistence for sync_jobs / sync_discrepancies / bistrack_id_map (0079).
// These are present + tested so the tables and the RESUMABLE/idempotent shapes are proven, NOT because
// anything drives them this phase: no route and no worker reaches a write path that would start a sync
// (the structural guarantee). A nil pool no-ops every method (scaffold/offline mode).

var errNoDB = errors.New("bistrack: database not configured")

// ErrCrosswalkConflict is returned by UpsertIDMapping when a mapping would violate the crosswalk's
// SECOND uniqueness (entity_type, native_id), i.e. a native entity already maps to a DIFFERENT BisTrack
// id (a many-to-one, e.g. merged customers). The caller records it as a KindMappingError discrepancy
// rather than letting the crosswalk silently lie (supervisor C3). Distinct from a benign idempotent
// re-upsert of the SAME pair, which succeeds.
var ErrCrosswalkConflict = errors.New("bistrack: id-map conflict, native id already mapped to a different BisTrack id")

// Repository owns the three 0079 tables (pgx).
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// -- sync_jobs ----------------------------------------------------------------------------------------

// InsertJob writes a new sync_jobs row (typically PENDING) and returns its id. The composite cursor rides
// as (cursor_ts, cursor_last_id).
func (r *Repository) InsertJob(ctx context.Context, j SyncJob) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	if !j.EntityType.valid() {
		return "", fmt.Errorf("bistrack: unknown entity_type %q", j.EntityType)
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO sync_jobs (entity_type, direction, status, cursor_ts, cursor_last_id,
		                       rows_read, rows_written, discrepancies, error,
		                       scheduled_at, started_at, finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id::text`,
		j.EntityType, j.Direction, statusOrDefault(j.Status), nullTime(j.Cursor.UpdatedAt), j.Cursor.LastID,
		j.RowsRead, j.RowsWritten, j.Discrepancies, j.Error,
		j.ScheduledAt, j.StartedAt, j.FinishedAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("bistrack: insert sync job: %w", err)
	}
	return id, nil
}

// LatestJob returns the most recent job across all entities (for the status surface), or (nil, nil) when
// the table is empty, phase 0's honest "last_sync: null".
func (r *Repository) LatestJob(ctx context.Context) (*SyncJob, error) {
	if r.pool == nil {
		return nil, nil
	}
	row := r.pool.QueryRow(ctx, `
		SELECT id::text, entity_type, direction, status, cursor_ts, cursor_last_id,
		       rows_read, rows_written, discrepancies, error, scheduled_at, started_at, finished_at, created_at
		  FROM sync_jobs ORDER BY created_at DESC, id DESC LIMIT 1`)
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bistrack: latest job: %w", err)
	}
	return &j, nil
}

func scanJob(row pgx.Row) (SyncJob, error) {
	var (
		j          SyncJob
		cursorTS   *time.Time
		sched, sta *time.Time
		fin        *time.Time
	)
	if err := row.Scan(&j.ID, &j.EntityType, &j.Direction, &j.Status, &cursorTS, &j.Cursor.LastID,
		&j.RowsRead, &j.RowsWritten, &j.Discrepancies, &j.Error, &sched, &sta, &fin, &j.CreatedAt); err != nil {
		return SyncJob{}, err
	}
	if cursorTS != nil {
		j.Cursor.UpdatedAt = *cursorTS
	}
	j.ScheduledAt, j.StartedAt, j.FinishedAt = sched, sta, fin
	return j, nil
}

// -- sync_discrepancies -------------------------------------------------------------------------------

// InsertDiscrepancy records one unreconciled fact. detail is JSON-encoded (allowlisted, no raw PII).
func (r *Repository) InsertDiscrepancy(ctx context.Context, d SyncDiscrepancy) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	detail, err := json.Marshal(orEmptyMap(d.Detail))
	if err != nil {
		return "", fmt.Errorf("bistrack: marshal discrepancy detail: %w", err)
	}
	var id string
	err = r.pool.QueryRow(ctx, `
		INSERT INTO sync_discrepancies (sync_job_id, entity_type, bistrack_id, native_id, kind, detail)
		VALUES (NULLIF($1,'')::uuid, $2, $3, NULLIF($4,'')::uuid, $5, $6)
		RETURNING id::text`,
		d.SyncJobID, d.EntityType, d.BisTrackID, d.NativeID, d.Kind, detail).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("bistrack: insert discrepancy: %w", err)
	}
	return id, nil
}

// OpenDiscrepancyCount counts unresolved discrepancies (the status surface's number). 0 on a nil pool.
func (r *Repository) OpenDiscrepancyCount(ctx context.Context) (int64, error) {
	if r.pool == nil {
		return 0, nil
	}
	var n int64
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM sync_discrepancies WHERE resolved_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("bistrack: open discrepancy count: %w", err)
	}
	return n, nil
}

// -- bistrack_id_map (the crosswalk) ------------------------------------------------------------------

// UpsertIDMapping records/refreshes one crosswalk row. It is idempotent on (entity_type, bistrack_id) -
// re-upserting the SAME pair succeeds. But it FAILS with ErrCrosswalkConflict when it would break the
// second uniqueness (entity_type, native_id): a native entity already mapped to a DIFFERENT BisTrack id
// (a many-to-one). The caller turns that into a KindMappingError discrepancy (C3) rather than a silent
// duplicate.
func (r *Repository) UpsertIDMapping(ctx context.Context, m IDMapping) error {
	if r.pool == nil {
		return errNoDB
	}
	// ON CONFLICT on the bistrack_id key makes the same-pair re-upsert idempotent; a native_id collision
	// with a different bistrack_id is NOT covered by that conflict target, so it surfaces as a 23505 on
	// the (entity_type, native_id) unique, which we translate to the typed conflict error.
	_, err := r.pool.Exec(ctx, `
		INSERT INTO bistrack_id_map (entity_type, bistrack_id, native_id)
		VALUES ($1, $2, $3::uuid)
		ON CONFLICT (entity_type, bistrack_id)
		DO UPDATE SET native_id = EXCLUDED.native_id, updated_at = now()`,
		m.EntityType, m.BisTrackID, m.NativeID)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrCrosswalkConflict
		}
		return fmt.Errorf("bistrack: upsert id map: %w", err)
	}
	return nil
}

// Resolve looks up the native uuid for a BisTrack id (satisfies IDResolver over the crosswalk). ok=false
// on no row or a nil pool.
func (r *Repository) Resolve(entity EntityType, bistrackID string) (string, bool) {
	if r.pool == nil {
		return "", false
	}
	var nativeID string
	err := r.pool.QueryRow(context.Background(),
		`SELECT native_id::text FROM bistrack_id_map WHERE entity_type = $1 AND bistrack_id = $2`,
		entity, bistrackID).Scan(&nativeID)
	if err != nil {
		return "", false
	}
	return nativeID, true
}

// -- helpers ------------------------------------------------------------------------------------------

func statusOrDefault(s SyncStatus) SyncStatus {
	if s == "" {
		return StatusPending
	}
	return s
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
