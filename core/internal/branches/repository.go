package branches

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNoDB = errors.New("branches: database not configured")

// Repository reads the seeded branch locations (0003/0017) + the default branch setting
// (0014). It owns no table of its own; these are read-only SELECTs against locations and
// system_settings.
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// pgxQuerier is the read surface DefaultBranchID needs, satisfied by both *pgxpool.Pool and
// pgx.Tx, so the default-branch read can run either standalone (pool) or inside List's snapshot
// transaction (tx) without duplicating the decode logic.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DefaultBranchID reads system_settings.default_branch_id (migration 0014). Returns "" and
// no error when the setting is absent, a default is optional, not required, so callers can
// still list branches. The value is stored as a JSON string, so it is decoded as such.
func (r *Repository) DefaultBranchID(ctx context.Context) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	return defaultBranchID(ctx, r.pool)
}

// defaultBranchID is the querier-parameterised body shared by the standalone read (pool) and
// List's transactional read (tx), so both decode default_branch_id identically.
func defaultBranchID(ctx context.Context, q pgxQuerier) (string, error) {
	var raw []byte
	err := q.QueryRow(ctx,
		`SELECT value FROM system_settings WHERE key = 'default_branch_id'`).Scan(&raw)
	if err != nil {
		// Only a missing row means "no default configured" (optional, not an error for the
		// list path). Any other query/scan error (connection loss, decode failure, …) must
		// propagate, swallowing it would silently flag every branch is_default=false and
		// hide a real DB fault. Fail loud.
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("branches: read default_branch_id: %w", err)
	}
	var id string
	if len(raw) > 0 {
		// value is JSONB holding a JSON string ("5d1b3f6e-…"); trim the quotes safely.
		if uerr := json.Unmarshal(raw, &id); uerr != nil {
			return "", fmt.Errorf("branches: decode default_branch_id: %w", uerr)
		}
	}
	return id, nil
}

// List returns both branches (locations rows with loc_type='BRANCH'), each flagged with
// whether it is the system default. Ordered by code (KGN, TRT) so the response is stable.
func (r *Repository) List(ctx context.Context) ([]Branch, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// The default-branch setting and the branch locations were previously read in two separate
	// round-trips against the pool. Between them a concurrent write to system_settings could land,
	// so the is_default flag could be computed against a default_branch_id that never coexisted
	// with the locations snapshot. Run both reads inside ONE read-only transaction with a
	// repeatable-read snapshot so is_default is a consistent point-in-time view.
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
		IsoLevel:   pgx.RepeatableRead,
	})
	if err != nil {
		return nil, fmt.Errorf("branches: list begin: %w", err)
	}
	defer tx.Rollback(ctx) // read-only; rollback is the correct close for a snapshot read

	defaultID, err := defaultBranchID(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, code::text, name, address, is_active
		FROM locations
		WHERE loc_type = 'BRANCH'
		ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("branches: list: %w", err)
	}
	defer rows.Close()

	var out []Branch
	for rows.Next() {
		var b Branch
		var addr []byte
		if err := rows.Scan(&b.ID, &b.Code, &b.Name, &addr, &b.IsActive); err != nil {
			return nil, fmt.Errorf("branches: scan: %w", err)
		}
		if len(addr) > 0 {
			if err := json.Unmarshal(addr, &b.Address); err != nil {
				return nil, fmt.Errorf("branches: decode address for %s: %w", b.ID, err)
			}
		}
		b.IsDefault = defaultID != "" && b.ID == defaultID
		out = append(out, b)
	}
	return out, rows.Err()
}

// Exists reports whether `id` names a branch row this dealer actually has.
//
// -- WHY THIS READ LIVES HERE AND WHY IT IS SHAPED LIKE THIS -------------------------------------
//
// It is the fifth gate on a widget's branch narrowing, and it is the only one that needs a table.
// Commit 7027df5 closed SHAPE (dashboard/widget.go IsBranchID, whose own header says "Shape only"),
// GRANT (BranchGate.Allows, widget.go:290) and NARROWING (narrowToBranch); none of the three can
// answer "does locations hold this row", because none of them may read locations. This package
// already does, List reads `WHERE loc_type='BRANCH'` at :91-95 and DefaultBranchID reads
// system_settings at :45-46, and it owns no table of its own, so a fourth read of a table it is
// already the reader of adds no owner to anything.
//
// IT IS STRICTER THAN THE FOREIGN KEY, ON PURPOSE. `dashboard_user_widgets.branch_id` is
// `uuid REFERENCES locations (id)` (migration 0201), and `locations` holds ZONE and BIN rows as well
// as branches (0003). A zone id would satisfy the constraint and store cleanly, and the widget would
// then narrow a branch filter to something that is not a branch, a card that measures nothing,
// forever, with no error anywhere to explain it. `loc_type = 'BRANCH'` is what the caller meant when
// they picked a branch, so it is what this answers.
//
// IT COMPARES AS TEXT RATHER THAN CASTING TO uuid, and that is the difference between a refusal and
// the 500 this whole seam exists to remove. `WHERE id = $1::uuid` raises 22P02 for any string that
// is not a uuid, so a caller who reached this read with a malformed id would get the identical
// "internal error" the FK violation used to produce, the defect, moved one layer up. `id::text`
// cannot raise for ANY input, so every unknown id, well formed or not, comes back as a plain false
// that the caller can turn into words. The index cost is nil: this table holds two rows.
//
// An error is NOT an answer. A caller that cannot reach the database gets (false, err) and must
// decide for itself what to do about that, see dashboard's refuseUnknownBranches, which degrades to
// the pre-existing behaviour rather than refusing a save because a lookup blinked.
func (r *Repository) Exists(ctx context.Context, id string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	if id == "" {
		// "" is the widget substrate's "every branch I can see" and is never a row. Answering it
		// here would make an empty string look like a missing branch to a caller that forgot to
		// skip it; the honest answer is "no such row", and every caller already special-cases "".
		return false, nil
	}
	var ok bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM locations WHERE id::text = $1 AND loc_type = 'BRANCH')`,
		id).Scan(&ok); err != nil {
		return false, fmt.Errorf("branches: exists %s: %w", id, err)
	}
	return ok, nil
}
