package audit

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Execer is the write surface shared by *pgxpool.Pool and pgx.Tx, so a sibling module can have an audit
// row written on ITS OWN transaction, the DIB-175 order-edit path does this so the ORDER_CHANGED rows
// commit or roll back atomically with the line write (B2-1). Both pgx types satisfy it structurally.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Repo is the audit_log access layer. Insert is the ONLY write method, there is NO Update/Delete, so
// the trail is append-only by construction (US-E1).
type Repo struct{ pool *pgxpool.Pool }

// NewRepository wraps a pool (may be nil in scaffold/offline mode).
func NewRepository(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// entryCols is the projection shared by every READ. entity/actor/branch ids and request_id are
// COALESCE'd to " so NULLs surface as empty strings (mirrors the pricing audit reader). seq (0078) is
// read-only here and is deliberately ABSENT from the INSERT column list below, it is GENERATED ALWAYS,
// so Postgres assigns it and rejects any explicit value (R4).
const entryCols = `id::text, action, entity_type,
	COALESCE(entity_id::text,''), COALESCE(actor_id::text,''), COALESCE(branch_id::text,''),
	changes, COALESCE(request_id,''), created_at, seq`

// insertOn writes one row on the given Execer (pool or a caller's tx). The INSERT column list omits seq
// (GENERATED ALWAYS, R4). Empty actor/entity/branch ids become SQL NULL.
func insertOn(ctx context.Context, q Execer, e Entry) error {
	_, err := q.Exec(ctx, `
		INSERT INTO audit_log (action, entity_type, entity_id, actor_id, branch_id, changes, request_id)
		VALUES ($1, $2, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid, NULLIF($5,'')::uuid, $6, $7)`,
		e.Action, e.EntityType, e.EntityID, e.ActorID, e.BranchID, e.Changes, e.RequestID)
	return err
}

// Insert writes one row on the pool. Best-effort on a nil pool (a missing DB must never fail the wrapped
// request), the middleware and every pool-based Recorder caller relies on that.
func (r *Repo) Insert(ctx context.Context, e Entry) error {
	if r.pool == nil {
		return nil
	}
	return insertOn(ctx, r.pool, e)
}

// InsertTx writes one row on a caller-supplied Execer (a pgx.Tx). Unlike Insert there is NO nil-guard:
// a transaction is always a real handle, and the whole point (B2-1) is that a failure here propagates so
// the caller's transaction rolls back rather than committing a mutation with no trail.
func (r *Repo) InsertTx(ctx context.Context, q Execer, e Entry) error {
	return insertOn(ctx, q, e)
}

// List powers the admin viewer (newest-first, filtered over the 0013 indexes). Limit is clamped to
// 1..500 (default 200) so a viewer query can never table-scan the whole trail.
func (r *Repo) List(ctx context.Context, f Filter) ([]Entry, error) {
	if r.pool == nil {
		return []Entry{}, nil
	}
	q := `SELECT ` + entryCols + `
		FROM audit_log
		WHERE ($1 = '' OR entity_type = $1)
		  AND ($2 = '' OR action = $2)
		  AND ($3 = '' OR actor_id = $3::uuid)
		  AND ($4 = '' OR created_at >= $4::timestamptz)
		ORDER BY created_at DESC, id DESC
		LIMIT $5`
	rows, err := r.pool.Query(ctx, q, f.EntityType, f.Action, f.ActorID, f.Since, clampLimit(f.Limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEntries(rows)
}

// ListByEntity powers the operator reopen path (one transaction's chronological trail, oldest-first).
// It is UNFENCED by design: its callers are in-process seams that gate on the owning entity before
// they ask. The route's fenced read is ListByEntityFenced below.
func (r *Repo) ListByEntity(ctx context.Context, entityType, entityID string) ([]Entry, error) {
	if r.pool == nil {
		return []Entry{}, nil
	}
	q := `SELECT ` + entryCols + `
		FROM audit_log
		WHERE entity_type = $1 AND entity_id = $2::uuid
		ORDER BY created_at ASC, id ASC`
	rows, err := r.pool.Query(ctx, q, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEntries(rows)
}

// ListByEntityFenced is ListByEntity with the branch fence a scoped caller needs (HH-180): rows in
// the given branches plus branch-less rows, oldest-first. The fence is a predicate on the query
// itself, never a post-filter, and an empty branch set yields the branch-less rows only, so a scoped
// caller with no grants fails closed with no special case. Branch-less rows stay in because the
// coarse middleware rows carry no branch and they are the spine of every entity's trail.
func (r *Repo) ListByEntityFenced(ctx context.Context, entityType, entityID string, branches []string) ([]Entry, error) {
	if r.pool == nil {
		return []Entry{}, nil
	}
	if branches == nil {
		branches = []string{}
	}
	q := `SELECT ` + entryCols + `
		FROM audit_log
		WHERE entity_type = $1 AND entity_id = $2::uuid
		  AND (branch_id IS NULL OR branch_id = ANY($3::uuid[]))
		ORDER BY created_at ASC, id ASC`
	rows, err := r.pool.Query(ctx, q, entityType, entityID, branches)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEntries(rows)
}

// GetByID fetches a single row for the admin deep viewer. Read-only; returns (nil, nil) when absent.
func (r *Repo) GetByID(ctx context.Context, id string) (*Entry, error) {
	if r.pool == nil {
		return nil, nil
	}
	e, err := scanEntry(r.pool.QueryRow(ctx, `SELECT `+entryCols+` FROM audit_log WHERE id = $1::uuid`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// -- scanning ------------------------------------------------------------------

func collectEntries(rows pgx.Rows) ([]Entry, error) {
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEntry(row pgx.Row) (Entry, error) {
	var (
		e         Entry
		changes   []byte
		createdAt time.Time
	)
	if err := row.Scan(&e.ID, &e.Action, &e.EntityType,
		&e.EntityID, &e.ActorID, &e.BranchID,
		&changes, &e.RequestID, &createdAt, &e.Seq); err != nil {
		return Entry{}, err
	}
	e.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	if len(changes) > 0 {
		_ = json.Unmarshal(changes, &e.Changes)
	}
	if e.Changes == nil {
		e.Changes = map[string]any{}
	}
	// Lift actor_role out of the coarse-trail changes blob into a first-class column for the viewer.
	if ar, ok := e.Changes["actor_role"].(string); ok {
		e.ActorRole = ar
	}
	return e, nil
}

// clampLimit bounds a viewer limit to 1..500 with a 200 default.
func clampLimit(n int) int {
	switch {
	case n <= 0:
		return 200
	case n > 500:
		return 500
	default:
		return n
	}
}
