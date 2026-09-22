package dashboard

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the pgx implementation of Store over 0140/0141. It holds NO business rules: every
// decision about what a layout means lives in layout.go, where it can be tested without a database.
type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var _ Store = (*Repository)(nil)

// ProfileForRole maps a role code to a layout profile. An unmapped role returns "" and the service
// decides the fallback, the repository does not get to invent policy.
func (r *Repository) ProfileForRole(ctx context.Context, roleCode string) (string, error) {
	if r.pool == nil || roleCode == "" {
		return "", nil
	}
	var p string
	err := r.pool.QueryRow(ctx,
		`SELECT profile FROM dashboard_role_profiles WHERE role_code = $1::citext`, roleCode).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return p, err
}

func (r *Repository) ProfileTiles(ctx context.Context, profile string) ([]LayoutRow, error) {
	if r.pool == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT tile_id, position, visible FROM dashboard_profile_tiles
		  WHERE profile = $1 ORDER BY position, tile_id`, profile)
	if err != nil {
		return nil, err
	}
	return scanLayout(rows)
}

func (r *Repository) UserTiles(ctx context.Context, userID string) ([]LayoutRow, error) {
	if r.pool == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT tile_id, position, visible FROM dashboard_user_tiles
		  WHERE user_id = $1::uuid ORDER BY position, tile_id`, userID)
	if err != nil {
		return nil, err
	}
	return scanLayout(rows)
}

func scanLayout(rows pgx.Rows) ([]LayoutRow, error) {
	defer rows.Close()
	var out []LayoutRow
	for rows.Next() {
		var lr LayoutRow
		if err := rows.Scan(&lr.TileID, &lr.Position, &lr.Visible); err != nil {
			return nil, err
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}

// SaveUserTiles replaces the caller's override wholesale, inside one transaction.
//
// DELETE-then-INSERT, not upsert-and-hope: the override is whole-or-absent (layout.go), so a save
// that left a stale row behind from a previous, longer layout would resurrect a tile the person
// just removed. The transaction is what makes "replace" true even if the insert fails halfway.
func (r *Repository) SaveUserTiles(ctx context.Context, userID string, rows []LayoutRow) error {
	if r.pool == nil {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_user_tiles WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	for _, lr := range rows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO dashboard_user_tiles (user_id, tile_id, position, visible)
			 VALUES ($1::uuid, $2, $3, $4)`, userID, lr.TileID, lr.Position, lr.Visible); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ClearUserTiles is reset-to-role: the override rows go, the role default resumes.
func (r *Repository) ClearUserTiles(ctx context.Context, userID string) (int, error) {
	if r.pool == nil {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM dashboard_user_tiles WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// SaveProfileTiles replaces a role default wholesale (same reasoning as SaveUserTiles).
func (r *Repository) SaveProfileTiles(ctx context.Context, profile string, rows []LayoutRow, by string) error {
	if r.pool == nil {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_profile_tiles WHERE profile = $1`, profile); err != nil {
		return err
	}
	for _, lr := range rows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible, updated_by, updated_at)
			 VALUES ($1, $2, $3, $4, NULLIF($5,'')::uuid, now())`,
			profile, lr.TileID, lr.Position, lr.Visible, by); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// -- DIB-433 pinned tabs (0149) -----------------------------------------------------------------
// The same five verbs over the same shape, for the same reason: the pin set curates exactly the way
// the tile layout curates, so it stores exactly the way the tile layout stores.

func (r *Repository) ProfileTabs(ctx context.Context, profile string) ([]TabPin, error) {
	if r.pool == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT tab_key, position, visible FROM dashboard_profile_tabs
		  WHERE profile = $1 ORDER BY position, tab_key`, profile)
	if err != nil {
		return nil, err
	}
	return scanPins(rows)
}

func (r *Repository) UserTabs(ctx context.Context, userID string) ([]TabPin, error) {
	if r.pool == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT tab_key, position, visible FROM dashboard_user_tabs
		  WHERE user_id = $1::uuid ORDER BY position, tab_key`, userID)
	if err != nil {
		return nil, err
	}
	return scanPins(rows)
}

func scanPins(rows pgx.Rows) ([]TabPin, error) {
	defer rows.Close()
	var out []TabPin
	for rows.Next() {
		var p TabPin
		if err := rows.Scan(&p.TabKey, &p.Position, &p.Visible); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveUserTabs replaces the caller's pin override wholesale, inside one transaction -
// DELETE-then-INSERT for the same reason SaveUserTiles is: the override is whole-or-absent, so a
// save that left a row behind from a longer previous set would resurrect a tab the person just
// unpinned.
func (r *Repository) SaveUserTabs(ctx context.Context, userID string, rows []TabPin) error {
	if r.pool == nil {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_user_tabs WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	for _, p := range rows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO dashboard_user_tabs (user_id, tab_key, position, visible)
			 VALUES ($1::uuid, $2, $3, $4)`, userID, p.TabKey, p.Position, p.Visible); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ClearUserTabs is reset-to-role for the strip: the override rows go, the role default resumes.
func (r *Repository) ClearUserTabs(ctx context.Context, userID string) (int, error) {
	if r.pool == nil {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM dashboard_user_tabs WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// SaveProfileTabs replaces a role default's pins wholesale (same reasoning as SaveUserTabs).
func (r *Repository) SaveProfileTabs(ctx context.Context, profile string, rows []TabPin, by string) error {
	if r.pool == nil {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_profile_tabs WHERE profile = $1`, profile); err != nil {
		return err
	}
	for _, p := range rows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible, updated_by, updated_at)
			 VALUES ($1, $2, $3, $4, NULLIF($5,'')::uuid, now())`,
			profile, p.TabKey, p.Position, p.Visible, by); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// -- DIB-496 dealer hotkeys (0164) --------------------------------------------------------------

// Hotkeys reads the RESOLVED shell key map: this dealer's own rows if they have curated any, else
// the platform roster (D-111, migration 0191). ORDER BY binding so the wire order, which is also
// the shortcut overlay's display order, is deterministic rather than heap order. What a row MEANS
// (canonical spelling, dropped junk, first-binding-wins) is decided in hotkeys.go, where it is
// tested without a database.
//
// WHY THE TIER IS RESOLVED HERE AND NOT IN THE SERVICE. The service's rule is about the STORE's
// answer, and it is a rule worth keeping exactly as it is: an unreadable store serves the shipped
// defaults, a store that answers "nothing" is believed. Resolving the tier above that line would
// have forced those two cases to change meaning; resolving it below leaves them untouched, and
// makes the service's job the same one word it always was. A store that answers no rows now means
// "nobody has bound anything at any level", which is still exactly the state Service.hotkeys
// respects rather than papering over.
//
// WHOLE-OR-ABSENT, NOT ROW-BY-ROW. `NOT EXISTS (SELECT 1 FROM dealer_hotkeys)` is the whole tier
// rule: a dealer who has bound anything owns their map entire. Merging the two maps key by key
// would mean a platform operator adding a binding could silently appear inside a map a dealer
// thought they controlled, and "which level decided this" would stop being answerable in a
// sentence, the same reason the counter roster resolves whole-or-absent one level at a time.
func (r *Repository) Hotkeys(ctx context.Context) ([]HotkeyBinding, error) {
	if r.pool == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT binding, action_id FROM dealer_hotkeys
		UNION ALL
		SELECT binding, action_id FROM platform_hotkeys
		 WHERE surface = 'app.shell'
		   AND NOT EXISTS (SELECT 1 FROM dealer_hotkeys)
		 ORDER BY binding`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HotkeyBinding
	for rows.Next() {
		var hb HotkeyBinding
		if err := rows.Scan(&hb.Binding, &hb.Action); err != nil {
			return nil, err
		}
		out = append(out, hb)
	}
	return out, rows.Err()
}

func (r *Repository) Home(ctx context.Context, profile string) (HomeConfig, error) {
	if r.pool == nil {
		return HomeConfig{Profile: profile}, nil
	}
	var (
		h            HomeConfig
		unit, module *string
	)
	err := r.pool.QueryRow(ctx,
		`SELECT landing_unit, landing_module, takeover, mobile_only
		   FROM dashboard_profile_home WHERE profile = $1`, profile).
		Scan(&unit, &module, &h.Takeover, &h.MobileOnly)
	if errors.Is(err, pgx.ErrNoRows) {
		// No row is not an error: it means "land on the dashboard", which is what every desk role
		// wants and what a newly-added profile should do until somebody decides otherwise.
		return HomeConfig{Profile: profile, MobileOnly: true}, nil
	}
	if err != nil {
		return HomeConfig{}, err
	}
	h.Profile = profile
	if unit != nil {
		h.LandingUnit = *unit
	}
	if module != nil {
		h.LandingModule = *module
	}
	return h, nil
}

func (r *Repository) SaveHome(ctx context.Context, profile string, h HomeConfig, by string) error {
	if r.pool == nil {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO dashboard_profile_home (profile, landing_unit, landing_module, takeover, mobile_only, updated_by, updated_at)
		 VALUES ($1, NULLIF($2,''), NULLIF($3,''), $4, $5, NULLIF($6,'')::uuid, now())
		 ON CONFLICT (profile) DO UPDATE SET
		   landing_unit = EXCLUDED.landing_unit,
		   landing_module = EXCLUDED.landing_module,
		   takeover = EXCLUDED.takeover,
		   mobile_only = EXCLUDED.mobile_only,
		   updated_by = EXCLUDED.updated_by,
		   updated_at = now()`,
		profile, h.LandingUnit, h.LandingModule, h.Takeover, h.MobileOnly, by)
	return err
}
