package reporting

// repository.go, this module's own read/write layer. Two kinds of statement live here and they are
// kept visibly apart:
//
//	OWN TABLES     report_templates, report_runs, report_saved_views, commission_*, read and written.
//	PEER TABLES    invoices, stock_moves, purchasing_* …, READ ONLY, and only through catalog.go's
//	               fixed queries. There is no INSERT, UPDATE or DELETE against a peer table anywhere in
//	               this package, and there never should be: a reporting module that writes is a
//	               reporting module that can be blamed for a number changing.
//
// The one apparent exception is print_queue, which this module INSERTs into. That is not a peer write
// in the sense that matters, 0088 built print_queue as the shared substrate every printed document
// goes through, and 0105 established that a new document type joins it by widening the CHECK (which
// 0143 did) rather than by growing a private print path. billing writes statements into it the same way.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is reporting's persistence layer. A nil pool is tolerated (scaffold/offline mode) and
// every method answers ErrNoDatabase rather than panicking.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// -- templates ----------------------------------------------------------------

// ListTemplates reads every ACTIVE definition and joins it to its compiled catalog entry. A definition
// with no catalog entry is SKIPPED rather than returned broken: it cannot be run, so listing it would
// put a report in the nav that 404s when clicked.
func (r *Repository) ListTemplates(ctx context.Context) ([]Template, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx, `
SELECT code, name, category, description, legacy_name, sort_order, is_active, param_schema
  FROM report_templates
 WHERE is_active
 ORDER BY category, sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("list report templates: %w", err)
	}
	defer rows.Close()

	var out []Template
	for rows.Next() {
		var t Template
		var schema []byte
		if err := rows.Scan(&t.Code, &t.Name, &t.Category, &t.Description, &t.LegacyName,
			&t.SortOrder, &t.IsActive, &schema); err != nil {
			return nil, fmt.Errorf("scan report template: %w", err)
		}
		if err := json.Unmarshal(schema, &t.Params); err != nil {
			return nil, fmt.Errorf("report template %s: param_schema: %w", t.Code, err)
		}
		entry, ok := catalog[t.Code]
		if !ok {
			continue // definition with no compiled query, invisible by design
		}
		t.sql = entry.sql
		t.Columns = entry.columns
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTemplate reads ONE definition by code and binds its compiled query. A code with no catalog entry
// is ErrNotFound, the same answer as a code that does not exist at all, because from a caller's point
// of view there is no difference: neither can be run.
func (r *Repository) GetTemplate(ctx context.Context, code string) (Template, error) {
	if r.pool == nil {
		return Template{}, ErrNoDatabase
	}
	var t Template
	var schema []byte
	err := r.pool.QueryRow(ctx, `
SELECT code, name, category, description, legacy_name, sort_order, is_active, param_schema
  FROM report_templates
 WHERE code = $1 AND is_active`, code).
		Scan(&t.Code, &t.Name, &t.Category, &t.Description, &t.LegacyName, &t.SortOrder, &t.IsActive, &schema)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("get report template %s: %w", code, err)
	}
	if err := json.Unmarshal(schema, &t.Params); err != nil {
		return Template{}, fmt.Errorf("report template %s: param_schema: %w", code, err)
	}
	entry, ok := catalog[code]
	if !ok {
		return Template{}, ErrNotFound
	}
	t.sql = entry.sql
	t.Columns = entry.columns
	return t, nil
}

// -- runs (the printed artefact) ----------------------------------------------

// InsertRunWithPrintJob writes the artefact AND its print_queue row in ONE transaction, then links
// them. Either both exist or neither does: an artefact with no print job is a report nobody asked for,
// and a print job pointing at nothing is a printer that fails at 6am with a dangling uuid.
//
// The order matters. The run row is inserted first so print_queue.ref_id has something to point at
// (0088's doc_type-decides-what-ref_id-means posture), then the job, then the back-link.
func (r *Repository) InsertRunWithPrintJob(ctx context.Context, run Run) (Run, error) {
	if r.pool == nil {
		return Run{}, ErrNoDatabase
	}
	params, err := json.Marshal(run.Params)
	if err != nil {
		return Run{}, fmt.Errorf("marshal run params: %w", err)
	}
	totals, err := json.Marshal(run.Totals)
	if err != nil {
		return Run{}, fmt.Errorf("marshal run totals: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("begin report run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out Run
	out = run
	if err := tx.QueryRow(ctx, `
INSERT INTO report_runs (kind, template_code, params, branch_id, row_count, totals, html, run_by)
VALUES ($1, $2, $3::jsonb, NULLIF($4,'')::uuid, $5, $6::jsonb, $7, NULLIF($8,'')::uuid)
RETURNING id, created_at`,
		run.Kind, run.TemplateCode, string(params), run.BranchID, run.RowCount,
		string(totals), run.HTML, run.RunBy).Scan(&out.ID, &out.CreatedAt); err != nil {
		return Run{}, fmt.Errorf("insert report run: %w", err)
	}

	// A print job needs a branch. print_queue.branch_id is NOT NULL with an FK to locations, which is
	// right for a physical document: paper comes out of a printer that lives somewhere. A consolidated
	// (all-branch) report therefore cannot be queued without the caller naming where to print it, the
	// service resolves that from the caller's own branch before reaching here.
	if run.BranchID == "" {
		return Run{}, fmt.Errorf("printing a report needs a branch to print it at")
	}
	var jobID string
	if err := tx.QueryRow(ctx, `
INSERT INTO print_queue (doc_type, ref_id, branch_id, render_target, status, requested_by)
VALUES ($1, $2::uuid, $3::uuid, $4, 'QUEUED', NULLIF($5,'')::uuid)
RETURNING id`,
		PrintDocType, out.ID, run.BranchID, PrintRenderTarget, run.RunBy).Scan(&jobID); err != nil {
		return Run{}, fmt.Errorf("enqueue report print job: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE report_runs SET print_job_id = $1::uuid WHERE id = $2::uuid`,
		jobID, out.ID); err != nil {
		return Run{}, fmt.Errorf("link report print job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit report run: %w", err)
	}
	out.PrintJobID = jobID
	return out, nil
}

// GetRun reads one artefact, HTML included.
func (r *Repository) GetRun(ctx context.Context, id string) (Run, error) {
	if r.pool == nil {
		return Run{}, ErrNoDatabase
	}
	var run Run
	var params, totals []byte
	var branchID, printJobID, runBy *string
	err := r.pool.QueryRow(ctx, `
SELECT id, kind, template_code, params, branch_id::text, row_count, totals, html,
       print_job_id::text, run_by::text, created_at
  FROM report_runs WHERE id = $1::uuid`, id).
		Scan(&run.ID, &run.Kind, &run.TemplateCode, &params, &branchID, &run.RowCount, &totals,
			&run.HTML, &printJobID, &runBy, &run.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get report run: %w", err)
	}
	run.Params = jsonMap(params)
	run.Totals = jsonAnyMap(totals)
	run.BranchID = deref(branchID)
	run.PrintJobID = deref(printJobID)
	run.RunBy = deref(runBy)
	return run, nil
}

// ListRuns reads the most recent artefacts (HTML omitted, a list of documents, not the documents),
// fenced to the caller's branch reach BY THE DATA THE ARTEFACTS CARRY, not by r.branch_id, which is
// only the print branch (paper comes out of a printer that lives somewhere, so even a consolidated
// report is printed AT a branch):
//
//	STAMPED     params.branch_id is the artefact's own claim of what it holds: the report's own
//	            branch parameter for a TEMPLATE row, the period's branch for a COMMISSION row
//	            printed since the stamp exists. The comparison is TEXT against the granted branch
//	            ids, the exact equality the single read's map lookup performs, so a malformed value
//	            simply matches nothing instead of erroring the whole list, and an absent or empty
//	            value (consolidated) matches no grant either.
//	UNSTAMPED   a COMMISSION row printed before the stamp exists carries only the (rep, window)
//	            linkage, no period id, so the predicate asks the frozen periods that linkage
//	            matches: at least one must sit inside the reach and none may sit outside it (a
//	            consolidated match, branch NULL, is outside every reach). No match at all fails the
//	            EXISTS and the artefact reads as consolidated: global only, fail closed. The
//	            linkage is compared with the COLUMNS rendered to text (to_char for the dates, so
//	            the comparison does not depend on the session DateStyle) rather than the params
//	            values cast to uuid and date, so a malformed row simply matches nothing instead of
//	            erroring the whole list, the same safe skip the single read performs.
//
// A NULL parameter is the global caller's whole list; an empty array answers no rows. This is the
// same ruling artefactScopes and artefactReadable make for the single read, stated in SQL.
func (r *Repository) ListRuns(ctx context.Context, limit int, reach branchReach) ([]Run, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
SELECT r.id, r.kind, r.template_code, COALESCE(t.name, ''), r.params, r.branch_id::text,
       r.row_count, r.totals, r.print_job_id::text, r.run_by::text, r.created_at
  FROM report_runs r
  LEFT JOIN report_templates t ON t.code = r.template_code
 WHERE ($2::text[] IS NULL
    OR (r.params->>'branch_id' = ANY($2::text[]))
    OR (r.kind = 'COMMISSION'
        AND r.params->>'branch_id' IS NULL
        AND EXISTS (SELECT 1
                      FROM commission_runs cr
                     WHERE cr.rep_user_id::text = NULLIF(r.params->>'rep_user_id', '')
                       AND to_char(cr.period_start, 'YYYY-MM-DD') = r.params->>'period_start'
                       AND to_char(cr.period_end, 'YYYY-MM-DD') = r.params->>'period_end'
                       AND cr.branch_id::text = ANY($2::text[]))
        AND NOT EXISTS (SELECT 1
                          FROM commission_runs cr
                         WHERE cr.rep_user_id::text = NULLIF(r.params->>'rep_user_id', '')
                           AND to_char(cr.period_start, 'YYYY-MM-DD') = r.params->>'period_start'
                           AND to_char(cr.period_end, 'YYYY-MM-DD') = r.params->>'period_end'
                           AND (cr.branch_id IS NULL OR NOT (cr.branch_id::text = ANY($2::text[]))))))
 ORDER BY r.created_at DESC
 LIMIT $1`, limit, reach.filter())
	if err != nil {
		return nil, fmt.Errorf("list report runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var run Run
		var params, totals []byte
		var branchID, printJobID, runBy *string
		if err := rows.Scan(&run.ID, &run.Kind, &run.TemplateCode, &run.Name, &params, &branchID,
			&run.RowCount, &totals, &printJobID, &runBy, &run.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan report run: %w", err)
		}
		run.Params = jsonMap(params)
		run.Totals = jsonAnyMap(totals)
		run.BranchID = deref(branchID)
		run.PrintJobID = deref(printJobID)
		run.RunBy = deref(runBy)
		out = append(out, run)
	}
	return out, rows.Err()
}

// -- saved views --------------------------------------------------------------

// ListSavedViews returns the caller's own views plus everyone's shared ones for a template.
func (r *Repository) ListSavedViews(ctx context.Context, ownerUserID, templateCode string) ([]SavedView, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx, `
SELECT id, template_code, owner_user_id::text, name, params, is_shared, created_at
  FROM report_saved_views
 WHERE (owner_user_id = $1::uuid OR is_shared)
   AND ($2::text = '' OR template_code = $2::text)
 ORDER BY template_code, name`, ownerUserID, templateCode)
	if err != nil {
		return nil, fmt.Errorf("list saved views: %w", err)
	}
	defer rows.Close()
	var out []SavedView
	for rows.Next() {
		var v SavedView
		var params []byte
		if err := rows.Scan(&v.ID, &v.TemplateCode, &v.OwnerUserID, &v.Name, &params, &v.IsShared, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan saved view: %w", err)
		}
		v.Params = jsonMap(params)
		out = append(out, v)
	}
	return out, rows.Err()
}

// UpsertSavedView saves (or re-saves) a named parameter set. Saving twice under one name UPDATES -
// the unique index in 0145 makes that the only possible outcome, and it is the one a user expects.
func (r *Repository) UpsertSavedView(ctx context.Context, v SavedView) (SavedView, error) {
	if r.pool == nil {
		return SavedView{}, ErrNoDatabase
	}
	params, err := json.Marshal(v.Params)
	if err != nil {
		return SavedView{}, fmt.Errorf("marshal saved view params: %w", err)
	}
	var out SavedView = v
	if err := r.pool.QueryRow(ctx, `
INSERT INTO report_saved_views (template_code, owner_user_id, name, params, is_shared)
VALUES ($1, $2::uuid, $3, $4::jsonb, $5)
ON CONFLICT (owner_user_id, template_code, name)
DO UPDATE SET params = EXCLUDED.params, is_shared = EXCLUDED.is_shared, updated_at = now()
RETURNING id, created_at`,
		v.TemplateCode, v.OwnerUserID, v.Name, string(params), v.IsShared).
		Scan(&out.ID, &out.CreatedAt); err != nil {
		return SavedView{}, fmt.Errorf("save view: %w", err)
	}
	return out, nil
}

// DeleteSavedView removes a view the caller OWNS. A shared view is readable by everyone and deletable
// by its owner alone, sharing is a grant of sight, not of control.
func (r *Repository) DeleteSavedView(ctx context.Context, id, ownerUserID string) error {
	if r.pool == nil {
		return ErrNoDatabase
	}
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM report_saved_views WHERE id = $1::uuid AND owner_user_id = $2::uuid`, id, ownerUserID)
	if err != nil {
		return fmt.Errorf("delete saved view: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// -- small helpers ------------------------------------------------------------

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nowUTC is the module clock, swapped in tests.
var nowUTC = func() time.Time { return time.Now().UTC() }
