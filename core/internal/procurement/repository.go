package procurement

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// errNoDB is returned when the repository has no pool (scaffold/offline mode). It is distinct
// from ErrNotImplemented: errNoDB means "no database wired", ErrNotImplemented means "wired,
// but this method's behavior is not built yet".
var errNoDB = errors.New("procurement: database not configured")

// pgInvalidTextRepresentation is Postgres SQLSTATE 22P02 ("invalid text representation"),
// raised when a malformed string is cast to uuid. A non-UUID path id (id probe / IDOR
// fuzz) must surface as a 404, not a 500 leaking the cast error (review #9).
const pgInvalidTextRepresentation = "22P02"

// isInvalidUUID reports whether err is a Postgres invalid-text-representation (22P02) error,
// i.e. a malformed-UUID path id reached the query layer.
func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgInvalidTextRepresentation
}

// notFound reports whether err should map to ErrNotFound (404): either no row matched the
// tenant-scoped id (pgx.ErrNoRows) OR the id was a malformed UUID (22P02). Both are IDOR-safe
// 404s -- a probe never learns whether a row exists or whether its id was even well-formed.
func notFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err)
}

// projectSummaryCols is the canonical procurement_projects header SELECT list. line_count and
// subtotal_cents are derived (correlated aggregates over the project's lines, COALESCE 0); kept
// in one place so Create/List/Update scan identically. Requires the table aliased as p.
const projectSummaryCols = `p.id::text, p.customer_id::text, p.name, p.status, p.created_at, p.updated_at,
	(SELECT COUNT(*) FROM procurement_project_lines l WHERE l.project_id = p.id),
	(SELECT COALESCE(SUM(l.indicative_line_total_cents), 0) FROM procurement_project_lines l WHERE l.project_id = p.id)`

// lineCols is the read SELECT list for a line joined to its catalog product (alias l + pr).
// sku/qty/uom/notes COALESCE to "" (qty serialized as text to preserve scale); the indicative
// cents COALESCE to 0 and priced is derived (NULL prices == not yet RFQ'd, migration 0028).
const lineCols = `l.id::text, l.line_no, l.product_id::text, pr.sku::text,
	COALESCE(l.description, ''), COALESCE(l.qty_entered::text, ''), COALESCE(l.uom, ''), COALESCE(l.notes, ''),
	COALESCE(l.indicative_unit_price_cents, 0), COALESCE(l.indicative_line_total_cents, 0),
	(l.indicative_unit_price_cents IS NOT NULL)`

// lineReturning is the RETURNING list for INSERT/UPDATE on procurement_project_lines (no join
// alias available); sku is resolved via a subquery against products. Column order matches lineCols.
const lineReturning = `id::text, line_no, product_id::text,
	(SELECT sku::text FROM products WHERE id = procurement_project_lines.product_id),
	COALESCE(description, ''), COALESCE(qty_entered::text, ''), COALESCE(uom, ''), COALESCE(notes, ''),
	COALESCE(indicative_unit_price_cents, 0), COALESCE(indicative_line_total_cents, 0),
	(indicative_unit_price_cents IS NOT NULL)`

// Repository owns procurement_projects + procurement_project_lines (pgx). Zero cross-module
// access: the account/category/pricing facts SubmitRFQ needs are composed in the service via
// injected seams, never SELECTed here. Every method is tenant-scoped by customerID; a missing
// or cross-tenant row maps pgx.ErrNoRows to ErrNotFound (404, never a 403/leak).
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// nullable returns nil for an empty string so a TEXT/UUID column is written NULL, not "".
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// scanProject scans the projectSummaryCols header + derived aggregates into a Project.
func scanProject(row pgx.Row) (Project, error) {
	var p Project
	if err := row.Scan(
		&p.ID, &p.CustomerID, &p.Name, &p.Status, &p.CreatedAt, &p.UpdatedAt,
		&p.LineCount, &p.SubtotalCents,
	); err != nil {
		return Project{}, err
	}
	return p, nil
}

// scanLine scans the lineCols/lineReturning column order into a Line.
func scanLine(row pgx.Row) (Line, error) {
	var ln Line
	if err := row.Scan(
		&ln.ID, &ln.LineNo, &ln.ProductID, &ln.SKU, &ln.Description, &ln.QtyEntered,
		&ln.UOM, &ln.Notes, &ln.IndicativeUnitPriceCents, &ln.IndicativeLineTotalCents, &ln.Priced,
	); err != nil {
		return Line{}, err
	}
	return ln, nil
}

// CreateProject inserts a PLANNING project for the tenant, returning the (zero-line) summary.
func (r *Repository) CreateProject(ctx context.Context, customerID, createdByUserID, name string) (Project, error) {
	if r.pool == nil {
		return Project{}, errNoDB
	}
	q := `INSERT INTO procurement_projects AS p (customer_id, created_by_portal_user_id, name, status)
		VALUES ($1, $2, $3, 'PLANNING')
		RETURNING ` + projectSummaryCols
	p, err := scanProject(r.pool.QueryRow(ctx, q, customerID, nullable(createdByUserID), name))
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return p, nil
}

// ListProjects returns the tenant's non-terminal projects (CANCELLED excluded), newest-updated
// first, each carrying its derived line_count + subtotal_cents.
func (r *Repository) ListProjects(ctx context.Context, customerID string) ([]Project, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + projectSummaryCols + `
		FROM procurement_projects p
		WHERE p.customer_id = $1 AND p.status <> 'CANCELLED'
		ORDER BY p.updated_at DESC`
	rows, err := r.pool.Query(ctx, q, customerID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetProject loads one project header WITH its ordered lines, scoped to the tenant. A missing
// or cross-tenant id maps pgx.ErrNoRows to ErrNotFound (404, never 403).
func (r *Repository) GetProject(ctx context.Context, customerID, projectID string) (ProjectDetail, error) {
	if r.pool == nil {
		return ProjectDetail{}, errNoDB
	}
	q := `SELECT ` + projectSummaryCols + `
		FROM procurement_projects p WHERE p.customer_id = $1 AND p.id = $2`
	p, err := scanProject(r.pool.QueryRow(ctx, q, customerID, projectID))
	if err != nil {
		if notFound(err) {
			return ProjectDetail{}, ErrNotFound
		}
		return ProjectDetail{}, fmt.Errorf("get project %q: %w", projectID, err)
	}
	d := ProjectDetail{Project: p}

	lq := `SELECT ` + lineCols + `
		FROM procurement_project_lines l
		JOIN products pr ON pr.id = l.product_id
		WHERE l.project_id = $1
		ORDER BY l.line_no`
	rows, err := r.pool.Query(ctx, lq, projectID)
	if err != nil {
		return ProjectDetail{}, fmt.Errorf("get project lines %q: %w", projectID, err)
	}
	defer rows.Close()

	for rows.Next() {
		ln, err := scanLine(rows)
		if err != nil {
			return ProjectDetail{}, fmt.Errorf("scan project line: %w", err)
		}
		d.Lines = append(d.Lines, ln)
	}
	if err := rows.Err(); err != nil {
		return ProjectDetail{}, err
	}
	return d, nil
}

// UpdateProject applies a tenant-scoped partial rename and/or lane move. A status change is
// validated against the legal source state (canTransition) atomically under a row lock; a wrong
// source -> ErrInvalidTransition, a missing/cross-tenant row -> ErrNotFound. updated_at is bumped.
func (r *Repository) UpdateProject(ctx context.Context, customerID, projectID string, name, status *string) (Project, error) {
	if r.pool == nil {
		return Project{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Project{}, fmt.Errorf("update project %q: begin: %w", projectID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	err = tx.QueryRow(ctx,
		`SELECT status FROM procurement_projects WHERE id = $2 AND customer_id = $1 FOR UPDATE`,
		customerID, projectID).Scan(&current)
	if err != nil {
		if notFound(err) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("update project %q: load: %w", projectID, err)
	}
	if status != nil && *status != current && !canTransition(current, *status) {
		return Project{}, ErrInvalidTransition
	}

	q := `UPDATE procurement_projects AS p
		SET name = COALESCE($3, name), status = COALESCE($4, status), updated_at = now()
		WHERE p.id = $2 AND p.customer_id = $1
		RETURNING ` + projectSummaryCols
	p, err := scanProject(tx.QueryRow(ctx, q, customerID, projectID, name, status))
	if err != nil {
		return Project{}, fmt.Errorf("update project %q: %w", projectID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Project{}, fmt.Errorf("update project %q: commit: %w", projectID, err)
	}
	return p, nil
}

// DeleteProject deletes a tenant project only while it is in PLANNING (lines cascade). A wrong
// lane -> ErrInvalidTransition; a missing/cross-tenant row -> ErrNotFound. The conditional
// DELETE + existence check disambiguates the two so neither leaks as the other's status.
func (r *Repository) DeleteProject(ctx context.Context, customerID, projectID string) error {
	if r.pool == nil {
		return errNoDB
	}
	var deleted string
	err := r.pool.QueryRow(ctx,
		`DELETE FROM procurement_projects
		 WHERE id = $2 AND customer_id = $1 AND status = 'PLANNING'
		 RETURNING id::text`,
		customerID, projectID).Scan(&deleted)
	if err != nil {
		if isInvalidUUID(err) {
			// Malformed-UUID id: no such row could exist -> 404 (and the existence
			// re-query below would itself raise 22P02), so short-circuit (review #9).
			return ErrNotFound
		}
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			if eerr := r.pool.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM procurement_projects WHERE id = $2 AND customer_id = $1)`,
				customerID, projectID).Scan(&exists); eerr != nil {
				return fmt.Errorf("delete project %q: existence check: %w", projectID, eerr)
			}
			if !exists {
				return ErrNotFound
			}
			return ErrInvalidTransition
		}
		return fmt.Errorf("delete project %q: %w", projectID, err)
	}
	return nil
}

// AddLine appends a catalog line to a tenant project: line_no = MAX(line_no)+1 for the project
// and description is captured from the catalog product at add-time (sku is resolved on read).
// The project row is locked FOR UPDATE -- this both serializes concurrent appends (so line_no
// never collides) AND enforces the PLANNING freeze atomically: a non-PLANNING status read under
// the lock is ErrInvalidTransition (409), so a racing SubmitRFQ cannot slip a line onto a
// QUOTING project; a missing/cross-tenant row is ErrNotFound (404) (reviews #2 + #3 + #6).
func (r *Repository) AddLine(ctx context.Context, customerID, projectID string, in AddLineRequest) (Line, error) {
	if r.pool == nil {
		return Line{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Line{}, fmt.Errorf("add line: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM procurement_projects WHERE id = $2 AND customer_id = $1 FOR UPDATE`,
		customerID, projectID).Scan(&status)
	if err != nil {
		if notFound(err) {
			return Line{}, ErrNotFound
		}
		return Line{}, fmt.Errorf("add line: load project: %w", err)
	}
	if status != StatusPlanning {
		return Line{}, ErrInvalidTransition
	}

	q := `INSERT INTO procurement_project_lines
			(project_id, line_no, product_id, description, qty_entered, uom, notes)
		SELECT $1,
			COALESCE((SELECT MAX(line_no) FROM procurement_project_lines WHERE project_id = $1), 0) + 1,
			pr.id, pr.description, $3::numeric, $4, $5
		FROM products pr WHERE pr.id = $2
		RETURNING ` + lineReturning
	ln, err := scanLine(tx.QueryRow(ctx, q, projectID, in.ProductID, in.QtyEntered, in.UOM, nullable(in.Notes)))
	if err != nil {
		if isInvalidUUID(err) {
			// Malformed product_id -> IDOR-safe 404, not a 500 leaking the cast error (review #9).
			return Line{}, ErrNotFound
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// No catalog product with that id -> nothing inserted (client error, not a 500).
			return Line{}, httpx.BadRequestf("product %q not found", in.ProductID)
		}
		return Line{}, fmt.Errorf("add line: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Line{}, fmt.Errorf("add line: commit: %w", err)
	}
	return ln, nil
}

// UpdateLine edits qty/uom/notes on a tenant project's line (partial COALESCE update). The line
// must belong to the project AND the project to the tenant; otherwise ErrNotFound. The project
// row is locked FOR UPDATE first so the PLANNING freeze is enforced atomically: a non-PLANNING
// status is ErrInvalidTransition (409), beating a racing SubmitRFQ (reviews #2 + #3 + #6).
func (r *Repository) UpdateLine(ctx context.Context, customerID, projectID, lineID string, in UpdateLineRequest) (Line, error) {
	if r.pool == nil {
		return Line{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Line{}, fmt.Errorf("update line %q: begin: %w", lineID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM procurement_projects WHERE id = $2 AND customer_id = $1 FOR UPDATE`,
		customerID, projectID).Scan(&status)
	if err != nil {
		if notFound(err) {
			return Line{}, ErrNotFound
		}
		return Line{}, fmt.Errorf("update line %q: load project: %w", lineID, err)
	}
	if status != StatusPlanning {
		return Line{}, ErrInvalidTransition
	}

	// When qty or uom changes, the stored indicative_*_cents become stale, so CLEAR them to
	// NULL (priced is derived as "unit cents IS NOT NULL", so it flips back to false). The
	// line is repriced at the next submit-rfq. A notes-only edit leaves the prices intact
	// (review #3).
	q := `UPDATE procurement_project_lines
		SET qty_entered = COALESCE($4::numeric, qty_entered),
			uom = COALESCE($5, uom),
			notes = COALESCE($6, notes),
			indicative_unit_price_cents = CASE WHEN $4::numeric IS NOT NULL OR $5 IS NOT NULL
				THEN NULL ELSE indicative_unit_price_cents END,
			indicative_line_total_cents = CASE WHEN $4::numeric IS NOT NULL OR $5 IS NOT NULL
				THEN NULL ELSE indicative_line_total_cents END
		WHERE id = $3 AND project_id = $2
			AND project_id IN (SELECT id FROM procurement_projects WHERE id = $2 AND customer_id = $1)
		RETURNING ` + lineReturning
	ln, err := scanLine(tx.QueryRow(ctx, q, customerID, projectID, lineID, in.QtyEntered, in.UOM, in.Notes))
	if err != nil {
		if notFound(err) {
			return Line{}, ErrNotFound
		}
		return Line{}, fmt.Errorf("update line %q: %w", lineID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Line{}, fmt.Errorf("update line %q: commit: %w", lineID, err)
	}
	return ln, nil
}

// RemoveLine deletes one line from a tenant project (line+project+tenant scoped). The project
// row is locked FOR UPDATE first so the PLANNING freeze is enforced atomically: a non-PLANNING
// status is ErrInvalidTransition (409), beating a racing SubmitRFQ (reviews #2 + #3 + #6).
func (r *Repository) RemoveLine(ctx context.Context, customerID, projectID, lineID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("remove line %q: begin: %w", lineID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM procurement_projects WHERE id = $2 AND customer_id = $1 FOR UPDATE`,
		customerID, projectID).Scan(&status)
	if err != nil {
		if notFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("remove line %q: load project: %w", lineID, err)
	}
	if status != StatusPlanning {
		return ErrInvalidTransition
	}

	var deleted string
	err = tx.QueryRow(ctx,
		`DELETE FROM procurement_project_lines
		 WHERE id = $3 AND project_id = $2
			AND project_id IN (SELECT id FROM procurement_projects WHERE id = $2 AND customer_id = $1)
		 RETURNING id::text`,
		customerID, projectID, lineID).Scan(&deleted)
	if err != nil {
		if notFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("remove line %q: %w", lineID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("remove line %q: commit: %w", lineID, err)
	}
	return nil
}

// SubmitRFQ persists the service-computed indicative prices and transitions the tenant project
// PLANNING -> QUOTING in one tx, then returns the refreshed detail. The project is locked and
// re-checked (defense in depth: a wrong source state -> ErrInvalidTransition, missing -> 404).
func (r *Repository) SubmitRFQ(ctx context.Context, customerID, projectID string, priced []PricedLine) (ProjectDetail, error) {
	if r.pool == nil {
		return ProjectDetail{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ProjectDetail{}, fmt.Errorf("submit rfq %q: begin: %w", projectID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	err = tx.QueryRow(ctx,
		`SELECT status FROM procurement_projects WHERE id = $2 AND customer_id = $1 FOR UPDATE`,
		customerID, projectID).Scan(&current)
	if err != nil {
		if notFound(err) {
			return ProjectDetail{}, ErrNotFound
		}
		return ProjectDetail{}, fmt.Errorf("submit rfq %q: load: %w", projectID, err)
	}
	if !canTransition(current, StatusQuoting) {
		return ProjectDetail{}, ErrInvalidTransition
	}

	for _, pl := range priced {
		// An UNPRICED line (best-effort pricing left it unanchored) is persisted as NULL
		// cents, not 0, so the derived priced flag reads false on read-back (review #1).
		var unitCents, lineTotalCents any
		if pl.Priced {
			unitCents, lineTotalCents = pl.IndicativeUnitPriceCents, pl.IndicativeLineTotalCents
		}
		if _, err := tx.Exec(ctx,
			`UPDATE procurement_project_lines
			 SET indicative_unit_price_cents = $1, indicative_line_total_cents = $2
			 WHERE id = $3 AND project_id = $4`,
			unitCents, lineTotalCents, pl.LineID, projectID); err != nil {
			return ProjectDetail{}, fmt.Errorf("submit rfq %q: price line %q: %w", projectID, pl.LineID, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE procurement_projects SET status = 'QUOTING', updated_at = now()
		 WHERE id = $2 AND customer_id = $1`,
		customerID, projectID); err != nil {
		return ProjectDetail{}, fmt.Errorf("submit rfq %q: transition: %w", projectID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectDetail{}, fmt.Errorf("submit rfq %q: commit: %w", projectID, err)
	}
	return r.GetProject(ctx, customerID, projectID)
}
