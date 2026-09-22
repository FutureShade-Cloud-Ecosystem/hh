package dashboard

// widget_repository.go, the pgx implementation of WidgetStore, NeedsPolicyStore and GalleryStore
// over 0201, its free-span widening (0207) and the gallery (0208).
//
// It holds NO business rules. Which layer decides, what a forbidden measure means, how severity is
// scored, all of that lives in widget.go and needs.go where it is testable without a database. This
// file's only opinions are the ones SQL is entitled to: ordering, transactions, and the difference
// between "no rows" and "no answer".
//
// A SEPARATE TYPE FROM Repository, and deliberately so: a build whose database is behind 0201 gets a
// working cockpit with no widget layer instead of a *Repository that fails five methods. The seam is
// injected (Service.WithWidgetStore) rather than assumed, which is what makes "not migrated yet" a
// state the product can be in.

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WidgetRepository reads and writes the widget substrate.
type WidgetRepository struct{ pool *pgxpool.Pool }

func NewWidgetRepository(pool *pgxpool.Pool) *WidgetRepository { return &WidgetRepository{pool: pool} }

var (
	_ WidgetStore      = (*WidgetRepository)(nil)
	_ NeedsPolicyStore = (*WidgetRepository)(nil)
	_ GalleryStore     = (*WidgetRepository)(nil)
)

// widgetColumns is the SELECT list, in the order scanWidgets expects. One constant so the two reads
// cannot drift into scanning different columns into the same struct.
//
// cols/rows (0207) are COALESCEd to 0 for branch_id's reason exactly: NULL means "the preset decides",
// Widget.Cols is an int, and 0 is already how that absence is spelled in Go and omitted on the wire.
// A *int here would exist only to be dereferenced somewhere it might be nil.
const widgetColumns = `widget_key, shape, measure, split, COALESCE(branch_id::text, ''), time_range, size, ` +
	`COALESCE(cols, 0), COALESCE(rows, 0), title, position, visible`

// ProfileWidgets reads a role default, in layout order.
func (r *WidgetRepository) ProfileWidgets(ctx context.Context, profile string) ([]Widget, error) {
	if r.pool == nil || profile == "" {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+widgetColumns+` FROM dashboard_profile_widgets
		  WHERE profile = $1 ORDER BY position, widget_key`, profile)
	if err != nil {
		return nil, err
	}
	return scanWidgets(rows)
}

// UserWidgets reads one person's layout, in layout order.
func (r *WidgetRepository) UserWidgets(ctx context.Context, userID string) ([]Widget, error) {
	if r.pool == nil || userID == "" {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+widgetColumns+` FROM dashboard_user_widgets
		  WHERE user_id = $1::uuid ORDER BY position, widget_key`, userID)
	if err != nil {
		return nil, err
	}
	return scanWidgets(rows)
}

// scanWidgets maps rows to Widgets. branch_id arrives as " rather than NULL (COALESCE above)
// because Widget.BranchID is a string: an absent branch is the empty string in Go and an absent KEY
// on the wire, and a *string here would only exist to be dereferenced somewhere it might be nil.
func scanWidgets(rows pgx.Rows) ([]Widget, error) {
	defer rows.Close()
	var out []Widget
	for rows.Next() {
		var w Widget
		if err := rows.Scan(&w.Key, &w.Shape, &w.Measure, &w.Split, &w.BranchID,
			&w.Range, &w.Size, &w.Cols, &w.Rows, &w.Title, &w.Position, &w.Visible); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// SaveUserWidgets replaces the caller's layout wholesale, inside one transaction.
//
// DELETE-then-INSERT, not upsert-and-hope, the same choice SaveUserTiles makes and for the same
// reason: the layout is whole-or-absent, so a save that left a row behind from a longer previous
// layout would resurrect a widget the person just removed. The transaction is what makes "replace"
// true even if the insert fails halfway.
func (r *WidgetRepository) SaveUserWidgets(ctx context.Context, userID string, rows []Widget) error {
	if r.pool == nil {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_user_widgets WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	for _, w := range rows {
		if _, err := tx.Exec(ctx,
			// NULLIF($9,0)/NULLIF($10,0) is the mirror of the COALESCE in widgetColumns: Go's 0 is
			// "the preset decides" and the column's way of saying that is NULL, so the round trip is
			// lossless and a person who never touched the grip stores no span at all.
			`INSERT INTO dashboard_user_widgets
			   (user_id, widget_key, shape, measure, split, branch_id, time_range, size, cols, rows, title, position, visible)
			 VALUES ($1::uuid, $2, $3, $4, $5, NULLIF($6,'')::uuid, $7, $8, NULLIF($9,0)::smallint, NULLIF($10,0)::smallint, $11, $12, $13)`,
			userID, w.Key, w.Shape, w.Measure, w.Split, w.BranchID,
			w.Range, w.Size, w.Cols, w.Rows, w.Title, w.Position, w.Visible); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ClearUserWidgets is RESET TO ROLE DEFAULT: the person's rows go and the role default answers
// again. It writes nothing, which is the whole of the reset (0191).
func (r *WidgetRepository) ClearUserWidgets(ctx context.Context, userID string) (int, error) {
	if r.pool == nil {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM dashboard_user_widgets WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// SaveProfileWidgets replaces a role default wholesale (same reasoning as SaveUserWidgets).
func (r *WidgetRepository) SaveProfileWidgets(ctx context.Context, profile string, rows []Widget, by string) error {
	if r.pool == nil {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_profile_widgets WHERE profile = $1`, profile); err != nil {
		return err
	}
	for _, w := range rows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO dashboard_profile_widgets
			   (profile, widget_key, shape, measure, split, branch_id, time_range, size, cols, rows, title, position, visible, updated_by, updated_at)
			 VALUES ($1, $2, $3, $4, $5, NULLIF($6,'')::uuid, $7, $8, NULLIF($9,0)::smallint, NULLIF($10,0)::smallint, $11, $12, $13, NULLIF($14,'')::uuid, now())`,
			profile, w.Key, w.Shape, w.Measure, w.Split, w.BranchID,
			w.Range, w.Size, w.Cols, w.Rows, w.Title, w.Position, w.Visible, by); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// -- the "Add widget" gallery (0208) -------------------------------------------------------------

// galleryColumns is the SELECT list, in the order scanGallery expects, one constant, for
// widgetColumns' reason.
const galleryColumns = `entry_key, shape, measure, split, time_range, size, ` +
	`COALESCE(cols, 0), COALESCE(rows, 0), title, blurb, position, visible`

// ProfileGallery reads a role's gallery, in curated order.
//
// IT READS HIDDEN ENTRIES TOO, and that is deliberate rather than an oversight: `visible = false` is
// RETIREMENT and the admin editing this gallery has to be able to see what they retired in order to
// bring it back. The staff read drops them one layer up (GalleryCardsFor), where the decision about
// what a person is OFFERED belongs, the same division this file keeps everywhere: SQL owns ordering
// and transactions, Go owns what anything means.
func (r *WidgetRepository) ProfileGallery(ctx context.Context, profile string) ([]GalleryEntry, error) {
	if r.pool == nil || profile == "" {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+galleryColumns+` FROM dashboard_gallery_entries
		  WHERE profile = $1 ORDER BY position, entry_key`, profile)
	if err != nil {
		return nil, err
	}
	return scanGallery(rows)
}

func scanGallery(rows pgx.Rows) ([]GalleryEntry, error) {
	defer rows.Close()
	var out []GalleryEntry
	for rows.Next() {
		var e GalleryEntry
		if err := rows.Scan(&e.Key, &e.Shape, &e.Measure, &e.Split, &e.Range, &e.Size,
			&e.Cols, &e.Rows, &e.Title, &e.Blurb, &e.Position, &e.Visible); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveProfileGallery replaces a role's gallery wholesale, inside one transaction, DELETE-then-INSERT
// for SaveUserWidgets' reason: the document is whole, so a save that left a row behind from a longer
// previous gallery would resurrect an entry the admin just removed.
//
// AN EMPTY `rows` IS A LEGITIMATE SAVE and clears the gallery. The service decides that is allowed
// (SaveProfileGallery there argues why); this loop just runs zero times.
func (r *WidgetRepository) SaveProfileGallery(ctx context.Context, profile string, rows []GalleryEntry, by string) error {
	if r.pool == nil {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_gallery_entries WHERE profile = $1`, profile); err != nil {
		return err
	}
	for _, e := range rows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO dashboard_gallery_entries
			   (profile, entry_key, shape, measure, split, time_range, size, cols, rows, title, blurb, position, visible, updated_by, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8,0)::smallint, NULLIF($9,0)::smallint, $10, $11, $12, $13, NULLIF($14,'')::uuid, now())`,
			profile, e.Key, e.Shape, e.Measure, e.Split, e.Range, e.Size,
			e.Cols, e.Rows, e.Title, e.Blurb, e.Position, e.Visible, by); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// NeedsSources reads a job shape's Needs-you policy, in curated order.
//
// NO ROWS IS AN ANSWER, NOT AN ERROR, but it is the SERVICE that decides what the answer means (a
// dealer with no sources for this shape gets the shipped defaults only when the read FAILED; an
// empty successful read is respected). Keeping that distinction above this line is what lets the two
// cases stay two cases.
func (r *WidgetRepository) NeedsSources(ctx context.Context, profile string) ([]NeedsPolicy, error) {
	if r.pool == nil || profile == "" {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT source_key, position, visible, warn_at, stop_at, stale_after_minutes
		  FROM dashboard_needs_sources
		 WHERE profile = $1
		 ORDER BY position, source_key`, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NeedsPolicy{}
	for rows.Next() {
		var p NeedsPolicy
		if err := rows.Scan(&p.SourceKey, &p.Position, &p.Visible,
			&p.WarnAt, &p.StopAt, &p.StaleMins); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
