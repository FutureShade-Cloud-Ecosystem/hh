package settings

// platform_hotkeys_store.go, pgx persistence for the D-111 platform roster (migration 0191), and
// the role-items DELETE that "reset to the platform roster" needs to be a real reset.
//
// These sit beside store.go rather than in it because they are a DIFFERENT substrate: the 0172
// tables in store.go are per-dealer presentation curation; platform_hotkeys is the deployment's
// floor beneath them. Keeping the two files apart is what makes "which level answered" legible
// from the imports of a stack trace.

import "context"

// ReadPlatformHotkeys lists one surface's platform roster in legend order.
//
// ORDER BY position, action_id, position is what a platform operator arranged, and action_id
// breaks a tie so the read is byte-stable rather than heap order. A stable read matters more here
// than anywhere else in the feature: this list is what the dealer's editor shows as the
// destination of "reset to the platform roster", and a destination that reshuffles between two
// looks at the same screen is a destination nobody trusts.
func (s *Store) ReadPlatformHotkeys(ctx context.Context, surface string) ([]PlatformHotkey, error) {
	if s.pool == nil {
		return nil, errNoPool
	}
	rows, err := s.pool.Query(ctx, `
		SELECT action_id, binding, position
		  FROM platform_hotkeys
		 WHERE surface = $1
		 ORDER BY position, action_id`, surface)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatformHotkey{}
	for rows.Next() {
		var k PlatformHotkey
		if err := rows.Scan(&k.Action, &k.Binding, &k.Position); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortPlatformKeys(out)
	return out, nil
}

// ReplacePlatformHotkeys swaps one surface's whole roster in a single transaction.
//
// DELETE-THEN-INSERT, INSIDE ONE TRANSACTION, AND SCOPED TO THE SURFACE. Deleting first is what
// makes the write whole-or-absent: an UPSERT would leave behind any action the new roster no
// longer binds, so unbinding a key would silently not happen. Scoping to the surface is what keeps
// the two substrates two, editing the counter's roster cannot touch the app-wide map, and the
// screen that edits both edits them with two separate writes for exactly that reason.
//
// The transaction also matters for the unique index on (surface, binding): swapping two actions'
// keys deletes both rows before inserting either, so the intermediate state where both hold the
// same key never exists.
func (s *Store) ReplacePlatformHotkeys(ctx context.Context, surface string, keys []PlatformHotkey, updatedBy string) error {
	if s.pool == nil {
		return errNoPool
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM platform_hotkeys WHERE surface = $1`, surface); err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := tx.Exec(ctx, `
			INSERT INTO platform_hotkeys (surface, action_id, binding, position, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, NULLIF($5,'')::uuid, now())`,
			surface, k.Action, k.Binding, k.Position, updatedBy); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeleteRoleItems clears the dealer's curation of ONE KIND on one surface+profile.
//
// WHY THIS EXISTS, AND WHY IT IS SCOPED TO A KIND. Until now the kit could only ever OVERWRITE a
// dealer's role items, ReplaceRoleItems swaps, and there was no DELETE route at all. That left a
// one-way door: the counter editor's "reset" wrote the default values INTO the dealer level, so
// after one reset-and-save a dealer was permanently curated with what was really a default, and no
// level beneath them could ever answer again. Harmless while the level beneath was a constant in
// Go; fatal to D-111, whose whole promise is that a platform operator's change reaches a dealer who
// has not overridden it. Reset has to mean DELETE, which is what it already means for a person's
// own override (ResetUserItems) and for a workspace row.
//
// The KIND scope is not optional. ReplaceRoleItems' contract is per-kind whole-or-absent, curating
// the columns cannot silently erase the action bar, and a DELETE that ignored kind would do
// exactly the thing that contract exists to prevent: clearing the counter's key roster would take
// the hot list and the panel defaults with it.
func (s *Store) DeleteRoleItems(ctx context.Context, surfaceID, profile, kind string) error {
	if s.pool == nil {
		return errNoPool
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM surface_role_items WHERE surface_id = $1 AND profile = $2 AND item_kind = $3`,
		surfaceID, profile, kind)
	return err
}
