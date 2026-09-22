package settings

// store.go, pgx persistence over the 0172 substrate. Role-profile resolution reads the 0140
// dashboard_role_profiles table (one role→shape truth program-wide; a second mapping for the same
// person is exactly the drift 0149 ruled against).

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the kit's pgx store.
type Store struct{ pool *pgxpool.Pool }

// NewStore builds the store. A nil pool is allowed in scaffolds; every method then errors.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var errNoPool = errors.New("settings: store has no database pool")

// ProfileFor maps a role code to its 0140 job shape. "" (no row) is an ordinary answer, the
// service applies the least-privileged fallback, never an error.
func (s *Store) ProfileFor(ctx context.Context, roleCode string) (string, error) {
	if s.pool == nil {
		return "", errNoPool
	}
	var profile string
	err := s.pool.QueryRow(ctx,
		`SELECT profile FROM dashboard_role_profiles WHERE role_code = $1`, roleCode).Scan(&profile)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return profile, err
}

func scanItems(rows pgx.Rows) ([]Item, error) {
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Kind, &it.ID, &it.Position, &it.Visible); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ReadRoleItems lists the dealer's item set for one surface+profile, kind-then-position ordered.
func (s *Store) ReadRoleItems(ctx context.Context, surfaceID, profile string) ([]Item, error) {
	if s.pool == nil {
		return nil, errNoPool
	}
	rows, err := s.pool.Query(ctx, `
		SELECT item_kind, item_id, position, visible
		  FROM surface_role_items
		 WHERE surface_id = $1 AND profile = $2
		 ORDER BY item_kind, position, item_id`, surfaceID, profile)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

// ReplaceRoleItems swaps the dealer's set for the (surface, profile, KINDS PRESENT IN THE SET) in
// one transaction: kinds not present in the write are left alone, so curating the columns cannot
// silently erase the action bar.
func (s *Store) ReplaceRoleItems(ctx context.Context, surfaceID, profile string, items []Item, updatedBy string) error {
	if s.pool == nil {
		return errNoPool
	}
	kinds := map[string]bool{}
	for _, it := range items {
		kinds[it.Kind] = true
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for k := range kinds {
		if _, err := tx.Exec(ctx,
			`DELETE FROM surface_role_items WHERE surface_id = $1 AND profile = $2 AND item_kind = $3`,
			surfaceID, profile, k); err != nil {
			return err
		}
	}
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,'')::uuid)`,
			surfaceID, profile, it.Kind, it.ID, it.Position, it.Visible, updatedBy); err != nil {
			return fmt.Errorf("settings: write role item %s/%s: %w", it.Kind, it.ID, err)
		}
	}
	return tx.Commit(ctx)
}

// ReadUserItems lists one person's override rows for one surface.
func (s *Store) ReadUserItems(ctx context.Context, userID, surfaceID string) ([]Item, error) {
	if s.pool == nil {
		return nil, errNoPool
	}
	rows, err := s.pool.Query(ctx, `
		SELECT item_kind, item_id, position, visible
		  FROM surface_user_items
		 WHERE user_id = $1 AND surface_id = $2
		 ORDER BY item_kind, position, item_id`, userID, surfaceID)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

// ReplaceUserItems swaps one person's override for the (surface, kinds present), the same
// per-kind whole-or-absent contract as the role write.
func (s *Store) ReplaceUserItems(ctx context.Context, userID, surfaceID string, items []Item) error {
	if s.pool == nil {
		return errNoPool
	}
	kinds := map[string]bool{}
	for _, it := range items {
		kinds[it.Kind] = true
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for k := range kinds {
		if _, err := tx.Exec(ctx,
			`DELETE FROM surface_user_items WHERE user_id = $1 AND surface_id = $2 AND item_kind = $3`,
			userID, surfaceID, k); err != nil {
			return err
		}
	}
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO surface_user_items (user_id, surface_id, item_kind, item_id, position, visible)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, surfaceID, it.Kind, it.ID, it.Position, it.Visible); err != nil {
			return fmt.Errorf("settings: write user item %s/%s: %w", it.Kind, it.ID, err)
		}
	}
	return tx.Commit(ctx)
}

// DeleteUserItems clears one person's override for one surface (reset).
func (s *Store) DeleteUserItems(ctx context.Context, userID, surfaceID string) error {
	if s.pool == nil {
		return errNoPool
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM surface_user_items WHERE user_id = $1 AND surface_id = $2`, userID, surfaceID)
	return err
}

// ReadUserWorkspace reads one person's ergonomics row for one surface.
func (s *Store) ReadUserWorkspace(ctx context.Context, userID, surfaceID string) (WorkspaceRow, bool, error) {
	if s.pool == nil {
		return WorkspaceRow{}, false, errNoPool
	}
	var r WorkspaceRow
	err := s.pool.QueryRow(ctx, `
		SELECT user_id::text, surface_id, split_pct, arrangement,
		       primary_collapsed, secondary_collapsed, cost_hidden, open_ref, write_key
		  FROM surface_user_workspace
		 WHERE user_id = $1 AND surface_id = $2`, userID, surfaceID).
		Scan(&r.UserID, &r.SurfaceID, &r.SplitPct, &r.Arrangement,
			&r.PrimaryCollapsed, &r.SecondaryCollapsed, &r.CostHidden, &r.OpenRef, &r.WriteKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkspaceRow{}, false, nil
	}
	return r, err == nil, err
}

// UpsertUserWorkspace writes one person's whole row for one surface.
func (s *Store) UpsertUserWorkspace(ctx context.Context, row WorkspaceRow) error {
	if s.pool == nil {
		return errNoPool
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO surface_user_workspace
		    (user_id, surface_id, split_pct, arrangement, primary_collapsed, secondary_collapsed, cost_hidden, open_ref, write_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, surface_id) DO UPDATE SET
		    split_pct = EXCLUDED.split_pct, arrangement = EXCLUDED.arrangement,
		    primary_collapsed = EXCLUDED.primary_collapsed,
		    secondary_collapsed = EXCLUDED.secondary_collapsed,
		    cost_hidden = EXCLUDED.cost_hidden, open_ref = EXCLUDED.open_ref,
		    write_key = EXCLUDED.write_key,
		    updated_at = now()`,
		row.UserID, row.SurfaceID, row.SplitPct, row.Arrangement,
		row.PrimaryCollapsed, row.SecondaryCollapsed, row.CostHidden, row.OpenRef, row.WriteKey)
	return err
}

// DeleteUserWorkspace clears one person's row for one surface (reset).
func (s *Store) DeleteUserWorkspace(ctx context.Context, userID, surfaceID string) error {
	if s.pool == nil {
		return errNoPool
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM surface_user_workspace WHERE user_id = $1 AND surface_id = $2`, userID, surfaceID)
	return err
}
