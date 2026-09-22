package dashboard

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// quickaction_repository.go, DIB-434. The pgx implementation of QuickActionStore over 0151. It
// holds NO business rules: what a stored row MEANS is decided in quickaction.go, where it is tested
// without a database.

type QuickActionRepository struct{ pool *pgxpool.Pool }

func NewQuickActionRepository(pool *pgxpool.Pool) *QuickActionRepository {
	return &QuickActionRepository{pool: pool}
}

var _ QuickActionStore = (*QuickActionRepository)(nil)

// Configs reads every stored action config.
//
// TWO QUERIES, NOT A JOIN. An action can have a global switch row with no per-profile rows (an
// admin toggled it off for everyone and never touched the matrix), and per-profile rows with no
// switch row (0151 seeds both, but a hand-written INSERT need not). A join would silently drop
// whichever half was missing; two reads merged in Go keep both halves visible, and the resolver
// already knows what to do with a half-configured action.
func (r *QuickActionRepository) Configs(ctx context.Context) (map[string]ActionConfig, error) {
	out := map[string]ActionConfig{}
	if r.pool == nil {
		return out, nil
	}

	rows, err := r.pool.Query(ctx, `SELECT action_id, enabled FROM quick_action_settings`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var enabled bool
		if err := rows.Scan(&id, &enabled); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = ActionConfig{ActionID: id, Enabled: enabled, Profiles: map[string]bool{}}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	vrows, err := r.pool.Query(ctx,
		`SELECT action_id, profile, visible FROM quick_action_profile_visibility`)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var id, profile string
		var visible bool
		if err := vrows.Scan(&id, &profile, &visible); err != nil {
			return nil, err
		}
		cfg, ok := out[id]
		if !ok {
			// Per-profile rows with no switch row: the action IS configured, and its global switch
			// falls back to the catalog default. Seeding Enabled from the catalog here (rather than
			// leaving it false) is what stops a hand-written visibility INSERT from turning the
			// action off everywhere as a side effect.
			enabled := true
			if meta, known := ActionByID[id]; known {
				enabled = meta.DefaultEnabled
			}
			cfg = ActionConfig{ActionID: id, Enabled: enabled, Profiles: map[string]bool{}}
		}
		cfg.Profiles[profile] = visible
		out[id] = cfg
	}
	return out, vrows.Err()
}

// SaveConfig writes one action's switch and REPLACES its per-profile matrix, in one transaction.
//
// DELETE-then-INSERT for the matrix, matching SaveProfileTiles: the matrix the admin submitted is
// the whole matrix. An upsert would leave a row behind for a profile the admin just removed from
// the form, and that row would keep overriding the catalog default forever with a value nobody can
// see in the UI any more.
func (r *QuickActionRepository) SaveConfig(ctx context.Context, cfg ActionConfig, by string) error {
	if r.pool == nil {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO quick_action_settings (action_id, enabled, updated_by, updated_at)
		 VALUES ($1, $2, NULLIF($3,'')::uuid, now())
		 ON CONFLICT (action_id) DO UPDATE SET
		   enabled = EXCLUDED.enabled,
		   updated_by = EXCLUDED.updated_by,
		   updated_at = now()`,
		cfg.ActionID, cfg.Enabled, by); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM quick_action_profile_visibility WHERE action_id = $1`, cfg.ActionID); err != nil {
		return err
	}
	// Profiles order is the closed job-shape list, not map order, so the statement sequence is
	// deterministic and a failing test names the same row every run.
	for _, p := range Profiles {
		v, ok := cfg.Profiles[p]
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO quick_action_profile_visibility (action_id, profile, visible, updated_by, updated_at)
			 VALUES ($1, $2, $3, NULLIF($4,'')::uuid, now())`,
			cfg.ActionID, p, v, by); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
