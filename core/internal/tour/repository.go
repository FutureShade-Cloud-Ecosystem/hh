package tour

// repository.go, pgx over the four tables this module owns and nothing else:
// tour_policy, platform_tour_chapters, platform_tour_steps (0192) and tour_user_progress (0193).
//
// ZERO CROSS-MODULE TABLE ACCESS, AND THIS MODULE NEEDS NO SEAM TO HONOUR IT. Every fact the tour
// serves is in its own four tables, the chapters, their steps, the two knobs, and the caller's own
// bookmark, so there is no consumer-declared interface here and no adapter in main.go. That is
// worth saying out loud rather than leaving as an absence: the obvious wrong turn would have been
// to JOIN `users` for a display name on a bookmark, or `roles` to validate a chapter's role. 0192
// already refused the second (role is editorial, a free CHECK, not a foreign key) and the first is
// a fact nothing in this feature renders.
//
// ORDERS ARE TOTAL, NOT MERELY INTENDED. Every list read orders by (position, key) rather than by
// position alone. Two chapters seeded at the same position would otherwise come back in heap order,
// which is stable until a VACUUM and then is not, and a walkthrough whose chapters swap places
// between two looks at the same screen is a walkthrough nobody trusts.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errNoPool is what every read and write answers when the deployment has no database wired. The
// READS turn it into OFF/empty one layer up (service.go); the WRITES let it reach the wire, because
// a copy edit that silently did nothing is worse than one that says it could not be saved.
var errNoPool = errors.New("tour: database not configured")

// Repository is the module's pgx access layer. A nil pool is tolerated by construction, a
// deployment that boots without a database still serves a tour-free app.
type Repository struct{ pool *pgxpool.Pool }

// NewRepository wraps a pool (may be nil).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// -- the policy ----------------------------------------------------------------------------------

// ReadPolicy resolves the effective policy for a branch: the branch row when one exists, else the
// dealer NULL row, the 0043/0173 branch-NULL idiom 0192 §1 adopts, spelled the way dispatch's
// ReadPolicy spells it so the two cannot drift into two different meanings of "override".
//
// A MISSING ROW IS NOT AN ERROR HERE. 0192 seeds the NULL row, but a database that has lost it must
// answer OFF rather than 500, pgx.ErrNoRows returns the restrictive default, which is exactly what
// the service would have substituted anyway.
func (r *Repository) ReadPolicy(ctx context.Context, branchID string) (Policy, error) {
	if r.pool == nil {
		return DefaultPolicy(), errNoPool
	}
	const q = `
SELECT COALESCE(branch_id::text,''), demo_mode, dealer_enabled
  FROM tour_policy
 WHERE ($1 <> '' AND branch_id::text = $1) OR branch_id IS NULL
 ORDER BY branch_id::text NULLS LAST
 LIMIT 1`
	var p Policy
	err := r.pool.QueryRow(ctx, q, branchID).Scan(&p.BranchID, &p.DemoMode, &p.DealerEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return DefaultPolicy(), err
	}
	return p, nil
}

// SetDemoMode writes the PLATFORM operator's half of the policy and touches nothing else.
//
// THE UPDATE LISTS ONE COLUMN ON PURPOSE. `dealer_enabled` is the dealer's knob (0192 §1) and a
// write by one owner must not restate the other's value, see PolicyEdit's comment for the failure
// that prevents. The INSERT arm supplies the column's own default rather than a Go-side constant,
// so the two spellings of "a fresh policy" cannot disagree.
//
// The NULL row and a branch row are two statements rather than one ON CONFLICT, because
// `branch_id` is UNIQUE and NULL is not equal to NULL in a unique index: an ON CONFLICT on the
// dealer row would never fire and would insert a second NULL row on every save.
func (r *Repository) SetDemoMode(ctx context.Context, branchID string, demoMode bool, updatedBy string) error {
	if r.pool == nil {
		return errNoPool
	}
	if branchID == "" {
		tag, err := r.pool.Exec(ctx, `
UPDATE tour_policy
   SET demo_mode = $1, updated_by = NULLIF($2,'')::uuid, updated_at = now()
 WHERE branch_id IS NULL`, demoMode, updatedBy)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		_, err = r.pool.Exec(ctx, `
INSERT INTO tour_policy (branch_id, demo_mode, updated_by)
VALUES (NULL, $1, NULLIF($2,'')::uuid)`, demoMode, updatedBy)
		return err
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO tour_policy (branch_id, demo_mode, updated_by)
VALUES ($1::uuid, $2, NULLIF($3,'')::uuid)
ON CONFLICT (branch_id) DO UPDATE
   SET demo_mode = EXCLUDED.demo_mode, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		branchID, demoMode, updatedBy)
	return err
}

// -- the chapters and their steps ----------------------------------------------------------------

// chapterRow is one platform_tour_chapters row as read. It is package-private, and it stays a
// separate type from the wire's Chapter even though the two now carry the same six values: the row
// has no steps and the payload has no `visible`, and collapsing them would make every future column
// a payload field the moment somebody adds it to the SELECT.
type chapterRow struct {
	Key      string
	Position int
	Title    string
	Promise  string
	Role     string
	Route    string
}

// ReadVisibleChapters lists the chapters a deployment shows, in order.
//
// `visible = false` IS RETIREMENT, NOT DELETION (0192's add-only ruling). Filtering here rather
// than in Go means a retired chapter never reaches the service, so no later code path can decide to
// show it; and the row stays on disk so the bookmarks that name it still read.
func (r *Repository) ReadVisibleChapters(ctx context.Context) ([]chapterRow, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx, `
SELECT chapter_key, position, title, promise, role, route
  FROM platform_tour_chapters
 WHERE visible
 ORDER BY position, chapter_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []chapterRow{}
	for rows.Next() {
		var c chapterRow
		if err := rows.Scan(&c.Key, &c.Position, &c.Title, &c.Promise, &c.Role, &c.Route); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReadSteps reads EVERY visible chapter's steps in one query, grouped by chapter.
//
// ONE QUERY, NOT ONE PER CHAPTER. Nine chapters is nine round trips at N+1, and the number is nine
// only until somebody seeds a tenth. The join to platform_tour_chapters is what keeps a retired
// chapter's steps out of the map, so the two reads cannot disagree about which chapters exist.
func (r *Repository) ReadSteps(ctx context.Context) (map[string][]Step, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx, `
SELECT s.chapter_key, s.step_key, s.position, s.route, s.anchor, s.line, s.advance
  FROM platform_tour_steps s
  JOIN platform_tour_chapters c ON c.chapter_key = s.chapter_key
 WHERE c.visible
 ORDER BY s.chapter_key, s.position, s.step_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Step{}
	for rows.Next() {
		var chapter string
		var s Step
		if err := rows.Scan(&chapter, &s.StepKey, &s.Position, &s.Route, &s.Anchor, &s.Line, &s.Advance); err != nil {
			return nil, err
		}
		out[chapter] = append(out[chapter], s)
	}
	return out, rows.Err()
}

// UpdateStepCopy rewrites one step's line, route and anchor. It reports whether a row was there.
//
// AN UPDATE, NOT AN UPSERT. A copy edit names a step that exists; if it does not, the honest answer
// is "there is no such step" and not a new step nobody authored appearing in the walkthrough with a
// position of 0, which is where an INSERT's column default would put it, ahead of the step the
// chapter actually opens on.
func (r *Repository) UpdateStepCopy(ctx context.Context, in StepEdit, updatedBy string) (bool, error) {
	if r.pool == nil {
		return false, errNoPool
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE platform_tour_steps
   SET line = $3, route = $4, anchor = $5, updated_by = NULLIF($6,'')::uuid, updated_at = now()
 WHERE chapter_key = $1 AND step_key = $2`,
		in.ChapterKey, in.StepKey, in.Line, in.Route, in.Anchor, updatedBy)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// -- the bookmark --------------------------------------------------------------------------------

// ReadProgress reads ONE person's bookmarks, keyed by chapter.
//
// THE user_id PARAMETER IS THE WHOLE SECURITY MODEL OF THIS TABLE, so it is the first argument of
// every method that touches it and there is no read here that omits it. 0193 gives a dealer's
// manager no read of anybody's progress, and the shape of these signatures is what makes writing
// one an obvious edit rather than a missing WHERE clause.
func (r *Repository) ReadProgress(ctx context.Context, userID string) (map[string]Mark, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx, `
SELECT chapter_key, step_key, state
  FROM tour_user_progress
 WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// KEYED BY CHAPTER, NOT ORDERED, because the caller emits marks in the order it emits chapters
	//, one order for the payload, decided in one place. Returning a sorted slice here would give
	// the payload two orderings to keep in step, which is one more than it needs.
	out := map[string]Mark{}
	for rows.Next() {
		var m Mark
		if err := rows.Scan(&m.ChapterKey, &m.StepKey, &m.State); err != nil {
			return nil, err
		}
		out[m.ChapterKey] = m
	}
	return out, rows.Err()
}

// UpsertProgress writes one person's bookmark in one chapter.
func (r *Repository) UpsertProgress(ctx context.Context, userID string, in ProgressEdit) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO tour_user_progress (user_id, chapter_key, step_key, state)
VALUES ($1::uuid, $2, $3, $4)
ON CONFLICT (user_id, chapter_key) DO UPDATE
   SET step_key = EXCLUDED.step_key, state = EXCLUDED.state, updated_at = now()`,
		userID, in.ChapterKey, in.StepKey, in.State)
	return err
}

// DeleteProgress is RESTART, and it is a DELETE.
//
// 0193's header argues this at length and it is the one line of this file worth reading twice:
// restarting a chapter removes the row rather than writing step 0, because "never started" and
// "started and went back to the beginning" are the same state to the person. Writing a row would
// make them two states the client has to tell apart, and would leave a DONE chapter's history
// silently overwritten by a restart that the person then abandoned.
func (r *Repository) DeleteProgress(ctx context.Context, userID, chapterKey string) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM tour_user_progress WHERE user_id = $1::uuid AND chapter_key = $2`,
		userID, chapterKey)
	return err
}
